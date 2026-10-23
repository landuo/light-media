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

func TestPluginOriginalCloneRejectsInstalledSourceWithoutClass(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	installCloneSource(t, a.pluginStore, builtin.SystemProcessManifest())
	r := callTest(t, a.Handler(), "POST", "/api/plugins/SystemProcess/clone", `{"suffix":"NoClass"}`, cookie, "")
	var body struct {
		Data struct {
			Success bool `json:"success"`
		} `json:"data"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &body) != nil || body.Data.Success {
		t.Fatalf("original missing class clone result: %d %s", r.Code, r.Body.String())
	}
	a.wg.Wait()
	const target = "SystemProcessnoclass"
	for _, table := range []string{"plugin_installations", "plugin_values", "plugin_runs"} {
		var n int
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE instance_id=?`, target).Scan(&n); err != nil || n != 0 {
			t.Fatalf("failed clone wrote %s n=%d %v", table, n, err)
		}
	}
	if _, ok := a.pluginRunningInstances.Load(target); ok {
		t.Fatal("failed clone created running target")
	}
}

func TestPluginOriginalCloneLoadedUninstalledSourceAndReuseDeletedCode(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	const source = "SystemProcess"
	const target = "SystemProcessreusable"
	installCloneSource(t, a.pluginStore, builtin.SystemProcessManifest())
	if _, err := a.pluginStore.SaveConfig(ctx, source, 0, map[string]any{"enable": false, "owned": "source", "onlyonce": false}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginStore.SaveData(ctx, source, "history", 0, "source-history"); err != nil {
		t.Fatal(err)
	}
	if a.startOriginalPlugin(ctx, source) == nil {
		t.Fatal("source start")
	}
	r := callTest(t, a.Handler(), "DELETE", "/api/plugins/"+source, `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	r = callTest(t, a.Handler(), "POST", "/api/plugins/"+source+"/reload", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	in, err := a.pluginStore.Installation(ctx, source)
	if err != nil || in.Installed {
		t.Fatalf("source must remain uninstalled: %v", err)
	}
	sourceObject, ok := a.pluginRunningInstances.Load(source)
	if !ok {
		t.Fatal("explicit reload did not load source class")
	}
	before, err := a.pluginStore.Config(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	sourceData, err := a.pluginStore.Data(ctx, source, "history")
	if err != nil {
		t.Fatal(err)
	}
	clone := func() {
		t.Helper()
		resp := callTest(t, a.Handler(), "POST", "/api/plugins/"+source+"/clone", `{"suffix":"Reusable"}`, cookie, "")
		assertOriginalRegistrySuccess(t, resp.Code, resp.Body.Bytes())
		a.wg.Wait()
	}
	clone()
	first, err := a.pluginStore.Installation(ctx, target)
	if err != nil || !first.Installed {
		t.Fatalf("first clone %v", err)
	}
	if _, err = a.pluginStore.SaveData(ctx, target, "history", 0, "old-clone-history"); err != nil {
		t.Fatal(err)
	}
	r = callTest(t, a.Handler(), "DELETE", "/api/plugins/"+target, `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	r = callTest(t, a.Handler(), "POST", "/api/plugins/"+target+"/reload", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	if _, ok = a.pluginRunningInstances.Load(target); ok {
		t.Fatal("removed alias was revived before explicit clone")
	}
	clone()
	second, err := a.pluginStore.Installation(ctx, target)
	if err != nil || !second.Installed || second.Generation <= first.Generation {
		t.Fatalf("explicit clone must create new generation old=%d new=%d %v", first.Generation, second.Generation, err)
	}
	if _, err = a.pluginStore.Data(ctx, target, "history"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old clone KV survived recreation: %v", err)
	}
	if _, ok = a.pluginRunningInstances.Load(target); !ok {
		t.Fatal("recreated target not initialized")
	}
	after, err := a.pluginStore.Config(ctx, source)
	if err != nil || after.Revision != before.Revision || !bytes.Equal(after.Value, before.Value) {
		t.Fatalf("source config changed: %v", err)
	}
	afterData, err := a.pluginStore.Data(ctx, source, "history")
	if err != nil || afterData.Revision != sourceData.Revision || !bytes.Equal(afterData.Value, sourceData.Value) {
		t.Fatalf("source KV changed: %v", err)
	}
	object, ok := a.pluginRunningInstances.Load(source)
	if !ok || object != sourceObject {
		t.Fatal("clone altered source object")
	}
	in, err = a.pluginStore.Installation(ctx, source)
	if err != nil || in.Installed {
		t.Fatal("clone implicitly reinstalled source")
	}
}
