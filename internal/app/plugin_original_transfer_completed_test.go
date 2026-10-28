package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

// Owned wire peer, not a substitute for the migrated module dispatcher or
// native downloader client. Its journal records actual HTTP tag mutations.
type ownedCompletionPeer struct {
	mu         sync.Mutex
	Hash, Path string
	Progress   float64
	Tags       []string
	Writes     [][]string
	ReadHook   func()
	Fail       bool
	FailRead   bool
}

func TestOriginalTransferCompletedCancelledNativeResume(t *testing.T) {
	a, object, plan, cfg := originalTransferModuleFixture(t, "hardlink", "")
	ctx := context.Background()
	hold := filepath.Join(filepath.Dir(plan.Files[0].Source), "resume-release")
	cfg["modules"] = map[string]string{"recognize_media": "module_search_media", "transfer": "module_execute"}
	cfg["result"] = nil
	cfg["transfer_hold"] = hold
	encoded, _ := json.Marshal(cfg)
	if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, encoded); err != nil {
		t.Fatal(err)
	}
	if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	job := overwriteStart(t, a, plan)
	finished := make(chan error, 1)
	go func() { finished <- a.runImport(ctx, job, plan.ID, nil) }()
	entered := false
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
		if _, err := os.Stat(hold + ".entered"); err == nil {
			entered = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !entered {
		t.Fatal("actual SDK hold")
	}
	if _, err := a.Store.DB.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hold, []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; !originalTransferInterrupted(err) {
		t.Fatal("actual cancellation", err)
	}
	if _, err := os.Stat(plan.Files[0].Target); !os.IsNotExist(err) {
		t.Fatal("cancelled publication", err)
	}
	var count int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='original_transfer_callback'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("cancelled execution forged callback", count, err)
	}
	before := ownedModuleRead(t, object)
	current, err := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.retryOriginalScrapeJob(ctx, current); err != nil {
		t.Fatal("same UI retry action", err)
	}
	if _, err = a.Store.DB.ExecContext(ctx, `UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	job, err = scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err = a.runImport(ctx, job, plan.ID, nil); err != nil {
		t.Fatal("resume known native cancellation", err)
	}
	input, err := os.Stat(plan.Files[0].Source)
	output, e2 := os.Stat(plan.Files[0].Target)
	if err != nil || e2 != nil || !os.SameFile(input, output) {
		t.Fatal("resumed actual hardlink", err, e2)
	}
	if got := ownedModuleRead(t, object).Calls["module_execute"]; got != before.Calls["module_execute"] {
		t.Fatal("plugin decision replayed", got, before.Calls)
	}
	if err = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='original_transfer_callback'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("one successful physical callback", count, err)
	}
}

func (p *ownedCompletionPeer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch r.URL.Path {
	case "/transmission/rpc":
		var in struct {
			Method    string
			Params    map[string]any
			Arguments map[string]any
			ID        any
			Tag       any
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "bad rpc", 400)
			return
		}
		args := in.Params
		if args == nil {
			args = in.Arguments
		}
		value := map[string]any{}
		switch strings.ReplaceAll(in.Method, "-", "_") {
		case "session_get":
			value["version"] = "4.1.0"
		case "torrent_get":
			value["torrents"] = []any{map[string]any{"id": 1, "hash_string": p.Hash, "name": "Owned.2024.mkv", "download_dir": p.Path, "total_size": 22, "left_until_done": 0, "percent_done": p.Progress, "status": 6, "labels": p.Tags}}
		case "torrent_set":
			ids, _ := args["ids"].([]any)
			if len(ids) != 1 || ids[0] != p.Hash {
				http.Error(w, "wrong hash", 400)
				return
			}
			labels, _ := args["labels"].([]any)
			tags := []string{}
			for _, tag := range labels {
				s, ok := tag.(string)
				if !ok {
					http.Error(w, "bad label", 400)
					return
				}
				tags = append(tags, s)
			}
			p.Writes = append(p.Writes, tags)
			p.Tags = tags
		default:
			http.Error(w, "unexpected rpc", 400)
			return
		}
		if in.Params != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": in.ID, "result": value})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{"tag": in.Tag, "result": "success", "arguments": value})
		}
	case "/api/v2/torrents/info":
		if p.FailRead {
			http.Error(w, "owned query failure", 503)
			return
		}
		if p.ReadHook != nil {
			hook := p.ReadHook
			p.ReadHook = nil
			hook()
		}
		_ = json.NewEncoder(w).Encode([]any{map[string]any{"hash": p.Hash, "name": "Owned.2024.mkv", "save_path": p.Path, "content_path": p.Path + "/Owned.2024.mkv", "total_size": 22, "completed": 22, "progress": p.Progress, "state": "uploading", "tags": strings.Join(p.Tags, ",")}})
	case "/api/v2/torrents/addTags":
		if err := r.ParseForm(); err != nil || r.Form.Get("hashes") != p.Hash {
			http.Error(w, "wrong hash", 400)
			return
		}
		if p.Fail {
			http.Error(w, "owned write failure", 503)
			return
		}
		tags := strings.Split(r.Form.Get("tags"), ",")
		p.Writes = append(p.Writes, tags)
		p.Tags = append(p.Tags, tags...)
	default:
		http.NotFound(w, r)
	}
}

func TestOriginalTransferCompletedFixedSourceProgress(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/transfer-completed-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name     string
			Progress []any
			Marked   bool
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 11 {
		t.Fatal("fixed guard oracle", err)
	}
	for _, c := range oracle.Cases {
		switch c.Name {
		case "empty_result", "partial_download", "null_progress", "mixed_progress", "all_successful", "all_failed_but_done":
			t.Run(c.Name, func(t *testing.T) {
				rows := []any{}
				for _, n := range c.Progress {
					rows = append(rows, map[string]any{"progress": n})
				}
				for _, value := range []any{rows, map[string]any{plugins.ModuleTupleKey: rows}} {
					encoded, _ := json.Marshal(value)
					got, err := originalTorrentDownloadCompleted(encoded)
					if err != nil || got != c.Marked {
						t.Fatal("fixed completed predicate", got, err, c.Marked)
					}
				}
			})
		}
	}
}

func TestOriginalTransferCompletedActualImportSDK(t *testing.T) {
	for _, scenario := range []string{"success_none", "false_suppresses", "list_calls_native", "partial_download", "plugin_partial_tuple", "list_accumulates_incomplete", "failed_callback", "false_transfer_no_callback", "query_failure", "tag_write_failure", "transmission_preserves_labels"} {
		t.Run(scenario, func(t *testing.T) {
			a, object, plan, cfg := originalTransferModuleFixture(t, "hardlink", "")
			ctx := context.Background()
			peer := &ownedCompletionPeer{Hash: strings.Repeat("a", 40), Path: a.Settings().AllowedRoots[0], Progress: 1, Tags: []string{"light-media", "kept"}, Fail: scenario == "tag_write_failure"}
			if scenario == "partial_download" {
				peer.Progress = .99
			}
			if scenario == "query_failure" {
				peer.FailRead = true
			}
			if scenario == "transmission_preserves_labels" {
				peer.Tags = []string{" light-media ", "kept", "kept"}
			}
			server := httptest.NewServer(http.HandlerFunc(peer.serveHTTP))
			defer server.Close()
			settings := a.Settings()
			d := DownloaderConfig{Config: download.Config{ID: "owned-completion", Kind: "qb", BaseURL: server.URL}, Name: "Owned completion", DownloadPath: peer.Path}
			if scenario == "transmission_preserves_labels" {
				d.Kind = "tr"
			}
			settings.Downloaders = []DownloaderConfig{d}
			settings.DefaultDownloader = d.ID
			if err := a.saveSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			intent := Intent{ID: "intent_completion", Hash: peer.Hash, State: "downloaded", DownloaderID: d.ID, DownloaderSnapshot: downloaderSnapshot(d), DownloadPath: peer.Path, Media: plan.Media, RootID: plan.RootID, CreatedAt: now(), UpdatedAt: now()}
			if err := a.Store.Put(ctx, "intent", intent.ID, intent); err != nil {
				t.Fatal(err)
			}
			// Source download.py:919/955 freezes the module's returned name;
			// transfer.py:3487/3515 and the six callbacks inherit that value.
			var wantDownloader any = d.Name
			if scenario == "success_none" || scenario == "failed_callback" || scenario == "false_suppresses" {
				wantDownloader = "Original provider name"
				if scenario == "false_suppresses" {
					wantDownloader = nil
				}
				raw, _ := json.Marshal(wantDownloader)
				tx, err := a.Store.DB.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if err = a.saveDownloadContextTx(ctx, tx, downloadContextSnapshot{DownloadKind: "intent", DownloadID: intent.ID, Hash: intent.Hash, OriginalDownloader: raw}); err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			plan, err = a.previewImport(ctx, ImportRequest{IntentID: intent.ID, MediaType: plan.Media.MediaType, TMDBID: plan.Media.ID, RootID: plan.RootID, Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: plan.Files[0].Source}}})
			if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
				t.Fatal("sealed download preview", plan, err)
			}
			intent.ImportPlanID = plan.ID
			if err = a.Store.Put(ctx, "intent", intent.ID, intent); err != nil {
				t.Fatal(err)
			}
			modules := cfg["modules"].(map[string]string)
			modules["transfer_completed"] = "module_storage_parent"
			if scenario == "plugin_partial_tuple" || scenario == "list_accumulates_incomplete" {
				modules["list_torrents"] = "module_storage_siblings"
				rows := []any{map[string]any{"progress": 99.9}}
				cfg["storage_siblings"] = rows
				if scenario == "plugin_partial_tuple" {
					cfg["storage_siblings"] = map[string]any{plugins.ModuleTupleKey: rows}
				}
			}
			switch scenario {
			case "false_suppresses":
				cfg["storage_parent"] = false
			case "list_calls_native":
				cfg["storage_parent"] = []any{1}
			}
			if scenario == "failed_callback" {
				modules["transfer"] = "module_execute"
				cfg["result"] = map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": false, "message": "owned failure"}}
			}
			if scenario == "false_transfer_no_callback" {
				modules["transfer"] = "module_execute"
				cfg["result"] = false
			}
			encoded, _ := json.Marshal(cfg)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, encoded); err != nil {
				t.Fatal(err)
			}
			if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			job := overwriteStart(t, a, plan)
			runErr := a.runImport(ctx, job, plan.ID, nil)
			failed := scenario == "failed_callback" || scenario == "false_transfer_no_callback"
			if (runErr != nil) != failed {
				t.Fatal("actual transfer outcome", runErr)
			}
			state := "succeeded"
			if failed {
				state = "failed"
			}
			changed, err := a.finishJob(ctx, job, state, "", "", time.Now().Unix())
			if err != nil || !changed {
				t.Fatal("actual job finalization", changed, err)
			}
			observed := ownedModuleRead(t, object)
			a.wg.Wait()
			observed = ownedModuleRead(t, object)
			if scenario != "false_transfer_no_callback" {
				assertOwnedTransferDownloader(t, observed.TransferEvents, wantDownloader, intent.Hash)
			}
			wantCall := scenario != "partial_download" && scenario != "false_transfer_no_callback" && scenario != "plugin_partial_tuple" && scenario != "list_accumulates_incomplete" && scenario != "query_failure"
			if (observed.Calls["module_storage_parent"] == 1) != wantCall {
				t.Fatal("source completion hook", observed.Calls)
			}
			wantWrite := wantCall && scenario != "false_suppresses" && scenario != "tag_write_failure"
			peer.mu.Lock()
			writes := append([][]string{}, peer.Writes...)
			peer.mu.Unlock()
			wantTags := []string{"已整理"}
			if scenario == "transmission_preserves_labels" {
				wantTags = []string{"light-media", "kept", "已整理"}
			}
			if (len(writes) == 1) != wantWrite || wantWrite && !reflect.DeepEqual(writes[0], wantTags) {
				t.Fatal("actual native tags", writes, wantWrite)
			}
			if wantCall {
				var call struct{ Kwargs map[string]any }
				if json.Unmarshal(observed.Inputs["module_storage_parent"], &call) != nil || !reflect.DeepEqual(call.Kwargs, map[string]any{"hashs": peer.Hash, "downloader": d.Name}) {
					t.Fatal("fixed string selector and downloader name", call)
				}
			}
			// A duplicate terminal job commit cannot re-invoke ordinary effects.
			if changed, err = a.finishJob(ctx, job, state, "", "", time.Now().Unix()); err != nil || changed {
				t.Fatal("duplicate commit", changed, err)
			}
			if got := ownedModuleRead(t, object).Calls["module_storage_parent"]; got != observed.Calls["module_storage_parent"] {
				t.Fatal("replayed completion", got)
			}
			if scenario == "tag_write_failure" || scenario == "query_failure" {
				a.wg.Wait()
				if messages := a.systemMessages.get("system"); len(messages) != 0 {
					t.Fatal("source protocol helper must log without SystemError", string(messages))
				}
			}
			if scenario == "partial_download" || scenario == "tag_write_failure" || scenario == "query_failure" {
				peer.mu.Lock()
				peer.Progress = 1
				peer.Fail = false
				peer.FailRead = false
				peer.mu.Unlock()
				if err := a.pollDownloads(ctx); err != nil {
					t.Fatal("source all-history-hit retry", err)
				}
				peer.mu.Lock()
				count := len(peer.Writes)
				peer.mu.Unlock()
				if count != 1 {
					t.Fatal("actual retry tag", count)
				}
				if err := a.pollDownloads(ctx); err != nil {
					t.Fatal(err)
				}
				peer.mu.Lock()
				count = len(peer.Writes)
				peer.mu.Unlock()
				if count != 1 {
					t.Fatal("already tagged torrent replay", count)
				}
			}
		})
	}
}

func TestOriginalTransferCompletedPendingAndNewTask(t *testing.T) {
	for _, phase := range []string{"before_query", "during_query"} {
		t.Run(phase, func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			peer := &ownedCompletionPeer{Hash: strings.Repeat("a", 40), Path: t.TempDir(), Progress: 1, Tags: []string{"light-media"}}
			server := httptest.NewServer(http.HandlerFunc(peer.serveHTTP))
			defer server.Close()
			d := DownloaderConfig{Config: download.Config{ID: "guard-peer", Kind: "qb", BaseURL: server.URL}, Name: "Owned completion", DownloadPath: peer.Path}
			settings := a.Settings()
			settings.Downloaders = []DownloaderConfig{d}
			if err := a.saveSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			intent := Intent{ID: "guard-intent", Hash: peer.Hash, State: "imported", DownloaderID: d.ID, DownloaderSnapshot: downloaderSnapshot(d)}
			if err := a.Store.Put(ctx, "intent", intent.ID, intent); err != nil {
				t.Fatal(err)
			}
			plan := ImportPlan{ID: "pending-plan", IntentID: intent.ID}
			if err := a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
				t.Fatal(err)
			}
			var registerErr error
			register := func() { _, registerErr = a.enqueue(ctx, "import", map[string]any{"id": plan.ID}, "") }
			if phase == "before_query" {
				register()
			} else {
				peer.ReadHook = register
			}
			if err := a.markOriginalTorrentCompleted(ctx, intent); err != nil {
				t.Fatal(err)
			}
			if registerErr != nil {
				t.Fatal(registerErr)
			}
			peer.mu.Lock()
			count := len(peer.Writes)
			peer.mu.Unlock()
			if count != 0 {
				t.Fatal("unfinished/new task tagged", count)
			}
			if _, err := a.Store.DB.ExecContext(ctx, `UPDATE jobs SET state='failed' WHERE kind='import'`); err != nil {
				t.Fatal(err)
			}
			if err := a.markOriginalTorrentCompleted(ctx, intent); err != nil {
				t.Fatal(err)
			}
			peer.mu.Lock()
			count = len(peer.Writes)
			peer.mu.Unlock()
			if count != 1 {
				t.Fatal("source failed tasks count as done", count)
			}
		})
	}
}
