package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"light-media/internal/catalog"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func verifyOwnedProductionCatalogSeasons(t *testing.T, dir, origin string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
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

	success("POST", "/api/plugins/"+manifest.PluginID+"/clone", map[string]any{"suffix": "Seasons"})
	clone := manifest.PluginID + "seasons"
	stop()
	seed, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	for key, data := range map[string]any{"source-detail:tv:120": sourceMediaInformation{ProviderInfo: json.RawMessage(`{"seasons":[{"season_number":1,"name":"Native Season One","episode_count":3}]}`)}, "media:tv:120": catalog.Media{ID: 120, MediaType: "tv", Title: "Owned Seasons Browser", Year: 2026, Seasons: []catalog.SeasonSummary{{SeasonNumber: 1, Name: "Native Season One", EpisodeCount: 3}}}, "season:120:1": catalog.Season{SeriesID: 120, SeasonNumber: 1, Episodes: []catalog.Episode{}}} {
		if e = seed.Store.Put(context.Background(), "cache", key, map[string]any{"at": time.Now(), "data": data}); e != nil {
			t.Fatal(e)
		}
	}
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
	oracle := readOwnedSeasonsOracle(t, "testdata/standard-catalog-modules/seasons-source-oracle.json")
	var proof []any
	for _, c := range oracle.Cases {
		if c.Name == "native_dto_failure" || c.Name == "list_native_dto_failure" || c.Name == "native_special_and_missing" {
			continue
		}
		method := "module_execute"
		if c.Fail {
			method = "module_fail"
		}
		success("PUT", "/api/plugins/"+manifest.PluginID, map[string]any{"enabled": true, "modules": map[string]string{"async_tmdb_seasons": method}, "result": c.Plugin})
		success("PUT", "/api/plugins/"+clone, map[string]any{"enabled": true, "modules": map[string]string{"async_tmdb_seasons": "module_execute"}, "result": nil})
		before := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
		raw := request("GET", "/api/catalog/seasons?id=120", nil, c.Status)
		if c.Status == 200 {
			var x struct{ Data json.RawMessage }
			if json.Unmarshal(raw, &x) != nil || !originalBaseJSONEqual(x.Data, c.Output) {
				t.Fatal("fixed actual season DTO", c.Name, string(raw), string(c.Output))
			}
		}
		after := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
		count := 0
		for _, x := range c.Calls {
			if x.Kind == "clone" {
				count++
			}
		}
		if after[0].Calls[method]-before[0].Calls[method] != 1 || after[1].Calls["module_execute"]-before[1].Calls["module_execute"] != count {
			t.Fatal("source season dispatch", c.Name)
		}
		proof = append(proof, map[string]any{"case": c.Name, "status": c.Status, "result": json.RawMessage(raw), "before": before, "after": after})
	}
	primary := map[string]any{"source": "themoviedb", "tmdb_id": 120, "media_id": "120", "type": "电视剧", "title": "Owned Seasons Browser", "year": "2026", "number_of_seasons": 1, "poster_path": "/owned-seasons.png", "backdrop_path": "/owned-seasons.png", "logo_path": "/owned-seasons.png"}
	config := map[string]any{"enabled": true, "modules": map[string]string{"async_recognize_media": "module_search_media", "async_tmdb_seasons": "module_execute"}, "media_result": primary, "result": nil}
	if os.Getenv("PLUGIN_PROCESS_CATALOG_DETAIL_SEASONS") == "true" {
		primary["season_info"] = []any{map[string]any{"season_number": 0, "name": "Recognized Specials", "episode_count": 1}, map[string]any{"season_number": 2, "name": "Recognized Season Two", "episode_count": 8}, map[string]any{"season_number": 1, "name": "Recognized Season One", "episode_count": 3}}
		config["modules"].(map[string]string)["async_tmdb_episodes"] = "module_episodes"
		config["episode_result"] = false
	}
	success("PUT", "/api/plugins/"+manifest.PluginID, config)
	success("PUT", "/api/plugins/"+clone, map[string]any{"enabled": true, "modules": map[string]string{"async_tmdb_seasons": "module_execute"}, "result": []any{map[string]any{"season_number": 2, "name": "Clone Season Two", "episode_count": 8}, map[string]any{"season_number": 0, "name": "Clone Specials", "episode_count": 1}}})
	check := func() {
		t.Helper()
		raw := request("GET", "/api/catalog/seasons?id=120", nil, 200)
		var x struct {
			Data []struct {
				SeasonNumber int `json:"season_number"`
			}
		}
		if json.Unmarshal(raw, &x) != nil || len(x.Data) != 3 || x.Data[0].SeasonNumber != 2 || x.Data[1].SeasonNumber != 0 || x.Data[2].SeasonNumber != 1 {
			t.Fatal("clone/native order and special zero", string(raw))
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
	if statePath := os.Getenv("PLUGIN_PROCESS_CATALOG_SEASONS_STATE"); statePath != "" {
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
			t.Fatal("browser season gate timeout")
		}
	}
	writeOwnedEventsProcessEvidence(t, "catalog-seasons52-proof.json", mustCleanSeedJSON(proof))
	success("DELETE", "/api/plugins/"+clone, nil)
	success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
	request("GET", "/api/v1/plugin/"+clone+"/snapshot", nil, 404)
	request("GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", nil, 404)
	raw = request("GET", "/api/catalog/seasons?id=120", nil, 200)
	var x struct{ Data []map[string]any }
	if json.Unmarshal(raw, &x) != nil || len(x.Data) != 1 || x.Data[0]["name"] != "Native Season One" {
		t.Fatal("native seasons restore", string(raw))
	}
	t.Log("actual seasons9 fixed cases, clone 2/0/native1 order, cold zero replay, uninstall native restoration")
}
