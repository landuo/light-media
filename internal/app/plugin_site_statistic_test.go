package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/statistic"
	"light-media/internal/sites"
)

func statisticExecute(t *testing.T, a *App) PluginRun {
	t.Helper()
	ctx := context.Background()
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if run.InstanceID != "SiteStatistic" {
		t.Fatal("unexpected worker", run.InstanceID)
	}
	a.executePluginRun(ctx, run)
	finished, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || finished.State != "succeeded" {
		t.Fatal("statistic worker failed", finished.State, finished.ErrorCode, err)
	}
	return finished
}
func statisticSave(t *testing.T, a *App, cookie *http.Cookie, config builtin.SiteStatisticConfiguration) {
	t.Helper()
	stored, err := a.pluginStore.Config(context.Background(), "SiteStatistic")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"revision": stored.Revision, "config": config})
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/SiteStatistic/config", string(raw), cookie, "")
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
}
func statisticInstall(t *testing.T, a *App, cookie *http.Cookie) {
	t.Helper()
	r := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"SiteStatistic"}`, cookie, "")
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	statisticExecute(t, a)
}

func TestPluginSiteStatisticRealCollectionEventNoticeAndCrashReceipt(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	var requested atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested.Add(1)
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<a href="logout.php">退出</a><a href="userdetails.php?id=7">synthetic-user</a><div>上传：2 GB 下载：1 GB 总做种数：0 总做种体积：0 B</div>`)
		case "/userdetails.php":
			fmt.Fprint(w, `<a href="logout.php">退出</a>`)
		case "/getusertorrentlistajax.php":
			fmt.Fprint(w, `<table class="torrents"><tr><td>名称</td><td>大小</td><td>类型</td><td>在做种</td></tr></table>`)
		default:
			t.Error("unexpected", r.URL.Path)
		}
	}))
	defer server.Close()
	site := SiteConfig{Config: sites.Config{ID: "s1", Kind: "jiayuan", Name: "合成站点", BaseURL: server.URL, Cookie: "synthetic-never-live"}, Enabled: true}
	if _, err := a.saveManagedSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	statisticInstall(t, a, cookie)
	statisticSave(t, a, cookie, builtin.SiteStatisticConfiguration{Enabled: true, OnlyOnce: true, NotifyType: "all", DashboardType: "all"})
	configured := statisticExecute(t, a)
	stored, _ := a.pluginStore.Config(ctx, "SiteStatistic")
	var saved builtin.SiteStatisticConfiguration
	json.Unmarshal(stored.Value, &saved)
	if saved.OnlyOnce || stored.Revision != configured.ConfigRevision {
		t.Fatal("date flag not atomically cleared")
	}
	form := callTest(t, a.Handler(), "GET", "/api/plugins/SiteStatistic/form", "", cookie, "")
	if form.Code != 200 || !strings.Contains(form.Body.String(), `"notify_type":"all"`) {
		t.Fatal("saved notification lost on reopen", form.Body.String())
	}
	var due string
	if err := a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE service_id='SiteStatistic.once' AND active=1`).Scan(&due); err != nil {
		t.Fatal(err)
	}
	at, err := time.Parse(time.RFC3339Nano, due)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := a.pluginStore.enqueueDuePluginSchedules(ctx, at); err != nil || count != 1 {
		t.Fatal("once did not fire", count, err)
	}
	service := statisticExecute(t, a)
	var result struct {
		Queued bool   `json:"queued"`
		JobID  string `json:"job_id"`
	}
	json.Unmarshal(service.Result, &result)
	if !result.Queued || result.JobID == "" || requested.Load() != 0 {
		t.Fatal("slow network occupied plugin process", string(service.Result), requested.Load())
	}
	job, err := a.claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != result.JobID || job.Kind != "site_refresh" {
		t.Fatal("missing real host collection job")
	}
	if err := a.safeRun(ctx, job); err != nil {
		t.Fatal(err)
	}
	if requested.Load() != 3 {
		t.Fatal("no real account protocol request", requested.Load())
	}
	// A committed queue receipt recovers without submitting a second host job.
	a.Store.DB.Exec(`UPDATE plugin_runs SET state='running',result=NULL WHERE id=?`, service.ID)
	if err := a.pluginStore.recoverPluginRuns(ctx); err != nil {
		t.Fatal(err)
	}
	replay, err := a.pluginStore.Run(ctx, service.ID)
	if err != nil || replay.State != "succeeded" || string(replay.Result) != string(service.Result) {
		t.Fatal("submission receipt changed on recovery")
	}
	var jobs int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='site_refresh'`).Scan(&jobs)
	if jobs != 1 {
		t.Fatal("duplicate refresh job")
	}
	if err := a.dispatchPluginEvents(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	// One per-site event and one full-refresh event; only '*' emits a notice.
	firstEvent := statisticExecute(t, a)
	noticeRun := statisticExecute(t, a)
	if strings.Contains(string(firstEvent.Result), `"notified":true`) {
		noticeRun = firstEvent
	}
	var notices int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='notification'`).Scan(&notices)
	if notices != 1 {
		t.Fatal("single/full event gate", notices)
	}
	var notification Notification
	if err := a.Store.Get(ctx, "notification", "plugin_notice_"+noticeRun.ID, &notification); err != nil || !strings.Contains(notification.Message, "2.0G") || !strings.Contains(notification.Message, "合成站点") || notification.MType != "站点" {
		t.Fatal("bad actual notification", notification, err)
	}
	last, err := a.pluginStore.Data(ctx, "SiteStatistic", "last_notify")
	if err != nil || !strings.Contains(string(last.Value), `"fingerprint"`) {
		t.Fatal("persistent dedup missing", err)
	}
	a.Store.DB.Exec(`UPDATE plugin_runs SET state='running',result=NULL WHERE id=?`, noticeRun.ID)
	if err := a.pluginStore.recoverPluginRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if replay, err := a.pluginStore.Run(ctx, noticeRun.ID); err != nil || replay.State != "succeeded" || string(replay.Result) != string(noticeRun.Result) {
		t.Fatal("wrong notice recovery", err)
	}
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='notification'`).Scan(&notices)
	if notices != 1 {
		t.Fatal("crash duplicated notice")
	}
	page := callTest(t, a.Handler(), "GET", "/api/plugins/SiteStatistic/page", "", cookie, "")
	for _, field := range []string{"VApexChart", "合成站点", "synthetic-user", "总做种体积", "2.0G"} {
		if page.Code != 200 || !strings.Contains(page.Body.String(), field) {
			t.Fatal("actual worker page missing", field, page.Code, page.Body.String())
		}
	}
	if strings.Contains(page.Body.String(), site.Cookie) {
		t.Fatal("view disclosed site credentials")
	}
	dashboard := callTest(t, a.Handler(), "GET", "/api/plugins/SiteStatistic/dashboard", "", cookie, "")
	if dashboard.Code != 200 || !strings.Contains(dashboard.Body.String(), "VApexChart") {
		t.Fatal("missing original dashboard")
	}
}

func TestPluginSiteStatisticLiveThreeSecondCallbackToNotification(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="logout.php">退出</a><a href="userdetails.php?id=3">synthetic</a><div>上传：2 GB 下载：1 GB 总做种数：0 总做种体积：0 B</div>`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := a.saveManagedSite(ctx, SiteConfig{Config: sites.Config{ID: "live", Kind: "jiayuan", Name: "合成实时回调站", BaseURL: server.URL, Cookie: "synthetic-only"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	r := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"SiteStatistic"}`, cookie, "")
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	statisticSave(t, a, cookie, builtin.SiteStatisticConfiguration{Enabled: true, OnlyOnce: true, NotifyType: "all", DashboardType: "today"})
	started := time.Now()
	a.Start(ctx)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		var jobs, notices int
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind='site_refresh' AND state='succeeded'`).Scan(&jobs); err != nil {
			t.Fatal(err)
		}
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='notification'`).Scan(&notices); err != nil {
			t.Fatal(err)
		}
		if jobs == 1 && notices == 1 {
			if time.Since(started) < 3*time.Second {
				t.Fatal("date callback ran before its source delay")
			}
			stored, err := a.pluginStore.Config(ctx, "SiteStatistic")
			if err != nil {
				t.Fatal(err)
			}
			var config builtin.SiteStatisticConfiguration
			json.Unmarshal(stored.Value, &config)
			if config.OnlyOnce {
				t.Fatal("once flag replay risk")
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("live scheduler/worker/event chain did not complete")
		case <-ticker.C:
		}
	}
}

func TestSiteStatisticSnapshotBackfillActiveAndCredentialBoundary(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	one := parallelSearchSite("s1", "http://127.0.0.1:9911")
	one.Name = "历史站点"
	one.Domain = "a.example"
	two := parallelSearchSite("s2", "http://127.0.0.1:9912")
	two.Enabled = false
	c := a.Settings()
	c.Sites = []SiteConfig{one, two}
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	day := time.Now().In(shanghai())
	up := int64(2048)
	prior := int64(1024)
	snapshot := siteSnapshot{SiteID: one.ID, Name: one.Name, Domain: one.Domain, ConfigKey: a.managedSiteKey(one), Day: day.Format("2006-01-02"), Stats: &sites.UserStats{Uploaded: &up, CapturedAt: now()}}
	if err := a.Store.Put(ctx, "site_stats_latest", one.ID, snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Day = day.AddDate(0, 0, -7).Format("2006-01-02")
	snapshot.Stats.Uploaded = &prior
	a.Store.Put(ctx, "site_stats_history", one.ID+":"+snapshot.Day, snapshot)
	snapshot.SiteID = two.ID
	snapshot.ConfigKey = a.managedSiteKey(two)
	a.Store.Put(ctx, "site_stats_latest", two.ID, snapshot)
	actual, err := a.siteStatisticsSnapshot(ctx)
	if err != nil || len(actual.Latest) != 1 || len(actual.History) != 1 {
		t.Fatal("snapshot scope", actual, err)
	}
	c = a.Settings()
	c.Sites[0].Cookie = "rotated-synthetic-identity"
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	actual, err = a.siteStatisticsSnapshot(ctx)
	if err != nil || len(actual.Latest) != 0 {
		t.Fatal("old identity data reused", actual, err)
	}
}

func TestSiteStatisticNoticeAndOnceRegistrationRollback(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	statisticInstall(t, a, cookie)
	statisticSave(t, a, cookie, builtin.SiteStatisticConfiguration{Enabled: true, OnlyOnce: true, NotifyType: "all"})
	a.Store.DB.Exec(`CREATE TRIGGER fail_statistic_flag BEFORE UPDATE ON plugin_values WHEN OLD.instance_id='SiteStatistic' AND OLD.kind='config' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`)
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(ctx, run)
	finished, _ := a.pluginStore.Run(ctx, run.ID)
	if finished.State != "failed" {
		t.Fatal("failed flag reported success")
	}
	var count int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE service_id='SiteStatistic.once'`).Scan(&count)
	if count != 0 {
		t.Fatal("failed flag published date")
	}
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_event_subscriptions WHERE instance_id='SiteStatistic' AND active=1`).Scan(&count)
	if count != 0 {
		t.Fatal("failed init published active event handler")
	}
	a.Store.DB.Exec(`DROP TRIGGER fail_statistic_flag`)
	installation, _ := a.pluginStore.Installation(ctx, "SiteStatistic")
	identity := plugins.Identity{PluginID: "SiteStatistic", InstanceID: "SiteStatistic", Version: installation.Version}
	if _, err := a.pluginHostCall(ctx, installation, nil, identity, "site.statistics.refresh", json.RawMessage(`{}`)); pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
		t.Fatal("unbound collection allowed", err)
	}
	if r := callTest(t, a.Handler(), "GET", "/api/plugins/SiteStatistic/page", "", nil, ""); r.Code != 401 {
		t.Fatal("anonymous account view allowed")
	}
}

func TestPluginSiteStatisticNotificationRollbackAndEnabledGate(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	statisticInstall(t, a, cookie)
	statisticSave(t, a, cookie, builtin.SiteStatisticConfiguration{Enabled: true, NotifyType: "all"})
	statisticExecute(t, a)
	// An event receipt fails after both message and dedup writes: all three
	// must roll back together, then a retry may publish the notification once.
	run, err := a.pluginStore.EnqueueRun(ctx, "SiteStatistic", "reload", nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err = a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER fail_statistic_receipt BEFORE INSERT ON plugin_site_statistic_operations BEGIN SELECT RAISE(ABORT,'synthetic late failure'); END`); err != nil {
		t.Fatal(err)
	}
	notice := &statistic.Notification{Title: "站点数据统计", Text: "合成统计正文", Fingerprint: strings.Repeat("a", 64)}
	if _, err = a.commitSiteStatisticNotice(ctx, run, notice, "all", time.Now().In(shanghai())); err == nil {
		t.Fatal("late receipt failure accepted")
	}
	var count int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='notification'`).Scan(&count)
	if count != 0 {
		t.Fatal("rolled back notice persisted")
	}
	if _, err = a.pluginStore.Data(ctx, "SiteStatistic", "last_notify"); !missingPluginValue(err) {
		t.Fatal("rolled back fingerprint persisted", err)
	}
	a.Store.DB.Exec(`DROP TRIGGER fail_statistic_receipt`)
	if _, err = a.commitSiteStatisticNotice(ctx, run, notice, "all", time.Now().In(shanghai())); err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.FinishRun(ctx, run.ID, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	statisticSave(t, a, cookie, builtin.SiteStatisticConfiguration{Enabled: false, NotifyType: "all"})
	statisticExecute(t, a)
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_event_subscriptions WHERE instance_id='SiteStatistic' AND active=1`).Scan(&count)
	if count != 0 {
		t.Fatal("PluginManager get_state gate bypassed")
	}
}

func TestPluginMergeSiteSwitchActuallyBindsSiteStatistic(t *testing.T) {
	ctx := context.Background()
	a, cookie, _, _ := siteSwitchFixture(t)
	statisticInstall(t, a, cookie)
	statisticSave(t, a, cookie, builtin.SiteStatisticConfiguration{Enabled: true, DashboardType: "total", NotifyType: "inc"})
	statisticExecute(t, a)
	siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []string{"s1"}, "statistic_follow_enable_sites": true})
	siteSwitchExecute(t, a, true)
	statisticExecute(t, a) // The real related Go implementation, no availability seam.
	stored, err := a.pluginStore.Config(ctx, "SiteStatistic")
	if err != nil {
		t.Fatal(err)
	}
	var config builtin.SiteStatisticConfiguration
	json.Unmarshal(stored.Value, &config)
	if !config.Enabled || config.DashboardType != "total" || config.NotifyType != "inc" || len(config.StatisticSites) != 1 || config.StatisticSites[0] != (builtin.SiteReference{Native: identityRequire(t, a, "s1").SourceID}) {
		t.Fatal("real peer field clobbered", string(stored.Value))
	}
	current := a.Settings().Sites[1]
	current.Enabled = true
	if _, err = a.saveManagedSite(ctx, current); err != nil {
		t.Fatal(err)
	}
	statisticExecute(t, a)
	stored, _ = a.pluginStore.Config(ctx, "SiteStatistic")
	json.Unmarshal(stored.Value, &config)
	if len(config.StatisticSites) != 2 {
		t.Fatal("core site change did not follow actual statistic peer", string(stored.Value))
	}
}

func TestSiteDataRefreshClockCoalescesAndDisables(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	if err := a.scheduleSiteDataRefresh(ctx, at); err != nil {
		t.Fatal(err)
	}
	var count int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='site_refresh'`).Scan(&count)
	if count != 0 {
		t.Fatal("interval collected immediately")
	}
	if err := a.scheduleSiteDataRefresh(ctx, at.Add(19*time.Hour)); err != nil {
		t.Fatal(err)
	}
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='site_refresh'`).Scan(&count)
	if count != 1 {
		t.Fatal("missed intervals not coalesced", count)
	}
	c := a.Settings()
	zero := 0
	c.SiteDataRefreshHours = &zero
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := a.scheduleSiteDataRefresh(ctx, at.Add(20*time.Hour)); err != nil {
		t.Fatal(err)
	}
	c = a.Settings()
	six := 6
	c.SiteDataRefreshHours = &six
	a.saveSettings(ctx, c)
	if err := a.scheduleSiteDataRefresh(ctx, at.Add(21*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='site_refresh_clock'`).Scan(&raw)
	var clock struct{ Next time.Time }
	json.Unmarshal(raw, &clock)
	if !clock.Next.Equal(at.Add(27 * time.Hour)) {
		t.Fatal("reenabling replayed disabled interval", string(raw))
	}
}
