package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins/builtin"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
	"light-media/internal/plugins/statistic"
	"light-media/internal/sites"
)

// These fixtures run the compiled SDK against the real App and SQLite. Only
// account observations and the final sender are synthetic; no live accounts.
func statisticRouterInstall(t *testing.T, a *App, instance string) {
	t.Helper()
	useBuiltInPluginProcess(t, a)
	manifest, _, ok := builtin.Lookup("SiteStatistic")
	if !ok {
		t.Fatal("missing compiled SiteStatistic")
	}
	run := uninstallInstall(t, a, instance, manifest, builtin.SiteStatisticConfiguration{Enabled: true, NotifyType: "all"}, true)
	statisticRouterExecute(t, a, run)
}
func statisticRouterExecute(t *testing.T, a *App, run PluginRun) PluginRun {
	t.Helper()
	a.executePluginRun(context.Background(), run)
	got, err := a.pluginStore.Run(context.Background(), run.ID)
	if err != nil || got.State != "succeeded" {
		t.Fatal("actual SDK execution", got.State, got.ErrorCode, err)
	}
	return got
}
func statisticRouterSnapshot(t *testing.T, a *App) {
	t.Helper()
	ctx := context.Background()
	site := parallelSearchSite("router-owned-site", "http://127.0.0.1:9911")
	site.Name = "合成路由站点"
	site.Domain = "owned.invalid"
	settings := a.Settings()
	settings.Sites = []SiteConfig{site}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	uploaded, downloaded := int64(2<<30), int64(1<<30)
	snapshot := siteSnapshot{SiteID: site.ID, Name: site.Name, Domain: site.Domain, ConfigKey: a.managedSiteKey(site), Day: time.Now().In(shanghai()).Format("2006-01-02"), Stats: &sites.UserStats{Uploaded: &uploaded, Downloaded: &downloaded, CapturedAt: now()}}
	if err := a.Store.Put(ctx, "site_stats_latest", site.ID, snapshot); err != nil {
		t.Fatal(err)
	}
}
func statisticRouterEvent(t *testing.T, a *App, key string) {
	t.Helper()
	ctx := context.Background()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = recordDomainEventTx(ctx, tx, DomainEvent{EventID: domainEventID("site.refreshed", key), Type: "site.refreshed", SiteID: "*"}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = a.dispatchPluginEvents(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
}
func statisticRouterClaim(t *testing.T, a *App, instance string) PluginRun {
	t.Helper()
	run, err := a.pluginStore.claimRunForInstance(context.Background(), instance)
	if err != nil || run.Action != "event" {
		t.Fatal("genuine event claim", run.Action, err)
	}
	return run
}
func statisticRouterReceipt(t *testing.T, a *App, run PluginRun) siteStatisticNoticeReceipt {
	t.Helper()
	raw, err := a.pluginStore.siteStatisticReceipt(context.Background(), run)
	var receipt siteStatisticNoticeReceipt
	if err != nil || json.Unmarshal(raw, &receipt) != nil {
		t.Fatal("operation receipt", err)
	}
	return receipt
}
func statisticRouterCount(t *testing.T, a *App, table string) int {
	t.Helper()
	var count int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
func TestSiteStatisticRouterSDKInstancesAndPreferences(t *testing.T) {
	for _, binding := range []string{"default", "admin"} {
		t.Run(binding, func(t *testing.T) {
			a := newTestApp(t)
			statisticRouterSnapshot(t, a)
			statisticRouterInstall(t, a, "SiteStatistic")
			statisticRouterInstall(t, a, "SiteStatistic_owned_clone")
			if binding == "admin" {
				noticePreferenceTest(t, a, "switch", "站点", "admin")
				noticePreferenceTest(t, a, "user", "admin", map[string]any{"Lark": "owned-recipient"})
			}
			var sends atomic.Int32
			a.pluginStore.notices.send = func(ctx context.Context, q pluginNoticeQueueEntry) (int, error) {
				sends.Add(1)
				raw, _ := json.Marshal(q.Call)
				if binding == "admin" && !strings.Contains(string(raw), "owned-recipient") {
					t.Error("Router ignored notification preferences", string(raw))
				}
				if !a.pluginMu.TryLock() {
					t.Error("shared pluginMu held over sender callback")
				} else {
					a.pluginMu.Unlock()
				}
				var n int
				if err := a.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM plugin_site_statistic_notices`).Scan(&n); err != nil {
					t.Error("sender held SQL transaction", err)
				}
				return 1, nil
			}
			statisticRouterEvent(t, a, "owned-instances")
			for _, instance := range []string{"SiteStatistic", "SiteStatistic_owned_clone"} {
				run := statisticRouterExecute(t, a, statisticRouterClaim(t, a, instance))
				receipt := statisticRouterReceipt(t, a, run)
				if !receipt.Notified || receipt.RouteID == "" {
					t.Fatal("SDK bypassed Router", receipt)
				}
				route, _, err := a.pluginStore.readNoticeRoute(context.Background(), a.Store.DB, receipt.RouteID)
				if err != nil || route.Instance != instance || !route.Complete {
					t.Fatal("instance-bound route", route, err)
				}
				if _, err = a.pluginStore.Data(context.Background(), instance, "last_notify"); err != nil {
					t.Fatal(err)
				}
			}
			if sends.Load() != 2 || statisticRouterCount(t, a, "plugin_notice_publications") != 2 {
				t.Fatal("independent instances did not each route exactly once", sends.Load())
			}
			statisticRouterEvent(t, a, "owned-duplicate-snapshot")
			for _, instance := range []string{"SiteStatistic", "SiteStatistic_owned_clone"} {
				run := statisticRouterExecute(t, a, statisticRouterClaim(t, a, instance))
				if statisticRouterReceipt(t, a, run).Notified {
					t.Fatal("same-day fingerprint resent")
				}
			}
			if sends.Load() != 2 {
				t.Fatal("duplicate external send", sends.Load())
			}
		})
	}
}
func TestSiteStatisticRouterCommittedReplyLossAndUnknownNoResend(t *testing.T) {
	for _, stage := range []string{"site_statistic_accept", "route_step", "route_finish", "queue_intent", "queue_finish", "sender_unknown"} {
		t.Run(stage, func(t *testing.T) {
			a := newTestApp(t)
			statisticRouterSnapshot(t, a)
			statisticRouterInstall(t, a, "SiteStatistic")
			var lost atomic.Bool
			var sends atomic.Int32
			a.pluginStore.notices.afterCommit = func(op string) error {
				if op == stage && lost.CompareAndSwap(false, true) {
					return errors.New("owned committed acknowledgement lost")
				}
				return nil
			}
			a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) {
				sends.Add(1)
				if stage == "sender_unknown" {
					return 1, seedhost.ErrUnknown
				}
				return 1, nil
			}
			statisticRouterEvent(t, a, "owned-reply-loss")
			run := statisticRouterExecute(t, a, statisticRouterClaim(t, a, "SiteStatistic"))
			receipt := statisticRouterReceipt(t, a, run)
			if stage != "sender_unknown" && !lost.Load() {
				t.Fatal("fault hook never reached", stage)
			}
			var wg sync.WaitGroup
			errs := make(chan error, 8)
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); errs <- a.runNoticeRoute(context.Background(), receipt.RouteID) }()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal("concurrent receipt replay", err)
				}
			}
			if sends.Load() != 1 {
				t.Fatal("committed or uncertain send retried", sends.Load())
			}
			if _, err := a.pluginStore.Data(context.Background(), run.InstanceID, "last_notify"); err != nil {
				t.Fatal("normal Router return did not save fingerprint", stage, err)
			}
			ids := noticeRouteTestIDs(t, a, "plugin_message_queue")
			if len(ids) != 1 {
				t.Fatal("duplicate queue", ids)
			}
			q, err := a.pluginStore.readPluginMessage(context.Background(), a.Store.DB, ids[0])
			want := "consumed"
			if stage == "sender_unknown" {
				want = "unknown"
			}
			if err != nil || q.State != want {
				t.Fatal("durable outcome", q.State, err)
			}
		})
	}
}
func TestSiteStatisticRouterAcceptanceRollback(t *testing.T) {
	a := newTestApp(t)
	statisticRouterSnapshot(t, a)
	statisticRouterInstall(t, a, "SiteStatistic")
	statisticRouterEvent(t, a, "owned-rollback")
	run := statisticRouterClaim(t, a, "SiteStatistic")
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_statistic_reject BEFORE INSERT ON plugin_site_statistic_operations BEGIN SELECT RAISE(ABORT,'owned late rollback'); END`); err != nil {
		t.Fatal(err)
	}
	payload := &statistic.Notification{Title: "合成统计", Text: "owned", Fingerprint: strings.Repeat("a", 64)}
	if _, err := a.commitSiteStatisticNotice(context.Background(), run, payload, "all", time.Now().In(shanghai())); err == nil {
		t.Fatal("late failure accepted")
	}
	for _, table := range []string{"plugin_site_statistic_notices", "plugin_site_statistic_pending", "plugin_notice_routes", "plugin_notice_publications", "plugin_message_queue", "plugin_site_statistic_operations"} {
		if n := statisticRouterCount(t, a, table); n != 0 {
			t.Fatal("partial acceptance escaped rollback", table, n)
		}
	}
	var history int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='notification'`).Scan(&history); err != nil || history != 0 {
		t.Fatal("history escaped rollback", history, err)
	}
	if _, err := a.pluginStore.Data(context.Background(), run.InstanceID, "last_notify"); !missingPluginValue(err) {
		t.Fatal("dedup escaped rollback", err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_statistic_reject`); err != nil {
		t.Fatal(err)
	}
	statisticRouterExecute(t, a, run)
	if statisticRouterCount(t, a, "plugin_notice_routes") != 1 {
		t.Fatal("retry failed to accept exactly once")
	}
}
func TestSiteStatisticRouterProofCorruptionFailsClosed(t *testing.T) {
	for _, target := range []string{"source_missing", "source_tampered", "receipt_missing", "receipt_tampered", "history_missing", "run_input_tampered"} {
		t.Run(target, func(t *testing.T) {
			a := newTestApp(t)
			statisticRouterSnapshot(t, a)
			statisticRouterInstall(t, a, "SiteStatistic")
			statisticRouterEvent(t, a, "owned-corruption")
			run := statisticRouterClaim(t, a, "SiteStatistic")
			raw, err := a.commitSiteStatisticNotice(context.Background(), run, &statistic.Notification{Title: "合成统计", Text: "owned", Fingerprint: strings.Repeat("b", 64)}, "all", time.Now().In(shanghai()))
			var receipt siteStatisticNoticeReceipt
			if err != nil || json.Unmarshal(raw, &receipt) != nil || receipt.RouteID == "" {
				t.Fatal("acceptance", err)
			}
			query := ""
			switch target {
			case "source_missing":
				query = `DELETE FROM plugin_site_statistic_notices`
			case "source_tampered":
				query = `UPDATE plugin_site_statistic_notices SET sealed=x'00'`
			case "receipt_missing":
				query = `DELETE FROM plugin_site_statistic_operations`
			case "receipt_tampered":
				query = `UPDATE plugin_site_statistic_operations SET result=x'00'`
			case "history_missing":
				query = `DELETE FROM records WHERE kind='notification'`
			case "run_input_tampered":
				query = `UPDATE plugin_runs SET input=x'00' WHERE action='event'`
			}
			if _, err = a.Store.DB.Exec(query); err != nil {
				t.Fatal(err)
			}
			var sends atomic.Int32
			a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
			if err = a.runNoticeRoute(context.Background(), receipt.RouteID); err == nil {
				t.Fatal("corrupt proof accepted", target)
			}
			if sends.Load() != 0 || statisticRouterCount(t, a, "plugin_notice_publications") != 0 || statisticRouterCount(t, a, "plugin_message_queue") != 0 {
				t.Fatal("corrupt source reached external boundary", target, sends.Load())
			}
		})
	}
}
func TestSiteStatisticRouterColdRecoveryAndLegacyReceipt(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint("legacy=", legacy), func(t *testing.T) {
			data := t.TempDir()
			a, err := New(Options{DataDir: data})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { a.Close() }()
			statisticRouterSnapshot(t, a)
			statisticRouterInstall(t, a, "SiteStatistic")
			statisticRouterEvent(t, a, "owned-cold")
			run := statisticRouterClaim(t, a, "SiteStatistic")
			var routeID string
			if legacy {
				tx, err := a.Store.DB.BeginTx(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = a.pluginStore.saveSiteStatisticReceiptTx(context.Background(), tx, run, map[string]bool{"notified": true}); err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
			} else {
				raw, err := a.commitSiteStatisticNotice(context.Background(), run, &statistic.Notification{Title: "合成统计", Text: "owned cold", Fingerprint: strings.Repeat("c", 64)}, "all", time.Now().In(shanghai()))
				var receipt siteStatisticNoticeReceipt
				if err != nil || json.Unmarshal(raw, &receipt) != nil {
					t.Fatal(err)
				}
				routeID = receipt.RouteID
			}
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			a, err = New(Options{DataDir: data})
			if err != nil {
				t.Fatal(err)
			}
			var sends atomic.Int32
			a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
			if err = a.pluginStore.recoverPluginRuns(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !legacy {
				pending, readErr := a.pluginStore.Run(context.Background(), run.ID)
				if readErr != nil || pending.State != "reconcile_pending" {
					t.Fatal("incomplete route was reported terminal", pending.State, readErr)
				}
				if err = a.resumePluginNoticeRoutes(context.Background(), 200); err != nil {
					t.Fatal("cold accepted route", err)
				}
				route, _, readErr := a.pluginStore.readNoticeRoute(context.Background(), a.Store.DB, routeID)
				if readErr != nil || !route.Complete || route.SourceError != "" {
					t.Fatal("resume did not finish route", readErr)
				}
			}
			got, err := a.pluginStore.Run(context.Background(), run.ID)
			if err != nil || got.State != "succeeded" {
				t.Fatal("cold operation recovery", got.State, err)
			}
			want := int32(1)
			if legacy {
				want = 0
			}
			if sends.Load() != want {
				t.Fatal("cold/legacy external attempt count", sends.Load(), want)
			}
			if legacy && statisticRouterCount(t, a, "plugin_notice_routes") != 0 {
				t.Fatal("legacy receipt invented route")
			}
		})
	}
}

func TestSiteStatisticRouterConcurrentAcceptedRouteSendsOnce(t *testing.T) {
	a := newTestApp(t)
	statisticRouterSnapshot(t, a)
	statisticRouterInstall(t, a, "SiteStatistic")
	statisticRouterEvent(t, a, "owned-concurrent-acceptance")
	run := statisticRouterClaim(t, a, "SiteStatistic")
	raw, err := a.commitSiteStatisticNotice(context.Background(), run, &statistic.Notification{Title: "合成统计", Text: "owned concurrent", Fingerprint: strings.Repeat("d", 64)}, "all", time.Now().In(shanghai()))
	var receipt siteStatisticNoticeReceipt
	if err != nil || json.Unmarshal(raw, &receipt) != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var sends atomic.Int32
	a.pluginStore.notices.send = func(ctx context.Context, _ pluginNoticeQueueEntry) (int, error) {
		if sends.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return 1, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- a.runNoticeRoute(ctx, receipt.RouteID) }()
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("accepted route did not reach sender")
	}
	if !a.pluginMu.TryLock() {
		t.Error("shared plugin lock retained while concurrent sender blocked")
	} else {
		a.pluginMu.Unlock()
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if sends.Load() != 1 || statisticRouterCount(t, a, "plugin_notice_publications") != 1 || statisticRouterCount(t, a, "plugin_message_queue") != 1 {
		t.Fatal("parallel route drivers duplicated effects", sends.Load())
	}
}

func TestSiteStatisticRouterNotificationWindowQueuesWithoutSending(t *testing.T) {
	a := newTestApp(t)
	statisticRouterSnapshot(t, a)
	statisticRouterInstall(t, a, "SiteStatistic")
	clock := time.Date(2026, 10, 21, 2, 0, 0, 0, time.UTC)
	a.pluginStore.notices.now = func() time.Time { return clock }
	noticePreferenceTest(t, a, "window", "", []any{map[string]any{"start": "12:00", "end": "12:00"}})
	if err := a.pluginStore.reloadNoticePeriods(context.Background()); err != nil {
		t.Fatal(err)
	}
	var sends atomic.Int32
	a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
	statisticRouterEvent(t, a, "owned-delayed-window")
	run := statisticRouterExecute(t, a, statisticRouterClaim(t, a, "SiteStatistic"))
	if !statisticRouterReceipt(t, a, run).Notified {
		t.Fatal("window erased accepted statistic notice")
	}
	ids := noticeRouteTestIDs(t, a, "plugin_message_queue")
	if len(ids) != 1 || sends.Load() != 0 {
		t.Fatal("quiet window bypassed", ids, sends.Load())
	}
	q, err := a.pluginStore.readPluginMessage(context.Background(), a.Store.DB, ids[0])
	if err != nil || q.State != "queued" || q.Direct {
		t.Fatal("quiet window queue state", q.State, q.Direct, err)
	}
}

func TestSiteStatisticRouterStaleAcceptanceVersusHistoricalReplay(t *testing.T) {
	for _, transition := range []string{"config_edit", "reset"} {
		t.Run(transition, func(t *testing.T) {
			a := newTestApp(t)
			statisticRouterSnapshot(t, a)
			statisticRouterInstall(t, a, "SiteStatistic")
			statisticRouterEvent(t, a, "owned-before-transition")
			run := statisticRouterClaim(t, a, "SiteStatistic")
			payload := &statistic.Notification{Title: "合成统计", Text: "owned historical", Fingerprint: strings.Repeat("e", 64)}
			raw, err := a.commitSiteStatisticNotice(context.Background(), run, payload, "all", time.Now().In(shanghai()))
			var receipt siteStatisticNoticeReceipt
			if err != nil || json.Unmarshal(raw, &receipt) != nil {
				t.Fatal(err)
			}
			if transition == "reset" {
				err = a.pluginStore.Reset(context.Background(), run.InstanceID)
			} else {
				_, err = a.pluginStore.SaveConfig(context.Background(), run.InstanceID, run.ConfigRevision, builtin.SiteStatisticConfiguration{Enabled: false, NotifyType: "all"})
			}
			if err != nil {
				t.Fatal("lifecycle transition", err)
			}
			if _, err = a.commitSiteStatisticNotice(context.Background(), run, payload, "all", time.Now().In(shanghai())); err == nil {
				t.Fatal("stale run accepted new host call")
			}
			var sends atomic.Int32
			a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
			if err = a.runNoticeRoute(context.Background(), receipt.RouteID); err != nil {
				t.Fatal("immutable accepted route lost after transition", err)
			}
			if sends.Load() != 1 {
				t.Fatal("accepted historical effect count", sends.Load())
			}
			if _, err = a.pluginStore.Data(context.Background(), run.InstanceID, "last_notify"); !missingPluginValue(err) {
				t.Fatal("old completion repopulated changed lifecycle KV", transition, err)
			}
		})
	}
}

func TestSiteStatisticRouterRejectsReloadAndForgedPayload(t *testing.T) {
	a := newTestApp(t)
	statisticRouterSnapshot(t, a)
	statisticRouterInstall(t, a, "SiteStatistic")
	ctx := context.Background()
	if _, err := a.pluginStore.EnqueueRun(ctx, "SiteStatistic", "reload", nil); err != nil {
		t.Fatal(err)
	}
	reload, err := a.pluginStore.claimRunForInstance(ctx, "SiteStatistic")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.commitSiteStatisticNotice(ctx, reload, &statistic.Notification{Title: "forged", Text: "forged", Fingerprint: strings.Repeat("f", 64)}, "all", time.Now().In(shanghai())); err == nil {
		t.Fatal("reload manufactured notice authority")
	}
	statisticRouterExecute(t, a, reload)
	statisticRouterEvent(t, a, "owned-forged-sdk")
	run := statisticRouterClaim(t, a, "SiteStatistic")
	installation, err := a.pluginStore.Installation(ctx, run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []json.RawMessage{json.RawMessage(`{"batch_id":"foreign"}`), json.RawMessage(`{"title":"forged","text":"forged","notified":true}`)} {
		if _, err = a.pluginSiteStatisticNotifyHost(ctx, installation, &run, raw); err == nil {
			t.Fatal("SDK payload forged notice authority", string(raw))
		}
	}
	if statisticRouterCount(t, a, "plugin_notice_routes") != 0 {
		t.Fatal("forged source produced route")
	}
	statisticRouterExecute(t, a, run)
}

// Create a genuine SDK routing failure, then model process loss between the
// durable route terminal receipt and recording the SDK run's terminal result.
func TestSiteStatisticRouterColdRecoveryPreservesTerminalRouteFailure(t *testing.T) {
	data := t.TempDir()
	a, err := New(Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { a.Close() }()
	statisticRouterSnapshot(t, a)
	statisticRouterInstall(t, a, "SiteStatistic")
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_route_publication_failure BEFORE INSERT ON plugin_notice_publications BEGIN SELECT RAISE(ABORT,'owned publication failure'); END`); err != nil {
		t.Fatal(err)
	}
	statisticRouterEvent(t, a, "owned-terminal-route-failure")
	run := statisticRouterClaim(t, a, "SiteStatistic")
	a.executePluginRun(context.Background(), run)
	failed, err := a.pluginStore.Run(context.Background(), run.ID)
	if err != nil || failed.State != "failed" || failed.ErrorCode == "" {
		t.Fatal("normal execution did not fail", failed.State, failed.ErrorCode, err)
	}
	receipt := statisticRouterReceipt(t, a, run)
	route, _, err := a.pluginStore.readNoticeRoute(context.Background(), a.Store.DB, receipt.RouteID)
	if err != nil || !route.Complete || route.SourceError == "" {
		t.Fatal("test did not create durable source failure", route.Complete, route.SourceError, err)
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_route_publication_failure`); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET state='running',result=NULL,error_code='' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	var sends atomic.Int32
	a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
	recovered, err := a.pluginStore.Run(context.Background(), run.ID)
	if err != nil || recovered.State != "failed" || recovered.ErrorCode != failed.ErrorCode {
		t.Fatal("cold failure differs from normal SDK", recovered.State, recovered.ErrorCode, failed.ErrorCode, err)
	}
	if err = a.resumePluginNoticeRoutes(context.Background(), 200); err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.recoverSiteStatisticRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered, err = a.pluginStore.Run(context.Background(), run.ID)
	if err != nil || recovered.State != "failed" || recovered.ErrorCode != failed.ErrorCode {
		t.Fatal("terminal failure changed during reconciliation", recovered.State, recovered.ErrorCode, err)
	}
	if sends.Load() != 0 || statisticRouterCount(t, a, "plugin_notice_publications") != 0 || statisticRouterCount(t, a, "plugin_message_queue") != 0 {
		t.Fatal("cold failure retried downstream effects", sends.Load())
	}
}

// The original source saves last_notify only after post_message returns.
// A confirmed Router exception must therefore permit a later genuine event,
// without replaying the failed route or any of its accepted queue attempts.
func TestSiteStatisticRouterTerminalFailureDoesNotCommitFingerprint(t *testing.T) {
	a := newTestApp(t)
	statisticRouterSnapshot(t, a)
	statisticRouterInstall(t, a, "SiteStatistic")
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_statistic_terminal_failure BEFORE INSERT ON plugin_notice_publications BEGIN SELECT RAISE(ABORT,'owned publication failure'); END`); err != nil {
		t.Fatal(err)
	}
	statisticRouterEvent(t, a, "owned-dedup-failed-route")
	first := statisticRouterClaim(t, a, "SiteStatistic")
	a.executePluginRun(context.Background(), first)
	failed, err := a.pluginStore.Run(context.Background(), first.ID)
	if err != nil || failed.State != "failed" {
		t.Fatal("SDK routing failure", failed.State, err)
	}
	receipt := statisticRouterReceipt(t, a, first)
	route, _, err := a.pluginStore.readNoticeRoute(context.Background(), a.Store.DB, receipt.RouteID)
	if err != nil || !route.Complete || route.SourceError == "" {
		t.Fatal("durable source failure missing", err)
	}
	if _, err = a.pluginStore.Data(context.Background(), first.InstanceID, "last_notify"); !missingPluginValue(err) {
		t.Fatal("throwing post_message committed last_notify", err)
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_statistic_terminal_failure`); err != nil {
		t.Fatal(err)
	}
	var sends atomic.Int32
	a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
	// Reading/resuming the failed original route still cannot issue a new send.
	if err = a.runNoticeRoute(context.Background(), receipt.RouteID); err == nil {
		t.Fatal("failed original route became success")
	}
	if sends.Load() != 0 {
		t.Fatal("failed original route resent")
	}
	statisticRouterEvent(t, a, "owned-dedup-after-failed-route")
	second := statisticRouterExecute(t, a, statisticRouterClaim(t, a, "SiteStatistic"))
	next := statisticRouterReceipt(t, a, second)
	if !next.Notified || next.RouteID == "" || next.RouteID == receipt.RouteID {
		t.Fatal("later genuine event did not get its own attempt", next)
	}
	before, err := a.pluginStore.Data(context.Background(), first.InstanceID, "last_notify")
	if err != nil {
		t.Fatal("normal return did not save last_notify", err)
	}
	statisticRouterEvent(t, a, "owned-dedup-after-success")
	third := statisticRouterExecute(t, a, statisticRouterClaim(t, a, "SiteStatistic"))
	skipped := statisticRouterReceipt(t, a, third)
	if skipped.Notified || skipped.RouteID != "" {
		t.Fatal("successful fingerprint did not suppress duplicate", skipped)
	}
	after, err := a.pluginStore.Data(context.Background(), first.InstanceID, "last_notify")
	if err != nil || after.Revision != before.Revision || string(after.Value) != string(before.Value) {
		t.Fatal("suppressed event mutated successful fingerprint", err)
	}
	if sends.Load() != 1 || statisticRouterCount(t, a, "plugin_notice_routes") != 2 || statisticRouterCount(t, a, "plugin_notice_publications") != 1 || statisticRouterCount(t, a, "plugin_message_queue") != 1 {
		t.Fatal("source exception retry/dedup counts", sends.Load())
	}
}

func TestSiteStatisticRouterPendingFingerprintSuppressesDuplicateAcceptance(t *testing.T) {
	a := newTestApp(t)
	statisticRouterSnapshot(t, a)
	statisticRouterInstall(t, a, "SiteStatistic")
	statisticRouterEvent(t, a, "owned-pending-first")
	first := statisticRouterClaim(t, a, "SiteStatistic")
	snapshot, err := a.siteStatisticsSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	selected, err := statistic.Select(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	payload := statistic.BuildNotification(selected, "all", snapshot.Now.Format("2006-01-02"))
	if payload == nil {
		t.Fatal("synthetic snapshot produced no notice")
	}
	raw, err := a.commitSiteStatisticNotice(context.Background(), first, payload, "all", snapshot.Now)
	var receipt siteStatisticNoticeReceipt
	if err != nil || json.Unmarshal(raw, &receipt) != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.Data(context.Background(), first.InstanceID, "last_notify"); !missingPluginValue(err) {
		t.Fatal("pending acceptance prematurely saved completed dedup", err)
	}
	var sends atomic.Int32
	a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
	statisticRouterEvent(t, a, "owned-pending-second")
	second := statisticRouterExecute(t, a, statisticRouterClaim(t, a, "SiteStatistic"))
	skipped := statisticRouterReceipt(t, a, second)
	if skipped.Notified || skipped.RouteID != "" {
		t.Fatal("pending fingerprint accepted duplicate route", skipped)
	}
	if sends.Load() != 0 || statisticRouterCount(t, a, "plugin_notice_routes") != 1 {
		t.Fatal("pending original gained duplicate effects", sends.Load())
	}
	if err = a.runNoticeRoute(context.Background(), receipt.RouteID); err != nil {
		t.Fatal(err)
	}
	if sends.Load() != 1 {
		t.Fatal("original accepted route did not send exactly once", sends.Load())
	}
	if _, err = a.pluginStore.Data(context.Background(), first.InstanceID, "last_notify"); err != nil {
		t.Fatal("normal return failed to save completed fingerprint", err)
	}
}

func TestSiteStatisticRouterFailurePreservesPreviousSuccessfulFingerprint(t *testing.T) {
	a := newTestApp(t)
	statisticRouterSnapshot(t, a)
	statisticRouterInstall(t, a, "SiteStatistic")
	var sends atomic.Int32
	a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
	statisticRouterEvent(t, a, "owned-success-before-failure")
	statisticRouterExecute(t, a, statisticRouterClaim(t, a, "SiteStatistic"))
	before, err := a.pluginStore.Data(context.Background(), "SiteStatistic", "last_notify")
	if err != nil {
		t.Fatal(err)
	}
	var snapshot siteSnapshot
	if err = a.Store.Get(context.Background(), "site_stats_latest", "router-owned-site", &snapshot); err != nil {
		t.Fatal(err)
	}
	*snapshot.Stats.Uploaded += 1 << 30
	if err = a.Store.Put(context.Background(), "site_stats_latest", "router-owned-site", snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_later_route_failure BEFORE INSERT ON plugin_notice_publications BEGIN SELECT RAISE(ABORT,'owned later failure'); END`); err != nil {
		t.Fatal(err)
	}
	statisticRouterEvent(t, a, "owned-changed-snapshot-fails")
	run := statisticRouterClaim(t, a, "SiteStatistic")
	a.executePluginRun(context.Background(), run)
	failed, err := a.pluginStore.Run(context.Background(), run.ID)
	if err != nil || failed.State != "failed" {
		t.Fatal("later SDK route did not fail", failed.State, err)
	}
	after, err := a.pluginStore.Data(context.Background(), "SiteStatistic", "last_notify")
	if err != nil || after.Revision != before.Revision || string(after.Value) != string(before.Value) {
		t.Fatal("later route failure overwrote prior successful dedup", err)
	}
	if sends.Load() != 1 {
		t.Fatal("failed later route sent", sends.Load())
	}
}

func TestSiteStatisticRouterOldCompletionPreservesNewDedupValue(t *testing.T) {
	for _, transition := range []string{"new_generation", "data_revision"} {
		t.Run(transition, func(t *testing.T) {
			a := newTestApp(t)
			statisticRouterSnapshot(t, a)
			statisticRouterInstall(t, a, "SiteStatistic")
			statisticRouterEvent(t, a, "owned-old-pending-completion")
			run := statisticRouterClaim(t, a, "SiteStatistic")
			raw, err := a.commitSiteStatisticNotice(context.Background(), run, &statistic.Notification{Title: "合成统计", Text: "old pending", Fingerprint: strings.Repeat("9", 64)}, "all", time.Now().In(shanghai()))
			var receipt siteStatisticNoticeReceipt
			if err != nil || json.Unmarshal(raw, &receipt) != nil {
				t.Fatal(err)
			}
			if transition == "new_generation" {
				if err = a.pluginStore.Reset(context.Background(), run.InstanceID); err != nil {
					t.Fatal(err)
				}
				if _, _, err = a.pluginStore.SaveConfigAndQueue(context.Background(), run.InstanceID, 0, builtin.SiteStatisticConfiguration{Enabled: true, NotifyType: "all"}); err != nil {
					t.Fatal(err)
				}
				reload, err := a.pluginStore.claimRunForInstance(context.Background(), run.InstanceID)
				if err != nil {
					t.Fatal(err)
				}
				statisticRouterExecute(t, a, reload)
			}
			before, err := a.pluginStore.SaveData(context.Background(), run.InstanceID, "last_notify", 0, map[string]string{"date": "2026-10-21", "type": "all", "fingerprint": strings.Repeat("8", 64), "time": "12:00:00"})
			if err != nil {
				t.Fatal(err)
			}
			var sends atomic.Int32
			a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
			if err = a.runNoticeRoute(context.Background(), receipt.RouteID); err != nil {
				t.Fatal("old accepted route completion", err)
			}
			after, err := a.pluginStore.Data(context.Background(), run.InstanceID, "last_notify")
			if err != nil || before.Revision != after.Revision || string(before.Value) != string(after.Value) {
				t.Fatal("old route overwrote newer dedup state", transition, err)
			}
			if sends.Load() != 1 {
				t.Fatal("old source route attempt count", sends.Load())
			}
		})
	}
}
