package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/download"
	"light-media/internal/sites"
)

func TestSubscriptionSiteSelectionAndTotalEpisodesPersist(t *testing.T) {
	a, source, _ := importTestApp(t)
	m := importFixture(t, a, "tv")
	episodes := []catalog.Episode{}
	for i := 1; i <= 8; i++ {
		episodes = append(episodes, catalog.Episode{ID: int64(i), SeasonNumber: 1, EpisodeNumber: i, AirDate: "2024-01-01"})
	}
	importCache(t, a, "season:123:1", catalog.Season{SeriesID: m.ID, SeasonNumber: 1, Episodes: episodes})
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "qb", Kind: "qb", BaseURL: "http://127.0.0.1:1"}, SharedPaths: true}}
	a.config.Sites = []SiteConfig{{Config: sites.Config{ID: "one", Kind: "hhanclub", BaseURL: "http://127.0.0.1:1"}, Enabled: true}}
	request := SubscriptionRequest{Keyword: "keyword test", MediaType: "tv", TMDBID: m.ID, Season: 1, TargetScope: "season", StartEpisode: 3, TotalEpisodes: 5, SiteIDs: []string{"one"}, DownloaderID: "qb", DownloadPath: source, State: "paused"}
	s, err := a.saveSubscription(context.Background(), "", request)
	if err != nil {
		t.Fatal(err)
	}
	if s.Keyword != request.Keyword || s.TotalEpisodes != 5 || !reflect.DeepEqual(s.SiteIDs, []string{"one"}) {
		t.Fatalf("options lost: %+v", s)
	}
	coverage, err := a.subscriptionCoverage(context.Background(), s, []int{1, 2, 3, 4, 5, 6, 7, 8})
	if err != nil || !reflect.DeepEqual(coverage.Known, []int{3, 4, 5}) {
		t.Fatalf("total boundary ignored: %+v %v", coverage, err)
	}
	request.TotalEpisodes = 2
	if _, err = a.saveSubscription(context.Background(), s.ID, request); err == nil {
		t.Fatal("end before start accepted")
	}
	request.TotalEpisodes = 5
	request.SiteIDs = []string{"missing"}
	if _, err = a.saveSubscription(context.Background(), s.ID, request); err == nil {
		t.Fatal("nonexistent site accepted")
	}
}

func TestSubscriptionOnlyQueriesSelectedSites(t *testing.T) {
	a, _, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	selected, other := 0, 0
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		selected++
		io.WriteString(w, workflowHTML("测试影片.2024.1080p.WEB-DL.x264-TEST", "", false))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		other++
		io.WriteString(w, workflowHTML("测试影片.2024.1080p.WEB-DL.x264-TEST", "", false))
	}))
	defer second.Close()
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/torrents/info" {
			workflowJSON(w, []any{})
		} else {
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()
	workflowConfigure(t, a, first.URL, qb.URL)
	copySite := a.config.Sites[0]
	copySite.ID = "other"
	copySite.BaseURL = second.URL
	a.config.Sites = append(a.config.Sites, copySite)
	s := Subscription{ID: "select-sites", Media: m, TargetScope: "movie", SiteIDs: []string{"fixture-site"}, DownloaderID: "fixture-qb", Policy: Policy{MinSeeders: 999999}, State: "active", Revision: 1, EpisodeDates: map[int]string{}}
	ctx := context.Background()
	if err := a.Store.Put(ctx, "subscription", s.ID, s); err != nil {
		t.Fatal(err)
	}
	job := importClaim(t, a, "subscription", s.ID)
	if err := a.checkSubscription(ctx, job, s.ID, 1); err != nil {
		t.Fatal(err)
	}
	if selected == 0 || other != 0 {
		t.Fatalf("wrong selected sites requested: selected=%d other=%d", selected, other)
	}
}

func TestSubscriptionDefaultDestinationSavesWithoutContactingDownloader(t *testing.T) {
	a, _, _ := importTestApp(t)
	m := importFixture(t, a, "tv")
	importCache(t, a, "season:123:1", catalog.Season{SeriesID: m.ID, SeasonNumber: 1, Episodes: []catalog.Episode{{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2024-01-01"}}})
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "offline", Kind: "qb", BaseURL: "http://127.0.0.1:1"}, SharedPaths: true}}
	s, err := a.saveSubscription(context.Background(), "", SubscriptionRequest{MediaType: "tv", TMDBID: m.ID, Season: 1, TargetScope: "season", StartEpisode: 1, State: "paused"})
	if err != nil {
		t.Fatal(err)
	}
	if s.DownloaderID != "" || s.DownloadPath != "" || s.DirectoryID != "" || s.RootID != "" {
		t.Fatalf("automatic destination was prematurely frozen: %+v", s)
	}
	var persisted Subscription
	if err = a.Store.Get(context.Background(), "subscription", s.ID, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.DownloaderID != "" || persisted.DownloadPath != "" {
		t.Fatal("default destination lost on persistence")
	}
}

func TestExistingSubscriptionCanPauseAfterItsConnectionsAreRemoved(t *testing.T) {
	a, _, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	ctx := context.Background()
	old := Subscription{ID: "orphaned", Media: m, TargetScope: "movie", State: "active", Revision: 1, DownloaderID: "removed", DownloadPath: "/unmounted/downloads", DirectoryID: "removed-directory", RootID: "removed-library", SiteIDs: []string{"removed-site"}}
	if err := a.Store.Put(ctx, "subscription", old.ID, old); err != nil {
		t.Fatal(err)
	}
	in := SubscriptionRequest{MediaType: "movie", TMDBID: m.ID, TargetScope: "movie", State: "paused", Revision: 1, DownloaderID: old.DownloaderID, DownloadPath: old.DownloadPath, DirectoryID: old.DirectoryID, RootID: old.RootID, SiteIDs: old.SiteIDs}
	saved, err := a.saveSubscription(ctx, old.ID, in)
	if err != nil || saved.State != "paused" || saved.Revision != 2 || saved.DownloaderID != old.DownloaderID || saved.DirectoryID != old.DirectoryID {
		t.Fatalf("local pause blocked: %+v %v", saved, err)
	}
	in.State, in.Revision = "active", 2
	if _, err = a.saveSubscription(ctx, old.ID, in); err == nil {
		t.Fatal("reactivation accepted removed connection")
	}
}
