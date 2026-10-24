package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/priorityrules"
)

func TestSubscriptionSingleSeasonSkipsMultiSeasonBeforeMaterialOrAdd(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	m := importFixture(t, a, "tv")
	importCache(t, a, "season:123:1", catalog.Season{SeriesID: m.ID, SeasonNumber: 1, Episodes: []catalog.Episode{{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, Name: "episode", AirDate: "2024-01-01"}}})
	var gets, adds atomic.Int64
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/torrents/info" {
			workflowJSON(w, []any{})
			return
		}
		adds.Add(1)
		t.Error("single-season subscription reached multi-season daemon", r.URL.Path)
		w.WriteHeader(400)
	}))
	defer qb.Close()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			io.WriteString(w, workflowHTML("测试剧集 S01-S02 1080p WEB-DL", "", false))
		case "/download.php":
			gets.Add(1)
			w.Write(automaticPackTorrent([]string{"测试剧集 S01E01 1080p.mkv", "测试剧集 S02E01 1080p.mkv"}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	workflowConfigure(t, a, site.URL, qb.URL)
	a.config.Downloaders[0].DownloadPath, a.config.Downloaders[0].SharedPaths = source, true
	a.config.PriorityRules = &priorityrules.Configuration{SubscribeGroups: []string{"quality"}, Groups: []priorityrules.Group{{Name: "quality", RuleString: priorityText("WEBDL & 4K > WEBDL & 1080P")}}, Mode: priorityrules.Rust}
	best := true
	s, e := a.saveSubscription(ctx, "", SubscriptionRequest{MediaType: "tv", TMDBID: m.ID, Season: 1, TotalEpisodes: 1, BestVersion: &best, DownloaderID: "fixture-qb", DownloadPath: source, RootID: "root", Schedule: Schedule{Mode: "manual"}})
	if e != nil {
		t.Fatal(e)
	}
	if e = a.checkSubscription(ctx, importClaim(t, a, "subscription", s.ID), s.ID, s.Revision); e != nil {
		t.Fatal(e)
	}
	var intents int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='intent'`).Scan(&intents); e != nil || intents != 0 || gets.Load() != 0 || adds.Load() != 0 {
		t.Fatal("single-season original subset gate was widened", intents, gets.Load(), adds.Load(), e)
	}
}
