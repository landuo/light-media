package app

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/autosignin"
	sr "light-media/internal/plugins/seedrescue"
)

func TestPluginCommonNativeReadViewsInstallCloneReset(t *testing.T) {
	for _, manifest := range []plugins.Manifest{autosignin.Manifest(), sr.NativeManifest()} {
		t.Run(manifest.PluginID, func(t *testing.T) {
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			cookie := setupTestAdmin(t, a)
			ctx := context.Background()
			p := a.pluginStore
			installCloneSource(t, p, manifest)
			config := map[string]any{"enabled": false, "onlyonce": false}
			if manifest.PluginID == sr.PluginID {
				config = sr.ParseConfig(sr.Object{}).Current()
			}
			if _, err := p.SaveConfig(ctx, manifest.PluginID, 0, config); err != nil {
				t.Fatal(err)
			}
			clone, err := p.Clone(ctx, manifest.PluginID, PluginCloneRequest{Suffix: "CommonRead"})
			if err != nil {
				t.Fatal(err)
			}
			path := "/page"
			if manifest.PluginID == sr.PluginID {
				path = "/api/status"
			}
			read := func(id string) {
				t.Helper()
				before := instanceReadTables(t, a)
				response := callTest(t, a.Handler(), "GET", "/api/plugins/"+id+path, "", cookie, "")
				if response.Code != 200 {
					t.Fatalf("read %s: %d %s", id, response.Code, response.Body.String())
				}
				if !json.Valid(response.Body.Bytes()) {
					t.Fatal("invalid view JSON")
				}
				if !reflect.DeepEqual(before, instanceReadTables(t, a)) {
					t.Fatal("read changed configuration, jobs or native state")
				}
			}
			read(manifest.PluginID)
			read(clone.InstanceID)
			sourceBefore := reloadParityRows(t, a, manifest.PluginID)
			reset := callTest(t, a.Handler(), "POST", "/api/plugins/"+clone.InstanceID+"/reset", `{}`, cookie, "")
			if reset.Code != 200 {
				t.Fatalf("reset %d %s", reset.Code, reset.Body.String())
			}
			read(clone.InstanceID)
			if !reflect.DeepEqual(sourceBefore, reloadParityRows(t, a, manifest.PluginID)) {
				t.Fatal("clone reset/view changed source")
			}
		})
	}
}
