package app

import (
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

func TestOriginalLarkSubscribeInteractionLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	t.Setenv("SUBSCRIBE_MODE", "spider")
	type card struct{ method, content string }
	cards := make(chan card, 24)
	lark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/open-apis")
		switch {
		case path == "/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-subscribe-fixture"}`)
		case strings.Contains(path, "/reactions"):
			_, _ = io.WriteString(w, `{"code":0,"data":{"reaction_id":"rx_fixture"}}`)
		case path == "/im/v1/messages" || path == "/im/v1/messages/om_subscribe":
			var in struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			select {
			case cards <- card{r.Method, in.Content}:
			default:
				t.Error("unexpected extra Lark card")
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_subscribe","chat_id":"oc_subscribe"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(lark.Close)
	var searches, latestPages, materials, adds atomic.Int32
	var latest atomic.Bool
	torrent, hash := workflowTorrent("测试影片 (2024) 1080p.mkv")
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			if latest.Load() {
				if r.URL.Query().Get("search") != "" {
					t.Error("latest resource refresh performed a tracker keyword search")
				}
				latestPages.Add(1)
				_, _ = io.WriteString(w, workflowHTML("测试影片.2024.1080p.WEB-DL.x264-TEST", "", false))
				return
			}
			searches.Add(1)
			_, _ = io.WriteString(w, workflowHTML("Different.Film.2024.1080p", "", false))
		case "/download.php":
			if !latest.Load() {
				t.Error("a nonmatching resource reached torrent material")
			}
			materials.Add(1)
			_, _ = w.Write(torrent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(tracker.Close)
	a, source, _ := importTestApp(t)
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			if adds.Load() > 0 {
				task := workflowTask(hash, 0)
				task["save_path"] = source
				workflowJSON(w, []any{task})
				return
			}
			workflowJSON(w, []any{})
		case "/api/v2/torrents/add":
			if !latest.Load() || r.ParseMultipartForm(4<<20) != nil || r.FormValue("savepath") != source {
				t.Error("unexpected downloader mutation or save path")
			}
			adds.Add(1)
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v5.0.0")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(qb.Close)
	a.pluginOriginalLarkTestOrigin = lark.URL + "/open-apis"
	m := importFixture(t, a, "movie")
	var metadataRequests atomic.Int32
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadataRequests.Add(1)
		switch r.URL.Path {
		case "/3/search/movie":
			workflowJSON(w, map[string]any{"page": 1, "total_pages": 1, "total_results": 1, "results": []map[string]any{
				{"id": m.ID, "title": m.Title, "original_title": m.OriginalTitle, "release_date": "2024-01-01"},
			}})
		case "/3/movie/123":
			workflowJSON(w, map[string]any{"id": m.ID, "title": m.Title, "original_title": m.OriginalTitle,
				"release_date": "2024-01-01", "original_language": m.OriginalLanguage, "origin_country": m.OriginCountry,
				"production_countries": m.ProductionCountries, "genres": m.Genres, "overview": m.Overview,
				"poster_path": "/source_poster.jpg", "backdrop_path": "/source_backdrop.jpg", "vote_average": 7.3,
				"external_ids": map[string]any{"imdb_id": "tt1234567"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(metadata.Close)
	ownedMetadata := ownedLoopbackFixtureTransport(t, metadata)
	installMetadataFixture(t, a, "owned-source-tmdb", []string{"/3/movie/123", "/3/search/movie"}, ownedMetadata.RoundTrip)
	workflowConfigure(t, a, tracker.URL, qb.URL)
	a.config.Downloaders[0].DownloadPath, a.config.Downloaders[0].SharedPaths = source, true
	a.settingsMu.Lock()
	err := a.saveSettings(t.Context(), a.Settings())
	a.settingsMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	request := SubscriptionRequest{MediaType: "movie", TMDBID: m.ID, DownloaderID: "fixture-qb", DownloadPath: source, RootID: "root", Schedule: Schedule{Mode: "manual"}}
	s, err := a.saveSubscription(t.Context(), "", request)
	if err != nil {
		t.Fatal(err)
	}
	s.CreatedAt = time.Now().Add(-2 * time.Minute).Format(time.RFC3339Nano)
	if err := a.Store.Put(t.Context(), "subscription", s.ID, s); err != nil {
		t.Fatal(err)
	}
	a.Start(t.Context())
	admin := setupTestAdmin(t, a)
	installMergeSiteSwitchPackage(t, a, admin, bundle, "LarkMessager")
	api := a.Handler()
	const plugin = "/api/plugins/LarkMessager"
	response := callTest(t, api, "PUT", plugin, `{"enabled":true,"app_id":"owned-app","app_secret":"owned-secret","chat_id":"oc_subscribe","verification_token":"owned-verification","switchs":["插件"]}`, admin, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	post := func(eventType string, fields map[string]any) {
		t.Helper()
		raw := mustCleanSeedJSON(map[string]any{"header": map[string]any{"event_type": eventType}, "event": fields})
		result := callTest(t, api, "POST", plugin+"/api/webhook?token=owned-verification", string(raw), nil, "")
		if result.Code != 200 || !strings.Contains(result.Body.String(), `"success":true`) {
			t.Fatalf("webhook admission failed: %d", result.Code)
		}
	}
	message := 0
	send := func(value string) {
		t.Helper()
		message++
		post("im.message.receive_v1", map[string]any{
			"sender":  map[string]any{"sender_id": map[string]any{"open_id": "ou_owner"}},
			"message": map[string]any{"message_id": fmt.Sprintf("om_input_%d", message), "chat_id": "oc_subscribe", "message_type": "text", "content": string(mustCleanSeedJSON(map[string]string{"text": value}))},
		})
	}
	click := func(value string) {
		t.Helper()
		post("card.action.trigger", map[string]any{
			"operator": map[string]any{"open_id": "ou_owner"}, "context": map[string]any{"open_message_id": "om_subscribe", "open_chat_id": "oc_subscribe"},
			"action": map[string]any{"value": map[string]string{"callback_data": value}},
		})
	}
	next := func(method string, fragments ...string) string {
		t.Helper()
		select {
		case result := <-cards:
			if result.method != method {
				t.Fatalf("unexpected %s card: %s", result.method, result.content)
			}
			for _, fragment := range fragments {
				if !strings.Contains(result.content, fragment) {
					t.Fatalf("card missing %q: %s", fragment, result.content)
				}
			}
			return result.content
		case <-time.After(12 * time.Second):
			t.Fatalf("no card for %v", fragments)
			return ""
		}
	}
	send("/sites")
	next("POST", "站点管理")
	send("/subscribes")
	first := next("POST", "订阅管理", "| 1 | 测试影片 | 电影 | 2024 | 电影 | 新建 |")
	token := regexp.MustCompile(`subscribes:[0-9a-f]{12}:`).FindString(first)
	if token == "" {
		t.Fatal("subscription callback ID is missing")
	}
	send("list")
	next("POST", "订阅管理", "第 1/1 页，共 1 个订阅")
	click(token + "search")
	next("PATCH", "当前操作：搜索订阅")
	send("１，999")
	next("POST", "开始搜索订阅【测试影片】...")
	next("POST", "已完成 1 个订阅搜索：测试影片；未找到：999")
	next("POST", "订阅中")
	var searchJob, state string
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT id,state FROM jobs WHERE kind='subscription' AND json_extract(payload,'$.id')=?`, s.ID).Scan(&searchJob, &state); err != nil || state != "succeeded" || searches.Load() == 0 {
		t.Fatalf("completion card did not follow an actual terminal search: %s %d %v", state, searches.Load(), err)
	}
	// Keep the test subscription in the source P state so accepted download
	// evidence does not complete the subscription before the delete scenario.
	var pending Subscription
	if err := a.Store.Get(t.Context(), "subscription", s.ID, &pending); err != nil {
		t.Fatal(err)
	}
	pending.SourceState = "P"
	if err := a.Store.Put(t.Context(), "subscription", s.ID, pending); err != nil {
		t.Fatal(err)
	}
	latest.Store(true)
	click(token + "refresh")
	next("POST", "开始刷新订阅...")
	next("POST", "订阅刷新执行完成")
	next("PATCH", "待定")
	if latestPages.Load() != 2 || materials.Load() != 1 || adds.Load() != 1 {
		t.Fatalf("refresh did not browse and accept exactly one torrent from its batch: pages=%d material=%d adds=%d", latestPages.Load(), materials.Load(), adds.Load())
	}
	var latestJobs int
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM jobs WHERE kind='subscription' AND state='succeeded'
		AND json_extract(payload,'$.id')=? AND json_extract(payload,'$.latest_batch') IS NOT NULL`, s.ID).Scan(&latestJobs); err != nil || latestJobs != 1 {
		t.Fatalf("cache match did not run through its own native job: %d %v", latestJobs, err)
	}
	paused, err := a.saveSubscriptionStatus(t.Context(), s.ID, "paused", s.Revision)
	if err != nil {
		t.Fatal(err)
	}
	priorSearches, priorMetadata := searches.Load(), metadataRequests.Load()
	click(token + "metadata")
	next("POST", "开始刷新订阅元数据...")
	next("POST", "订阅元数据刷新完成")
	next("PATCH", "暂停")
	var updated Subscription
	if err := a.Store.Get(t.Context(), "subscription", s.ID, &updated); err != nil || updated.State != "paused" || updated.Revision != paused.Revision || updated.Media.VoteAverage != 7.3 || metadataRequests.Load() <= priorMetadata || searches.Load() != priorSearches {
		t.Fatalf("metadata did not update the paused record independently: state=%s vote=%v searches=%d metadata=%d err=%v", updated.State, updated.Media.VoteAverage, searches.Load(), metadataRequests.Load(), err)
	}

	// An existing future task must be fenced by the same native transaction
	// that removes the subscription from chat and bumps its revision.
	tx, err := a.Store.DB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	job, err := enqueueTx(t.Context(), tx, "subscription", map[string]any{"id": s.ID, "revision": paused.Revision}, id("wf_"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `UPDATE jobs SET run_at=? WHERE id=?`, time.Now().Add(time.Hour).Unix(), job.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	click(token + "delete")
	next("PATCH", "当前操作：删除订阅")
	send("1, 1")
	next("POST", "已删除 1 个订阅：测试影片；未找到：1")
	next("POST", "当前没有任何订阅")
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT state FROM jobs WHERE id=?`, job.ID).Scan(&state); err != nil || state != "cancelled" {
		t.Fatalf("deletion did not fence existing native job: %s %v", state, err)
	}
	readded, err := a.saveSubscription(t.Context(), "", request)
	if err != nil {
		t.Fatal(err)
	}
	nextAlias, err := a.Store.originalSubscriptionByAlias(t.Context(), 2)
	if err != nil || nextAlias.ID != readded.ID || nextAlias.ID == s.ID {
		t.Fatalf("deleted alias was reused or replaced: %s %v", nextAlias.ID, err)
	}
	t.Log("indexed Lark /subscribes reused native numeric aliases, chose the newest slash session, waited for actual keyword search and cache-based Add, refreshed paused metadata separately, and atomically fenced deletion without alias reuse")
}
