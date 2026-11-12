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
	// Creation publishes SiteUpdated asynchronously. Establish the baseline only
	// after its consumer finishes, so this test measures its explicit refresh.
	a.wg.Wait()
	baselineEvents, err := a.domainEvents(ctx, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	baselineRecords := siteStatisticsRecordSnapshot(t, a)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER fail_site_fact BEFORE INSERT ON domain_events BEGIN SELECT RAISE(ABORT,'synthetic late outbox failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.refreshManagedSite(ctx, site.ID); err == nil {
		t.Fatal("outbox failure reported refresh success")
	}
	if got := siteStatisticsRecordSnapshot(t, a); got != baselineRecords {
		t.Fatal("failed refresh changed committed snapshots")
	}
	a.Store.DB.Exec(`DROP TRIGGER fail_site_fact`)
	if _, err := a.refreshManagedSite(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	events, err := a.domainEvents(ctx, baselineEvents.NextCursor, 200)
	if err != nil || len(events.Events) != 1 || events.Events[0].Type != "site.refreshed" || events.Events[0].SiteID != site.ID || events.Events[0].Purpose != "site_statistics" {
		t.Fatal("missing committed fact", events, err)
	}
}

func TestSiteStatisticsErrorDTOEmitsSourceRefreshFactsWhileJobFails(t *testing.T) {
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
	// Creation publishes SiteUpdated asynchronously. Establish the baseline only
	// after its consumer finishes, so this test measures its explicit refresh.
	a.wg.Wait()
	baselineEvents, err := a.domainEvents(ctx, 0, 200)
	if err != nil {
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
	events, err := a.domainEvents(ctx, baselineEvents.NextCursor, 200)
	if err != nil {
		t.Fatal(err)
	}
	var sites []string
	for _, event := range events.Events {
		if event.Type == "site.refreshed" {
			sites = append(sites, event.SiteID)
		}
	}
	if len(sites) != 2 || sites[0] != site.ID || sites[1] != "*" {
		t.Fatal("source error DTO lost refresh facts", sites)
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

// Compare complete records rather than only row counts: a failed refresh must
// neither insert a new history row nor replace an existing connection/snapshot.
func siteStatisticsRecordSnapshot(t *testing.T, a *App) string {
	t.Helper()
	rows, err := a.Store.DB.Query(`SELECT kind,id,payload,updated_at FROM records WHERE kind IN ('site_connection','site_stats_latest','site_stats_history') ORDER BY kind,id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var records [][4]string
	for rows.Next() {
		var row [4]string
		if err := rows.Scan(&row[0], &row[1], &row[2], &row[3]); err != nil {
			t.Fatal(err)
		}
		records = append(records, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
