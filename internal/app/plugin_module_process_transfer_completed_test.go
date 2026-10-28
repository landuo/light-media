package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/download"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func verifyOwnedProductionTransferCompleted(t *testing.T, dir, origin string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	worker := os.Getenv("OWNED_TRANSFER_MODULE_PROCESS_BINARY")
	payload, err := os.ReadFile(worker)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(worker, "--manifest").Output()
	var manifest plugins.Manifest
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != ownedModuleID {
		t.Fatal("actual module artifact", err)
	}
	digest := sha256.Sum256(payload)
	sum := hex.EncodeToString(digest[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: sum, ExecutableSHA256: sum, Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer repo.Close()
	index.Releases[0].Artifacts[0].URL = repo.URL + "/worker"
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": manifest.PluginID, "repo_url": repo.URL + "/index"})
	success("POST", "/api/plugins/"+manifest.PluginID+"/clone", map[string]any{"suffix": "Completion"})
	clone := manifest.PluginID + "completion"
	base := t.TempDir()
	source, target := filepath.Join(base, "source"), filepath.Join(base, "library")
	if os.MkdirAll(source, 0750) != nil || os.MkdirAll(target, 0750) != nil {
		t.Fatal("owned paths")
	}
	peer := &ownedCompletionPeer{Hash: strings.Repeat("c", 40), Path: source, Progress: 1, Tags: []string{"light-media", "kept"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/owned/journal" {
			peer.mu.Lock()
			defer peer.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"writes": peer.Writes, "tags": peer.Tags})
			return
		}
		peer.serveHTTP(w, r)
	}))
	defer server.Close()
	d := DownloaderConfig{Config: download.Config{ID: "owned-completion", Kind: "qb", BaseURL: server.URL}, Name: "Owned completion", DownloadPath: source}
	// Explicit synthetic downloaded history; all preview/execute/callback/tag
	// operations below enter the running production server and actual SDK.
	stop()
	seed, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	settings := seed.Settings()
	settings.AllowedRoots = []string{source}
	settings.Roots = []Root{{ID: "completion-root", Path: target, Kind: "movie"}}
	settings.Downloaders = []DownloaderConfig{d}
	settings.DefaultDownloader = d.ID
	if err = seed.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("Completion%d", i)
		path := filepath.Join(source, name+".2024.mkv")
		if err = os.WriteFile(path, []byte("owned completion bytes"), 0640); err != nil {
			t.Fatal(err)
		}
		intent := Intent{ID: fmt.Sprintf("intent_completion_%d", i), Hash: peer.Hash, State: "downloaded", DownloaderID: d.ID, DownloaderSnapshot: downloaderSnapshot(d), DownloadPath: source, Media: catalog.Media{ID: int64(900042 + i), MediaType: "movie", Title: name, Year: 2024}, RootID: "completion-root", CreatedAt: now(), UpdatedAt: now()}
		if err = seed.Store.Put(context.Background(), "intent", intent.ID, intent); err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			name := json.RawMessage(`"Original provider name"`)
			if i == 1 {
				name = json.RawMessage(`null`)
			}
			tx, e := seed.Store.DB.BeginTx(context.Background(), nil)
			if e != nil {
				t.Fatal(e)
			}
			if e = seed.saveDownloadContextTx(context.Background(), tx, downloadContextSnapshot{DownloadKind: "intent", DownloadID: intent.ID, Hash: intent.Hash, OriginalDownloader: name}); e != nil {
				_ = tx.Rollback()
				t.Fatal(e)
			}
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
		}
	}
	if err = seed.Close(); err != nil {
		t.Fatal(err)
	}
	start()
	read := func(id string) ownedModuleSnapshot {
		var v ownedModuleSnapshot
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/snapshot", nil, 200), &v) != nil || v.PID == 0 {
			t.Fatal("actual SDK snapshot")
		}
		return v
	}
	set := func(id string, cfg any) { success("PUT", "/api/plugins/"+id, cfg) }
	cloneCfg := map[string]any{"enabled": true, "modules": map[string]string{"transfer_completed": "module_storage_parent"}}
	configure := func(i int, sourceResult, cloneResult any, hold string) map[string]any {
		name := fmt.Sprintf("Completion%d", i)
		full, e := sourceTMDBMediaInfo(mustCleanSeedJSON(map[string]any{"id": 900042 + i, "title": name, "original_title": name, "release_date": "2024-01-01", "genres": []any{}, "credits": map[string]any{"cast": []any{}, "crew": []any{}}, "external_ids": map[string]any{}}), "movie", "image.tmdb.org", "")
		if e != nil {
			t.Fatal(e)
		}
		cfg := map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_search_media", "transfer": "module_execute", "transfer_completed": "module_storage_parent"}, "media_result": full, "result": nil, "storage_parent": sourceResult, "storage_root": source, "transfer_hold": hold}
		cloneCfg["storage_parent"] = cloneResult
		set(manifest.PluginID, cfg)
		set(clone, cloneCfg)
		return cfg
	}
	preview := func(i int) ImportPlan {
		var v struct{ Data ImportPlan }
		name := fmt.Sprintf("Completion%d.2024.mkv", i)
		in := ImportRequest{IntentID: fmt.Sprintf("intent_completion_%d", i), MediaType: "movie", TMDBID: int64(900042 + i), RootID: "completion-root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: filepath.Join(source, name)}}}
		if json.Unmarshal(request("POST", "/api/import/preview", in, 200), &v) != nil || len(v.Data.Files) != 1 || v.Data.Files[0].Status != "ready" {
			t.Fatal("actual download preview", v)
		}
		return v.Data
	}
	execute := func(p ImportPlan) Job {
		var v struct{ Data Job }
		if json.Unmarshal(request("POST", "/api/import/execute", map[string]any{"plan_id": p.ID}, 200), &v) != nil || v.Data.ID == "" {
			t.Fatal("actual execute", v)
		}
		return v.Data
	}
	await := func(j Job, want string) Job {
		for until := time.Now().Add(20 * time.Second); time.Now().Before(until); {
			var v struct{ Data struct{ Job Job } }
			if json.Unmarshal(request("GET", "/api/jobs/"+j.ID, nil, 200), &v) != nil {
				t.Fatal("job")
			}
			if v.Data.Job.State == want {
				return v.Data.Job
			}
			if v.Data.Job.State == "failed" {
				t.Fatal("actual job failed", v.Data.Job)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("job deadline", j.ID, want)
		return Job{}
	}
	actual := []any{}
	for i := 0; i < 3; i++ {
		var first, second any
		if i == 0 {
			first = false
		}
		if i == 1 {
			second = false
		}
		configure(i, first, second, "")
		before := []ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
		p := preview(i)
		if _, e := os.Stat(p.Files[0].Target); !os.IsNotExist(e) {
			t.Fatal("preview wrote target", e)
		}
		j := await(execute(p), "succeeded")
		// Job state commits before the ordinary post-commit completion hook.
		for until := time.Now().Add(10 * time.Second); time.Now().Before(until); {
			if read(manifest.PluginID).Calls["module_storage_parent"] > before[0].Calls["module_storage_parent"] {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		after := []ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
		var wantDownloader any = d.Name
		if i == 0 {
			wantDownloader = "Original provider name"
		}
		if i == 1 {
			wantDownloader = nil
		}
		for n, snapshot := range after {
			assertOwnedTransferDownloader(t, snapshot.TransferEvents[len(before[n].TransferEvents):], wantDownloader, peer.Hash)
		}
		wantClone := 0
		if i > 0 {
			wantClone = 1
		}
		if after[0].Calls["module_storage_parent"] != before[0].Calls["module_storage_parent"]+1 || after[1].Calls["module_storage_parent"] != before[1].Calls["module_storage_parent"]+wantClone {
			t.Fatal("actual source/clone dispatch", i, before, after)
		}
		for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
			peer.mu.Lock()
			count := len(peer.Writes)
			peer.mu.Unlock()
			if i < 2 || count == 1 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		peer.mu.Lock()
		writes := len(peer.Writes)
		peer.mu.Unlock()
		want := 0
		if i == 2 {
			want = 1
		}
		if writes != want {
			t.Fatal("actual tag writes", i, writes)
		}
		input, e := os.Stat(p.Files[0].Source)
		output, e2 := os.Stat(p.Files[0].Target)
		if e != nil || e2 != nil || !os.SameFile(input, output) {
			t.Fatal("real hardlink", e, e2)
		}
		actual = append(actual, map[string]any{"plan": p, "job": j, "before": before, "after": after, "tag_writes": writes})
	}
	before := []ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
	stop()
	start()
	cold := []ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
	for i, v := range cold {
		if v.PID == before[i].PID || v.Calls["module_storage_parent"] != 0 || v.Calls["module_execute"] != 0 {
			t.Fatal("cold replay", cold)
		}
	}
	writeOwnedEventsProcessEvidence(t, "completed42-cli.json", mustCleanSeedJSON(map[string]any{"actual": actual, "before": before, "cold": cold, "peer": server.URL}))
	if statePath := os.Getenv("PLUGIN_PROCESS_COMPLETED_STATE"); statePath != "" {
		hold := filepath.Join(source, "ui-release")
		cfg := configure(3, nil, nil, hold)
		p := preview(3)
		j := execute(p)
		entered := false
		for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
			if _, e := os.Stat(hold + ".entered"); e == nil {
				entered = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !entered {
			t.Fatal("actual SDK hold")
		}
		request("POST", "/api/jobs/"+j.ID+"/cancel", map[string]any{}, 200)
		if err = os.WriteFile(hold, []byte("release"), 0600); err != nil {
			t.Fatal(err)
		}
		await(j, "cancelled")
		time.Sleep(100 * time.Millisecond)
		if _, e := os.Stat(p.Files[0].Target); !os.IsNotExist(e) {
			t.Fatal("cancelled task published", e)
		}
		state := map[string]any{"origin": origin, "data_dir": dir, "source": manifest.PluginID, "clone": clone, "source_config": cfg, "clone_config": cloneCfg, "job": j, "plan": p, "peer": server.URL}
		if err = os.WriteFile(statePath, mustCleanSeedJSON(state), 0600); err != nil {
			t.Fatal(err)
		}
		done := false
		for until := time.Now().Add(40 * time.Minute); time.Now().Before(until); {
			if _, e := os.Stat(statePath + ".restart"); e == nil {
				stop()
				start()
				_ = os.Remove(statePath + ".restart")
				_ = os.WriteFile(statePath+".restart.done", mustCleanSeedJSON([]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}), 0600)
			}
			if _, e := os.Stat(statePath + ".done"); e == nil {
				done = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !done {
			t.Fatal("browser deadline")
		}
		final := await(j, "succeeded")
		input, e := os.Stat(p.Files[0].Source)
		output, e2 := os.Stat(p.Files[0].Target)
		if e != nil || e2 != nil || !os.SameFile(input, output) {
			t.Fatal("UI actual hardlink", e, e2)
		}
		peer.mu.Lock()
		writes := append([][]string{}, peer.Writes...)
		peer.mu.Unlock()
		if len(writes) != 2 {
			t.Fatal("UI tag effects", writes)
		}
		writeOwnedEventsProcessEvidence(t, "completed42-browser-final.json", mustCleanSeedJSON(map[string]any{"job": final, "plan": p, "writes": writes, "source": read(manifest.PluginID), "clone": read(clone)}))
	}
	success("DELETE", "/api/plugins/"+clone, nil)
	success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
	request("GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", nil, 404)
	t.Log("actual transfer completion: source/clone suppression, native tags, three hardlinks, cold no replay, uninstall")
}
