package app

import (
	"context"
	"encoding/json"
	"testing"

	"light-media/internal/plugins"
)

// Real SDK worker, real host registry/configuration and an owned SQLite schema
// outage. Fixed-source SystemConfigOper.get reads its already-published cache,
// even while the backing table is unavailable; Manager strips only empty keys.
func TestSharedManagerQueriesIndependentReadOutageCache(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	managerInstall(t, a, cookie, "ALPHA")
	p := managerWorker(t, a, "OwnedAlpha")
	value := json.RawMessage(`{"ordinary":{"list":[3,null,false]},"":"drop","zero":0}`)
	want := json.RawMessage(`{"ordinary":{"list":[3,null,false]},"zero":0}`)
	if err := managerBaseSet(t, p, "OwnedAlpha", value); err != nil {
		t.Fatal(err)
	}
	managerEqual(t, managerQuery(t, p, "config", "OwnedAlpha"), want)
	if _, err := a.Store.DB.Exec(`ALTER TABLE plugin_values RENAME TO owned_unavailable_values`); err != nil {
		t.Fatal(err)
	}
	outage := true
	defer func() {
		if outage {
			if _, err := a.Store.DB.Exec(`ALTER TABLE owned_unavailable_values RENAME TO plugin_values`); err != nil {
				t.Error(err)
			}
		}
	}()
	if _, err := a.Store.DB.Exec(`SELECT * FROM plugin_values`); err == nil {
		t.Fatal("SQLite outage not reached")
	}
	// Installation/generation validation remains intact: only ordinary plugin
	// values are unavailable, never identity or permission tables.
	got, err := managerCall(t, p, "query", map[string]any{"method": "config", "target": "OwnedAlpha"})
	if err != nil {
		t.Fatalf("source keeps warm configuration during SQL read outage; actual worker query failed: %v", err)
	}
	managerEqual(t, got, want)
	managerEqual(t, managerBaseGet(t, p, "OwnedAlpha", nil), value)
	managerEqual(t, managerQuery(t, p, "ids", nil), []string{"OwnedAlpha"})
	// SystemConfigOper.set publishes before its first SQL read too, not only
	// before UPDATE/DELETE. Even this failed producer changes getter output.
	failedSet := json.RawMessage(`{"ordinary":"published-before-select-failure","":"base-only"}`)
	if err := managerBaseSet(t, p, "OwnedAlpha", failedSet); err == nil {
		t.Fatal("config write unexpectedly escaped SQL outage")
	}
	managerEqual(t, managerQuery(t, p, "config", "OwnedAlpha"), json.RawMessage(`{"ordinary":"published-before-select-failure"}`))
	managerEqual(t, managerBaseGet(t, p, "OwnedAlpha", nil), failedSet)
	// Cached data does not waive per-invocation admission.
	callback, revoke := p.originalBaseSessionHost(context.Background(), "query", nil)
	identity := plugins.Identity{PluginID: p.installation.PluginID, InstanceID: p.installation.InstanceID, Version: p.installation.Version}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := callback(canceled, identity, plugins.SharedPluginConfig, json.RawMessage(`{"plugin_id":"OwnedAlpha"}`)); err == nil {
		t.Fatal("outage cache ignored canceled authority")
	}
	revoke()
	if _, err := callback(context.Background(), identity, plugins.SharedPluginConfig, json.RawMessage(`{"plugin_id":"OwnedAlpha"}`)); err == nil {
		t.Fatal("outage cache reused revoked authority")
	}
	if _, err := a.Store.DB.Exec(`ALTER TABLE owned_unavailable_values RENAME TO plugin_values`); err != nil {
		t.Fatal(err)
	}
	outage = false
	fresh := json.RawMessage(`{"ordinary":"post-recovery-native-write"}`)
	if _, err := a.pluginStore.saveOriginalPluginConfiguration(context.Background(), p.installation, fresh); err != nil {
		t.Fatal(err)
	}
	managerEqual(t, managerQuery(t, p, "config", "OwnedAlpha"), fresh)
	t.Logf("warm cache survives actual SQL read failure, admission holds, native recovery refreshes; worker PID %d", managerSnapshot(t, p).PID)
}

func TestSharedManagerQueriesIndependentJSONEdgesAndReadOnly(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	managerInstall(t, a, cookie, "ALPHA")
	p := managerWorker(t, a, "OwnedAlpha")
	before := managerSnapshot(t, p)
	for _, row := range []struct {
		in, want string
		failure  bool
	}{
		{`-0.0`, `{}`, false},
		{`1e-999`, `{}`, false},
		{`"\u0000"`, ``, true},
		{`{"\u0000":0,"":false,"space ":null}`, `{"\u0000":0,"space ":null}`, false},
	} {
		if err := managerBaseSet(t, p, "OwnedAlpha", json.RawMessage(row.in)); err != nil {
			t.Fatal(err)
		}
		var changesBefore, changesAfter int64
		if err := a.Store.DB.QueryRow(`SELECT total_changes()`).Scan(&changesBefore); err != nil {
			t.Fatal(err)
		}
		got, err := managerCall(t, p, "query", map[string]any{"method": "config", "target": "OwnedAlpha"})
		if (err != nil) != row.failure {
			t.Fatalf("source edge %s error=%v", row.in, err)
		}
		if !row.failure {
			managerEqual(t, got, json.RawMessage(row.want))
		}
		managerEqual(t, managerBaseGet(t, p, "OwnedAlpha", nil), json.RawMessage(row.in))
		if err := a.Store.DB.QueryRow(`SELECT total_changes()`).Scan(&changesAfter); err != nil {
			t.Fatal(err)
		}
		if changesAfter != changesBefore {
			t.Fatal("query changed SQLite", changesBefore, changesAfter)
		}
	}
	if after := managerSnapshot(t, p); before.InitCount != after.InitCount || before.Calls["get_state"] != after.Calls["get_state"] {
		t.Fatal("query calls mutated initializer/state")
	}
	// Failed initialization preserves a class and does not add running state;
	// stopping/retrying that failed class changes insertion order only as source.
	if err := managerBaseSet(t, p, "OwnedAlpha", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	managerInstall(t, a, cookie, "BETA")
	managerInstall(t, a, cookie, "BROKEN")
	if err := a.stopOriginalPlugin(context.Background(), "OwnedBroken"); err != nil {
		t.Fatal(err)
	}
	managerEqual(t, managerQuery(t, p, "ids", nil), []string{"OwnedAlpha", "OwnedBeta"})
	if a.startOriginalPlugin(context.Background(), "OwnedBroken") != nil {
		t.Fatal("broken plugin unexpectedly started")
	}
	managerEqual(t, managerQuery(t, p, "ids", nil), []string{"OwnedAlpha", "OwnedBeta", "OwnedBroken"})
	managerEqual(t, managerQuery(t, p, "running", nil), []string{"OwnedAlpha", "OwnedBeta"})
	managerEqual(t, managerQuery(t, p, "config", "OwnedBroken"), map[string]any{})
}

func TestSharedManagerQueriesIndependentFailedClassAndStoppingConfig(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	managerInstall(t, a, cookie, "ALPHA")
	p := managerWorker(t, a, "OwnedAlpha")
	managerInstall(t, a, cookie, "BROKEN")
	brokenValue := json.RawMessage(`{"ordinary":"failed-class","":"drop"}`)
	if err := managerBaseSet(t, p, "OwnedBroken", brokenValue); err != nil {
		t.Fatal(err)
	}
	if _, running := a.pluginRunningInstances.Load("OwnedBroken"); running {
		t.Fatal("failed class is running")
	}
	managerEqual(t, managerQuery(t, p, "config", "OwnedBroken"), json.RawMessage(`{"ordinary":"failed-class"}`))
	if err := a.stopOriginalPlugin(context.Background(), "OwnedBroken"); err != nil {
		t.Fatal(err)
	}
	managerEqual(t, managerQuery(t, p, "config", "OwnedBroken"), map[string]any{})
	managerEqual(t, managerBaseGet(t, p, "OwnedBroken", nil), brokenValue)

	managerInstall(t, a, cookie, "BETA")
	beta := managerWorker(t, a, "OwnedBeta")
	before := managerSnapshot(t, beta)
	stopValue := json.RawMessage(`{"enabled":false,"ordinary":"saved-after-init","":"retained-in-base"}`)
	if err := managerBaseSet(t, p, "OwnedBeta", stopValue); err != nil {
		t.Fatal(err)
	}
	if managerSnapshot(t, beta).InitCount != before.InitCount {
		t.Fatal("saved config initialized worker")
	}
	if err := a.stopOriginalPlugin(context.Background(), "OwnedBeta"); err != nil {
		t.Fatal(err)
	}
	var observed struct {
		Config, Base     json.RawMessage
		Classes, Running []string
	}
	if err := json.Unmarshal(managerBaseGet(t, p, "OwnedBeta", new("stop_observation")), &observed); err != nil {
		t.Fatal(err)
	}
	managerEqual(t, observed.Config, json.RawMessage(`{"enabled":false,"ordinary":"saved-after-init"}`))
	managerEqual(t, observed.Base, stopValue)
	managerEqual(t, mustCleanSeedJSON(observed.Classes), []string{"OwnedAlpha", "OwnedBeta"})
	managerEqual(t, mustCleanSeedJSON(observed.Running), []string{"OwnedAlpha", "OwnedBeta"})
	managerEqual(t, managerQuery(t, p, "config", "OwnedBeta"), map[string]any{})
	managerEqual(t, managerBaseGet(t, p, "OwnedBeta", nil), stopValue)
	t.Logf("actual detached cleanup PID %d read newest config without erasing Base empty key", before.PID)
}

// A loaded class does not imply that the target's lazy per-key cache was warm.
// Source constructs its actual SystemConfigOper before startup and prefetches
// ordinary saved configuration; an outage between two class loads does not lose
// the second class's saved configuration or prevent its initialization.
func TestSharedManagerQueriesIndependentColdUnreadClassOutage(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	managerInstall(t, a, cookie, "ALPHA")
	managerInstall(t, a, cookie, "BETA_READONLY")
	p := managerWorker(t, a, "OwnedAlpha")
	value := json.RawMessage(`{"ordinary":"stored-before-cold","":"retained-in-base"}`)
	if err := managerBaseSet(t, p, "OwnedBeta", value); err != nil {
		t.Fatal(err)
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	markets := []string{}
	cold, err := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if cold.startOriginalPlugin(context.Background(), "OwnedAlpha") == nil {
		t.Fatal("actual alpha cold startup failed")
	}
	alpha := managerWorker(t, cold, "OwnedAlpha")
	if _, err := cold.Store.DB.Exec(`ALTER TABLE plugin_values RENAME TO owned_unavailable_values`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := cold.Store.DB.Exec(`ALTER TABLE owned_unavailable_values RENAME TO plugin_values`); err != nil {
			t.Error(err)
		}
	}()
	beta := cold.startOriginalPlugin(context.Background(), "OwnedBeta")
	got, queryErr := managerCall(t, alpha, "query", map[string]any{"method": "config", "target": "OwnedBeta"})
	if queryErr != nil {
		t.Fatalf("source prefetch makes newly registered class config readable during mid-start SQL outage; actual worker query failed: %v (beta loaded=%v)", queryErr, beta != nil)
	}
	managerEqual(t, got, json.RawMessage(`{"ordinary":"stored-before-cold"}`))
	if beta == nil {
		t.Fatal("source second class initializes from constructor cache; target did not")
	}
	managerEqual(t, managerQuery(t, alpha, "ids", nil), []string{"OwnedAlpha", "OwnedBeta"})
	managerEqual(t, managerQuery(t, alpha, "running", nil), []string{"OwnedAlpha", "OwnedBeta"})
	managerEqual(t, managerBaseGet(t, alpha, "OwnedBeta", nil), value)
}
