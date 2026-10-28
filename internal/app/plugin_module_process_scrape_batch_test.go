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
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func verifyOwnedProductionScrapeBatch(t *testing.T, dir, origin string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
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
	success("POST", "/api/plugins/"+manifest.PluginID+"/clone", map[string]any{"suffix": "Transfer"})
	clone := manifest.PluginID + "transfer"
	base := t.TempDir()
	source, target := filepath.Join(base, "source"), filepath.Join(base, "library")
	if os.MkdirAll(source, 0750) != nil || os.MkdirAll(target, 0750) != nil {
		t.Fatal("owned module directories")
	}
	var settings struct{ Data Settings }
	if json.Unmarshal(request("GET", "/api/settings", nil, 200), &settings) != nil {
		t.Fatal("actual settings")
	}
	settings.Data.AllowedRoots = []string{source}
	settings.Data.Roots = []Root{{ID: "owned-module-transfer-root", Kind: "mixed", Path: target}}
	request("PUT", "/api/settings", settings.Data, 200)
	type trace struct {
		PID    int            `json:"pid"`
		Calls  map[string]int `json:"calls"`
		Events []struct {
			Type string
			Data json.RawMessage
		} `json:"transfer_events"`
	}
	read := func(id string) trace {
		t.Helper()
		var out trace
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/snapshot", nil, 200), &out) != nil || out.PID == 0 {
			t.Fatal("actual SDK snapshot")
		}
		return out
	}
	cfgFor := func(id int64, title string) map[string]any {
		full, e := sourceTMDBMediaInfo(mustCleanSeedJSON(map[string]any{"id": id, "name": title, "original_name": title, "first_air_date": "2024-01-01", "genres": []any{}, "credits": map[string]any{"cast": []any{}, "crew": []any{}}, "external_ids": map[string]any{}}), "tv", "image.tmdb.org", "")
		if e != nil {
			t.Fatal(e)
		}
		episodes := []any{}
		for i := 1; i <= 3; i++ {
			episodes = append(episodes, map[string]any{"episode_number": i, "season_number": 1, "name": fmt.Sprintf("Episode %d", i), "air_date": "2024-01-01"})
		}
		return map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_search_media", "tmdb_episodes": "module_episodes", "metadata_nfo": "module_metadata_nfo", "transfer": "module_execute"}, "media_result": full, "episode_result": map[string]any{plugins.ModuleTupleKey: episodes}, "nfo_result": "<episodedetails><title>Owned batch NFO</title></episodedetails>", "result": nil}
	}
	set := func(id string, cfg any) { success("PUT", "/api/plugins/"+id, cfg) }
	events := func(x trace) []json.RawMessage {
		var out []json.RawMessage
		for _, e := range x.Events {
			if e.Type == "metadata.scrape" {
				out = append(out, e.Data)
			}
		}
		return out
	}
	wait := func(jobID string, want string) Job {
		t.Helper()
		var data struct{ Data struct{ Job Job } }
		for until := time.Now().Add(15 * time.Second); time.Now().Before(until); {
			if json.Unmarshal(request("GET", "/api/jobs/"+jobID, nil, 200), &data) != nil {
				t.Fatal("job")
			}
			if data.Data.Job.State == want {
				return data.Data.Job
			}
			if data.Data.Job.State == "failed" && want != "failed" {
				t.Fatal("real job failed", data.Data.Job.ID, data.Data.Job.ErrorCode, data.Data.Job.ErrorMessage)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("job timeout", data)
		return Job{}
	}
	var proof []any
	for index, mode := range []string{"success", "failed-last"} {
		id := int64(90003600 + index)
		name := "ScrapeBatch" + mode
		src, dst := filepath.Join(source, name), filepath.Join(target, name)
		if os.MkdirAll(src, 0750) != nil || os.MkdirAll(dst, 0750) != nil {
			t.Fatal("batch dirs")
		}
		paths := []string{}
		for i := 1; i <= 3; i++ {
			path := filepath.Join(src, fmt.Sprintf("%s.S01E%02d.mkv", name, i))
			if e := os.WriteFile(path, []byte(fmt.Sprintf("owned batch episode %d", i)), 0640); e != nil {
				t.Fatal(e)
			}
			paths = append(paths, path)
		}
		cfg := cfgFor(id, name)
		if mode == "failed-last" {
			cfg["transfer_results"] = map[string]any{paths[2]: map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": false, "message": "owned last task rejection"}}}
		}
		set(manifest.PluginID, cfg)
		set(clone, map[string]any{"enabled": true, "modules": map[string]string{"transfer": "module_execute"}, "result": nil})
		baseline := [2]int{len(events(read(manifest.PluginID))), len(events(read(clone)))}
		season := 1
		in := manualOrganizeRequest{Paths: []string{src}, MediaType: "tv", TMDBID: id, Season: &season, TargetPath: dst, Mode: "hardlink", Renaming: importFlag(false), Scraping: importFlag(true)}
		var submitted struct{ Data manualOrganizeOutput }
		if json.Unmarshal(request("POST", "/api/files/organize", in, 200), &submitted) != nil || submitted.Data.Submitted != 3 {
			t.Fatal("real batch submit", submitted)
		}
		for i, item := range submitted.Data.Items {
			want := "succeeded"
			if i == 2 && mode == "failed-last" {
				want = "failed"
			}
			wait(item.JobID, want)
		}
		wantFiles := 3
		if mode == "failed-last" {
			wantFiles = 2
		}
		var traces [2]trace
		for until := time.Now().Add(10 * time.Second); time.Now().Before(until); {
			traces = [2]trace{read(manifest.PluginID), read(clone)}
			if len(events(traces[0])) == baseline[0]+1 && len(events(traces[1])) == baseline[1]+1 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		for i, x := range traces {
			e := events(x)
			if len(e) != baseline[i]+1 {
				t.Fatal("one actual aggregate per instance", len(e), baseline[i])
			}
			e = e[baseline[i]:]
			var payload struct {
				Files []string `json:"file_list"`
			}
			if json.Unmarshal(e[0], &payload) != nil || len(payload.Files) != wantFiles {
				t.Fatal("actual aggregate paths", string(e[0]))
			}
		}
		for i, path := range paths {
			dest := filepath.Join(dst, filepath.Base(path))
			srcinfo, se := os.Stat(path)
			dstinfo, de := os.Stat(dest)
			if i >= wantFiles {
				if !os.IsNotExist(de) {
					t.Fatal("failed task published")
				}
				continue
			}
			if se != nil || de != nil || !os.SameFile(srcinfo, dstinfo) {
				t.Fatal("actual hardlink", se, de)
			}
			nfo := dest[:len(dest)-4] + ".nfo"
			found := false
			for until := time.Now().Add(10 * time.Second); time.Now().Before(until); {
				if bytes, e := os.ReadFile(nfo); e == nil && len(bytes) > 0 {
					found = true
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !found {
				entries, _ := os.ReadDir(dst)
				names := []string{}
				for _, entry := range entries {
					names = append(names, entry.Name())
				}
				writeOwnedEventsProcessEvidence(t, "scrape-batch36-nfo-debug.json", mustCleanSeedJSON(map[string]any{"source": json.RawMessage(request("GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", nil, 200)), "clone": json.RawMessage(request("GET", "/api/v1/plugin/"+clone+"/snapshot", nil, 200)), "files": names, "jobs": json.RawMessage(request("GET", "/api/jobs", nil, 200)), "settings": settings.Data}))
				t.Fatal("actual aggregate NFO absent", nfo)
			}
		}
		proof = append(proof, map[string]any{"mode": mode, "submitted": submitted.Data, "source": traces[0], "clone": traces[1], "paths": paths, "target": dst})
		if mode == "success" && os.Getenv("PLUGIN_PROCESS_SCRAPE_DEFERRED_FIXTURE") == "true" {
			oldPath := filepath.Join(dst, filepath.Base(paths[0]))
			newDir := filepath.Join(target, "ScrapeDeferred37")
			if e := os.MkdirAll(newDir, 0750); e != nil {
				t.Fatal(e)
			}
			baseline = [2]int{len(events(read(manifest.PluginID))), len(events(read(clone)))}
			move := manualOrganizeRequest{Paths: []string{oldPath}, TargetPath: newDir, MediaType: "tv", TMDBID: id, Season: &season, Mode: "hardlink", Scraping: importFlag(true), Renaming: importFlag(false), Preview: true}
			var preview struct{ Data manualOrganizeOutput }
			if json.Unmarshal(request("POST", "/api/files/organize", move, 200), &preview) != nil || preview.Data.Total != 1 || preview.Data.Failed != 0 {
				t.Fatal("managed target preview", preview)
			}
			if _, e := os.Stat(oldPath); e != nil {
				t.Fatal("preview removed owned input", e)
			}
			move.Preview = false
			cfg["result"] = false
			set(manifest.PluginID, cfg)
			var rejected struct{ Data manualOrganizeOutput }
			if json.Unmarshal(request("POST", "/api/files/organize", move, 200), &rejected) != nil || rejected.Data.Submitted != 1 {
				t.Fatal("actual rejected reorganization submission", rejected)
			}
			failed := wait(rejected.Data.Items[0].JobID, "failed")
			if _, e := os.Stat(oldPath); e != nil {
				t.Fatal("rejected provider removed old target", e)
			}
			if len(events(read(manifest.PluginID))) != baseline[0] || len(events(read(clone))) != baseline[1] {
				t.Fatal("rejected transfer emitted scrape success")
			}
			cfg["result"] = nil
			set(manifest.PluginID, cfg)
			var next struct{ Data manualOrganizeOutput }
			if json.Unmarshal(request("POST", "/api/files/organize", move, 200), &next) != nil || next.Data.Submitted != 1 {
				t.Fatal("actual managed target reorganization", next)
			}
			wait(next.Data.Items[0].JobID, "succeeded")
			newPath := filepath.Join(newDir, filepath.Base(oldPath))
			for until := time.Now().Add(10 * time.Second); time.Now().Before(until); {
				traces = [2]trace{read(manifest.PluginID), read(clone)}
				if len(events(traces[0])) == baseline[0]+1 && len(events(traces[1])) == baseline[1]+1 {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			for i, x := range traces {
				es := events(x)
				if len(es) != baseline[i]+1 {
					t.Fatal("actual deferred event missing/duplicate", len(es), baseline[i])
				}
				var event struct {
					Files []string `json:"file_list"`
				}
				if json.Unmarshal(es[len(es)-1], &event) != nil || len(event.Files) != 1 || event.Files[0] != newPath {
					t.Fatal("actual deferred target", string(es[len(es)-1]))
				}
			}
			if _, e := os.Stat(oldPath); !os.IsNotExist(e) {
				t.Fatal("old managed target remained", e)
			}
			for _, path := range paths {
				if _, e := os.Stat(path); e != nil {
					t.Fatal("download source touched", e)
				}
			}
			for _, path := range paths[1:] {
				if _, e := os.Stat(filepath.Join(dst, filepath.Base(path))); e != nil {
					t.Fatal("unselected target touched", e)
				}
			}
			nfo := newPath[:len(newPath)-4] + ".nfo"
			for until := time.Now().Add(10 * time.Second); time.Now().Before(until); {
				if raw, e := os.ReadFile(nfo); e == nil && len(raw) > 0 {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if raw, e := os.ReadFile(nfo); e != nil || len(raw) == 0 {
				t.Fatal("deferred actual NFO", e)
			}
			proof = append(proof, map[string]any{"mode": "deferred-managed-file", "preview": preview, "rejected": rejected, "failed_job": failed, "submitted": next, "old_path": oldPath, "new_path": newPath, "source": traces[0], "clone": traces[1], "jobs": json.RawMessage(request("GET", "/api/jobs", nil, 200))})
		}
	}
	before := [2]trace{read(manifest.PluginID), read(clone)}
	stop()
	start()
	after := [2]trace{read(manifest.PluginID), read(clone)}
	for i, x := range after {
		if x.PID == before[i].PID || len(x.Events) > 0 || x.Calls["module_execute"] != 0 {
			t.Fatal("cold batch replay", before, after)
		}
	}
	proof = append(proof, map[string]any{"mode": "cold", "before": before, "after": after})
	if os.Getenv("PLUGIN_PROCESS_SCRAPE_LEGACY_FIXTURE") == "true" {
		// Controlled persisted v2 receipt, prepared only while the actual server
		// is stopped. The restarted production main performs cleanup and scrape.
		stop()
		a, e := New(Options{DataDir: dir})
		if e != nil {
			t.Fatal(e)
		}
		ctx := context.Background()
		oldPath := filepath.Join(target, "ScrapeBatchfailed-last", "ScrapeBatchfailed-last.S01E01.mkv")
		var old Asset
		if e = a.assetAtPath(ctx, oldPath, &old); e != nil {
			a.Close()
			t.Fatal(e)
		}
		var oldVideo managedFile
		if e = a.Store.Get(ctx, "managed_file", tokenHash(oldPath), &oldVideo); e != nil {
			a.Close()
			t.Fatal(e)
		}
		newDir := filepath.Join(target, "LegacyQuality37")
		newPath := filepath.Join(newDir, filepath.Base(oldPath))
		if e = os.MkdirAll(newDir, 0750); e != nil {
			a.Close()
			t.Fatal(e)
		}
		if e = os.Link(oldPath, newPath); e != nil {
			a.Close()
			t.Fatal(e)
		}
		replacement := old
		replacement.ID = id("asset_")
		replacement.Path = newPath
		replacement.ScrapeState = "pending"
		newVideo := oldVideo
		newVideo.Path = newPath
		newVideo.AssetID = replacement.ID
		newVideo.Snapshot, e = snapshotPath(newPath)
		if e != nil {
			a.Close()
			t.Fatal(e)
		}
		var event map[string]any
		rows, e := a.Store.DB.Query(`SELECT payload FROM records WHERE kind='original_scrape_batch'`)
		if e != nil {
			a.Close()
			t.Fatal(e)
		}
		for rows.Next() {
			var raw []byte
			var batch originalScrapeBatch
			if e = rows.Scan(&raw); e != nil {
				t.Fatal(e)
			}
			if e = json.Unmarshal(raw, &batch); e != nil {
				t.Fatal(e)
			}
			for _, entry := range batch.Targets {
				var v map[string]any
				if e = json.Unmarshal(entry.Event, &v); e != nil {
					t.Fatal(e)
				}
				for _, file := range v["file_list"].([]any) {
					if file == oldPath {
						event = v
					}
				}
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil || event == nil {
			a.Close()
			t.Fatal("persisted source event", e)
		}
		item := event["fileitem"].(map[string]any)
		item["path"] = newDir + "/"
		item["name"] = filepath.Base(newDir)
		item["basename"] = filepath.Base(newDir)
		event["file_list"] = []string{newPath}
		oldRoot, e := a.importRoot(old.RootID)
		if e != nil {
			a.Close()
			t.Fatal(e)
		}
		root, e := a.importRoot("owned-module-transfer-root")
		if e != nil {
			a.Close()
			t.Fatal(e)
		}
		replacement.RootID = root.ID
		newVideo.RootID = root.ID
		key, batchID := id("legacy_quality_"), id("scrape_batch_")
		legacy := qualityCleanup{Version: 2, ID: key, NewAsset: replacement.ID, NewPath: newPath, NewRoot: root.ID, NewSnapshot: newVideo.Snapshot, NewFiles: []managedFile{newVideo}, OldAssets: []string{old.ID}, Files: []managedFile{oldVideo}, RootPaths: map[string]string{old.RootID: oldRoot.Path, root.ID: root.Path}, State: "pending"}
		tx, e := a.Store.DB.BeginTx(ctx, nil)
		if e != nil {
			a.Close()
			t.Fatal(e)
		}
		old.State = "superseded"
		for _, v := range []struct {
			kind, key string
			value     any
		}{{"asset", old.ID, old}, {"asset", replacement.ID, replacement}, {"managed_file", tokenHash(newPath), newVideo}, {"original_scrape_batch", batchID, originalScrapeBatch{ID: batchID, Closed: true}}} {
			if e = txPut(ctx, tx, v.kind, v.key, v.value); e != nil {
				tx.Rollback()
				a.Close()
				t.Fatal(e)
			}
		}
		if _, e = tx.ExecContext(ctx, `UPDATE asset_slots SET asset_id=? WHERE asset_id=?`, replacement.ID, old.ID); e != nil {
			tx.Rollback()
			a.Close()
			t.Fatal(e)
		}
		if e = a.writeQualitySealedTx(ctx, tx, "subscription_quality_cleanup", key, legacy); e != nil {
			tx.Rollback()
			a.Close()
			t.Fatal(e)
		}
		cleanup, e := enqueueTx(ctx, tx, "quality_cleanup", map[string]any{"id": key, "original_scrape_batch": batchID, "original_scrape_deferred": true, "original_metadata_event": event}, batchID)
		if e != nil {
			tx.Rollback()
			a.Close()
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			a.Close()
			t.Fatal(e)
		}
		if e = a.Close(); e != nil {
			t.Fatal(e)
		}
		start()
		wait(cleanup.ID, "succeeded")
		var actual [2]trace
		for until := time.Now().Add(10 * time.Second); time.Now().Before(until); {
			actual = [2]trace{read(manifest.PluginID), read(clone)}
			if len(events(actual[0])) == 1 && len(events(actual[1])) == 1 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		for _, x := range actual {
			es := events(x)
			if len(es) != 1 {
				t.Fatal("actual main legacy ordinary event", len(es))
			}
			var observed struct {
				Files []string `json:"file_list"`
			}
			if json.Unmarshal(es[0], &observed) != nil || len(observed.Files) != 1 || observed.Files[0] != newPath {
				t.Fatal("actual legacy target", string(es[0]))
			}
		}
		if _, e = os.Stat(oldPath); !os.IsNotExist(e) {
			t.Fatal("actual main legacy cleanup", e)
		}
		nfo := newPath[:len(newPath)-4] + ".nfo"
		for until := time.Now().Add(10 * time.Second); time.Now().Before(until); {
			if bytes, e := os.ReadFile(nfo); e == nil && len(bytes) > 0 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if bytes, e := os.ReadFile(nfo); e != nil || len(bytes) == 0 {
			t.Fatal("actual legacy core NFO", e)
		}
		stop()
		start()
		cold := [2]trace{read(manifest.PluginID), read(clone)}
		for i, x := range cold {
			if x.PID == actual[i].PID || len(x.Events) != 0 || x.Calls["module_metadata_nfo"] != 0 {
				t.Fatal("legacy cold replay", cold)
			}
		}
		proof = append(proof, map[string]any{"mode": "legacy-quality-v2-cold-recovery", "setup": "synthetic persisted v2 receipt prepared with actual owned old/new files while main stopped", "cleanup_job": cleanup, "old_path": oldPath, "new_path": newPath, "actual": actual, "cold": cold})
	}
	writeOwnedEventsProcessEvidence(t, "scrape-batch36-cli.json", mustCleanSeedJSON(proof))
	if statePath := os.Getenv("PLUGIN_PROCESS_SCRAPE_BATCH_STATE"); statePath != "" {
		src, dst := filepath.Join(source, "ScrapeBatchUI"), filepath.Join(target, "ScrapeBatchUI")
		if os.MkdirAll(src, 0750) != nil || os.MkdirAll(dst, 0750) != nil {
			t.Fatal("UI dirs")
		}
		paths := []string{}
		for i := 1; i <= 3; i++ {
			path := filepath.Join(src, fmt.Sprintf("ScrapeBatchUI.S01E%02d.mkv", i))
			if e := os.WriteFile(path, []byte(fmt.Sprintf("owned UI batch episode %d", i)), 0640); e != nil {
				t.Fatal(e)
			}
			paths = append(paths, path)
		}
		cfg := cfgFor(90003699, "ScrapeBatchUI")
		if os.Getenv("PLUGIN_PROCESS_SCRAPE_DEFERRED_FIXTURE") == "true" {
			src = filepath.Join(target, "ScrapeBatchsuccess")
			paths = []string{filepath.Join(src, "ScrapeBatchsuccess.S01E02.mkv")}
			cfg = cfgFor(90003600, "ScrapeBatchsuccess")
		}
		set(manifest.PluginID, cfg)
		set(clone, map[string]any{"enabled": true, "modules": map[string]string{"transfer": "module_execute"}, "result": nil})
		state := map[string]any{"origin": origin, "data_dir": dir, "source": manifest.PluginID, "clone": clone, "source_path": src, "target": dst, "paths": paths, "source_config": cfg}
		if e := os.WriteFile(statePath, mustCleanSeedJSON(state), 0600); e != nil {
			t.Fatal(e)
		}
		done := false
		for until := time.Now().Add(40 * time.Minute); time.Now().Before(until); {
			if _, e := os.Stat(statePath + ".restart"); e == nil {
				stop()
				start()
				_ = os.Remove(statePath + ".restart")
				_ = os.WriteFile(statePath+".restart.done", mustCleanSeedJSON([]trace{read(manifest.PluginID), read(clone)}), 0600)
			}
			if _, e := os.Stat(statePath + ".done"); e == nil {
				done = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !done {
			t.Fatal("batch UI gate timed out")
		}
	}
	success("DELETE", "/api/plugins/"+clone, nil)
	success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
	request("GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", nil, 404)
	t.Log("actual three-file metadata batch: source/clone aggregation, failed last task, native NFO publication, cold no replay and uninstall")
}
