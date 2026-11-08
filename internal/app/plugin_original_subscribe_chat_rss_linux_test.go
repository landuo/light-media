package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOriginalLarkPausedSearchAndRSSReplacementLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	t.Setenv("SUBSCRIBE_MODE", "rss")
	t.Setenv("NO_CACHE_SITE_KEY", "m-team")

	type card struct{ method, content string }
	cards := make(chan card, 16)
	lark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/open-apis")
		switch {
		case path == "/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-subscribe-fixture"}`)
		case strings.Contains(path, "/reactions"):
			_, _ = io.WriteString(w, `{"code":0,"data":{"reaction_id":"rx_fixture"}}`)
		case path == "/im/v1/messages" || path == "/im/v1/messages/om_rss":
			var in struct{ Content string }
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			select {
			case cards <- card{r.Method, in.Content}:
			default:
				t.Error("unexpected extra Lark card")
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_rss","chat_id":"oc_rss"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(lark.Close)

	a, source, _ := importTestApp(t)
	torrent, hash := workflowTorrent("测试影片 (2024) 1080p.mkv")
	var searches, feeds, oldReads, newReads, adds atomic.Int32
	var relisted atomic.Bool
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			searches.Add(1)
			if r.URL.Query().Get("search") == "" {
				t.Error("manual search did not pass a keyword to the tracker")
			}
			_, _ = io.WriteString(w, workflowHTML("Different.Film.2024.1080p", "", false))
		case "/rss.php":
			feeds.Add(1)
			if r.URL.Query().Get("passkey") != "owned-feed" || r.Header.Get("Cookie") != "" {
				t.Error("configured RSS URL or cookie separation changed")
			}
			token, trackerID := "owned-old", 101
			if relisted.Load() {
				token, trackerID = "owned-new", 102
			}
			w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
			_, _ = fmt.Fprintf(w, `<rss version="2.0"><channel><item><title>测试影片.2024.1080p.WEB-DL.x264-TEST</title><description>same signature</description><link>details.php?id=%d</link><enclosure url="download.php?id=%d&amp;passkey=%s" length="1024" type="application/x-bittorrent"/></item></channel></rss>`, trackerID, trackerID, token)
		case "/download.php":
			switch r.URL.Query().Get("passkey") {
			case "owned-old":
				if r.URL.Query().Get("id") != "101" {
					t.Error("old enclosure lost the tracker ID")
				}
				oldReads.Add(1)
				http.NotFound(w, r)
			case "owned-new":
				if r.URL.Query().Get("id") != "102" {
					t.Error("replacement enclosure lost the tracker ID")
				}
				newReads.Add(1)
				w.Header().Set("Content-Type", "application/x-bittorrent")
				_, _ = w.Write(torrent)
			default:
				http.NotFound(w, r)
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
			if !relisted.Load() || r.ParseMultipartForm(4<<20) != nil || r.FormValue("savepath") != source {
				t.Error("unexpected downloader mutation or save path")
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
				t.Error("download did not use the relisted enclosure's exact bytes")
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

	a.pluginOriginalLarkTestOrigin = lark.URL + "/open-apis"
	m := importFixture(t, a, "movie")
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
	ownedMetadata := ownedLoopbackFixtureTransport(t, metadata)
	installMetadataFixture(t, a, "owned-source-rss-tmdb", []string{"/3/movie/123", "/3/search/movie"}, ownedMetadata.RoundTrip)
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
	sub.CreatedAt = time.Now().Add(-2 * time.Minute).Format(time.RFC3339Nano)
	if err := a.Store.Put(t.Context(), "subscription", sub.ID, sub); err != nil {
		t.Fatal(err)
	}
	paused, err := a.saveSubscriptionStatus(t.Context(), sub.ID, "paused", sub.Revision)
	if err != nil {
		t.Fatal(err)
	}

	a.Start(t.Context())
	admin := setupTestAdmin(t, a)
	installMergeSiteSwitchPackage(t, a, admin, bundle, "LarkMessager")
	api := a.Handler()
	const plugin = "/api/plugins/LarkMessager"
	response := callTest(t, api, "PUT", plugin, `{"enabled":true,"app_id":"owned-app","app_secret":"owned-secret","chat_id":"oc_rss","verification_token":"owned-verification","switchs":["插件"]}`, admin, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	post := func(eventType string, fields map[string]any) {
		t.Helper()
		raw := mustCleanSeedJSON(map[string]any{"header": map[string]any{"event_type": eventType}, "event": fields})
		result := callTest(t, api, "POST", plugin+"/api/webhook?token=owned-verification", string(raw), nil, "")
		var ack struct{ Success bool }
		if err := json.Unmarshal(result.Body.Bytes(), &ack); result.Code != 200 || err != nil || !ack.Success {
			t.Fatalf("webhook admission failed: %d %s", result.Code, result.Body.String())
		}
	}
	message := 0
	send := func(value string) {
		t.Helper()
		message++
		post("im.message.receive_v1", map[string]any{
			"sender":  map[string]any{"sender_id": map[string]any{"open_id": "ou_owner"}},
			"message": map[string]any{"message_id": fmt.Sprintf("om_rss_%d", message), "chat_id": "oc_rss", "message_type": "text", "content": string(mustCleanSeedJSON(map[string]string{"text": value}))},
		})
	}
	click := func(value string) {
		t.Helper()
		post("card.action.trigger", map[string]any{
			"operator": map[string]any{"open_id": "ou_owner"}, "context": map[string]any{"open_message_id": "om_rss", "open_chat_id": "oc_rss"},
			"action": map[string]any{"value": map[string]string{"callback_data": value}},
		})
	}
	next := func(method string, fragment string) string {
		t.Helper()
		select {
		case result := <-cards:
			if result.method != method || !strings.Contains(result.content, fragment) {
				t.Fatalf("wanted %s with %q, got %s: %s", method, fragment, result.method, result.content)
			}
			if strings.Contains(result.content, "owned-old") || strings.Contains(result.content, "owned-new") {
				t.Error("a private enclosure was exposed in chat")
			}
			return result.content
		case <-time.After(12 * time.Second):
			t.Fatalf("no %s card for %q", method, fragment)
			return ""
		}
	}
	send("/subscribes")
	first := next("POST", "暂停")
	token := regexp.MustCompile(`subscribes:[0-9a-f]{12}:`).FindString(first)
	if token == "" {
		t.Fatal("subscription callback ID is missing")
	}
	click(token + "search")
	next("PATCH", "当前操作：搜索订阅")
	send("1")
	next("POST", "开始搜索订阅【测试影片】")
	next("POST", "已完成 1 个订阅搜索")
	next("POST", "暂停")
	var actual Subscription
	if err := a.Store.Get(t.Context(), "subscription", sub.ID, &actual); err != nil {
		t.Fatal(err)
	}
	if searches.Load() == 0 || feeds.Load() != 0 || adds.Load() != 0 || actual.State != "paused" || actual.Revision != paused.Revision || actual.NextCheck != paused.NextCheck {
		t.Fatalf("explicit ID search did not preserve pause: searches=%d rss=%d adds=%d state=%s revision=%d next=%v", searches.Load(), feeds.Load(), adds.Load(), actual.State, actual.Revision, actual.NextCheck)
	}
	var explicitJobs int
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM jobs
		WHERE kind='subscription' AND state='succeeded' AND json_extract(payload,'$.id')=?
		AND json_extract(payload,'$.revision')=? AND json_extract(payload,'$._chat_paused_search')=1`, paused.ID, paused.Revision).Scan(&explicitJobs); err != nil || explicitJobs != 1 {
		t.Fatalf("expected one successful revision-bound manual search: jobs=%d err=%v", explicitJobs, err)
	}
	active, err := a.saveSubscriptionStatus(t.Context(), actual.ID, "active", actual.Revision)
	if err != nil {
		t.Fatal(err)
	}
	active.SourceState = "P"
	active.CreatedAt = time.Now().Add(-2 * time.Minute).Format(time.RFC3339Nano)
	if err := a.Store.Put(t.Context(), "subscription", active.ID, active); err != nil {
		t.Fatal(err)
	}

	refresh := func() {
		t.Helper()
		click(token + "refresh")
		next("POST", "开始刷新订阅")
		next("POST", "订阅刷新执行完成")
		next("PATCH", "待定")
	}
	refresh()
	if feeds.Load() != 1 || oldReads.Load() != 1 || newReads.Load() != 0 || adds.Load() != 0 {
		t.Fatalf("failed enclosure unexpectedly acquired material: feeds=%d old=%d new=%d adds=%d", feeds.Load(), oldReads.Load(), newReads.Load(), adds.Load())
	}
	// The fixed source tracks download failures by tracker ID as well as
	// invalid enclosures by URL. The relist has a new ID but the same
	// signature so this isolates the URL eviction and deduplication path.
	relisted.Store(true)
	refresh()
	if feeds.Load() != 2 || oldReads.Load() != 1 || newReads.Load() != 1 || adds.Load() != 1 {
		t.Fatalf("same-title replacement did not use the new enclosure: feeds=%d old=%d new=%d adds=%d", feeds.Load(), oldReads.Load(), newReads.Load(), adds.Load())
	}
	t.Logf("explicit ID searched a paused subscription (tracker requests=%d; same revision and schedule); two RSS refreshes read the original 404 once, accepted the replacement bytes once and added one downloader task", searches.Load())
}
