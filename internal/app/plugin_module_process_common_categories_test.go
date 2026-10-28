package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func verifyOwnedProductionCommonCategories(t *testing.T, dir, origin string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
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

	success("POST", "/api/plugins/"+manifest.PluginID+"/clone", map[string]any{"suffix": "Common"})
	clone := manifest.PluginID + "common"
	read := func(id string) ownedModuleSnapshot {
		t.Helper()
		var out ownedModuleSnapshot
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/snapshot", nil, 200), &out) != nil || out.PID <= 0 {
			t.Fatal("actual worker")
		}
		return out
	}
	set := func(id string, value any, fail bool) {
		method := "module_page_size"
		if fail {
			method = "module_fail"
		}
		success("PUT", "/api/plugins/"+id, map[string]any{"enabled": true, "modules": map[string]string{"media_category": method, "scheduler_job": "module_execute"}, "page_size": value, "result": nil})
	}
	if err = os.WriteFile(filepath.Join(dir, "category.yaml"), []byte("movie:\n  原生电影: {}\ntv:\n  原生剧集: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name   string
			Plugin json.RawMessage
			Fail   bool
			Result json.RawMessage
			Error  *string
			Calls  []string
		}
	}
	oracleRaw, err := os.ReadFile("testdata/standard-module-host/media-category-source-oracle.json")
	if err != nil || json.Unmarshal(oracleRaw, &oracle) != nil {
		t.Fatal("process fixed oracle", err)
	}
	var proof []any
	for _, c := range oracle.Cases {
		set(manifest.PluginID, c.Plugin, c.Fail)
		set(clone, nil, false)
		before := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
		status := 200
		if c.Error != nil {
			status = 500
		}
		raw := request("GET", "/api/settings/rules", nil, status)
		if status == 200 {
			var res struct {
				Data struct{ Categories json.RawMessage }
			}
			if json.Unmarshal(raw, &res) != nil || !originalBaseJSONEqual(res.Data.Categories, c.Result) {
				t.Fatal("fixed category response", c.Name, string(raw))
			}
		}
		after := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
		method := "module_page_size"
		if c.Fail {
			method = "module_fail"
		}
		n := 0
		if slices.Contains(c.Calls, "clone") {
			n = 1
		}
		if after[0].Calls[method]-before[0].Calls[method] != 1 || after[1].Calls["module_page_size"]-before[1].Calls["module_page_size"] != n {
			t.Fatal("process category sequence", c.Name)
		}
		proof = append(proof, map[string]any{"case": c.Name, "response": json.RawMessage(raw), "before": before, "after": after})
	}
	set(manifest.PluginID, nil, false)
	set(clone, map[string]any{"电影": []string{"分身电影"}, "电视剧": []string{"分身剧集"}}, false)
	before := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
	success("GET", "/api/v1/system/runscheduler?jobid=scheduler_job", nil)
	after := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
	for i := range after {
		if after[i].Calls["module_execute"]-before[i].Calls["module_execute"] != 1 {
			t.Fatal("real manual common dispatch", before, after)
		}
	}
	proof = append(proof, map[string]any{"mode": "manual", "before": before, "after": after, "progress": json.RawMessage(request("GET", "/api/v1/dashboard/schedule/scheduler_job/progress", nil, 200))})
	stop()
	restarted := time.Now()
	start()
	cold := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
	for i := range cold {
		if cold[i].PID == after[i].PID || cold[i].Calls["module_execute"] != 0 {
			t.Fatal("cold common replay", after, cold)
		}
	}
	proof = append(proof, map[string]any{"mode": "cold", "before": after, "after": cold, "categories": json.RawMessage(request("GET", "/api/settings/rules", nil, 200)), "schedule": json.RawMessage(request("GET", "/api/plugins/services", nil, 200))})
	if os.Getenv("PLUGIN_PROCESS_COMMON_WAIT") == "true" {
		for deadline := time.Now().Add(630 * time.Second); read(manifest.PluginID).Calls["module_execute"] == 0 && time.Now().Before(deadline); {
			time.Sleep(200 * time.Millisecond)
		}
		tick := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
		elapsed := time.Since(restarted)
		for _, x := range tick {
			if x.Calls["module_execute"] != 1 {
				t.Fatal("actual 10-minute source/clone tick", tick)
			}
		}
		if elapsed < 599*time.Second || elapsed > 620*time.Second {
			t.Fatal("actual interval", elapsed)
		}
		proof = append(proof, map[string]any{"mode": "actual-ten-minute-tick", "elapsed_seconds": elapsed.Seconds(), "after": tick, "progress": json.RawMessage(request("GET", "/api/v1/dashboard/schedule/scheduler_job/progress", nil, 200))})
	}
	if statePath := os.Getenv("PLUGIN_PROCESS_COMMON_CATEGORY_STATE"); statePath != "" {
		state := map[string]any{"origin": origin, "data_dir": dir, "source": manifest.PluginID, "clone": clone}
		if err = os.WriteFile(statePath, mustCleanSeedJSON(state), 0600); err != nil {
			t.Fatal(err)
		}
		done := false
		for until := time.Now().Add(30 * time.Minute); time.Now().Before(until); {
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
			t.Fatal("common/category browser gate timeout")
		}
	}
	writeOwnedEventsProcessEvidence(t, "common-category49-proof.json", mustCleanSeedJSON(proof))
	success("DELETE", "/api/plugins/"+clone, nil)
	success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
	request("GET", "/api/v1/plugin/"+clone+"/snapshot", nil, 404)
	request("GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", nil, 404)
	success("GET", "/api/v1/system/runscheduler?jobid=scheduler_job", nil)
	t.Log("actual category source cases, common manual/cold/optional actual10minute trigger and uninstall passed")
}
