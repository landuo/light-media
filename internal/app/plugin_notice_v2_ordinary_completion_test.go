package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func actualOrdinaryNoticeStats(t *testing.T, a *App, s Subscription, wantAccepted int) {
	t.Helper()
	ctx := context.Background()
	fixture := installNoticeSourceFixtures(t, a, s.Media, s.Season, s.TotalEpisodes)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, e := io.ReadAll(r.Body)
		if e != nil {
			t.Error(e)
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || len(fields) != 7 || r.URL.Path != "/subscribe/done" {
			t.Error("ordinary worker did not use original payload", string(raw), r.URL.Path)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	subscriptionStatsInstallSettings(t, a, server.URL, true)
	prepare, e := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE kind='subscription_completion_prepare' AND json_extract(payload,'$.id')=? AND state='queued' ORDER BY rowid DESC LIMIT 1`, s.ID))
	if e != nil {
		t.Fatal("ordinary accepted/inventory did not queue real prepare", e)
	}
	prepare = qualityRunningJob(t, a, prepare)
	if e = a.safeRun(ctx, prepare); e != nil {
		t.Fatal("actual ordinary fresh source/history/delete failed", e)
	}
	complete, e := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='subscription_completion' ORDER BY rowid DESC LIMIT 1`))
	if e != nil {
		t.Fatal(e)
	}
	complete = qualityRunningJob(t, a, complete)
	if e = a.safeRun(ctx, complete); e != nil {
		t.Fatal("ordinary native Notice/events/stats admission failed", e)
	}
	stats, r := subscriptionStatsOnly(t, a)
	var receipt subscriptionCompletionReceipt
	if e = a.readQualitySealed(ctx, a.Store.DB, "subscription_completion", r.Input.Completion.ID, &receipt); e != nil || receipt.Stage != "complete" || receipt.Input.Version != 2 || receipt.Input.Ordinary == nil || len(receipt.Input.Accepted) != wantAccepted {
		t.Fatal("ordinary input used best-version or fake accepted facts", receipt.Stage, len(receipt.Input.Accepted), e)
	}
	route := noticeV2OnlyRoute(t, a)
	rendered, e := a.pluginStore.noticeBusinessRender(ctx, route.ID)
	if e != nil || !strings.Contains(rendered.Snapshot.Fields["title"].(string), "已完成订阅") || fixture.Metadata.Load() != 1 || fixture.Sends.Load() != 1 || calls.Load() != 0 {
		t.Fatal("ordinary source template/fresh lookup/async separation failed", e, fixture.Metadata.Load(), fixture.Sends.Load(), calls.Load())
	}
	var history, events, subscriptions int
	a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='subscription_history'`).Scan(&history)
	a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='source_subscribe_complete'`).Scan(&events)
	a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='subscription'`).Scan(&subscriptions)
	if history != 1 || events != 1 || subscriptions != 0 {
		t.Fatal("ordinary original finish order was incomplete", history, events, subscriptions)
	}
	stats = qualityRunningJob(t, a, stats)
	if e = a.safeRun(ctx, stats); e != nil || calls.Load() != 1 {
		t.Fatal("ordinary actual statistics child failed", e, calls.Load())
	}
	if e = a.safeRun(ctx, stats); e != nil || calls.Load() != 1 {
		t.Fatal("ordinary child repeated POST", e)
	}
	dir, settings := a.Dir, a.Settings()
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	cold, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	cold.config = settings
	stats = qualityRunningJob(t, cold, stats)
	if e = cold.safeRun(ctx, stats); e != nil || calls.Load() != 1 {
		t.Fatal("cold ordinary completion repeated HTTP", e, calls.Load())
	}
	if fixture.Sends.Load() != 1 || fixture.Metadata.Load() != 1 {
		t.Fatal("cold ordinary completion reconstructed notification")
	}
}
func TestPluginNoticeV2ActualOrdinaryMovieAndSeasonCompletion(t *testing.T) {
	for _, kind := range []string{"movie", "tv"} {
		t.Run(kind, func(t *testing.T) {
			ordinaryMovieDownloadWorkflow(t, kind, func(a *App, s Subscription) { actualOrdinaryNoticeStats(t, a, s, 1) })
		})
	}
}
func TestPluginNoticeV2ActualOrdinaryLibraryInventoryCompletion(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	m := importFixture(t, a, "movie")
	input := filepath.Join(source, "测试影片 (2024).mkv")
	importWrite(t, input, []byte("owned ordinary inventory without subscription Add"))
	plan, e := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: m.ID, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: input}}})
	if e != nil {
		t.Fatal(e)
	}
	job, e := a.executeImport(ctx, plan.ID, nil)
	if e != nil {
		t.Fatal(e)
	}
	reorgRunJob(t, a, job)
	workflowConfigure(t, a, "http://127.0.0.1:1", "http://127.0.0.1:1")
	s, e := a.saveSubscription(ctx, "", SubscriptionRequest{MediaType: "movie", TMDBID: m.ID, DownloaderID: "fixture-qb", RootID: "root", Schedule: Schedule{Mode: "manual"}})
	if e != nil {
		t.Fatal(e)
	}
	check := importClaim(t, a, "subscription", s.ID)
	if e = a.checkSubscription(ctx, check, s.ID, s.Revision); e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Get(ctx, "subscription", s.ID, &s); e != nil {
		t.Fatal(e)
	}
	actualOrdinaryNoticeStats(t, a, s, 0)
}
