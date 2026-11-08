package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/metainfo"
)

func TestOriginalLarkTVMissingEpisodeLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	t.Setenv("SUBSCRIBE_MODE", "spider")
	a, source, _ := importTestApp(t)
	m := importFixture(t, a, "tv")
	// Start with the native inventory already containing episodes 1 and 4.
	for _, ep := range []int{1, 4} {
		if _, err := a.Store.DB.ExecContext(t.Context(), `INSERT INTO asset_slots VALUES(?,?,?,?)`, mediaKey(m), 1, ep, "owned-existing-slot"); err != nil {
			t.Fatal(err)
		}
	}

	cards := make(chan string, 12)
	lark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch path := strings.TrimPrefix(r.URL.Path, "/open-apis"); {
		case path == "/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-tv-fixture"}`)
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
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_tv","chat_id":"oc_tv"}}`)
		default:
			t.Errorf("unexpected Lark request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(lark.Close)
	a.pluginOriginalLarkTestOrigin = lark.URL + "/open-apis"

	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/tv", "/3/search/multi":
			workflowJSON(w, map[string]any{"page": 1, "total_pages": 1, "total_results": 1, "results": []map[string]any{
				{"id": m.ID, "media_type": "tv", "name": m.Title, "original_name": m.Title, "first_air_date": "2024-01-01"},
			}})
		case "/3/search/movie":
			workflowJSON(w, map[string]any{"page": 1, "total_pages": 1, "total_results": 0, "results": []any{}})
		case "/3/tv/123":
			workflowJSON(w, map[string]any{"id": m.ID, "name": m.Title, "original_name": m.Title, "first_air_date": "2024-01-01",
				"original_language": m.OriginalLanguage, "origin_country": m.OriginCountry, "production_countries": m.ProductionCountries,
				"genres": m.Genres, "overview": m.Overview, "number_of_seasons": 1, "number_of_episodes": 4,
				"seasons":      []any{map[string]any{"id": 340, "season_number": 1, "episode_count": 4, "air_date": "2024-01-01"}},
				"external_ids": map[string]any{"imdb_id": "tt1234567"},
			})
		case "/3/tv/123/season/1":
			var episodes []map[string]any
			for n := 1; n <= 4; n++ {
				episodes = append(episodes, map[string]any{"id": 501 + n, "season_number": 1, "episode_number": n, "name": "测试分集", "air_date": "2024-01-01"})
			}
			workflowJSON(w, map[string]any{"id": 340, "season_number": 1, "air_date": "2024-01-01", "episodes": episodes})
		case "/3/tv/123/images":
			workflowJSON(w, map[string]any{"id": m.ID, "posters": []any{}, "backdrops": []any{}, "logos": []any{}})
		default:
			t.Errorf("unexpected metadata request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(metadata.Close)
	paths := []string{"/3/search/tv", "/3/search/multi", "/3/search/movie", "/3/tv/123", "/3/tv/123/season/1", "/3/tv/123/images"}
	installMetadataFixture(t, a, "owned-source-tmdb", paths, ownedLoopbackFixtureTransport(t, metadata).RoundTrip)

	torrent := automaticPackTorrent([]string{
		"测试剧集 S01E01 1080p.mkv", "测试剧集 S01E02 1080p.mkv", "测试剧集 S01E04 1080p.mkv", "readme.txt",
	})
	material, err := metainfo.ParseMaterial(torrent)
	if err != nil {
		t.Fatal(err)
	}
	const release = "测试剧集.S01.2024.1080p.WEB-DL.x264-TEST"
	var materials, adds, priorities, starts atomic.Int32
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
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
			task := workflowTask(material.Hash, 0)
			task["save_path"] = source
			workflowJSON(w, []any{task})
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(4 << 20); err != nil || r.FormValue("savepath") != source || r.FormValue("stopped") != "true" {
				t.Errorf("partial pack must be added stopped to the resolved directory: %v", err)
			}
			file, _, err := r.FormFile("torrents")
			if err != nil {
				t.Error(err)
			} else {
				defer file.Close()
				data, err := io.ReadAll(file)
				if err != nil || !bytes.Equal(data, torrent) {
					t.Error("Add did not reuse the inspected pack")
				}
			}
			adds.Add(1)
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/torrents/files":
			_, _ = io.WriteString(w, `[{"index":7,"name":"测试剧集 S01E01 1080p.mkv"},{"index":3,"name":"测试剧集 S01E02 1080p.mkv"},{"index":23,"name":"测试剧集 S01E04 1080p.mkv"},{"index":99,"name":"readme.txt"}]`)
		case "/api/v2/torrents/filePrio":
			if err := r.ParseForm(); err != nil || r.Form.Get("hash") != material.Hash || r.Form.Get("id") != "7|23|99" || r.Form.Get("priority") != "0" {
				t.Errorf("wrong qB file priorities: %v %v", r.Form, err)
			}
			priorities.Add(1)
		case "/api/v2/torrents/start":
			if err := r.ParseForm(); err != nil || r.Form.Get("hashes") != material.Hash || priorities.Load() != 1 {
				t.Errorf("Start preceded setting unwanted file priorities: %v %v", r.Form, err)
			}
			starts.Add(1)
		case "/api/v2/app/preferences":
			workflowJSON(w, map[string]string{"save_path": source})
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v5.0.0")
		case "/api/v2/auth/login":
			_, _ = io.WriteString(w, "Ok.")
		default:
			t.Errorf("unexpected downloader request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(qb.Close)
	workflowConfigure(t, a, tracker.URL, qb.URL)
	a.config.Downloaders[0].DownloadPath, a.config.Downloaders[0].SharedPaths = source, true
	if err := a.saveSettings(t.Context(), a.Settings()); err != nil {
		t.Fatal(err)
	}
	a.Start(t.Context())
	admin := setupTestAdmin(t, a)
	installMergeSiteSwitchPackage(t, a, admin, bundle, "LarkMessager")
	api := a.Handler()
	const plugin = "/api/plugins/LarkMessager"
	configured := callTest(t, api, "PUT", plugin, `{"enabled":true,"app_id":"owned-app","app_secret":"owned-secret","chat_id":"oc_tv","verification_token":"owned-verification","switchs":["资源下载","订阅"]}`, admin, "")
	assertOriginalRegistrySuccess(t, configured.Code, configured.Body.Bytes())

	post := func(eventType string, fields map[string]any) {
		t.Helper()
		raw := mustCleanSeedJSON(map[string]any{"header": map[string]any{"event_type": eventType}, "event": fields})
		result := callTest(t, api, "POST", plugin+"/api/webhook?token=owned-verification", string(raw), nil, "")
		var acknowledgement struct{ Success bool }
		if result.Code != 200 || json.Unmarshal(result.Body.Bytes(), &acknowledgement) != nil || !acknowledgement.Success {
			t.Fatalf("webhook admission failed: %d %s", result.Code, result.Body.String())
		}
	}
	click := func(value string) {
		t.Helper()
		post("card.action.trigger", map[string]any{
			"operator": map[string]any{"open_id": "ou_owned_tv"}, "context": map[string]any{"open_message_id": "om_tv", "open_chat_id": "oc_tv"},
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
	post("im.message.receive_v1", map[string]any{
		"sender":  map[string]any{"sender_id": map[string]any{"open_id": "ou_owned_tv"}},
		"message": map[string]any{"message_id": "om_search", "chat_id": "oc_tv", "message_type": "text", "content": string(mustCleanSeedJSON(map[string]string{"text": "测试剧集 第1季"}))},
	})
	first := next("共找到1条相关信息")
	request := regexp.MustCompile(`media:[0-9a-f]{12}:`).FindString(first)
	if request == "" {
		t.Fatal("no TV media selection callback")
	}
	click(request + "select:1")
	next("第 1 季缺失 2-3 集")
	next("开始搜索 电视剧")
	next("共找到1条相关资源", "自动选择下载", release)
	click(request + "download:0")
	next("开始下载", "E02")
	if materials.Load() != 1 || adds.Load() != 1 || priorities.Load() != 1 || starts.Load() != 1 {
		t.Fatalf("incorrect partial pack effects: materials=%d adds=%d priorities=%d starts=%d", materials.Load(), adds.Load(), priorities.Load(), starts.Load())
	}
	var intentID string
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT id FROM records WHERE kind='intent'`).Scan(&intentID); err != nil {
		t.Fatal(err)
	}
	var intent Intent
	if err := a.Store.Get(t.Context(), "intent", intentID, &intent); err != nil || intent.State != "accepted" || !slices.Equal(intent.SelectedEpisodes, []int{2}) {
		t.Fatalf("wrong sealed file selection: state=%s episodes=%v err=%v", intent.State, intent.SelectedEpisodes, err)
	}
	if receipt, err := a.sourcePostAddReceipt(t.Context(), a.Store.DB, intent); err != nil || !receipt.SourceReturned || !receipt.Selection.Started || !slices.Equal(receipt.Selection.Episodes, []int{2}) {
		t.Fatalf("native post-Add receipt did not confirm E02: %+v %v", receipt, err)
	}
	next("测试剧集", "订阅")
	var subID string
	deadline := time.Now().Add(4 * time.Second)
	for {
		err := a.Store.DB.QueryRowContext(t.Context(), `SELECT json_extract(payload,'$.subscription_id') FROM domain_events
			WHERE json_extract(payload,'$.event_type')='subscription.added'`).Scan(&subID)
		if err == nil && subID != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no native fallback SubscribeAdded: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	var fallback Subscription
	if err := a.Store.Get(t.Context(), "subscription", subID, &fallback); err != nil || fallback.Media.ID != m.ID || fallback.Season != 1 || fallback.SourceState != "R" {
		t.Fatalf("uncovered episode did not leave an R subscription for season one: %+v %v", fallback, err)
	}
	raw, err := a.pluginStore.readSubscriptionCompletionSealed(t.Context(), a.Store.DB, "source_subscription_origin", fallback.ID)
	var origin struct {
		Note []int `json:"note"`
	}
	if err != nil || json.Unmarshal(raw, &origin) != nil || !slices.Equal(origin.Note, []int{1}) {
		t.Fatalf("fallback must retain the original pack's actual begin-episode note: %v %v", origin.Note, err)
	}
	t.Log("owned E01/E04 inventory produced a 2–3 missing notice; one S01 pack supplied E02 only via actual paused Add, priority skip and Start; E03 remained with an R fallback subscription and source note [1]")
}
