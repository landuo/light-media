package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/priorityrules"
	"light-media/internal/sites"
)

func TestPriorityRuleDownloadSortAffectsRealIdentitySearch(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/torrents.php" {
			t.Error("unexpected material access", r.URL.Path)
		}
		io.WriteString(w, keywordFixtureListing(workflowHTML("Synthetic.2026.1080p.WEB-DL", "", true)))
	})
	one := httptest.NewServer(handler)
	defer one.Close()
	two := httptest.NewServer(handler)
	defer two.Close()
	workflowConfigure(t, a, one.URL, "http://127.0.0.1:1")
	c := a.Settings()
	c.PriorityRules = &priorityrules.Configuration{TorrentPriority: []string{"site"}}
	c.Sites[0].Priority = 10
	second := c.Sites[0]
	second.ID = "sort-second"
	second.Name = "Second"
	second.BaseURL = two.URL
	second.Priority = 0
	c.Sites = append(c.Sites, second)
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	identity := catalog.Media{ID: 123, MediaType: "movie", Title: "Synthetic", OriginalTitle: "Synthetic", Year: 2026}
	if err := a.Store.Put(ctx, "cache", "media:movie:123", map[string]any{"at": time.Now(), "data": identity}); err != nil {
		t.Fatal(err)
	}
	for i, site := range c.Sites {
		upload := int64(100 - i*99)
		snapshot := siteSnapshot{SiteID: site.ID, Name: site.Name, ConfigKey: a.managedSiteKey(site), Stats: &sites.UserStats{Uploaded: &upload}}
		if err := a.Store.Put(ctx, "site_stats_latest", site.ID, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	search := func(want string) {
		t.Helper()
		out, err := a.search(ctx, SearchRequest{MediaType: "movie", TMDBID: 123}, false)
		if err != nil || len(out.Candidates) != 2 || out.Candidates[0].Resource.SiteID != want {
			t.Fatal("configured download sort not applied to real search", priorityCandidateIDs(out), err)
		}
	}
	search(second.ID)
	rev, err := ruleRevision(ctx, a.Store.DB, "priority")
	if err != nil {
		t.Fatal(err)
	}
	putRules(t, a, cookie, "priority", rev, []string{"upload"}, 200)
	search(c.Sites[0].ID)
	// Changing credentials invalidates the account snapshot used for sorting.
	c = a.Settings()
	c.Sites[0].Cookie = "new-synthetic-cookie"
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	search(second.ID)
}
