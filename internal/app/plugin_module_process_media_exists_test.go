package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"light-media/internal/catalog"
	"light-media/internal/download"
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

func verifyOwnedProductionMediaExists(t *testing.T, dir, origin string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
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
	success("POST", "/api/plugins/"+manifest.PluginID+"/clone", map[string]any{"suffix": "Exists"})
	clone := manifest.PluginID + "exists"
	ctx := context.Background()
	stop()
	seed, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	media := catalog.Media{ID: 900038, MediaType: "tv", Title: "Owned existence", OriginalTitle: "Owned existence", Year: 2020}
	episodes := []catalog.Episode{}
	for n := 1; n <= 3; n++ {
		episodes = append(episodes, catalog.Episode{ID: int64(n), SeasonNumber: 1, EpisodeNumber: n, AirDate: "2020-01-01"})
	}
	for key, data := range map[string]any{"media:tv:900038": media, "season:900038:1": catalog.Season{SeriesID: media.ID, SeasonNumber: 1, Episodes: episodes}} {
		if err = seed.Store.Put(ctx, "cache", key, map[string]any{"at": time.Now(), "data": data}); err != nil {
			t.Fatal(err)
		}
	}
	settings := seed.Settings()
	settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-existence-qb", Kind: "qb", BaseURL: "http://127.0.0.1:1"}, Name: "Owned unused"}}
	if err = seed.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	subscriptionStatsInstallSettings(t, seed, "http://owned-unused.invalid", false)
	if err = seed.Close(); err != nil {
		t.Fatal(err)
	}
	start()
	full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":900038,"name":"Owned existence","original_name":"Owned existence","first_air_date":"2020-01-01","seasons":[{"season_number":1,"episode_count":3}],"alternative_titles":{"results":[]},"genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "tv", "image.tmdb.org", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"enabled": true, "modules": map[string]string{"async_recognize_media": "module_page_size", "recognize_media": "module_page_size", "media_exists": "module_execute"}, "page_size": full, "result": nil}
	cloneCfg := map[string]any{"enabled": true, "modules": map[string]string{"media_exists": "module_execute"}, "result": map[string]any{"type": "电视剧", "seasons": map[string]any{"1": []int{1, 3}}}}
	set := func(id string, c any) { success("PUT", "/api/plugins/"+id, c) }
	set(manifest.PluginID, cfg)
	set(clone, cloneCfg)
	type trace struct {
		PID    int
		Calls  map[string]int
		Inputs map[string]json.RawMessage
	}
	read := func(id string) trace {
		t.Helper()
		var x trace
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/snapshot", nil, 200), &x) != nil || x.PID == 0 {
			t.Fatal("real existence worker snapshot")
		}
		return x
	}
	var created struct{ Data Subscription }
	body := map[string]any{"media_type": "tv", "tmdb_id": media.ID, "name": media.Title, "year": "2020", "season": 1, "total_episodes": 3, "manual_total_episode": true, "target_scope": "season", "start_episode": 1, "downloader_id": "owned-existence-qb", "state": "active", "schedule": map[string]any{"mode": "manual"}}
	if json.Unmarshal(request("POST", "/api/subscriptions", body, 200), &created) != nil || created.Data.ID == "" {
		t.Fatal("actual subscription create")
	}
	list := func() Subscription {
		t.Helper()
		var listed struct {
			Data struct{ Items []Subscription }
		}
		if json.Unmarshal(request("GET", "/api/subscriptions", nil, 200), &listed) != nil {
			t.Fatal("subscriptions")
		}
		for _, s := range listed.Data.Items {
			if s.ID == created.Data.ID {
				return s
			}
		}
		t.Fatal("actual subscription missing")
		return Subscription{}
	}
	check := func(want string) Job {
		t.Helper()
		var queued struct{ Data Job }
		if json.Unmarshal(request("POST", "/api/subscriptions/"+created.Data.ID+"/check", map[string]any{}, 200), &queued) != nil || queued.Data.ID == "" {
			t.Fatal("check job missing")
		}
		for until := time.Now().Add(10 * time.Second); time.Now().Before(until); {
			var current struct{ Data struct{ Job Job } }
			if json.Unmarshal(request("GET", "/api/jobs/"+queued.Data.ID, nil, 200), &current) != nil {
				t.Fatal("job readback")
			}
			if current.Data.Job.State == want {
				return current.Data.Job
			}
			if current.Data.Job.State == "failed" && want != "failed" {
				t.Fatal("actual check failed", current.Data.Job.ErrorCode, current.Data.Job.ErrorMessage)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("check timeout")
		return Job{}
	}
	var proof []any
	for _, sample := range []struct {
		name             string
		result           any
		lack, cloneCalls int
	}{{"clone-partial", nil, 1, 1}, {"source-false", false, 3, 0}, {"source-empty-list", []any{}, 3, 1}, {"restored-clone", nil, 1, 1}} {
		cfg["result"] = sample.result
		set(manifest.PluginID, cfg)
		before := [2]trace{read(manifest.PluginID), read(clone)}
		job := check("succeeded")
		sub := list()
		after := [2]trace{read(manifest.PluginID), read(clone)}
		if sub.LackEpisode == nil || *sub.LackEpisode != sample.lack || after[0].Calls["module_execute"]-before[0].Calls["module_execute"] != 1 || after[1].Calls["module_execute"]-before[1].Calls["module_execute"] != sample.cloneCalls {
			t.Fatal("fixed existence scope/short circuit mismatch", sample.name, sub.LackEpisode, before, after)
		}
		proof = append(proof, map[string]any{"mode": sample.name, "subscription": sub, "job": job, "before": before, "after": after})
	}
	cfg["result"] = []any{false}
	set(manifest.PluginID, cfg)
	listFailure := check("failed")
	if listFailure.ErrorCode != "PLUGIN_MEDIA_EXISTS_INVALID" {
		t.Fatal("truthy list must fail at TV existence caller", listFailure)
	}
	proof = append(proof, map[string]any{"mode": "source-truthy-list", "job": listFailure})
	cfg["result"] = nil
	set(manifest.PluginID, cfg)
	cloneCfg["result"] = map[string]any{"seasons": nil}
	set(clone, cloneCfg)
	failed := check("failed")
	if failed.ErrorCode != "PLUGIN_MEDIA_EXISTS_INVALID" {
		t.Fatal("malformed existence silently accepted", failed.ErrorCode)
	}
	proof = append(proof, map[string]any{"mode": "invalid-dto", "job": failed, "subscription": list()})
	cloneCfg["result"] = map[string]any{"type": "电视剧", "seasons": map[string]any{"1": []int{1, 3}}}
	set(clone, cloneCfg)
	check("succeeded")
	before := [2]trace{read(manifest.PluginID), read(clone)}
	previous := list()
	stop()
	start()
	after := [2]trace{read(manifest.PluginID), read(clone)}
	restored := list()
	if previous.LackEpisode == nil || restored.LackEpisode == nil || *previous.LackEpisode != *restored.LackEpisode || previous.ID != restored.ID {
		t.Fatal("cold progress changed")
	}
	for i, x := range after {
		if x.PID == before[i].PID || x.Calls["module_execute"] != 0 {
			t.Fatal("cold replayed query", before, after)
		}
	}
	proof = append(proof, map[string]any{"mode": "cold", "before": before, "after": after, "subscription": restored})
	// A fresh complete observation must reach the existing source finish path
	// (check, history, deletion, notification/event), not merely stop search.
	cloneCfg["result"] = map[string]any{"type": "电视剧", "seasons": map[string]any{"1": []int{1, 2, 3}}}
	set(clone, cloneCfg)
	completedID := created.Data.ID
	check("succeeded")
	completed := false
	for until := time.Now().Add(12 * time.Second); time.Now().Before(until); {
		var jobs struct{ Data []Job }
		if json.Unmarshal(request("GET", "/api/jobs", nil, 200), &jobs) != nil {
			t.Fatal("completion jobs")
		}
		for _, j := range jobs.Data {
			if j.Kind == "subscription_completion" && j.State == "succeeded" {
				completed = true
				proof = append(proof, map[string]any{"mode": "actual-completion", "subscription_id": completedID, "job": j})
			}
			if (j.Kind == "subscription_completion" || j.Kind == "subscription_completion_prepare") && j.State == "failed" {
				t.Fatal("actual completion failed", j.ErrorCode, j.ErrorMessage)
			}
		}
		if completed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !completed {
		t.Fatal("actual completion did not finish")
	}
	// A second user-created subscription is left active for the page checks.
	cloneCfg["result"] = map[string]any{"type": "电视剧", "seasons": map[string]any{"1": []int{1, 3}}}
	set(clone, cloneCfg)
	if json.Unmarshal(request("POST", "/api/subscriptions", body, 200), &created) != nil || created.Data.ID == "" || created.Data.ID == completedID {
		t.Fatal("actual recreate after completion")
	}
	check("succeeded")
	if statePath := os.Getenv("PLUGIN_PROCESS_MEDIA_EXISTS_STATE"); statePath != "" {
		state := map[string]any{"origin": origin, "data_dir": dir, "source": manifest.PluginID, "clone": clone, "subscription": created.Data.ID, "source_config": cfg, "clone_config": cloneCfg}
		if err = os.WriteFile(statePath, mustCleanSeedJSON(state), 0600); err != nil {
			t.Fatal(err)
		}
		done := false
		for until := time.Now().Add(40 * time.Minute); time.Now().Before(until); {
			if _, e := os.Stat(statePath + ".restart"); e == nil {
				stop()
				start()
				os.Remove(statePath + ".restart")
				os.WriteFile(statePath+".restart.done", mustCleanSeedJSON([]trace{read(manifest.PluginID), read(clone)}), 0600)
			}
			if _, e := os.Stat(statePath + ".done"); e == nil {
				done = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !done {
			t.Fatal("media exists browser gate timed out")
		}
	}
	stop()
	inspect, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	var assets, slots, intents, histories, completedEvents int
	inspect.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='asset'`).Scan(&assets)
	inspect.Store.DB.QueryRow(`SELECT count(*) FROM asset_slots`).Scan(&slots)
	inspect.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='intent'`).Scan(&intents)
	inspect.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='subscription_history'`).Scan(&histories)
	inspect.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='source_subscribe_complete'`).Scan(&completedEvents)
	if histories != 1 || completedEvents != 1 {
		t.Fatal("actual completion history/event missing or duplicated", histories, completedEvents)
	}
	if assets != 0 || slots != 0 || intents != 0 {
		t.Fatal("existence observation fabricated local ownership", assets, slots, intents)
	}
	if err = inspect.Close(); err != nil {
		t.Fatal(err)
	}
	start()
	proof = append(proof, map[string]any{"mode": "actual-local-state", "assets": assets, "slots": slots, "intents": intents, "completion_histories": histories, "complete_events": completedEvents})
	evidence := os.Getenv("PLUGIN_PROCESS_EVENTS36_EVIDENCE_DIR")
	if evidence != "" {
		os.MkdirAll(evidence, 0750)
		if err = os.WriteFile(filepath.Join(evidence, "mediaexists38-cli.json"), mustCleanSeedJSON(proof), 0600); err != nil {
			t.Fatal(err)
		}
	}
	request("DELETE", "/api/subscriptions/"+created.Data.ID, nil, 200)
	success("DELETE", "/api/plugins/"+clone, nil)
	success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
	request("GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", nil, 404)
	t.Log("actual media_exists: ordinary check scope, source False short circuit, clone fallback, malformed failure, cold progress/no queries, zero fabricated inventory and uninstall")
}
