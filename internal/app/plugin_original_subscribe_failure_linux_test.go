package app

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOriginalLarkRSSPromptAndStableFailureCooldownLinux(t *testing.T) {
	t.Setenv("SUBSCRIBE_MODE", "rss")
	t.Setenv("NO_CACHE_SITE_KEY", "m-team")

	a, source, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	torrent, hash := workflowTorrent("测试影片 (2024) 1080p.mkv")
	var phase, feeds, gets, posts, adds atomic.Int32
	phase.Store(1)
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stage := phase.Load()
		switch r.URL.Path {
		case "/rss.php":
			feeds.Add(1)
			pageID := 201
			if stage == 3 {
				pageID = 202
			}
			w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
			_, _ = fmt.Fprintf(w, `<rss version="2.0"><channel><item><title>测试影片.2024.1080p.WEB-DL.x264-TESTV%d</title><description>listing revision %d</description><link>details.php?id=%d</link><enclosure url="download.php?id=%d&amp;passkey=owned-stage-%d" length="1024" type="application/x-bittorrent"/></item></channel></rss>`, stage, stage, pageID, 300+stage, stage)
		case "/download.php":
			token := r.URL.Query().Get("passkey")
			if token != fmt.Sprintf("owned-stage-%d", stage) || stage == 2 || r.Header.Get("Cookie") != "fixture-cookie" {
				t.Errorf("unexpected download request in stage %d", stage)
				http.Error(w, "unexpected enclosure", 400)
				return
			}
			switch r.Method {
			case http.MethodGet:
				gets.Add(1)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = io.WriteString(w, `<form action="/ignored"><input name="ignored" value="ignored"></form><form action="?"><input name="confirm" value="owned+confirm"><input name="download" value="1"><button>下载种子文件</button></form>`)
			case http.MethodPost:
				posts.Add(1)
				if err := r.ParseForm(); err != nil || len(r.PostForm) != 2 || r.PostForm.Get("confirm") != "owned+confirm" || r.PostForm.Get("download") != "1" {
					t.Error("confirmation did not submit exactly the recognized form fields")
					http.Error(w, "wrong form", 400)
					return
				}
				if stage == 1 {
					_, _ = io.WriteString(w, "<html>confirmation failed</html>")
					return
				}
				w.Header().Set("Content-Type", "application/x-bittorrent")
				_, _ = w.Write(torrent)
			default:
				http.Error(w, "unexpected method", 405)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(tracker.Close)

	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v5.0.0")
		case "/api/v2/app/preferences":
			workflowJSON(w, map[string]string{"save_path": source})
		case "/api/v2/torrents/info":
			if adds.Load() > 0 {
				task := workflowTask(hash, 0)
				task["save_path"] = source
				workflowJSON(w, []any{task})
				return
			}
			workflowJSON(w, []any{})
		case "/api/v2/torrents/add":
			if phase.Load() != 3 || r.ParseMultipartForm(4<<20) != nil || r.FormValue("savepath") != source {
				t.Error("unexpected downloader Add")
				http.Error(w, "unexpected Add", 400)
				return
			}
			part, _, err := r.FormFile("torrents")
			if err != nil {
				t.Error(err)
				http.Error(w, "missing torrent", 400)
				return
			}
			defer part.Close()
			got, err := io.ReadAll(part)
			if err != nil || !bytes.Equal(got, torrent) {
				t.Error("downloader Add did not contain the confirmation response bytes")
				http.Error(w, "wrong torrent", 400)
				return
			}
			adds.Add(1)
			_, _ = io.WriteString(w, "Ok.")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(qb.Close)

	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/movie":
			workflowJSON(w, map[string]any{"page": 1, "total_pages": 1, "total_results": 1, "results": []map[string]any{
				{"id": m.ID, "title": m.Title, "original_title": m.OriginalTitle, "release_date": "2024-01-01"},
			}})
		case "/3/movie/123":
			workflowJSON(w, map[string]any{"id": m.ID, "title": m.Title, "original_title": m.OriginalTitle, "release_date": "2024-01-01",
				"original_language": m.OriginalLanguage, "origin_country": m.OriginCountry, "production_countries": m.ProductionCountries, "genres": m.Genres})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(metadata.Close)
	transport := ownedLoopbackFixtureTransport(t, metadata)
	installMetadataFixture(t, a, "owned-source-prompt-tmdb", []string{"/3/movie/123", "/3/search/movie"}, transport.RoundTrip)
	workflowConfigure(t, a, tracker.URL, qb.URL)
	a.config.Sites[0].RSS = tracker.URL + "/rss.php?passkey=owned-feed"
	a.config.Downloaders[0].DownloadPath, a.config.Downloaders[0].SharedPaths = source, true
	a.settingsMu.Lock()
	err := a.saveSettings(t.Context(), a.Settings())
	a.settingsMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	sub, err := a.saveSubscription(t.Context(), "", SubscriptionRequest{
		MediaType: "movie", TMDBID: m.ID, DownloaderID: "fixture-qb", DownloadPath: source, RootID: "root", Schedule: Schedule{Mode: "manual"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sub.SourceState = "P"
	sub.CreatedAt = time.Now().Add(-2 * time.Minute).Format(time.RFC3339Nano)
	if err := a.Store.Put(t.Context(), "subscription", sub.ID, sub); err != nil {
		t.Fatal(err)
	}
	a.Start(t.Context())

	refresh := func(stage, wantGets, wantPosts, wantAdds int32) {
		t.Helper()
		phase.Store(stage)
		if err := a.originalChatRefreshSubscriptions(t.Context()); err != nil {
			t.Fatal(err)
		}
		if feeds.Load() != stage || gets.Load() != wantGets || posts.Load() != wantPosts || adds.Load() != wantAdds {
			t.Fatalf("stage %d: feeds=%d gets=%d posts=%d adds=%d", stage, feeds.Load(), gets.Load(), posts.Load(), adds.Load())
		}
	}
	refresh(1, 1, 1, 0)
	if len(a.originalChatRSSInvalidSnapshot()) != 0 {
		t.Fatal("a failed confirmation poisoned the separate invalid-enclosure cache")
	}
	var fingerprint string
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT id FROM records WHERE kind='subscription_download_failure'`).Scan(&fingerprint); err != nil {
		t.Fatal(err)
	}
	var failure subscriptionDownloadFailure
	if err := a.readQualitySealed(t.Context(), a.Store.DB, "subscription_download_failure", fingerprint, &failure); err != nil {
		t.Fatal(err)
	}
	retryIn := time.Until(time.Unix(failure.RetryAt, 0))
	if failure.Count != 1 || retryIn < 24*time.Hour-time.Minute || retryIn > 24*time.Hour {
		t.Fatalf("expected one confirmed material failure with a 24-hour cooldown: count=%d remaining=%v", failure.Count, retryIn)
	}
	refresh(2, 1, 1, 0)
	var cooldownSkips int
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM events WHERE module='subscriptions' AND event='candidate_cooldown'`).Scan(&cooldownSkips); err != nil || cooldownSkips != 1 {
		t.Fatalf("expected one cooldown decision for the stable resource key: count=%d err=%v", cooldownSkips, err)
	}
	var stillCooling subscriptionDownloadFailure
	if err := a.readQualitySealed(t.Context(), a.Store.DB, "subscription_download_failure", fingerprint, &stillCooling); err != nil || stillCooling != failure {
		t.Fatalf("skipping a cooled candidate must not renew its cooldown: %v", err)
	}
	var cached originalChatLatestSite
	if err := a.originalChatLatestRead(t.Context(), a.Store.DB, originalChatLatestCacheKind, "rss:fixture-site", &cached); err != nil {
		t.Fatal(err)
	}
	var relistRecognized bool
	for _, row := range cached.Rows {
		if strings.HasSuffix(row.Resource.Title, "-TESTV2") && len(row.Media) != 0 {
			relistRecognized = true
		}
	}
	if !relistRecognized {
		t.Fatal("rotated enclosure must enter the recognized candidate cache before being skipped")
	}
	refresh(3, 2, 2, 1)
	t.Log("failed first-download confirmation cooled down the same source page ID across a changed enclosure; a different source page ID confirmed and added the exact torrent")
}
