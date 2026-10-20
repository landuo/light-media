package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/metainfo"
	"light-media/internal/releasemeta"
	"light-media/internal/sites"
)

func TestPluginLarkActualBatchMaterialReuseSelectedFilesAndDaemonAcceptance(t *testing.T) {
	for _, branch := range []string{"movie", "whole_season", "select_missing_episode", "partial_subscribe_R"} {
		t.Run(branch, func(t *testing.T) {
			a, source, _ := importTestApp(t)
			kind, title := "tv", "测试剧集 S01 1080p"
			if branch == "movie" {
				kind, title = "movie", "测试影片.2024.1080p.WEB-DL.x264-TEST"
			}
			if branch == "partial_subscribe_R" {
				title = "测试剧集 S01E02 1080p"
			}
			m := importFixture(t, a, kind)
			torrent, hash := workflowTorrent("测试影片.2024.1080p.mkv")
			if kind == "tv" {
				torrent = automaticPackTorrent([]string{"测试剧集 S01E01 1080p.mkv", "测试剧集 S01E02 1080p.mkv", "测试剧集 S01E03 1080p.mkv"})
				if branch == "partial_subscribe_R" {
					torrent, _ = workflowTorrent("测试剧集 S01E02 1080p.mkv")
				}
				material, e := metainfo.ParseMaterial(torrent)
				if e != nil {
					t.Fatal(e)
				}
				hash = material.Hash
				season := catalog.Season{SeriesID: m.ID, SeasonNumber: 1}
				for ep := 1; ep <= 3; ep++ {
					season.Episodes = append(season.Episodes, catalog.Episode{SeasonNumber: 1, EpisodeNumber: ep, AirDate: "2024-01-01"})
				}
				importCache(t, a, "season:123:1", season)
			}
			var adds, gets, files, sets, starts atomic.Int64
			qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					if adds.Load() == 0 {
						workflowJSON(w, []any{})
					} else {
						task := workflowTask(hash, 0)
						task["save_path"] = source
						workflowJSON(w, []any{task})
					}
				case "/api/v2/torrents/add":
					if e := r.ParseMultipartForm(4 << 20); e != nil {
						t.Error(e)
					}
					wantPaused := "false"
					if branch == "select_missing_episode" {
						wantPaused = "true"
					}
					if r.FormValue("stopped") != wantPaused || r.FormValue("savepath") != source {
						t.Error("batch did not use original paused/location source branch", r.FormValue("stopped"), r.FormValue("savepath"))
					}
					adds.Add(1)
					io.WriteString(w, "Ok.")
				case "/api/v2/torrents/files":
					files.Add(1)
					io.WriteString(w, `[{"index":7,"name":"测试剧集 S01E01 1080p.mkv"},{"index":3,"name":"测试剧集 S01E02 1080p.mkv"},{"index":12,"name":"测试剧集 S01E03 1080p.mkv"}]`)
				case "/api/v2/torrents/filePrio":
					r.ParseForm()
					if r.Form.Get("id") != "7|12" || r.Form.Get("priority") != "0" {
						t.Error("batch selected wrong daemon files", r.Form)
					}
					sets.Add(1)
				case "/api/v2/torrents/start":
					starts.Add(1)
				default:
					t.Error("ungranted daemon endpoint", r.URL.Path)
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
					w.Write(torrent)
				default:
					http.NotFound(w, r)
				}
			}))
			defer site.Close()
			workflowConfigure(t, a, site.URL, qb.URL)
			if branch == "partial_subscribe_R" {
				installNoticeSourceFixtures(t, a, m, 1, 3)
			}
			a.config.Directories = []DirectoryRule{{ID: "owned", SourcePath: source, MediaType: "mixed", RootID: "root", Mode: "hardlink"}}
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
			raw, e := meta.EncodeState()
			if e != nil {
				t.Fatal(e)
			}
			candidate := Candidate{Accepted: true, Resource: sites.Resource{SiteID: "fixture-site", ID: "101", Title: title, Seeders: 12}}
			s := &pluginLarkMediaSession{Request: "batch-owned", User: "ou_owned", Selected: &m, Candidates: []Candidate{candidate}, CandidateMetas: []json.RawMessage{raw}}
			facts := pluginLarkMediaTargets{Media: m, Known: map[int][]int{}, Missing: map[int][]int{}}
			if kind == "movie" {
				facts.Requests = []SearchRequest{{MediaType: kind, TMDBID: m.ID}}
			} else {
				facts.Known[1], facts.Missing[1] = []int{1, 2, 3}, []int{1, 2, 3}
				if branch == "select_missing_episode" {
					facts.Missing[1] = []int{2}
				}
				facts.Requests = []SearchRequest{{MediaType: kind, TMDBID: m.ID, Season: 1, Episodes: []int{1, 2, 3}}}
			}
			s.Targets = facts.Requests
			dirs, e := a.larkDownloadDirectoryFacts(ctx, core, m)
			if e != nil {
				t.Fatal(e)
			}
			if branch == "partial_subscribe_R" {
				meta, e := parser.ParseTitle("测试剧集 S01", nil, nil)
				if e != nil {
					t.Fatal(e)
				}
				s.Meta, e = meta.EncodeState()
				if e != nil {
					t.Fatal(e)
				}
				e = a.larkAutoDownloadMedia(ctx, core, s, dirs, nil)
				if e != nil || adds.Load() != 1 {
					t.Fatal("partial actual batch did not finish original notice after Add", e, adds.Load())
				}
				rows, e := a.Store.List(ctx, "subscription", 10, 0)
				if e != nil || len(rows) != 1 {
					t.Fatal("partial actual batch did not create one subscription", e, len(rows))
				}
				var sub Subscription
				if json.Unmarshal(rows[0], &sub) != nil || sub.SourceState != "R" || len(sub.SourceNote) != 1 || sub.SourceNote[0] != 2 {
					t.Fatal("partial actual batch lost source R/note from actual accepted Context", sub.SourceState, sub.SourceNote)
				}
				origin, e := a.sourceSubscriptionOrigin(ctx, sub)
				if e != nil || origin.State != "R" || len(origin.Note) != 1 || origin.Note[0] != 2 {
					t.Fatal("source R creation fact was only a native active flag", origin, e)
				}
				return
			}
			out, e := a.larkRunAcquisitionBatch(ctx, core, s, facts, dirs, nil)
			if e != nil || len(out.Downloaded) != 1 || len(out.Remaining) != 0 || adds.Load() != 1 || gets.Load() != 1 {
				t.Fatal("batch did not reach actual acknowledged Add with original material reuse", branch, e, len(out.Downloaded), out.Remaining, adds.Load(), gets.Load())
			}
			if branch == "select_missing_episode" {
				if files.Load() != 1 || sets.Load() != 1 || starts.Load() != 1 {
					t.Fatal("batch only planned selected files", files.Load(), sets.Load(), starts.Load())
				}
			} else if files.Load()+sets.Load()+starts.Load() != 0 {
				t.Fatal("unselected download incorrectly ran post-Add selection")
			}
			rows, e := a.Store.List(ctx, "intent", 10, 0)
			if e != nil || len(rows) != 1 {
				t.Fatal(e, len(rows))
			}
			var accepted Intent
			if json.Unmarshal(rows[0], &accepted) != nil || accepted.State != "accepted" {
				t.Fatal("batch counted a planned intent", accepted.State)
			}
			context, e := a.loadDownloadContext(ctx, "intent", accepted.ID, accepted.Hash)
			if e != nil || context == nil || len(context.SourceMeta) == 0 {
				t.Fatal("original batch MetaInfo mutation was lost", e)
			}
		})
	}
}

func TestPluginLarkActualBatchKnownRejectionFallsBackUnknownReplyNeverDoes(t *testing.T) {
	for _, branch := range []string{"known_reject_fallback", "unknown_reply_stops"} {
		t.Run(branch, func(t *testing.T) {
			a, source, _ := importTestApp(t)
			m := importFixture(t, a, "movie")
			first, _ := workflowTorrent("测试影片.2024.1080p.FIRST.mkv")
			second, secondHash := workflowTorrent("测试影片.2024.1080p.SECOND.mkv")
			var adds atomic.Int64
			qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					if adds.Load() < 2 {
						workflowJSON(w, []any{})
					} else {
						workflowJSON(w, []any{workflowTask(secondHash, 0)})
					}
				case "/api/v2/torrents/add":
					if adds.Add(1) == 1 {
						if branch == "known_reject_fallback" {
							io.WriteString(w, "Fails.")
						} else {
							io.WriteString(w, "unconfirmed proxy body")
						}
					} else {
						io.WriteString(w, "Ok.")
					}
				default:
					t.Error("ungranted daemon endpoint", r.URL.Path)
					w.WriteHeader(400)
				}
			}))
			defer qb.Close()
			titles := []string{"测试影片.2024.1080p.WEB-DL.x264-FIRST", "测试影片.2024.1080p.WEB-DL.x264-SECOND"}
			site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/torrents.php":
					firstHTML := strings.TrimSuffix(workflowHTML(titles[0], "", false), "</table>")
					secondHTML := strings.ReplaceAll(workflowHTML(titles[1], "", false), "id=101", "id=102")
					secondHTML = secondHTML[strings.Index(secondHTML, "<tr><td>类别"):]
					io.WriteString(w, firstHTML+secondHTML)
				case "/download.php":
					if r.URL.Query().Get("id") == "101" {
						w.Write(first)
					} else {
						w.Write(second)
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer site.Close()
			workflowConfigure(t, a, site.URL, qb.URL)
			a.config.Directories = []DirectoryRule{{ID: "owned", SourcePath: source, MediaType: "mixed", Mode: "hardlink", RootID: "root"}}
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
			s := &pluginLarkMediaSession{Request: "batch-fallback", User: "ou_owned", Selected: &m}
			for i, title := range titles {
				meta, e := parser.ParseTitle(title, nil, nil)
				if e != nil {
					t.Fatal(e)
				}
				raw, e := meta.EncodeState()
				if e != nil {
					t.Fatal(e)
				}
				s.CandidateMetas = append(s.CandidateMetas, raw)
				id := "101"
				if i == 1 {
					id = "102"
				}
				s.Candidates = append(s.Candidates, Candidate{Accepted: true, Resource: sites.Resource{SiteID: "fixture-site", ID: id, Title: title}})
			}
			facts := pluginLarkMediaTargets{Media: m, Requests: []SearchRequest{{MediaType: "movie", TMDBID: m.ID}}}
			dirs, e := a.larkDownloadDirectoryFacts(ctx, core, m)
			if e != nil {
				t.Fatal(e)
			}
			out, e := a.larkRunAcquisitionBatch(ctx, core, s, facts, dirs, nil)
			if branch == "unknown_reply_stops" {
				if pluginErrorCodeInternal(e) != "LARK_UNCONFIRMED" || adds.Load() != 1 || len(out.Downloaded) != 0 {
					t.Fatal("unknown Add silently fell back or counted success", e, adds.Load(), len(out.Downloaded))
				}
				return
			}
			if e != nil || adds.Load() != 2 || len(out.Downloaded) != 1 || out.Downloaded[0].ID != "1" {
				t.Fatal("native acknowledged rejection did not try original next candidate", e, adds.Load(), out.Downloaded)
			}
			var failed, accepted, reservations int
			a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='intent' AND json_extract(payload,'$.state')='failed'`).Scan(&failed)
			a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='intent' AND json_extract(payload,'$.state')='accepted'`).Scan(&accepted)
			a.Store.DB.QueryRow(`SELECT count(*) FROM reservations`).Scan(&reservations)
			if failed != 1 || accepted != 1 || reservations != 1 {
				t.Fatal("fallback did not preserve original rejected history and only reserve accepted scope", failed, accepted, reservations)
			}
		})
	}
}
