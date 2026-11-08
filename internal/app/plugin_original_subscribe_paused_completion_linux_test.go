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
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOriginalLarkPausedSubscriptionCompletionLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	a, source, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	torrent, hash := workflowTorrent("测试影片 (2024) 1080p.mkv")
	var adds atomic.Int32

	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			_, _ = io.WriteString(w, workflowHTML("测试影片.2024.1080p.WEB-DL.x264-TEST", "", false))
		case "/download.php":
			_, _ = w.Write(torrent)
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
			} else {
				workflowJSON(w, []any{})
			}
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(4 << 20); err != nil || r.FormValue("savepath") != source {
				t.Error("unexpected Add save path", err)
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
			raw, err := io.ReadAll(part)
			if err != nil || !bytes.Equal(raw, torrent) {
				t.Error("Add did not contain the matched torrent")
				http.Error(w, "unexpected material", 400)
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
				"original_language": m.OriginalLanguage, "origin_country": m.OriginCountry, "production_countries": m.ProductionCountries, "genres": m.Genres,
				"poster_path": "/owned.jpg", "overview": m.Overview, "external_ids": map[string]string{"imdb_id": "tt1234567"}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(metadata.Close)
	transport := ownedLoopbackFixtureTransport(t, metadata)
	installMetadataFixture(t, a, "owned-source-paused-tmdb", []string{"/3/movie/123", "/3/search/movie"}, transport.RoundTrip)
	workflowConfigure(t, a, tracker.URL, qb.URL)
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

	type card struct{ method, content string }
	cards := make(chan card, 12)
	releaseNotice := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(releaseNotice) }) }
	defer release()
	lark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/open-apis")
		switch {
		case path == "/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-paused-fixture"}`)
		case strings.Contains(path, "/reactions"):
			_, _ = io.WriteString(w, `{"code":0,"data":{"reaction_id":"rx_fixture"}}`)
		case path == "/im/v1/messages" || path == "/im/v1/messages/om_paused":
			var in struct{ Content string }
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			select {
			case cards <- card{r.Method, in.Content}:
			default:
				t.Error("unexpected extra Lark card")
			}
			if strings.Contains(in.Content, "已完成订阅") {
				select {
				case <-releaseNotice:
				case <-r.Context().Done():
					return
				}
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_paused","chat_id":"oc_paused"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(lark.Close)
	a.pluginOriginalLarkTestOrigin = lark.URL + "/open-apis"
	a.Start(t.Context())
	admin := setupTestAdmin(t, a)
	installMergeSiteSwitchPackage(t, a, admin, bundle, "LarkMessager")
	api := a.Handler()
	const plugin = "/api/plugins/LarkMessager"
	result := callTest(t, api, "PUT", plugin, `{"enabled":true,"app_id":"owned-app","app_secret":"owned-secret","chat_id":"oc_paused","verification_token":"owned-verification","switchs":["插件","订阅"]}`, admin, "")
	assertOriginalRegistrySuccess(t, result.Code, result.Body.Bytes())
	post := func(eventType string, fields map[string]any) {
		t.Helper()
		raw := mustCleanSeedJSON(map[string]any{"header": map[string]any{"event_type": eventType}, "event": fields})
		result := callTest(t, api, "POST", plugin+"/api/webhook?token=owned-verification", string(raw), nil, "")
		var ack struct{ Success bool }
		if err := json.Unmarshal(result.Body.Bytes(), &ack); result.Code != 200 || err != nil || !ack.Success {
			t.Fatalf("webhook admission failed: %d %s", result.Code, result.Body.String())
		}
	}
	send := func(index int, text string) {
		t.Helper()
		post("im.message.receive_v1", map[string]any{
			"sender":  map[string]any{"sender_id": map[string]any{"open_id": "ou_owner"}},
			"message": map[string]any{"message_id": fmt.Sprintf("om_paused_%d", index), "chat_id": "oc_paused", "message_type": "text", "content": string(mustCleanSeedJSON(map[string]string{"text": text}))},
		})
	}
	next := func(method, fragment string) string {
		t.Helper()
		select {
		case got := <-cards:
			if got.method != method || !strings.Contains(got.content, fragment) {
				t.Fatalf("wanted %s with %q, got %s: %s", method, fragment, got.method, got.content)
			}
			return got.content
		case <-time.After(12 * time.Second):
			t.Fatalf("no %s card for %q", method, fragment)
			return ""
		}
	}
	send(1, "/subscribes")
	first := next("POST", "暂停")
	token := regexp.MustCompile(`subscribes:[0-9a-f]{12}:`).FindString(first)
	if token == "" {
		t.Fatal("subscription callback ID is missing")
	}
	post("card.action.trigger", map[string]any{
		"operator": map[string]any{"open_id": "ou_owner"}, "context": map[string]any{"open_message_id": "om_paused", "open_chat_id": "oc_paused"},
		"action": map[string]any{"value": map[string]string{"callback_data": token + "search"}},
	})
	next("PATCH", "当前操作：搜索订阅")
	send(2, "1")
	next("POST", "开始搜索订阅【测试影片】")
	next("POST", "已完成订阅")

	var live, histories, events int
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT
		(SELECT COUNT(*) FROM records WHERE kind='subscription' AND id=?),
		(SELECT COUNT(*) FROM records WHERE kind='subscription_history'),
		(SELECT COUNT(*) FROM records WHERE kind='source_subscribe_complete')`, paused.ID).Scan(&live, &histories, &events); err != nil || live != 0 || histories != 1 || events != 0 || adds.Load() != 1 {
		t.Fatalf("completion notice preceded accepted Add and history/delete, or event ran early: live=%d histories=%d events=%d adds=%d err=%v", live, histories, events, adds.Load(), err)
	}
	select {
	case got := <-cards:
		t.Fatalf("search replied before the completion notification returned: %s", got.content)
	case <-time.After(250 * time.Millisecond):
	}
	release()
	next("POST", "已完成 1 个订阅搜索")
	next("POST", "当前没有任何订阅")

	var key string
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT id FROM records WHERE kind='source_subscribe_complete'`).Scan(&key); err != nil {
		t.Fatal(err)
	}
	var receipt subscriptionCompletionReceipt
	if err := a.readQualitySealed(t.Context(), a.Store.DB, "subscription_completion", key, &receipt); err != nil {
		t.Fatal(err)
	}
	basis := receipt.Input.Ordinary
	var origin Subscription
	if err := json.Unmarshal(receipt.Input.Source.Subscription, &origin); err != nil || origin.State != "paused" || origin.Revision != paused.Revision ||
		receipt.Stage != "complete" || len(receipt.Input.Accepted) != 1 || basis == nil || len(basis.Inventory) != 0 {
		t.Fatalf("completion lacked paused, revision-bound, downloader-only proof: origin=%s stage=%s accepted=%d err=%v", origin.State, receipt.Stage, len(receipt.Input.Accepted), err)
	}
	t.Log("one confirmed Add completed the paused movie without inventory, persisted history and removed the live row before notifying, then replied and redrew after the notification returned")
}
