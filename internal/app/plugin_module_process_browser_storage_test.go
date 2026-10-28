package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func verifyOwnedProductionBrowserStorage(t *testing.T, dir, origin string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	binary := os.Getenv("OWNED_BROWSER_STORAGE_PROCESS_BINARY")
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(binary, "--manifest").Output()
	var manifest plugins.Manifest
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedModuleOverride" {
		t.Fatal("owned browser storage SDK", err)
	}
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}
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
	folder := filepath.Join(t.TempDir(), "OwnedStorageBrowser")
	if os.MkdirAll(folder, 0750) != nil || os.WriteFile(filepath.Join(folder, "File10.mkv"), []byte("owned native browser file"), 0640) != nil {
		t.Fatal("owned browser directory")
	}
	var settings struct{ Data Settings }
	if json.Unmarshal(request("GET", "/api/settings", nil, 200), &settings) != nil {
		t.Fatal("owned browser settings")
	}
	settings.Data.AllowedRoots = append(settings.Data.AllowedRoots, folder)
	request("PUT", "/api/settings", settings.Data, 200)
	var roots struct{ Data []browserRoot }
	if json.Unmarshal(request("GET", "/api/files/roots", nil, 200), &roots) != nil {
		t.Fatal("actual browser roots")
	}
	var root browserRoot
	for _, candidate := range roots.Data {
		if candidate.Path == folder {
			root = candidate
		}
	}
	if root.ID == "" {
		t.Fatal("owned directory not in actual browser roots")
	}
	uri := "/api/files/list?" + url.Values{"root_id": {root.ID}, "path": {folder}, "sort": {"name"}, "order": {"asc"}}.Encode()
	set := func(result any) {
		success("PUT", "/api/plugins/"+manifest.PluginID, map[string]any{"enabled": true, "modules": map[string]string{"list_files": "module_execute"}, "result": result})
	}
	plugin := []map[string]any{{"storage": "local", "type": "file", "path": filepath.Join(folder, "File2.mkv"), "name": "File2.mkv", "size": "12.0", "modify_time": "2.0"}}
	list := func(want int) []browserEntry {
		t.Helper()
		var out struct {
			Data struct{ Items []browserEntry }
		}
		raw := request("GET", uri, nil, want)
		if want == 200 && json.Unmarshal(raw, &out) != nil {
			t.Fatal("actual browser list")
		}
		return out.Data.Items
	}
	snapshot := func(id string) ownedModuleSnapshot {
		t.Helper()
		var out ownedModuleSnapshot
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/snapshot", nil, 200), &out) != nil || out.PID <= 0 {
			t.Fatal("actual browser SDK snapshot")
		}
		return out
	}
	set(plugin)
	items := list(200)
	if len(items) != 2 || items[0].Name != "File2.mkv" || items[1].Name != "File10.mkv" || items[0].Protected == "" {
		t.Fatal("actual source concat/natural order/guarded local projection", items)
	}
	set(false)
	list(500)
	set(map[string]any{"__plugin_module_tuple": plugin})
	list(500)
	set(nil)
	if rows := list(200); len(rows) != 1 || rows[0].Name != "File10.mkv" {
		t.Fatal("real native fallback", rows)
	}
	success("PUT", "/api/plugins/"+manifest.PluginID, map[string]any{"enabled": true, "modules": map[string]string{"list_files": "module_fail"}})
	if rows := list(200); len(rows) != 1 {
		t.Fatal("real throw→native fallback", rows)
	}
	set([]map[string]any{{"storage": "local", "type": "file", "path": "/outside/owned-denied.mkv", "name": "Denied.mkv"}})
	list(500)
	success("POST", "/api/plugins/"+manifest.PluginID+"/clone", map[string]any{"suffix": "Files"})
	clone := manifest.PluginID + "files"
	success("PUT", "/api/plugins/"+clone, map[string]any{"enabled": true, "modules": map[string]string{"list_files": "module_execute"}, "result": []map[string]any{{"storage": "local", "type": "file", "path": filepath.Join(folder, "File3.mkv"), "name": "File3.mkv", "size": 3}}})
	set(plugin)
	if rows := list(200); len(rows) != 3 || rows[0].Name != "File2.mkv" || rows[1].Name != "File3.mkv" || rows[2].Name != "File10.mkv" {
		t.Fatal("real separate source/clone list order", rows)
	}
	beforeSource, beforeClone := snapshot(manifest.PluginID), snapshot(clone)
	stop()
	start()
	if rows := list(200); len(rows) != 3 || rows[0].Name != "File2.mkv" || rows[1].Name != "File3.mkv" {
		t.Fatal("real browser cold projection", rows)
	}
	afterSource, afterClone := snapshot(manifest.PluginID), snapshot(clone)
	if beforeSource.PID == afterSource.PID || beforeClone.PID == afterClone.PID || afterSource.PID == afterClone.PID {
		t.Fatal("browser storage cold SDK identity")
	}
	var mutationConfig map[string]any
	if os.Getenv("PLUGIN_PROCESS_BROWSER_MUTATION_FIXTURE") == "true" {
		mutationConfig = verifyOwnedProductionBrowserMutations(t, folder, root, manifest.PluginID, clone, request, success, stop, start)
	}
	proof, _ := json.Marshal(map[string]any{"before_source": beforeSource, "before_clone": beforeClone, "after_source": afterSource, "after_clone": afterClone, "items": items, "folder": folder, "root_id": root.ID})
	writeOwnedEventsProcessEvidence(t, "browser-storage-real-main.json", proof)
	if statePath := os.Getenv("PLUGIN_PROCESS_BROWSER_STORAGE_STATE"); statePath != "" {
		state, _ := json.Marshal(map[string]any{"origin": origin, "data_dir": dir, "directory": folder, "root_id": root.ID, "source": manifest.PluginID, "clone": clone, "mutation_config": mutationConfig, "source_config": map[string]any{"enabled": true, "modules": map[string]string{"list_files": "module_execute"}, "result": plugin}})
		if os.WriteFile(statePath, state, 0600) != nil {
			t.Fatal("browser storage state")
		}
		t.Log("actual browser storage ready; waiting for owned browser marker")
		deadline := time.Now().Add(45 * time.Minute)
		finished := false
		for time.Now().Before(deadline) {
			if _, err := os.Stat(statePath + ".restart"); err == nil {
				stop()
				start()
				_ = os.Remove(statePath + ".restart")
				_ = os.WriteFile(statePath+".restart.done", []byte("restarted"), 0600)
			}
			if _, err := os.Stat(statePath + ".done"); err == nil {
				finished = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !finished {
			t.Fatal("browser storage completion marker absent")
		}
	}
	success("DELETE", "/api/plugins/"+clone, nil)
	success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
	request("GET", "/api/v1/plugin/"+clone+"/snapshot", nil, 404)
	request("GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", nil, 404)
	if rows := list(200); len(rows) != 1 || rows[0].Name != "File10.mkv" {
		t.Fatal("uninstall did not restore native browser", rows)
	}
	t.Log("actual browser storage host list/scalar/tuple/throw/path guard/clone/cold/uninstall verified; browser interaction recorded separately")
}
