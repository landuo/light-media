package app

import (
	"bytes"
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/plugins/artifacts"
)

func TestOriginalCloneSuffixActualSDKSafeAPISourceCases(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-clone-suffix/source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Suffix          string
			SourceID        string `json:"source_id"`
			TargetID        string `json:"target_id"`
			Length          int    `json:"target_length"`
			Success, Loaded bool
			InitCount       *int `json:"init_count"`
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 28 {
		t.Fatal("fixed clone HTTP/helper filesystem oracle")
	}
	a, cookie, source := ownedModuleFixture(t, true)
	saveOwnedModule(t, a, cookie, source, `{"enabled":true,"marker":"owned-source","modules":{}}`)
	source = originalBaseSDKLoaded(t, a, ownedModuleID)
	before := ownedModuleRead(t, source)
	clones := []string{}
	for _, row := range oracle.Cases {
		if row.Suffix != "_Copy" && row.Suffix != strings.Repeat("A", 21) && row.Suffix != strings.Repeat("A", 96-len(ownedModuleID)) {
			continue
		}
		t.Run(row.Suffix, func(t *testing.T) {
			if row.SourceID != ownedModuleID || row.TargetID != ownedModuleID+strings.ToLower(row.Suffix) || !row.Success || row.InitCount == nil || *row.InitCount != 1 {
				t.Fatal("fixed successful HTTP clone source conditions missing")
			}
			body, _ := json.Marshal(map[string]any{"suffix": row.Suffix})
			res := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedModuleID+"/clone", string(body), cookie, "")
			assertOriginalRegistrySuccess(t, res.Code, res.Body.Bytes())
			value, ok := a.pluginRunningInstances.Load(row.TargetID)
			if !ok {
				t.Fatal("source-valid safe clone not running")
			}
			object := value.(*originalPluginInstance)
			got := ownedModuleRead(t, object)
			if got.PID == before.PID || got.Calls["init"] != *row.InitCount {
				t.Fatal("actual SDK clone identity/final HTTP init differs from source")
			}
			stored, e := a.pluginStore.Config(context.Background(), row.TargetID)
			var config map[string]any
			if e != nil || json.Unmarshal(stored.Value, &config) != nil || config["enabled"] != false || config["enable"] != false || config["marker"] != "owned-source" {
				t.Fatal("actual persisted clone config copy/disable differs", e)
			}
			primary, e := artifacts.ReadInstalled(a.pluginPackageRoot(), ownedModuleID, validatePluginArtifactContract)
			if e != nil {
				t.Fatal(e)
			}
			copy, e := artifacts.ReadInstalled(a.pluginPackageRoot(), row.TargetID, validatePluginArtifactContract)
			if e != nil || copy.SHA256 != primary.SHA256 || copy.Manifest.PluginID != ownedModuleID {
				t.Fatal("safe API suffix rewrote/lost compiled worker", e)
			}
			clones = append(clones, row.TargetID)
		})
	}
	if len(clones) != 3 {
		t.Fatal("safe source cases not all executed", len(clones))
	}
	for _, id := range clones {
		res := callTest(t, a.Handler(), "DELETE", "/api/plugins/"+id, "", cookie, "")
		assertOriginalRegistrySuccess(t, res.Code, res.Body.Bytes())
		if _, ok := a.pluginRunningInstances.Load(id); ok {
			t.Fatal("safe clone uninstall retained runtime")
		}
	}
	if after := ownedModuleRead(t, source); after.PID != before.PID || !bytes.Equal(after.Inputs["init"], before.Inputs["init"]) {
		t.Fatal("safe suffix operations modified source object/config")
	}
}

func TestOriginalCloneSuffixActualSDKUnicodeAndUnloadedSourceCases(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-clone-suffix/source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Suffix          string
			Target          string `json:"target_id"`
			Success, Loaded bool
			Init            *int `json:"init_count"`
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 28 {
		t.Fatal("fixed HTTP source cases")
	}
	a, cookie, source := ownedModuleFixture(t, true)
	saveOwnedModule(t, a, cookie, source, `{"enabled":true,"marker":"owned-source","modules":{}}`)
	source = originalBaseSDKLoaded(t, a, ownedModuleID)
	before := ownedModuleRead(t, source)
	for _, row := range oracle.Cases {
		if row.Suffix == "" || row.Suffix == "_Copy" || row.Suffix == strings.Repeat("A", 21) || row.Suffix == strings.Repeat("A", 96-len(ownedModuleID)) {
			continue
		}
		t.Run(row.Suffix, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"suffix": row.Suffix})
			res := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedModuleID+"/clone", string(body), cookie, "")
			var response struct{ Data struct{ Success bool } }
			if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &response) != nil || response.Data.Success != row.Success {
				t.Fatalf("actual HTTP source success mismatch: %d %s", res.Code, res.Body.String())
			}
			if !row.Success {
				return
			}
			in, e := a.pluginStore.Installation(context.Background(), row.Target)
			if e != nil || !in.Installed {
				t.Fatal("successful copied code not installed", e)
			}
			if _, e = os.Stat(filepath.Join(a.pluginPackageRoot(), row.Target)); e != nil {
				t.Fatal("actual copied code missing", e)
			}
			cfg, e := a.pluginStore.Config(context.Background(), row.Target)
			var config map[string]any
			if e != nil || json.Unmarshal(cfg.Value, &config) != nil || config["enable"] != false || config["enabled"] != false || config["marker"] != "owned-source" {
				t.Fatal("actual clone config side effects", e)
			}
			beforeCfg := bytes.Clone(cfg.Value)
			runtime, loaded := a.pluginRunningInstances.Load(row.Target)
			_, registered := a.pluginOriginalClasses.Load(row.Target)
			if loaded != row.Loaded || registered != row.Loaded {
				t.Fatal("source import/registry state differs", loaded, registered, row.Loaded)
			}
			if loaded {
				got := ownedModuleRead(t, runtime.(*originalPluginInstance))
				if got.PID == before.PID || row.Init == nil || got.Calls["init"] != *row.Init {
					t.Fatal("actual Unicode worker PID/init isolation")
				}
			}
			// Source static paths lowercase the complete supplied class spelling;
			// SimpleFold cannot match dotted-I expansion to i+combining dot.
			alias := plugins.OriginalPluginLower(ownedModuleID) + row.Suffix
			file := callTest(t, a.Handler(), "GET", "/api/v1/plugin/file/"+url.PathEscape(alias)+"/owned-module-override", "", cookie, "")
			wantFile, readErr := os.ReadFile(filepath.Join(a.pluginPackageRoot(), row.Target, "owned-module-override"))
			if file.Code != 200 || readErr != nil || !bytes.Equal(file.Body.Bytes(), wantFile) {
				t.Fatal("source full-lower static alias bytes differ", file.Code, readErr)
			}
			if plugins.OriginalPluginLower(row.Suffix) != strings.TrimPrefix(row.Target, ownedModuleID) {
				t.Fatal("source lower identity")
			}
			res = callTest(t, a.Handler(), "DELETE", "/api/plugins/"+url.PathEscape(row.Target), "", cookie, "")
			assertOriginalRegistrySuccess(t, res.Code, res.Body.Bytes())
			_, e = os.Stat(filepath.Join(a.pluginPackageRoot(), row.Target))
			if row.Loaded && !os.IsNotExist(e) || !row.Loaded && e != nil {
				t.Fatal("source class registry-dependent uninstall code retention differs", e)
			}
			if !row.Loaded {
				cfg, e = a.pluginStore.Config(context.Background(), row.Target)
				if e != nil || !bytes.Equal(cfg.Value, beforeCfg) {
					t.Fatal("unloaded clone lost retained config", e)
				}
			}
		})
	}
	if got := ownedModuleRead(t, source); got.PID != before.PID || !bytes.Equal(got.Inputs["init"], before.Inputs["init"]) {
		t.Fatal("source object/config changed")
	}
}
