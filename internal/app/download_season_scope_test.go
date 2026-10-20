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
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/metainfo"
	"light-media/internal/priorityrules"
	"light-media/internal/releasemeta"
	"light-media/internal/sites"
)

func TestPluginLarkActualMultiSeasonOneAddColdImportAndScopeProof(t *testing.T) {
	for _, branch := range []string{"actual_cold_import", "actual_cold_import_tr", "scope_tamper", "scope_cipher_tamper", "missing_scope_receipt", "other_scope_receipt", "exclude_second_before_add", "selected_multi_scope", "selected_sparse_scope", "selected_episode_scope"} {
		t.Run(branch, func(t *testing.T) {
			selected := strings.HasPrefix(branch, "selected_")
			positive := strings.HasPrefix(branch, "actual_cold_import") || selected
			a, source, _ := importTestApp(t)
			m := importFixture(t, a, "tv")
			for _, season := range []int{1, 2} {
				value := catalog.Season{SeriesID: m.ID, SeasonNumber: season}
				for ep := 1; ep <= 2; ep++ {
					value.Episodes = append(value.Episodes, catalog.Episode{ID: int64(season*10 + ep), SeasonNumber: season, EpisodeNumber: ep, Name: "episode", AirDate: "2024-01-01"})
				}
				importCache(t, a, "season:123:"+string(rune('0'+season)), value)
			}
			title := "测试剧集 S01-S02 1080p WEB-DL"
			if branch == "selected_episode_scope" {
				title = "测试剧集 S01-S02E01 1080p WEB-DL"
			}
			raw := automaticPackTorrent([]string{"测试剧集 S01E01 1080p.mkv", "测试剧集 S01E02 1080p.mkv", "测试剧集 S02E01 1080p.mkv", "测试剧集 S02E02 1080p.mkv"})
			if branch == "selected_sparse_scope" || branch == "selected_episode_scope" {
				raw = automaticPackTorrent([]string{"测试剧集 S01E01 1080p.mkv", "测试剧集 S02E01 1080p.mkv"})
			}
			material, e := metainfo.ParseMaterial(raw)
			if e != nil {
				t.Fatal(e)
			}
			var adds, gets atomic.Int64
			var complete atomic.Bool
			qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if branch == "actual_cold_import_tr" {
					var req struct {
						ID     json.RawMessage            `json:"id"`
						Method string                     `json:"method"`
						Params map[string]json.RawMessage `json:"params"`
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
						return
					}
					result := map[string]any{}
					switch req.Method {
					case "session_get":
						result["version"] = "4.1.0"
					case "torrent_add":
						var paused bool
						var path string
						if json.Unmarshal(req.Params["paused"], &paused) != nil || paused || json.Unmarshal(req.Params["download_dir"], &path) != nil || path != source {
							t.Error("TR multi-season lost source None/path", req.Params)
						}
						adds.Add(1)
						result["torrent_added"] = map[string]any{"hash_string": material.Hash}
					case "torrent_get":
						var fields []string
						_ = json.Unmarshal(req.Params["fields"], &fields)
						if strings.Contains(strings.Join(fields, ","), "files") {
							files := []any{}
							for _, f := range material.Files {
								done := int64(0)
								if complete.Load() {
									done = f.Size
								}
								files = append(files, map[string]any{"name": f.Path, "length": f.Size, "bytes_completed": done})
							}
							result["torrents"] = []any{map[string]any{"hash_string": material.Hash, "files": files}}
						} else if adds.Load() == 0 {
							result["torrents"] = []any{}
						} else {
							status, progress, left := 4, 0, material.TotalBytes
							if complete.Load() {
								status, progress, left = 6, 1, 0
							}
							result["torrents"] = []any{map[string]any{"hash_string": material.Hash, "name": title, "status": status, "download_dir": source, "total_size": material.TotalBytes, "percent_done": progress, "left_until_done": left}}
						}
					default:
						t.Error("unexpected multi-season TR action", req.Method)
					}
					workflowJSON(w, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
					return
				}
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					if branch == "exclude_second_before_add" && adds.Load() == 0 {
						if err := a.Store.Put(context.Background(), "exclusion", mediaKey(m)+":2:1", map[string]any{"blocked": true}); err != nil {
							t.Error(err)
						}
					}
					if adds.Load() == 0 {
						workflowJSON(w, []any{})
						return
					}
					progress := 0.0
					if complete.Load() {
						progress = 1
					}
					row := workflowTask(material.Hash, progress)
					row["save_path"] = source
					row["total_size"] = material.TotalBytes
					if complete.Load() {
						row["completed"] = material.TotalBytes
					}
					workflowJSON(w, []any{row})
				case "/api/v2/torrents/add":
					if e := r.ParseMultipartForm(4 << 20); e != nil {
						t.Error(e)
					}
					if r.FormValue("stopped") != "false" || r.FormValue("savepath") != source {
						t.Error("multi-season lost source None selection/path", r.Form)
					}
					adds.Add(1)
					io.WriteString(w, "Ok.")
				case "/api/v2/torrents/files":
					rows := []any{}
					for i, f := range material.Files {
						progress := 0
						if complete.Load() {
							progress = 1
						}
						rows = append(rows, map[string]any{"index": i, "name": f.Path, "size": f.Size, "progress": progress})
					}
					workflowJSON(w, rows)
				default:
					t.Error("unexpected multi-season daemon action", r.URL.Path)
					w.WriteHeader(400)
				}
			}))
			defer qb.Close()
			site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/torrents.php":
					io.WriteString(w, workflowHTML(title, "", false))
				case "/download.php":
					gets.Add(1)
					w.Write(raw)
				default:
					http.NotFound(w, r)
				}
			}))
			defer site.Close()
			workflowConfigure(t, a, site.URL, qb.URL)
			if branch == "actual_cold_import_tr" {
				a.config.Downloaders[0].Kind = "tr"
			}
			a.config.Downloaders[0].DownloadPath, a.config.Downloaders[0].SharedPaths = source, true
			a.config.Directories = []DirectoryRule{{ID: "multi", Enabled: true, SourcePath: source, RootID: "root", MediaType: "tv", Mode: "hardlink"}}
			installUnregisteredLarkConfig(t, a, map[string]any{"enabled": true})
			core, ctx := larkStartedCoreFixture(t, a)
			options, e := a.globalRecognitionOptions(ctx)
			if e != nil {
				t.Fatal(e)
			}
			parser, e := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
			if e != nil {
				t.Fatal(e)
			}
			meta, e := parser.ParseTitle(title, nil, nil)
			if e != nil {
				t.Fatal(e)
			}
			encoded, e := meta.EncodeState()
			if e != nil {
				t.Fatal(e)
			}
			requests := []SearchRequest{{MediaType: "tv", TMDBID: m.ID, Season: 1, Episodes: []int{1, 2}}, {MediaType: "tv", TMDBID: m.ID, Season: 2, Episodes: []int{1, 2}}}
			c := Candidate{Accepted: true, Season: 1, Resource: sites.Resource{SiteID: "fixture-site", ID: "101", Title: title, Seeders: 12}}
			session := &pluginLarkMediaSession{Request: "multi-owned", User: "ou_owned", Selected: &m, Targets: requests, Candidates: []Candidate{c}, CandidateMetas: []json.RawMessage{encoded}}
			facts := pluginLarkMediaTargets{Media: m, Known: map[int][]int{1: {1, 2}, 2: {1, 2}}, Missing: map[int][]int{1: {1, 2}, 2: {1, 2}}, Requests: requests}
			dirs, e := a.larkDownloadDirectoryFacts(ctx, core, m)
			if e != nil {
				t.Fatal(e)
			}
			if selected {
				if e = a.larkExecuteMediaDownload(ctx, core, session, "single", &c, dirs, nil); e != nil || adds.Load() != 1 || gets.Load() != 1 {
					t.Fatal("explicit resource selection lost actual multi-season Add", adds.Load(), gets.Load(), e)
				}
			} else {
				out, e := a.larkRunAcquisitionBatch(ctx, core, session, facts, dirs, nil)
				if branch == "exclude_second_before_add" {
					if adds.Load() != 0 || len(out.Downloaded) != 0 || len(out.Remaining) != 1 || len(out.Remaining[0].Seasons) != 2 {
						t.Fatal("new exclusion in second season did not stop external Add", adds.Load(), out.Downloaded, e)
					}
					var reserved, accepted int
					if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM reservations`).Scan(&reserved); err != nil || reserved != 0 {
						t.Fatal("known pre-Add rejection held seasonal reservations", reserved, err)
					}
					if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='intent' AND json_extract(payload,'$.state')='accepted'`).Scan(&accepted); err != nil || accepted != 0 {
						t.Fatal("exclusion fabricated acceptance", accepted, err)
					}
					return
				}
				if e != nil || len(out.Downloaded) != 1 || len(out.Remaining) != 0 || adds.Load() != 1 || gets.Load() != 1 {
					t.Fatal("actual multi-season source entry did not receive exactly one torrent", len(out.Downloaded), out.Remaining, adds.Load(), gets.Load(), e)
				}
			}
			rows, e := a.Store.List(ctx, "intent", 10, 0)
			if e != nil || len(rows) != 1 {
				t.Fatal(e, len(rows))
			}
			var in Intent
			if json.Unmarshal(rows[0], &in) != nil || in.State != "accepted" || in.SelectedEpisodes != nil {
				t.Fatal("multi-season acceptance lost source None", in.State, in.SelectedEpisodes)
			}
			var plain map[string]any
			if json.Unmarshal(rows[0], &plain) != nil || plain["season_scope_sha"] == nil {
				t.Fatal("accepted multi-season scope missing")
			}
			var count int
			if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM reservations WHERE intent_id=? AND season IN (1,2) AND episode IN (1,2)`, in.ID).Scan(&count); e != nil || count != len(material.Files) {
				t.Fatal("actual acquisition did not reserve both seasons", count, e)
			}
			var sealed []byte
			if e = a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='download_season_scope' AND id=?`, "intent:"+in.ID).Scan(&sealed); e != nil {
				t.Fatal(e)
			}
			scopeRaw, se := a.pluginStore.unseal("download_season_scope", "system", "intent:"+in.ID, sealed)
			if se != nil {
				t.Fatal(se)
			}
			var scope struct {
				SourceBasis json.RawMessage
				Requested   []SearchRequest
				Files       []json.RawMessage
			}
			if json.Unmarshal(scopeRaw, &scope) != nil || len(scope.SourceBasis) == 0 || len(scope.Requested) != 2 || len(scope.Files) != len(material.Files) {
				t.Fatal("scope lost actual requested/source basis")
			}
			var basis struct {
				Mode    string
				Targets struct{ Known, Missing map[int][]int }
			}
			if json.Unmarshal(scope.SourceBasis, &basis) != nil || basis.Mode != func() string {
				if selected {
					return "selected"
				}
				return "whole_missing"
			}() || !equalQualityEpisodes(basis.Targets.Known[2], []int{1, 2}) || !equalQualityEpisodes(basis.Targets.Missing[2], []int{1, 2}) {
				t.Fatal("source selected/missing facts were guessed", string(scope.SourceBasis))
			}
			frozen, e := a.loadDownloadContext(ctx, "intent", in.ID, in.Hash)
			if e != nil || frozen == nil {
				t.Fatal(e)
			}
			state, e := releasemeta.DecodeMetaObjectState(frozen.SourceMeta)
			if e != nil {
				t.Fatal(e)
			}
			seasons, episodes, e := larkMetaNumbers(state)
			if e != nil || !equalQualityEpisodes(seasons, []int{1, 2}) || !equalQualityEpisodes(episodes, func() []int {
				if branch == "selected_episode_scope" {
					return []int{1}
				}
				return []int{}
			}()) {
				t.Fatal("multi-season source MetaInfo was flattened to one season/fictional range", seasons, episodes, e)
			}
			cfg, dir := a.Settings(), a.Dir
			core.release()
			if e = a.Close(); e != nil {
				t.Fatal(e)
			}
			next, e := New(Options{DataDir: dir})
			if e != nil {
				t.Fatal(e)
			}
			a = next
			a.config = cfg
			t.Cleanup(func() { _ = next.Close() })
			ctx = context.Background()
			if branch == "scope_tamper" {
				plain["season_scope_sha"] = "changed"
				altered, _ := json.Marshal(plain)
				if e = a.Store.Put(ctx, "intent", in.ID, json.RawMessage(altered)); e != nil {
					t.Fatal(e)
				}
			}
			if branch == "scope_cipher_tamper" {
				var sealed []byte
				if e = a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='download_season_scope' AND id=?`, "intent:"+in.ID).Scan(&sealed); e != nil {
					t.Fatal(e)
				}
				sealed[len(sealed)-1] ^= 1
				if _, e = a.Store.DB.Exec(`UPDATE records SET payload=? WHERE kind='download_season_scope' AND id=?`, sealed, "intent:"+in.ID); e != nil {
					t.Fatal(e)
				}
			}
			if branch == "missing_scope_receipt" {
				if _, e = a.Store.DB.Exec(`DELETE FROM records WHERE kind='download_season_scope' AND id=?`, "intent:"+in.ID); e != nil {
					t.Fatal(e)
				}
			}
			if branch == "other_scope_receipt" {
				if _, e = a.Store.DB.Exec(`UPDATE records SET id=? WHERE kind='download_season_scope' AND id=?`, "intent:other", "intent:"+in.ID); e != nil {
					t.Fatal(e)
				}
			}
			for _, f := range material.Files {
				importWrite(t, filepath.Join(source, f.Path), bytes.Repeat([]byte{'n'}, int(f.Size)))
			}
			complete.Store(true)
			pollErr := a.pollDownloads(ctx)
			if positive && pollErr != nil {
				t.Fatal(pollErr)
			}
			if branch == "scope_tamper" && pollErr == nil {
				t.Fatal("tampered actual source receipt was accepted")
			}
			if e = a.Store.Get(ctx, "intent", in.ID, &in); e != nil {
				t.Fatal(e)
			}
			if !positive {
				if in.ImportPlanID != "" || in.Error == "" && pollErr == nil {
					t.Fatal("invalid scope proof reached import", in.ImportPlanID, in.Error)
				}
				if adds.Load() != 1 {
					t.Fatal("proof failure repeated external Add", adds.Load())
				}
				return
			}
			if in.ImportPlanID == "" {
				t.Fatal("cold completed multi-season torrent did not plan import", in.Error)
			}
			reorgRunJob(t, a, reorgJob(t, a, "import", in.ImportPlanID))
			var plan ImportPlan
			if e = a.Store.Get(ctx, "import_plan", in.ImportPlanID, &plan); e != nil || len(plan.Files) != len(material.Files) {
				t.Fatal(e, len(plan.Files))
			}
			seen := map[int]int{}
			for _, f := range plan.Files {
				seen[f.Season]++
				var asset Asset
				if e = a.assetAtPath(ctx, f.Target, &asset); e != nil || asset.Season != f.Season || asset.IntentID != in.ID {
					t.Fatal("wrong actual seasonal asset", asset, e)
				}
				reorgRunJob(t, a, reorgJob(t, a, "scrape", asset.ID))
				if _, e = os.Stat(f.Source); e != nil {
					t.Fatal("source seed file removed", e)
				}
				if _, e = os.Stat(strings.TrimSuffix(f.Target, filepath.Ext(f.Target)) + ".nfo"); e != nil {
					t.Fatal("actual per-season scrape absent", e)
				}
			}
			if seen[1] != len(material.Files)/2 || seen[2] != len(material.Files)/2 {
				t.Fatal("season flattened during organize", seen)
			}
			if e = a.Store.Get(ctx, "intent", in.ID, &in); e != nil || in.State != "imported" || adds.Load() != 1 {
				t.Fatal("multi-season completion was partial/re-added", in.State, adds.Load(), e)
			}
		})
	}
}

func TestSubscriptionSingleSeasonSkipsMultiSeasonBeforeMaterialOrAdd(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	m := importFixture(t, a, "tv")
	importCache(t, a, "season:123:1", catalog.Season{SeriesID: m.ID, SeasonNumber: 1, Episodes: []catalog.Episode{{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, Name: "episode", AirDate: "2024-01-01"}}})
	var gets, adds atomic.Int64
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/torrents/info" {
			workflowJSON(w, []any{})
			return
		}
		adds.Add(1)
		t.Error("single-season subscription reached multi-season daemon", r.URL.Path)
		w.WriteHeader(400)
	}))
	defer qb.Close()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			io.WriteString(w, workflowHTML("测试剧集 S01-S02 1080p WEB-DL", "", false))
		case "/download.php":
			gets.Add(1)
			w.Write(automaticPackTorrent([]string{"测试剧集 S01E01 1080p.mkv", "测试剧集 S02E01 1080p.mkv"}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	workflowConfigure(t, a, site.URL, qb.URL)
	a.config.Downloaders[0].DownloadPath, a.config.Downloaders[0].SharedPaths = source, true
	a.config.PriorityRules = &priorityrules.Configuration{SubscribeGroups: []string{"quality"}, Groups: []priorityrules.Group{{Name: "quality", RuleString: priorityText("WEBDL & 4K > WEBDL & 1080P")}}, Mode: priorityrules.Rust}
	best := true
	s, e := a.saveSubscription(ctx, "", SubscriptionRequest{MediaType: "tv", TMDBID: m.ID, Season: 1, TotalEpisodes: 1, BestVersion: &best, DownloaderID: "fixture-qb", DownloadPath: source, RootID: "root", Schedule: Schedule{Mode: "manual"}})
	if e != nil {
		t.Fatal(e)
	}
	if e = a.checkSubscription(ctx, importClaim(t, a, "subscription", s.ID), s.ID, s.Revision); e != nil {
		t.Fatal(e)
	}
	var intents int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='intent'`).Scan(&intents); e != nil || intents != 0 || gets.Load() != 0 || adds.Load() != 0 {
		t.Fatal("single-season original subset gate was widened", intents, gets.Load(), adds.Load(), e)
	}
}
