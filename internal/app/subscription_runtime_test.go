package app

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/download"
)

func subscriptionTestApp(t *testing.T) *App {
	t.Helper()
	a, e := New(Options{DataDir: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = a.Close() })
	a.config = Settings{Downloaders: []DownloaderConfig{{Config: download.Config{ID: "test-qb", Kind: "qb", BaseURL: "http://127.0.0.1:1"}, DownloadPath: "/downloads"}}, Roots: []Root{{ID: "test-tv", Path: t.TempDir(), Kind: "tv"}}, DefaultDownloader: "test-qb", DefaultRoot: "test-tv"}
	m := catalog.Media{ID: 206484, MediaType: "tv", Title: "诛仙", OriginalTitle: "Jade Dynasty", Year: 2022}
	season := catalog.Season{SeriesID: m.ID, SeasonNumber: 4, Episodes: []catalog.Episode{{ID: 1, SeasonNumber: 4, EpisodeNumber: 1, AirDate: "2026-10-15"}, {ID: 2, SeasonNumber: 4, EpisodeNumber: 2, AirDate: "2026-10-16"}, {ID: 3, SeasonNumber: 4, EpisodeNumber: 3, AirDate: "2026-10-17"}, {ID: 4, SeasonNumber: 4, EpisodeNumber: 4, AirDate: "2026-10-18"}, {ID: 5, SeasonNumber: 4, EpisodeNumber: 5}}}
	for key, value := range map[string]any{"media:tv:206484": m, "season:206484:4": season} {
		if e = a.Store.Put(context.Background(), "cache", key, map[string]any{"at": time.Now(), "data": value}); e != nil {
			t.Fatal(e)
		}
	}
	return a
}
func subscriptionRequest(t *testing.T) SubscriptionRequest {
	return SubscriptionRequest{MediaType: "tv", TMDBID: 206484, Season: 4, Schedule: fridaySchedule(t), DownloaderID: "test-qb", RootID: "test-tv", State: "active"}
}
func TestSubscriptionPersistenceRevisionAndInflightReservation(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	request := subscriptionRequest(t)
	s, e := a.saveSubscription(ctx, "", request)
	if e != nil {
		t.Fatal(e)
	}
	if s.Revision != 1 || s.NextCheck == "" || s.State != "active" {
		t.Fatalf("saved %+v", s)
	}
	if _, e = a.saveSubscription(ctx, "", request); e == nil {
		t.Fatal("duplicate subscription accepted")
	}
	j, e := a.enqueue(ctx, "subscription", map[string]any{"id": s.ID, "revision": 1}, "wf-test")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.ExecContext(ctx, `INSERT INTO reservations VALUES(?,?,?,?)`, mediaKey(s.Media), 4, 2, "inflight-test"); e != nil {
		t.Fatal(e)
	}
	request.State = "paused"
	request.Revision = 1
	paused, e := a.saveSubscription(ctx, s.ID, request)
	if e != nil {
		t.Fatal(e)
	}
	if paused.Revision != 2 || paused.NextCheck != "" {
		t.Fatalf("paused %+v", paused)
	}
	var state string
	var generation int
	if e = a.Store.DB.QueryRowContext(ctx, `SELECT state,generation FROM jobs WHERE id=?`, j.ID).Scan(&state, &generation); e != nil {
		t.Fatal(e)
	}
	if state != "cancelled" || generation != 1 {
		t.Fatalf("old job state %s generation %d", state, generation)
	}
	if e = a.guardSubscription(ctx, s.ID, 1); e == nil {
		t.Fatal("old revision remained usable")
	}
	if _, e = a.saveSubscription(ctx, s.ID, request); e == nil {
		t.Fatal("stale optimistic revision accepted")
	}
	if e = a.cancelSubscription(ctx, s.ID); e != nil {
		t.Fatal(e)
	}
	var reservations int
	_ = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM reservations WHERE intent_id='inflight-test'`).Scan(&reservations)
	if reservations != 1 {
		t.Fatal("pause/cancel released an inflight reservation")
	}
}
func TestSubscriptionUnknownEpisodeAndPolicyValidation(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	for _, change := range []func(*SubscriptionRequest){func(r *SubscriptionRequest) { r.Episodes = []int{6} }, func(r *SubscriptionRequest) { r.AllowEarly = []int{6} }, func(r *SubscriptionRequest) { r.EpisodeDates = map[int]string{6: "2026-10-16"} }, func(r *SubscriptionRequest) { r.Excluded = []int{1, 1} }, func(r *SubscriptionRequest) { r.Policy.Include = "(" }, func(r *SubscriptionRequest) {
		r.Schedule.Exceptions = []ScheduleException{{Date: "2026-10-16", Action: "skip", Episode: 6}}
	}} {
		request := subscriptionRequest(t)
		change(&request)
		if _, e := a.saveSubscription(ctx, "", request); e == nil {
			t.Fatalf("invalid request accepted %+v", request)
		}
	}
}
func TestSubscriptionCoverageSubtractsLibraryReservationsAndExclusions(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	request := subscriptionRequest(t)
	request.Excluded = []int{3}
	s, e := a.saveSubscription(ctx, "", request)
	if e != nil {
		t.Fatal(e)
	}
	_, e = a.Store.DB.ExecContext(ctx, `INSERT INTO asset_slots VALUES(?,?,?,?)`, mediaKey(s.Media), 4, 1, "asset-test")
	if e != nil {
		t.Fatal(e)
	}
	_, e = a.Store.DB.ExecContext(ctx, `INSERT INTO reservations VALUES(?,?,?,?)`, mediaKey(s.Media), 4, 2, "intent-test")
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Put(ctx, "exclusion", fmt.Sprintf("%s:4:4", mediaKey(s.Media)), map[string]bool{"blocked": true}); e != nil {
		t.Fatal(e)
	}
	coverage, e := a.subscriptionCoverage(ctx, s, []int{1, 2, 3, 4, 5})
	if e != nil {
		t.Fatal(e)
	}
	if len(coverage.Missing) != 1 || coverage.Missing[0] != 5 || len(coverage.Library) != 1 || len(coverage.Reserved) != 1 || len(coverage.Excluded) != 2 {
		t.Fatalf("coverage %+v", coverage)
	}
	result, e := a.subscriptionList(ctx)
	if e != nil {
		t.Fatal(e)
	}
	items := result.(map[string]any)["items"].([]map[string]any)
	if len(items) != 1 || items[0]["revision"] != float64(1) {
		t.Fatalf("list %+v", result)
	}
}
func TestCalendarPredictionsDoNotInventEpisodes(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	request := subscriptionRequest(t)
	request.Episodes = []int{1, 5}
	_, e := a.saveSubscription(ctx, "", request)
	if e != nil {
		t.Fatal(e)
	}
	result, e := a.calendar(ctx, "2026-10-12", "2026-10-18")
	if e != nil {
		t.Fatal(e)
	}
	entries := result.(map[string]any)["entries"].([]CalendarEntry)
	if len(entries) != 2 {
		t.Fatalf("calendar %+v", entries)
	}
	metadata, predictions := 0, 0
	for _, entry := range entries {
		switch entry.Source {
		case "metadata":
			metadata++
			if entry.Episode != 1 || !entry.Confirmed {
				t.Fatalf("wrong known episode %+v", entry)
			}
		case "prediction":
			predictions++
			if entry.Episode != 0 || entry.Confirmed || entry.State != "pending_confirmation" {
				t.Fatalf("invented episode %+v", entry)
			}
		}
	}
	if metadata != 1 || predictions != 1 {
		t.Fatalf("sources %+v", entries)
	}
}
func TestCheckCompletedSubscriptionDoesNotSearchAgain(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	request := subscriptionRequest(t)
	request.Episodes = []int{1}
	s, e := a.saveSubscription(ctx, "", request)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.ExecContext(ctx, `INSERT INTO asset_slots VALUES(?,?,?,?)`, mediaKey(s.Media), 4, 1, "asset-completed"); e != nil {
		t.Fatal(e)
	}
	_, e = a.enqueue(ctx, "subscription", map[string]any{"id": s.ID, "revision": s.Revision}, "wf-check")
	if e != nil {
		t.Fatal(e)
	}
	j, e := a.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.checkSubscription(ctx, j, s.ID, s.Revision); e != nil {
		t.Fatal(e)
	}
	var current Subscription
	if e = a.Store.Get(ctx, "subscription", s.ID, &current); e != nil {
		t.Fatal(e)
	}
	if current.LastReason != "目标已满足，等待原订阅完成判定、历史与通知流程" || current.LastCheck == "" || current.State != "active" {
		t.Fatalf("completed %+v", current)
	}
	// Rechecking a coverage observation must neither search again nor
	// multiply preparation. An orphan slot is not a completed source history.
	if e = a.checkSubscription(ctx, j, s.ID, s.Revision); e != nil {
		t.Fatal(e)
	}
	var preparation, histories, stats int
	a.Store.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind='subscription_completion_prepare' AND json_extract(payload,'$.id')=?`, s.ID).Scan(&preparation)
	a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='subscription_history'`).Scan(&histories)
	a.Store.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind='subscription_stats'`).Scan(&stats)
	if preparation != 1 || histories != 0 || stats != 0 {
		t.Fatal("coverage observation fabricated completion or duplicated preparation", preparation, histories, stats)
	}
	var usage int
	_ = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM site_usage`).Scan(&usage)
	if usage != 0 {
		t.Fatal("complete subscription used site budget")
	}
}
func TestPausedGuardAndContextFailBeforeNetwork(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	request := subscriptionRequest(t)
	s, e := a.saveSubscription(ctx, "", request)
	if e != nil {
		t.Fatal(e)
	}
	guarded := subscriptionContext(ctx, s.ID, s.Revision)
	request.State = "paused"
	request.Revision = s.Revision
	if _, e = a.saveSubscription(ctx, s.ID, request); e != nil {
		t.Fatal(e)
	}
	if e = a.guardSubscriptionContext(guarded); e == nil {
		t.Fatal("context guard missed state/revision change")
	}
	if e = a.checkSubscription(ctx, Job{}, s.ID, s.Revision); e == nil {
		t.Fatal("paused subscription checked")
	}
	var usage int
	_ = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM site_usage`).Scan(&usage)
	if usage != 0 {
		t.Fatal("paused subscription consumed quota")
	}
}
func TestQuotaDeferralPersistsActualNextCheck(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	s, e := a.saveSubscription(ctx, "", subscriptionRequest(t))
	if e != nil {
		t.Fatal(e)
	}
	used := time.Now().Add(-time.Hour).Unix()
	_, e = a.Store.DB.ExecContext(ctx, `INSERT INTO site_usage VALUES(?,?)`, "mteam", used)
	if e != nil {
		t.Fatal(e)
	}
	reset := a.quotaReset(ctx, []string{"mteam"})
	if reset.Unix() != used+24*3600+2 {
		t.Fatalf("rolling quota reset %v", reset)
	}
	if e = a.recordSubscriptionCheck(ctx, s, "预算不足，延期检查", nil, reset); e != nil {
		t.Fatal(e)
	}
	var latest Subscription
	if e = a.Store.Get(ctx, "subscription", s.ID, &latest); e != nil {
		t.Fatal(e)
	}
	next, e := time.Parse(time.RFC3339Nano, latest.NextCheck)
	if e != nil || !next.Equal(reset) {
		t.Fatalf("next_check not actual deferral %+v %v", latest, e)
	}
	b, _ := json.Marshal(latest)
	if string(b) == "" {
		t.Fatal("serialization failed")
	}
}

func TestPauseDoesNotRequireMetadataProvider(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	request := subscriptionRequest(t)
	request.Episodes = []int{1}
	s, e := a.saveSubscription(ctx, "", request)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Delete(ctx, "cache", "media:tv:206484"); e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Delete(ctx, "cache", "season:206484:4"); e != nil {
		t.Fatal(e)
	}
	request.State = "paused"
	request.Revision = s.Revision
	paused, e := a.saveSubscription(ctx, s.ID, request)
	if e != nil {
		t.Fatalf("pause attempted unavailable metadata provider: %v", e)
	}
	if paused.State != "paused" || paused.NextCheck != "" {
		t.Fatalf("pause %+v", paused)
	}
	request.State = ""
	request.Revision = paused.Revision
	again, e := a.saveSubscription(ctx, s.ID, request)
	if e != nil || again.State != "paused" {
		t.Fatalf("editing a paused record implicitly resumed it: %+v %v", again, e)
	}
}

func TestSubscriptionCoverageExplainsUnreleasedUnknownAndPendingImport(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	request := subscriptionRequest(t)
	request.Excluded = []int{7}
	request.EpisodeDates = map[int]string{6: time.Now().In(shanghai()).AddDate(0, 0, 3).Format("2006-01-02")}
	request.AllowEarly = []int{2}
	past := time.Now().In(shanghai()).AddDate(0, 0, -3).Format("2006-01-02")
	future := time.Now().In(shanghai()).AddDate(0, 0, 5).Format("2006-01-02")
	season := catalog.Season{SeriesID: 206484, SeasonNumber: 4}
	for ep := 1; ep <= 8; ep++ {
		date := past
		if ep == 2 || ep == 7 {
			date = future
		}
		if ep == 3 || ep == 6 || ep == 8 {
			date = ""
		}
		season.Episodes = append(season.Episodes, catalog.Episode{EpisodeNumber: ep, SeasonNumber: 4, AirDate: date})
	}
	importCache(t, a, "season:206484:4", season)
	s, err := a.saveSubscription(ctx, "", request)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		ep    int
		state string
	}{{4, "downloaded"}, {5, "downloading"}} {
		key := fmt.Sprintf("intent-%d", item.ep)
		if err = a.Store.Put(ctx, "intent", key, Intent{ID: key, State: item.state, Media: s.Media, Season: 4, Episodes: []int{item.ep}}); err != nil {
			t.Fatal(err)
		}
		if _, err = a.Store.DB.ExecContext(ctx, `INSERT INTO reservations VALUES(?,?,?,?)`, mediaKey(s.Media), 4, item.ep, key); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = a.Store.DB.ExecContext(ctx, `INSERT INTO asset_slots VALUES(?,?,?,?)`, mediaKey(s.Media), 4, 8, "unknown-date-but-owned"); err != nil {
		t.Fatal(err)
	}
	coverage, err := a.subscriptionCoverage(ctx, s, []int{1, 2, 3, 4, 5, 6, 7, 8})
	if err != nil {
		t.Fatal(err)
	}
	assert := func(name string, got, want []int) {
		t.Helper()
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s = %v, want %v; coverage %+v", name, got, want, coverage)
		}
	}
	assert("missing physical", coverage.Missing, []int{1, 2, 3, 6})
	assert("unreleased even AllowEarly", coverage.Unreleased, []int{2, 6})
	assert("unknown date", coverage.UnknownDate, []int{3})
	assert("pending import", coverage.PendingImport, []int{4})
	assert("all reserved", coverage.Reserved, []int{4, 5})
	assert("library overrides date", coverage.Library, []int{8})
	// This read-only view remains useful after credentials/provider caches have
	// expired: it uses cached dates and persistent intent ownership only.
	result, err := a.subscriptionList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	entry := result.(map[string]any)["items"].([]map[string]any)[0]
	for _, key := range []string{"unreleased_episodes", "unknown_airdate_episodes", "pending_import_episodes"} {
		if _, ok := entry[key]; !ok {
			t.Fatalf("missing UI contract field %s", key)
		}
	}
}
