package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/sites"
)

func TestSiteRequestLimitsWindowSpacingAndCancellation(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	at := time.Unix(1000, 0)
	for _, ts := range []int64{980, 990} {
		if _, err := a.Store.DB.Exec(`INSERT INTO site_usage VALUES(?,?)`, "rate-fixture", ts); err != nil {
			t.Fatal(err)
		}
	}
	site := SiteConfig{Config: sites.Config{ID: "rate-fixture"}, SiteOptions: SiteOptions{LimitInterval: 60, LimitCount: 2, LimitSeconds: 20}}
	delay, err := a.siteRequestDelay(ctx, site, at)
	if err != nil || delay != 41*time.Second {
		t.Fatalf("window must dominate spacing: %v / %v", delay, err)
	}
	site.LimitCount = 3
	delay, err = a.siteRequestDelay(ctx, site, at)
	if err != nil || delay != 11*time.Second {
		t.Fatalf("minimum request spacing ignored: %v / %v", delay, err)
	}
	site.LimitSeconds = 0
	delay, err = a.siteRequestDelay(ctx, site, at)
	if err != nil || delay != 0 {
		t.Fatal("available window was blocked")
	}
	site.LimitSeconds = 1
	if _, err = a.Store.DB.Exec(`INSERT INTO site_usage VALUES(?,?)`, site.ID, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err = a.waitSiteRequestLimits(cancelled, site); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("rate wait was not cancellable: %v", err)
	}
}

func TestSiteRequestLimitBlocksNetworkWithoutRecordingRequest(t *testing.T) {
	a := newTestApp(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	site := SiteConfig{Config: sites.Config{ID: "rate-fixture", Kind: "ubits", Name: "Synthetic", BaseURL: server.URL, Cookie: "synthetic-cookie"}, Enabled: true, SiteOptions: SiteOptions{LimitInterval: 60, LimitCount: 1}}
	a.cfgMu.Lock()
	a.config.Sites = []SiteConfig{site}
	a.cfgMu.Unlock()
	if _, err := a.Store.DB.Exec(`INSERT INTO site_usage VALUES(?,?)`, site.ID, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := a.withSite(ctx, site, false, func(client *sites.Client) error {
		_, err := client.Search(ctx, "fixture", 1)
		return err
	})
	var used int
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM site_usage WHERE site_id=?`, site.ID).Scan(&used)
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.Code != "SITE_REQUEST_LIMIT" || calls.Load() != 0 || used != 1 {
		t.Fatalf("limited request reached the network or was counted: %v / %d / %d", err, calls.Load(), used)
	}
}

func TestAutomaticSiteSearchAllowsHeavyRecentUsage(t *testing.T) {
	a := newTestApp(t)
	fixture, err := os.ReadFile("../sites/testdata/ubits-search.sanitized.html")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fixture)
	}))
	defer server.Close()
	site := SiteConfig{Config: sites.Config{ID: "busy-site", Kind: "ubits", Name: "Synthetic", BaseURL: server.URL, Cookie: "synthetic-cookie"}, Enabled: true}
	a.config.Sites = []SiteConfig{site}
	if _, err := a.Store.DB.Exec(`WITH RECURSIVE requests(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM requests WHERE n<610)
		INSERT INTO site_usage SELECT ?, ? FROM requests`, site.ID, time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	err = a.withSite(ctx, site, true, func(client *sites.Client) error {
		result, err := client.Search(ctx, "fixture", 1)
		if err == nil && len(result.Resources) == 0 {
			t.Fatal("automatic search reached the fixture but returned no resources")
		}
		return err
	})
	if err != nil {
		t.Fatalf("historical usage blocked automatic search: %v", err)
	}
}

func TestSiteUserAgentChangeClearsAuthenticationCooldown(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	config := a.Settings()
	config.Sites = []SiteConfig{{Config: sites.Config{ID: "ua-fixture", Kind: "nexusphp", Name: "Synthetic", BaseURL: "https://fixture.example.org", Cookie: "synthetic-cookie", UserAgent: "Old-UA"}, Enabled: true}}
	if err := a.saveSettings(ctx, config); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "site_cooldown", "ua-fixture", map[string]int64{"until": time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	config = a.Settings()
	config.Sites[0].UserAgent = "Fixed-UA"
	if err := a.saveSettings(ctx, config); err != nil {
		t.Fatal(err)
	}
	var cooldown any
	if err := a.Store.Get(ctx, "site_cooldown", "ua-fixture", &cooldown); err == nil {
		t.Fatal("fixed authentication UA remained blocked by stale cooldown")
	}
}

func TestManagedSiteResourceBrowseEmptyQueryAndFailure(t *testing.T) {
	a := newTestApp(t)
	fixture, err := os.ReadFile("../sites/testdata/ubits-search.sanitized.html")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/torrents.php" || r.URL.Query().Get("search") != "" || r.URL.Query().Get("page") != "0" {
			t.Error("site browse did not use the site's first torrent page")
		}
		if r.Header.Get("Cookie") != "synthetic-cookie" || r.Header.Get("User-Agent") != "Synthetic-UA" {
			t.Error("configured authentication headers missing")
		}
		_, _ = w.Write(fixture)
	}))
	defer server.Close()
	site := SiteConfig{Config: sites.Config{ID: "browse-fixture", Kind: "ubits", Name: "Synthetic", BaseURL: server.URL, Cookie: "synthetic-cookie", UserAgent: "Synthetic-UA"}, Enabled: true}
	a.cfgMu.Lock()
	a.config.Sites = []SiteConfig{site}
	a.cfgMu.Unlock()
	result, err := a.browseManagedSiteResources(context.Background(), site.ID, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	var out SearchOutput
	raw, _ := json.Marshal(result)
	if err = json.Unmarshal(raw, &out); err != nil || len(out.Candidates) != 2 || len(out.Sites) != 1 || out.Sites[0].Status != "ok" {
		t.Fatalf("recent resources missing: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("single-site browse reached unexpected endpoints")
	}
	site.Enabled = false
	a.cfgMu.Lock()
	a.config.Sites = []SiteConfig{site}
	a.cfgMu.Unlock()
	if _, err = a.site(site.ID); err == nil {
		t.Fatal("disabled site was available for automatic requests")
	}
	if _, err = a.browseManagedSiteResources(context.Background(), site.ID, "", 1); err != nil || calls.Load() != 2 {
		t.Fatal("explicit card browse must work without enabling background requests")
	}
}

func TestMoviePilotExtraSecretsAreRedacted(t *testing.T) {
	site := SiteConfig{SiteOptions: SiteOptions{Token: "synthetic-authorization", RSS: "https://fixture.invalid/rss?passkey=synthetic-rss", Note: siteJSON(`{"private":"synthetic-note"}`)}}
	input := site.Token + " " + site.RSS + " " + string(site.Note)
	got := cleanWithSettings(input, Settings{Sites: []SiteConfig{site}})
	for _, value := range []string{"synthetic-authorization", "synthetic-rss", "synthetic-note"} {
		if strings.Contains(got, value) {
			t.Fatal("imported site secret leaked into diagnostics")
		}
	}
}
