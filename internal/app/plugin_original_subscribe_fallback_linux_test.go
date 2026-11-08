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

func TestOriginalLarkSameSearchFallbackLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	a, source, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	rejectedTorrent, rejectedHash := workflowTorrent("测试影片 (2024) 2160p.mkv")
	acceptedTorrent, acceptedHash := workflowTorrent("测试影片 (2024) 1080p.mkv")
	firstRow := strings.Replace(workflowHTML("测试影片.2024.2160p.WEB-DL.x264-FIRST", "", false), ">12</a>", ">300</a>", 1)
	secondRow := strings.ReplaceAll(workflowHTML("测试影片.2024.1080p.WEB-DL.x264-SECOND", "", false), "id=101", "id=102")
	const rowStart = "<tr><td>类别"
	results := strings.TrimSuffix(firstRow, "</table>") + secondRow[strings.Index(secondRow, rowStart):]
	var refusedAdds, acceptedAdds, refusedMaterial, acceptedMaterial atomic.Int32

	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			if r.URL.Query().Get("search") == "" {
				t.Error("manual fallback browsed the tracker without a keyword")
			}
			_, _ = io.WriteString(w, results)
		case "/download.php":
			switch r.URL.Query().Get("id") {
			case "101":
				refusedMaterial.Add(1)
				_, _ = w.Write(rejectedTorrent)
			case "102":
				acceptedMaterial.Add(1)
				_, _ = w.Write(acceptedTorrent)
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
			if acceptedAdds.Load() > 0 {
				task := workflowTask(acceptedHash, 0)
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
			switch {
			case err == nil && bytes.Equal(raw, rejectedTorrent):
				if refusedAdds.Add(1) != 1 || acceptedAdds.Load() != 0 {
					t.Error("first choice was submitted more than once or out of order")
				}
				_, _ = io.WriteString(w, "Fails.")
			case err == nil && bytes.Equal(raw, acceptedTorrent):
				if acceptedAdds.Add(1) != 1 || refusedAdds.Load() != 1 {
					t.Error("fallback was submitted more than once or before refusal")
				}
				_, _ = io.WriteString(w, "Ok.")
			default:
				t.Error("Add did not contain a fixture torrent")
				http.Error(w, "unexpected material", 400)
			}
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
	type card struct{ method, content string }
	cards := make(chan card, 12)
	lark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/open-apis")
		switch {
		case path == "/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-fallback-fixture"}`)
		case strings.Contains(path, "/reactions"):
			_, _ = io.WriteString(w, `{"code":0,"data":{"reaction_id":"rx_fixture"}}`)
		case path == "/im/v1/messages" || path == "/im/v1/messages/om_fallback":
			var in struct{ Content string }
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			select {
			case cards <- card{r.Method, in.Content}:
			default:
				t.Error("unexpected extra Lark card")
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_fallback","chat_id":"oc_fallback"}}`)
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
	result := callTest(t, api, "PUT", plugin, `{"enabled":true,"app_id":"owned-app","app_secret":"owned-secret","chat_id":"oc_fallback","verification_token":"owned-verification","switchs":["插件","订阅"]}`, admin, "")
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
			"message": map[string]any{"message_id": fmt.Sprintf("om_fallback_%d", index), "chat_id": "oc_fallback", "message_type": "text", "content": string(mustCleanSeedJSON(map[string]string{"text": text}))},
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
	first := next("POST", "测试影片")
	token := regexp.MustCompile(`subscribes:[0-9a-f]{12}:`).FindString(first)
	if token == "" {
		t.Fatal("subscription callback ID is missing")
	}
	post("card.action.trigger", map[string]any{
		"operator": map[string]any{"open_id": "ou_owner"}, "context": map[string]any{"open_message_id": "om_fallback", "open_chat_id": "oc_fallback"},
		"action": map[string]any{"value": map[string]string{"callback_data": token + "search"}},
	})
	next("PATCH", "当前操作：搜索订阅")
	send(2, "1")
	next("POST", "开始搜索订阅【测试影片】")
	next("POST", "已完成订阅")
	next("POST", "已完成 1 个订阅搜索")
	next("POST", "当前没有任何订阅")

	if refusedAdds.Load() != 1 || acceptedAdds.Load() != 1 || refusedMaterial.Load() != 1 || acceptedMaterial.Load() != 1 {
		t.Fatalf("expected one Add and material request per candidate: rejected=%d/%d accepted=%d/%d", refusedAdds.Load(), refusedMaterial.Load(), acceptedAdds.Load(), acceptedMaterial.Load())
	}
	var children, roots, rejections, failures int
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT
		(SELECT COUNT(*) FROM jobs WHERE kind IN ('download','subscription_download') AND state IN ('failed','succeeded')
			AND json_extract(payload,'$._parent_job_id') IN (SELECT id FROM jobs WHERE kind='subscription')),
		(SELECT COUNT(DISTINCT json_extract(payload,'$._parent_job_id')) FROM jobs WHERE kind IN ('download','subscription_download')),
		(SELECT COUNT(*) FROM records WHERE kind='subscription_download_rejection'),
		(SELECT COUNT(*) FROM records WHERE kind='subscription_download_failure')`).Scan(&children, &roots, &rejections, &failures); err != nil || children != 2 || roots != 1 || rejections != 1 || failures != 1 {
		t.Fatalf("fallback escaped its original search or duplicated outcomes: jobs=%d roots=%d rejections=%d cooldowns=%d err=%v", children, roots, rejections, failures, err)
	}
	var firstState, secondState string
	var refusedReservations int
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT
		(SELECT json_extract(payload,'$.state') FROM records WHERE kind='intent' AND json_extract(payload,'$.hash')=?),
		(SELECT json_extract(payload,'$.state') FROM records WHERE kind='intent' AND json_extract(payload,'$.hash')=?),
		(SELECT COUNT(*) FROM reservations AS r JOIN records AS i ON i.id=r.intent_id AND i.kind='intent'
			WHERE json_extract(i.payload,'$.hash')=?)`, rejectedHash, acceptedHash, rejectedHash).Scan(&firstState, &secondState, &refusedReservations); err != nil || firstState != "failed" || secondState != "accepted" && secondState != "downloading" || refusedReservations != 0 {
		t.Fatalf("wrong refusal or acceptance state: first=%s second=%s rejectedReservations=%d err=%v", firstState, secondState, refusedReservations, err)
	}
	var live, completed int
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT
		(SELECT COUNT(*) FROM records WHERE kind='subscription' AND id=?),
		(SELECT COUNT(*) FROM records WHERE kind='source_subscribe_complete')`, sub.ID).Scan(&live, &completed); err != nil || live != 0 || completed != 1 {
		t.Fatalf("fallback acceptance did not finish the original subscription: live=%d completed=%d err=%v", live, completed, err)
	}
	t.Log("one manual search attempted two actual Add calls in order; refusal released the first intent and cooled only its resource; the second accepted resource finished the same subscription")
}
