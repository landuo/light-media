package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"light-media/internal/sites"
)

func TestSiteStatisticsSnapshotAndFactCommitTogether(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="logout.php">退出</a><a href="userdetails.php?id=1">synthetic</a><div>上传：1 GB 下载：0 B 总做种数：0 总做种体积：0 B</div>`)
	}))
	defer server.Close()
	site := SiteConfig{Config: sites.Config{ID: "atomic", Kind: "jiayuan", Name: "合成事务站点", BaseURL: server.URL, Cookie: "synthetic-cookie"}, Enabled: true}
	if _, err := a.saveManagedSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER fail_site_fact BEFORE INSERT ON domain_events BEGIN SELECT RAISE(ABORT,'synthetic late outbox failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.refreshManagedSite(ctx, site.ID); err == nil {
		t.Fatal("outbox failure reported refresh success")
	}
	for _, kind := range []string{"site_connection", "site_stats_latest", "site_stats_history"} {
		var count int
		a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind=?`, kind).Scan(&count)
		if count != 0 {
			t.Fatal("partial snapshot committed", kind)
		}
	}
	a.Store.DB.Exec(`DROP TRIGGER fail_site_fact`)
	if _, err := a.refreshManagedSite(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	events, err := a.domainEvents(ctx, 0, 200)
	if err != nil || len(events.Events) != 1 || events.Events[0].Type != "site.refreshed" || events.Events[0].SiteID != site.ID || events.Events[0].Purpose != "site_statistics" {
		t.Fatal("missing committed fact", events, err)
	}
}

func TestSiteStatisticsFullFailureDoesNotEmitAggregateSuccess(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "synthetic unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	site := SiteConfig{Config: sites.Config{ID: "failed", Kind: "jiayuan", Name: "合成失败站点", BaseURL: server.URL, Cookie: "synthetic-cookie"}, Enabled: true}
	if _, err := a.saveManagedSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	if _, err := a.enqueue(ctx, "site_refresh", map[string]string{"id": "site_refresh"}, ""); err != nil {
		t.Fatal(err)
	}
	job, err := a.claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.safeRun(ctx, job); pluginErrorCode(err) != "SITE_STATISTICS_REFRESH_FAILED" {
		t.Fatal("full failure became success", err)
	}
	events, err := a.domainEvents(ctx, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events.Events {
		if event.Type == "site.refreshed" {
			t.Fatal("failed data emitted success fact")
		}
	}
	view, err := a.jobView(ctx, job)
	if err != nil || view.Summary.TypeLabel != "刷新站点数据" || view.Summary.Subject != "所有启用站点" {
		t.Fatal("unidentified background task", view, err)
	}
}

func TestSiteRefreshEventRejectsDownloadAuthority(t *testing.T) {
	base := DomainEvent{EventID: domainEventID("site.refreshed", "synthetic"), Type: "site.refreshed", SiteID: "*"}
	for _, mutate := range []func(*DomainEvent){func(e *DomainEvent) { e.SiteID = "" }, func(e *DomainEvent) { e.SiteID = "https://private.invalid/path" }, func(e *DomainEvent) { e.IntentID = "intent_unrelated" }, func(e *DomainEvent) { e.AssetID = "asset_unrelated" }, func(e *DomainEvent) { e.MediaID = "movie:1" }, func(e *DomainEvent) { e.Purpose = "core_download" }} {
		event := base
		mutate(&event)
		if _, err := validateDomainEvent(event); err == nil {
			raw, _ := json.Marshal(event)
			t.Fatal("mixed site event authority accepted", string(raw))
		}
	}
}
