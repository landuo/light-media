package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

// Expectations come from oracle-registry-v4's original core+endpoint AST.
// Installation, loaded class, and running object are distinct original states.
func TestPluginOriginalRegistryPUTThreeStates(t *testing.T) {
	for _, state := range []string{"no-class", "class-only-init-failed", "running"} {
		t.Run(state, func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			cookie := setupTestAdmin(t, a)
			installCloneSource(t, a.pluginStore, builtin.SystemProcessManifest())
			if _, err := a.pluginStore.SaveConfig(ctx, "SystemProcess", 0, map[string]any{"enable": false, "old": "owned"}); err != nil {
				t.Fatal(err)
			}
			original, err := a.pluginStore.Config(ctx, "SystemProcess")
			if err != nil {
				t.Fatal(err)
			}
			if state == "running" {
				useBuiltInPluginProcess(t, a)
				if a.startOriginalPlugin(ctx, "SystemProcess") == nil {
					t.Fatal("start")
				}
			}
			if state == "class-only-init-failed" {
				exe, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				counter := filepath.Join(t.TempDir(), "count")
				a.pluginCommand = func(string) (plugins.Command, error) {
					return plugins.Command{Path: exe, Args: []string{"-test.run=^TestOriginalCloneExceptionSDKFixture$", "--", counter, "1"}}, nil
				}
				if a.startOriginalPlugin(ctx, "SystemProcess") != nil {
					t.Fatal("expected original start to catch first init error")
				}
			}
			var before int
			if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			r := callTest(t, a.Handler(), "PUT", "/api/plugins/SystemProcess", `{"enable":false,"new":"owned"}`, cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			a.wg.Wait()
			after, err := a.pluginStore.Config(ctx, "SystemProcess")
			if err != nil {
				t.Fatal(err)
			}
			if state == "no-class" {
				if after.Revision != original.Revision || !bytes.Equal(after.Value, original.Value) {
					t.Fatal("missing class must make original save return false without writes")
				}
			} else {
				var conf map[string]any
				if err = json.Unmarshal(after.Value, &conf); err != nil {
					t.Fatal(err)
				}
				if conf["new"] != "owned" || conf["old"] != nil {
					t.Fatalf("class registry must permit save: %s", after.Value)
				}
			}
			var n int
			if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			delta := 0
			if state == "running" {
				delta = 1
			}
			if n != before+delta {
				t.Fatalf("init runs %d -> %d want delta %d", before, n, delta)
			}
			_, running := a.pluginRunningInstances.Load("SystemProcess")
			if running != (state == "running") {
				t.Fatal("PUT unexpectedly creates or removes running instance")
			}
		})
	}
}

func TestPluginOriginalUninstalledExplicitReloadAndReset(t *testing.T) {
	for _, action := range []string{"reload", "reset"} {
		t.Run(action, func(t *testing.T) {
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			ctx := context.Background()
			cookie := setupTestAdmin(t, a)
			installCloneSource(t, a.pluginStore, builtin.SystemProcessManifest())
			if _, err := a.pluginStore.SaveConfig(ctx, "SystemProcess", 0, map[string]any{"enable": false, "retained": "owned"}); err != nil {
				t.Fatal(err)
			}
			if _, err := a.pluginStore.SaveData(ctx, "SystemProcess", "owned-history", 0, "retained"); err != nil {
				t.Fatal(err)
			}
			if err := a.pluginStore.Uninstall(ctx, "SystemProcess"); err != nil {
				t.Fatal(err)
			}
			// No pid startup uses installed list; explicit reload below does not.
			if err := a.initializeOriginalPlugins(ctx); err != nil {
				t.Fatal(err)
			}
			if _, ok := a.pluginRunningInstances.Load("SystemProcess"); ok {
				t.Fatal("startup loaded uninstalled class")
			}
			r := callTest(t, a.Handler(), "POST", "/api/plugins/SystemProcess/"+action, `{}`, cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			a.wg.Wait()
			if _, ok := a.pluginRunningInstances.Load("SystemProcess"); !ok {
				t.Fatal("explicit original loader must find retained compiled code")
			}
			in, err := a.pluginStore.Installation(ctx, "SystemProcess")
			if err != nil {
				t.Fatal(err)
			}
			if in.Installed {
				t.Fatal("explicit reload must not re-add installed list")
			}
			var n int
			if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id='SystemProcess' AND action='init' AND state='succeeded'`).Scan(&n); err != nil || n != 1 {
				t.Fatalf("real init count=%d err=%v", n, err)
			}
			var configN, dataN int
			if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_values WHERE instance_id='SystemProcess' AND kind='config'`).Scan(&configN); err != nil {
				t.Fatal(err)
			}
			if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_values WHERE instance_id='SystemProcess' AND kind='data'`).Scan(&dataN); err != nil {
				t.Fatal(err)
			}
			want := 1
			if action == "reset" {
				want = 0
			}
			if configN != want || dataN != want {
				t.Fatalf("retained values config=%d data=%d want=%d", configN, dataN, want)
			}
		})
	}
}

func TestPluginOriginalUnknownRegistryHTTPNoObject(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	// Truly unknown IDs cannot have plugin_values rows under the Go FK. Do not
	// disable constraints or manufacture installation/owner receipts to fake it.
	id := "OwnedUnknownOriginalRegistry"
	for _, action := range []string{"reload", "reset"} {
		r := callTest(t, a.Handler(), "POST", "/api/plugins/"+id+"/"+action, `{}`, cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
		if _, ok := a.pluginRunningInstances.Load(id); ok {
			t.Fatal("unknown ID acquired object")
		}
	}
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, `{"owned":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	var n int
	for _, table := range []string{"plugin_installations", "plugin_values", "plugin_runs"} {
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE instance_id=?`, id).Scan(&n); err != nil || n != 0 {
			t.Fatalf("unknown target wrote %s n=%d err=%v", table, n, err)
		}
	}
}

func assertOriginalRegistrySuccess(t *testing.T, status int, raw []byte) {
	t.Helper()
	var body struct {
		Data struct {
			Success bool `json:"success"`
		} `json:"data"`
	}
	if status != 200 || json.Unmarshal(raw, &body) != nil || !body.Data.Success {
		t.Fatalf("original endpoint success expected: %d %s", status, raw)
	}
}
