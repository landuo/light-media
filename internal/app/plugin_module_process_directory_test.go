package app

import (
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

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func verifyOwnedProductionDirectoryCollector(t *testing.T, dir, origin string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	planner := os.Getenv("PLUGIN_PROCESS_DIRECTORY_PLAN") == "true"
	evidenceName := "directory39-cli.json"
	if planner {
		evidenceName = "directory41-cli.json"
	}
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

	full, e := sourceTMDBMediaInfo(json.RawMessage(`{"id":900039,"name":"DirectoryCollector","original_name":"DirectoryCollector","first_air_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "tv", "image.tmdb.org", "")
	if e != nil {
		t.Fatal(e)
	}
	cfg := map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_search_media", "tmdb_episodes": "module_episodes", "get_file_item": "module_storage_tree", "list_files": "module_storage_tree"}, "media_result": full, "episode_result": map[string]any{plugins.ModuleTupleKey: []any{map[string]any{"episode_number": 1, "season_number": 1}, map[string]any{"episode_number": 2, "season_number": 1}}}}
	if planner {
		cfg["modules"].(map[string]string)["transfer"] = "module_execute"
		cfg["result"] = nil
		cfg["episode_result"] = map[string]any{plugins.ModuleTupleKey: []any{map[string]any{"episode_number": 1, "season_number": 1}, map[string]any{"episode_number": 2, "season_number": 1}, map[string]any{"episode_number": 3, "season_number": 1}}}
	}
	set := func(id string, c any) { success("PUT", "/api/plugins/"+id, c) }
	setup := func(label string) (string, string, []string, map[string]any) {
		src, dst := filepath.Join(source, label), filepath.Join(target, label)
		nested := filepath.Join(src, "Nested")
		if os.MkdirAll(nested, 0750) != nil || os.MkdirAll(dst, 0750) != nil {
			t.Fatal("owned directories")
		}
		paths := []string{filepath.Join(nested, "DirectoryCollector.S01E02.mkv"), filepath.Join(src, "DirectoryCollector.S01E01.mkv")}
		for i, p := range append(append([]string{}, paths...), filepath.Join(src, "Unselected.S01E03.mkv")) {
			if e := os.WriteFile(p, []byte(fmt.Sprintf("owned collector file %d", i)), 0640); e != nil {
				t.Fatal(e)
			}
		}
		item := func(path, kind string) any {
			return map[string]any{"storage": "local", "path": path, "type": kind, "name": filepath.Base(path), "extension": strings.TrimPrefix(filepath.Ext(path), "."), "size": 22}
		}
		tree := map[string]any{"list_files:" + src: map[string]any{plugins.ModuleTupleKey: []any{item(nested, "dir"), item(paths[1], "file")}}, "list_files:" + nested: map[string]any{plugins.ModuleTupleKey: []any{item(paths[0], "file")}}}

		if planner {
			sub := filepath.Join(nested, "DirectoryCollector.S01E02.zh.srt")
			audio := filepath.Join(src, "DirectoryOrphan.S01E03.flac")
			for _, p := range []string{sub, audio} {
				if e := os.WriteFile(p, []byte("owned directory extra"), 0640); e != nil {
					t.Fatal(e)
				}
			}
			tree["list_files:"+src] = map[string]any{plugins.ModuleTupleKey: []any{item(audio, "file"), item(nested, "dir"), item(paths[1], "file")}}
			tree["list_files:"+nested] = map[string]any{plugins.ModuleTupleKey: []any{item(sub, "file"), item(paths[0], "file")}}
			paths = []string{paths[0], sub, paths[1], audio}
		}
		return src, dst, paths, tree
	}
	src, dst, paths, tree := setup("CLI")
	cloneCfg := map[string]any{"enabled": true, "modules": map[string]string{"list_files": "module_storage_tree"}, "storage_tree": tree}
	cfg["storage_tree"] = map[string]any{"get_file_item:" + src: false}
	set(manifest.PluginID, cfg)
	set(clone, cloneCfg)
	season := 1
	in := manualOrganizeRequest{Paths: []string{src}, MediaType: "tv", TMDBID: 900039, Season: &season, TargetPath: dst, Mode: "hardlink", Renaming: importFlag(false), Scraping: importFlag(false), Preview: true}
	submit := func() manualOrganizeOutput {
		var out struct{ Data manualOrganizeOutput }
		if json.Unmarshal(request("POST", "/api/files/organize", in, 200), &out) != nil {
			t.Fatal("actual organize")
		}
		return out.Data
	}
	failed := submit()
	if failed.Failed != 1 || len(failed.Items) != 1 || failed.Items[0].ErrorCode != "NO_MEDIA_FILES" {
		t.Fatal("source False must stop native scan", failed)
	}
	if read(manifest.PluginID).Calls["module_storage_tree"] != 1 || read(clone).Calls["module_storage_tree"] != 0 {
		t.Fatal("False terminated selected refresh")
	}
	cfg["storage_tree"] = map[string]any{}
	set(manifest.PluginID, cfg)
	preview := submit()
	if preview.Failed != 0 || len(preview.Items) != len(paths) {
		t.Fatal("actual recursive provider order", preview)
	}
	for i, p := range paths {
		if preview.Items[i].Source != p {
			t.Fatal("source directory planner order", preview, paths)
		}
		if _, e := os.Stat(filepath.Join(dst, filepath.Base(p))); !os.IsNotExist(e) {
			t.Fatal("preview publication", e)
		}
	}
	in.Preview = false
	actual := submit()
	if actual.Submitted != len(paths) || actual.Failed != 0 {
		t.Fatal("actual directory submit", actual)
	}
	for _, item := range actual.Items {
		done := false
		for until := time.Now().Add(15 * time.Second); time.Now().Before(until); {
			var job struct{ Data struct{ Job Job } }
			if json.Unmarshal(request("GET", "/api/jobs/"+item.JobID, nil, 200), &job) != nil {
				t.Fatal("job")
			}
			if job.Data.Job.State == "succeeded" {
				done = true
				break
			}
			if job.Data.Job.State == "failed" {
				t.Fatal("actual directory failed", job.Data.Job)
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !done {
			t.Fatal("directory job timeout")
		}
		input, e := os.Stat(item.Source)
		output, e2 := os.Stat(item.Target)
		if e != nil || e2 != nil || !os.SameFile(input, output) {
			t.Fatal("actual hardlink", e, e2)
		}
	}
	if _, e := os.Stat(filepath.Join(dst, "Unselected.S01E03.mkv")); !os.IsNotExist(e) {
		t.Fatal("provider excluded file was published", e)
	}
	before := [2]trace{read(manifest.PluginID), read(clone)}
	stop()
	start()
	cold := [2]trace{read(manifest.PluginID), read(clone)}
	for i, x := range cold {
		if x.PID == before[i].PID || x.Calls["module_storage_tree"] != 0 || x.Calls["module_search_media"] != 0 || x.Calls["module_execute"] != 0 {
			t.Fatal("cold replays collection", before, cold)
		}
	}
	for _, item := range actual.Items {
		input, e := os.Stat(item.Source)
		output, e2 := os.Stat(item.Target)
		if e != nil || e2 != nil || !os.SameFile(input, output) {
			t.Fatal("cold hardlink", e, e2)
		}
	}
	writeOwnedEventsProcessEvidence(t, evidenceName, mustCleanSeedJSON(map[string]any{"failed": failed, "preview": preview, "actual": actual, "before": before, "cold": cold, "paths": paths}))
	if statePath := os.Getenv("PLUGIN_PROCESS_DIRECTORY_STATE"); statePath != "" {
		src, dst, paths, tree = setup("UI")
		cfg["storage_tree"] = map[string]any{}
		cloneCfg["storage_tree"] = tree
		set(manifest.PluginID, cfg)
		set(clone, cloneCfg)
		state := map[string]any{"origin": origin, "data_dir": dir, "source": manifest.PluginID, "clone": clone, "source_path": src, "target": dst, "paths": paths, "source_config": cfg, "clone_config": cloneCfg}
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
			t.Fatal("directory UI gate timed out")
		}
	}
	success("DELETE", "/api/plugins/"+clone, nil)
	success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
	request("GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", nil, 404)
	t.Log("actual directory collector: source False, None-to-clone recursive listing, provider order, two real hardlinks/excluded source preserved, cold no replay, uninstall")
}
