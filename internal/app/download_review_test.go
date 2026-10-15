package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/download"
)

func downloadReviewFixture(t *testing.T, kind string) (*App, Intent, string, *atomic.Int32) {
	t.Helper()
	a, source, _ := importTestApp(t)
	m := importFixture(t, a, kind)
	if kind == "tv" {
		importCache(t, a, "season:123:1", catalog.Season{SeriesID: 123, SeasonNumber: 1, Episodes: []catalog.Episode{{ID: 501, SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2020-01-01"}, {ID: 502, SeasonNumber: 1, EpisodeNumber: 2, AirDate: "2020-01-08"}}})
	}
	path := filepath.Join(source, "ambiguous.EP099.mkv")
	importWrite(t, path, bytes.Repeat([]byte{'x'}, 1024))
	var writes atomic.Int32
	hash := strings.Repeat("a", 40)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			task := workflowTask(hash, 1)
			task["save_path"] = source
			task["state"] = "stalledUP"
			workflowJSON(w, []any{task})
		case "/api/v2/torrents/files":
			workflowJSON(w, []any{map[string]any{"name": filepath.Base(path), "size": 1024, "progress": 1}})
		default:
			writes.Add(1)
			http.Error(w, "unexpected mutation", 500)
		}
	}))
	t.Cleanup(server.Close)
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-qb", Kind: "qb", BaseURL: server.URL}, DownloadPath: source, Mappings: []PathMapping{{From: source, To: source}}}}
	intent := Intent{ID: "owned-intent", Media: m, State: "downloaded", Hash: hash, RootID: "root", DownloaderID: "owned-qb", ImportPlanID: "old-review-plan", WorkflowID: "wf-review", ImportMode: "hardlink"}
	if kind == "tv" {
		intent.Season = 1
		intent.Episodes = []int{1}
	}
	ctx := context.Background()
	if err := a.Store.Put(ctx, "intent", intent.ID, intent); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "import_plan", intent.ImportPlanID, ImportPlan{ID: intent.ImportPlanID, IntentID: intent.ID, Media: m, RootID: "root", State: "needs_review"}); err != nil {
		t.Fatal(err)
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Reserve(ctx, tx, mediaKey(m), intent.Season, intent.Episodes, intent.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return a, intent, path, &writes
}
func runDownloadReviewImport(t *testing.T, a *App, job Job) {
	t.Helper()
	claimed, err := a.claim(context.Background())
	if err != nil || claimed.ID != job.ID {
		t.Fatalf("claim %+v %v", claimed, err)
	}
	if err = a.safeRun(context.Background(), claimed); err != nil {
		t.Fatal("manual correction import", err)
	}
}
func TestDownloadedReservationCanBeCorrectedWithoutRemovingTorrent(t *testing.T) {
	a, in, path, writes := downloadReviewFixture(t, "tv")
	ctx := context.Background()
	listed, err := a.downloadFiles(ctx, in.ID)
	if err != nil || len(listed.Files) != 1 || listed.Files[0].ApplicationPath != path || listed.Files[0].Progress != 1 {
		t.Fatalf("actual managed file list %+v %v", listed, err)
	}
	// Independent manual import is blocked by the original download ownership.
	plain, err := a.previewImport(ctx, ImportRequest{MediaType: "tv", TMDBID: 123, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: path, Season: 1, Episodes: []int{1}}}})
	if err != nil || plain.Files[0].Status != "blocked" {
		t.Fatalf("fixture lost original reservation: %+v %v", plain, err)
	}
	preview, err := a.previewDownloadReview(ctx, in.ID, DownloadReviewRequest{Files: []ImportFileRequest{{Path: path, Season: 1, Episodes: []int{1}}}})
	if err != nil || preview.TargetChanged || preview.Plan.Files[0].Status != "ready" {
		t.Fatalf("linked preview %+v %v", preview, err)
	}
	if _, err = a.executeImport(ctx, preview.Plan.ID, nil); err == nil {
		t.Fatal("generic execute bypassed explicit review confirmation")
	}
	job, err := a.confirmDownloadReview(ctx, in.ID, preview.Plan.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := a.confirmDownloadReview(ctx, in.ID, preview.Plan.ID, false)
	if err != nil || repeat.ID != job.ID {
		t.Fatalf("confirmation not idempotent: %+v %v", repeat, err)
	}
	runDownloadReviewImport(t, a, job)
	var final Intent
	if err = a.Store.Get(ctx, "intent", in.ID, &final); err != nil || final.State != "imported" || final.Hash != in.Hash || final.ImportPlanID != preview.Plan.ID {
		t.Fatalf("final %+v %v", final, err)
	}
	var reservations int
	a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM reservations WHERE intent_id=?`, in.ID).Scan(&reservations)
	if reservations != 0 || writes.Load() != 0 {
		t.Fatalf("remaining reservations=%d downloader mutations=%d", reservations, writes.Load())
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("download source removed", err)
	}
	var old ImportPlan
	a.Store.Get(ctx, "import_plan", in.ImportPlanID, &old)
	if old.State != "superseded" {
		t.Fatalf("old plan state %s", old.State)
	}
	if _, err = a.executeImport(ctx, old.ID, nil); err == nil {
		t.Fatal("superseded plan was executable")
	}
}
func TestDownloadIdentityCorrectionRequiresConfirmationAndPausesOriginalSubscription(t *testing.T) {
	a, in, path, writes := downloadReviewFixture(t, "movie")
	ctx := context.Background()
	in.SubscriptionID = "original-sub"
	in.SubscriptionRevision = 4
	in.Auto = true
	a.Store.Put(ctx, "intent", in.ID, in)
	a.Store.Put(ctx, "subscription", in.SubscriptionID, Subscription{ID: in.SubscriptionID, Media: in.Media, State: "active", Revision: 4, NextCheck: now()})
	correct := catalog.Media{ID: 124, MediaType: "movie", Title: "已人工确认的正确影片", Year: 2024}
	importCache(t, a, "media:movie:124", correct)
	preview, err := a.previewDownloadReview(ctx, in.ID, DownloadReviewRequest{MediaType: "movie", TMDBID: 124, Files: []ImportFileRequest{{Path: path}}})
	if err != nil || !preview.IdentityChanged || !preview.TargetChanged || !preview.SubscriptionWillPause {
		t.Fatalf("retarget preview %+v %v", preview, err)
	}
	if _, err = a.confirmDownloadReview(ctx, in.ID, preview.Plan.ID, false); err == nil {
		t.Fatal("target change lacked explicit confirmation")
	}
	var unchanged Intent
	a.Store.Get(ctx, "intent", in.ID, &unchanged)
	if unchanged.Media.ID != 123 || unchanged.SubscriptionID != "original-sub" {
		t.Fatal("preview/declined confirmation changed original intent")
	}
	job, err := a.confirmDownloadReview(ctx, in.ID, preview.Plan.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	var subscription Subscription
	a.Store.Get(ctx, "subscription", "original-sub", &subscription)
	if subscription.State != "paused" || subscription.Revision != 5 || subscription.NextCheck != "" {
		t.Fatalf("original subscription %+v", subscription)
	}
	var oldReservations, newReservations int
	a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM reservations WHERE media_key='movie:123'`).Scan(&oldReservations)
	a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM reservations WHERE media_key='movie:124' AND intent_id=?`, in.ID).Scan(&newReservations)
	if oldReservations != 0 || newReservations != 1 {
		t.Fatalf("retarget not atomic old=%d new=%d", oldReservations, newReservations)
	}
	runDownloadReviewImport(t, a, job)
	var final Intent
	a.Store.Get(ctx, "intent", in.ID, &final)
	if final.State != "imported" || final.Media.ID != 124 || final.SubscriptionID != "" || final.Auto {
		t.Fatalf("retarget final %+v", final)
	}
	if writes.Load() != 0 {
		t.Fatal("retarget modified real downloader")
	}
}
func TestDownloadCorrectionRejectsUnownedAndChangedSources(t *testing.T) {
	a, in, path, writes := downloadReviewFixture(t, "movie")
	ctx := context.Background()
	other := filepath.Join(filepath.Dir(path), "other.mkv")
	importWrite(t, other, []byte("not in this torrent"))
	if _, err := a.previewDownloadReview(ctx, in.ID, DownloadReviewRequest{Files: []ImportFileRequest{{Path: other}}}); err == nil {
		t.Fatal("unowned source accepted")
	}
	preview, err := a.previewDownloadReview(ctx, in.ID, DownloadReviewRequest{Files: []ImportFileRequest{{Path: path}}})
	if err != nil {
		t.Fatal(err)
	}
	importWrite(t, path, []byte("changed since preview"))
	if _, err = a.confirmDownloadReview(ctx, in.ID, preview.Plan.ID, false); err == nil {
		t.Fatal("changed source confirmed")
	}
	var count int
	a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM reservations WHERE intent_id=?`, in.ID).Scan(&count)
	if count != 1 || writes.Load() != 0 {
		t.Fatal("failed review discarded ownership or touched downloader")
	}
}

func TestDownloadRetargetConflictRollsBackOriginalReservationAndSubscription(t *testing.T) {
	a, in, path, _ := downloadReviewFixture(t, "movie")
	ctx := context.Background()
	in.SubscriptionID = "original-sub"
	in.SubscriptionRevision = 4
	a.Store.Put(ctx, "intent", in.ID, in)
	a.Store.Put(ctx, "subscription", in.SubscriptionID, Subscription{ID: in.SubscriptionID, Media: in.Media, State: "active", Revision: 4, NextCheck: now()})
	importCache(t, a, "media:movie:124", catalog.Media{ID: 124, MediaType: "movie", Title: "正确影片", Year: 2024})
	preview, err := a.previewDownloadReview(ctx, in.ID, DownloadReviewRequest{MediaType: "movie", TMDBID: 124, Files: []ImportFileRequest{{Path: path}}})
	if err != nil {
		t.Fatal(err)
	}
	// Another acquisition wins the proposed target after the review was shown.
	if _, err = a.Store.DB.ExecContext(ctx, `INSERT INTO reservations VALUES('movie:124',0,0,'other-intent')`); err != nil {
		t.Fatal(err)
	}
	if _, err = a.confirmDownloadReview(ctx, in.ID, preview.Plan.ID, true); err == nil {
		t.Fatal("conflicting retarget confirmed")
	}
	var owner string
	if err = a.Store.DB.QueryRowContext(ctx, `SELECT intent_id FROM reservations WHERE media_key='movie:123' AND season=0 AND episode=0`).Scan(&owner); err != nil || owner != in.ID {
		t.Fatalf("original reservation lost owner=%s err=%v", owner, err)
	}
	var sub Subscription
	a.Store.Get(ctx, "subscription", in.SubscriptionID, &sub)
	if sub.State != "active" || sub.Revision != 4 {
		t.Fatalf("failed retarget paused subscription %+v", sub)
	}
	var original Intent
	a.Store.Get(ctx, "intent", in.ID, &original)
	if original.Media.ID != 123 || original.ImportPlanID != in.ImportPlanID {
		t.Fatalf("failed retarget rewrote intent %+v", original)
	}
}

func TestDownloadReviewHTTPContractCompletesOriginalIntent(t *testing.T) {
	a, in, path, writes := downloadReviewFixture(t, "tv")
	cookie := setupTestAdmin(t, a)
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	request := func(method, route string, body any) json.RawMessage {
		t.Helper()
		var encoded []byte
		if body != nil {
			encoded, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, server.URL+"/api/"+route, bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", server.URL)
		req.AddCookie(cookie)
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var envelope struct {
			Data  json.RawMessage `json:"data"`
			Error *APIError       `json:"error"`
		}
		if err = json.NewDecoder(response.Body).Decode(&envelope); err != nil || response.StatusCode != 200 || envelope.Error != nil {
			t.Fatalf("HTTP %s %s status=%d err=%v api=%+v", method, route, response.StatusCode, err, envelope.Error)
		}
		return envelope.Data
	}
	var files downloadReviewFiles
	if err := json.Unmarshal(request("GET", "downloads/"+in.ID+"/files", nil), &files); err != nil || len(files.Files) != 1 || files.Files[0].ApplicationPath != path {
		t.Fatalf("GET files %+v err=%v", files, err)
	}
	var preview DownloadReviewPreview
	if err := json.Unmarshal(request("POST", "downloads/"+in.ID+"/import-preview", DownloadReviewRequest{Files: []ImportFileRequest{{Path: path, Season: 1, Episodes: []int{1}}}}), &preview); err != nil || len(preview.Plan.Files) != 1 || preview.Plan.Files[0].Status != "ready" {
		t.Fatalf("HTTP preview %+v %v", preview, err)
	}
	var job Job
	if err := json.Unmarshal(request("POST", "downloads/"+in.ID+"/import-confirm", map[string]any{"plan_id": preview.Plan.ID, "confirm_target_change": false}), &job); err != nil || job.Kind != "import" {
		t.Fatalf("HTTP confirm %+v %v", job, err)
	}
	runDownloadReviewImport(t, a, job)
	if err := json.Unmarshal(request("GET", "downloads/"+in.ID+"/files", nil), &files); err != nil || files.Intent.State != "imported" {
		t.Fatalf("HTTP completed %+v %v", files.Intent, err)
	}
	if writes.Load() != 0 {
		t.Fatal("HTTP correction removed/modified downloader seed")
	}
}
