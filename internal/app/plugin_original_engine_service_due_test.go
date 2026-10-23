package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"light-media/internal/plugins"
	signin "light-media/internal/plugins/autosignin"
	seed "light-media/internal/plugins/cleaninvalidseed"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

// Future common-origin regression, not claimed passing. Wait for a real due
// instant rather than forging next_due, a private plan, receipt, or grace.
func originalEngineActualDue(t *testing.T, manifest plugins.Manifest) {
	t.Helper()
	t.Setenv("TZ", "UTC")
	ctx := context.Background()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	settings := a.Settings()
	settings.Sites = nil
	settings.Downloaders = nil
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	installCloneSource(t, a.pluginStore, manifest)
	originalEngineAppSave(t, a, manifest.PluginID, map[string]any{"enabled": true, "onlyonce": false, "notify": false, "cron": "* * * * *", "sign_sites": []any{}, "login_sites": []any{}, "downloaders": []any{}, "download_dirs": "", "min_seeding_days": 0})
	p := a.startOriginalPlugin(ctx, manifest.PluginID)
	if p == nil {
		t.Fatal("actual start failed")
	}
	first := originalEngineAppPointer(t, p)
	a.updateOriginalPluginJobs(ctx, manifest.PluginID)
	var dueText string
	if err := a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE instance_id=? AND service_id=? AND group_key='original_services' AND active=1`, manifest.PluginID, manifest.PluginID).Scan(&dueText); err != nil {
		t.Fatal(err)
	}
	due, err := time.Parse(time.RFC3339Nano, dueText)
	if err != nil {
		t.Fatal(err)
	}
	delay := time.Until(due)
	if delay < 0 || delay > time.Minute+time.Second {
		t.Fatalf("unexpected real cron due %s", dueText)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	<-timer.C
	if queued, err := a.pluginStore.enqueueDuePluginSchedules(ctx, time.Now()); err != nil || queued != 1 {
		t.Fatalf("original service enqueue %d %v", queued, err)
	}
	run, err := a.pluginStore.claimRunForInstance(ctx, manifest.PluginID)
	if err != nil || run.Action != "service" {
		t.Fatalf("actual service claim %+v %v", run, err)
	}
	a.executePluginRun(ctx, run)
	final, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var occurrenceState string
	if err := a.Store.DB.QueryRow(`SELECT state FROM plugin_schedule_occurrences WHERE run_id=? AND instance_id=? AND service_id=?`, run.ID, manifest.PluginID, manifest.PluginID).Scan(&occurrenceState); err != nil {
		t.Fatal(err)
	}
	if originalEngineAppPointer(t, p) != first {
		t.Fatal("service used a replacement source object")
	}
	if manifest.PluginID == "AutoSignIn" {
		if final.State != "succeeded" || occurrenceState != "succeeded" {
			t.Fatalf("actual service failed %+v occurrence=%s", final, occurrenceState)
		}
		state := autoSignInSDKState(t, a, manifest.PluginID)
		actual := state.Runs[run.ID]
		if actual == nil || actual.Action != "service" || actual.Phase != "completed" || len(actual.Lanes["main-clock"]) == 0 {
			t.Fatal("no durable evidence of actual source SignIn clock")
		}
	} else {
		if final.State != "failed" || final.ErrorCode != "CLEAN_SEED_SOURCE_FAILED" || occurrenceState != "failed" {
			t.Fatalf("expected original no-downloader exception %+v occurrence=%s", final, occurrenceState)
		}
		var result seedhost.Result
		if err := json.Unmarshal(final.Result, &result); err != nil || result.SourceError != "AttributeError" {
			t.Fatalf("actual source error missing %+v %v", result, err)
		}
	}
	var initCount, serviceCount, otherCount int
	if err := a.Store.DB.QueryRow(`SELECT sum(CASE WHEN action='init' THEN 1 ELSE 0 END),sum(CASE WHEN action='service' THEN 1 ELSE 0 END),sum(CASE WHEN action NOT IN ('init','service') THEN 1 ELSE 0 END) FROM plugin_runs WHERE instance_id=?`, manifest.PluginID).Scan(&initCount, &serviceCount, &otherCount); err != nil {
		t.Fatal(err)
	}
	if initCount != 1 || serviceCount != 1 || otherCount != 0 {
		t.Fatalf("unexpected source entries init=%d service=%d other=%d", initCount, serviceCount, otherCount)
	}
}
func TestOriginalEngineActualDueSignInNoSites(t *testing.T) {
	originalEngineActualDue(t, signin.Manifest())
}
func TestOriginalEngineActualDueCleanNoDownloaders(t *testing.T) {
	originalEngineActualDue(t, seed.Manifest())
}
