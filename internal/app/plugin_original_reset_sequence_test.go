package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"light-media/internal/plugins/builtin"
)

// Real HTTP/store/SDK assertion. This does not pretend to trace the separate
// reset broadcast handlers, their asynchronous ordering, or failure effects.
func TestPluginOriginalResetClearsTargetAndReloadsOnce(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	installCloneSource(t, a.pluginStore, builtin.SystemProcessManifest())
	source := "SystemProcess"
	if _, err := a.pluginStore.SaveConfig(ctx, source, 0, map[string]any{"enable": true, "owned": "source"}); err != nil {
		t.Fatal(err)
	}
	target, err := a.pluginStore.Clone(ctx, source, PluginCloneRequest{Suffix: "ResetTarget"})
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := a.pluginStore.Clone(ctx, source, PluginCloneRequest{Suffix: "ResetSibling"})
	if err != nil {
		t.Fatal(err)
	}
	targetConfig, err := a.pluginStore.Config(ctx, target.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.SaveConfig(ctx, target.InstanceID, targetConfig.Revision, map[string]any{"enable": true, "owned": "clear-target"}); err != nil {
		t.Fatal(err)
	}
	ids := []string{source, target.InstanceID, sibling.InstanceID}
	objects := map[string]*originalPluginInstance{}
	configs := map[string]PluginStoredValue{}
	values := map[string]PluginStoredValue{}
	runs := map[string]int{}
	for _, id := range ids {
		if _, err := a.pluginStore.SaveData(ctx, id, "owned-history", 0, []string{id}); err != nil {
			t.Fatal(err)
		}
		object := a.startOriginalPlugin(ctx, id)
		if object == nil {
			t.Fatalf("real start %s failed", id)
		}
		objects[id] = object
		config, err := a.pluginStore.Config(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		configs[id] = config
		value, err := a.pluginStore.Data(ctx, id, "owned-history")
		if err != nil {
			t.Fatal(err)
		}
		values[id] = value
		var n int
		if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id=?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		runs[id] = n
	}
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+target.InstanceID+"/reset", `{}`, cookie, "")
	var body struct {
		Data struct {
			Success bool `json:"success"`
		} `json:"data"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || !body.Data.Success {
		t.Fatalf("original reset response: %d %s", response.Code, response.Body.String())
	}
	a.wg.Wait()
	if _, err = a.pluginStore.Config(ctx, target.InstanceID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("target config was not deleted: %v", err)
	}
	if _, err = a.pluginStore.Data(ctx, target.InstanceID, "owned-history"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("target KV was not deleted: %v", err)
	}
	loaded, ok := a.pluginRunningInstances.Load(target.InstanceID)
	if !ok || loaded == objects[target.InstanceID] {
		t.Fatal("reset did not replace loaded target object")
	}
	if state, err := loaded.(*originalPluginInstance).getState(ctx); err != nil || state {
		t.Fatalf("reset default state=%v err=%v", state, err)
	}
	var total, success int
	if err = a.Store.DB.QueryRow(`SELECT count(*),COALESCE(SUM(state='succeeded'),0) FROM plugin_runs WHERE instance_id=?`, target.InstanceID).Scan(&total, &success); err != nil {
		t.Fatal(err)
	}
	if total != runs[target.InstanceID]+1 || success != total {
		t.Fatalf("reset must perform exactly one successful actual init: before=%d total=%d success=%d", runs[target.InstanceID], total, success)
	}
	for _, id := range []string{source, sibling.InstanceID} {
		loaded, ok := a.pluginRunningInstances.Load(id)
		if !ok || loaded != objects[id] {
			t.Fatalf("reset replaced unrelated object %s", id)
		}
		cfg, err := a.pluginStore.Config(ctx, id)
		if err != nil || cfg.Revision != configs[id].Revision || !bytes.Equal(cfg.Value, configs[id].Value) {
			t.Fatalf("unrelated config changed %s: %v", id, err)
		}
		value, err := a.pluginStore.Data(ctx, id, "owned-history")
		if err != nil || value.Revision != values[id].Revision || !bytes.Equal(value.Value, values[id].Value) {
			t.Fatalf("unrelated KV changed %s: %v", id, err)
		}
		var n int
		if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id=?`, id).Scan(&n); err != nil || n != runs[id] {
			t.Fatalf("unrelated init %s: %d err=%v", id, n, err)
		}
	}
}
