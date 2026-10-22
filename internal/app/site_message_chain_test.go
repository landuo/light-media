package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/notice"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
	"light-media/internal/sites"
)

// Producer expectations are the pinned SiteChain methods, not a guessed
// notification UI. These are synthetic DTOs, separate from HTTP parser evidence.
func TestSiteMessageProducerSourceOrderAndMetadata(t *testing.T) {
	level := "Power User"
	snapshot := siteSnapshot{SourceData: &sites.SourceUserData{MessageUnread: 2, Ratio: .5, UserLevel: &level, Messages: [][]any{{"heading", "date", "body", "owned-source", "ignored"}, {nil, nil, nil}}}}
	steps := siteMessageBusinesses("合成站", "https://owned.invalid/inbox", snapshot)
	if len(steps) != 3 {
		t.Fatal("details must precede the single ratio warning", len(steps))
	}
	want := []notice.Object{
		{"mtype": "站点", "source": "owned-source", "title": "【站点 合成站 消息】", "text": "时间：date\n标题：heading\n内容：\nbody", "link": "https://owned.invalid/inbox"},
		{"mtype": "站点", "title": "【站点 合成站 消息】", "text": "时间：None\n标题：None\n内容：\nNone", "link": "https://owned.invalid/inbox"},
		{"mtype": "站点", "title": "【站点分享率低预警】", "text": "站点 合成站 分享率 0.5，请注意！"},
	}
	for i, step := range steps {
		if step.State != "pending" || len(step.Business) != 0 || step.Failure != "" {
			t.Fatal("plan eagerly constructed a later notification", i, step)
		}
		business, err := siteMessageBusinessAt("合成站", "https://owned.invalid/inbox", snapshot, step)
		if err != nil {
			t.Fatal("ordinal construction failed", i, err)
		}
		var got sourceBusinessNotice
		if json.Unmarshal(business, &got) != nil || !got.AbsentContext {
			t.Fatal("producer lost the source's absent template context", i)
		}
		for key, value := range want[i] {
			if !reflect.DeepEqual(got.Envelope[key], value) {
				t.Fatal("source envelope differs", i, key, got.Envelope[key], value)
			}
		}
		if i > 0 && got.Envelope["source"] != nil {
			t.Fatal("producer invented a deduplication source", i)
		}
		if i == 2 && got.Envelope["link"] != nil {
			t.Fatal("ratio warning invented a link")
		}
	}
}

func TestSiteMessageProducerSummaryTruthinessAndOriginalVIPRegex(t *testing.T) {
	for _, test := range []struct {
		name, level string
		unread      int64
		ratio       float64
		want        int
	}{
		{"zero unread", "", 0, 0, 0},
		{"positive unread", "", 3, 0, 1},
		{"negative unread is truthy", "", -1, 0, 1},
		{"VI is exempt in original regex", "VI", 0, .5, 0},
		{"Unicode capital dotted I", "Vİ", 0, .5, 0},
		{"Unicode lowercase dotless I", "Vı", 0, .5, 0},
		{"VIP lower case", "vip", 0, .5, 0},
		{"embedded VIP", "privileged VIP user", 0, .5, 0},
		{"Chinese exemption", "贵宾", 0, .5, 0},
		{"ordinary ratio warning", "Power User", 0, .5, 1},
		{"one is not low", "", 0, 1, 0},
		{"negative ratio is truthy and low", "", 0, -1, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := siteSnapshot{SourceData: &sites.SourceUserData{MessageUnread: test.unread, Ratio: test.ratio, UserLevel: &test.level}}
			steps := siteMessageBusinesses("owned", "https://owned.invalid/", snapshot)
			if len(steps) != test.want {
				t.Fatal("source branching changed", len(steps), test.want)
			}
			if test.unread != 0 {
				var got sourceBusinessNotice
				if len(steps) != 1 {
					t.Fatal("summary count", len(steps))
				}
				business, err := siteMessageBusinessAt("owned", "https://owned.invalid/", snapshot, steps[0])
				if err != nil || json.Unmarshal(business, &got) != nil {
					t.Fatal("summary absent")
				}
				if got.Envelope["title"] != fmt.Sprintf("站点 owned 收到 %d 条新消息，请登陆查看", test.unread) || got.Envelope["link"] != "https://owned.invalid/" {
					t.Fatal("summary does not preserve title and site URL", got.Envelope)
				}
			}
		})
	}
	// Rows alone do not bypass the unread guard.
	if got := siteMessageBusinesses("owned", "url", siteSnapshot{SourceData: &sites.SourceUserData{Messages: [][]any{{"head", "date", "content"}}}}); len(got) != 0 {
		t.Fatal("zero unread emitted detail notices")
	}
	if got := siteMessageBusinesses("owned", "url", siteSnapshot{}); len(got) != 0 {
		t.Fatal("None DTO emitted notices")
	}
}

// Every request is served by this owned loopback fixture. The response bodies
// are existing source-parser fixtures, not real accounts or live site traffic.
func siteMessageHTTPFixture(t *testing.T, a *App, duplicate bool, lowRatio ...bool) *atomic.Int32 {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/plugins/site-nexus-account-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name    string
			Replies map[string][]struct {
				Status int
				Body   string
			}
		}
	}
	if json.Unmarshal(raw, &fixture) != nil {
		t.Fatal("invalid owned fixture")
	}
	name := "messages-box-order-is-unspecified"
	if duplicate {
		name = "message-duplicates-and-null-content"
	}
	bodies := map[string]string{}
	canonical := func(s string) string {
		u, e := url.Parse(s)
		if e != nil {
			t.Fatal(e)
		}
		return u.Path + "?" + u.Query().Encode()
	}
	for _, item := range fixture.Cases {
		if item.Name != name {
			continue
		}
		for path, replies := range item.Replies {
			if len(replies) != 0 {
				bodies[canonical(path)] = replies[0].Body
			}
		}
	}
	if len(bodies) == 0 {
		t.Fatal("owned message fixture absent")
	}
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, ok := bodies[canonical(r.URL.RequestURI())]
		if !ok {
			t.Errorf("unexpected owned fixture request: %s", r.URL.RequestURI())
			http.Error(w, "unexpected path", 500)
			return
		}
		if len(lowRatio) > 0 && lowRatio[0] {
			body = strings.ReplaceAll(body, "上传量：2 GB 下载量：1 GB 分享率：2.00", "上传量：1 GB 下载量：2 GB 分享率：0.50")
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	c := a.Settings()
	c.Sites = []SiteConfig{{Config: sites.Config{ID: "known", Kind: "jiayuan", Name: "合成站点", BaseURL: server.URL, Cookie: "synthetic-site-cookie"}, SiteOptions: SiteOptions{Domain: "known.example"}, Enabled: true}}
	read := true
	c.ReadSiteMessages = &read
	if err := a.saveSettings(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return calls
}

func siteMessageHistory(t *testing.T, a *App) []Notification {
	t.Helper()
	rows, err := a.Store.DB.Query(`SELECT payload FROM records WHERE kind='notification' ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []Notification
	for rows.Next() {
		var raw []byte
		var n Notification
		if err = rows.Scan(&raw); err != nil || json.Unmarshal(raw, &n) != nil {
			t.Fatal("bad history", err)
		}
		result = append(result, n)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSiteMessageActualAPIRepeatedNoSourceAndColdNoRecollection(t *testing.T) {
	a, _, _, _ := pluginHTTPFixture(t)
	calls := siteMessageHTTPFixture(t, a, true)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		run := enqueueSyntheticPluginHTTP(t, a)
		a.executePluginRun(ctx, run)
		finished, err := a.pluginStore.Run(ctx, run.ID)
		if err != nil || finished.State != "succeeded" {
			t.Fatal("actual API chain did not return normally", finished.State, finished.ErrorCode, err)
		}
		if _, err = a.pluginStore.siteStatisticReceipt(ctx, run); err != nil {
			t.Fatal("normal API return missing", err)
		}
		if got := siteMessageHistory(t, a); len(got) != 2*(i+1) {
			t.Fatal("source-less rows were deduplicated or repeated", len(got))
		}
	}
	beforeCalls := calls.Load()
	before := siteMessageHistory(t, a)
	for _, n := range before {
		if n.MType != "站点" || n.Title != "【站点 合成站点 消息】" || n.Message != "时间：None\n标题：title\n内容：\nNone" {
			t.Fatal("actual Router history differs from source", n)
		}
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if err = cold.resumePluginNoticeRoutes(ctx, 200); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != beforeCalls || !reflect.DeepEqual(siteMessageHistory(t, cold), before) {
		t.Fatal("cold recovery re-collected, marked read, or duplicated history")
	}
}

func siteMessageClaimBatch(t *testing.T, a *App) Job {
	t.Helper()
	ctx := context.Background()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	job, err := enqueueSiteRefreshTx(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	claimed, err := a.claim(ctx)
	if err != nil || claimed.ID != job.ID {
		t.Fatal("actual batch not claimed", claimed.ID, job.ID, err)
	}
	return claimed
}

func TestSiteMessageActualBatchTwoStatisticClonesCollectOnlyOnce(t *testing.T) {
	a := newTestApp(t)
	calls := siteMessageHTTPFixture(t, a, false)
	statisticRouterInstall(t, a, "SiteStatistic")
	statisticRouterInstall(t, a, "SiteStatistic_owned_clone")
	ctx := context.Background()
	job := siteMessageClaimBatch(t, a)
	if err := a.runSiteRefresh(ctx, job); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	if before != 6 {
		t.Fatal("core collection request sequence changed", before)
	}
	if histories := siteMessageHistory(t, a); len(histories) != 1 {
		t.Fatal("core producer should emit once, not once per clone", histories)
	}
	if err := a.dispatchPluginEvents(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, instance := range []string{"SiteStatistic", "SiteStatistic_owned_clone"} {
		// Zero-delay subscriptions create one invocation per event. Both the
		// collected-site fact and '*' batch completion are real inputs. Equal
		// created_at timestamps do not promise their random run-ID ordering.
		seen := map[string]int{}
		for ordinal := 0; ordinal < 2; ordinal++ {
			run := statisticRouterClaim(t, a, instance)
			var payload []byte
			if err := a.Store.DB.QueryRow(`SELECT e.payload FROM plugin_event_batches b JOIN plugin_event_deliveries d ON d.batch_id=b.id JOIN domain_events e ON e.event_id=d.event_id WHERE b.run_id=? AND b.instance_id=?`, run.ID, instance).Scan(&payload); err != nil {
				t.Fatal(err)
			}
			var event DomainEvent
			if json.Unmarshal(payload, &event) != nil || event.Type != "site.refreshed" {
				t.Fatal("wrong event invocation", string(payload))
			}
			seen[event.SiteID]++
			statisticRouterExecute(t, a, run)
		}
		if len(seen) != 2 || seen["known"] != 1 || seen["*"] != 1 {
			t.Fatal("clone did not consume both original events", instance, seen)
		}
		var pending int
		if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id=? AND action='event' AND state='pending'`, instance).Scan(&pending); err != nil || pending != 0 {
			t.Fatal("clone event left unexecuted", instance, pending, err)
		}
	}
	if calls.Load() != before {
		t.Fatal("statistics clone recollected site userdata")
	}
	var coreDetails, cloneSummaries int
	for _, n := range siteMessageHistory(t, a) {
		if n.Title == "【站点 合成站点 消息】" {
			coreDetails++
		} else {
			cloneSummaries++
		}
	}
	if coreDetails != 1 || cloneSummaries != 2 {
		t.Fatal("core and per-clone event notices were conflated", coreDetails, cloneSummaries)
	}
}

func TestSiteMessagePostUnknownStopsBatchButPreservesCollectedFact(t *testing.T) {
	a := newTestApp(t)
	calls := siteMessageHTTPFixture(t, a, true, true)
	var later atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		later.Add(1)
		fmt.Fprint(w, `<a href="logout.php">out</a>`)
	}))
	defer server.Close()
	c := a.Settings()
	second := c.Sites[0]
	second.ID, second.Domain, second.BaseURL = "later", "later.example", server.URL
	c.Sites = append(c.Sites, second)
	if err := a.saveSettings(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_site_message_failure BEFORE INSERT ON plugin_notice_publications BEGIN SELECT RAISE(ABORT,'owned synchronous message exception'); END`); err != nil {
		t.Fatal(err)
	}
	job := siteMessageClaimBatch(t, a)
	err := a.runSiteRefresh(context.Background(), job)
	var post *siteMessagePostError
	if !errors.As(err, &post) {
		t.Fatal("post unknown outcome was swallowed by batch", err)
	}
	if calls.Load() != 7 || later.Load() != 0 {
		t.Fatal("unknown outcome repeated collection or continued to next site", calls.Load(), later.Load())
	}
	var snapshot siteSnapshot
	if err = a.Store.Get(context.Background(), "site_connection", "known", &snapshot); err != nil || snapshot.SourceData == nil {
		t.Fatal("post exception rolled back collected DTO", err)
	}
	var single, all int
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='site.refreshed' AND json_extract(payload,'$.site_id')='known'`).Scan(&single); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='site.refreshed' AND json_extract(payload,'$.site_id')='*'`).Scan(&all); err != nil {
		t.Fatal(err)
	}
	if single != 1 || all != 0 {
		t.Fatal("exception changed source event ordering", single, all)
	}
	for _, n := range siteMessageHistory(t, a) {
		if strings.Contains(n.Title, "分享率") {
			t.Fatal("ratio notice ran before the unknown local step was reconciled")
		}
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_site_message_failure`); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	_ = a.resumePluginNoticeRoutes(context.Background(), 200)
	if calls.Load() != before || later.Load() != 0 {
		t.Fatal("recovery recollected a failed source chain")
	}
}

func TestSiteMessageActualAPIPostReturnAndUnknownSenderNeverResent(t *testing.T) {
	for _, mode := range []string{"false_return", "unknown_sender", "business_accept", "route_step", "route_finish", "queue_intent", "queue_finish"} {
		t.Run(mode, func(t *testing.T) {
			a, _, _, _ := pluginHTTPFixture(t)
			calls := siteMessageHTTPFixture(t, a, false)
			var sends atomic.Int32
			var fault atomic.Bool
			a.pluginStore.notices.afterCommit = func(stage string) error {
				if stage == mode && fault.CompareAndSwap(false, true) {
					return errors.New("owned committed response lost")
				}
				return nil
			}
			a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) {
				sends.Add(1)
				if mode == "unknown_sender" {
					return 1, seedhost.ErrUnknown
				}
				if mode == "false_return" {
					return 0, nil
				}
				return 1, nil
			}
			run := enqueueSyntheticPluginHTTP(t, a)
			a.executePluginRun(context.Background(), run)
			finished, err := a.pluginStore.Run(context.Background(), run.ID)
			if err != nil || finished.State != "succeeded" {
				t.Fatal("normal source return changed into failure", finished.State, finished.ErrorCode, err)
			}
			if mode != "false_return" && mode != "unknown_sender" && !fault.Load() {
				t.Fatal("commit boundary was not exercised", mode)
			}
			if calls.Load() != 6 || sends.Load() != 1 || len(siteMessageHistory(t, a)) != 1 {
				t.Fatal("source sequence duplicated", calls.Load(), sends.Load())
			}
			ids := noticeRouteTestIDs(t, a, "plugin_message_queue")
			if len(ids) != 1 {
				t.Fatal("expected one genuine queue receipt", ids)
			}
			q, err := a.pluginStore.readPluginMessage(context.Background(), a.Store.DB, ids[0])
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unknown_sender" && q.State != "unknown" {
				t.Fatal("unknown send was treated as known", q.State)
			}
			dir := a.Dir
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			cold, err := New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer cold.Close()
			cold.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
			if err = cold.resumePluginNoticeRoutes(context.Background(), 200); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 6 || sends.Load() != 1 || len(siteMessageHistory(t, cold)) != 1 {
				t.Fatal("cold recovery repeated known or unknown effects", calls.Load(), sends.Load())
			}
		})
	}
}

func TestSiteMessageCollectionFactCommitFailureBlocksRecollection(t *testing.T) {
	a, _, _, _ := pluginHTTPFixture(t)
	calls := siteMessageHTTPFixture(t, a, false)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_collection_fact_failure BEFORE UPDATE ON records WHEN OLD.kind='site_message_collection' BEGIN SELECT RAISE(ABORT,'owned collection fact rollback'); END`); err != nil {
		t.Fatal(err)
	}
	run := enqueueSyntheticPluginHTTP(t, a)
	a.executePluginRun(context.Background(), run)
	finished, err := a.pluginStore.Run(context.Background(), run.ID)
	if err != nil || finished.State == "succeeded" {
		t.Fatal("failed fact transaction reported success", finished.State, err)
	}
	if calls.Load() != 6 {
		t.Fatal("fixture never completed source requests", calls.Load())
	}
	for _, kind := range []string{"site_connection", "site_stats_latest", "site_stats_history", "notification"} {
		var count int
		if err = a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind=?`, kind).Scan(&count); err != nil || count != 0 {
			t.Fatal("fact transaction leaked partial state", kind, count, err)
		}
	}
	var events, admitted int
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='site.refreshed'`).Scan(&events); err != nil || events != 0 {
		t.Fatal("rolled-back fact emitted event", events, err)
	}
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='site_message_collection'`).Scan(&admitted); err != nil || admitted != 1 {
		t.Fatal("request admission was not durable", admitted, err)
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_collection_fact_failure`); err != nil {
		t.Fatal(err)
	}
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	_ = cold.resumeSiteMessageCollections(context.Background())
	if calls.Load() != 6 || len(siteMessageHistory(t, cold)) != 0 {
		t.Fatal("unknown collection was re-requested or its messages invented")
	}
}

func TestSiteMessageFinalAPIReceiptFailurePreservesFactAndNeverRefetches(t *testing.T) {
	a, _, _, _ := pluginHTTPFixture(t)
	calls := siteMessageHTTPFixture(t, a, false)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_final_api_failure BEFORE INSERT ON plugin_site_statistic_operations BEGIN SELECT RAISE(ABORT,'owned normal-return receipt rollback'); END`); err != nil {
		t.Fatal(err)
	}
	run := enqueueSyntheticPluginHTTP(t, a)
	a.executePluginRun(context.Background(), run)
	finished, err := a.pluginStore.Run(context.Background(), run.ID)
	if err != nil || finished.State == "succeeded" {
		t.Fatal("missing API completion reported success", finished.State, err)
	}
	var snapshot siteSnapshot
	if err = a.Store.Get(context.Background(), "site_connection", "known", &snapshot); err != nil || snapshot.SourceData == nil {
		t.Fatal("final return failure rolled back the earlier source save", err)
	}
	var events int
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='site.refreshed'`).Scan(&events); err != nil || events != 1 {
		t.Fatal("final receipt failure changed the earlier source event", events, err)
	}
	if calls.Load() != 6 || len(siteMessageHistory(t, a)) != 1 {
		t.Fatal("final receipt failure changed completed message side effect")
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_final_api_failure`); err != nil {
		t.Fatal(err)
	}
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if err = cold.resumePluginNoticeRoutes(context.Background(), 200); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 6 || len(siteMessageHistory(t, cold)) != 1 {
		t.Fatal("cold final-receipt reconciliation re-collected or resent")
	}
	recovered, err := cold.pluginStore.Run(context.Background(), run.ID)
	if err != nil || recovered.State != "succeeded" {
		t.Fatal("cold local receipt completion did not finish original API run", recovered.State, recovered.ErrorCode, err)
	}
	if _, err = cold.pluginStore.siteStatisticReceipt(context.Background(), run); err != nil {
		t.Fatal("cold normal-return receipt missing", err)
	}
}

// This fixture supplies a frozen synthetic module DTO, not HTTP parser evidence.
// The job ownership, sealed collection, Router, history SQL and completion proof
// are real. It exercises optional source metadata no native parser currently emits.
func siteMessageSeedCollectedDTO(t *testing.T, a *App, job Job, siteID string, dto *sites.SourceUserData) siteMessageCollection {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("frozen source DTO unexpectedly recollected over HTTP: %s", r.URL)
		http.Error(w, "must not recollect", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	site := SiteConfig{Config: sites.Config{ID: siteID, Kind: "jiayuan", Name: "合成来源站", BaseURL: server.URL, Cookie: "synthetic-only"}, SiteOptions: SiteOptions{Domain: siteID + ".invalid"}, Enabled: true}
	settings := a.Settings()
	settings.Sites = append(settings.Sites, site)
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	origin := siteCollectionOrigin{Kind: "job", ID: job.ID, Generation: int64(job.Generation)}
	r := siteMessageCollection{Version: 1, Stage: "collected", Origin: origin, Name: site.Name, URL: site.BaseURL, PolicyVersion: "site-chain/1528176/v1:", ReadMessages: true, SiteEnabled: site.Enabled, EventKey: job.ID + ":" + siteID, Snapshot: siteSnapshot{SiteID: siteID, ConfigKey: a.managedSiteKey(site), Name: site.Name, Domain: site.Domain, SourceOutcome: "data", SourceData: dto}}
	r.ID = a.pluginStore.noticeID("site-collection", origin.Kind, origin.ID, siteID)
	r.InputSHA = siteCollectionInputSHA(r)
	if err := freezeSiteCollectionRows(&r); err != nil {
		t.Fatal(err)
	}
	r.SnapshotSHA = siteCollectionSnapshotSHA(r)
	r.Steps = siteMessageBusinesses(r.Name, r.URL, r.Snapshot)
	tx, err := a.Store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = recordDomainEventTx(context.Background(), tx, DomainEvent{EventID: domainEventID("site.refreshed", r.EventKey), Type: "site.refreshed", SiteID: siteID}); err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.saveSiteMessageCollection(context.Background(), tx, r); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSiteMessageGlobalActualHistorySourceDedupAndDeletion(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	job := siteMessageClaimBatch(t, a)
	first := siteMessageSeedCollectedDTO(t, a, job, "first", &sites.SourceUserData{MessageUnread: 4, Messages: [][]any{{"one", "date", "body", "shared/source:42"}, {"two", "date", "body", "shared/source:42"}, {"same", "date", "body"}, {"same", "date", "body"}}})
	finished, err := a.finishSiteMessageCollection(ctx, first.ID)
	if err != nil || finished.Stage != "complete" {
		t.Fatal("real Router failed synthetic frozen DTO", finished.Stage, err)
	}
	if finished.Steps[0].State != "complete" || finished.Steps[1].State != "skipped" || finished.Steps[2].State != "complete" || finished.Steps[3].State != "complete" {
		t.Fatal("source existence/no-source semantics changed", finished.Steps)
	}
	history := siteMessageHistory(t, a)
	if len(history) != 3 || history[0].Source != "shared/source:42" || history[1].Source != "" || history[2].Source != "" {
		t.Fatal("source was not saved by real Router history", history)
	}
	second := siteMessageSeedCollectedDTO(t, a, job, "different-site", &sites.SourceUserData{MessageUnread: 1, Messages: [][]any{{"another site", "date", "body", "shared/source:42"}}})
	finished, err = a.finishSiteMessageCollection(ctx, second.ID)
	if err != nil || finished.Stage != "complete" || finished.Steps[0].State != "skipped" || len(siteMessageHistory(t, a)) != 3 {
		t.Fatal("source dedup was incorrectly scoped by site", finished.Stage, err)
	}
	// The private test DB is the only deletion target. Real history deletion
	// must remove dedup evidence; route acceptance is not a permanent reservation.
	if _, err = a.Store.DB.Exec(`DELETE FROM records WHERE kind='notification' AND id=?`, history[0].ID); err != nil {
		t.Fatal(err)
	}
	third := siteMessageSeedCollectedDTO(t, a, job, "after-deletion", &sites.SourceUserData{MessageUnread: 1, Messages: [][]any{{"again", "date", "body", "shared/source:42"}}})
	finished, err = a.finishSiteMessageCollection(ctx, third.ID)
	if err != nil || finished.Stage != "complete" || finished.Steps[0].State != "complete" || len(siteMessageHistory(t, a)) != 3 {
		t.Fatal("deleted history left a permanent source reservation", finished.Stage, err)
	}
}

func TestSiteMessageMalformedLaterRowRetainsPriorPostAndStopsRatio(t *testing.T) {
	a := newTestApp(t)
	job := siteMessageClaimBatch(t, a)
	r := siteMessageSeedCollectedDTO(t, a, job, "short-row", &sites.SourceUserData{MessageUnread: 3, Ratio: .5, Messages: [][]any{{"first", "date", "body"}, {"short"}, {"never", "date", "body"}}})
	finished, err := a.finishSiteMessageCollection(context.Background(), r.ID)
	if err == nil || finished.Stage != "failed" || finished.Failure != "ValueError" || len(finished.Steps) != 4 || finished.Steps[0].State != "complete" || finished.Steps[1].State != "failed" {
		t.Fatal("tuple unpacking failure changed source ordering", finished, err)
	}
	for i, step := range finished.Steps[2:] {
		if step.State != "pending" || len(step.Business) != 0 || step.Failure != "" {
			t.Fatal("later ordinal was constructed after tuple error", i+2, step)
		}
	}
	history := siteMessageHistory(t, a)
	if len(history) != 1 || history[0].Message != "时间：date\n标题：first\n内容：\nbody" {
		t.Fatal("later error rolled back earlier post or continued sequence", history)
	}
	if again, err := a.finishSiteMessageCollection(context.Background(), r.ID); err == nil || again.Stage != "failed" || len(siteMessageHistory(t, a)) != 1 {
		t.Fatal("failed sequence retried its known exception", again.Stage, err)
	}
}

func TestSiteMessageReclaimedJobUsesCurrentLeaseWithoutRecollecting(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	original := siteMessageClaimBatch(t, a)
	r := siteMessageSeedCollectedDTO(t, a, original, "reclaimed", &sites.SourceUserData{MessageUnread: 1, Messages: [][]any{{"frozen", "date", "body"}}})
	// Reclaim through the real claim path, which advances the lease generation.
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='retry_wait',run_at=0 WHERE id=? AND generation=?`, original.ID, original.Generation); err != nil {
		t.Fatal(err)
	}
	current, err := a.claim(ctx)
	if err != nil || current.ID != original.ID || current.Generation <= original.Generation {
		t.Fatal("job was not genuinely reclaimed", current, err)
	}
	stale := context.WithValue(ctx, siteCollectionLeaseKey{}, siteCollectionLease{ID: original.ID, Generation: int64(original.Generation)})
	if _, err = a.finishSiteMessageCollection(stale, r.ID); err == nil || len(siteMessageHistory(t, a)) != 0 {
		t.Fatal("stale caller adopted another worker's generation", err)
	}
	finished, err := a.finishSiteMessageCollection(ctx, r.ID)
	if err != nil || finished.Stage != "complete" || finished.Origin != r.Origin || finished.Origin.Generation != int64(original.Generation) {
		t.Fatal("recovery did not retain the original collection identity", finished, err)
	}
	if history := siteMessageHistory(t, a); len(history) != 1 || history[0].Message != "时间：date\n标题：frozen\n内容：\nbody" {
		t.Fatal("reclaimed job did not use original frozen DTO", history)
	}
}

// These cases freeze synthetic raw module DTOs. They exercise the real core job,
// sequential Router and history, but do not claim that a native HTTP parser
// currently emits scalar/dictionary message rows.
func TestSiteMessageRawLaterScalarFailsOnlyAfterFirstRealPost(t *testing.T) {
	for _, rawRow := range []string{"null", "42", "true"} {
		t.Run(rawRow, func(t *testing.T) {
			a := newTestApp(t)
			job := siteMessageClaimBatch(t, a)
			var dto sites.SourceUserData
			raw := `{"message_unread":3,"ratio":0.5,"message_unread_contents":[["first","date","body"],` + rawRow + `,["never","date","body"]]}`
			if err := json.Unmarshal([]byte(raw), &dto); err != nil {
				t.Fatal("DTO eagerly rejected a later raw row", err)
			}
			r := siteMessageSeedCollectedDTO(t, a, job, "raw-scalar", &dto)
			if len(r.Steps) != 4 {
				t.Fatal("raw ordinal plan lost later rows or ratio", len(r.Steps))
			}
			for i, step := range r.Steps {
				if step.State != "pending" || len(step.Business) != 0 || step.Failure != "" {
					t.Fatal("admission eagerly touched raw row", i, step)
				}
			}
			finished, err := a.finishSiteMessageCollection(context.Background(), r.ID)
			if err == nil || finished.Stage != "failed" || finished.Failure != "TypeError" || finished.Steps[0].State != "complete" || finished.Steps[1].State != "failed" {
				t.Fatal("scalar unpacking did not fail at its own ordinal", finished, err)
			}
			for i, step := range finished.Steps[2:] {
				if step.State != "pending" || len(step.Business) != 0 || step.Failure != "" {
					t.Fatal("later notification built after raw TypeError", i+2, step)
				}
			}
			history := siteMessageHistory(t, a)
			if len(history) != 1 || history[0].Message != "时间：date\n标题：first\n内容：\nbody" {
				t.Fatal("later scalar error erased or overtook first real post", history)
			}
			if again, err := a.finishSiteMessageCollection(context.Background(), r.ID); err == nil || again.Stage != "failed" || len(siteMessageHistory(t, a)) != 1 {
				t.Fatal("known raw-row failure was retried", again.Stage, err)
			}
		})
	}
}

func TestSiteMessageRawStringAndOrderedDictionaryFollowPythonUnpack(t *testing.T) {
	a := newTestApp(t)
	job := siteMessageClaimBatch(t, a)
	var dto sites.SourceUserData
	raw := `{"message_unread":2,"message_unread_contents":["题日文源外",{"z-head":10,"a-date":20,"m-body":30,"dict-source":40,"ignored":50}]}`
	if err := json.Unmarshal([]byte(raw), &dto); err != nil {
		t.Fatal("DTO rejected iterable raw rows", err)
	}
	r := siteMessageSeedCollectedDTO(t, a, job, "iterable-rows", &dto)
	finished, err := a.finishSiteMessageCollection(context.Background(), r.ID)
	if err != nil || finished.Stage != "complete" {
		t.Fatal("JSON iterable rows did not follow source unpacking", finished.Stage, err)
	}
	history := siteMessageHistory(t, a)
	if len(history) != 2 {
		t.Fatal("iterable row notices missing", history)
	}
	if history[0].Message != "时间：日\n标题：题\n内容：\n文" || history[0].Source != "源" {
		t.Fatal("string unpack used UTF-8 bytes or lost fourth character", history[0])
	}
	if history[1].Message != "时间：a-date\n标题：z-head\n内容：\nm-body" || history[1].Source != "dict-source" {
		t.Fatal("dictionary unpack sorted keys or used values", history[1])
	}
}

func TestSiteMessageLaterNotificationValidationPreservesFirstPost(t *testing.T) {
	a := newTestApp(t)
	job := siteMessageClaimBatch(t, a)
	var dto sites.SourceUserData
	// [] is false in Python, so source existence is not queried. It still fails
	// Notification's string source validation at this second ordinal.
	raw := `{"message_unread":3,"ratio":0.5,"message_unread_contents":[["first","date","body"],["invalid","date","body",[]],["never","date","body"]]}`
	if err := json.Unmarshal([]byte(raw), &dto); err != nil {
		t.Fatal(err)
	}
	r := siteMessageSeedCollectedDTO(t, a, job, "invalid-notification", &dto)
	finished, err := a.finishSiteMessageCollection(context.Background(), r.ID)
	if err == nil || finished.Stage != "failed" || finished.Failure != "ValidationError" || len(finished.Steps) != 4 || finished.Steps[0].State != "complete" || finished.Steps[1].State != "failed" {
		t.Fatal("Notification construction failure lost original ordinal", finished, err)
	}
	for i, step := range finished.Steps[2:] {
		if step.State != "pending" || len(step.Business) != 0 || step.Failure != "" {
			t.Fatal("later row or ratio constructed after validation exception", i+2, step)
		}
	}
	history := siteMessageHistory(t, a)
	if len(history) != 1 || history[0].Message != "时间：date\n标题：first\n内容：\nbody" {
		t.Fatal("late Notification error changed first successful post", history)
	}
	if again, err := a.finishSiteMessageCollection(context.Background(), r.ID); err == nil || again.Stage != "failed" || len(siteMessageHistory(t, a)) != 1 {
		t.Fatal("validation failure was incorrectly resumed", again.Stage, err)
	}
}

func TestSiteStatisticAPIDataPreservesRawMessageRows(t *testing.T) {
	// API serialization evidence only, not an HTTP parser capability claim.
	rawRows := `[null,42,true,"题日文源",{"z-head":1,"a-date":2,"m-body":3},["head","date","body","source",99]]`
	var dto sites.SourceUserData
	if err := json.Unmarshal([]byte(`{"message_unread":6,"message_unread_contents":`+rawRows+`}`), &dto); err != nil {
		t.Fatal("DTO discarded scalar rows before API serialization", err)
	}
	data := siteStatisticAPIData(siteSnapshot{SourceData: &dto})
	encoded, err := json.Marshal(data["message_unread_contents"])
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if json.Unmarshal([]byte(rawRows), &want) != nil || json.Unmarshal(encoded, &got) != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("API replaced raw rows with array-only projection", string(encoded))
	}
	// The ordered dictionary is significant to later Python unpacking, even
	// though ordinary API object equality does not expose its insertion order.
	if !strings.Contains(string(encoded), `{"z-head":1,"a-date":2,"m-body":3}`) {
		t.Fatal("API DTO roundtrip reordered source dictionary keys", string(encoded))
	}
}
