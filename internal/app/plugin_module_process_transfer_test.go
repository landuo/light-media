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

func verifyOwnedProductionTransfer(t *testing.T, dir string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	worker := os.Getenv("OWNED_TRANSFER_EVENTS_BINARY")
	payload, err := os.ReadFile(worker)
	if err != nil {
		t.Fatal("actual transfer worker required", err)
	}
	raw, err := exec.Command(worker, "--manifest").Output()
	var manifest plugins.Manifest
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedTransferEvents" {
		t.Fatal("actual transfer worker manifest")
	}
	digest := sha256.Sum256(payload)
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: hex.EncodeToString(digest[:]), ExecutableSHA256: hex.EncodeToString(digest[:]), Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}
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
	base := t.TempDir()
	source, target := filepath.Join(base, "source"), filepath.Join(base, "library")
	for _, path := range []string{source, target} {
		if err := os.MkdirAll(path, 0750); err != nil {
			t.Fatal(err)
		}
	}
	var current struct{ Data Settings }
	if json.Unmarshal(request("GET", "/api/settings", nil, 200), &current) != nil {
		t.Fatal("real settings")
	}
	current.Data.AllowedRoots = []string{source}
	current.Data.Roots = []Root{{ID: "owned-transfer-root", Path: target, Kind: "mixed"}}
	request("PUT", "/api/settings", current.Data, 200)
	mediaID := int64(123)
	configure := func(id, name, mode string, cancel bool, identity int64) {
		mediaID = identity
		wire, _ := json.Marshal(map[string]any{"id": identity, "title": "Owned transfer", "original_title": "Owned Transfer", "release_date": "2024-01-01", "overview": "owned", "genres": []any{}, "credits": map[string]any{"cast": []any{}, "crew": []any{}}, "external_ids": map[string]any{}, "images": map[string]any{}})
		full, err := sourceTMDBMediaInfo(wire, "movie", "image.tmdb.org", "")
		if err != nil {
			t.Fatal(err)
		}
		patches := map[string]any{"transfer.rename": map[string]any{"updated": true, "updated_str": name + "/" + name + ".mkv"}}
		if cancel {
			patches["transfer.intercept"] = map[string]any{"cancel": true, "reason": "owned production veto"}
		}
		success("PUT", "/api/plugins/"+id, map[string]any{"enabled": true, "storage": true, "storage_root": base, "storage_mode": mode, "mediainfo": full, "patches": patches})
	}
	read := func(id string) []struct {
		Type string
		Data json.RawMessage
	} {
		var rows []struct {
			Type string
			Data json.RawMessage
		}
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/observations", nil, 200), &rows) != nil {
			t.Fatal("actual transfer observations")
		}
		return rows
	}
	pid := func(id string) int {
		var state struct{ PID int }
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/state", nil, 200), &state) != nil || state.PID <= 0 {
			t.Fatal("actual SDK PID")
		}
		return state.PID
	}
	run := func(name, method string, want bool) ImportPlan {
		path := filepath.Join(source, name+".2024.mkv")
		if err := os.WriteFile(path, []byte("owned production transfer bytes"), 0640); err != nil {
			t.Fatal(err)
		}
		var preview struct{ Data ImportPlan }
		if json.Unmarshal(request("POST", "/api/import/preview", ImportRequest{MediaType: "movie", TMDBID: mediaID, RootID: "owned-transfer-root", Mode: method, Scraping: importFlag(false), Files: []ImportFileRequest{{Path: path}}}, 200), &preview) != nil || len(preview.Data.Files) != 1 || preview.Data.Files[0].Status != "ready" {
			t.Fatal("real transfer preview", name, preview.Data.Files)
		}
		var execution struct{ Data Job }
		if json.Unmarshal(request("POST", "/api/import/execute", map[string]any{"plan_id": preview.Data.ID}, 200), &execution) != nil || execution.Data.ID == "" {
			t.Fatal("real transfer submission")
		}
		deadline := time.Now().Add(20 * time.Second)
		var actual ImportPlan
		for time.Now().Before(deadline) {
			var plans struct{ Data []ImportPlan }
			if json.Unmarshal(request("GET", "/api/import/plans", nil, 200), &plans) != nil {
				t.Fatal("real import plans")
			}
			for _, plan := range plans.Data {
				if plan.ID == preview.Data.ID {
					actual = plan
				}
			}
			if len(actual.Files) == 1 && (actual.Files[0].Status == "imported" || actual.Files[0].Status == "error") {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if len(actual.Files) != 1 || (actual.Files[0].Status == "imported") != want || !want && actual.Files[0].Status != "error" {
			t.Fatal("actual transfer outcome", actual)
		}
		bytes, readErr := os.ReadFile(actual.Files[0].Target)
		if want && (readErr != nil || string(bytes) != "owned production transfer bytes") || !want && !os.IsNotExist(readErr) {
			t.Fatal("actual transfer target", readErr)
		}
		if bytes, err := os.ReadFile(path); err != nil || string(bytes) != "owned production transfer bytes" {
			t.Fatal("real source retained")
		}
		if !want {
			// Failure commits the plan before the job finishes. Observe its
			// actual terminal state instead of racing a redundant cancellation.
			terminal := false
			for until := time.Now().Add(10 * time.Second); time.Now().Before(until); {
				var result struct{ Data struct{ Job Job } }
				if json.Unmarshal(request("GET", "/api/jobs/"+execution.Data.ID, nil, 200), &result) != nil {
					t.Fatal("actual rejected transfer job")
				}
				if result.Data.Job.State == "failed" {
					terminal = true
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !terminal {
				t.Fatal("rejected transfer did not become failed", execution.Data.ID)
			}
		}
		return actual
	}
	configure(manifest.PluginID, "Source", "", false, 123)
	first := run("First", "hardlink", true)
	if first.Files[0].Target != filepath.Join(target, "Source", "Source.mkv") {
		t.Fatal("source rename not consumed")
	}
	configure(manifest.PluginID, "Veto", "", true, 124)
	run("Veto", "copy", false)
	configure(manifest.PluginID, "Lie", "lie", false, 125)
	run("Lie", "hardlink", false)
	configure(manifest.PluginID, "Source", "", false, 126)
	success("POST", "/api/plugins/"+manifest.PluginID+"/clone", map[string]any{"suffix": "Isolated"})
	clone := manifest.PluginID + "isolated"
	configure(clone, "Clone", "", false, 126)
	beforeSource, beforeClone := pid(manifest.PluginID), pid(clone)
	if beforeSource == beforeClone {
		t.Fatal("clone shares SDK process")
	}
	cloned := run("Clone", "copy", true)
	if cloned.Files[0].Target != filepath.Join(target, "Clone", "Clone.mkv") {
		t.Fatal("clone naming or storage was not applied")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rows := read(clone)
		if len(rows) > 0 && rows[len(rows)-1].Type == "transfer.complete" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	sourceRows, cloneRows := read(manifest.PluginID), read(clone)
	copyCalls := 0
	for _, row := range cloneRows {
		if row.Type == "storage.copy" {
			copyCalls++
		}
	}
	if copyCalls != 1 {
		t.Fatal("clone storage object not actually called", copyCalls)
	}
	if os.Getenv("PLUGIN_PROCESS_DIRECTORY_STORAGE_FIXTURE") == "true" {
		modes := []string{"directory-names", "late-list-error"}
		if os.Getenv("PLUGIN_PROCESS_DIRECTORY_HISTORY_FIXTURE") == "true" {
			modes = append(modes, "directory-names", "directory-names")
		}
		for i, mode := range modes {
			label := []string{"DiscNames", "DiscLate", "DiscNoMedia", "DiscKeepSource"}[i]
			identity := int64(900046 + i)
			configure(manifest.PluginID, label, "", false, identity)
			configure(clone, label, mode, false, identity)
			disc := filepath.Join(source, label+".2024.BluRay")
			for name, data := range map[string]string{"BDMV/STREAM/00001.m2ts": "owned stream", "BDMV/index.bdmv": "owned index"} {
				path := filepath.Join(disc, name)
				if os.MkdirAll(filepath.Dir(path), 0750) != nil || os.WriteFile(path, []byte(data), 0640) != nil {
					t.Fatal("owned disc setup")
				}
			}
			if err := os.Mkdir(filepath.Join(disc, "CERTIFICATE"), 0750); err != nil {
				t.Fatal(err)
			}
			var out struct{ Data manualOrganizeOutput }
			if json.Unmarshal(request("POST", "/api/files/organize", manualOrganizeRequest{Paths: []string{disc}, TargetPath: target, Mode: "hardlink", MediaType: "movie", TMDBID: identity, Scraping: importFlag(false)}, 200), &out) != nil || out.Data.Submitted != 1 {
				t.Fatal("actual selected-storage directory", out)
			}
			var job Job
			for until := time.Now().Add(20 * time.Second); time.Now().Before(until); {
				var state struct{ Data struct{ Job Job } }
				if json.Unmarshal(request("GET", "/api/jobs/"+out.Data.Items[0].JobID, nil, 200), &state) != nil {
					t.Fatal("directory job")
				}
				job = state.Data.Job
				if job.State == "succeeded" || job.State == "failed" || job.State == "needs_review" {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if (job.State == "succeeded") != (i != 1) || i == 1 && job.State != "failed" {
				t.Fatal("actual directory state", job)
			}
			var plans struct{ Data []ImportPlan }
			if json.Unmarshal(request("GET", "/api/import/plans", nil, 200), &plans) != nil {
				t.Fatal("final directory plans")
			}
			destination := ""
			for _, plan := range plans.Data {
				if plan.ID == out.Data.Items[0].PlanID && len(plan.Files) == 1 {
					destination = plan.Files[0].Target
				}
			}
			if destination == "" {
				t.Fatal("missing frozen final directory target")
			}
			physical := []any{}
			for _, relative := range []string{"BDMV/STREAM/00001.m2ts", "BDMV/index.bdmv"} {
				mapped := relative
				if mode == "directory-names" {
					mapped = map[string]string{"BDMV/STREAM/00001.m2ts": "BluRay/Streams/feature.m2ts", "BDMV/index.bdmv": "BluRay/index-custom.bdmv"}[relative]
				}
				input, output := filepath.Join(disc, relative), filepath.Join(destination, mapped)
				s, e := os.Stat(input)
				d, e2 := os.Stat(output)
				if e != nil || e2 != nil || !os.SameFile(s, d) {
					t.Fatal("actual renamed or partial member", input, output, e, e2)
				}
				physical = append(physical, map[string]any{"source": input, "target": output, "same_file": true, "size": d.Size()})
			}
			var event map[string]any
			eventType := "transfer.complete"
			if i == 1 {
				eventType = "transfer.failed"
			}
			for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
				for _, row := range read(clone) {
					if row.Type != eventType {
						continue
					}
					var value map[string]any
					if json.Unmarshal(row.Data, &value) == nil && filepath.Clean(sourceText(sourceMap(value["fileitem"])["path"])) == disc {
						event = value
					}
				}
				if event != nil {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if event == nil {
				t.Fatal("actual directory event")
			}
			info := sourceMap(event["transferinfo"])
			if i == 1 && (info["fileitem"] != nil || info["transfer_type"] != nil || len(info["file_list"].([]any)) != 0 || len(info["file_list_new"].([]any)) != 0) {
				t.Fatal("source fresh exception DTO", info)
			}
			writeOwnedEventsProcessEvidence(t, "directory-storage46-"+label+".json", mustCleanSeedJSON(map[string]any{"job": job, "physical": physical, "event": event, "source": read(manifest.PluginID), "clone": read(clone)}))
			if i >= 2 {
				var history struct {
					Data struct{ Items []transferHistoryItem }
				}
				if json.Unmarshal(request("GET", "/api/history/transfers", nil, 200), &history) != nil {
					t.Fatal("directory history")
				}
				var selected transferHistoryItem
				for _, h := range history.Data.Items {
					if h.PlanID == out.Data.Items[0].PlanID {
						selected = h
					}
				}
				if selected.ID == "" {
					t.Fatal("actual directory history absent")
				}
				if i == 2 {
					current.Data.RMTMediaExt = json.RawMessage(`[".mkv"]`)
					request("PUT", "/api/settings", current.Data, 200)
				} else {
					configure(clone, label, "directory-no-bluray", false, identity)
				}
				response := request("DELETE", "/api/history/transfers/"+selected.ID, historyDeleteRequest{Confirm: true, DeleteTarget: i == 2, DeleteSource: i == 3, FileID: selected.executionFileID()}, 200)
				_, targetErr := os.Stat(destination)
				_, sourceErr := os.Stat(disc)
				if sourceErr != nil || os.IsNotExist(targetErr) != (i == 2) {
					t.Fatal("ordinary directory delete effects", sourceErr, targetErr)
				}
				if i == 2 {
					current.Data.RMTMediaExt = nil
					request("PUT", "/api/settings", current.Data, 200)
				}
				if i == 3 {
					found := false
					for until := time.Now().Add(5 * time.Second); time.Now().Before(until) && !found; {
						for _, row := range read(clone) {
							if row.Type == "downloadfile.deleted" {
								var v map[string]any
								_ = json.Unmarshal(row.Data, &v)
								found = found || v["src"] == disc
							}
						}
						if !found {
							time.Sleep(20 * time.Millisecond)
						}
					}
					if !found {
						t.Fatal("fixed endpoint event missing for retained source")
					}
				}
				writeOwnedEventsProcessEvidence(t, "directory-history47-"+label+".json", mustCleanSeedJSON(map[string]any{"response": json.RawMessage(response), "source_exists": sourceErr == nil, "target_exists": targetErr == nil, "source": read(manifest.PluginID), "clone": read(clone), "history_after": json.RawMessage(request("GET", "/api/history/transfers", nil, 200))}))
			}
		}
		sourceRows, cloneRows = read(manifest.PluginID), read(clone)
	}
	stop()
	start()
	afterSource, afterClone := pid(manifest.PluginID), pid(clone)
	if afterSource == beforeSource || afterClone == beforeClone || afterSource == afterClone {
		t.Fatal("actual cold SDK isolation")
	}
	if len(read(manifest.PluginID)) != len(sourceRows) || len(read(clone)) != len(cloneRows) {
		t.Fatal("cold startup replayed transfer")
	}
	success("DELETE", "/api/plugins/"+clone, nil)
	request("GET", "/api/v1/plugin/"+clone+"/state", nil, 404)
	configure(manifest.PluginID, "AfterCold", "", false, 127)
	run("AfterCold", "hardlink", true)
	browserClone := ""
	if os.Getenv("PLUGIN_PROCESS_SERVICES_BROWSER_STATE") != "" {
		configure(manifest.PluginID, "BrowserSource", "", false, 128)
		success("POST", "/api/plugins/"+manifest.PluginID+"/clone", map[string]any{"suffix": "Browser"})
		browserClone = manifest.PluginID + "browser"
		configure(browserClone, "BrowserClone", "", false, 128)
		if err := os.WriteFile(filepath.Join(source, "Browser.2024.mkv"), []byte("owned production browser media"), 0640); err != nil {
			t.Fatal(err)
		}
	}
	if path := os.Getenv("PLUGIN_PROCESS_TRANSFER_DETAILS"); path != "" {
		raw, _ := json.Marshal(map[string]any{"source_dir": source, "target_dir": target, "plugin": manifest.PluginID, "browser_clone": browserClone, "media_id": mediaID, "source_pid": afterSource, "clone_previous_pid": beforeClone})
		if os.WriteFile(path, raw, 0600) != nil {
			t.Fatal("transfer browser details")
		}
	}
	if os.Getenv("PLUGIN_PROCESS_SERVICES_BROWSER_STATE") == "" {
		success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
		request("GET", "/api/v1/plugin/"+manifest.PluginID+"/state", nil, 404)
	}
	t.Logf("actual transfer source/clone %d/%d -> %d/%d; real link/copy, veto, false-success rejected; clone storage isolation; no cold replay", beforeSource, beforeClone, afterSource, afterClone)
}
