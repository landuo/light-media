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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/metainfo"
	"light-media/internal/priorityrules"
	"light-media/internal/releasemeta"
)

// Original SubscribeChain tries a qualifying full pack before even a higher
// ranked single episode. Only actual daemon acceptance counts as a download.
func TestSubscriptionQualityActualBatchFullPackFirstKnownFailureFallbackUnknownStops(t *testing.T) {
	for _, branch := range []string{"full_pack", "known_reject_fallback", "unknown_stops", "cancel_before_add", "root_deleted_before_add", "cancel_after_add", "accept_commit_cold", "unknown_cold_new_generation", "cancel_after_add_cold_fault", "revision_after_add", "removed_after_add", "cancel_after_single", "unknown_after_cancel", "root_deleted_after_add", "receipt_tamper_cold", "receipt_wrong_job_cold", "receipt_wrong_child_cold", "subscription_cancel_after_add", "full_pack_extra_before_start", "full_pack_extra_sparse", "full_pack_extra_existing"} {
		t.Run(branch, func(t *testing.T) {
			extra := strings.HasPrefix(branch, "full_pack_extra")
			acquiredEpisodes, targetEpisodes := []int{1, 2}, []int{1, 2}
			if extra {
				acquiredEpisodes, targetEpisodes = []int{1, 2, 3}, []int{2, 3}
			}
			if branch == "full_pack_extra_sparse" {
				acquiredEpisodes, targetEpisodes = []int{1, 3}, []int{3}
			}
			a, source, _ := importTestApp(t)
			ctx := context.Background()
			var job Job
			var s Subscription
			var changed Subscription
			var forbidRemote atomic.Bool
			var complete atomic.Bool
			mutated := false
			m := importFixture(t, a, "tv")
			season := catalog.Season{SeriesID: m.ID, SeasonNumber: 1, Episodes: []catalog.Episode{{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2024-01-01"}, {ID: 2, SeasonNumber: 1, EpisodeNumber: 2, AirDate: "2024-01-01"}}}
			if extra {
				for i := range season.Episodes {
					season.Episodes[i].Name = "episode"
				}
				season.Episodes = append(season.Episodes, catalog.Episode{ID: 3, SeasonNumber: 1, EpisodeNumber: 3, AirDate: "2024-01-01", Name: "episode"})
			}
			importCache(t, a, "season:123:1", season)
			titles := []string{"测试剧集.S01E02.2160p.WEB-DL.x264-TEST", "测试剧集.S01.1080p.WEB-DL.x264-TEST", "测试剧集.S01E01.2160p.WEB-DL.x264-TEST"}
			ids := []string{"101", "102", "103"}
			data := map[string][]byte{}
			data[ids[0]], _ = workflowTorrent("测试剧集 S01E02 2160p.mkv")
			data[ids[1]] = automaticPackTorrent([]string{"测试剧集 S01E01 1080p.mkv", "测试剧集 S01E02 1080p.mkv"})
			data[ids[2]], _ = workflowTorrent("测试剧集 S01E01 2160p.mkv")
			if extra {
				titles[0] = "测试剧集.S01E03.2160p.WEB-DL.x264-TEST"
				titles[2] = "测试剧集.S01E02.2160p.WEB-DL.x264-TEST"
				data[ids[0]], _ = workflowTorrent("测试剧集 S01E03 2160p.mkv")
				data[ids[1]] = automaticPackTorrent([]string{"测试剧集 S01E01 1080p.mkv", "测试剧集 S01E02 1080p.mkv", "测试剧集 S01E03 1080p.mkv"})
				data[ids[2]], _ = workflowTorrent("测试剧集 S01E02 2160p.mkv")
				if branch == "full_pack_extra_sparse" {
					data[ids[1]] = automaticPackTorrent([]string{"测试剧集 S01E01 1080p.mkv", "测试剧集 S01E03 1080p.mkv"})
				}
			}
			materials := map[string]metainfo.Material{}
			byHash := map[string]string{}
			for id, raw := range data {
				material, e := metainfo.ParseMaterial(raw)
				if e != nil {
					t.Fatal(e)
				}
				materials[id], byHash[material.Hash] = material, id
			}
			var mu sync.Mutex
			adds := []string{}
			accepted := map[string]bool{}
			qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if forbidRemote.Load() {
					t.Error("receipt-only cold recovery touched downloader", r.URL.Path)
					w.WriteHeader(503)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					if !mutated && (branch == "cancel_before_add" || branch == "root_deleted_before_add") {
						mutated = true
						if branch == "cancel_before_add" {
							if _, e := a.Store.DB.Exec(`UPDATE jobs SET state='cancelled',generation=generation+1 WHERE id=?`, job.ID); e != nil {
								t.Error(e)
							}
						} else {
							if _, e := a.Store.DB.Exec(`DELETE FROM records WHERE kind='subscription_acquisition' AND id=?`, job.ID); e != nil {
								t.Error(e)
							}
						}
					}
					rows := []any{}
					for id := range accepted {
						if filter := r.URL.Query().Get("hashes"); filter != "" && !strings.Contains(filter, materials[id].Hash) {
							continue
						}
						progress := 0.0
						if complete.Load() {
							progress = 1
						}
						task := workflowTask(materials[id].Hash, progress)
						if extra {
							task["total_size"] = materials[id].TotalBytes
							if complete.Load() {
								task["completed"] = materials[id].TotalBytes
							}
						}
						task["save_path"] = source
						rows = append(rows, task)
					}
					workflowJSON(w, rows)
				case "/api/v2/torrents/add":
					if e := r.ParseMultipartForm(4 << 20); e != nil {
						t.Error(e)
						return
					}
					file, _, e := r.FormFile("torrents")
					if e != nil {
						t.Error(e)
						return
					}
					raw, _ := io.ReadAll(file)
					file.Close()
					material, e := metainfo.ParseMaterial(raw)
					id := byHash[material.Hash]
					if e != nil || id == "" {
						t.Error("unknown actual Add material", e)
						return
					}
					adds = append(adds, id)
					// Source complete episode matches also use episodes=None.
					paused := "false"
					if r.FormValue("stopped") != paused || r.FormValue("savepath") != source {
						t.Error("batch lost original None/selected or download path", id, r.FormValue("stopped"), r.FormValue("savepath"))
					}
					if id == "102" && (branch == "known_reject_fallback" || branch == "cancel_after_single") {
						io.WriteString(w, "Fails.")
						return
					}
					if branch == "unknown_after_cancel" {
						if _, e := a.Store.DB.Exec(`UPDATE jobs SET state='cancelled',generation=generation+1 WHERE id=?`, job.ID); e != nil {
							t.Error(e)
						}
					}
					if id == "102" && strings.HasPrefix(branch, "unknown") {
						io.WriteString(w, "proxy response with no acknowledged outcome")
						return
					}
					accepted[id] = true
					if strings.HasPrefix(branch, "cancel_after_add") || strings.HasPrefix(branch, "receipt_") || branch == "cancel_after_single" && id == "101" {
						if _, e := a.Store.DB.Exec(`UPDATE jobs SET state='cancelled',generation=generation+1 WHERE id=?`, job.ID); e != nil {
							t.Error(e)
						}
					}
					if branch == "subscription_cancel_after_add" {
						if ce := a.cancelSubscription(ctx, s.ID); ce != nil {
							t.Error(ce)
						}
					}
					if branch == "revision_after_add" {
						var e error
						changed, e = a.saveSubscription(ctx, s.ID, SubscriptionRequest{MediaType: "tv", TMDBID: m.ID, Season: 1, TotalEpisodes: 2, DownloaderID: "fixture-qb", DownloadPath: source, RootID: "root", BestVersion: func() *bool { v := true; return &v }(), Revision: s.Revision, Policy: Policy{Include: "4K"}, Schedule: Schedule{Mode: "manual"}})
						if e != nil {
							t.Error(e)
						}
					}
					if branch == "root_deleted_after_add" {
						if _, e := a.Store.DB.Exec(`DELETE FROM records WHERE kind='subscription_acquisition' AND id=?`, job.ID); e != nil {
							t.Error(e)
						}
					}
					if branch == "removed_after_add" {
						if _, e := a.Store.DB.Exec(`DELETE FROM records WHERE kind='subscription' AND id=?`, s.ID); e != nil {
							t.Error(e)
						}
					}
					io.WriteString(w, "Ok.")
				case "/api/v2/torrents/files":
					id := byHash[r.URL.Query().Get("hash")]
					rows := []any{}
					for index, f := range materials[id].Files {
						rows = append(rows, map[string]any{"index": index, "name": f.Path, "size": f.Size, "progress": func() int {
							if complete.Load() {
								return 1
							}
							return 0
						}()})
					}
					workflowJSON(w, rows)
				case "/api/v2/torrents/start":
					w.WriteHeader(200)
				default:
					t.Error("unexpected owned batch endpoint", r.URL.Path)
					w.WriteHeader(400)
				}
			}))
			defer qb.Close()
			site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if forbidRemote.Load() {
					t.Error("receipt-only cold recovery touched tracker", r.URL.Path)
					w.WriteHeader(503)
					return
				}
				switch r.URL.Path {
				case "/torrents.php":
					var page strings.Builder
					for index, title := range titles {
						html := strings.ReplaceAll(workflowHTML(title, "", false), "id=101", "id="+ids[index])
						if index == 0 {
							page.WriteString(strings.TrimSuffix(html, "</table>"))
						} else {
							page.WriteString(strings.TrimSuffix(html[strings.Index(html, "<tr><td>类别"):], "</table>"))
						}
					}
					io.WriteString(w, page.String()+"</table>")
				case "/download.php":
					w.Write(data[r.URL.Query().Get("id")])
				default:
					http.NotFound(w, r)
				}
			}))
			defer site.Close()
			workflowConfigure(t, a, site.URL, qb.URL)
			a.config.Downloaders[0].DownloadPath, a.config.Downloaders[0].SharedPaths = source, true
			a.config.PriorityRules = &priorityrules.Configuration{SubscribeGroups: []string{"quality"}, Groups: []priorityrules.Group{{Name: "quality", RuleString: priorityText("WEBDL & 4K > WEBDL & 1080P")}}, Mode: priorityrules.Rust}
			directoryID := ""
			if extra {
				directoryID = "quality-extra"
				mode := "always"
				a.config.Directories = []DirectoryRule{{ID: directoryID, Enabled: true, MonitorType: "monitor", SourcePath: source, RootID: "root", MediaType: "tv", Mode: "hardlink", OverwriteModeKnown: true, OverwriteMode: &mode}}
			}
			var protected Asset
			protectedSource := filepath.Join(source, "old-测试剧集 S01E01 2160p.mkv")
			if branch == "full_pack_extra_existing" {
				importWrite(t, protectedSource, bytes.Repeat([]byte{'o'}, 1024))
				plan, pe := a.previewImport(ctx, ImportRequest{MediaType: "tv", TMDBID: m.ID, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: protectedSource, Season: 1, Episodes: []int{1}}}})
				if pe != nil {
					t.Fatal(pe)
				}
				j, pe := a.executeImport(ctx, plan.ID, nil)
				if pe != nil {
					t.Fatal(pe)
				}
				reorgRunJob(t, a, j)
				if pe = a.assetAtPath(ctx, plan.Files[0].Target, &protected); pe != nil {
					t.Fatal(pe)
				}
			}
			best := true
			total, startEpisode := 2, 1
			if extra {
				total, startEpisode = 3, targetEpisodes[0]
			}
			s, e := a.saveSubscription(ctx, "", SubscriptionRequest{MediaType: "tv", TMDBID: m.ID, Season: 1, TotalEpisodes: total, StartEpisode: startEpisode, DownloaderID: "fixture-qb", DownloadPath: source, DirectoryID: directoryID, RootID: "root", BestVersion: &best, Schedule: Schedule{Mode: "manual"}})
			if e != nil {
				t.Fatal(e)
			}
			s.EpisodePriority = map[string]int{"1": 20, "2": 20}
			if extra {
				s.EpisodePriority = map[string]int{}
				for _, ep := range targetEpisodes {
					s.EpisodePriority[strconv.Itoa(ep)] = 20
				}
			}
			if e = a.Store.Put(ctx, "subscription", s.ID, s); e != nil {
				t.Fatal(e)
			}
			job = importClaim(t, a, "subscription", s.ID)
			if branch == "accept_commit_cold" || branch == "cancel_after_add_cold_fault" || strings.HasPrefix(branch, "receipt_") {
				if _, e = a.Store.DB.Exec(`CREATE TRIGGER acquisition_accept_failure BEFORE INSERT ON records WHEN NEW.kind='subscription_quality_accepted' BEGIN SELECT RAISE(ABORT,'owned_acquisition_accept_boundary'); END`); e != nil {
					t.Fatal(e)
				}
			}
			e = a.checkSubscription(ctx, job, s.ID, s.Revision)
			mu.Lock()
			observed := append([]string(nil), adds...)
			mu.Unlock()
			if branch == "accept_commit_cold" {
				if e == nil || len(observed) != 1 {
					t.Fatal("actual postAdd SQL fault was not reached", observed, e)
				}
				var current Subscription
				if e = a.Store.Get(ctx, "subscription", s.ID, &current); e != nil || current.EpisodePriority["1"] != 20 || current.SourceNote != nil {
					t.Fatal("failed acceptance advanced progress", current.SourceNote, current.EpisodePriority, e)
				}
				if _, e = a.Store.DB.Exec(`DROP TRIGGER acquisition_accept_failure`); e != nil {
					t.Fatal(e)
				}
				config := a.Settings()
				dir := a.Dir
				if e = a.Close(); e != nil {
					t.Fatal(e)
				}
				next, ce := New(Options{DataDir: dir})
				if ce != nil {
					t.Fatal(ce)
				}
				a = next
				t.Cleanup(func() { _ = next.Close() })
				a.config = config
				if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='queued' WHERE id=?`, job.ID); e != nil {
					t.Fatal(e)
				}
				job, e = a.claim(ctx)
				if e == nil && job.Kind == "subscription_acquisition_receipt" {
					forbidRemote.Store(true)
					if e = a.safeRun(ctx, job); e != nil {
						t.Fatal("real early receipt recovery", e)
					}
					if _, e = a.finishJob(ctx, job, "succeeded", "", "", job.RunAt); e != nil {
						t.Fatal(e)
					}
					job, e = a.claim(ctx)
				}
				if e != nil || job.Kind != "subscription" || job.Generation < 2 {
					t.Fatal("real cold parent claim", job.Kind, job.Generation, e)
				}
				forbidRemote.Store(true)
				if e = a.checkSubscription(ctx, job, s.ID, s.Revision); e != nil {
					t.Fatal("known normally returned postAdd failed cold SQL recovery", e)
				}
				mu.Lock()
				after := len(adds)
				mu.Unlock()
				if after != 1 {
					t.Fatal("known success recovery repeated daemon Add", after)
				}
				if e = a.Store.Get(ctx, "subscription", s.ID, &current); e != nil || current.EpisodePriority["1"] != 99 || current.EpisodePriority["2"] != 99 || !equalQualityEpisodes(current.SourceNote, []int{1, 2}) {
					t.Fatal("known cold acceptance lost source quality", current.SourceNote, current.EpisodePriority, e)
				}
				return
			}
			if branch == "cancel_after_add_cold_fault" || strings.HasPrefix(branch, "receipt_") {
				if e == nil || len(observed) != 1 {
					t.Fatal("actual known receipt fault not reached", observed, e)
				}
				if _, e = a.Store.DB.Exec(`DROP TRIGGER acquisition_accept_failure`); e != nil {
					t.Fatal(e)
				}
				config := a.Settings()
				dir := a.Dir
				if e = a.Close(); e != nil {
					t.Fatal(e)
				}
				next, ce := New(Options{DataDir: dir})
				if ce != nil {
					t.Fatal(ce)
				}
				a = next
				t.Cleanup(func() { _ = next.Close() })
				a.config = config
				if branch == "receipt_tamper_cold" {
					var sealed []byte
					var key string
					if ce = a.Store.DB.QueryRow(`SELECT id,payload FROM records WHERE kind='subscription_acquisition_receipt'`).Scan(&key, &sealed); ce != nil {
						t.Fatal(ce)
					}
					sealed[len(sealed)-1] ^= 1
					if _, ce = a.Store.DB.Exec(`UPDATE records SET payload=? WHERE kind='subscription_acquisition_receipt' AND id=?`, sealed, key); ce != nil {
						t.Fatal(ce)
					}
				}
				if branch == "receipt_wrong_job_cold" {
					if _, ce = a.Store.DB.Exec(`UPDATE jobs SET payload=CAST(json_set(payload,'$.post_sha','wrong') AS BLOB) WHERE kind='subscription_acquisition_receipt'`); ce != nil {
						t.Fatal(ce)
					}
				}
				if branch == "receipt_wrong_child_cold" {
					if _, ce = a.Store.DB.Exec(`UPDATE jobs SET payload=CAST(json_set(payload,'$.id','wrong') AS BLOB) WHERE kind='subscription_download'`); ce != nil {
						t.Fatal(ce)
					}
				}
				forbidRemote.Store(true)
				receiptJob, ce := a.claim(ctx)
				if ce != nil || receiptJob.Kind != "subscription_acquisition_receipt" {
					t.Fatal("actual cold receipt job absent", receiptJob.Kind, ce)
				}
				ce = a.safeRun(ctx, receiptJob)
				if strings.HasPrefix(branch, "receipt_") {
					if ce == nil {
						t.Fatal("forged local receipt/job/child blessed an acceptance")
					}
					var current Subscription
					if ce = a.Store.Get(ctx, "subscription", s.ID, &current); ce != nil || current.SourceNote != nil || current.EpisodePriority["1"] != 20 {
						t.Fatal("forged receipt advanced progress", current.SourceNote, current.EpisodePriority, ce)
					}
					mu.Lock()
					after := len(adds)
					mu.Unlock()
					if after != 1 {
						t.Fatal("forged receipt repeated Add", after)
					}
					return
				}
				if ce != nil {
					t.Fatal("real receipt dispatcher", ce)
				}
				if _, ce = a.finishJob(ctx, receiptJob, "succeeded", "", "", receiptJob.RunAt); ce != nil {
					t.Fatal(ce)
				}
				var current Subscription
				if ce = a.Store.Get(ctx, "subscription", s.ID, &current); ce != nil || current.EpisodePriority["1"] != 99 || current.EpisodePriority["2"] != 99 || !equalQualityEpisodes(current.SourceNote, []int{1, 2}) {
					t.Fatal("cold known receipt failed after parent cancel", current.SourceNote, current.EpisodePriority, ce)
				}
				mu.Lock()
				after := len(adds)
				mu.Unlock()
				if after != 1 {
					t.Fatal("cold receipt repeated Add", after)
				}
				return
			}
			if branch == "root_deleted_after_add" {
				if e == nil || len(observed) != 1 {
					t.Fatal("missing root did not stop local acceptance", observed, e)
				}
				rows, ce := a.Store.List(ctx, "intent", 10, 0)
				if ce != nil || len(rows) != 1 {
					t.Fatal(ce, len(rows))
				}
				var in Intent
				json.Unmarshal(rows[0], &in)
				post, ce := a.sourcePostAddReceipt(ctx, a.Store.DB, in)
				if ce != nil || !post.SourceReturned || in.State != "post_add" {
					t.Fatal("known normally returned receipt was rolled back with missing scope", in.State, ce)
				}
				var jobs, facts, markers int
				if ce = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='subscription_acquisition_receipt'`).Scan(&jobs); ce != nil {
					t.Fatal(ce)
				}
				a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='subscription_quality_accepted'`).Scan(&facts)
				a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='subscription_acquisition_receipt_error'`).Scan(&markers)
				if jobs != 0 || facts != 0 || markers != 1 {
					t.Fatal("missing scope fabricated native authority or dropped known receipt marker", jobs, facts, markers)
				}
				return
			}
			if branch == "revision_after_add" || branch == "removed_after_add" || branch == "cancel_after_single" || branch == "subscription_cancel_after_add" {
				want := 1
				if branch == "cancel_after_single" {
					want = 2
				}
				if len(observed) != want {
					t.Fatal("known receipt lost or continued cancelled batch", observed, e)
				}
				var fact subscriptionQualityAccepted
				rows, ce := a.Store.List(ctx, "intent", 10, 0)
				if ce != nil {
					t.Fatal(ce)
				}
				acceptedID := ""
				for _, raw := range rows {
					var in Intent
					json.Unmarshal(raw, &in)
					if in.State == "accepted" {
						acceptedID = in.ID
					}
				}
				if acceptedID == "" {
					t.Fatal("no immutable known accepted fact", e)
				}
				if ce = a.readQualitySealed(ctx, a.Store.DB, "subscription_quality_accepted", acceptedID, &fact); ce != nil {
					t.Fatal(ce)
				}
				if branch == "revision_after_add" {
					var current Subscription
					if ce = a.Store.Get(ctx, "subscription", s.ID, &current); ce != nil || current.Revision != changed.Revision || !equalQualityEpisodes(current.SourceNote, changed.SourceNote) || fact.Disposition != "subscription_changed" {
						t.Fatal("old receipt rewrote new source rules", current.Revision, changed.Revision, current.SourceNote, fact.Disposition, ce)
					}
				}
				if branch == "subscription_cancel_after_add" {
					var current Subscription
					if ce = a.Store.Get(ctx, "subscription", s.ID, &current); ce != nil || current.State != "cancelled" || current.Revision != s.Revision+1 || current.SourceNote != nil || fact.Disposition != "subscription_changed" {
						t.Fatal("known receipt resurrected cancelled subscription", current.State, current.Revision, current.SourceNote, fact.Disposition, ce)
					}
				}
				if branch == "removed_after_add" {
					var count int
					if ce = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='subscription' AND id=?`, s.ID).Scan(&count); ce != nil || count != 0 || fact.Disposition != "subscription_removed" {
						t.Fatal("old receipt resurrected removed subscription", count, fact.Disposition, ce)
					}
				}
				if branch == "cancel_after_single" {
					var current Subscription
					if ce = a.Store.Get(ctx, "subscription", s.ID, &current); ce != nil || !equalQualityEpisodes(current.SourceNote, []int{2}) || current.EpisodePriority["1"] != 20 || current.EpisodePriority["2"] != 100 {
						t.Fatal("cancelled batch advanced unaccepted episodes", current.SourceNote, current.EpisodePriority, ce)
					}
				}
				return
			}
			if branch == "cancel_after_add" {
				if len(observed) != 1 {
					t.Fatal("cancelled parent changed real Add count", observed, e)
				}
				var current Subscription
				if ce := a.Store.Get(ctx, "subscription", s.ID, &current); ce != nil || current.EpisodePriority["1"] != 99 || current.EpisodePriority["2"] != 99 || !equalQualityEpisodes(current.SourceNote, []int{1, 2}) {
					t.Fatal("known normal receipt became unknown after parent cancellation", current.SourceNote, current.EpisodePriority, e, ce)
				}
				var state string
				if ce := a.Store.DB.QueryRow(`SELECT json_extract(payload,'$.state') FROM records WHERE kind='intent'`).Scan(&state); ce != nil || state != "accepted" {
					t.Fatal("known native accepted fact lost after cancellation", state, ce)
				}
				if retry := a.checkSubscription(ctx, job, s.ID, s.Revision); retry == nil {
					t.Fatal("cancelled parent was allowed new external work")
				}
				mu.Lock()
				after := len(adds)
				mu.Unlock()
				if after != 1 {
					t.Fatal("cancelled parent repeated Add", after)
				}
				return
			}
			if branch == "cancel_before_add" || branch == "root_deleted_before_add" {
				want := 0
				if branch == "cancel_after_add" {
					want = 1
				}
				if e == nil || len(observed) != want {
					t.Fatal("revoked parent/head allowed native Add or fallback", observed, e)
				}
				var current Subscription
				if e = a.Store.Get(ctx, "subscription", s.ID, &current); e != nil || !equalQualityEpisodes(current.SourceNote, nil) || current.EpisodePriority["1"] != 20 || current.EpisodePriority["2"] != 20 {
					t.Fatal("revoked scope advanced source quality", current.SourceNote, current.EpisodePriority, e)
				}
				if e = a.checkSubscription(ctx, job, s.ID, s.Revision); e == nil {
					t.Fatal("revoked batch was recreated")
				}
				mu.Lock()
				after := len(adds)
				mu.Unlock()
				if after != want {
					t.Fatal("revoked scope retried Add", after)
				}
				return
			}
			if strings.HasPrefix(branch, "unknown") {
				var nativeReceipts int
				if ce := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='subscription_acquisition_receipt'`).Scan(&nativeReceipts); ce != nil || nativeReceipts != 0 {
					t.Fatal("unknown Add fabricated known receipt job", nativeReceipts, ce)
				}
				if e == nil || len(observed) != 1 || observed[0] != "102" {
					t.Fatal("uncertain full-pack Add did not stop actual fallback", observed, e)
				}
				if retry := a.checkSubscription(ctx, job, s.ID, s.Revision); retry == nil {
					t.Fatal("uncertain batch warm retry lost review boundary")
				}
				mu.Lock()
				after := len(adds)
				mu.Unlock()
				if after != 1 {
					t.Fatal("uncertain batch repeated daemon Add", after)
				}
				config := a.Settings()
				dir := a.Dir
				if e = a.Close(); e != nil {
					t.Fatal(e)
				}
				next, ce := New(Options{DataDir: dir})
				if ce != nil {
					t.Fatal(ce)
				}
				a = next
				t.Cleanup(func() { _ = next.Close() })
				a.config = config
				if branch == "unknown_cold_new_generation" {
					if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='queued' WHERE id=?`, job.ID); e != nil {
						t.Fatal(e)
					}
					job, e = a.claim(ctx)
					if e != nil || job.Kind != "subscription" || job.Generation < 2 {
						t.Fatal("real cold unknown parent claim", job.Kind, job.Generation, e)
					}
				}
				if retry := a.checkSubscription(ctx, job, s.ID, s.Revision); retry == nil {
					t.Fatal("cold unknown batch lost review boundary")
				}
				mu.Lock()
				after = len(adds)
				mu.Unlock()
				if after != 1 {
					t.Fatal("cold unknown repeated Add", after)
				}
				return
			}
			if e != nil || len(observed) == 0 || observed[0] != "102" {
				t.Fatal("queued intent was counted as full-pack daemon acceptance", observed, e)
			}
			want := 1
			if branch == "known_reject_fallback" {
				want = 3
			}
			if len(observed) != want {
				root, re := a.readAcquisition(ctx, a.Store.DB, job.ID)
				t.Log("native root", re)
				for _, c := range root.Candidates {
					t.Log("candidate", c.Resource.ID, c.Resource.Title, c.Accepted, c.NeedsConfirmation, c.Episodes)
				}
				for _, st := range root.Steps {
					t.Log("step", st.Input.Candidate.Resource.ID, st.Input.Episodes, st.Phase, st.Error)
				}
				t.Fatal("batch did not preserve full-pack success / known failure fallthrough", observed)
			}
			if branch == "full_pack" || extra {
				rows, e := a.Store.List(ctx, "intent", 10, 0)
				if e != nil || len(rows) != 1 {
					t.Fatal(e, len(rows))
				}
				var in Intent
				if json.Unmarshal(rows[0], &in) != nil || in.State != "accepted" || in.SelectedEpisodes != nil {
					t.Fatal("full-pack None selection was replaced by an explicit episode set", in.State, in.SelectedEpisodes)
				}
				context, e := a.loadDownloadContext(ctx, "intent", in.ID, in.Hash)
				if e != nil || context == nil {
					t.Fatal("actual full pack has no source Context", e)
				}
				meta, e := releasemeta.DecodeMetaObjectState(context.SourceMeta)
				if e != nil {
					t.Fatal("actual original MetaInfo range not persisted", e)
				}
				_, episodes, e := larkMetaNumbers(meta)
				if e != nil || !equalQualityEpisodes(episodes, func() []int {
					if extra {
						return []int{1, 2, 3}
					}
					return []int{1, 2}
				}()) {
					t.Fatal("batch-mutated original full episode range lost", episodes, e)
				}
				if extra {
					if !equalQualityEpisodes(in.Episodes, acquiredEpisodes) {
						t.Fatal("acquired pack scope clipped to subscription target", in.Episodes)
					}
					var current Subscription
					if e = a.Store.Get(ctx, "subscription", s.ID, &current); e != nil || !equalQualityEpisodes(current.SourceNote, targetEpisodes) || len(current.EpisodePriority) != len(targetEpisodes) || current.EpisodePriority["3"] != 99 || len(targetEpisodes) == 2 && current.EpisodePriority["2"] != 99 {
						t.Fatal("full pack extra episode contaminated target quality progress", current.SourceNote, current.EpisodePriority, e)
					}
					if branch == "full_pack_extra_sparse" {
						var imaginary int
						if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM reservations WHERE intent_id=? AND episode=2`, in.ID).Scan(&imaginary); e != nil || imaginary != 0 {
							t.Fatal("sparse material reserved an absent episode", imaginary, e)
						}
					}
					for _, f := range materials["102"].Files {
						importWrite(t, filepath.Join(source, f.Path), bytes.Repeat([]byte{'x'}, int(f.Size)))
					}
					complete.Store(true)
					if e = a.pollDownloads(ctx); e != nil {
						t.Fatal(e)
					}
					if e = a.Store.Get(ctx, "intent", in.ID, &in); e != nil || in.ImportPlanID == "" {
						t.Fatal("actual full pack did not plan all acquired episodes", in.Error, e)
					}
					reorgRunJob(t, a, reorgJob(t, a, "import", in.ImportPlanID))
					var plan ImportPlan
					if e = a.Store.Get(ctx, "import_plan", in.ImportPlanID, &plan); e != nil || len(plan.Files) != len(acquiredEpisodes) {
						t.Fatal("extra acquired episode lost from import", len(plan.Files), e)
					}
					var cleanups int
					if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='quality_cleanup'`).Scan(&cleanups); e != nil || cleanups != 0 {
						t.Fatal("new-only pack invented old-file cleanup", cleanups, e)
					}
					for _, f := range plan.Files {
						var asset Asset
						if e = a.assetAtPath(ctx, f.Target, &asset); e != nil {
							t.Fatal(e)
						}
						reorgRunJob(t, a, reorgJob(t, a, "scrape", asset.ID))
						if _, e = os.Stat(f.Target); e != nil {
							t.Fatal(e)
						}
						if _, e = os.Stat(f.Source); e != nil {
							t.Fatal("seeding source removed", e)
						}
					}
					if branch == "full_pack_extra_existing" {
						var previous Asset
						if e = a.Store.Get(ctx, "asset", protected.ID, &previous); e != nil || previous.State != "superseded" {
							t.Fatal("actual per-file always policy did not replace extra episode owner", previous.State, e)
						}
						if got, er := os.ReadFile(protectedSource); er != nil || !bytes.Equal(got, bytes.Repeat([]byte{'o'}, 1024)) {
							t.Fatal("extra episode replacement removed old seeding source", er)
						}
					}
					if e = a.Store.Get(ctx, "subscription", s.ID, &current); e != nil || !equalQualityEpisodes(current.SourceNote, targetEpisodes) || len(current.EpisodePriority) != len(targetEpisodes) {
						t.Fatal("import expanded subscription target", current.SourceNote, current.EpisodePriority, e)
					}
				}
			}
		})
	}
}
