package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"light-media/internal/sites"
)

func verifyOwnedProductionDownloadContext(t *testing.T, dir string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	worker := os.Getenv("OWNED_TRANSFER_EVENTS_BINARY")
	payload, err := os.ReadFile(worker)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(worker, "--manifest").Output()
	var manifest plugins.Manifest
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedTransferEvents" {
		t.Fatal("download context worker")
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
	t.Cleanup(repository.Close)
	index.Releases[0].Artifacts[0].URL = repository.URL + "/worker"
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": manifest.PluginID, "repo_url": repository.URL + "/index"})
	base := t.TempDir()
	destination := filepath.Join(base, "downloads")
	if os.MkdirAll(destination, 0750) != nil {
		t.Fatal("owned destination")
	}
	type sample struct {
		ID, Title, Hash string
		Torrent         []byte
	}
	samples := map[string]sample{}
	byHash := map[string]sample{}
	for i, name := range []string{"Normal", "Veto", "VetoError", "Escape", "Clone", "Cold", "Browser"} {
		id := fmt.Sprint(101 + i)
		title := "OwnedContext" + name + ".2024"
		torrent, hash := workflowTorrent(title + ".mkv")
		s := sample{id, title, hash, torrent}
		samples[name] = s
		byHash[hash] = s
	}
	var mu sync.Mutex
	tasks := map[string]string{}
	adds := 0
	postFixture := os.Getenv("PLUGIN_PROCESS_DOWNLOAD_ADDED_FIXTURE") == "true"
	postFailure := false
	postCalls := []json.RawMessage{}
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/owned/post-download":
			raw, e := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			var call struct {
				Kwargs struct {
					Context   originalDownloadContext
					Directory string          `json:"download_dir"`
					Content   json.RawMessage `json:"torrent_content"`
				}
			}
			if e != nil || json.Unmarshal(raw, &call) != nil || call.Kwargs.Directory != destination || len(call.Kwargs.Context.Meta) == 0 || len(call.Kwargs.Context.Media) == 0 {
				t.Error("actual post-Add source kwargs", e)
			}
			content, ok := plugins.DecodeModuleResultBytes(call.Kwargs.Content)
			found := false
			for _, s := range samples {
				found = found || bytes.Equal(content, s.Torrent)
			}
			if !ok || !found {
				t.Error("actual post-Add metainfo bytes")
			}
			postCalls = append(postCalls, append(json.RawMessage(nil), raw...))
			if postFailure {
				w.WriteHeader(http.StatusServiceUnavailable)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
		case "/api/v2/app/preferences":
			_ = json.NewEncoder(w).Encode(map[string]string{"save_path": destination})
		case "/api/v2/torrents/info":
			rows := []any{}
			for hash, path := range tasks {
				if selected := r.URL.Query().Get("hashes"); selected != "" && !strings.Contains("|"+selected+"|", "|"+hash+"|") {
					continue
				}
				task := workflowTask(hash, 0)
				task["name"] = byHash[hash].Title
				task["save_path"] = path
				rows = append(rows, task)
			}
			workflowJSON(w, rows)
		case "/api/v2/torrents/add":
			if r.ParseMultipartForm(4<<20) != nil {
				t.Error("owned multipart")
			}
			part, _, e := r.FormFile("torrents")
			if e != nil {
				t.Error(e)
				w.WriteHeader(400)
				return
			}
			data, _ := io.ReadAll(part)
			_ = part.Close()
			found := false
			for hash, s := range byHash {
				if string(data) == string(s.Torrent) {
					tasks[hash] = r.FormValue("savepath")
					found = true
					break
				}
			}
			if !found {
				t.Error("actual material mismatch")
				w.WriteHeader(400)
				return
			}
			adds++
			_, _ = io.WriteString(w, "Ok.")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(qb.Close)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			name := r.URL.Query().Get("search")
			selected := samples["Browser"]
			for _, s := range samples {
				if strings.Contains(name, s.Title) {
					selected = s
					break
				}
			}
			_, _ = io.WriteString(w, strings.ReplaceAll(workflowHTML(selected.Title, "", false), "id=101", "id="+selected.ID))
		case "/download.php":
			for _, s := range samples {
				if r.URL.Query().Get("id") == s.ID {
					_, _ = w.Write(s.Torrent)
					return
				}
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(site.Close)
	var current struct{ Data Settings }
	if json.Unmarshal(request("GET", "/api/settings", nil, 200), &current) != nil {
		t.Fatal("settings")
	}
	settings := current.Data
	settings.AllowedRoots = []string{base}
	settings.Roots = nil
	settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-context-qb", Kind: "qb", BaseURL: qb.URL, APIKey: "owned-context-protocol-key"}, Name: "Owned context downloader"}}
	settings.DefaultDownloader = "owned-context-qb"
	settings.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-context-site", Kind: "jiayuan", Name: "Owned context site", BaseURL: site.URL, Cookie: "synthetic-context-cookie"}, Enabled: true, DailyBudget: 100}}
	request("PUT", "/api/settings", settings, 200)
	media, err := sourceTMDBMediaInfo([]byte(`{"id":123,"title":"Owned context media","original_title":"Owned Context","release_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"images":{},"external_ids":{},"owned_private":"source-provider-payload"}`), "movie", "image.tmdb.org", "")
	if err != nil {
		t.Fatal(err)
	}
	config := func(id string, cancel, fail bool, path string) {
		patch := map[string]any{}
		if cancel {
			patch["cancel"] = true
			patch["reason"] = "owned context veto"
		}
		if path != "" {
			patch["options"] = map[string]any{"save_path": path}
		}
		fields := map[string]any{"enabled": true, "mediainfo": media, "patches": map[string]any{"resource.download": patch}, "fail": map[string]bool{"resource.download": fail}}
		if postFixture {
			fields["post_url"] = qb.URL + "/owned/post-download"
		}
		success("PUT", "/api/plugins/"+id, fields)
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
			t.Fatal("actual context observations")
		}
		return rows
	}
	readPosts := func(id string) []json.RawMessage {
		var rows []json.RawMessage
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/post-downloads", nil, 200), &rows) != nil {
			t.Fatal("actual post-Add SDK observations")
		}
		return rows
	}
	pid := func(id string) int {
		var state struct{ PID int }
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/state", nil, 200), &state) != nil || state.PID <= 0 {
			t.Fatal("actual SDK process")
		}
		return state.PID
	}
	run := func(name string, want bool) {
		s := samples[name]
		var queued struct{ Data NativeDownload }
		if json.Unmarshal(request("POST", "/api/downloads/add", NativeDownloadRequest{SiteID: "owned-context-site", ResourceID: s.ID, Query: s.Title, DownloaderID: "owned-context-qb", DownloadPath: destination, MediaType: "movie", TMDBID: 123}, 200), &queued) != nil || queued.Data.ID == "" {
			t.Fatal("actual Add queued")
		}
		deadline := time.Now().Add(15 * time.Second)
		var actual NativeDownload
		for time.Now().Before(deadline) {
			var out struct{ Data NativeDownload }
			if json.Unmarshal(request("GET", "/api/downloads/native/"+queued.Data.ID, nil, 200), &out) != nil {
				t.Fatal("actual Add status")
			}
			actual = out.Data
			if actual.State == "accepted" || actual.State == "failed" {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if (actual.State == "accepted") != want || !want && actual.State != "failed" {
			t.Fatal("actual native outcome", name, actual.State, actual.Error)
		}
		mu.Lock()
		_, exists := tasks[s.Hash]
		path := tasks[s.Hash]
		mu.Unlock()
		if exists != want || want && path != destination {
			t.Fatal("independent downloader task readback", name, exists, path)
		}
		writeOwnedEventsProcessEvidence(t, "download-context-"+name+".json", request("GET", "/api/downloads/native/"+queued.Data.ID, nil, 200))
	}
	config(manifest.PluginID, false, false, "")
	run("Normal", true)
	deadline := time.Now().Add(5 * time.Second)
	for len(read(manifest.PluginID)) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	initial := read(manifest.PluginID)
	if len(initial) != 2 || initial[0].Type != "resource.download" || initial[1].Type != "download.added" {
		t.Fatal("actual ordinary download event order")
	}
	for _, event := range initial {
		var frame struct{ Context originalDownloadContext }
		if json.Unmarshal(event.Data, &frame) != nil || !bytes.Contains(frame.Context.Media, []byte("source-provider-payload")) || !bytes.Contains(frame.Context.Torrent, []byte("synthetic-context-cookie")) || !bytes.Contains(frame.Context.Meta, []byte("OwnedContextNormal")) {
			t.Fatal("actual full source Context was lost", event.Type)
		}
		writeOwnedEventsProcessEvidence(t, "download-context-event-"+event.Type+".json", event.Data)
	}
	config(manifest.PluginID, true, false, "")
	run("Veto", false)
	config(manifest.PluginID, true, true, "")
	run("VetoError", false)
	config(manifest.PluginID, false, false, "/outside/owned-context-rejected")
	run("Escape", false)
	config(manifest.PluginID, true, false, "")
	success("POST", "/api/plugins/"+manifest.PluginID+"/clone", map[string]any{"suffix": "Context"})
	clone := manifest.PluginID + "context"
	config(clone, false, false, "")
	config(manifest.PluginID, false, false, "")
	run("Clone", true)
	sourceBefore, cloneBefore := pid(manifest.PluginID), pid(clone)
	sourceRows, cloneRows := read(manifest.PluginID), read(clone)
	var sourcePosts, clonePosts []json.RawMessage
	if postFixture {
		sourcePosts, clonePosts = readPosts(manifest.PluginID), readPosts(clone)
		if len(sourcePosts) != 2 || len(clonePosts) != 1 {
			t.Fatal("post-Add called for veto/escape or omitted actual Add", len(sourcePosts), len(clonePosts))
		}
	}
	stop()
	start()
	sourceAfter, cloneAfter := pid(manifest.PluginID), pid(clone)
	if sourceBefore == sourceAfter || cloneBefore == cloneAfter || sourceAfter == cloneAfter || len(read(manifest.PluginID)) != len(sourceRows) || len(read(clone)) != len(cloneRows) {
		t.Fatal("cold context replay/isolation")
	}
	if postFixture {
		source, _ := json.Marshal(readPosts(manifest.PluginID))
		beforeSource, _ := json.Marshal(sourcePosts)
		copy, _ := json.Marshal(readPosts(clone))
		beforeClone, _ := json.Marshal(clonePosts)
		if !originalBaseJSONEqual(source, beforeSource) || !originalBaseJSONEqual(copy, beforeClone) {
			t.Fatal("cold startup replayed ordinary post-Add module")
		}
		mu.Lock()
		postFailure = true
		mu.Unlock()
	}
	success("DELETE", "/api/plugins/"+clone, nil)
	request("GET", "/api/v1/plugin/"+clone+"/state", nil, 404)
	run("Cold", true)
	if postFixture {
		posts := readPosts(manifest.PluginID)
		mu.Lock()
		calls, _ := json.Marshal(postCalls)
		count := len(postCalls)
		postFailure = false
		mu.Unlock()
		if len(posts) != 3 || count != 4 {
			t.Fatal("real post-Add source/clone/failure count", len(posts), count)
		}
		writeOwnedEventsProcessEvidence(t, "download-added-real-peer-calls.json", calls)
		raw, _ := json.Marshal(map[string]any{"source": posts, "clone": clonePosts, "source_pid_before": sourceBefore, "clone_pid_before": cloneBefore, "source_pid_after": sourceAfter, "clone_pid_after": cloneAfter, "peer_calls": count, "last_peer_status": 503, "last_native_accepted": true})
		writeOwnedEventsProcessEvidence(t, "download-added-real-module-lifecycle.json", raw)
		t.Log("actual ordinary download_added: full Context/bytes/source+clone, veto/escape no callback, cold no replay, real peer503 preserves accepted task")
	}
	wantAdds := 3
	if os.Getenv("PLUGIN_PROCESS_RESOURCE_SELECTION_FIXTURE") == "true" {
		// Disable the separate statistics dependency in this owned instance;
		// subscription creation/check itself still enters the production HTTP,
		// persisted job and live SDK paths below. No business rows are seeded.
		stop()
		seed, e := New(Options{DataDir: dir})
		if e != nil {
			t.Fatal(e)
		}
		subscriptionStatsInstallSettings(t, seed, "http://owned-unused.invalid", false)
		if e = seed.Close(); e != nil {
			t.Fatal(e)
		}
		start()
		var selectedContexts []json.RawMessage
		for i, quality := range []bool{false, true} {
			identity := 123 + int64(i)
			name := strings.TrimSuffix(samples["Browser"].Title, ".2024")
			provider, _ := json.Marshal(map[string]any{"id": identity, "title": name, "original_title": name, "release_date": "2024-01-01", "genres": []any{}, "credits": map[string]any{"cast": []any{}, "crew": []any{}}, "images": map[string]any{}, "external_ids": map[string]any{}})
			full, e := sourceTMDBMediaInfo(provider, "movie", "image.tmdb.org", "")
			if e != nil {
				t.Fatal(e)
			}
			success("PUT", "/api/plugins/"+manifest.PluginID, map[string]any{"enabled": true, "mediainfo": full, "patches": map[string]any{"resource.selection": map[string]any{"updated": true, "updated_contexts": []any{}}}, "fail": map[string]bool{"resource.selection": quality}})
			before := len(read(manifest.PluginID))
			var created struct{ Data Subscription }
			body := map[string]any{"media_type": "movie", "tmdb_id": identity, "name": name, "year": "2024", "keyword": samples["Browser"].Title, "site_ids": []string{"owned-context-site"}, "downloader_id": "owned-context-qb", "download_path": destination, "best_version": quality, "state": "active", "schedule": map[string]any{"mode": "manual"}}
			if json.Unmarshal(request("POST", "/api/subscriptions", body, 200), &created) != nil || created.Data.ID == "" {
				t.Fatal("actual selection subscription create")
			}
			var queued struct{ Data Job }
			if json.Unmarshal(request("POST", "/api/subscriptions/"+created.Data.ID+"/check", map[string]any{}, 200), &queued) != nil || queued.Data.ID == "" {
				t.Fatal("actual selection check queued")
			}
			deadline := time.Now().Add(15 * time.Second)
			finished := false
			for time.Now().Before(deadline) {
				var current struct{ Data struct{ Job Job } }
				if json.Unmarshal(request("GET", "/api/jobs/"+queued.Data.ID, nil, 200), &current) != nil {
					t.Fatal("actual selection job")
				}
				if current.Data.Job.State == "succeeded" {
					finished = true
					break
				}
				if current.Data.Job.State == "failed" || current.Data.Job.State == "retry_wait" {
					t.Fatal("actual selection job failed", current.Data.Job.State, current.Data.Job.ErrorCode)
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !finished {
				t.Fatal("selection check did not finish")
			}
			rows := read(manifest.PluginID)
			count := 0
			for _, row := range rows[before:] {
				if row.Type == "resource.selection" {
					count++
					writeOwnedEventsProcessEvidence(t, fmt.Sprintf("resource-selection-%d-event.json", i), row.Data)
					var event struct {
						Contexts []json.RawMessage
						Origin   string
					}
					if json.Unmarshal(row.Data, &event) != nil || len(event.Contexts) == 0 || !strings.HasPrefix(event.Origin, "Subscribe|") {
						t.Fatal("actual selector context/source absent", i, len(event.Contexts), event.Origin)
					}
					selectedContexts = event.Contexts
				}
			}
			if count != 1 {
				t.Fatal("selector not invoked exactly once", count)
			}
			var downloads struct{ Data struct{ Intents []Intent } }
			if json.Unmarshal(request("GET", "/api/downloads?downloader_id=owned-context-qb", nil, 200), &downloads) != nil {
				t.Fatal("actual download intents readback")
			}
			for _, intent := range downloads.Data.Intents {
				if intent.SubscriptionID == created.Data.ID {
					t.Fatal("source empty selection created an intent")
				}
			}
			request("DELETE", "/api/subscriptions/"+created.Data.ID, nil, 200)
		}
		// Keep the actually selected full Context across a real server restart
		// after local intent commit fails. A new callback would now clear the
		// batch, and the recognition provider is removed before recovery.
		name := strings.TrimSuffix(samples["Browser"].Title, ".2024")
		provider, _ := json.Marshal(map[string]any{"id": 125, "title": name, "original_title": name, "release_date": "2024-01-01", "genres": []any{}, "credits": map[string]any{"cast": []any{}, "crew": []any{}}, "images": map[string]any{}, "external_ids": map[string]any{}})
		full, e := sourceTMDBMediaInfo(provider, "movie", "image.tmdb.org", "")
		if e != nil || len(selectedContexts) == 0 {
			t.Fatal("actual selected Context fixture", e)
		}
		for i, raw := range selectedContexts {
			var selected originalDownloadContext
			if json.Unmarshal(raw, &selected) != nil {
				t.Fatal("actual selection input")
			}
			selected.Media = full
			selected.Extra = map[string]json.RawMessage{"owned_cold_marker": json.RawMessage(`{"source":"actual selection before SQL failure"}`)}
			selectedContexts[i], _ = json.Marshal(selected)
		}
		success("PUT", "/api/plugins/"+manifest.PluginID, map[string]any{"enabled": true, "mediainfo": full, "patches": map[string]any{"resource.selection": map[string]any{"updated": true, "updated_contexts": selectedContexts}}})
		before := len(read(manifest.PluginID))
		var created struct{ Data Subscription }
		body := map[string]any{"media_type": "movie", "tmdb_id": 125, "name": name, "year": "2024", "keyword": samples["Browser"].Title, "site_ids": []string{"owned-context-site"}, "downloader_id": "owned-context-qb", "download_path": destination, "best_version": true, "state": "active", "schedule": map[string]any{"mode": "manual"}}
		if json.Unmarshal(request("POST", "/api/subscriptions", body, 200), &created) != nil || created.Data.ID == "" {
			t.Fatal("actual cold selection subscription")
		}
		db, e := sql.Open("sqlite", filepath.Join(dir, "light-media.db"))
		if e != nil {
			t.Fatal(e)
		}
		defer db.Close()
		key, e := os.ReadFile(filepath.Join(dir, "secret.key"))
		if e != nil || len(key) != 32 {
			t.Fatal("owned observer key", e)
		}
		observerStore := &Store{DB: db, key: key}
		observer := &App{Store: observerStore, pluginStore: &PluginStore{store: observerStore}}
		observeAcquisition := func(label, jobID string) {
			t.Helper()
			root, e := observer.readAcquisition(context.Background(), db, jobID)
			if e != nil {
				t.Fatal("owned acquisition observer", e)
			}
			phases := []string{}
			for _, step := range root.Steps {
				phases = append(phases, step.Phase+":"+step.Error)
			}
			proof, _ := json.Marshal(map[string]any{"generation": root.Generation, "completed": root.Completed, "kind": root.Subscription.Media.MediaType, "current_priority": root.Subscription.CurrentPriority, "candidate_count": len(root.Candidates), "selection_context_count": len(root.SelectionContexts), "phases": phases})
			writeOwnedEventsProcessEvidence(t, "resource-selection-cold-"+label+"-acquisition.json", proof)
			t.Log(label, string(proof))
		}
		if _, e = db.Exec(`CREATE TRIGGER owned_selection_intent_failure BEFORE INSERT ON records WHEN NEW.kind='intent' BEGIN SELECT RAISE(ABORT,'owned_selection_intent_failure'); END`); e != nil {
			t.Fatal(e)
		}
		var queued struct{ Data Job }
		if json.Unmarshal(request("POST", "/api/subscriptions/"+created.Data.ID+"/check", map[string]any{}, 200), &queued) != nil || queued.Data.ID == "" {
			t.Fatal("actual cold selection queued")
		}
		waitJob := func(failed bool) {
			t.Helper()
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				var current struct{ Data struct{ Job Job } }
				if json.Unmarshal(request("GET", "/api/jobs/"+queued.Data.ID, nil, 200), &current) != nil {
					t.Fatal("actual cold selection job")
				}
				state := current.Data.Job.State
				if failed && (state == "retry_wait" || state == "failed") {
					raw, _ := json.Marshal(current.Data.Job)
					writeOwnedEventsProcessEvidence(t, "resource-selection-cold-local-failure.json", raw)
					return
				}
				if !failed && state == "succeeded" {
					raw, _ := json.Marshal(current.Data.Job)
					writeOwnedEventsProcessEvidence(t, "resource-selection-cold-recovered-job.json", raw)
					return
				}
				if failed && state == "succeeded" || !failed && (state == "failed" || state == "retry_wait") {
					t.Fatal("unexpected actual cold selection result", state, current.Data.Job.ErrorCode)
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatal("cold selection deadline")
		}
		waitJob(true)
		observeAcquisition("failed", queued.Data.ID)
		oldPID := pid(manifest.PluginID)
		success("PUT", "/api/plugins/"+manifest.PluginID, map[string]any{"enabled": true, "patches": map[string]any{"resource.selection": map[string]any{"updated": true, "updated_contexts": []any{}}}})
		stop()
		if _, e = db.Exec(`DROP TRIGGER owned_selection_intent_failure`); e != nil {
			t.Fatal(e)
		}
		// Advance only this owned failed job's retry deadline; no business
		// payload/context is changed by this controlled clock setup.
		if _, e = db.Exec(`UPDATE jobs SET run_at=0 WHERE id=? AND state='retry_wait'`, queued.Data.ID); e != nil {
			t.Fatal(e)
		}
		start()
		waitJob(false)
		observeAcquisition("recovered", queued.Data.ID)
		newPID := pid(manifest.PluginID)
		if oldPID == newPID {
			t.Fatal("actual selection cold process did not restart")
		}
		selection, added := 0, 0
		for _, row := range read(manifest.PluginID)[before:] {
			if row.Type == "resource.selection" {
				selection++
			}
			if row.Type == "download.added" {
				added++
				var result struct{ Context originalDownloadContext }
				if json.Unmarshal(row.Data, &result) != nil || !originalBaseJSONEqual(result.Context.Extra["owned_cold_marker"], json.RawMessage(`{"source":"actual selection before SQL failure"}`)) {
					t.Fatal("frozen Context lost on actual main recovery", string(row.Data))
				}
				writeOwnedEventsProcessEvidence(t, "resource-selection-cold-download-added.json", row.Data)
			}
		}
		if selection != 1 || added != 1 {
			t.Fatal("actual selection or Add event repeated after cold", selection, added)
		}
		coldProof, _ := json.Marshal(map[string]any{"contexts": selectedContexts, "source_pid_before": oldPID, "source_pid_after": newPID, "selection_calls": selection, "download_added_calls": added})
		writeOwnedEventsProcessEvidence(t, "resource-selection-cold-contexts.json", coldProof)
		request("DELETE", "/api/subscriptions/"+created.Data.ID, nil, 200)
		wantAdds = 4
		config(manifest.PluginID, false, false, "")
		t.Log("actual ordinary+quality empty selection has no Add; nonempty quality SQL failure→cold→one actual Add preserves frozen Context without replay")
	}
	if os.Getenv("PLUGIN_PROCESS_SERVICES_BROWSER_STATE") != "" {
		if path := os.Getenv("PLUGIN_PROCESS_DOWNLOAD_CONTEXT_DETAILS"); path != "" {
			raw, _ := json.Marshal(map[string]any{"site_id": "owned-context-site", "downloader_id": "owned-context-qb", "directory": destination, "browser": samples["Browser"], "plugin": manifest.PluginID, "config": map[string]any{"enabled": true, "mediainfo": media}, "source_pid": sourceAfter})
			if os.WriteFile(path, raw, 0600) != nil {
				t.Fatal("browser details")
			}
		}
	} else {
		success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
		request("GET", "/api/v1/plugin/"+manifest.PluginID+"/state", nil, 404)
	}
	mu.Lock()
	actualAdds := adds
	mu.Unlock()
	if actualAdds != wantAdds {
		t.Fatal("veto/escape performed Add", actualAdds)
	}
	t.Logf("actual download Context host: normal/veto/error/escape, source+clone %d/%d→%d/%d,%d independent Add tasks,cold no replay", sourceBefore, cloneBefore, sourceAfter, cloneAfter, actualAdds)
}
