package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func verifyOwnedProductionTransferTypes(t *testing.T, dir, origin string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	worker := os.Getenv("OWNED_TRANSFER_MODULE_PROCESS_BINARY")
	payload, err := os.ReadFile(worker)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(worker, "--manifest").Output()
	var manifest plugins.Manifest
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != ownedModuleID {
		t.Fatal("actual module transfer worker", err)
	}
	digest := sha256.Sum256(payload)
	sum := hex.EncodeToString(digest[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: sum, ExecutableSHA256: sum, Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer repository.Close()
	index.Releases[0].Artifacts[0].URL = repository.URL + "/worker"
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": manifest.PluginID, "repo_url": repository.URL + "/index"})

	success("POST", "/api/plugins/"+manifest.PluginID+"/clone", map[string]any{"suffix": "Types"})
	clone := manifest.PluginID + "types"
	read := func(id string) ownedModuleSnapshot {
		t.Helper()
		var x ownedModuleSnapshot
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/snapshot", nil, 200), &x) != nil || x.PID <= 0 {
			t.Fatal("actual worker")
		}
		return x
	}
	var proof []any
	for _, c := range readTransferTypesOracle(t, "testdata/standard-module-host/transfer-types-source-oracle.json").Cases {
		method := "module_execute"
		if c.Fail {
			method = "module_fail"
		}
		success("PUT", "/api/plugins/"+manifest.PluginID, map[string]any{"enabled": true, "modules": map[string]string{"support_transtype": method}, "result": c.Plugin})
		success("PUT", "/api/plugins/"+clone, map[string]any{"enabled": true, "modules": map[string]string{"support_transtype": "module_execute"}, "result": nil})
		before := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
		raw := request("GET", "/api/directories/transfer-types", nil, c.Status)
		if c.Status == 200 {
			var x struct{ Data json.RawMessage }
			if json.Unmarshal(raw, &x) != nil || !originalBaseJSONEqual(x.Data, c.Output) {
				t.Fatal("fixed transfer types", c.Name, string(raw), string(c.Output))
			}
		}
		after := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
		wantClone := 0
		for _, x := range c.Calls {
			if x.Kind == "clone" {
				wantClone++
			}
		}
		if after[0].Calls[method]-before[0].Calls[method] != 1 || after[1].Calls["module_execute"]-before[1].Calls["module_execute"] != wantClone {
			t.Fatal("source type dispatch", c.Name)
		}
		proof = append(proof, map[string]any{"case": c.Name, "status": c.Status, "result": json.RawMessage(raw), "before": before, "after": after})
	}
	config := map[string]any{"enabled": true, "modules": map[string]string{"support_transtype": "module_execute"}, "result": nil}
	cloneConfig := map[string]any{"enabled": true, "modules": map[string]string{"support_transtype": "module_execute"}, "result": json.RawMessage(`{"copy":"分身复制","link":"分身硬链接"}`)}
	success("PUT", "/api/plugins/"+manifest.PluginID, config)
	success("PUT", "/api/plugins/"+clone, cloneConfig)
	owned, err := os.MkdirTemp(filepath.Dir(dir), "types55-owned-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(owned)
	for _, name := range []string{"source", "library"} {
		if err = os.Mkdir(filepath.Join(owned, name), 0750); err != nil {
			t.Fatal(err)
		}
	}
	saved := request("PUT", "/api/directories/manage", map[string]any{"directories": []any{map[string]any{"id": "types55", "name": "Owned Transfer Types", "source_path": filepath.Join(owned, "source"), "library_path": filepath.Join(owned, "library"), "media_type": "mixed", "mode": "auto", "monitor_type": "downloader", "enabled": true, "renaming": false, "scraping": false}}}, 200)
	var savedDirectories struct {
		Data struct {
			Directories []struct{ Config DirectoryRule }
		}
	}
	if json.Unmarshal(saved, &savedDirectories) != nil || len(savedDirectories.Data.Directories) != 1 || savedDirectories.Data.Directories[0].Config.Mode != "auto" {
		t.Fatal("actual directory saved mode", string(saved))
	}

	check := func() {
		t.Helper()
		raw := request("GET", "/api/directories/transfer-types", nil, 200)
		var x struct{ Data json.RawMessage }
		if json.Unmarshal(raw, &x) != nil || !originalBaseJSONEqual(x.Data, json.RawMessage(`{"transtype":{"copy":"分身复制","link":"分身硬链接"}}`)) {
			t.Fatal("clone return", string(raw))
		}
	}
	check()
	before := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
	stop()
	start()
	after := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
	for i, x := range after {
		if x.PID == before[i].PID || x.Calls["module_execute"] != 0 {
			t.Fatal("cold replay", before, after)
		}
	}
	proof = append(proof, map[string]any{"mode": "cold", "before": before, "after": after})
	check()
	if statePath := os.Getenv("PLUGIN_PROCESS_TRANSFER_TYPES_STATE"); statePath != "" {
		if err = os.WriteFile(statePath, mustCleanSeedJSON(map[string]any{"origin": origin, "data_dir": dir, "source": manifest.PluginID, "clone": clone, "source_config": config, "clone_config": cloneConfig, "owned": owned}), 0600); err != nil {
			t.Fatal(err)
		}
		done := false
		for until := time.Now().Add(25 * time.Minute); time.Now().Before(until); {
			if _, e := os.Stat(statePath + ".restart"); e == nil {
				stop()
				start()
				os.Remove(statePath + ".restart")
				os.WriteFile(statePath+".restart.done", mustCleanSeedJSON([]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}), 0600)
			}
			if _, e := os.Stat(statePath + ".done"); e == nil {
				done = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !done {
			t.Fatal("browser transfer types gate timeout")
		}
	}
	writeOwnedEventsProcessEvidence(t, "transfer-types55-proof.json", mustCleanSeedJSON(proof))
	success("DELETE", "/api/plugins/"+clone, nil)
	success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
	request("GET", "/api/v1/plugin/"+clone+"/snapshot", nil, 404)
	request("GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", nil, 404)
	raw = request("GET", "/api/directories/transfer-types", nil, 200)
	var x struct {
		Data struct {
			Types json.RawMessage `json:"transtype"`
		}
	}
	if json.Unmarshal(raw, &x) != nil || !originalBaseJSONEqual(x.Data.Types, originalLocalTransferTypes) {
		t.Fatal("native restore", string(raw))
	}
	t.Log("actual ten fixed transfer types, clone terminal order, cold zero replay and uninstall native restore")
}
