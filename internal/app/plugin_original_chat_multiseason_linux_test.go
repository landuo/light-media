package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/metainfo"
)

func TestOriginalLarkWholeMultiSeasonLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	t.Setenv("SUBSCRIBE_MODE", "spider")
	a, source, _ := importTestApp(t)
	m := importFixture(t, a, "tv")
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
				"genres": m.Genres, "overview": m.Overview, "number_of_seasons": 2, "number_of_episodes": 2,
				"seasons": []any{
					map[string]any{"id": 341, "season_number": 1, "episode_count": 1, "air_date": "2024-01-01"},
					map[string]any{"id": 342, "season_number": 2, "episode_count": 1, "air_date": "2024-02-01"},
				},
				"external_ids": map[string]any{"imdb_id": "tt1234567"},
			})
		case "/3/tv/123/season/1", "/3/tv/123/season/2":
			season := 1
			if strings.HasSuffix(r.URL.Path, "/2") {
				season = 2
			}
			episodes := []map[string]any{{"id": 501 + season, "season_number": season, "episode_number": 1, "name": "测试分集", "air_date": "2024-01-01"}}
			workflowJSON(w, map[string]any{"id": 340 + season, "season_number": season, "air_date": "2024-01-01", "episodes": episodes})
		case "/3/tv/123/images":
			workflowJSON(w, map[string]any{"id": m.ID, "posters": []any{}, "backdrops": []any{}, "logos": []any{}})
		default:
			t.Errorf("unexpected metadata request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(metadata.Close)
	paths := []string{"/3/search/tv", "/3/search/multi", "/3/search/movie", "/3/tv/123", "/3/tv/123/season/1", "/3/tv/123/season/2", "/3/tv/123/images"}
	installMetadataFixture(t, a, "owned-source-tmdb", paths, ownedLoopbackFixtureTransport(t, metadata).RoundTrip)

	const pack = "测试剧集 S01-S02"
	names := []string{"测试剧集 S01E01 1080p.mkv", "测试剧集 S02E01 1080p.mkv"}
	var files []any
	for _, name := range names {
		files = append(files, map[string]any{"length": 1024, "path": []any{name}})
	}
	torrent := workflowBencode(map[string]any{"info": map[string]any{"name": pack, "piece length": 16384, "pieces": bytes.Repeat([]byte{'p'}, 20), "files": files}})
	material, err := metainfo.ParseMaterial(torrent)
	if err != nil {
		t.Fatal(err)
	}
	const release = "测试剧集.S01-S02.2024.1080p.WEB-DL.x264-TEST"
	var materials, adds atomic.Int32
	var finished atomic.Bool
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
			task["content_path"] = filepath.Join(source, pack)
			if finished.Load() {
				task["progress"], task["state"], task["total_size"], task["completed"] = 1, "stalledUP", 2048, 2048
			}
			workflowJSON(w, []any{task})
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(4 << 20); err != nil || r.FormValue("savepath") != source || (r.FormValue("stopped") == "true" || r.FormValue("paused") == "true") {
				t.Errorf("complete multiseason pack must be added running to the resolved directory: %v", err)
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
			if !finished.Load() {
				t.Error("complete multi-season Add requested episode selection")
			}
			var listing []map[string]any
			for index, name := range names {
				listing = append(listing, map[string]any{"index": index, "name": pack + "/" + name, "size": 1024, "progress": 1})
			}
			workflowJSON(w, listing)
		case "/api/v2/torrents/filePrio", "/api/v2/torrents/start":
			t.Error("complete multi-season pack must not split or restart its downloader task")
			http.Error(w, "unexpected episode mutation", 400)
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
	a.config.Directories = []DirectoryRule{{
		ID: "owned-multi-season", Name: "跨季合集", SourcePath: source, LibraryPath: a.Settings().Roots[0].Path,
		RootID: "root", MediaType: "tv", Mode: "hardlink", MonitorType: "downloader", Enabled: true,
	}}
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
		"message": map[string]any{"message_id": "om_search", "chat_id": "oc_tv", "message_type": "text", "content": string(mustCleanSeedJSON(map[string]string{"text": "测试剧集"}))},
	})
	first := next("共找到1条相关信息")
	request := regexp.MustCompile(`media:[0-9a-f]{12}:`).FindString(first)
	if request == "" {
		t.Fatal("no TV media selection callback")
	}
	click(request + "select:1")
	next("第 1 季", "第 2 季")
	next("开始搜索 电视剧")
	next("共找到1条相关资源", "自动选择下载", release)
	click(request + "download:0")
	next("开始下载")
	var intentID string
	deadline := time.Now().Add(5 * time.Second)
	var intent Intent
	for {
		err := a.Store.DB.QueryRowContext(t.Context(), `SELECT id FROM records WHERE kind='intent'`).Scan(&intentID)
		if err == nil {
			err = a.Store.Get(t.Context(), "intent", intentID, &intent)
		}
		if err == nil && intent.State == "accepted" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("whole multi-season Add did not reach acceptance: state=%s err=%v", intent.State, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	proof, err := a.intentSeasonProof(t.Context(), a.Store.DB, intent)
	if err != nil || proof == nil || len(proof.Scopes) != 2 || len(proof.Files) != 2 ||
		proof.Scopes[0].Season != 1 || proof.Scopes[1].Season != 2 ||
		!slices.Equal(proof.Scopes[0].Episodes, []int{1}) || !slices.Equal(proof.Scopes[1].Episodes, []int{1}) ||
		adds.Load() != 1 || materials.Load() != 1 || len(intent.SelectedEpisodes) != 0 {
		t.Fatalf("one Add did not freeze two distinct season scopes: proof=%+v material=%d add=%d err=%v", proof, materials.Load(), adds.Load(), err)
	}
	if err := os.Mkdir(filepath.Join(source, pack), 0750); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		importWrite(t, filepath.Join(source, pack, name), bytes.Repeat([]byte{0}, 1024))
	}
	finished.Store(true)
	if err := a.pollDownloads(t.Context()); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(6 * time.Second)
	for {
		if err := a.Store.Get(t.Context(), "intent", intentID, &intent); err != nil {
			t.Fatal(err)
		}
		if intent.State == "imported" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("download completion did not publish both files: state=%s error=%s", intent.State, intent.Error)
		}
		time.Sleep(25 * time.Millisecond)
	}
	for i, name := range names {
		var assetID string
		if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT asset_id FROM asset_slots WHERE media_key=? AND season=? AND episode=1`, mediaKey(m), i+1).Scan(&assetID); err != nil {
			t.Fatal(err)
		}
		var asset Asset
		if err := a.Store.Get(t.Context(), "asset", assetID, &asset); err != nil {
			t.Fatal(err)
		}
		original, srcErr := os.Stat(filepath.Join(source, pack, name))
		linked, dstErr := os.Stat(asset.Path)
		if srcErr != nil || dstErr != nil || !os.SameFile(original, linked) {
			t.Fatalf("season %d did not retain its own hardlinked asset: %v %v", i+1, srcErr, dstErr)
		}
	}
	var subscriptions, intents int
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM records WHERE kind='subscription'), (SELECT COUNT(*) FROM records WHERE kind='intent')`).Scan(&subscriptions, &intents); err != nil || subscriptions != 0 || intents != 1 || adds.Load() != 1 {
		t.Fatalf("whole pack split or created a fallback: subscriptions=%d intents=%d adds=%d err=%v", subscriptions, intents, adds.Load(), err)
	}
	t.Log("two wholly missing seasons used one real Add without file selection, then published S01E01 and S02E01 as two distinct local hardlinks")
}
