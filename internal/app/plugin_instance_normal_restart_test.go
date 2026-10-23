package app

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	ab "light-media/internal/plugins/autobackup"
	bf "light-media/internal/plugins/brushflow"
)

// A completed, authenticated compiled initialization remains usable after a
// normal restart. This does not authorize replay of an unknown interrupted run.
func TestPluginCompletedCompiledInstancesRemainLoadedAfterNormalRestart(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	p := a.pluginStore
	installCloneSource(t, p, bf.NativeManifest())
	config := bf.DefaultConfig()
	config["enabled"] = false
	_, brush, err := p.SaveConfigAndQueue(ctx, bf.PluginID, 0, config)
	if err != nil {
		t.Fatal(err)
	}
	reloadParityComplete(t, a, bf.PluginID, brush.ID, "init")
	installCloneSource(t, p, ab.Manifest())
	_, backup, err := p.SaveConfigAndQueue(ctx, "AutoBackup", 0, map[string]any{"enabled": false, "onlyonce": true, "webdav_enabled": false})
	if err != nil {
		t.Fatal(err)
	}
	reloadParityComplete(t, a, "AutoBackup", backup.ID, "init")
	var scheduled int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id='AutoBackup' AND service_id='AutoBackup.once' AND active=1`).Scan(&scheduled); err != nil || scheduled != 1 {
		t.Fatal("real init did not register one schedule", scheduled, err)
	}
	before := map[string]map[string][]string{}
	for _, id := range []string{bf.PluginID, "AutoBackup"} {
		before[id] = reloadParityRows(t, a, id)
	}
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	useBuiltInPluginProcess(t, cold)
	if err = cold.initializeNativePlugins(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{bf.PluginID, "AutoBackup"} {
		if !reflect.DeepEqual(before[id], reloadParityRows(t, cold, id)) {
			t.Fatal("normal restart changed completed config/run/schedule", id)
		}
		in, err := cold.pluginStore.Installation(ctx, id)
		if err != nil || !in.Installed || !in.DesiredEnabled {
			t.Fatal("completed instance no longer loaded", id, err)
		}
		w := callTest(t, cold.Handler(), "GET", "/api/plugins/"+id+"/form", "", cookie, "")
		if w.Code != 200 {
			t.Fatal("completed Form unavailable", id, w.Code, w.Body.String())
		}
	}
	w := callTest(t, cold.Handler(), "GET", "/api/plugins/"+bf.PluginID+"/api/status", "", cookie, "")
	if w.Code != 200 {
		t.Fatal("completed original remote unavailable", w.Code, w.Body.String())
	}
	if _, err = cold.pluginStore.ClaimRun(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("normal restart replayed completed init", err)
	}
	for _, id := range []string{bf.PluginID, "AutoBackup"} {
		if !reflect.DeepEqual(before[id], reloadParityRows(t, cold, id)) {
			t.Fatal("read after restart mutated config or schedule", id)
		}
	}
	executionLeaseAssertCount(t, cold.pluginStore, 0)
}
