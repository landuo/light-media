package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
)

// Original explicit pid loading scans code independently of UserInstalledPlugins.
// The internal Go namespace is IPC storage identity, not a user installation.
func TestPluginOriginalNeverInstalledCompiledReloadAndSave(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	const id = "SystemProcess"
	if _, err := a.pluginStore.Installation(ctx, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("fixture already installed or namespace existed: %v", err)
	}
	if err := a.initializeOriginalPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.pluginRunningInstances.Load(id); ok {
		t.Fatal("startup without pid loaded never installed code")
	}
	r := callTest(t, a.Handler(), "POST", "/api/plugins/"+id+"/reload", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	first, ok := a.pluginRunningInstances.Load(id)
	if !ok {
		t.Fatal("explicit compiled code reload did not create running object")
	}
	in, err := a.pluginStore.Installation(ctx, id)
	if err != nil || in.Installed {
		t.Fatalf("namespace must not imply installed: %+v %v", in, err)
	}
	r = callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, `{"enable":false,"owned_namespace_marker":"saved","onlyonce":false}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	before, err := a.pluginStore.Config(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(before.Value, []byte(`"saved"`)) {
		t.Fatal("loaded uninstalled class failed to save")
	}
	r = callTest(t, a.Handler(), "POST", "/api/plugins/"+id+"/reload", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	second, ok := a.pluginRunningInstances.Load(id)
	if !ok || second == first {
		t.Fatal("explicit reload must replace object")
	}
	after, err := a.pluginStore.Config(ctx, id)
	if err != nil || before.Revision != after.Revision || !bytes.Equal(before.Value, after.Value) {
		t.Fatalf("reload changed persisted config: %v", err)
	}
	in, err = a.pluginStore.Installation(ctx, id)
	if err != nil || in.Installed {
		t.Fatalf("save/reload implicitly installed namespace: %v", err)
	}
	var n int
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id=? AND action='init' AND state='succeeded'`, id).Scan(&n); err != nil || n != 3 {
		t.Fatalf("reload/save/reload must have 3 real inits: %d %v", n, err)
	}
	const unknown = "OwnedNoCompiledCodeNamespace"
	r = callTest(t, a.Handler(), "POST", "/api/plugins/"+unknown+"/reload", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	for _, table := range []string{"plugin_installations", "plugin_values", "plugin_runs"} {
		if err = a.Store.DB.QueryRow(`SELECT count(*) FROM `+table+` WHERE instance_id=?`, unknown).Scan(&n); err != nil || n != 0 {
			t.Fatalf("unknown created %s row: %d %v", table, n, err)
		}
	}
	if _, ok := a.pluginRunningInstances.Load(unknown); ok {
		t.Fatal("unknown acquired object")
	}
}
