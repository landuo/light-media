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
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func verifyOwnedProductionBluray(t *testing.T, dir, origin string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	scraping := os.Getenv("PLUGIN_PROCESS_BLURAY_SCRAPE_FIXTURE") == "true"
	metadataText := "<movie><title>Owned Blu-ray directory metadata</title></movie>"
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
	set := func(id string, cfg any) { success("PUT", "/api/plugins/"+id, cfg) }
	setup := func(label, mode string) (manualOrganizeRequest, string, map[string]any, map[string]any) {
		path := filepath.Join(source, label+".2024.BluRay")
		for name, data := range map[string]string{"BDMV/STREAM/00001.m2ts": "owned bluray stream", "BDMV/index.bdmv": "owned bluray index"} {
			p := filepath.Join(path, name)
			if os.MkdirAll(filepath.Dir(p), 0750) != nil || os.WriteFile(p, []byte(data), 0640) != nil {
				t.Fatal("owned disc files")
			}
		}
		dest := filepath.Join(target, label)
		if err := os.MkdirAll(dest, 0750); err != nil {
			t.Fatal(err)
		}
		full, e := sourceTMDBMediaInfo(mustCleanSeedJSON(map[string]any{"id": 900044 + len(label), "title": label, "original_title": label, "release_date": "2024-01-01", "genres": []any{}, "credits": map[string]any{"cast": []any{}, "crew": []any{}}, "external_ids": map[string]any{}}), "movie", "image.tmdb.org", "")
		if e != nil {
			t.Fatal(e)
		}
		cfg := map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_search_media", "transfer": "module_execute"}, "media_result": full, "result": nil, "storage_root": source}
		ccfg := map[string]any{"enabled": true, "modules": map[string]string{"transfer": "module_execute"}, "result": nil}
		if scraping {
			cfg["modules"].(map[string]string)["metadata_nfo"] = "module_metadata_nfo"
			cfg["nfo_result"] = metadataText
		}
		returned := filepath.Join(dest, label+" (2024)")
		if mode == "copy" {
			returned = filepath.Join(dest, "Plugin Returned Disc")
			ccfg["modules"] = map[string]string{"transfer": "module_transfer"}
			ccfg["storage_root"], ccfg["transfer_root"], ccfg["transfer_target"], ccfg["transfer_mode"] = source, target, returned, "copy"
			ccfg["result"] = map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": true, "need_scrape": scraping, "target_item": map[string]any{"storage": "local", "type": "dir", "path": returned + "/"}, "target_diritem": map[string]any{"storage": "local", "type": "dir", "path": returned + "/"}, "file_list": []string{filepath.Join(path, "BDMV", "STREAM", "00001.m2ts"), filepath.Join(path, "BDMV", "index.bdmv")}, "file_list_new": []string{filepath.Join(returned, "BDMV", "STREAM", "00001.m2ts"), filepath.Join(returned, "BDMV", "index.bdmv")}}}
		}
		if mode == "veto" {
			cfg["result"] = false
		}
		set(manifest.PluginID, cfg)
		set(clone, ccfg)
		method := "hardlink"
		if mode == "copy" {
			method = "copy"
		}
		return manualOrganizeRequest{Paths: []string{path}, TargetPath: dest, Mode: method, MediaType: "movie", TMDBID: int64(900044 + len(label)), Scraping: importFlag(scraping)}, returned, cfg, ccfg
	}
	submit := func(in manualOrganizeRequest) manualOrganizeOutput {
		var out struct{ Data manualOrganizeOutput }
		if json.Unmarshal(request("POST", "/api/files/organize", in, 200), &out) != nil {
			t.Fatal("actual directory request")
		}
		return out.Data
	}
	await := func(id, want string) Job {
		for until := time.Now().Add(20 * time.Second); time.Now().Before(until); {
			var out struct{ Data struct{ Job Job } }
			if json.Unmarshal(request("GET", "/api/jobs/"+id, nil, 200), &out) != nil {
				t.Fatal("actual job")
			}
			if out.Data.Job.State == want {
				return out.Data.Job
			}
			if out.Data.Job.State == "failed" {
				t.Fatal("directory job failed", out.Data.Job)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("directory job timeout", id, want)
		return Job{}
	}
	proofs := []any{}
	for _, mode := range []string{"native", "copy", "veto"} {
		in, returned, _, _ := setup("Disc"+mode, mode)
		in.Preview = true
		preview := submit(in)
		if len(preview.Items) != 1 || preview.Failed != map[bool]int{true: 1, false: 0}[mode == "veto"] {
			t.Fatal("single directory preview", preview)
		}
		if _, e := os.Stat(returned); !os.IsNotExist(e) {
			t.Fatal("preview writes", e)
		}
		if mode == "veto" {
			proofs = append(proofs, preview)
			continue
		}
		in.Preview = false
		before := []trace{read(manifest.PluginID), read(clone)}
		out := submit(in)
		if out.Submitted != 1 || len(out.Items) != 1 {
			t.Fatal("single directory job", out)
		}
		job := await(out.Items[0].JobID, "succeeded")
		if scraping {
			var scrape Job
			for until := time.Now().Add(10 * time.Second); time.Now().Before(until); {
				var jobs struct{ Data []Job }
				if json.Unmarshal(request("GET", "/api/jobs", nil, 200), &jobs) != nil {
					t.Fatal("scrape jobs")
				}
				for _, candidate := range jobs.Data {
					if candidate.Kind == "scrape" && candidate.WorkflowID == originalScrapeBatchID(job) {
						scrape = candidate
					}
				}
				if scrape.ID != "" {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if scrape.ID == "" {
				t.Fatal("missing actual directory scrape job", job)
			}
			scrape = await(scrape.ID, "succeeded")
			nfo := filepath.Join(returned, filepath.Base(returned)+".nfo")
			if b, e := os.ReadFile(nfo); e != nil || string(b) != metadataText {
				t.Fatal("actual source NFO module and storage write", e)
			}
			proofs = append(proofs, map[string]any{"metadata_job": scrape, "nfo": nfo, "bytes": metadataText})
		}
		for _, name := range []string{"BDMV/STREAM/00001.m2ts", "BDMV/index.bdmv"} {
			s, e := os.Stat(filepath.Join(in.Paths[0], name))
			d, e2 := os.Stat(filepath.Join(returned, name))
			if e != nil || e2 != nil || os.SameFile(s, d) != (mode == "native") {
				t.Fatal("actual directory member", mode, name, e, e2)
			}
		}
		var histories struct {
			Data struct {
				Items []transferHistoryItem
				Total int
			}
		}
		if json.Unmarshal(request("GET", "/api/history/transfers?q=Disc"+mode, nil, 200), &histories) != nil || histories.Data.Total != 1 || histories.Data.Items[0].Target != returned || histories.Data.Items[0].Status != "success" {
			t.Fatal("whole directory history", histories)
		}
		if histories.Data.Items[0].Size != map[string]int64{"native": 19, "copy": 0}[mode] {
			t.Fatal("source history size", mode, histories.Data.Items[0].Size)
		}
		after := []trace{read(manifest.PluginID), read(clone)}
		for n, snapshot := range after {
			key := "module_execute"
			if n == 1 && mode == "copy" {
				key = "module_transfer"
			}
			if snapshot.Calls[key] != before[n].Calls[key]+1 {
				t.Fatal("one whole-directory hook", mode, n, before, after)
			}
		}
		proofs = append(proofs, map[string]any{"mode": mode, "job": job, "preview": preview, "history": histories, "before": before, "after": after})
		if mode == "copy" {
			h := histories.Data.Items[0]
			request("DELETE", "/api/history/transfers/"+h.ID, historyDeleteRequest{Confirm: true, DeleteTarget: true, FileID: h.executionFileID()}, 200)
			if _, e := os.Stat(returned); !os.IsNotExist(e) {
				t.Fatal("real directory history target delete", e)
			}
			if _, e := os.Stat(in.Paths[0]); e != nil {
				t.Fatal("source retained", e)
			}
		}
	}
	before := []trace{read(manifest.PluginID), read(clone)}
	stop()
	start()
	cold := []trace{read(manifest.PluginID), read(clone)}
	for n, snapshot := range cold {
		if snapshot.PID == before[n].PID || snapshot.Calls["module_execute"] != 0 || snapshot.Calls["module_transfer"] != 0 || snapshot.Calls["module_metadata_nfo"] != 0 {
			t.Fatal("cold no transfer replay", before, cold)
		}
	}
	writeOwnedEventsProcessEvidence(t, "bluray44-cli.json", mustCleanSeedJSON(map[string]any{"cases": proofs, "before_cold": before, "cold": cold}))
	if statePath := os.Getenv("PLUGIN_PROCESS_BLURAY_STATE"); statePath != "" {
		in, returned, cfg, ccfg := setup("UIBluray", "native")
		hold := filepath.Join(source, "ui-release")
		cfg["transfer_hold"] = hold
		set(manifest.PluginID, cfg)
		out := submit(in)
		if out.Submitted != 1 {
			t.Fatal("UI task", out)
		}
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
		request("POST", "/api/jobs/"+out.Items[0].JobID+"/cancel", map[string]any{}, 200)
		if e := os.WriteFile(hold, []byte("release"), 0600); e != nil {
			t.Fatal(e)
		}
		job := await(out.Items[0].JobID, "cancelled")
		if _, e := os.Stat(returned); !os.IsNotExist(e) {
			t.Fatal("cancelled directory published", e)
		}
		state := map[string]any{"origin": origin, "data_dir": dir, "source": manifest.PluginID, "clone": clone, "source_config": cfg, "clone_config": ccfg, "input": in, "target": returned, "job": job}
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
			t.Fatal("browser deadline")
		}
		final := await(job.ID, "succeeded")
		var histories struct {
			Data struct {
				Items []transferHistoryItem
				Total int
			}
		}
		if json.Unmarshal(request("GET", "/api/history/transfers?q=UIBluray", nil, 200), &histories) != nil || histories.Data.Total != 0 {
			t.Fatal("browser must actually delete selected directory history", histories)
		}
		if _, e := os.Stat(returned); !os.IsNotExist(e) {
			t.Fatal("browser target deletion", e)
		}
		if _, e := os.Stat(in.Paths[0]); e != nil {
			t.Fatal("browser source retained", e)
		}
		writeOwnedEventsProcessEvidence(t, "bluray44-browser-final.json", mustCleanSeedJSON(map[string]any{"job": final, "history": histories, "source": read(manifest.PluginID), "clone": read(clone)}))
	}
	success("DELETE", "/api/plugins/"+clone, nil)
	success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
	request("GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", nil, 404)
	t.Log("actual Blu-ray directory host: one task; source/clone native and returned-copy; veto; history deletion; cold no replay; uninstall")
}
