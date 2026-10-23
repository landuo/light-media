package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"os"
	"path/filepath"
	"testing"
)

func TestPluginOriginalConfigurationHTTPSequence(t *testing.T) {
	for _, loaded := range []bool{false, true} {
		name := "not-running"
		if loaded {
			name = "running"
		}
		t.Run(name, func(t *testing.T) {
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			cookie := setupTestAdmin(t, a)
			ctx := context.Background()
			installCloneSource(t, a.pluginStore, builtin.SystemProcessManifest())
			if _, err := a.pluginStore.SaveConfig(ctx, "SystemProcess", 0, map[string]any{"enable": false}); err != nil {
				t.Fatal(err)
			}
			var original *originalPluginInstance
			if loaded {
				original = a.startOriginalPlugin(ctx, "SystemProcess")
				if original == nil {
					t.Fatal("init")
				}
			}
			var before int
			a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs`).Scan(&before)
			// Deliberately stale client revision is not a condition in original PUT.
			response := callTest(t, a.Handler(), "PUT", "/api/plugins/SystemProcess/config", `{"revision":999,"config":{"enable":true,"unknown":0,"onlyonce":true,"empty":""}}`, cookie, "")
			var body struct {
				Data struct {
					Success bool `json:"success"`
				} `json:"data"`
			}
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || !body.Data.Success {
				t.Fatalf("save %d %s", response.Code, response.Body.String())
			}
			stored, err := a.pluginStore.Config(ctx, "SystemProcess")
			if err != nil {
				t.Fatal(err)
			}
			var config map[string]any
			json.Unmarshal(stored.Value, &config)
			if loaded && (config["enable"] != true || config["onlyonce"] != true || config["unknown"] != float64(0) || config["empty"] != "") {
				t.Fatal("original dict altered")
			}
			if !loaded && (len(config) != 1 || config["enable"] != false) {
				t.Fatal("save without imported class must be ignored")
			}
			current, exists := a.pluginRunningInstances.Load("SystemProcess")
			var after int
			a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs`).Scan(&after)
			if loaded {
				if !exists || current != original || after != before+1 {
					t.Fatal("save must init same object exactly once, without stop/reload")
				}
				if state, err := original.getState(ctx); err != nil || !state {
					t.Fatal("applied state", state, err)
				}
			} else if exists || after != before {
				t.Fatal("original manager.init on absent running object must be no-op")
			}
			// The existing transport alias and original raw-dict route share the method.
			response = callTest(t, a.Handler(), "PUT", "/api/plugins/SystemProcess", `{"enable":false,"other":false}`, cookie, "")
			if response.Code != 200 {
				t.Fatalf("raw original PUT %d %s", response.Code, response.Body.String())
			}
			response = callTest(t, a.Handler(), "POST", "/api/plugins/SystemProcess/reload", `{}`, cookie, "")
			if response.Code != 200 {
				t.Fatalf("reload %d %s", response.Code, response.Body.String())
			}
			replacement, ok := a.pluginRunningInstances.Load("SystemProcess")
			if !ok || loaded && replacement == original {
				t.Fatal("reload must stop and create replacement")
			}
			if state, err := replacement.(*originalPluginInstance).getState(ctx); err != nil || state {
				t.Fatal("reload state", state, err)
			}
			a.wg.Wait()
		})
	}
}

func TestPluginOriginalConfigurationInitErrorKeepsSavedConfigAndObject(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	installCloneSource(t, a.pluginStore, builtin.SystemProcessManifest())
	if _, err := a.pluginStore.SaveConfig(ctx, "SystemProcess", 0, map[string]any{"enable": false}); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(t.TempDir(), "count")
	a.pluginCommand = func(string) (plugins.Command, error) {
		return plugins.Command{Path: executable, Args: []string{"-test.run=^TestOriginalCloneExceptionSDKFixture$", "--", counter, "2"}}, nil
	}
	object := a.startOriginalPlugin(ctx, "SystemProcess")
	if object == nil {
		t.Fatal("initial start")
	}
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/SystemProcess", `{"enable":true,"saved_before_failure":"owned"}`, cookie, "")
	if response.Code == 200 {
		t.Fatalf("init exception turned into original success: %s", response.Body.String())
	}
	stored, err := a.pluginStore.Config(ctx, "SystemProcess")
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	json.Unmarshal(stored.Value, &value)
	if value["saved_before_failure"] != "owned" {
		t.Fatal("save rolled back after init error")
	}
	got, ok := a.pluginRunningInstances.Load("SystemProcess")
	if !ok || got != object {
		t.Fatal("direct init error removed old object")
	}
	if _, registered := a.pluginRegisteredAPIs.Load("SystemProcess"); registered {
		t.Fatal("registration ran after failed init")
	}
	raw, err := os.ReadFile(counter)
	if err != nil || string(raw) != "2" {
		t.Fatalf("init calls %s %v", raw, err)
	}
}
