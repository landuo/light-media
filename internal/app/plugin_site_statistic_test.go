package app

import (
	"context"
	"encoding/json"
	"light-media/internal/sites"
	"testing"
	"time"
)

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
