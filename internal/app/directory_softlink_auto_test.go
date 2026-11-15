package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/download"
)

func saveSoftlinkDirectoryAPI(t *testing.T, a *App, cookie *http.Cookie, source, target, monitor string, enabled bool) DirectoryRule {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"directories": []any{map[string]any{"name": "actual softlink rule", "source_path": source, "library_path": target, "media_type": "movie", "mode": "softlink", "monitor_type": monitor, "enabled": enabled, "include_existing": true, "renaming": false, "scraping": false, "library_type_folder": false, "overwrite_mode": "never", "overwrite_mode_known": true}}})
	r := callTest(t, a.Handler(), "PUT", "/api/directories/manage", string(body), cookie, "")
	if r.Code != 200 {
		t.Fatal("save actual directory API", r.Code, r.Body.String())
	}
	rules := a.Settings().Directories
	if len(rules) != 1 || rules[0].Mode != "softlink" || rules[0].Enabled != enabled || rules[0].MonitorType != monitor {
		t.Fatal("persisted directory mode/gate", rules)
	}
	r = callTest(t, a.Handler(), "GET", "/api/directories/manage", "", cookie, "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"mode":"softlink"`) {
		t.Fatal("directory card reload", r.Code, r.Body.String())
	}
	return rules[0]
}

func TestDirectorySoftlinkActualSaveAndAutomaticPublication(t *testing.T) {
	for _, branch := range []string{"downloader-video-subtitle", "monitor-bluray"} {
		t.Run(branch, func(t *testing.T) {
			a, source, target := importTestApp(t)
			nativeMoveFixture(t, a)
			cookie := setupTestAdmin(t, a)
			observer := installOwnedTransferObserverWithCookie(t, a, cookie, nil, nil, map[string]any{"storage": true, "storage_root": filepath.Dir(source)})
			var path string
			contents := map[string][]byte{}
			monitor := "monitor"
			if branch == "monitor-bluray" {
				path = filepath.Join(source, "Test Film (2024).BluRay")
				contents[filepath.Join(path, "BDMV/STREAM/00001.m2ts")] = []byte("actual automatic disc stream")
				contents[filepath.Join(path, "BDMV/index.bdmv")] = []byte("actual disc index")
				contents[filepath.Join(path, "movie.nfo")] = []byte(`<movie><title>Test Film</title><uniqueid type="tmdb" default="true">123</uniqueid></movie>`)
			} else {
				monitor = "downloader"
				path = filepath.Join(source, "Test Film (2024).mkv")
				contents[path] = []byte("actual automatic movie")
				contents[strings.TrimSuffix(path, ".mkv")+".zh.srt"] = []byte("1\n00:00:01,000 --> 00:00:02,000\nowned subtitle\n")
				contents[strings.TrimSuffix(path, ".mkv")+".nfo"] = []byte(`<movie><title>Test Film</title><uniqueid type="tmdb" default="true">123</uniqueid></movie>`)
			}
			for path, data := range contents {
				importWrite(t, path, data)
			}
			before, _, err := snapshotOriginalDirectory(source)
			if err != nil {
				t.Fatal(err)
			}
			var complete atomic.Bool
			{
				const hash = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
				qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" {
						t.Errorf("automatic rule mutated downloader: %s", r.Method)
						http.Error(w, "read only", 405)
						return
					}
					switch r.URL.Path {
					case "/api/v2/torrents/info":
						progress := 0.5
						state := "downloading"
						if complete.Load() {
							progress = 1
							state = "uploading"
						}
						var size int
						for _, data := range contents {
							size += len(data)
						}
						task := workflowTask(hash, progress)
						task["save_path"], task["state"], task["total_size"], task["completed"] = source, state, size, int(float64(size)*progress)
						workflowJSON(w, []any{task})
					case "/api/v2/torrents/files":
						rows := []any{}
						progress := 0.5
						if complete.Load() {
							progress = 1
						}
						i := 0
						for name, data := range contents {
							rows = append(rows, map[string]any{"index": i, "name": strings.TrimPrefix(name, source+string(filepath.Separator)), "size": len(data), "progress": progress})
							i++
						}
						workflowJSON(w, rows)
					default:
						http.NotFound(w, r)
					}
				}))
				defer qb.Close()
				a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "actual-qb", Kind: "qb", BaseURL: qb.URL}, SharedPaths: true}}
			}
			rule := saveSoftlinkDirectoryAPI(t, a, cookie, source, target, monitor, true)
			directoryPoll(t, a, rule)
			directoryAge(t, a, rule, path)
			{
				directoryPoll(t, a, rule)
				observed := directoryObservation(t, a, rule, path)
				if observed.JobID != "" || observed.State != "downloading" {
					t.Fatal("incomplete real downloader file entered queue", observed)
				}
				complete.Store(true)
				directoryAge(t, a, rule, path)
			}
			directoryPoll(t, a, rule)
			observed := directoryRecognize(t, a, rule, path)
			if observed.PlanID == "" || observed.State != "queued" {
				t.Fatal("automatic recognition did not queue import", observed)
			}
			var plan ImportPlan
			if err = a.Store.Get(t.Context(), "import_plan", observed.PlanID, &plan); err != nil || len(plan.Files) != 1 || plan.Mode != "softlink" || plan.Files[0].Method != "softlink" || plan.Files[0].Status != "ready" {
				t.Fatal("automatic mode/whole-disc plan", plan, err)
			}
			if (plan.Files[0].DirectoryDigest != "") != (monitor == "monitor") {
				t.Fatal("disc root lost to individual streams", plan.Files)
			}
			runSoftlinkDirectoryJob(t, a, observed.JobID)
			if err = a.Store.Get(t.Context(), "import_plan", observed.PlanID, &plan); err != nil {
				t.Fatal(err)
			}
			var asset Asset
			if err = a.Store.Get(t.Context(), "asset", plan.Files[0].AssetID, &asset); err != nil {
				t.Fatal("actual automatic asset", err)
			}
			var owned managedFile
			if err = a.Store.Get(t.Context(), "managed_file", tokenHash(asset.Path), &owned); err != nil || owned.Softlink == nil {
				t.Fatal("actual automatic link ownership", owned, err)
			}
			if _, err = snapshotOwnedTransferPath(asset.Path, owned.Snapshot, owned.Softlink, true); err != nil {
				t.Fatal("actual target entry/ref", err)
			}
			if monitor == "downloader" {
				if text, e := os.Readlink(asset.Path); e != nil || text != path {
					t.Fatal("real automatic video link", text, e)
				}
				var count int
				if err = a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='managed_file' AND json_extract(payload,'$.asset_id')=? AND json_extract(payload,'$.kind')='subtitle' AND json_type(payload,'$.softlink')='object'`, asset.ID).Scan(&count); err != nil || count != 1 {
					t.Fatal("automatic subtitle link missing", count, err)
				}
			} else {
				if owned.Softlink.Directory == nil || len(owned.Softlink.Directory.Members) != len(contents) {
					t.Fatal("automatic disc per-leaf links", owned)
				}
				_, cached, e := a.directoryObservations(t.Context(), rule.ID)
				if e != nil || len(cached) != 0 {
					t.Fatal("disc aggregate became cached torrent leaf evidence", cached, e)
				}
				for relative, member := range owned.Softlink.Directory.Members {
					if member.Softlink == nil || member.Softlink.Source != filepath.Join(path, relative) {
						t.Fatal("disc leaf reference", relative, member)
					}
				}
			}
			page := historyParityReadPage(t, a, "")
			wantHistory := 1
			if monitor == "downloader" {
				wantHistory = 2
			}
			if page.Total != wantHistory {
				t.Fatal("automatic media/related history", page)
			}
			found := false
			for _, item := range page.Items {
				if item.Status != "success" {
					t.Fatal("automatic history outcome", item)
				}
				if item.Source == path {
					found = true
				}
			}
			if !found {
				t.Fatal("automatic primary source history", page)
			}
			if now, _, e := snapshotOriginalDirectory(source); e != nil || now != before {
				t.Fatal("automatic softlink changed source", now, e)
			}
			for name, data := range contents {
				if got, e := os.ReadFile(name); e != nil || string(got) != string(data) {
					t.Fatal("source bytes changed", name, e)
				}
			}
			wantCalls := 2
			if monitor == "monitor" {
				wantCalls = len(contents)
			}
			if calls := sharedModeSDKCalls(t, a, observer, "softlink"); len(calls) != wantCalls {
				t.Fatal("selected Source actor media/children count", calls)
			}
			directoryPoll(t, a, rule)
			after := directoryObservation(t, a, rule, path)
			if after.State != "imported" || after.PlanID != plan.ID {
				t.Fatal("automatic history gate requeued original source", after)
			}
		})
	}
}

func TestDirectorySoftlinkDisabledAndNoneKeepAutomationClosed(t *testing.T) {
	for _, monitor := range []string{"monitor", "none"} {
		t.Run(monitor, func(t *testing.T) {
			a, source, target := importTestApp(t)
			cookie := setupTestAdmin(t, a)
			path := directoryMovieFile(t, source)
			enabled := monitor == "none"
			rule := saveSoftlinkDirectoryAPI(t, a, cookie, source, target, monitor, enabled)
			if err := a.pollDirectoryFiles(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := a.pollDirectoryFileRule(t.Context(), rule); fileCode(err) != "DIRECTORY_MONITOR_DISABLED" {
				t.Fatal("direct old-rule invocation bypassed gate", err)
			}
			var count int
			if err := a.Store.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind IN ('organize_file','import')`).Scan(&count); err != nil || count != 0 {
				t.Fatal("disabled or none queued work", count, err)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != "synthetic monitor video" {
				t.Fatal("disabled rule changed source", err)
			}
		})
	}
}
