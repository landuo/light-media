package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/catalog"
)

func TestOrdinarySubscriptionCompletionUsesRealAcceptedDownloadWithoutPriority100(t *testing.T) {
	for _, kind := range []string{"movie", "tv"} {
		t.Run(kind, func(t *testing.T) {
			ordinaryMovieDownloadWorkflow(t, kind, func(a *App, s Subscription) {
				ctx := context.Background()
				accepted, e := a.completionAcceptedClosure(ctx, a.Store.DB, s)
				if e != nil || len(accepted) != 1 {
					t.Fatal("ordinary completion lacks actual accepted closure", len(accepted), e)
				}
				basis, complete, e := a.ordinaryCompletionBasis(ctx, a.Store.DB, s, accepted)
				if e != nil || !complete || basis == nil || len(basis.Inventory) != 0 || len(basis.AcceptedEpisodes) != 1 {
					t.Fatal("ordinary acceptance was treated as best priority100", basis, complete, e)
				}
				var fact subscriptionQualityAccepted
				if e = cleanupJournalStrict(accepted[0].Raw, &fact); e != nil || fact.Resource.Priority == 100 {
					t.Fatal("fixture unexpectedly uses highest quality", fact, e)
				}
				if e = a.Store.Delete(ctx, "download_post_add", accepted[0].ID); e != nil {
					t.Fatal(e)
				}
				if _, e = a.completionAcceptedClosure(ctx, a.Store.DB, s); e == nil {
					t.Fatal("orphan accepted fact replaced actual post-Add proof")
				}
			})
		})
	}
}

// A real owned search -> preview -> confirm -> Client.Add -> accepted SQL path.
// The callback lets the composed host run the same real completion/Notice chain
// without a compact MediaInfo fixture or a manufactured completion receipt.
func ordinaryMovieDownloadWorkflow(t *testing.T, kind string, after func(*App, Subscription)) {
	t.Helper()
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	m := importFixture(t, a, kind)
	name, listing := "测试影片 (2024) 1080p.mkv", "测试影片.2024.1080p.WEB-DL.x264-TEST"
	season := 0
	if kind == "tv" {
		name, listing, season = "测试剧集 S01E01 1080p.mkv", "测试剧集.S01E01.1080p.WEB-DL.x264-TEST", 1
		importCache(t, a, "season:123:1", catalog.Season{SeriesID: m.ID, SeasonNumber: 1, Episodes: []catalog.Episode{{ID: 1, EpisodeNumber: 1, SeasonNumber: 1, AirDate: "2024-01-01"}}})
	}
	torrent, hash := workflowTorrent(name)
	var added atomic.Bool
	var adds atomic.Int64
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			if added.Load() {
				task := workflowTask(hash, 0)
				task["save_path"] = source
				workflowJSON(w, []any{task})
			} else {
				workflowJSON(w, []any{})
			}
		case "/api/v2/torrents/add":
			if e := r.ParseMultipartForm(4 << 20); e != nil || r.FormValue("savepath") != source {
				t.Error("actual ordinary Add input", e, r.Form)
			}
			adds.Add(1)
			added.Store(true)
			io.WriteString(w, "Ok.")
		default:
			t.Error("unexpected ordinary downloader call", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			io.WriteString(w, workflowHTML(listing, "", false))
		case "/download.php":
			w.Write(torrent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	workflowConfigure(t, a, site.URL, qb.URL)
	a.config.Downloaders[0].DownloadPath = source
	a.config.Downloaders[0].SharedPaths = true
	request := SubscriptionRequest{MediaType: kind, TMDBID: m.ID, Season: season, DownloaderID: "fixture-qb", DownloadPath: source, RootID: "root", Schedule: Schedule{Mode: "manual"}}
	if kind == "tv" {
		request.TotalEpisodes = 1
	}
	s, e := a.saveSubscription(ctx, "", request)
	if e != nil {
		t.Fatal(e)
	}
	j := importClaim(t, a, "subscription", s.ID)
	if e = a.checkSubscription(ctx, j, s.ID, s.Revision); e != nil {
		t.Fatal(e)
	}
	raw, e := a.Store.List(ctx, "intent", 10, 0)
	if e != nil || len(raw) != 1 {
		t.Fatal("actual ordinary subscription did not plan one resource", len(raw), e)
	}
	var in Intent
	if e = json.Unmarshal(raw[0], &in); e != nil {
		t.Fatal(e)
	}
	reorgRunJob(t, a, reorgJob(t, a, "download", in.ID))
	if e = a.Store.Get(ctx, "subscription", s.ID, &s); e != nil || s.BestVersion || s.State != "active" || s.SourceState != "R" || !bytes.Equal(mustCleanSeedJSON(s.SourceNote), []byte(`[1]`)) || adds.Load() != 1 {
		t.Fatal("ordinary real Add did not update source note / queued completion", s, adds.Load(), e)
	}
	if kind == "tv" && (s.LackEpisode == nil || *s.LackEpisode != 0) {
		t.Fatal("ordinary lack ignores actually accepted download", s.LackEpisode)
	}
	var jobs int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='subscription_completion_prepare' AND json_extract(payload,'$.id')=?`, s.ID).Scan(&jobs); e != nil || jobs != 1 {
		t.Fatal("real Add did not queue original completion preparation", jobs, e)
	}
	if after != nil {
		after(a, s)
	}
}

func TestOrdinaryCompletionUsesRealInventoryAndKeepsActiveUntilOriginalFinish(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	m := importFixture(t, a, "movie")
	input := filepath.Join(source, "测试影片 (2024).mkv")
	importWrite(t, input, []byte("owned original inventory"))
	plan, e := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: m.ID, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: input}}})
	if e != nil {
		t.Fatal(e)
	}
	j, e := a.executeImport(ctx, plan.ID, nil)
	if e != nil {
		t.Fatal(e)
	}
	reorgRunJob(t, a, j)
	workflowConfigure(t, a, "http://127.0.0.1:1", "http://127.0.0.1:1")
	s, e := a.saveSubscription(ctx, "", SubscriptionRequest{MediaType: "movie", TMDBID: m.ID, DownloaderID: "fixture-qb", RootID: "root", Schedule: fridaySchedule(t)})
	if e != nil {
		t.Fatal(e)
	}
	check := importClaim(t, a, "subscription", s.ID)
	if e = a.checkSubscription(ctx, check, s.ID, s.Revision); e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Get(ctx, "subscription", s.ID, &s); e != nil || s.State != "active" {
		t.Fatal("inventory bypassed source completion via native completed flag", s.State, e)
	}
	if next, e := time.Parse(time.RFC3339Nano, s.NextCheck); e != nil || !next.After(time.Now()) {
		t.Fatal("pending completion retained an immediately due scheduler time", s.NextCheck, e)
	}
	if e = a.prepareSubscriptionCompletion(ctx, check, s.ID, s.Revision); e == nil {
		t.Fatal("ordinary subscription job was accepted as completion preparation")
	} else {
		var api *APIError
		if !errors.As(e, &api) || api.Code != "SUBSCRIPTION_COMPLETION_UNCONFIRMED" {
			t.Fatal("wrong job reached source factory instead of actual job guard", e)
		}
	}
	accepted, e := a.completionAcceptedClosure(ctx, a.Store.DB, s)
	if e != nil || len(accepted) != 0 {
		t.Fatal("inventory fabricated actual download", len(accepted), e)
	}
	basis, complete, e := a.ordinaryCompletionBasis(ctx, a.Store.DB, s, accepted)
	if e != nil || !complete || len(basis.Inventory) != 1 || len(basis.AcceptedEpisodes) != 0 {
		t.Fatal("owned actual inventory did not supply ordinary force-completion basis", basis, complete, e)
	}
	importWrite(t, plan.Files[0].Target, []byte("externally changed library video"))
	if _, complete, e = a.ordinaryCompletionBasis(ctx, a.Store.DB, s, accepted); e == nil || complete {
		t.Fatal("changed library file still supplies completion proof", complete, e)
	}
}

func TestOrdinaryCompletionCombinesEarlierRevisionActualDownloadWithCurrentSeasonInventory(t *testing.T) {
	ordinaryMovieDownloadWorkflow(t, "tv", func(a *App, original Subscription) {
		ctx := context.Background()
		importCache(t, a, "season:123:1", catalog.Season{SeriesID: original.Media.ID, SeasonNumber: 1, Episodes: []catalog.Episode{{ID: 1, EpisodeNumber: 1, SeasonNumber: 1, AirDate: "2024-01-01"}, {ID: 2, EpisodeNumber: 2, SeasonNumber: 1, AirDate: "2024-01-01"}, {ID: 3, EpisodeNumber: 3, SeasonNumber: 1, AirDate: "2024-01-01"}}})
		for _, name := range []string{"测试剧集 S01E02 1080p.mkv", "测试剧集 S01E03 1080p.mkv"} {
			path := filepath.Join(a.config.AllowedRoots[0], name)
			importWrite(t, path, []byte("owned existing episode"))
			plan, e := a.previewImport(ctx, ImportRequest{MediaType: "tv", TMDBID: original.Media.ID, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: path}}})
			if e != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
				t.Fatal("actual existing season inventory", plan, e)
			}
			job, e := a.executeImport(ctx, plan.ID, nil)
			if e != nil {
				t.Fatal(e)
			}
			reorgRunJob(t, a, job)
		}
		current, e := a.saveSubscription(ctx, original.ID, SubscriptionRequest{MediaType: "tv", TMDBID: original.Media.ID, Season: 1, TotalEpisodes: 3, DownloaderID: original.DownloaderID, RootID: original.RootID, DownloadPath: original.DownloadPath, Revision: original.Revision, Schedule: Schedule{Mode: "manual"}})
		if e != nil || current.Revision == original.Revision {
			t.Fatal("actual season edit", current, e)
		}
		accepted, e := a.completionAcceptedClosure(ctx, a.Store.DB, current)
		if e != nil || len(accepted) != 1 {
			t.Fatal("real old revision fact was discarded", len(accepted), e)
		}
		basis, complete, e := a.ordinaryCompletionBasis(ctx, a.Store.DB, current, accepted)
		if e != nil || !complete || len(basis.Inventory) != 2 || !equalQualityEpisodes(basis.AcceptedEpisodes, []int{1}) || !equalQualityEpisodes(basis.Targets, []int{1, 2, 3}) {
			t.Fatal("mixed actual inventory/download season did not complete", basis, complete, e)
		}
		current.SourceState = "P"
		if _, complete, e = a.ordinaryCompletionBasis(ctx, a.Store.DB, current, accepted); e != nil || complete {
			t.Fatal("pending source state bypassed completion guard", complete, e)
		}
	})
}

func TestOrdinaryCompletionNewRevisionRevokesOnlyObsoletePreparation(t *testing.T) {
	ordinaryMovieDownloadWorkflow(t, "tv", func(a *App, original Subscription) {
		ctx := context.Background()
		old := qualityRunningJob(t, a, reorgJob(t, a, "subscription_completion_prepare", original.ID))
		current, e := a.saveSubscription(ctx, original.ID, SubscriptionRequest{MediaType: "tv", TMDBID: original.Media.ID, Season: 1, TotalEpisodes: 1, DownloaderID: original.DownloaderID, RootID: original.RootID, DownloadPath: original.DownloadPath, Revision: original.Revision, Schedule: Schedule{Mode: "manual"}})
		if e != nil {
			t.Fatal(e)
		}
		tx, e := a.Store.DB.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		for i := 0; i < 2; i++ {
			if e = enqueueSubscriptionCompletionPreparationForTx(ctx, tx, current, old.WorkflowID); e != nil {
				t.Fatal("obsolete revision blocked actual accepted transaction", e)
			}
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
		prior, e := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, old.ID))
		if e != nil || prior.State != "cancelled" || prior.Generation <= old.Generation {
			t.Fatal("running obsolete preparation retained mutation scope", prior, e)
		}
		if e = requireSubscriptionPreparationJob(ctx, a.Store.DB, old, original.ID, original.Revision); e == nil {
			t.Fatal("obsolete job retained source completion grant")
		}
		var count int
		if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='subscription_completion_prepare' AND json_extract(payload,'$.id')=? AND json_extract(payload,'$.revision')=? AND state='queued'`, current.ID, current.Revision).Scan(&count); e != nil || count != 1 {
			t.Fatal("current revision queue is not idempotent", count, e)
		}
		var intentCount int
		if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='intent' AND json_extract(payload,'$.state')='accepted'`).Scan(&intentCount); e != nil || intentCount != 1 {
			t.Fatal("preparation scope edit changed actual download", intentCount, e)
		}
	})
}
