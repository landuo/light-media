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

func TestOriginalLarkMediaInteractionLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	a, source, _ := importTestApp(t)
	m := importFixture(t, a, "movie")

	cards := make(chan string, 20)
	lark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch path := strings.TrimPrefix(r.URL.Path, "/open-apis"); {
		case path == "/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-media-fixture"}`)
		case strings.Contains(path, "/reactions"):
			_, _ = io.WriteString(w, `{"code":0,"data":{"reaction_id":"rx_fixture"}}`)
		case path == "/im/v1/messages" && r.Method == http.MethodPost:
			var in struct{ Content string }
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			select {
			case cards <- in.Content:
			default:
				t.Error("unexpected extra Lark card")
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_media","chat_id":"oc_media"}}`)
		default:
			t.Errorf("unexpected Lark request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(lark.Close)
	a.pluginOriginalLarkTestOrigin = lark.URL + "/open-apis"

	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/movie", "/3/search/multi":
			rows := []map[string]any{{"id": m.ID, "media_type": "movie", "title": m.Title, "original_title": m.OriginalTitle, "release_date": "2024-01-01"}}
			for i := 1; i <= 8; i++ {
				rows = append(rows, map[string]any{"id": m.ID + int64(i), "media_type": "movie", "title": fmt.Sprintf("测试影片候选%d", i), "release_date": "2024-01-01"})
			}
			workflowJSON(w, map[string]any{"page": 1, "total_pages": 1, "total_results": len(rows), "results": rows})
		case "/3/search/tv":
			workflowJSON(w, map[string]any{"page": 1, "total_pages": 1, "total_results": 0, "results": []any{}})
		case "/3/movie/123/images":
			workflowJSON(w, map[string]any{"id": m.ID, "posters": []any{}, "backdrops": []any{}, "logos": []any{}})
		case "/3/movie/123":
			workflowJSON(w, map[string]any{"id": m.ID, "title": m.Title, "original_title": m.OriginalTitle,
				"release_date": "2024-01-01", "original_language": m.OriginalLanguage, "origin_country": m.OriginCountry,
				"production_countries": m.ProductionCountries, "genres": m.Genres, "overview": m.Overview,
				"poster_path": "/source_poster.jpg", "backdrop_path": "/source_backdrop.jpg",
				"external_ids": map[string]any{"imdb_id": "tt1234567"},
			})
		default:
			t.Errorf("unexpected metadata request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(metadata.Close)
	installMetadataFixture(t, a, "owned-source-tmdb", []string{"/3/movie/123", "/3/movie/123/images", "/3/search/movie", "/3/search/multi", "/3/search/tv"}, ownedLoopbackFixtureTransport(t, metadata).RoundTrip)

	const release = "测试影片.2024.1080p.WEB-DL.x264-TEST"
	torrent, hash := workflowTorrent("测试影片 (2024) 1080p.mkv")
	var searches, materials, adds atomic.Int32
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			searches.Add(1)
			_, _ = io.WriteString(w, workflowHTML(release, "", false))
		case "/download.php":
			materials.Add(1)
			_, _ = w.Write(torrent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(tracker.Close)
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			if adds.Load() == 0 {
				workflowJSON(w, []any{})
				return
			}
			task := workflowTask(hash, 0)
			task["save_path"] = source
			workflowJSON(w, []any{task})
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(4 << 20); err != nil || r.FormValue("savepath") != source {
				t.Errorf("invalid downloader submission: savepath=%q, err=%v", r.FormValue("savepath"), err)
			}
			file, _, err := r.FormFile("torrents")
			if err != nil {
				t.Error(err)
			} else {
				defer file.Close()
				data, err := io.ReadAll(file)
				if err != nil || !bytes.Equal(data, torrent) {
					t.Error("downloader did not receive fixture torrent material")
				}
			}
			adds.Add(1)
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/app/preferences":
			workflowJSON(w, map[string]string{"save_path": source})
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v5.0.0")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(qb.Close)
	workflowConfigure(t, a, tracker.URL, qb.URL)
	a.config.Downloaders[0].DownloadPath, a.config.Downloaders[0].SharedPaths = source, true
	a.settingsMu.Lock()
	err := a.saveSettings(t.Context(), a.Settings())
	a.settingsMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	a.Start(t.Context())
	admin := setupTestAdmin(t, a)
	installMergeSiteSwitchPackage(t, a, admin, bundle, "LarkMessager")
	api := a.Handler()
	const plugin = "/api/plugins/LarkMessager"
	configured := callTest(t, api, "PUT", plugin, `{"enabled":true,"app_id":"owned-app","app_secret":"owned-secret","chat_id":"oc_media","verification_token":"owned-verification","switchs":["插件","资源下载","订阅"]}`, admin, "")
	assertOriginalRegistrySuccess(t, configured.Code, configured.Body.Bytes())
	post := func(eventType string, fields map[string]any) {
		t.Helper()
		raw := mustCleanSeedJSON(map[string]any{"header": map[string]any{"event_type": eventType}, "event": fields})
		result := callTest(t, api, "POST", plugin+"/api/webhook?token=owned-verification", string(raw), nil, "")
		if result.Code != 200 || !strings.Contains(result.Body.String(), `"success":true`) {
			t.Fatalf("webhook admission failed: %d", result.Code)
		}
	}
	send := func(seq int, value string) {
		t.Helper()
		post("im.message.receive_v1", map[string]any{
			"sender":  map[string]any{"sender_id": map[string]any{"open_id": "ou_owner"}},
			"message": map[string]any{"message_id": fmt.Sprintf("om_input_%d", seq), "chat_id": "oc_media", "message_type": "text", "content": string(mustCleanSeedJSON(map[string]string{"text": value}))},
		})
	}
	click := func(user, value string) {
		t.Helper()
		post("card.action.trigger", map[string]any{
			"operator": map[string]any{"open_id": user}, "context": map[string]any{"open_message_id": "om_media", "open_chat_id": "oc_media"},
			"action": map[string]any{"value": map[string]string{"callback_data": value}},
		})
	}
	next := func(fragments ...string) string {
		t.Helper()
		select {
		case card := <-cards:
			for _, fragment := range fragments {
				if !strings.Contains(card, fragment) {
					t.Fatalf("card missing %q: %s", fragment, card)
				}
			}
			return card
		case <-time.After(12 * time.Second):
			t.Fatalf("no card for %v", fragments)
			return ""
		}
	}
	token := regexp.MustCompile(`media:[0-9a-f]{12}:`)
	send(1, "搜索 测试影片")
	card := next("共找到9条相关信息", "测试影片", "page-next")
	request := token.FindString(card)
	if request == "" || strings.Contains(card, "测试影片候选8") {
		t.Fatalf("first page did not have an eight-result callback: %s", card)
	}
	click("ou_other", request+"select:1")
	next("交互已失效，请重新搜索或订阅")
	if searches.Load() != 0 {
		t.Fatal("the other user triggered a tracker search")
	}
	click("ou_owner", request+"page-next")
	next("测试影片候选8", "page-prev")
	send(2, "上一页")
	next("测试影片", "page-next")
	click("ou_owner", request+"select:1")
	next("开始搜索 电影 测试影片 (2024)")
	next("共找到1条相关资源", release, request+"download:1")
	click("ou_owner", request+"download:1")
	next("测试影片", "开始下载")
	var state string
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT state FROM jobs WHERE kind='download'`).Scan(&state); err != nil || state != "succeeded" || searches.Load() < 1 || materials.Load() != 1 || adds.Load() != 1 {
		t.Fatalf("no actual accepted download: state=%s tracker=%d material=%d adds=%d err=%v", state, searches.Load(), materials.Load(), adds.Load(), err)
	}
	send(3, "订阅 测试影片")
	card = next("共找到9条相关信息")
	addRequest := token.FindString(card)
	if addRequest == "" || addRequest == request {
		t.Fatal("successful new search did not replace the previous interaction")
	}
	click("ou_owner", request+"download:1")
	next("交互已失效，请重新搜索或订阅")
	send(4, "１")
	next("测试影片", "订阅")
	var subscriptionID string
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := a.Store.DB.QueryRowContext(t.Context(), `SELECT json_extract(payload,'$.subscription_id') FROM domain_events
			WHERE json_extract(payload,'$.event_type')='subscription.added'`).Scan(&subscriptionID)
		if err == nil && subscriptionID != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("chat subscription did not emit the durable native Add event: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	var saved Subscription
	if err := a.Store.Get(t.Context(), "subscription", subscriptionID, &saved); err != nil || saved.Media.ID != m.ID || saved.SourceState != "N" || adds.Load() != 1 {
		t.Fatalf("wrong persisted subscription or stale-card download: source_state=%s media_id=%d adds=%d err=%v", saved.SourceState, saved.Media.ID, adds.Load(), err)
	}
	t.Logf("verified actual indexed Lark worker: 9 metadata results across 2 pages; other user rejected; %d tracker requests, 1 exact torrent fetch, 1 accepted native download; old callback expired, Unicode numeric selection created N and durable SubscribeAdded", searches.Load())
}
