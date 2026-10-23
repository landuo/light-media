package app

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/uninstall"
)

// The uninstall selection is an execution intent, not a persistent boolean.
// A copied nonempty source selection must not be replayed when its clone loads.
func TestPluginCloneUninstallArrayIntentCannotRemoveOwnedTargets(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	p := a.pluginStore
	target := builtin.SystemProcessManifest()
	sibling := builtin.CategoryEditorManifest()
	installCloneSource(t, p, target)
	installCloneSource(t, p, sibling)
	for _, id := range []string{target.PluginID, sibling.PluginID} {
		if _, err := p.SaveConfig(ctx, id, 0, map[string]any{"enabled": false, "owned_marker": id}); err != nil {
			t.Fatal(err)
		}
		if _, err := p.SaveData(ctx, id, "history", 0, []string{"owned " + id}); err != nil {
			t.Fatal(err)
		}
	}
	m := uninstall.Manifest()
	installCloneSource(t, p, m)
	original, err := p.SaveConfig(ctx, m.PluginID, 0, map[string]any{"plugin_ids": []string{target.PluginID}, "clear_config": true, "clear_data": true, "unknown_selected": []string{sibling.PluginID}, "unknown_flag": false})
	if err != nil {
		t.Fatal(err)
	}
	clone, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "ArrayIntentOwned"})
	if err != nil {
		t.Fatal(err)
	}
	sourceBefore := reloadParityRows(t, a, m.PluginID)
	targetBefore := reloadParityRows(t, a, target.PluginID)
	siblingBefore := reloadParityRows(t, a, sibling.PluginID)
	copied, err := p.Config(ctx, clone.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err = json.Unmarshal(copied.Value, &cfg); err != nil {
		t.Fatal(err)
	}
	ids, ok := cfg["plugin_ids"].([]any)
	if !ok || len(ids) != 0 {
		t.Fatal("copied uninstall selection was not neutralized as empty array")
	}
	if cfg["clear_config"] != true || cfg["clear_data"] != true || cfg["unknown_flag"] != false || !reflect.DeepEqual(cfg["unknown_selected"], []any{sibling.PluginID}) {
		t.Fatal("clone changed non-trigger options/unknown selection")
	}
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+clone.InstanceID+"/reload", `{}`, cookie, "")
	if response.Code != 200 {
		t.Fatalf("real clone reload: %d %s", response.Code, response.Body.String())
	}
	run := reloadParityResponseRun(t, response.Body.Bytes())
	done := reloadParityComplete(t, a, clone.InstanceID, run.ID, "reload")
	cloneAfter, err := p.Config(ctx, clone.InstanceID)
	if err != nil || cloneAfter.Revision != copied.Revision || !bytes.Equal(cloneAfter.Value, copied.Value) {
		t.Fatal("empty-selection reload unexpectedly rewrote clone config", err)
	}
	if !reflect.DeepEqual(targetBefore, reloadParityRows(t, a, target.PluginID)) || !reflect.DeepEqual(siblingBefore, reloadParityRows(t, a, sibling.PluginID)) || !reflect.DeepEqual(sourceBefore, reloadParityRows(t, a, m.PluginID)) {
		t.Fatal("copied array intent mutated source/target/sibling")
	}
	after, err := p.Config(ctx, m.PluginID)
	if err != nil || after.Revision != original.Revision || !bytes.Equal(after.Value, original.Value) {
		t.Fatal("source's original explicit uninstall selection changed", err)
	}
	for _, table := range []string{"plugin_uninstall_operations", "plugin_uninstall_drains", "plugin_deferred_operations"} {
		var count int
		if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE run_id=?`, done.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("unrequested uninstall acquired effect source %s=%d: %v", table, count, err)
		}
	}
}
