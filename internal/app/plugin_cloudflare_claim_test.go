package app

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	cf "light-media/internal/plugins/cloudflarespeedtest"
)

func TestPluginCloudflareClaimRejectsExpiredAndSQLFailureBeforeRunning(t *testing.T) {
	for _, action := range []string{"once", "service"} {
		for _, failure := range []string{"expired", "sql"} {
			t.Run(action+"/"+failure, func(t *testing.T) {
				a := newTestApp(t)
				ctx := context.Background()
				cookie, _ := cloudflareTestInstall(t, a)
				a.pluginCloudflare.now = func() time.Time { return time.Now().Add(-2 * time.Hour) }
				config := cloudflareTestConfig("-h")
				config["onlyonce"] = action == "once"
				config["cron"] = ""
				if action == "service" {
					config["cron"] = "* * * * *"
				}
				cloudflareTestConfigure(t, a, cookie, config)
				var due string
				if err := a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE instance_id=? AND active=1`, cf.PluginID).Scan(&due); err != nil {
					t.Fatal(err)
				}
				at, err := time.Parse(time.RFC3339Nano, due)
				if err != nil {
					t.Fatal(err)
				}
				if count, err := a.pluginStore.enqueueDuePluginSchedules(ctx, at); err != nil || count != 1 {
					t.Fatal("owned occurrence not queued", count, err)
				}
				run, err := a.pluginStore.scanPluginRun(a.Store.DB.QueryRow(`SELECT `+pluginRunColumns+` FROM plugin_runs WHERE instance_id=? AND action=? AND state='pending'`, cf.PluginID, action))
				if err != nil {
					t.Fatal(err)
				}
				if failure == "sql" {
					if _, err = a.Store.DB.Exec(`ALTER TABLE plugin_schedules RENAME TO owned_missing_schedules`); err != nil {
						t.Fatal(err)
					}
					defer func() {
						if _, err := a.Store.DB.Exec(`ALTER TABLE owned_missing_schedules RENAME TO plugin_schedules`); err != nil {
							t.Error(err)
						}
					}()
				}
				claimed, err := a.pluginStore.claimRunForInstance(ctx, cf.PluginID)
				actual, readErr := a.pluginStore.Run(ctx, run.ID)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if failure == "sql" {
					if err == nil || errors.Is(err, sql.ErrNoRows) || actual.State != "pending" {
						t.Fatal("claim SQL failure did not roll back", claimed.State, actual.State, err)
					}
				} else if !errors.Is(err, sql.ErrNoRows) || actual.State != "canceled" || actual.ErrorCode != "PLUGIN_SCHEDULE_MISFIRE" {
					t.Fatal("expired source task entered running", claimed.State, actual.State, actual.ErrorCode, err)
				}
				if _, _, err := a.pluginStore.readDeferred(ctx, a.Store.DB, run); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("rejected claim created a native execution receipt", err)
				}
			})
		}
	}
}
