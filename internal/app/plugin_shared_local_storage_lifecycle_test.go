package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins"
)

func sharedLocalStorageCurrent(t *testing.T, a *App, id string) *originalPluginInstance {
	t.Helper()
	v, ok := a.pluginRunningInstances.Load(id)
	if !ok {
		t.Fatal("actual worker not loaded", id)
	}
	return v.(*originalPluginInstance)
}
func TestSharedLocalStorageActualModuleOverrides(t *testing.T) {
	a, cookie, _ := sharedLocalStorageFixture(t, sharedLocalStorageAll)
	root := sharedLocalStorageRoot(t, a)
	path := filepath.Join(root, "module.mkv")
	if e := sharedLocalStorageFixtureWrite(t, path, []byte("owned"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, x := range []struct {
		name, method, cap, result, want string
		exists                          bool
	}{
		{"exists-false", "get_file_item", plugins.LocalStorageExistsCapability, "false", "false", true},
		{"exists-truthy", "get_file_item", plugins.LocalStorageExistsCapability, `{"ordinary":"value"}`, "true", true},
		{"delete-false", "delete_file", plugins.LocalStorageDeleteCapability, "false", "false", true},
		{"delete-true", "delete_file", plugins.LocalStorageDeleteCapability, "true", "true", true},
		{"delete-null-native", "delete_file", plugins.LocalStorageDeleteCapability, "null", "true", false},
		{"list-scalar-terminal", "list_files", plugins.LocalStorageListCapability, "false", "false", false},
	} {
		t.Run(x.name, func(t *testing.T) {
			cfg := mustCleanSeedJSON(map[string]any{"module_method": x.method, "module_result": json.RawMessage(x.result)})
			r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedLocalStorage", string(cfg), cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			a.wg.Wait()
			p := sharedLocalStorageCurrent(t, a, "OwnedLocalStorage")
			out, e := sharedLocalStorageCall(t, p, x.cap, plugins.LocalStorageRequest{FileItem: sharedLocalStorageItemInput(t, map[string]any{"path": path})})
			if e != nil || string(out) != x.want {
				t.Fatal("module source result", e, string(out), x.want)
			}
			_, e = os.Stat(path)
			if (e == nil) != x.exists {
				t.Fatal("module override native short circuit changed", e)
			}
		})
	}
	kept := filepath.Join(root, "kept.mkv")
	if e := sharedLocalStorageFixtureWrite(t, kept, []byte("owned native merge"), 0600); e != nil {
		t.Fatal(e)
	}
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedLocalStorage", `{"module_method":"list_files","module_result":[]}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	out, e := sharedLocalStorageCall(t, sharedLocalStorageCurrent(t, a, "OwnedLocalStorage"), plugins.LocalStorageListCapability, plugins.LocalStorageRequest{FileItem: sharedLocalStorageItemInput(t, map[string]any{"path": root})})
	var listed []*plugins.ModuleFileItem
	if e != nil || json.Unmarshal(out, &listed) != nil || len(listed) != 1 || string(listed[0].Fields["name"]) != `"kept.mkv"` {
		t.Fatal("source empty-list override did not accumulate actual native file", e, string(out))
	}

}
func TestSharedLocalStorageOrdinaryCloneColdAndCapabilities(t *testing.T) {
	a, cookie, p := sharedLocalStorageFixture(t, sharedLocalStorageAll)
	dir := a.Dir
	root := sharedLocalStorageRoot(t, a)
	path := filepath.Join(root, "shared.mkv")
	if e := sharedLocalStorageFixtureWrite(t, path, []byte("owned"), 0600); e != nil {
		t.Fatal(e)
	}
	resp := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedLocalStorage/clone", `{"suffix":"Shared"}`, cookie, "")
	assertOriginalRegistrySuccess(t, resp.Code, resp.Body.Bytes())
	a.wg.Wait()
	clone := sharedLocalStorageCurrent(t, a, "OwnedLocalStorageshared")
	var originalSnap, cloneSnap map[string]json.RawMessage
	if e := p.readSession(context.Background(), "snapshot", &originalSnap); e != nil {
		t.Fatal(e)
	}
	if e := clone.readSession(context.Background(), "snapshot", &cloneSnap); e != nil {
		t.Fatal(e)
	}
	if string(originalSnap["pid"]) == string(cloneSnap["pid"]) {
		t.Fatal("clone shared process")
	}
	item := sharedLocalStorageItemInput(t, map[string]any{"path": path})
	if out, e := sharedLocalStorageCall(t, clone, plugins.LocalStorageExistsCapability, plugins.LocalStorageRequest{FileItem: item}); e != nil || string(out) != "true" {
		t.Fatal("clone shared root", e, string(out))
	}
	if out, e := sharedLocalStorageCall(t, p, plugins.LocalStorageDeleteCapability, plugins.LocalStorageRequest{FileItem: item}); e != nil || string(out) != "true" {
		t.Fatal(e, string(out))
	}
	if out, e := sharedLocalStorageCall(t, clone, plugins.LocalStorageExistsCapability, plugins.LocalStorageRequest{FileItem: item}); e != nil || string(out) != "false" {
		t.Fatal("clone effect not visible", e, string(out))
	}
	if e := a.Close(); e != nil {
		t.Fatal(e)
	}
	markets := []string{}
	cold, e := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	for _, id := range []string{"OwnedLocalStorage", "OwnedLocalStorageshared"} {
		worker := cold.startOriginalPlugin(context.Background(), id)
		if worker == nil {
			t.Fatal("cold worker absent")
		}
		if out, e := sharedLocalStorageCall(t, worker, plugins.LocalStorageExistsCapability, plugins.LocalStorageRequest{FileItem: item}); e != nil || string(out) != "false" {
			t.Fatal("cold re-created deletion", e, string(out))
		}
	}
	denied, _, w := sharedLocalStorageFixture(t, nil)
	deniedRoot := sharedLocalStorageRoot(t, denied)
	for _, method := range sharedLocalStorageAll {
		if _, e := sharedLocalStorageCall(t, w, method, plugins.LocalStorageRequest{FileItem: sharedLocalStorageItemInput(t, map[string]any{"path": deniedRoot})}); e == nil {
			t.Fatal("ungranted capability accepted", method)
		}
	}
}
