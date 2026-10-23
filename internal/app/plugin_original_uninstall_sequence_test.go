package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"light-media/internal/plugins/builtin"
)

func TestPluginOriginalUninstallClassAndCodeSequence(t *testing.T) {
	for _, mode := range []string{"loaded-ordinary", "loaded-clone", "stopped-clone"} {
		t.Run(mode, func(t *testing.T) {
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			ctx := context.Background()
			cookie := setupTestAdmin(t, a)
			installCloneSource(t, a.pluginStore, builtin.SystemProcessManifest())
			if _, err := a.pluginStore.SaveConfig(ctx, "SystemProcess", 0, map[string]any{"enable": false, "owned": "source"}); err != nil {
				t.Fatal(err)
			}
			if a.startOriginalPlugin(ctx, "SystemProcess") == nil {
				t.Fatal("source start")
			}
			// Formal endpoint creates real aliases and runs the original initializer chain.
			for _, suffix := range []string{"Target", "Sibling"} {
				r := callTest(t, a.Handler(), "POST", "/api/plugins/SystemProcess/clone", `{"suffix":"`+suffix+`"}`, cookie, "")
				if r.Code != 200 {
					t.Fatalf("clone %d %s", r.Code, r.Body.String())
				}
			}
			a.wg.Wait()
			ids := []string{"SystemProcess", "SystemProcesstarget", "SystemProcesssibling"}
			configs := map[string]PluginStoredValue{}
			data := map[string]PluginStoredValue{}
			objects := map[string]any{}
			for _, id := range ids {
				if _, err := a.pluginStore.SaveData(ctx, id, "owned-history", 0, id); err != nil {
					t.Fatal(err)
				}
				c, err := a.pluginStore.Config(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				configs[id] = c
				v, err := a.pluginStore.Data(ctx, id, "owned-history")
				if err != nil {
					t.Fatal(err)
				}
				data[id] = v
				o, ok := a.pluginRunningInstances.Load(id)
				if !ok {
					t.Fatalf("missing real object %s", id)
				}
				objects[id] = o
			}
			target := "SystemProcesstarget"
			if mode == "loaded-ordinary" {
				target = "SystemProcess"
			}
			if mode == "stopped-clone" {
				if err := a.stopOriginalPlugin(ctx, target); err != nil {
					t.Fatal(err)
				}
			}
			method, path := "DELETE", "/api/plugins/"+target
			if mode == "stopped-clone" {
				method, path = "POST", "/api/plugins/"+target+"/uninstall"
			}
			r := callTest(t, a.Handler(), method, path, `{}`, cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			a.wg.Wait()
			in, err := a.pluginStore.Installation(ctx, target)
			if err != nil || in.Installed {
				t.Fatalf("installed list not removed: %v", err)
			}
			if _, ok := a.pluginRunningInstances.Load(target); ok {
				t.Fatal("uninstall did not stop target")
			}
			if _, ok := a.pluginRegisteredAPIs.Load(target); ok {
				t.Fatal("uninstall left registered target API")
			}
			// SystemProcess has no public services. This checks absence, not a claim
			// that a non-empty scheduler removal path has been exercised.
			var jobs int
			if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_schedules WHERE instance_id=? AND group_key='original_services' AND active=1`, target).Scan(&jobs); err != nil || jobs != 0 {
				t.Fatalf("public jobs remain %d %v", jobs, err)
			}
			for _, id := range ids {
				c, ce := a.pluginStore.Config(ctx, id)
				v, ve := a.pluginStore.Data(ctx, id, "owned-history")
				if id == target && mode == "loaded-clone" {
					if !errors.Is(ce, sql.ErrNoRows) || !errors.Is(ve, sql.ErrNoRows) {
						t.Fatalf("loaded clone did not clear own values %v %v", ce, ve)
					}
				} else {
					if ce != nil || ve != nil || c.Revision != configs[id].Revision || v.Revision != data[id].Revision || !bytes.Equal(c.Value, configs[id].Value) || !bytes.Equal(v.Value, data[id].Value) {
						t.Fatalf("unrelated or retained values changed %s %v %v", id, ce, ve)
					}
				}
				if id != target {
					o, ok := a.pluginRunningInstances.Load(id)
					if !ok || o != objects[id] {
						t.Fatalf("other instance stopped %s", id)
					}
				}
			}
			r = callTest(t, a.Handler(), "POST", "/api/plugins/"+target+"/reload", `{}`, cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			a.wg.Wait()
			_, loaded := a.pluginRunningInstances.Load(target)
			if loaded != (mode != "loaded-clone") {
				t.Fatalf("code existence after %s: loaded=%v", mode, loaded)
			}
			in, err = a.pluginStore.Installation(ctx, target)
			if err != nil || in.Installed {
				t.Fatal("reload re-added user installation")
			}
		})
	}
}
