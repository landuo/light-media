package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/download"
)

const coreFileHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type coreDeleteDaemon struct {
	mu                          sync.Mutex
	name, path, mode, tag, kind string
	present                     bool
	deletes                     int
	requests                    []string
}

func (d *coreDeleteDaemon) handle(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.requests = append(d.requests, r.URL.Path)
		if d.kind == "qb" {
			switch r.URL.Path {
			case "/api/v2/torrents/info":
				rows := []any{}
				if d.present {
					rows = append(rows, map[string]any{"hash": coreFileHash, "name": d.name, "state": "uploading", "save_path": d.path, "content_path": filepath.Join(d.path, d.name), "total_size": 1024, "completed": 1024, "progress": 1, "ratio": 1, "tags": d.tag, "upspeed": 1024, "dlspeed": 0})
				}
				workflowJSON(w, rows)
			case "/api/v2/torrents/files":
				workflowJSON(w, []any{map[string]any{"name": d.name, "size": 1024, "progress": 1}})
			case "/api/v2/torrents/delete":
				if r.Method != "POST" {
					t.Error("mutation method")
				}
				_ = r.ParseForm()
				if r.Form.Get("hashes") != coreFileHash || r.Form.Get("deleteFiles") != "false" {
					t.Error("unsafe delete wire")
				}
				d.deletes++
				if d.mode == "reject" {
					http.Error(w, "synthetic API refusal", 500)
					return
				}
				if d.mode != "keep" {
					d.present = false
				}
				if d.mode == "lost" {
					c, _, e := w.(http.Hijacker).Hijack()
					if e == nil {
						_ = c.Close()
					}
					return
				}
			default:
				t.Error("unexpected qB", r.URL.Path)
				http.NotFound(w, r)
			}
			return
		}
		var in map[string]any
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		if dec.Decode(&in) != nil {
			t.Error("TR body")
			return
		}
		method, _ := in["method"].(string)
		args, _ := in["params"].(map[string]any)
		id := in["id"]
		if args == nil {
			args, _ = in["arguments"].(map[string]any)
			id = in["tag"]
		}
		modern := strings.Contains(method, "_")
		if method == "session-get" || method == "torrent-get" || method == "torrent-remove" {
			modern = false
		}
		reply := func(v any) {
			if modern {
				workflowJSON(w, map[string]any{"jsonrpc": "2.0", "id": id, "result": v})
			} else {
				workflowJSON(w, map[string]any{"tag": id, "result": "success", "arguments": v})
			}
		}
		switch method {
		case "session_get", "session-get":
			reply(map[string]any{"version": "4.1.0"})
		case "torrent_get", "torrent-get":
			rows := []any{}
			if d.present {
				rows = append(rows, map[string]any{"id": 3, "hash_string": coreFileHash, "name": d.name, "status": 6, "download_dir": d.path, "total_size": 1024, "left_until_done": 0, "percent_done": 1, "labels": []string{d.tag}, "error": 0, "files": []any{map[string]any{"name": d.name, "length": 1024, "bytes_completed": 1024}}})
			}
			reply(map[string]any{"torrents": rows})
		case "torrent_remove", "torrent-remove":
			ids, _ := args["ids"].([]any)
			if len(ids) != 1 || ids[0] != coreFileHash || args["delete_local_data"] != false {
				t.Error("unsafe TR remove wire", args)
			}
			d.deletes++
			if d.mode == "reject" {
				workflowJSON(w, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -1, "message": "synthetic reject"}})
				return
			}
			if d.mode != "keep" {
				d.present = false
			}
			if d.mode == "lost" {
				c, _, e := w.(http.Hijacker).Hijack()
				if e == nil {
					_ = c.Close()
				}
				return
			}
			reply(map[string]any{})
		default:
			t.Error("unexpected TR", method)
			http.Error(w, "bad", 500)
		}
	})
}

func coreHistoryAccepted(t *testing.T, a *App, item transferHistoryItem, d DownloaderConfig) {
	t.Helper()
	ctx := context.Background()
	var plan ImportPlan
	if e := a.Store.Get(ctx, "import_plan", item.PlanID, &plan); e != nil {
		t.Fatal(e)
	}
	in := Intent{ID: "core-owned-accepted", WorkflowID: plan.WorkflowID, Media: plan.Media, RootID: plan.RootID, DownloaderID: d.ID, DownloaderSnapshot: downloaderSnapshot(d), Hash: coreFileHash, State: "accepted", Purpose: "core_download", ImportPlanID: plan.ID, SiteID: "core-owned-site"}
	plan.IntentID = in.ID
	if e := a.Store.Put(ctx, "import_plan", plan.ID, plan); e != nil {
		t.Fatal(e)
	}
	if e := a.acceptDownload(ctx, in, Job{ID: "job_owned_accepted"}); e != nil {
		t.Fatal(e)
	}
	in.State = "imported"
	if e := a.Store.Put(ctx, "intent", in.ID, in); e != nil {
		t.Fatal(e)
	}
}
func coreDeleteHistoryHTTP(t *testing.T, a *App, item transferHistoryItem) {
	t.Helper()
	raw, _ := json.Marshal(historyDeleteRequest{Confirm: true, DeleteSource: true, FileID: item.executionFileID()})
	_, err := a.transferHistoryAPI(httptest.NewRequest("DELETE", "/api/history/transfers/"+item.ID, bytes.NewReader(raw)), "transfers/"+item.ID)
	if err != nil {
		t.Fatal(err)
	}
}
func coreDeleteJob(t *testing.T, a *App) Job {
	t.Helper()
	j, e := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='download_file_deleted' ORDER BY created_at LIMIT 1`))
	if e != nil {
		t.Fatal(e)
	}
	j.State = "running"
	j.Generation++
	_, e = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=? WHERE id=?`, j.Generation, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func coreDeleteRecord(t *testing.T, a *App, j Job) coreDownloadRemoval {
	t.Helper()
	var p struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(j.Payload, &p)
	r, e := a.readCoreDownloadRemoval(context.Background(), a.Store.DB, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestCoreDownloadFileDeletedActualHistoryNativeHTTP(t *testing.T) {
	for _, kind := range []string{"qb", "tr"} {
		for _, mode := range []string{"success", "reject", "lost", "keep"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				a, item := historyRemovalFixture(t)
				daemon := &coreDeleteDaemon{kind: kind, name: filepath.Base(item.Source), path: filepath.Dir(item.Source), mode: mode, tag: "light-media", present: true}
				s := httptest.NewServer(daemon.handle(t))
				defer s.Close()
				d := DownloaderConfig{Config: download.Config{ID: "owned", Kind: kind, BaseURL: s.URL}, Name: "自有合成下载器"}
				a.config.Downloaders = []DownloaderConfig{d}
				a.config.DefaultDownloader = d.ID
				coreHistoryAccepted(t, a, item, d)
				if e := a.saveSettings(context.Background(), a.Settings()); e != nil {
					t.Fatal(e)
				}
				coreDeleteHistoryHTTP(t, a, item)
				if _, e := os.Stat(item.Source); !os.IsNotExist(e) {
					t.Fatal("source not removed", e)
				}
				if _, e := os.Stat(item.Target); e != nil {
					t.Fatal("library hardlink lost", e)
				}
				j := coreDeleteJob(t, a)
				e := a.safeRun(context.Background(), j)
				r := coreDeleteRecord(t, a, j)
				want := map[string]string{"success": "confirmed", "reject": "unknown", "lost": "unknown", "keep": "acknowledged"}[mode]
				if r.State != want {
					t.Fatalf("state %s want %s err %v", r.State, want, e)
				}
				if (mode == "reject" || mode == "lost") != (e != nil) {
					t.Fatal("unknown not review", e)
				}
				if r.SourceReturn == nil || *r.SourceReturn != (mode == "success" || mode == "keep") {
					t.Fatal("source return fact", r.SourceReturn)
				}
				data, e := a.downloadDeletedSourceData(context.Background(), a.Store.DB, r.EventID)
				if e != nil {
					t.Fatal(e)
				}
				if data["hash"] != coreFileHash || len(data["torrents"].([]map[string]any)) != 1 {
					t.Fatal("source event snapshot")
				}
				var count int
				if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='download.deleted'`).Scan(&count); e != nil || count != 1 {
					t.Fatal(count, e)
				}
				_ = a.safeRun(context.Background(), j)
				dir := a.Dir
				if e = a.Close(); e != nil {
					t.Fatal(e)
				}
				cold, e := New(Options{DataDir: dir})
				if e != nil {
					t.Fatal(e)
				}
				defer cold.Close()
				_ = cold.safeRun(context.Background(), coreColdClaim(t, cold, j.ID))
				if _, e = cold.downloadDeletedSourceData(context.Background(), cold.Store.DB, r.EventID); e != nil {
					t.Fatal("cold source read", e)
				}
				daemon.mu.Lock()
				deletes := daemon.deletes
				daemon.mu.Unlock()
				if deletes != 1 {
					t.Fatal("mutation replay", deletes)
				}
				if _, e = os.Stat(item.Target); e != nil {
					t.Fatal("delete-data false not preserved", e)
				}
			})
		}
	}
}

func TestCoreDownloadFileDeletedSourceNoOpAndDefaultRouting(t *testing.T) {
	t.Run("src-only", func(t *testing.T) {
		a, item := historyRemovalFixture(t)
		coreDeleteHistoryHTTP(t, a, item)
		var n int
		_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='download_file_deleted'`).Scan(&n)
		if n != 0 {
			t.Fatal("src inferred hash")
		}
		var key string
		if e := a.Store.DB.QueryRow(`SELECT file_event_id FROM plugin_download_file_events WHERE file_event_id LIKE 'hfe_%'`).Scan(&key); e != nil {
			t.Fatal(e)
		}
		p, e := a.pluginStore.downloadFileEvent(context.Background(), a.Store.DB, key)
		if e != nil || p.Hash != "" {
			t.Fatal("source event", e)
		}
	})
	for _, tag := range []string{"light-media", "MOVIEPILOT"} {
		t.Run("global-default-"+tag, func(t *testing.T) {
			a, item := historyRemovalFixture(t)
			source := &coreDeleteDaemon{kind: "tr", name: filepath.Base(item.Source), path: filepath.Dir(item.Source), tag: tag, present: true}
			target := &coreDeleteDaemon{kind: "qb", name: source.name, path: source.path, tag: tag, present: false}
			s1 := httptest.NewServer(source.handle(t))
			defer s1.Close()
			s2 := httptest.NewServer(target.handle(t))
			defer s2.Close()
			d := DownloaderConfig{Config: download.Config{ID: "source", Kind: "tr", BaseURL: s1.URL}, Name: "来源"}
			other := DownloaderConfig{Config: download.Config{ID: "default", Kind: "qb", BaseURL: s2.URL}, Name: "默认"}
			a.config.Downloaders = []DownloaderConfig{d, other}
			a.config.DefaultDownloader = other.ID
			coreHistoryAccepted(t, a, item, d)
			coreDeleteHistoryHTTP(t, a, item)
			j := coreDeleteJob(t, a)
			if e := a.safeRun(context.Background(), j); e != nil {
				t.Fatal(e)
			}
			r := coreDeleteRecord(t, a, j)
			source.mu.Lock()
			srcDeletes := source.deletes
			source.mu.Unlock()
			target.mu.Lock()
			dstDeletes := target.deletes
			target.mu.Unlock()
			if srcDeletes != 0 {
				t.Fatal("deleted matched nondefault")
			}
			if tag == "light-media" {
				if dstDeletes != 1 || len(r.Torrents) != 1 || r.DefaultID != "default" || r.State != "already_absent" {
					t.Fatal("default routing", r, dstDeletes)
				}
			} else if dstDeletes != 0 || r.State != "no_match" || r.EventID != "" {
				t.Fatal("adopted old tag", r, dstDeletes)
			}
		})
	}
}

func TestCoreDownloadFileDeletedUnknownEvidenceAndConfigGuard(t *testing.T) {
	for _, kind := range []string{"config-drift", "receipt", "root", "source", "accepted", "event", "chain", "dispatching"} {
		t.Run(kind, func(t *testing.T) {
			a, item := historyRemovalFixture(t)
			daemon := &coreDeleteDaemon{kind: "qb", name: filepath.Base(item.Source), path: filepath.Dir(item.Source), tag: "light-media", present: true}
			s := httptest.NewServer(daemon.handle(t))
			defer s.Close()
			d := DownloaderConfig{Config: download.Config{ID: "owned", Kind: "qb", BaseURL: s.URL}}
			a.config.Downloaders = []DownloaderConfig{d}
			coreHistoryAccepted(t, a, item, d)
			coreDeleteHistoryHTTP(t, a, item)
			j := coreDeleteJob(t, a)
			r := coreDeleteRecord(t, a, j)
			switch kind {
			case "config-drift":
				a.config.Downloaders[0].BaseURL += "/changed"
			case "receipt":
				_, _ = a.Store.DB.Exec(`DELETE FROM records WHERE kind='core_download_removal' AND id=?`, r.ID)
			case "root":
				_, _ = a.Store.DB.Exec(`DELETE FROM records WHERE kind='core_download_removal_root' AND id=?`, r.ID)
			case "source":
				_, _ = a.Store.DB.Exec(`DELETE FROM records WHERE kind='history_download_file_event' AND id=?`, r.ID)
			case "accepted":
				_, _ = a.Store.DB.Exec(`DELETE FROM records WHERE kind='download_added'`)
			case "event":
				_, _ = a.Store.DB.Exec(`DELETE FROM domain_events WHERE event_id=?`, domainEventID("downloadfile.deleted", r.ID))
			case "chain":
				_, _ = a.Store.DB.Exec(`DELETE FROM records WHERE kind='core_download_removal_chain' AND substr(id,1,69)=?`, r.ID+":")
			case "dispatching":
				r.State = "dispatching"
				if e := a.commitCoreDownloadRemoval(context.Background(), r, false); e != nil {
					t.Fatal(e)
				}
			}
			if e := a.safeRun(context.Background(), j); e == nil {
				t.Fatal("missing boundary accepted")
			}
			daemon.mu.Lock()
			n := daemon.deletes
			daemon.mu.Unlock()
			if n != 0 {
				t.Fatal("unsafe dispatch", n)
			}
		})
	}
}

func TestCoreDownloadFileDeletedActualSourceSnapshotOracle(t *testing.T) {
	a := newTestApp(t)
	var oracle struct {
		Snapshots []struct {
			Kind     string
			Input    map[string]any
			Expected []map[string]any
		}
	}
	raw, e := os.ReadFile("../../testdata/core-download-file-deleted/source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &oracle); e != nil {
		t.Fatal(e)
	}
	for _, c := range oracle.Snapshots {
		t.Run(c.Kind, func(t *testing.T) {
			if c.Kind == "tr" {
				c.Input["percentDone"] = 1
			}
			payload, _ := json.Marshal([]any{c.Input})
			d := DownloaderConfig{Config: download.Config{ID: "owned", Kind: c.Kind}}
			got, e := a.coreDeletedTorrentSnapshots(context.Background(), d, coreFileHash, payload)
			if e != nil {
				t.Fatal(e)
			}
			b, _ := json.Marshal(got)
			var normalized []map[string]any
			_ = json.Unmarshal(b, &normalized)
			if !reflect.DeepEqual(normalized, c.Expected) {
				t.Fatalf("source snapshot mismatch got=%s want=%v", b, c.Expected)
			}
		})
	}
}

func TestCoreDownloadFileDeletedActualAddImportHistoryChain(t *testing.T) {
	a, source, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	ctx := context.Background()
	filename := "测试影片 (2024).mkv"
	torrent, hash := workflowTorrent(filename)
	var mu sync.Mutex
	present := false
	adds, deletes := 0, 0
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			if hashes, ok := r.URL.Query()["hashes"]; ok && (len(hashes) != 1 || hashes[0] != hash) {
				t.Error("widened source query")
			}
			rows := []any{}
			if present {
				row := workflowTask(hash, 1)
				row["name"] = filename
				row["save_path"] = source
				row["tags"] = "light-media"
				rows = append(rows, row)
			}
			workflowJSON(w, rows)
		case "/api/v2/torrents/files":
			workflowJSON(w, []any{map[string]any{"name": filename, "size": 1024, "progress": 1}})
		case "/api/v2/torrents/add":
			if e := r.ParseMultipartForm(4 << 20); e != nil {
				t.Error(e)
			}
			f, _, e := r.FormFile("torrents")
			if e != nil {
				t.Error(e)
				return
			}
			b, _ := io.ReadAll(f)
			_ = f.Close()
			if !bytes.Equal(b, torrent) || r.FormValue("savepath") != source || r.FormValue("tags") != "light-media" {
				t.Error("actual add input")
			}
			adds++
			present = true
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/torrents/delete":
			_ = r.ParseForm()
			if r.Form.Get("hashes") != hash || r.Form.Get("deleteFiles") != "false" {
				t.Error("keep-data wire")
			}
			deletes++
			present = false
		default:
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			_, _ = io.WriteString(w, workflowHTML("测试影片.2024.1080p.WEB-DL.x264-TEST", "", false))
		case "/download.php":
			_, _ = w.Write(torrent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	workflowConfigure(t, a, site.URL, qb.URL)
	a.config.Downloaders[0].DownloadPath = source
	a.config.Directories = []DirectoryRule{{ID: "owned-core-source", Name: "自有整链目录", SourcePath: source, LibraryPath: a.Settings().Roots[0].Path, RootID: "root", MediaType: "movie", Mode: "hardlink", MonitorType: "downloader", Enabled: true}}
	preview, e := a.previewDownload(ctx, PreviewRequest{SearchRequest: SearchRequest{MediaType: m.MediaType, TMDBID: m.ID}, SiteID: "fixture-site", ResourceID: "101", DownloadPath: source}, false)
	if e != nil {
		t.Fatal(e)
	}
	in, e := a.confirmDownload(ctx, preview.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	j, e := a.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.safeRun(ctx, j); e != nil {
		t.Fatal(e)
	}
	_, _ = a.Store.DB.Exec(`UPDATE jobs SET state='succeeded' WHERE id=?`, j.ID)
	importWrite(t, filepath.Join(source, filename), bytes.Repeat([]byte{'v'}, 1024))
	if e = a.pollDownloads(ctx); e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Get(ctx, "intent", in.ID, &in); e != nil || in.ImportPlanID == "" {
		t.Fatal("completed download plan", in.Error, e)
	}
	importJob, e := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE kind='import' AND json_extract(payload,'$.id')=?`, in.ImportPlanID))
	if e != nil {
		t.Fatal(e)
	}
	importJob.State = "running"
	importJob.Generation++
	_, _ = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=? WHERE id=?`, importJob.Generation, importJob.ID)
	if e = a.safeRun(ctx, importJob); e != nil {
		t.Fatal(e)
	}
	_, _ = a.Store.DB.Exec(`UPDATE jobs SET state='succeeded' WHERE id=?`, importJob.ID)
	var plan ImportPlan
	_ = a.Store.Get(ctx, "import_plan", in.ImportPlanID, &plan)
	if len(plan.Files) != 1 {
		t.Fatal("import coverage")
	}
	item, e := a.transferHistoryItem(ctx, plan.Files[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	coreDeleteHistoryHTTP(t, a, item)
	deleteJob := coreDeleteJob(t, a)
	if e = a.safeRun(ctx, deleteJob); e != nil {
		t.Fatal(e)
	}
	r := coreDeleteRecord(t, a, deleteJob)
	data, e := a.downloadDeletedSourceData(ctx, a.Store.DB, r.EventID)
	if e != nil || data["hash"] != hash || r.State != "confirmed" {
		t.Fatal("final source fact", r.State, e)
	}
	mu.Lock()
	addCount, deleteCount := adds, deletes
	mu.Unlock()
	if addCount != 1 || deleteCount != 1 {
		t.Fatal("actual add/delete counts", addCount, deleteCount)
	}
	if _, e = os.Stat(item.Source); !os.IsNotExist(e) {
		t.Fatal("source not removed")
	}
	if _, e = os.Stat(item.Target); e != nil {
		t.Fatal("library lost", e)
	}
}

func TestCoreDownloadFileDeletedOldPairRollbackCannotReplay(t *testing.T) {
	a, item := historyRemovalFixture(t)
	daemon := &coreDeleteDaemon{kind: "qb", name: filepath.Base(item.Source), path: filepath.Dir(item.Source), mode: "lost", tag: "light-media", present: true}
	server := httptest.NewServer(daemon.handle(t))
	defer server.Close()
	d := DownloaderConfig{Config: download.Config{ID: "owned", Kind: "qb", BaseURL: server.URL}}
	a.config.Downloaders = []DownloaderConfig{d}
	coreHistoryAccepted(t, a, item, d)
	coreDeleteHistoryHTTP(t, a, item)
	j := coreDeleteJob(t, a)
	r := coreDeleteRecord(t, a, j)
	var receipt, root []byte
	_ = a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='core_download_removal' AND id=?`, r.ID).Scan(&receipt)
	_ = a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='core_download_removal_root' AND id=?`, r.ID).Scan(&root)
	if e := a.safeRun(context.Background(), j); pluginErrorCode(e) != "CORE_DOWNLOAD_DELETE_REVIEW" {
		t.Fatal(e)
	}
	_, _ = a.Store.DB.Exec(`UPDATE records SET payload=? WHERE kind='core_download_removal' AND id=?`, receipt, r.ID)
	_, _ = a.Store.DB.Exec(`UPDATE records SET payload=? WHERE kind='core_download_removal_root' AND id=?`, root, r.ID)
	if e := a.safeRun(context.Background(), j); e == nil {
		t.Fatal("rolled-back planned pair accepted")
	}
	daemon.mu.Lock()
	count := daemon.deletes
	daemon.mu.Unlock()
	if count != 1 {
		t.Fatal("unknown mutation replay", count)
	}
}

func TestCoreDownloadFileDeletedQueuedSecondSourceCannotReplayUnknownHash(t *testing.T) {
	a, source, _ := importTestApp(t)
	m := importFixture(t, a, "tv")
	ctx := context.Background()
	names := []string{"History.S01E01.mkv", "History.S01E02.mkv"}
	importCache(t, a, "season:123:1", catalog.Season{SeriesID: 123, SeasonNumber: 1, Episodes: []catalog.Episode{{ID: 101, SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2024-01-01"}, {ID: 102, SeasonNumber: 1, EpisodeNumber: 2, AirDate: "2024-01-01"}}})
	requests := []ImportFileRequest{}
	for i, name := range names {
		p := filepath.Join(source, name)
		importWrite(t, p, bytes.Repeat([]byte{'v'}, 1024))
		requests = append(requests, ImportFileRequest{Path: p, Season: 1, Episodes: []int{i + 1}})
	}
	var mu sync.Mutex
	deletes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			workflowJSON(w, []any{map[string]any{"hash": coreFileHash, "name": "History (2024)", "state": "uploading", "save_path": source, "total_size": 2048, "completed": 2048, "progress": 1, "tags": "light-media", "ratio": 1}})
		case "/api/v2/torrents/files":
			workflowJSON(w, []any{map[string]any{"name": names[0], "size": 1024, "progress": 1}, map[string]any{"name": names[1], "size": 1024, "progress": 1}})
		case "/api/v2/torrents/delete":
			_ = r.ParseForm()
			if r.Form.Get("deleteFiles") != "false" || r.Form.Get("hashes") != coreFileHash {
				t.Error("unsafe wire")
			}
			deletes++
			http.Error(w, "synthetic unknown mutation result", 500)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	d := DownloaderConfig{Config: download.Config{ID: "owned", Kind: "qb", BaseURL: server.URL}}
	a.config.Downloaders = []DownloaderConfig{d}
	a.config.DefaultDownloader = d.ID
	plan, e := a.previewImport(ctx, ImportRequest{MediaType: m.MediaType, TMDBID: m.ID, RootID: "root", Mode: "hardlink", Files: requests})
	if e != nil {
		t.Fatal(e)
	}
	coreHistoryAccepted(t, a, transferHistoryItem{PlanID: plan.ID}, d)
	j, e := a.executeImport(ctx, plan.ID, nil)
	if e != nil {
		t.Fatal(e)
	}
	j.State = "running"
	j.Generation++
	_, _ = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=? WHERE id=?`, j.Generation, j.ID)
	if e = a.safeRun(ctx, j); e != nil {
		t.Fatal(e)
	}
	_, _ = a.Store.DB.Exec(`UPDATE jobs SET state='succeeded' WHERE id=?`, j.ID)
	for _, f := range plan.Files {
		item, e := a.transferHistoryItem(ctx, f.ID)
		if e != nil {
			t.Fatal(e)
		}
		coreDeleteHistoryHTTP(t, a, item)
	}
	first := coreDeleteJob(t, a)
	if e = a.safeRun(ctx, first); pluginErrorCode(e) != "CORE_DOWNLOAD_DELETE_REVIEW" {
		t.Fatal(e)
	}
	second, e := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE kind='download_file_deleted' AND id!=?`, first.ID))
	if e != nil {
		t.Fatal(e)
	}
	second.State = "running"
	second.Generation++
	_, _ = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=? WHERE id=?`, second.Generation, second.ID)
	if e = a.safeRun(ctx, second); pluginErrorCode(e) != "CORE_DOWNLOAD_DELETE_REVIEW" {
		t.Fatal("second source was not blocked", e)
	}
	mu.Lock()
	count := deletes
	mu.Unlock()
	if count != 1 {
		t.Fatalf("second queued source replayed unresolved same hash: deletes=%d", count)
	}
	if r := coreDeleteRecord(t, a, second); r.State != "planned" || r.SourceReturned {
		t.Fatal("second source invented a dispatch", r.State)
	}
	if e = a.saveSettings(ctx, a.Settings()); e != nil {
		t.Fatal(e)
	}
	dir := a.Dir
	_ = a.Close()
	cold, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	if e = cold.safeRun(ctx, coreColdClaim(t, cold, second.ID)); pluginErrorCode(e) != "CORE_DOWNLOAD_DELETE_REVIEW" {
		t.Fatal("cold second source", e)
	}
	mu.Lock()
	count = deletes
	mu.Unlock()
	if count != 1 {
		t.Fatal("cold replay", count)
	}
	for _, f := range plan.Files {
		if _, e = os.Stat(f.Target); e != nil {
			t.Fatal("library part lost", e)
		}
	}
}

func coreColdClaim(t *testing.T, a *App, target string) Job {
	t.Helper()
	for i := 0; i < 20; i++ {
		j, e := a.claim(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if j.ID == target {
			return j
		}
		if _, e = a.finishJob(context.Background(), j, "failed", "OWNED_FIXTURE_UNUSED", "Other owned fixture jobs were not executed", j.RunAt); e != nil {
			t.Fatal(e)
		}
	}
	t.Fatal("owned core job not claimed")
	return Job{}
}

// Original list_torrents has no row-count cutoff. Keep the actual transport's
// byte/protocol limits, and prove a selected hash at the end is not discarded.
func TestCoreDownloadFileDeletedNative10001RowsKeepsFinalHashProjection(t *testing.T) {
	a := newTestApp(t)
	rows := make([]map[string]any, 10001)
	for i := range rows {
		hash := fmt.Sprintf("%040x", i+1)
		if i == len(rows)-1 {
			hash = coreFileHash
		}
		rows[i] = map[string]any{"hash": hash, "name": "History (2024).mkv", "state": "uploading", "save_path": "/owned/downloads", "progress": 1, "total_size": 1024, "completed": 1024, "tags": "light-media"}
	}
	payload, e := json.Marshal(rows)
	if e != nil {
		t.Fatal(e)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" || r.URL.Path != "/api/v2/torrents/info" || r.URL.Query().Get("hashes") != coreFileHash {
			t.Error("wrong source read scope")
		}
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	d := DownloaderConfig{Config: download.Config{ID: "owned", Kind: "qb", BaseURL: server.URL}}
	client, e := download.New(d.Config)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := client.SourceTorrentSnapshots(context.Background(), coreFileHash)
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := coreNativeRows(raw)
	if e != nil || len(parsed) != 10001 {
		t.Fatal("source rows capped", len(parsed), e)
	}
	projected, e := a.coreDeletedTorrentSnapshots(context.Background(), d, coreFileHash, raw)
	if e != nil || len(projected) != 1 || projected[0]["hash"] != coreFileHash {
		t.Fatal("final selected row lost", len(projected), e)
	}
	if requests != 1 {
		t.Fatal("unexpected read/retry", requests)
	}
}

func TestCoreDownloadFileDeletedDomainContractRejectsForeignReferences(t *testing.T) {
	key := "dde_" + strings.Repeat("a", 64)
	base := DomainEvent{Type: "download.deleted", DownloadDeletedID: key, EventID: domainEventID("download.deleted", key)}
	cases := map[string]func(*DomainEvent){"site": func(e *DomainEvent) { e.SourceSiteID = 1 }, "notice": func(e *DomainEvent) { e.NoticeID = "pn_foreign" }, "file": func(e *DomainEvent) { e.FileEventID = "hfe_" + strings.Repeat("b", 64) }, "kind": func(e *DomainEvent) { e.SubjectKind = "file_event" }, "subject": func(e *DomainEvent) { e.SubjectID = "hfe_" + key[4:] }, "purpose": func(e *DomainEvent) { e.Purpose = "file_deletion" }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			event := base
			change(&event)
			if _, e := validateDomainEvent(event); e == nil {
				t.Fatal("foreign authority/shape accepted")
			}
		})
	}
	valid, e := validateDomainEvent(base)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = validateDomainEvent(valid); e != nil {
		t.Fatal("canonical source event rejected", e)
	}
}
