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

	"light-media/internal/notice"
	seed "light-media/internal/plugins/cleaninvalidseed"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

func noticePreferenceTest(t *testing.T, a *App, kind, key string, value any) {
	t.Helper()
	tx, err := a.Store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = a.pluginStore.putNoticePreferenceTx(context.Background(), tx, kind, key, value); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func noticeRouteTestInit(t *testing.T, a *App) PluginRun {
	t.Helper()
	useBuiltInPluginProcess(t, a)
	run := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cleanSeedOwnerConfig(nil), true)
	if got := cleanSeedRuntimeExecute(t, a, run); got.State != "succeeded" {
		t.Fatal(got.State, got.ErrorCode)
	}
	return run
}
func noticeRouteTestToggle(t *testing.T, a *App, instance, user string) PluginRun {
	t.Helper()
	if _, err := a.pluginStore.enqueueCleanInvalidSeedCommand(context.Background(), instance, map[string]any{"action": "toggle_notify_all", "channel": "Web", "user": user}); err != nil {
		t.Fatal(err)
	}
	return cleanSeedRuntimeExecute(t, a, cleanSeedOwnerClaim(t, a, instance))
}
func noticeRouteTestIDs(t *testing.T, a *App, table string) []string {
	t.Helper()
	if table != "plugin_notice_routes" && table != "plugin_message_queue" {
		t.Fatal("fixture table")
	}
	rows, err := a.Store.DB.Query(`SELECT id FROM ` + table + ` ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestPluginNoticeRouteFailurePreservesHistoryAndPublicationBeforeSourceConfig(t *testing.T) {
	for _, stage := range []string{"publish", "queue"} {
		t.Run(stage, func(t *testing.T) {
			a := newTestApp(t)
			initial := noticeRouteTestInit(t, a)
			switch stage {
			case "publish":
				if _, err := a.Store.DB.Exec(`CREATE TRIGGER notice_fail BEFORE INSERT ON plugin_notice_publications BEGIN SELECT RAISE(ABORT,'owned publication failure'); END`); err != nil {
					t.Fatal(err)
				}
			case "queue":
				if _, err := a.Store.DB.Exec(`CREATE TRIGGER notice_fail BEFORE INSERT ON plugin_message_queue BEGIN SELECT RAISE(ABORT,'owned queue failure'); END`); err != nil {
					t.Fatal(err)
				}

			}
			got := noticeRouteTestToggle(t, a, initial.InstanceID, "owned")
			if got.State != "failed" || got.ErrorCode != "CLEAN_SEED_SOURCE_FAILED" {
				t.Fatal("downstream local failure not returned to source", got.State, got.ErrorCode)
			}
			config, err := a.pluginStore.Config(context.Background(), initial.InstanceID)
			if err != nil || config.Revision != 1 || strings.Contains(string(config.Value), `"notify_all":true`) {
				t.Fatal("source config passed failed first notice", config.Revision, err)
			}
			var histories, pubs, queue int
			if err = a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='notification'`).Scan(&histories); err != nil {
				t.Fatal(err)
			}
			if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_notice_publications`).Scan(&pubs); err != nil {
				t.Fatal(err)
			}
			if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_message_queue`).Scan(&queue); err != nil {
				t.Fatal(err)
			}
			wantPubs := 1
			if stage == "publish" {
				wantPubs = 0
			}
			if histories != 1 || pubs != wantPubs || queue != 0 {
				t.Fatal("downstream error erased accepted source prefix", histories, pubs, queue)
			}
			ids := noticeRouteTestIDs(t, a, "plugin_notice_routes")
			if len(ids) != 1 {
				t.Fatal(len(ids))
			}
			r, _, err := a.pluginStore.readNoticeRoute(context.Background(), a.Store.DB, ids[0])
			wantError := "RuntimeError"
			if err != nil || !r.Complete || r.SourceError != wantError {
				t.Fatal("exact failed source completion missing", r.Complete, r.SourceError, err)
			}
			if err = a.runNoticeRoute(context.Background(), r.ID); err == nil {
				t.Fatal("failed completion became success")
			}
			if len(noticeRouteTestIDs(t, a, "plugin_message_queue")) != 0 {
				t.Fatal("known failure was replayed")
			}
		})
	}
}

func TestPluginNoticeRouteDirectWaitsOutsideSQLAndConsumesSenderUnknown(t *testing.T) {
	a := newTestApp(t)
	initial := noticeRouteTestInit(t, a)
	var count atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer s.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	a.pluginStore.notices.send = func(ctx context.Context, q pluginNoticeQueueEntry) (int, error) {
		n := count.Load()
		readCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		var revision int64
		if err := a.Store.DB.QueryRowContext(readCtx, `SELECT revision FROM plugin_values WHERE instance_id=? AND kind='config'`, initial.InstanceID).Scan(&revision); err != nil {
			t.Error("sender called with SQL transaction retained", err)
			return 0, err
		}
		if n == 0 && revision != 1 || n == 1 && revision != 2 {
			t.Error("source crossed wrong notice/config boundary", n, revision)
		}
		if !a.pluginLifecycleMu.TryLock() {
			t.Error("lifecycle held during sender")
		} else {
			a.pluginLifecycleMu.Unlock()
		}
		if n == 0 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, strings.NewReader("owned"))
		if err != nil {
			return 0, err
		}
		resp, err := s.Client().Do(req)
		if err != nil {
			return 0, err
		}
		resp.Body.Close()
		if n == 1 {
			return 1, seedhost.ErrUnknown
		}
		return 1, nil
	}
	result := make(chan PluginRun, 1)
	go func() { result <- noticeRouteTestToggle(t, a, initial.InstanceID, "owned-user") }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("actual compiled source did not reach sender")
	}
	var revision int64
	if err := a.Store.DB.QueryRow(`SELECT revision FROM plugin_values WHERE instance_id=? AND kind='config'`, initial.InstanceID).Scan(&revision); err != nil || revision != 1 {
		t.Fatal("source did not wait for direct attempt", revision, err)
	}
	select {
	case <-result:
		t.Fatal("source returned before sender")
	default:
	}
	close(release)
	var got PluginRun
	select {
	case got = <-result:
	case <-time.After(5 * time.Second):
		t.Fatal("source did not resume")
	}
	if got.State != "succeeded" || count.Load() != 2 {
		t.Fatal("original swallowed sender failure changed source result", got.State, got.ErrorCode, count.Load())
	}
	ids := noticeRouteTestIDs(t, a, "plugin_message_queue")
	if len(ids) != 2 {
		t.Fatal(len(ids))
	}
	for i, id := range ids {
		entry, err := a.pluginStore.readPluginMessage(context.Background(), a.Store.DB, id)
		want := "consumed"
		if i == 1 {
			want = "unknown"
		}
		if err != nil || entry.State != want || entry.Modules != 1 {
			t.Fatal("transport/source outcomes conflated", entry.State, entry.Modules, err)
		}
	}
	if err := a.resumePluginNoticeRoutes(context.Background(), 200); err != nil {
		t.Fatal(err)
	}
	if err := a.drainPluginMessages(context.Background(), 200); err != nil || count.Load() != 2 {
		t.Fatal("consumed/unknown sender was retried", count.Load(), err)
	}
}

func TestPluginNoticeRouteWindowDirectBypassAndColdUnknownFIFOContinues(t *testing.T) {
	data := t.TempDir()
	a, err := New(Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if a != nil {
			_ = a.Close()
		}
	})
	d := cleanSeedOwnerDaemonNew(t)
	d.settings(a)
	sealed, err := a.Store.Seal(a.Settings())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`INSERT INTO records VALUES('config','main',?,?)`, sealed, now()); err != nil {
		t.Fatal(err)
	}
	useBuiltInPluginProcess(t, a)
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 18, 2, 0, 0, 0, time.UTC).UnixNano())
	nowFn := func() time.Time { return time.Unix(0, clock.Load()).In(time.FixedZone("owned-local", 8*3600)) }
	a.pluginStore.notices.now = nowFn
	a.pluginCleanInvalidSeed.now = nowFn
	noticePreferenceTest(t, a, "window", "", []any{map[string]any{"start": "12:00", "end": "12:00"}})
	if err := a.pluginStore.reloadNoticePeriods(context.Background()); err != nil {
		t.Fatal(err)
	}
	initial := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cleanSeedOwnerConfig(map[string]any{"notify": true}), true)
	if got := cleanSeedRuntimeExecute(t, a, initial); got.State != "succeeded" {
		t.Fatal(got.State, got.ErrorCode)
	}
	for i := 0; i < 2; i++ {
		if _, err = a.pluginStore.EnqueueRun(context.Background(), initial.InstanceID, "run", nil); err != nil {
			t.Fatal(err)
		}
		if got := cleanSeedRuntimeExecute(t, a, cleanSeedOwnerClaim(t, a, initial.InstanceID)); got.State != "succeeded" {
			t.Fatal(got.State, got.ErrorCode)
		}
	}
	old := noticeRouteTestIDs(t, a, "plugin_message_queue")
	if len(old) < 2 {
		t.Fatal("quiet-hour source messages not queued", len(old))
	}
	var mu sync.Mutex
	calls := map[string]int{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls[r.Header.Get("X-Queue")]++
		mu.Unlock()
		w.WriteHeader(204)
	}))
	defer s.Close()
	sender := func(ctx context.Context, q pluginNoticeQueueEntry) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("X-Queue", q.ID)
		resp, err := s.Client().Do(req)
		if err != nil {
			return 0, err
		}
		resp.Body.Close()
		return 1, nil
	}
	a.pluginStore.notices.send = sender
	if got := noticeRouteTestToggle(t, a, initial.InstanceID, "owned-direct-user"); got.State != "succeeded" {
		t.Fatal(got.State, got.ErrorCode)
	}
	mu.Lock()
	n := len(calls)
	mu.Unlock()
	if n != 2 {
		t.Fatal("explicit recipient did not bypass old FIFO/window", n)
	}
	for _, id := range old {
		entry, err := a.pluginStore.readPluginMessage(context.Background(), a.Store.DB, id)
		if err != nil || entry.State != "queued" || entry.Direct {
			t.Fatal("direct consumed unrelated old FIFO", entry.State, err)
		}
	}
	// Publication is independent of the closed core sending window.
	var events int
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='notice.message'`).Scan(&events); err != nil || events < len(old)+2 {
		t.Fatal("NoticeMessage wrongly withheld by quiet hours", events, err)
	}
	clock.Store(time.Date(2026, 10, 18, 4, 0, 0, 0, time.UTC).UnixNano())
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER lose_notice_finish BEFORE UPDATE ON plugin_message_queue WHEN NEW.state='consumed' BEGIN SELECT RAISE(ABORT,'owned after-send crash'); END`); err != nil {
		t.Fatal(err)
	}
	if err = a.drainPluginMessages(context.Background(), 200); !errors.Is(err, seedhost.ErrUnknown) {
		t.Fatal("unrecorded finish became delivered", err)
	}
	first, err := a.pluginStore.readPluginMessage(context.Background(), a.Store.DB, old[0])
	if err != nil || first.State != "intent" {
		t.Fatal("intent lost after accepted HTTP and failed SQL", first.State, err)
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER lose_notice_finish`); err != nil {
		t.Fatal(err)
	}
	a.logs.close()
	if err = a.Store.Close(); err != nil {
		t.Fatal(err)
	}
	a = nil
	a, err = New(Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	a.pluginStore.notices.now = nowFn
	a.pluginStore.notices.send = sender
	if err = a.drainPluginMessages(context.Background(), 200); err != nil {
		t.Fatal(err)
	}
	first, err = a.pluginStore.readPluginMessage(context.Background(), a.Store.DB, old[0])
	if err != nil || first.State != "unknown" {
		t.Fatal("cold intent did not remain unknown", first.State, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls[old[0]] != 1 {
		t.Fatal("cold unknown repeated the actual HTTP call", calls[old[0]])
	}
	for _, id := range old[1:] {
		if calls[id] != 1 {
			t.Fatal("unknown blocked later FIFO item", calls[id])
		}
	}
	for id, n := range calls {
		if n != 1 {
			t.Fatal("duplicate actual loopback send", id, n)
		}
	}
}

func TestPluginNoticeRouteIsolationAndOpaqueEventBacking(t *testing.T) {
	for _, binding := range []string{"missing", "empty", "bound"} {
		t.Run(binding, func(t *testing.T) {
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			d := cleanSeedOwnerDaemonNew(t)
			d.settings(a)
			noticePreferenceTest(t, a, "switch", "站点", "admin,user")
			if binding == "empty" {
				noticePreferenceTest(t, a, "user", "admin", map[string]any{})
			}
			if binding == "bound" {
				noticePreferenceTest(t, a, "user", "admin", map[string]any{"Lark": "owned-private-recipient"})
			}
			initial := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cleanSeedOwnerConfig(map[string]any{"notify": true}), true)
			cleanSeedRuntimeExecute(t, a, initial)
			if _, err := a.pluginStore.EnqueueRun(context.Background(), initial.InstanceID, "run", nil); err != nil {
				t.Fatal(err)
			}
			if got := cleanSeedRuntimeExecute(t, a, cleanSeedOwnerClaim(t, a, initial.InstanceID)); got.State != "succeeded" {
				t.Fatal(got.State, got.ErrorCode)
			}
			rows, err := a.Store.DB.Query(`SELECT id FROM plugin_notice_publications ORDER BY rowid`)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for rows.Next() {
				var id string
				if rows.Scan(&id) != nil {
					t.Fatal("scan")
				}
				ids = append(ids, id)
			}
			rows.Close()
			if len(ids) == 0 {
				t.Fatal("missing real publication")
			}
			for _, id := range ids {
				data, err := a.pluginStore.noticeMessage(context.Background(), a.Store.DB, id)
				if err != nil {
					t.Fatal(err)
				}
				if data["type"] != "站点" || data["mtype"] != "站点" {
					t.Fatal("event enum values lost")
				}
				if _, exists := data["save_history"]; exists {
					t.Fatal("source event still contains save_history")
				}
				switch binding {
				case "missing":
					if data["targets"] != nil {
						t.Fatal("missing user changed into empty map")
					}
				case "empty":
					m, ok := data["targets"].(map[string]any)
					if !ok || len(m) != 0 {
						t.Fatal("empty mapping became missing")
					}
				case "bound":
					m, ok := data["targets"].(map[string]any)
					if !ok || m["Lark"] != "owned-private-recipient" {
						t.Fatal("bound targets lost")
					}
				}
				var eventRaw []byte
				if err = a.Store.DB.QueryRow(`SELECT payload FROM domain_events WHERE event_id=?`, domainEventID("notice.message", id)).Scan(&eventRaw); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(eventRaw), "owned-private-recipient") || strings.Contains(string(eventRaw), "title") {
					t.Fatal("public event disclosed private payload")
				}
			}
			tx, err := a.Store.DB.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			fake := "pnm_" + strings.Repeat("a", 64)
			err = recordDomainEventTx(context.Background(), tx, DomainEvent{Type: "notice.message", EventID: domainEventID("notice.message", fake), NoticeID: fake})
			_ = tx.Rollback()
			if err == nil {
				t.Fatal("public reference without committed private fact accepted")
			}
			routeIDs := noticeRouteTestIDs(t, a, "plugin_notice_routes")
			r, steps, err := a.pluginStore.readNoticeRoute(context.Background(), a.Store.DB, routeIDs[0])
			if err != nil || !r.Complete || len(steps) == 0 {
				t.Fatal(err)
			}
			if _, err = a.Store.DB.Exec(`DELETE FROM plugin_notice_route_steps WHERE route_id=? AND ordinal=0`, r.ID); err != nil {
				t.Fatal(err)
			}
			if err = a.runNoticeRoute(context.Background(), r.ID); !errors.Is(err, seedhost.ErrUnknown) {
				t.Fatal("omitted stage replayed source fanout", err)
			}
		})
	}
}

func TestPluginNoticeRouteSenderOwnsNestedCopy(t *testing.T) {
	a := newTestApp(t)
	initial := noticeRouteTestInit(t, a)
	a.pluginStore.notices.send = func(_ context.Context, q pluginNoticeQueueEntry) (int, error) {
		body := q.Call.Kwargs["message"].(map[string]any)
		body["userid"] = "modified-native-copy"
		body["buttons"] = []any{[]any{map[string]any{"label": "owned"}}}
		q.Call.Args[0] = "modified-copy"
		return 1, nil
	}
	if got := noticeRouteTestToggle(t, a, initial.InstanceID, "original-owned-user"); got.State != "succeeded" {
		t.Fatal("native copy mutation broke durable finish", got.State, got.ErrorCode)
	}
	for _, id := range noticeRouteTestIDs(t, a, "plugin_message_queue") {
		entry, err := a.pluginStore.readPluginMessage(context.Background(), a.Store.DB, id)
		if err != nil || entry.State != "consumed" || entry.Call.Args[0] != "post_message" {
			t.Fatal("owned callback changed immutable call", entry.State, err)
		}
		body := entry.Call.Kwargs["message"].(map[string]any)
		if body["userid"] != "original-owned-user" || body["buttons"] != nil {
			t.Fatal("nested callback mutation reached sealed source call")
		}
	}
}

func TestPluginNoticeRouteDirectPendingCannotStarveBoundedFIFOScan(t *testing.T) {
	a := newTestApp(t)
	initial := noticeRouteTestInit(t, a)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER block_direct_intent BEFORE UPDATE ON plugin_message_queue WHEN NEW.state='intent' BEGIN SELECT RAISE(ABORT,'owned direct intent interruption'); END`); err != nil {
		t.Fatal(err)
	}
	if got := noticeRouteTestToggle(t, a, initial.InstanceID, "owned-direct-user"); got.State != "reconcile_pending" {
		t.Fatal("source direct interruption not retained", got.State, got.ErrorCode)
	}
	ids := noticeRouteTestIDs(t, a, "plugin_message_queue")
	if len(ids) != 1 {
		t.Fatal(len(ids))
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER block_direct_intent`); err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 10, 18, 10, 0, 0, 0, time.FixedZone("owned-local", 8*3600))
	a.pluginStore.notices.now = func() time.Time { return fixed }
	noticePreferenceTest(t, a, "window", "", []any{map[string]any{"start": "12:00", "end": "12:00"}})
	if err := a.pluginStore.reloadNoticePeriods(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := cleanSeedOwnerDaemonNew(t)
	d.settings(a)
	quiet := uninstallInstall(t, a, "CleanInvalidSeed-quiet", seed.Manifest(), cleanSeedOwnerConfig(map[string]any{"notify": true}), true)
	if got := cleanSeedRuntimeExecute(t, a, quiet); got.State != "succeeded" {
		t.Fatal(got.State, got.ErrorCode)
	}
	if _, err := a.pluginStore.EnqueueRun(context.Background(), quiet.InstanceID, "run", nil); err != nil {
		t.Fatal(err)
	}
	if got := cleanSeedRuntimeExecute(t, a, cleanSeedOwnerClaim(t, a, quiet.InstanceID)); got.State != "succeeded" {
		t.Fatal(got.State, got.ErrorCode)
	}
	ids = noticeRouteTestIDs(t, a, "plugin_message_queue")
	if len(ids) < 2 {
		t.Fatal("later real FIFO source missing")
	}
	fixed = time.Date(2026, 10, 18, 12, 0, 0, 0, time.FixedZone("owned-local", 8*3600))
	var calls atomic.Int32
	a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { calls.Add(1); return 1, nil }
	if err := a.drainPluginMessages(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("FIFO monitor stole source's direct continuation")
	}
	if err := a.drainPluginMessages(context.Background(), 1); err != nil || calls.Load() != 1 {
		t.Fatal("bounded skipped-direct scan starved later FIFO", calls.Load(), err)
	}
}

func TestPluginNoticeQueueUsesExplicitReloadedWindowSnapshot(t *testing.T) {
	a := newTestApp(t)
	at := time.Date(2026, 10, 18, 10, 0, 0, 0, time.FixedZone("owned", 8*3600))
	before, err := a.pluginStore.noticePeriods(context.Background())
	if err != nil || !notice.Allowed(before, at) {
		t.Fatal(err)
	}
	noticePreferenceTest(t, a, "window", "", []any{map[string]any{"start": "12:00", "end": "12:00"}})
	existing, err := a.pluginStore.noticePeriods(context.Background())
	if err != nil || !notice.Allowed(existing, at) {
		t.Fatal("DB edit silently reinitialized the source queue", err)
	}
	if err = a.pluginStore.reloadNoticePeriods(context.Background()); err != nil {
		t.Fatal(err)
	}
	next, err := a.pluginStore.noticePeriods(context.Background())
	if err != nil || notice.Allowed(next, at) {
		t.Fatal("explicit native init_config did not apply", err)
	}
	noticePreferenceTest(t, a, "window", "", json.Number("4"))
	if err = a.pluginStore.reloadNoticePeriods(context.Background()); err == nil {
		t.Fatal("invalid original schedule accepted")
	}
	retained, err := a.pluginStore.noticePeriods(context.Background())
	if err != nil || notice.Allowed(retained, at) {
		t.Fatal("failed reload replaced previous source periods", err)
	}
}

func TestPluginNoticeRouteCommittedAcceptanceReplyLossReadsBackBeforeContinuation(t *testing.T) {
	a := newTestApp(t)
	initial := noticeRouteTestInit(t, a)
	owner := a.pluginCleanInvalidSeed.owners[initial.InstanceID]
	var lost atomic.Bool
	owner.journal.afterCommit = func() error {
		if lost.Load() {
			return nil
		}
		var n int
		if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_clean_seed_outbox WHERE instance_id=?`, initial.InstanceID).Scan(&n); err != nil {
			return err
		}
		if n == 1 && lost.CompareAndSwap(false, true) {
			return errors.New("owned acknowledgement lost after actual SQL commit")
		}
		return nil
	}
	var sends atomic.Int32
	a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
	got := noticeRouteTestToggle(t, a, initial.InstanceID, "owned-user")
	if !lost.Load() || got.State != "succeeded" || sends.Load() != 2 {
		t.Fatal("exact committed A receipt did not continue once", lost.Load(), got.State, got.ErrorCode, sends.Load())
	}
	if len(cleanSeedNoticeTokens(t, a, initial.InstanceID)) != 2 || len(noticeRouteTestIDs(t, a, "plugin_message_queue")) != 2 {
		t.Fatal("A acknowledgement loss recreated notice/queue")
	}
	var history int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='notification'`).Scan(&history); err != nil || history != 2 {
		t.Fatal("duplicate accepted history", history, err)
	}
	config, err := a.pluginStore.Config(context.Background(), initial.InstanceID)
	if err != nil || config.Revision != 2 {
		t.Fatal("source config not continued exactly once", config.Revision, err)
	}
}

func TestPluginNoticeRouteCommittedStageReplyLossDoesNotRepeatSend(t *testing.T) {
	for _, operation := range []string{"route_step", "route_finish", "queue_intent", "queue_finish"} {
		t.Run(operation, func(t *testing.T) {
			a := newTestApp(t)
			initial := noticeRouteTestInit(t, a)
			var lost atomic.Bool
			a.pluginStore.notices.afterCommit = func(actual string) error {
				if actual == operation && lost.CompareAndSwap(false, true) {
					return errors.New("owned acknowledgement lost after actual commit")
				}
				return nil
			}
			var sends atomic.Int32
			a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
			got := noticeRouteTestToggle(t, a, initial.InstanceID, "owned-user")
			if !lost.Load() || got.State != "succeeded" || sends.Load() != 2 {
				t.Fatal("exact receipt failed to continue once", operation, lost.Load(), got.State, got.ErrorCode, sends.Load())
			}
			for _, id := range noticeRouteTestIDs(t, a, "plugin_notice_routes") {
				if err := a.runNoticeRoute(context.Background(), id); err != nil {
					t.Fatal(err)
				}
			}
			if sends.Load() != 2 {
				t.Fatal("route readback repeated a sender", sends.Load())
			}
			config, err := a.pluginStore.Config(context.Background(), initial.InstanceID)
			if err != nil || config.Revision != 2 {
				t.Fatal("source config did not continue exactly once", config.Revision, err)
			}
		})
	}
}

func TestPluginNoticeQueueUsesHostLocalClockRatherThanPluginScheduleZone(t *testing.T) {
	for _, zone := range []*time.Location{time.UTC, time.FixedZone("owned-west", -5*3600)} {
		t.Run(zone.String(), func(t *testing.T) {
			a := newTestApp(t)
			initial := noticeRouteTestInit(t, a)
			at := time.Date(2026, 10, 18, 12, 0, 0, 0, zone)
			a.pluginStore.notices.now = func() time.Time { return at }
			noticePreferenceTest(t, a, "window", "", []any{map[string]any{"start": "12:00", "end": "12:00"}})
			if err := a.pluginStore.reloadNoticePeriods(context.Background()); err != nil {
				t.Fatal(err)
			}
			d := cleanSeedOwnerDaemonNew(t)
			d.settings(a)
			// A business notification has no explicit user/immediately bypass.
			notify := uninstallInstall(t, a, "CleanInvalidSeed-clock", seed.Manifest(), cleanSeedOwnerConfig(map[string]any{"notify": true}), true)
			cleanSeedRuntimeExecute(t, a, notify)
			var sends atomic.Int32
			a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
			if _, err := a.pluginStore.EnqueueRun(context.Background(), notify.InstanceID, "run", nil); err != nil {
				t.Fatal(err)
			}
			got := cleanSeedRuntimeExecute(t, a, cleanSeedOwnerClaim(t, a, notify.InstanceID))
			if got.State != "succeeded" || sends.Load() == 0 {
				t.Fatal("host-local open period became a closed Shanghai period", initial.InstanceID, got.State, got.ErrorCode, sends.Load())
			}
			for _, id := range noticeRouteTestIDs(t, a, "plugin_message_queue") {
				entry, err := a.pluginStore.readPluginMessage(context.Background(), a.Store.DB, id)
				if err != nil || !entry.Direct || entry.State != "consumed" {
					t.Fatal("local window not retained at acceptance", entry.State, err)
				}
			}
		})
	}
}

func TestPluginNoticeEventsUseActualCollectionAndRejectMissingPrivateFacts(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		name := "authenticated"
		if corrupt {
			name = "missing-private-fact"
		}
		t.Run(name, func(t *testing.T) {
			a := newTestApp(t)
			initial := noticeRouteTestInit(t, a)
			p := a.pluginStore
			installStoredPlugin(t, p, "consumer")
			if _, _, err := p.SaveConfigAndQueue(context.Background(), "consumer", 0, map[string]any{"enabled": true}); err != nil {
				t.Fatal(err)
			}
			run, err := p.ClaimRun(context.Background())
			if err != nil || run.InstanceID != "consumer" {
				t.Fatal(run.InstanceID, err)
			}
			if err = p.configureEventSubscription(context.Background(), run, "refresh", []string{"notice.message"}, 0, true); err != nil {
				t.Fatal(err)
			}
			if err = p.FinishRun(context.Background(), run.ID, "succeeded", "", nil); err != nil {
				t.Fatal(err)
			}
			var before int64
			if err = a.Store.DB.QueryRow(`SELECT cursor FROM plugin_event_subscriptions WHERE instance_id='consumer'`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if got := noticeRouteTestToggle(t, a, initial.InstanceID, "owned-event-user"); got.State != "succeeded" {
				t.Fatal(got.State, got.ErrorCode)
			}
			if corrupt {
				if _, err = a.Store.DB.Exec(`DELETE FROM plugin_notice_publications WHERE rowid=(SELECT min(rowid) FROM plugin_notice_publications)`); err != nil {
					t.Fatal(err)
				}
				if err = a.dispatchPluginEvents(context.Background(), time.Now()); err == nil {
					t.Fatal("unauthenticated notice delivered")
				}
				var after int64
				if err = a.Store.DB.QueryRow(`SELECT cursor FROM plugin_event_subscriptions WHERE instance_id='consumer'`).Scan(&after); err != nil || after != before || pluginEventCount(t, a, "plugin_event_deliveries") != 0 {
					t.Fatal("failed notice collection partially advanced", before, after, err)
				}
				return
			}
			for i := 0; i < 2; i++ {
				if err = a.dispatchPluginEvents(context.Background(), time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			if pluginEventCount(t, a, "plugin_event_deliveries") != 2 || pluginEventCount(t, a, "plugin_event_batches") != 2 {
				t.Fatal("committed notice collection lost or duplicated events")
			}
			for i := 0; i < 2; i++ {
				eventRun, err := p.ClaimRun(context.Background())
				if err != nil || eventRun.Action != "event" || !json.Valid(eventRun.Input) {
					t.Fatal("notice event not durably queued", eventRun.Action, err)
				}
				if strings.Contains(string(eventRun.Input), "owned-event-user") {
					t.Fatal("private recipient leaked through public event batch")
				}
				if err := p.FinishRun(context.Background(), eventRun.ID, "succeeded", "", nil); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
