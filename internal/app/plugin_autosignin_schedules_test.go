package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/autosignin"
	durable "light-media/internal/plugins/autosignin/durable"
)

func TestAutoSignInScheduleIntervalFractionNeverTruncates(t *testing.T) {
	for _, tc := range []struct {
		hours  string
		ms     int64
		reject bool
	}{{"0.5", 1800000, false}, {"0", 0, false}, {"0.0000001", 0, true}, {"-1", 0, true}, {`{"nonfinite":"nan"}`, 0, true}} {
		raw := json.RawMessage(`{"id":"AutoSignIn","name":"站点自动签到服务","trigger":"interval","func":{"callable":"sign_in"},"kwargs":{"hours":` + tc.hours + `}}`)
		d, err := autoSignInServiceDefinition(raw, "UTC")
		if tc.reject {
			if err == nil {
				t.Fatalf("accepted unsupported hours %s", tc.hours)
			}
			continue
		}
		if err != nil || d.Trigger.IntervalMS != tc.ms {
			t.Fatalf("fraction %s: %+v %v", tc.hours, d, err)
		}
	}
}

func signinScheduleProjectFixture(t *testing.T, f *signinSQLFixture) {
	t.Helper()
	ctx := context.Background()
	tx, err := f.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	loaded, err := f.o.readTx(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.b.projectSchedulesTx(ctx, tx, map[string]pluginAutoSignInCell{}, loaded.State); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestAutoSignInScheduleRealOnceAndOwnerRevisionChain(t *testing.T) {
	ctx := context.Background()
	f := signinSQLSetup(t, map[string]any{"enabled": false, "onlyonce": true, "sign_sites": []any{}, "login_sites": []any{}})
	stamp := time.Date(2026, 10, 21, 12, 30, 0, 0, time.UTC)
	c := durable.Controller{Journal: f.j, Binding: f.o.binding, DigestKey: [32]byte{1}, Dependencies: durable.Dependencies{Timezone: "UTC", Now: func() time.Time { return stamp }, Inventory: func(context.Context) (autosignin.Inventory, error) { return autosignin.Inventory{}, nil }}}
	if _, err := c.Execute(ctx, f.run.ID, durable.Command{Action: "cold_init"}); err != nil {
		t.Fatal(err)
	}
	signinScheduleProjectFixture(t, f)
	var count int
	var due string
	var revision int64
	if err := f.a.Store.DB.QueryRow(`SELECT COUNT(*),next_due,config_revision FROM plugin_schedules WHERE instance_id='AutoSignIn' AND active=1`).Scan(&count, &due, &revision); err != nil {
		t.Fatal(err)
	}
	expected := stamp.Add(3 * time.Second).Format("2006-01-02T15:04:05.000000000Z")
	if count != 1 || due != expected || revision != f.run.ConfigRevision {
		t.Fatal("once lost source date/admission", count, due, revision)
	}
	cfg, err := f.a.pluginStore.Config(ctx, "AutoSignIn")
	if err != nil || cfg.Revision <= revision {
		t.Fatal("source reset did not advance public config", cfg.Revision, err)
	}
	tx, err := f.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	handled, err := f.a.pluginStore.autoSignInScheduleAdmissionTx(ctx, tx, "AutoSignIn", f.run.Generation, revision)
	tx.Rollback()
	if !handled || err != nil {
		t.Fatal("legitimate self-update rejected", handled, err)
	}
	if _, err = f.a.Store.DB.Exec(`UPDATE plugin_schedules SET next_due=NULL WHERE instance_id='AutoSignIn'`); err != nil {
		t.Fatal(err)
	}
	signinScheduleProjectFixture(t, f)
	var remains int
	if err = f.a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id='AutoSignIn' AND next_due IS NOT NULL`).Scan(&remains); err != nil || remains != 0 {
		t.Fatal("consumed once was replayed", remains, err)
	}
	if _, err = f.a.Store.DB.Exec(`UPDATE plugin_installations SET desired_enabled=0 WHERE instance_id='AutoSignIn'`); err != nil {
		t.Fatal(err)
	}
	tx, err = f.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.a.pluginStore.autoSignInScheduleAdmissionTx(ctx, tx, "AutoSignIn", f.run.Generation, revision)
	tx.Rollback()
	if err == nil {
		t.Fatal("onlyonce bypassed host revocation")
	}
}

func TestAutoSignInScheduleRandomSourceChoicesPersist(t *testing.T) {
	f := signinSQLSetup(t, map[string]any{"enabled": true, "onlyonce": false, "cron": "", "sign_sites": []any{}, "login_sites": []any{}})
	calls := 0
	c := durable.Controller{Journal: f.j, Binding: f.o.binding, DigestKey: [32]byte{2}, Dependencies: durable.Dependencies{Timezone: "UTC", Now: func() time.Time { return time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC) }, RandomInt: func(int, int) (int, error) { calls++; return 120, nil }, Inventory: func(context.Context) (autosignin.Inventory, error) { return autosignin.Inventory{}, nil }}}
	if _, err := c.Execute(context.Background(), f.run.ID, durable.Command{Action: "cold_init"}); err != nil {
		t.Fatal(err)
	}
	signinScheduleProjectFixture(t, f)
	var count int
	if err := f.a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id='AutoSignIn' AND active=1 AND service_id IN ('AutoSignIn|11:0','AutoSignIn|13:0')`).Scan(&count); err != nil || count != 2 {
		t.Fatal("random cron not projected", count, err)
	}
	if _, err := f.a.Store.DB.Exec(`UPDATE plugin_schedules SET next_due='2027-01-01T00:00:00.000000000Z' WHERE instance_id='AutoSignIn'`); err != nil {
		t.Fatal(err)
	}
	signinScheduleProjectFixture(t, f)
	if calls != 2 {
		t.Fatal("projection rerolled entropy", calls)
	}
	if err := f.a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id='AutoSignIn' AND next_due='2027-01-01T00:00:00.000000000Z'`).Scan(&count); err != nil || count != 2 {
		t.Fatal("restart changed next due", count, err)
	}
}

func TestAutoSignInScheduleActualOccurrenceBindsDifferentEffectiveRevision(t *testing.T) {
	ctx := context.Background()
	f := signinSQLSetup(t, map[string]any{"enabled": false, "onlyonce": true, "sign_sites": []any{}, "login_sites": []any{}})
	stamp := time.Now().UTC().Truncate(time.Second)
	c := durable.Controller{Journal: f.j, Binding: f.o.binding, DigestKey: [32]byte{3}, Dependencies: durable.Dependencies{Timezone: "UTC", Now: func() time.Time { return stamp }, Inventory: func(context.Context) (autosignin.Inventory, error) { return autosignin.Inventory{}, nil }}}
	if _, err := c.Execute(ctx, f.run.ID, durable.Command{Action: "cold_init"}); err != nil {
		t.Fatal(err)
	}
	signinScheduleProjectFixture(t, f)
	if err := f.a.pluginStore.FinishRun(ctx, f.run.ID, "succeeded", "", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	tx, err := f.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	occurrence := "ps_signin_owned_test"
	input, _ := json.Marshal(map[string]string{"occurrence_id": occurrence})
	run, err := f.a.pluginStore.enqueueRunTx(ctx, tx, "AutoSignIn", "service", input)
	if err != nil {
		t.Fatal(err)
	}
	var serviceID string
	var admission int64
	if err = tx.QueryRow(`SELECT service_id,config_revision FROM plugin_schedules WHERE instance_id='AutoSignIn' AND active=1`).Scan(&serviceID, &admission); err != nil {
		t.Fatal(err)
	}
	if admission == run.ConfigRevision {
		t.Fatal("fixture did not exercise self-update")
	}
	if _, err = tx.Exec(`INSERT INTO plugin_schedule_occurrences VALUES(?,?,?,?,?,?,'queued',?,1000,0,?)`, occurrence, run.InstanceID, serviceID, run.Generation, admission, stamp.Add(3*time.Second).Format(time.RFC3339Nano), run.ID, now()); err != nil {
		t.Fatal(err)
	}
	handled, code, err := f.a.pluginStore.claimAutoSignInScheduleTx(ctx, tx, run, stamp.Add(3*time.Second))
	if err != nil || !handled || code != "" {
		t.Fatal("actual occurrence claim failed", handled, code, err)
	}
	if _, err = tx.Exec(`UPDATE plugin_runs SET state='running' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	run.State = "running"
	var inv plugins.Invocation
	if err = json.Unmarshal(run.Input, &inv); err != nil {
		t.Fatal(err)
	}
	command, err := f.a.autoSignInTrustedService(ctx, run, inv)
	if err != nil || command.Action != "service" {
		t.Fatal("bound source service denied", command, err)
	}
	if _, err = f.a.Store.DB.Exec(`UPDATE plugin_schedule_occurrences SET manual=1 WHERE id=?`, occurrence); err != nil {
		t.Fatal(err)
	}
	if _, err = f.a.autoSignInTrustedService(ctx, run, inv); err == nil {
		t.Fatal("manual service bypassed separate authorization")
	}
}
