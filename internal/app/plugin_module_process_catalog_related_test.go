package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func verifyOwnedProductionCatalogRelated(t *testing.T, dir, origin string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
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

	success("POST", "/api/plugins/"+manifest.PluginID+"/clone", map[string]any{"suffix": "Related"})
	clone := manifest.PluginID + "related"
	stop()
	seed, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	seedOwnedRelatedNative(t, seed)
	if e = seed.Close(); e != nil {
		t.Fatal(e)
	}
	start()
	read := func(id string) ownedModuleSnapshot {
		t.Helper()
		var x ownedModuleSnapshot
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/snapshot", nil, 200), &x) != nil || x.PID <= 0 {
			t.Fatal("actual worker")
		}
		return x
	}
	var proof []any
	oracle := readOwnedRelatedOracle(t, "testdata/standard-catalog-modules/related-source-oracle.json")
	for _, c := range oracle.Cases {
		method := "module_execute"
		if c.Fail {
			method = "module_fail"
		}
		success("PUT", "/api/plugins/"+manifest.PluginID, map[string]any{"enabled": true, "modules": map[string]string{c.Method: method}, "result": c.Plugin})
		success("PUT", "/api/plugins/"+clone, map[string]any{"enabled": true, "modules": map[string]string{c.Method: "module_execute"}, "result": nil})
		before := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
		raw := request("GET", "/api/catalog/detail/"+c.Part+"?"+url.Values{"source": {c.Source}, "kind": {c.Kind}, "source_id": {"120"}}.Encode(), nil, c.Status)
		if c.Status == 200 {
			var x struct{ Data []map[string]any }
			if json.Unmarshal(raw, &x) != nil || len(x.Data) != len(c.Output) {
				t.Fatal("fixed related count", c.Name, string(raw))
			}
			key := "name"
			if c.Part == "recommendations" {
				key = "title"
			}
			for i, w := range c.Output {
				if x.Data[i][key] != w[key] {
					t.Fatal("fixed related ordered output", c.Name)
				}
			}
		}
		after := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
		calls := 0
		for _, x := range c.Calls {
			if x.Kind == "clone" {
				calls++
			}
		}
		if after[0].Calls[method]-before[0].Calls[method] != 1 || after[1].Calls["module_execute"]-before[1].Calls["module_execute"] != calls {
			t.Fatal("fixed related dispatch", c.Name)
		}
		proof = append(proof, map[string]any{"source": c.Source, "kind": c.Kind, "part": c.Part, "case": c.Name, "status": c.Status, "result": json.RawMessage(raw), "before": before, "after": after})
	}
	primary := map[string]any{"source": "themoviedb", "tmdb_id": 120, "media_id": "120", "type": "电影", "title": "Owned Detail Browser", "year": "2026", "poster_path": "/owned-related.png", "backdrop_path": "/owned-related.png", "logo_path": "/owned-related.png"}
	config := map[string]any{"enabled": true, "modules": map[string]string{"async_recognize_media": "module_search_media", "async_tmdb_movie_credits": "module_page_size", "async_tmdb_movie_recommend": "module_execute"}, "media_result": primary, "result": nil, "page_size": nil}
	success("PUT", "/api/plugins/"+manifest.PluginID, config)
	success("PUT", "/api/plugins/"+clone, map[string]any{"enabled": true, "modules": map[string]string{"async_tmdb_movie_credits": "module_page_size", "async_tmdb_movie_recommend": "module_execute"}, "page_size": []any{map[string]any{"id": 122, "name": "Clone Actor"}}, "result": []any{map[string]any{"source": "themoviedb", "tmdb_id": 123, "media_id": "123", "type": "电影", "title": "Clone Recommended", "year": "2026"}}})
	request("GET", "/api/catalog/detail?kind=movie&id=120", nil, 200)
	check := func() {
		t.Helper()
		for _, part := range []string{"credits", "recommendations"} {
			raw := request("GET", "/api/catalog/detail/"+part+"?source=themoviedb&kind=movie&source_id=120", nil, 200)
			var x struct{ Data []map[string]any }
			if json.Unmarshal(raw, &x) != nil || len(x.Data) != 2 {
				t.Fatal("actual clone+native", string(raw))
			}
		}
	}
	check()
	before := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
	stop()
	start()
	after := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
	for i, x := range after {
		if x.PID == before[i].PID || x.Calls["module_execute"] != 0 || x.Calls["module_page_size"] != 0 || x.Calls["module_search_media"] != 0 {
			t.Fatal("cold read replay", before, after)
		}
	}
	proof = append(proof, map[string]any{"mode": "cold", "before": before, "after": after})
	check()
	if statePath := os.Getenv("PLUGIN_PROCESS_CATALOG_RELATED_STATE"); statePath != "" {
		if e = os.WriteFile(statePath, mustCleanSeedJSON(map[string]any{"origin": origin, "data_dir": dir, "source": manifest.PluginID, "clone": clone, "source_config": config}), 0600); e != nil {
			t.Fatal(e)
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
			t.Fatal("browser related gate timeout")
		}
	}
	writeOwnedEventsProcessEvidence(t, "catalog-related51-proof.json", mustCleanSeedJSON(proof))
	success("DELETE", "/api/plugins/"+clone, nil)
	success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
	request("GET", "/api/v1/plugin/"+clone+"/snapshot", nil, 404)
	request("GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", nil, 404)
	raw = request("GET", "/api/catalog/detail/credits?source=themoviedb&kind=movie&source_id=120", nil, 200)
	var x struct{ Data []map[string]any }
	if json.Unmarshal(raw, &x) != nil || len(x.Data) != 1 || x.Data[0]["name"] != "native actor" {
		t.Fatal("native restore after uninstall", string(raw))
	}
	t.Log("actual related16 source cases, source/clone ordered lists, independent cold and native restore after uninstall")
}
