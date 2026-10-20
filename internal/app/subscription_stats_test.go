package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/subscriptionstats"
)

func subscriptionStatsOnly(t *testing.T, a *App) (Job, subscriptionStatsReceipt) {
	t.Helper()
	j, e := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='subscription_stats' ORDER BY rowid LIMIT 1`))
	if e != nil {
		t.Fatal(e)
	}
	var ref struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(j.Payload, &ref) != nil {
		t.Fatal("invalid stats job")
	}
	var r subscriptionStatsReceipt
	if e = a.readQualitySealed(context.Background(), a.Store.DB, "subscription_stats", ref.ID, &r); e != nil {
		t.Fatal(e)
	}
	var n int
	a.Store.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind='subscription_stats'`).Scan(&n)
	if n != 1 {
		t.Fatal("duplicated stats job", n)
	}
	return j, r
}
func subscriptionStatsInstallSettings(t *testing.T, a *App, host string, share bool) {
	t.Helper()
	uid := "owned-source-uid"
	v := sourceStatisticsSettings{Version: 1, Snapshot: subscriptionstats.SettingsSnapshot{Share: &share, Known: true, ServerHost: host, UserUID: &uid, UserAgent: "owned-source-UA"}}
	sealed, e := a.pluginStore.seal("source_statistics_settings", "system", "main", mustCleanSeedJSON(v))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`INSERT INTO records(kind,id,payload,updated_at) VALUES('source_statistics_settings','main',?,?)`, sealed, now()); e != nil {
		t.Fatal(e)
	}
}
func TestSubscriptionStatsActualLarkAddHTTPAsyncColdAndLostACK(t *testing.T) {
	for _, mode := range []string{"200", "500", "lost_http_reply", "disabled", "missing_settings", "invalid_source_settings", "intent_ack_lost", "finish_ack_lost", "cold_accepted", "cold_intent"} {
		t.Run(mode, func(t *testing.T) {
			a, core, ctx, request, saved, fixture := noticeV2SubscriptionFixture(t)
			var calls atomic.Int32
			var mu sync.Mutex
			var body []byte
			var path, uid, agent string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				raw, e := io.ReadAll(r.Body)
				if e != nil {
					t.Error(e)
				}
				mu.Lock()
				body = raw
				path = r.URL.Path
				uid = r.Header.Get("X-MoviePilot-User-Uid")
				agent = r.Header.Get("User-Agent")
				mu.Unlock()
				if mode == "lost_http_reply" {
					c, _, e := w.(http.Hijacker).Hijack()
					if e != nil {
						t.Error(e)
						return
					}
					c.Close()
					return
				}
				if mode == "500" {
					w.WriteHeader(500)
					return
				}
				w.WriteHeader(200)
			}))
			defer server.Close()
			if mode != "missing_settings" {
				subscriptionStatsInstallSettings(t, a, server.URL, mode != "disabled")
			}
			if mode == "invalid_source_settings" {
				badSettings, e := a.pluginStore.seal("source_statistics_settings", "system", "main", json.RawMessage(`{"Version":99,"Snapshot":{}}`))
				if e != nil {
					t.Fatal(e)
				}
				if _, e = a.Store.DB.Exec(`UPDATE records SET payload=? WHERE kind='source_statistics_settings' AND id='main'`, badSettings); e != nil {
					t.Fatal(e)
				}
			}
			ordinal := core.ordinal
			if e := a.larkCompleteSourceSubscription(ctx, core, request, saved); e != nil {
				t.Fatal("async admission blocked successful source add", e)
			}
			if calls.Load() != 0 || fixture.Sends.Load() != 1 || noticeV2AddedCount(t, a) != 1 {
				t.Fatal("child HTTP ran in parent", calls.Load())
			}
			j, r := subscriptionStatsOnly(t, a)
			if r.Stage != "accepted" {
				t.Fatal(r.Stage)
			}
			core.ordinal = ordinal
			if e := a.larkCompleteSourceSubscription(ctx, core, request, saved); e != nil {
				t.Fatal("actual parent readback failed", e)
			}
			subscriptionStatsOnly(t, a)
			j = qualityRunningJob(t, a, j)
			if mode == "cold_intent" {
				a.subscriptionStats.afterCommit = func(op string) error {
					if op == "intent" {
						panic("owned process exit after real intent commit")
					}
					return nil
				}
				if e := a.safeRun(context.Background(), j); pluginErrorCodeInternal(e) != "INTERNAL_TASK_ERROR" {
					t.Fatal("did not cross durable intent", e)
				}
				if calls.Load() != 0 {
					t.Fatal("crash fixture sent HTTP")
				}
			}
			if strings.HasPrefix(mode, "cold_") {
				dir, settings := a.Dir, a.Settings()
				core.release()
				if e := a.Close(); e != nil {
					t.Fatal(e)
				}
				var e error
				a, e = New(Options{DataDir: dir})
				if e != nil {
					t.Fatal(e)
				}
				defer a.Close()
				a.config = settings
				j = qualityRunningJob(t, a, j)
			}
			var lost atomic.Bool
			if mode == "intent_ack_lost" || mode == "finish_ack_lost" {
				want := strings.TrimSuffix(mode, "_ack_lost")
				a.subscriptionStats.afterCommit = func(op string) error {
					if op == want && lost.CompareAndSwap(false, true) {
						return errors.New("owned SQL Commit ACK lost")
					}
					return nil
				}
			}
			e := a.safeRun(context.Background(), j)
			wantCode := ""
			if mode == "missing_settings" || mode == "invalid_source_settings" {
				wantCode = "SUBSCRIPTION_STATS_DEPENDENCY"
			}
			if mode == "cold_intent" {
				wantCode = "SUBSCRIPTION_STATS_UNCONFIRMED"
			}
			if wantCode == "" && e != nil || wantCode != "" && pluginErrorCodeInternal(e) != wantCode {
				t.Fatal("wrong source child outcome", mode, e)
			}
			if (mode == "intent_ack_lost" || mode == "finish_ack_lost") && !lost.Load() {
				t.Fatal("lost ACK hook not reached")
			}
			if e = a.readQualitySealed(context.Background(), a.Store.DB, "subscription_stats", r.ID, &r); e != nil {
				t.Fatal(e)
			}
			wantedCalls := int32(1)
			if mode == "disabled" || mode == "missing_settings" || mode == "invalid_source_settings" || mode == "cold_intent" {
				wantedCalls = 0
			}
			if calls.Load() != wantedCalls {
				t.Fatal("unexpected real HTTP count", calls.Load(), wantedCalls)
			}
			switch mode {
			case "500":
				if r.Result.HTTPStatus != 500 || !r.Result.SourceReturned || r.Result.SourceValue {
					t.Fatal(r.Result)
				}
			case "lost_http_reply":
				if r.Result.Outcome != "unconfirmed" || !r.Result.SourceReturned || r.Result.SourceValue {
					t.Fatal(r.Result)
				}
			case "disabled":
				if r.Result.Outcome != "disabled" || !r.Result.SourceReturned || r.Result.SourceValue {
					t.Fatal(r.Result)
				}
			case "missing_settings", "invalid_source_settings":
				if r.Stage != "dependency" || r.Input.SettingsMissing == "" {
					t.Fatal("unknown settings lost", r.Stage)
				}
			case "cold_intent":
				if r.Stage != "unknown" || r.Result.SourceValue {
					t.Fatal(r.Stage)
				}
			default:
				if !r.Result.SourceReturned || !r.Result.SourceValue || r.Result.HTTPStatus != 200 {
					t.Fatal(r.Result)
				}
			}
			if wantedCalls == 1 {
				mu.Lock()
				var fields map[string]any
				decoder := json.NewDecoder(strings.NewReader(string(body)))
				decoder.UseNumber()
				decodeErr := decoder.Decode(&fields)
				gotPath, gotUID, gotAgent := path, uid, agent
				mu.Unlock()
				if decodeErr != nil || len(fields) != 16 || gotPath != "/subscribe/add" || gotUID != "owned-source-uid" || gotAgent != "owned-source-UA" || fields["season"] != json.Number("4") || fields["name"] != "诛仙" || fields["media_source"] != "themoviedb" || fields["media_id"] != "206484" {
					t.Fatal("actual source add locals/headers were not sent", fields, gotPath, decodeErr)
				}
			}
			_ = a.safeRun(context.Background(), j)
			if calls.Load() != wantedCalls || noticeV2AddedCount(t, a) != 1 {
				t.Fatal("child retry changed parent or duplicated HTTP")
			}
			if _, e = a.Store.DB.Exec(`DELETE FROM records WHERE kind='subscription_added'`); e != nil {
				t.Fatal(e)
			}
			if e = a.safeRun(context.Background(), j); e == nil {
				t.Fatal("missing source event accepted because stats receipt exists")
			}
		})
	}
}

func TestSubscriptionStatsActualCompletionAcceptedFactsNoticeAndHTTP(t *testing.T) {
	for _, mode := range []string{"200", "500", "disabled", "cold_accepted", "finish_ack_lost"} {
		t.Run(mode, func(t *testing.T) {
			qualityActualMovieWorkflow(t, "normal", func(a *App, s Subscription) {
				ctx := context.Background()
				fixture := installNoticeSourceFixtures(t, a, s.Media, 0, 0)
				var calls atomic.Int32
				var mu sync.Mutex
				var body []byte
				var path string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					raw, e := io.ReadAll(r.Body)
					if e != nil {
						t.Error(e)
					}
					mu.Lock()
					body = raw
					path = r.URL.Path
					mu.Unlock()
					if mode == "500" {
						w.WriteHeader(500)
					} else {
						w.WriteHeader(200)
					}
				}))
				defer server.Close()
				subscriptionStatsInstallSettings(t, a, server.URL, mode != "disabled")
				prepare, e := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE kind='subscription_completion_prepare' AND json_extract(payload,'$.id')=? ORDER BY rowid LIMIT 1`, s.ID))
				if e != nil {
					t.Fatal(e)
				}
				prepare = qualityRunningJob(t, a, prepare)
				if e = a.safeRun(ctx, prepare); e != nil {
					t.Fatal("real fresh source/check/history/delete failed", e)
				}
				complete, e := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='subscription_completion' ORDER BY rowid LIMIT 1`))
				if e != nil {
					t.Fatal(e)
				}
				complete = qualityRunningJob(t, a, complete)
				if e = a.safeRun(ctx, complete); e != nil {
					t.Fatal("real Notice/event/stats admission failed", e)
				}
				stats, r := subscriptionStatsOnly(t, a)
				if calls.Load() != 0 || fixture.Sends.Load() != 1 {
					t.Fatal("parent did child HTTP", calls.Load())
				}
				var receipt subscriptionCompletionReceipt
				if e = a.readQualitySealed(ctx, a.Store.DB, "subscription_completion", r.Input.Completion.ID, &receipt); e != nil || receipt.Stage != "complete" {
					t.Fatal("parent was not complete before worker", e)
				}
				if mode == "cold_accepted" {
					dir, settings := a.Dir, a.Settings()
					if e = a.Close(); e != nil {
						t.Fatal(e)
					}
					a, e = New(Options{DataDir: dir})
					if e != nil {
						t.Fatal(e)
					}
					defer a.Close()
					a.config = settings
				}
				var lost atomic.Bool
				if mode == "finish_ack_lost" {
					a.subscriptionStats.afterCommit = func(op string) error {
						if op == "finish" && lost.CompareAndSwap(false, true) {
							return errors.New("owned done receipt ACK lost")
						}
						return nil
					}
				}
				stats = qualityRunningJob(t, a, stats)
				if e = a.safeRun(ctx, stats); e != nil {
					t.Fatal("real completion child failed", e)
				}
				if e = a.readQualitySealed(ctx, a.Store.DB, "subscription_stats", r.ID, &r); e != nil {
					t.Fatal(e)
				}
				want := int32(1)
				if mode == "disabled" {
					want = 0
				}
				if calls.Load() != want || r.Stage != "complete" || !r.Result.SourceReturned || (mode == "500" || mode == "disabled") && r.Result.SourceValue {
					t.Fatal("wrong real completion result", calls.Load(), r.Stage, r.Result)
				}
				if want == 1 {
					mu.Lock()
					var fields map[string]any
					d := json.NewDecoder(strings.NewReader(string(body)))
					d.UseNumber()
					e = d.Decode(&fields)
					got := path
					mu.Unlock()
					if e != nil || len(fields) != 7 || got != "/subscribe/done" || fields["tmdbid"] == nil || fields["media_source"] != "themoviedb" {
						t.Fatal("did not send original seven fields", fields, got, e)
					}
				}
				if mode == "finish_ack_lost" && !lost.Load() {
					t.Fatal("final commit hook not crossed")
				}
				if e = a.safeRun(ctx, stats); e != nil || calls.Load() != want {
					t.Fatal("completion child repeated POST", e, calls.Load())
				}
				// Already consumed source history remains an authenticated dependency.
				if _, e = a.Store.DB.Exec(`DELETE FROM records WHERE kind='subscription_history'`); e != nil {
					t.Fatal(e)
				}
				if e = a.safeRun(ctx, stats); e == nil {
					t.Fatal("orphaned done receipt granted source authority")
				}
			})
		})
	}
}

func TestSubscriptionStatsAdmissionFailureRollsBackSourceAdded(t *testing.T) {
	a, core, ctx, request, saved, _ := noticeV2SubscriptionFixture(t)
	if _, e := a.Store.DB.Exec(`CREATE TRIGGER owned_stats_admission_failure BEFORE INSERT ON jobs WHEN NEW.kind='subscription_stats' BEGIN SELECT RAISE(ABORT,'owned admission failure'); END`); e != nil {
		t.Fatal(e)
	}
	if e := a.larkCompleteSourceSubscription(ctx, core, request, saved); e == nil {
		t.Fatal("thread admission failure falsely returned successful parent")
	}
	var stats int
	a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='subscription_stats'`).Scan(&stats)
	if stats != 0 || noticeV2AddedCount(t, a) != 0 {
		t.Fatal("failed child admission committed a partial source added", stats)
	}
}

func TestSubscriptionStatsActualHTTPReceiptWriteFailureBecomesUnknownWithoutRetry(t *testing.T) {
	a, core, ctx, request, saved, _ := noticeV2SubscriptionFixture(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if _, e := a.Store.DB.Exec(`CREATE TRIGGER owned_stats_finish_failure BEFORE UPDATE ON records WHEN NEW.kind='subscription_stats' BEGIN SELECT RAISE(ABORT,'owned final stats write failure'); END`); e != nil {
			t.Error(e)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	subscriptionStatsInstallSettings(t, a, server.URL, true)
	if e := a.larkCompleteSourceSubscription(ctx, core, request, saved); e != nil {
		t.Fatal(e)
	}
	j, r := subscriptionStatsOnly(t, a)
	j = qualityRunningJob(t, a, j)
	if e := a.safeRun(context.Background(), j); e == nil || calls.Load() != 1 {
		t.Fatal("final write failure was not real", e, calls.Load())
	}
	if e := a.readQualitySealed(ctx, a.Store.DB, "subscription_stats", r.ID, &r); e != nil || r.Stage != "intent" {
		t.Fatal("lost local result invented network ACK", r.Stage, e)
	}
	if _, e := a.Store.DB.Exec(`DROP TRIGGER owned_stats_finish_failure`); e != nil {
		t.Fatal(e)
	}
	if e := a.safeRun(context.Background(), j); pluginErrorCodeInternal(e) != "SUBSCRIPTION_STATS_UNCONFIRMED" || calls.Load() != 1 {
		t.Fatal("unknown original POST repeated", e, calls.Load())
	}
	if noticeV2AddedCount(t, a) != 1 {
		t.Fatal("child persistence failure rolled back parent")
	}
}
func TestSubscriptionStatsSealedProxyEnvironmentAndJobGeneration(t *testing.T) {
	a, core, ctx, request, saved, _ := noticeV2SubscriptionFixture(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	subscriptionStatsInstallSettings(t, a, server.URL, true)
	var sealed []byte
	if e := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='source_statistics_settings' AND id='main'`).Scan(&sealed); e != nil {
		t.Fatal(e)
	}
	raw, e := a.pluginStore.unseal("source_statistics_settings", "system", "main", sealed)
	if e != nil {
		t.Fatal(e)
	}
	var source sourceStatisticsSettings
	if cleanupJournalStrict(raw, &source) != nil {
		t.Fatal("invalid settings fixture")
	}
	empty := []string{}
	source.Environment = &empty
	sealed, e = a.pluginStore.seal("source_statistics_settings", "system", "main", mustCleanSeedJSON(source))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`UPDATE records SET payload=? WHERE kind='source_statistics_settings' AND id='main'`, sealed); e != nil {
		t.Fatal(e)
	}
	if e = a.larkCompleteSourceSubscription(ctx, core, request, saved); e != nil {
		t.Fatal(e)
	}
	j, r := subscriptionStatsOnly(t, a)
	if r.Input.Environment == nil || len(r.Input.Environment) != 0 {
		t.Fatal("known empty environment became missing")
	}
	// A changed live environment cannot reinterpret this already admitted call.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	t.Setenv("https_proxy", "http://127.0.0.1:1")
	t.Setenv("all_proxy", "http://127.0.0.1:1")
	t.Setenv("no_proxy", "")
	j = qualityRunningJob(t, a, j)
	stale := j
	j = qualityRunningJob(t, a, j)
	if e = a.safeRun(context.Background(), stale); e == nil || calls.Load() != 0 {
		t.Fatal("stale worker acquired HTTP authority", e)
	}
	if e = a.safeRun(context.Background(), j); e != nil || calls.Load() != 1 {
		t.Fatal("worker re-read proxy env after enqueue", e, calls.Load())
	}
}

func TestSubscriptionStatsCloseCancelsOwnedHTTPAndSealsUnknownBeforeDatabaseClosure(t *testing.T) {
	a, core, ctx, request, saved, _ := noticeV2SubscriptionFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() { close(release); server.Close() }()
	subscriptionStatsInstallSettings(t, a, server.URL, true)
	if e := a.larkCompleteSourceSubscription(ctx, core, request, saved); e != nil {
		t.Fatal(e)
	}
	job, receipt := subscriptionStatsOnly(t, a)
	job = qualityRunningJob(t, a, job)
	done := make(chan error, 1)
	go func() { done <- a.safeRun(context.Background(), job) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("owned HTTP did not start")
	}
	dir := a.Dir
	closed := make(chan error, 1)
	go func() { closed <- a.Close() }()
	select {
	case e := <-closed:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("App.Close did not cancel/wait source statistics before DB close")
	}
	if e := <-done; pluginErrorCodeInternal(e) != "SUBSCRIPTION_STATS_UNCONFIRMED" {
		t.Fatal("cancelled sent POST was turned into sourceFalse or success", e)
	}
	cold, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	if e = cold.readQualitySealed(context.Background(), cold.Store.DB, "subscription_stats", receipt.ID, &receipt); e != nil || receipt.Stage != "unknown" || receipt.Result.SourceValue {
		t.Fatal("DB closed before immutable unknown receipt", receipt.Stage, e)
	}
	job = qualityRunningJob(t, cold, job)
	if e = cold.safeRun(context.Background(), job); pluginErrorCodeInternal(e) != "SUBSCRIPTION_STATS_UNCONFIRMED" || calls.Load() != 1 {
		t.Fatal("cold cancelled POST repeated", e, calls.Load())
	}
	if _, _, e = a.beginSubscriptionStatistics(context.Background()); e == nil {
		t.Fatal("closed App admitted another statistics operation")
	}
}
