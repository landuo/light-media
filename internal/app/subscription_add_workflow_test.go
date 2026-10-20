package app

import (
	"bytes"
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
)

func uiSourceAddFixture(t *testing.T) (*App, SubscriptionRequest, *noticeSourceFixture) {
	t.Helper()
	a := subscriptionTestApp(t)
	in := subscriptionRequest(t)
	m, e := a.media(context.Background(), in.MediaType, in.TMDBID)
	if e != nil {
		t.Fatal(e)
	}
	in.Media = m
	fixture := installNoticeSourceFixtures(t, a, m, in.Season, 5)
	return a, in, fixture
}
func uiSourceOnlyAdd(t *testing.T, a *App) (Job, subscriptionAddReceipt) {
	t.Helper()
	j, e := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='subscription_add' ORDER BY rowid DESC LIMIT 1`))
	if e != nil {
		t.Fatal(e)
	}
	var ref struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(j.Payload, &ref) != nil {
		t.Fatal("invalid actual UI source job")
	}
	var r subscriptionAddReceipt
	if e = a.readQualitySealed(context.Background(), a.Store.DB, "subscription_add", ref.ID, &r); e != nil {
		t.Fatal(e)
	}
	return j, r
}
func TestUISubscriptionActualSaveDefaultNoticeAddedColdAndDuplicateBeforeAwaitStats(t *testing.T) {
	for _, boundary := range []string{"normal", "business_accept", "route_finish", "queue_finish"} {
		t.Run(boundary, func(t *testing.T) {
			a, in, fixture := uiSourceAddFixture(t)
			ctx := context.Background()
			var lost atomic.Bool
			a.pluginStore.notices.afterCommit = func(op string) error {
				if op == boundary && lost.CompareAndSwap(false, true) {
					return errors.New("owned UI notice SQL Commit ACK lost")
				}
				return nil
			}
			if _, e := a.createUISubscription(ctx, in, "owned-native-admin"); pluginErrorCodeInternal(e) != "SUBSCRIPTION_ASYNC_STATS_DEPENDENCY" {
				t.Fatal("UI did not reach actual original Notice/event before exact awaited dependency", e)
			}
			if boundary != "normal" && !lost.Load() {
				t.Fatal("owned commit boundary not reached")
			}
			job, r := uiSourceOnlyAdd(t, a)
			if r.Stage != "statistics_pending" || r.Saved == nil || r.Existing || fixture.Metadata.Load() != 1 || fixture.Sends.Load() != 1 || noticeV2AddedCount(t, a) != 1 {
				t.Fatal("UI source producer skipped or duplicated real step", r.Stage, fixture.Metadata.Load(), fixture.Sends.Load())
			}
			origin, e := a.sourceSubscriptionOrigin(ctx, *r.Saved)
			if e != nil || origin.SourceAddID != r.Identity.ID || string(origin.Username) != `"owned-native-admin"` {
				t.Fatal("actual API caller lost its creation origin", e)
			}
			route := noticeV2OnlyRoute(t, a)
			rendered, e := a.pluginStore.noticeBusinessRender(ctx, route.ID)
			if e != nil || !strings.Contains(rendered.Snapshot.Fields["title"].(string), "已添加订阅") || route.BusinessV2.Binding.Kind != "subscription_add" || route.BusinessV2.Binding.Added == nil || *route.BusinessV2.Binding.Added != r.Identity {
				t.Fatal("not an actual source-bound UI render", e)
			}
			// Editing is the source Modified path, never another source Added call.
			edit := in
			edit.Revision = r.Saved.Revision
			edit.Keyword = "owned edited query"
			updated, e := a.saveSubscription(ctx, r.Saved.ID, edit)
			if e != nil || noticeV2AddedCount(t, a) != 1 || fixture.Sends.Load() != 1 {
				t.Fatal("edit emitted another Added", e)
			}
			if updated.SourceContextSHA != r.Saved.SourceContextSHA {
				t.Fatal("edit rewrote original creation")
			}
			var stats int
			a.Store.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind='subscription_stats'`).Scan(&stats)
			if stats != 1 {
				t.Fatal("UI did not admit exactly one awaited statistics child")
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
			cold.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) {
				t.Fatal("cold UI operation resent completed notice")
				return 0, errors.New("ungranted")
			}
			job = qualityRunningJob(t, cold, job)
			if e = cold.safeRun(ctx, job); pluginErrorCodeInternal(e) != "SUBSCRIPTION_ASYNC_STATS_DEPENDENCY" || fixture.Sends.Load() != 1 || fixture.Metadata.Load() != 1 {
				t.Fatal("cold saved UI operation repeated source business", e)
			}
			if _, e = cold.Store.DB.Exec(`DELETE FROM records WHERE kind='source_subscription_origin'`); e != nil {
				t.Fatal(e)
			}
			if e = cold.safeRun(ctx, job); e == nil {
				t.Fatal("missing actual creation fact was replaced by UI receipt")
			}

		})
	}
}

func TestUISubscriptionActualAuthenticatedAPIAndExistingAddPreserveCreation(t *testing.T) {
	a, in, fixture := uiSourceAddFixture(t)
	ctx := context.Background()
	if e := a.bootstrapAdmin("owned-original-admin", "owned-password-only-test"); e != nil {
		t.Fatal(e)
	}
	token := "owned-ui-token-not-a-production-secret"
	if _, e := a.Store.DB.Exec(`INSERT INTO sessions(token_hash,expires_at) VALUES(?,?)`, tokenHash(token), time.Now().Add(time.Hour).Unix()); e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	request := func() {
		raw, e := json.Marshal(in)
		if e != nil {
			t.Fatal(e)
		}
		req, e := http.NewRequest(http.MethodPost, server.URL+"/api/subscriptions", bytes.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(sessionCookie(req, token, 3600))
		response, e := server.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		var out struct {
			Error APIError `json:"error"`
		}
		if json.NewDecoder(response.Body).Decode(&out) != nil || out.Error.Code != "SUBSCRIPTION_ASYNC_STATS_DEPENDENCY" {
			t.Fatal("real authenticated API skipped source producer or falsely completed await", response.StatusCode, out.Error.Code)
		}
	}
	request()
	_, first := uiSourceOnlyAdd(t, a)
	if first.Saved == nil || first.Existing || string(first.Input.Username) != `"owned-original-admin"` {
		t.Fatal("API caller did not come from authenticated account")
	}
	creation := first.Saved.SourceContextSHA
	// The same admin account is renamed; its existing session remains that
	// account. A repeated source create has exist_ok=True and its own notice,
	// event and current caller, while preserving the original row/origin.
	if _, e := a.Store.DB.Exec(`UPDATE admin SET username='owned-current-admin' WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	request()
	_, second := uiSourceOnlyAdd(t, a)
	if !second.Existing || second.Identity.ID == first.Identity.ID || second.Saved.ID != first.Saved.ID || second.Saved.Revision != first.Saved.Revision || second.Saved.SourceContextSHA != creation || string(second.Input.Username) != `"owned-current-admin"` || noticeV2AddedCount(t, a) != 2 || fixture.Sends.Load() != 2 {
		t.Fatal("existing source add edited old row or lost per-call event", second.Existing, fixture.Sends.Load())
	}
	origin, e := a.sourceSubscriptionOrigin(ctx, *second.Saved)
	if e != nil || string(origin.Username) != `"owned-original-admin"` {
		t.Fatal("existing source add rewrote original ownership", e)
	}
	var rows int
	a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='subscription'`).Scan(&rows)
	if rows != 1 {
		t.Fatal("duplicate UI create made second subscription", rows)
	}
}
func TestUISubscriptionActualSaveReceiptFailureRollsBackAndRecovers(t *testing.T) {
	a, in, fixture := uiSourceAddFixture(t)
	ctx := context.Background()
	source, e := a.buildUISubscriptionAdd(ctx, in, "owned-admin")
	if e != nil {
		t.Fatal(e)
	}
	identity, job, e := a.admitUISubscriptionAdd(ctx, source)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`CREATE TRIGGER owned_ui_save_receipt_failure BEFORE UPDATE ON records WHEN NEW.kind='subscription_add' BEGIN SELECT RAISE(ABORT,'owned UI save receipt write failure'); END`); e != nil {
		t.Fatal(e)
	}
	if e = a.runUISubscriptionAdd(ctx, job, identity.ID); e == nil {
		t.Fatal("source mutation without receipt committed")
	}
	var rows, origins int
	a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='subscription'`).Scan(&rows)
	a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='source_subscription_origin'`).Scan(&origins)
	if rows != 0 || origins != 0 || noticeV2AddedCount(t, a) != 0 || fixture.Sends.Load() != 0 {
		t.Fatal("save/receipt TX failure was partial", rows, origins)
	}
	if _, e = a.Store.DB.Exec(`DROP TRIGGER owned_ui_save_receipt_failure`); e != nil {
		t.Fatal(e)
	}
	if e = a.runUISubscriptionAdd(ctx, job, identity.ID); pluginErrorCodeInternal(e) != "SUBSCRIPTION_ASYNC_STATS_DEPENDENCY" || fixture.Sends.Load() != 1 || fixture.Metadata.Load() != 1 {
		t.Fatal("same admitted source could not recover without fresh recognize", e)
	}
}

func TestUISubscriptionDisabledStatisticsCompletesWithoutAsyncHTTPOrJSON(t *testing.T) {
	a, in, fixture := uiSourceAddFixture(t)
	ctx := context.Background()
	subscriptionStatsInstallSettings(t, a, "", false)
	saved, e := a.createUISubscription(ctx, in, "owned-admin")
	if e != nil || saved.ID == "" {
		t.Fatal("source disabledFalse prevented normal UI add", e)
	}
	_, r := uiSourceOnlyAdd(t, a)
	if r.Stage != "complete" || r.StatisticsID == "" || fixture.Sends.Load() != 1 || fixture.Metadata.Load() != 1 {
		t.Fatal("UI completed without actual parent/child source facts", r.Stage)
	}
	_, child := subscriptionStatsOnly(t, a)
	if child.Input.Mode != "await_async" || child.Input.Added == nil || *child.Input.Added != r.Identity || child.Stage != "complete" || child.Result.Outcome != "disabled" || !child.Result.SourceReturned || child.Result.SourceValue {
		t.Fatal("disabled UI used sync admission as async result", child.Stage, child.Result)
	}
	var messages int
	if e = a.Store.DB.QueryRow(`SELECT count(*) FROM events WHERE job_id=? AND event='sharing_disabled'`, child.JobID).Scan(&messages); e != nil || messages != 1 {
		t.Fatal("source disabled result missing from task events", messages, e)
	}
}

func TestUISubscriptionAuthenticatedAPIAwaitsActualAsyncStatistics(t *testing.T) {
	for _, mode := range []string{"200", "500", "lost_reply", "nonascii_header", "finish_sql_failure"} {
		t.Run(mode, func(t *testing.T) {
			a, in, fixture := uiSourceAddFixture(t)
			ctx := context.Background()
			if e := a.bootstrapAdmin("owned-ui-admin", "owned-password-for-test"); e != nil {
				t.Fatal(e)
			}
			token := "owned-async-ui-session"
			if _, e := a.Store.DB.Exec(`INSERT INTO sessions(token_hash,expires_at) VALUES(?,?)`, tokenHash(token), time.Now().Add(time.Hour).Unix()); e != nil {
				t.Fatal(e)
			}
			started := make(chan struct{})
			release := make(chan struct{})
			var count atomic.Int32
			var once sync.Once
			var mu sync.Mutex
			var body []byte
			var actualPath, uid string
			statistics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, e := io.ReadAll(r.Body)
				if e != nil {
					t.Error(e)
				}
				count.Add(1)
				mu.Lock()
				body = raw
				actualPath = r.URL.Path
				uid = r.Header.Get("X-MoviePilot-User-Uid")
				mu.Unlock()
				close(started)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				if mode == "lost_reply" {
					conn, _, e := w.(http.Hijacker).Hijack()
					if e != nil {
						t.Error(e)
						return
					}
					conn.Close()
					return
				}
				if mode == "finish_sql_failure" {
					if _, e := a.Store.DB.Exec(`CREATE TRIGGER owned_async_finish_failure BEFORE UPDATE ON records WHEN NEW.kind='subscription_stats' BEGIN SELECT RAISE(ABORT,'owned async receipt write failure'); END`); e != nil {
						t.Error(e)
					}
				}
				if mode == "500" {
					w.WriteHeader(500)
				} else {
					w.WriteHeader(200)
				}
			}))
			defer func() { once.Do(func() { close(release) }); statistics.Close() }()
			subscriptionStatsInstallSettings(t, a, statistics.URL, true)
			if mode == "nonascii_header" {
				var sealed []byte
				a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='source_statistics_settings' AND id='main'`).Scan(&sealed)
				raw, e := a.pluginStore.unseal("source_statistics_settings", "system", "main", sealed)
				if e != nil {
					t.Fatal(e)
				}
				var settings sourceStatisticsSettings
				if cleanupJournalStrict(raw, &settings) != nil {
					t.Fatal("invalid owned settings")
				}
				settings.Snapshot.UserAgent = "owned 电影"
				sealed, e = a.pluginStore.seal("source_statistics_settings", "system", "main", mustCleanSeedJSON(settings))
				if e != nil {
					t.Fatal(e)
				}
				if _, e = a.Store.DB.Exec(`UPDATE records SET payload=? WHERE kind='source_statistics_settings' AND id='main'`, sealed); e != nil {
					t.Fatal(e)
				}
			}
			api := httptest.NewServer(a.Handler())
			defer api.Close()
			type result struct {
				status int
				body   []byte
				e      error
			}
			response := make(chan result, 1)
			go func() {
				raw, e := json.Marshal(in)
				if e != nil {
					response <- result{e: e}
					return
				}
				req, e := http.NewRequest(http.MethodPost, api.URL+"/api/subscriptions", bytes.NewReader(raw))
				if e != nil {
					response <- result{e: e}
					return
				}
				req.Header.Set("Content-Type", "application/json")
				req.AddCookie(sessionCookie(req, token, 3600))
				res, e := api.Client().Do(req)
				if e != nil {
					response <- result{e: e}
					return
				}
				defer res.Body.Close()
				raw, e = io.ReadAll(res.Body)
				response <- result{res.StatusCode, raw, e}
			}()
			if mode != "nonascii_header" {
				select {
				case <-started:
				case out := <-response:
					t.Fatal("API did not issue actual async statistics", out.status, string(out.body), out.e)
				case <-time.After(5 * time.Second):
					t.Fatal("actual async request not reached")
				}
				if noticeV2AddedCount(t, a) != 1 || fixture.Sends.Load() != 1 {
					t.Fatal("statistics overtook actual Notice/Added")
				}
				select {
				case out := <-response:
					t.Fatal("UI returned before original awaited request", out.status, string(out.body))
				case <-time.After(30 * time.Millisecond):
				}
				once.Do(func() { close(release) })
			}
			var out result
			select {
			case out = <-response:
			case <-time.After(8 * time.Second):
				t.Fatal("actual UI await did not finish")
			}
			if out.e != nil {
				t.Fatal(out.e)
			}
			_, source := uiSourceOnlyAdd(t, a)
			childJob, child := subscriptionStatsOnly(t, a)
			if source.StatisticsID != child.ID || child.Input.Mode != "await_async" || child.Input.Added == nil || *child.Input.Added != source.Identity || noticeV2AddedCount(t, a) != 1 || fixture.Metadata.Load() != 1 || fixture.Sends.Load() != 1 {
				t.Fatal("not the same sealed UI/Notice/statistics operation")
			}
			if mode == "nonascii_header" {
				if out.status != 500 || source.Stage != "statistics_pending" || child.Stage != "complete" || child.Result.SourceReturned || child.Result.SourceError != "UnicodeEncodeError" || count.Load() != 0 {
					t.Fatal("source exception became normalFalse/success", out.status, source.Stage, child.Result, count.Load())
				}
			} else if mode == "finish_sql_failure" {
				if out.status == 200 || source.Stage != "statistics_pending" || child.Stage != "intent" || count.Load() != 1 {
					t.Fatal("lost local receipt became source ACK", out.status, source.Stage, child.Stage)
				}
				if _, e := a.Store.DB.Exec(`DROP TRIGGER owned_async_finish_failure`); e != nil {
					t.Fatal(e)
				}
				childJob = qualityRunningJob(t, a, childJob)
				if e := a.safeRun(ctx, childJob); pluginErrorCodeInternal(e) != "SUBSCRIPTION_STATS_UNCONFIRMED" || count.Load() != 1 {
					t.Fatal("unknown async POST repeated", e)
				}
			} else {
				if out.status != 200 || source.Stage != "complete" || child.Stage != "complete" || !child.Result.SourceReturned || count.Load() != 1 {
					t.Fatal("awaited normal source result failed API", out.status, string(out.body), source.Stage, child.Result)
				}
				if mode == "200" && !child.Result.SourceValue || mode != "200" && child.Result.SourceValue {
					t.Fatal("false/unknown remote response masqueraded as successful service ACK", child.Result)
				}
				if mode == "lost_reply" && child.Result.Outcome != "unconfirmed" {
					t.Fatal(child.Result)
				}
			}
			if mode != "nonascii_header" {
				mu.Lock()
				raw, path, gotUID := string(body), actualPath, uid
				mu.Unlock()
				if path != "/subscribe/add" || gotUID != "owned-source-uid" || !strings.Contains(raw, `"name":"诛仙"`) || strings.Contains(raw, `\u8bdb`) {
					t.Fatal("UI used sync encoding/header path", path, gotUID, raw)
				}
			}
			// A completed/failed source result is consumed locally on replay.
			childJob = qualityRunningJob(t, a, childJob)
			_ = a.safeRun(ctx, childJob)
			want := int32(1)
			if mode == "nonascii_header" {
				want = 0
			}
			if count.Load() != want {
				t.Fatal("async replay issued another POST")
			}
		})
	}
}

func TestUISubscriptionCloseWaitsActualAsyncReceiptBeforeDatabaseClosure(t *testing.T) {
	a, in, fixture := uiSourceAddFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	statistics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() { close(release); statistics.Close() }()
	subscriptionStatsInstallSettings(t, a, statistics.URL, true)
	done := make(chan error, 1)
	go func() {
		_, e := a.createUISubscription(context.Background(), in, "owned-native-admin")
		done <- e
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("actual UI async HTTP did not start")
	}
	if noticeV2AddedCount(t, a) != 1 || fixture.Sends.Load() != 1 {
		t.Fatal("statistics preceded source Notice/Added")
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
		t.Fatal("App.Close failed to cancel/join actual async UI producer")
	}
	if e := <-done; !errors.Is(e, context.Canceled) && pluginErrorCodeInternal(e) != "SUBSCRIPTION_STATS_UNCONFIRMED" {
		t.Fatal("cancelled sent async POST became normal sourceFalse/success", e)
	}
	cold, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	parentJob, parent := uiSourceOnlyAdd(t, cold)
	childJob, child := subscriptionStatsOnly(t, cold)
	if parent.Stage != "statistics_pending" || child.Stage != "unknown" || child.Input.Mode != "await_async" || child.Result.SourceValue || child.Input.Added == nil || *child.Input.Added != parent.Identity {
		t.Fatal("DB closed before authenticated unknown async receipt", parent.Stage, child.Stage)
	}
	childJob = qualityRunningJob(t, cold, childJob)
	if e = cold.safeRun(context.Background(), childJob); pluginErrorCodeInternal(e) != "SUBSCRIPTION_STATS_UNCONFIRMED" {
		t.Fatal("cold async child accepted unknown result", e)
	}
	parentJob = qualityRunningJob(t, cold, parentJob)
	if e = cold.safeRun(context.Background(), parentJob); pluginErrorCodeInternal(e) != "SUBSCRIPTION_STATS_UNCONFIRMED" || calls.Load() != 1 || fixture.Sends.Load() != 1 {
		t.Fatal("cold async operation repeated HTTP or source Notice", e, calls.Load())
	}
	if _, _, e = a.beginSubscriptionStatistics(context.Background()); e == nil {
		t.Fatal("closed App admitted another statistics operation")
	}
}
