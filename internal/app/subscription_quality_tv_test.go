package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/metainfo"
	"light-media/internal/priorityrules"
)

func TestSubscriptionQualitySingleSeasonPackOnlyImprovesSelectedEpisodeThroughRealDownloadAndImport(t *testing.T) {
	for _, kind := range []string{"qb", "tr"} {
		t.Run(kind, func(t *testing.T) {
			a, source, _ := importTestApp(t)
			ctx := context.Background()
			m := importFixture(t, a, "tv")
			episodes := []catalog.Episode{}
			for ep := 1; ep <= 3; ep++ {
				episodes = append(episodes, catalog.Episode{ID: int64(ep), SeasonNumber: 1, EpisodeNumber: ep, AirDate: "2024-01-01", Name: "episode"})
			}
			importCache(t, a, "season:123:1", catalog.Season{SeriesID: m.ID, SeasonNumber: 1, Episodes: episodes})
			oldSource := filepath.Join(source, "测试剧集 S01E02 1080p.mkv")
			oldBytes := bytes.Repeat([]byte{'l'}, 1024)
			importWrite(t, oldSource, oldBytes)
			oldPlan, e := a.previewImport(ctx, ImportRequest{MediaType: "tv", TMDBID: m.ID, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: oldSource, Season: 1, Episodes: []int{2}}}})
			if e != nil {
				t.Fatal(e)
			}
			j, e := a.executeImport(ctx, oldPlan.ID, nil)
			if e != nil {
				t.Fatal(e)
			}
			reorgRunJob(t, a, j)
			var old Asset
			if e = a.assetAtPath(ctx, oldPlan.Files[0].Target, &old); e != nil {
				t.Fatal(e)
			}
			names := []string{"测试剧集 S01E01 2160p.mkv", "测试剧集 S01E02 2160p.mkv", "测试剧集 S01E03 2160p.mkv"}
			torrent := automaticPackTorrent(names)
			material, e := metainfo.ParseMaterial(torrent)
			if e != nil {
				t.Fatal(e)
			}
			paths := []string{}
			for _, f := range material.Files {
				paths = append(paths, f.Path)
			}
			var added, complete atomic.Bool
			var adds, sets, starts atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "qb" {
					switch r.URL.Path {
					case "/api/v2/torrents/info":
						if !added.Load() {
							workflowJSON(w, []any{})
							return
						}
						progress := 0.0
						if complete.Load() {
							progress = 1
						}
						task := workflowTask(material.Hash, progress)
						task["save_path"] = source
						workflowJSON(w, []any{task})
					case "/api/v2/torrents/add":
						if err := r.ParseMultipartForm(4 << 20); err != nil || r.FormValue("stopped") != "true" || r.FormValue("savepath") != source {
							t.Error("selected quality Add input", err, r.Form)
						}
						adds.Add(1)
						added.Store(true)
						io.WriteString(w, "Ok.")
					case "/api/v2/torrents/files":
						rows := []any{}
						for i, path := range paths {
							progress := 0
							if i == 1 && complete.Load() {
								progress = 1
							}
							rows = append(rows, map[string]any{"index": i, "name": path, "size": 1024, "progress": progress})
						}
						workflowJSON(w, rows)
					case "/api/v2/torrents/filePrio":
						r.ParseForm()
						if r.Form.Get("hash") != material.Hash || r.Form.Get("id") != "0|2" || r.Form.Get("priority") != "0" {
							t.Error("quality selected wrong qB daemon files", r.Form)
						}
						sets.Add(1)
					case "/api/v2/torrents/start":
						starts.Add(1)
					default:
						t.Error("unexpected qB endpoint", r.URL.Path)
						http.NotFound(w, r)
					}
					return
				}
				var request struct {
					ID     json.RawMessage            `json:"id"`
					Method string                     `json:"method"`
					Params map[string]json.RawMessage `json:"params"`
				}
				if e := json.NewDecoder(r.Body).Decode(&request); e != nil {
					t.Error(e)
					return
				}
				result := map[string]any{}
				switch request.Method {
				case "session_get":
					result["version"] = "4.1.0"
				case "torrent_get":
					var fields []string
					json.Unmarshal(request.Params["fields"], &fields)
					if slices.Contains(fields, "files") {
						files := []any{}
						for i, path := range paths {
							done := 0
							if complete.Load() && i == 1 {
								done = 1024
							}
							files = append(files, map[string]any{"name": path, "length": 1024, "bytes_completed": done})
						}
						result["torrents"] = []any{map[string]any{"hash_string": material.Hash, "files": files, "priorities": []int{0, 0, 0}, "wanted": []bool{true, true, true}}}
					} else if !added.Load() {
						result["torrents"] = []any{}
					} else {
						done, left, status := 0, 1024, 4
						if complete.Load() {
							done, left, status = 1, 0, 6
						}
						result["torrents"] = []any{map[string]any{"hash_string": material.Hash, "name": "quality", "status": status, "download_dir": source, "total_size": 1024, "percent_done": done, "left_until_done": left}}
					}
				case "torrent_add":
					var paused bool
					json.Unmarshal(request.Params["paused"], &paused)
					if !paused {
						t.Error("TR quality Add was not paused")
					}
					adds.Add(1)
					added.Store(true)
					result["torrent_added"] = map[string]any{"hash_string": material.Hash}
				case "torrent_set":
					var ids []string
					json.Unmarshal(request.Params["ids"], &ids)
					if !reflect.DeepEqual(ids, []string{material.Hash}) {
						t.Error("TR quality widened hash", ids)
					}
					var selected []int
					if request.Params["files_wanted"] != nil {
						json.Unmarshal(request.Params["files_wanted"], &selected)
						if !reflect.DeepEqual(selected, []int{1}) {
							t.Error("TR wanted wrong episode", selected)
						}
					} else {
						json.Unmarshal(request.Params["files_unwanted"], &selected)
						if !reflect.DeepEqual(selected, []int{0, 2}) {
							t.Error("TR unwanted wrong episodes", selected)
						}
					}
					sets.Add(1)
				case "torrent_start":
					starts.Add(1)
				default:
					t.Error("unexpected TR endpoint", request.Method)
				}
				workflowJSON(w, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
			}))
			defer server.Close()
			site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/torrents.php":
					io.WriteString(w, workflowHTML("测试剧集 S01 2160p WEB-DL", "", false))
				case "/download.php":
					w.Write(torrent)
				default:
					http.NotFound(w, r)
				}
			}))
			defer site.Close()
			workflowConfigure(t, a, site.URL, server.URL)
			a.config.Downloaders[0].Kind = kind
			a.config.Downloaders[0].DownloadPath = source
			a.config.Downloaders[0].SharedPaths = true
			overwriteMode := "always"
			a.config.Directories = []DirectoryRule{{ID: "quality-tv", Enabled: true, MonitorType: "monitor", SourcePath: source, RootID: "root", MediaType: "tv", Mode: "hardlink", OverwriteModeKnown: true, OverwriteMode: &overwriteMode}}
			a.config.PriorityRules = &priorityrules.Configuration{SubscribeGroups: []string{"quality"}, Groups: []priorityrules.Group{{Name: "quality", RuleString: priorityText("WEBDL & 4K > WEBDL & 1080P")}}, Mode: priorityrules.Rust}
			best := true
			s, e := a.saveSubscription(ctx, "", SubscriptionRequest{MediaType: "tv", TMDBID: m.ID, Season: 1, TotalEpisodes: 3, BestVersion: &best, DownloaderID: "fixture-qb", DirectoryID: "quality-tv", DownloadPath: source, Schedule: Schedule{Mode: "manual"}})
			if e != nil {
				t.Fatal(e)
			}
			// Fixture represents previously acknowledged per-episode source facts;
			// this test's new download must change only episode 2 through actual Add.
			priority := 50
			s.CurrentPriority = &priority
			s.EpisodePriority = map[string]int{"1": 100, "2": 50, "3": 100}
			s.SourceNote = []int{1, 3}
			if e = a.Store.Put(ctx, "subscription", s.ID, s); e != nil {
				t.Fatal(e)
			}
			if e = a.checkSubscription(ctx, importClaim(t, a, "subscription", s.ID), s.ID, s.Revision); e != nil {
				t.Fatal(e)
			}
			rows, e := a.Store.List(ctx, "intent", 10, 0)
			if e != nil || len(rows) != 1 {
				t.Fatal("partial-pack quality planning", len(rows), e)
			}
			var in Intent
			json.Unmarshal(rows[0], &in)
			if !reflect.DeepEqual(in.Episodes, []int{2}) || !reflect.DeepEqual(in.SelectedEpisodes, []int{2}) {
				t.Fatal("quality did not keep actual selected episode", in.Episodes, in.SelectedEpisodes)
			}
			if in.State != "accepted" {
				t.Fatal("batch did not await actual selected download", in.State)
			}
			var current Subscription
			if e = a.Store.Get(ctx, "subscription", s.ID, &current); e != nil || !reflect.DeepEqual(current.EpisodePriority, map[string]int{"1": 100, "2": 100, "3": 100}) || !reflect.DeepEqual(current.SourceNote, []int{1, 2, 3}) || current.CurrentPriority == nil || *current.CurrentPriority != 100 {
				t.Fatal("actual selected Add changed wrong source progress", current, e)
			}
			if adds.Load() != 1 || starts.Load() != 1 || sets.Load() != map[string]int64{"qb": 1, "tr": 2}[kind] {
				t.Fatal("real file-selection operations", adds.Load(), sets.Load(), starts.Load())
			}
			newBytes := bytes.Repeat([]byte{'h'}, 1024)
			importWrite(t, filepath.Join(source, paths[1]), newBytes)
			complete.Store(true)
			if e = a.pollDownloads(ctx); e != nil {
				t.Fatal(e)
			}
			if e = a.Store.Get(ctx, "intent", in.ID, &in); e != nil || in.ImportPlanID == "" {
				t.Fatal("selected completed file did not plan replacement; unwanted files are intentionally absent", in.Error, e)
			}
			reorgRunJob(t, a, reorgJob(t, a, "import", in.ImportPlanID))
			var plan ImportPlan
			if e = a.Store.Get(ctx, "import_plan", in.ImportPlanID, &plan); e != nil || len(plan.Files) != 1 || !reflect.DeepEqual(plan.Files[0].Episodes, []int{2}) {
				t.Fatal("unwanted files entered import", plan, e)
			}
			var replacement Asset
			if e = a.assetAtPath(ctx, old.Path, &replacement); e != nil || replacement.ID == old.ID {
				t.Fatal("selected episode did not replace old video", e)
			}
			qualityRequireDirectoryOverwriteComplete(t, a, plan.ID+"/"+plan.Files[0].ID)
			reorgRunJob(t, a, reorgJob(t, a, "scrape", replacement.ID))
			for path, want := range map[string][]byte{oldSource: oldBytes, filepath.Join(source, paths[1]): newBytes, replacement.Path: newBytes} {
				got, e := os.ReadFile(path)
				if e != nil || !bytes.Equal(got, want) {
					t.Fatal("TV source/replacement not retained", path, e)
				}
			}
		})
	}
}
