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
)

func TestSubscriptionQualityActualFullModeNonePackFinishesAndThenOrganizes(t *testing.T) {
	for _, branch := range []string{"full_season", "full_extra_before_start", "full_scope_expanded", "full_scope_shrunk"} {
		t.Run(branch, func(t *testing.T) {
			a, source, _ := importTestApp(t)
			ctx := context.Background()
			m := importFixture(t, a, "tv")
			total, start := 2, 1
			if branch == "full_extra_before_start" {
				total, start = 3, 2
			} else if branch == "full_scope_shrunk" {
				total = 3
			}
			names := []string{}
			season := catalog.Season{SeriesID: m.ID, SeasonNumber: 1}
			knownTotal := total
			if branch == "full_scope_expanded" {
				knownTotal = 3
			}
			for ep := 1; ep <= knownTotal; ep++ {
				season.Episodes = append(season.Episodes, catalog.Episode{ID: int64(ep), SeasonNumber: 1, EpisodeNumber: ep, AirDate: "2024-01-01", Name: "episode"})
				if ep <= total {
					names = append(names, "测试剧集 S01E0"+string(rune('0'+ep))+" 1080p.mkv")
				}
			}
			importCache(t, a, "season:123:1", season)
			raw := automaticPackTorrent(names)
			material, e := metainfo.ParseMaterial(raw)
			if e != nil {
				t.Fatal(e)
			}
			var adds atomic.Int64
			var complete atomic.Bool
			qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/info":
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
					if e := r.ParseMultipartForm(4 << 20); e != nil || r.FormValue("stopped") != "false" || r.FormValue("savepath") != source {
						t.Error("full mode did not retain original None", e, r.Form)
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
					t.Error("ungranted full-mode downloader effect", r.URL.Path)
					w.WriteHeader(400)
				}
			}))
			defer qb.Close()
			site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/torrents.php":
					io.WriteString(w, workflowHTML("测试剧集 S01 1080p WEB-DL", "", false))
				case "/download.php":
					w.Write(raw)
				default:
					http.NotFound(w, r)
				}
			}))
			defer site.Close()
			workflowConfigure(t, a, site.URL, qb.URL)
			a.config.Downloaders[0].DownloadPath, a.config.Downloaders[0].SharedPaths = source, true
			a.config.Directories = []DirectoryRule{{ID: "full-completion", Enabled: true, MonitorType: "monitor", SourcePath: source, RootID: "root", MediaType: "tv", Mode: "hardlink"}}
			a.config.PriorityRules = &priorityrules.Configuration{SubscribeGroups: []string{"quality"}, Groups: []priorityrules.Group{{Name: "quality", RuleString: priorityText("WEBDL & 1080P")}}, Mode: priorityrules.Rust}
			yes := true
			s, e := a.saveSubscription(ctx, "", SubscriptionRequest{MediaType: "tv", TMDBID: m.ID, Season: 1, TotalEpisodes: total, StartEpisode: start, BestVersion: &yes, BestVersionFull: &yes, DownloaderID: "fixture-qb", DownloadPath: source, DirectoryID: "full-completion", RootID: "root", Schedule: Schedule{Mode: "manual"}})
			if e != nil {
				t.Fatal(e)
			}
			initial := 20
			s.CurrentPriority = &initial
			if e = a.Store.Put(ctx, "subscription", s.ID, s); e != nil {
				t.Fatal(e)
			}
			if e = a.checkSubscription(ctx, importClaim(t, a, "subscription", s.ID), s.ID, s.Revision); e != nil {
				t.Fatal(e)
			}
			var current Subscription
			if e = a.Store.Get(ctx, "subscription", s.ID, &current); e != nil || current.CurrentPriority == nil || *current.CurrentPriority != 100 || !current.BestVersionFull {
				t.Fatal("actual full mode acceptance lost source priority", current.CurrentPriority, e)
			}
			rows, e := a.Store.List(ctx, "intent", 10, 0)
			if e != nil || len(rows) != 1 {
				t.Fatal(e, len(rows))
			}
			var in Intent
			if json.Unmarshal(rows[0], &in) != nil || in.State != "accepted" || in.SelectedEpisodes != nil || adds.Load() != 1 {
				t.Fatal("full mode used a planned or selected intent", in.State, in.SelectedEpisodes, adds.Load())
			}
			proof, e := a.qualitySnapshot(ctx, a.Store.DB, "intent", in.ID, in.Hash)
			if e != nil || proof == nil || !proof.Resource.ConfirmedFullCoverage {
				t.Fatal("actual accepted pack did not retain authenticated full coverage", proof, e)
			}
			if branch == "full_scope_expanded" || branch == "full_scope_shrunk" {
				newTotal := 3
				if branch == "full_scope_shrunk" {
					newTotal = 2
				}
				changed, e := a.saveSubscription(ctx, s.ID, SubscriptionRequest{MediaType: "tv", TMDBID: m.ID, Season: 1, TotalEpisodes: newTotal, StartEpisode: 1, BestVersion: &yes, BestVersionFull: &yes, DownloaderID: "fixture-qb", DownloadPath: source, DirectoryID: "full-completion", RootID: "root", Schedule: Schedule{Mode: "manual"}, Revision: s.Revision})
				if e != nil {
					t.Fatal("real subscription expansion", e)
				}
				facts, e := a.completionAcceptedClosure(ctx, a.Store.DB, changed)
				if branch == "full_scope_expanded" && (e == nil || changed.CurrentPriority == nil || *changed.CurrentPriority != 0) {
					t.Fatal("old full-pack proof incorrectly completed expanded current scope", len(facts), changed.CurrentPriority, e)
				}
				if branch == "full_scope_shrunk" && (e != nil || len(facts) != 1 || changed.CurrentPriority == nil || *changed.CurrentPriority != 100) {
					t.Fatal("authenticated whole pack lost smaller current scope", len(facts), changed.CurrentPriority, e)
				}
				var history, accepted, subs int
				a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='subscription_history'`).Scan(&history)
				a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='subscription_quality_accepted'`).Scan(&accepted)
				a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='subscription'`).Scan(&subs)
				if history != 0 || accepted != 1 || subs != 1 || adds.Load() != 1 {
					t.Fatal("scope expansion discarded accepted fact or repeated external action", history, accepted, subs, adds.Load())
				}
				return
			}
			fixture := installNoticeSourceFixtures(t, a, s.Media, 1, total)
			var posts atomic.Int64
			statsHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				if r.URL.Path != "/subscribe/done" {
					t.Error(r.URL.Path)
				}
				w.WriteHeader(200)
			}))
			defer statsHTTP.Close()
			subscriptionStatsInstallSettings(t, a, statsHTTP.URL, true)
			prepare, e := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE kind='subscription_completion_prepare' AND json_extract(payload,'$.id')=? AND state='queued'`, s.ID))
			if e != nil {
				t.Fatal("actual full mode completion was not queued", e)
			}
			prepare = qualityRunningJob(t, a, prepare)
			if e = a.safeRun(ctx, prepare); e != nil {
				t.Fatal("actual None full pack rejected original completion", e)
			}
			finish, e := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='subscription_completion' ORDER BY rowid DESC LIMIT 1`))
			if e != nil {
				t.Fatal(e)
			}
			finish = qualityRunningJob(t, a, finish)
			if e = a.safeRun(ctx, finish); e != nil {
				t.Fatal("full mode did not run actual default Notice/event/stats", e)
			}
			statJob, statReceipt := subscriptionStatsOnly(t, a)
			statJob = qualityRunningJob(t, a, statJob)
			if e = a.safeRun(ctx, statJob); e != nil || posts.Load() != 1 {
				t.Fatal("actual source done child", posts.Load(), e)
			}
			var completion subscriptionCompletionReceipt
			if e = a.readQualitySealed(ctx, a.Store.DB, "subscription_completion", statReceipt.Input.Completion.ID, &completion); e != nil || completion.Stage != "complete" || len(completion.Input.Accepted) != 1 {
				t.Fatal("full-mode final receipt incomplete", completion.Stage, e)
			}
			var history, events, subs int
			a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='subscription_history'`).Scan(&history)
			a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='source_subscribe_complete'`).Scan(&events)
			a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='subscription'`).Scan(&subs)
			route := noticeV2OnlyRoute(t, a)
			rendered, e := a.pluginStore.noticeBusinessRender(ctx, route.ID)
			if e != nil || history != 1 || events != 1 || subs != 0 || !strings.Contains(rendered.Snapshot.Fields["title"].(string), "已完成洗版") || fixture.Metadata.Load() != 1 || fixture.Sends.Load() != 1 {
				t.Fatal("full-mode original finish order/source template missing", history, events, subs, fixture.Metadata.Load(), fixture.Sends.Load(), e)
			}
			for _, f := range material.Files {
				importWrite(t, filepath.Join(source, f.Path), bytes.Repeat([]byte{'f'}, int(f.Size)))
			}
			complete.Store(true)
			if e = a.pollDownloads(ctx); e != nil {
				t.Fatal(e)
			}
			if e = a.Store.Get(ctx, "intent", in.ID, &in); e != nil || in.ImportPlanID == "" {
				t.Fatal("source finish lost real accepted organize authority", in.Error, e)
			}
			reorgRunJob(t, a, reorgJob(t, a, "import", in.ImportPlanID))
			var plan ImportPlan
			if e = a.Store.Get(ctx, "import_plan", in.ImportPlanID, &plan); e != nil || len(plan.Files) != total {
				t.Fatal("actual full accepted scope lost after source deletion", len(plan.Files), e)
			}
			for _, f := range plan.Files {
				var asset Asset
				if e = a.assetAtPath(ctx, f.Target, &asset); e != nil {
					t.Fatal(e)
				}
				reorgRunJob(t, a, reorgJob(t, a, "scrape", asset.ID))
				si, e := os.Stat(f.Source)
				if e != nil {
					t.Fatal(e)
				}
				ti, e := os.Stat(f.Target)
				if e != nil || !os.SameFile(si, ti) {
					t.Fatal("full-mode organize removed seed or failed link", e)
				}
			}
			if e = a.safeRun(ctx, statJob); e != nil || posts.Load() != 1 || adds.Load() != 1 || fixture.Metadata.Load() != 1 || fixture.Sends.Load() != 1 {
				t.Fatal("finished full mode repeated source actions", e, adds.Load(), posts.Load())
			}
		})
	}
}
