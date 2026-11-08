package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/priorityrules"
	"light-media/internal/sites"
)

func TestOriginalLarkRSSMagnetLinuxAMD64(t *testing.T) {
	testOriginalLarkRSSMagnet(t, "movie")
}

func TestOriginalLarkRSSSingleSeasonMagnetLinuxAMD64(t *testing.T) {
	testOriginalLarkRSSMagnet(t, "episode")
}

func TestOriginalLarkRSSQualityMagnetLinuxAMD64(t *testing.T) {
	testOriginalLarkRSSMagnet(t, "quality")
}

func TestOriginalLarkRSSQualityFullMagnetLinuxAMD64(t *testing.T) {
	testOriginalLarkRSSMagnet(t, "quality_full")
}

func testOriginalLarkRSSMagnet(t *testing.T, mode string) {
	t.Helper()
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" || os.Getenv("OWNED_TRANSFER_EVENTS_BINARY") == "" {
		t.Skip("requires indexed Linux amd64 LarkMessager and SDK event observer")
	}
	t.Setenv("MP_SERVER_HOST", "")
	t.Setenv("SUBSCRIBE_MODE", "rss")
	a, source, library := importTestApp(t)
	full := mode == "quality_full"
	series, quality := mode != "movie", mode == "quality" || full
	kind, filename := "movie", "测试影片 (2024) 1080p.mkv"
	if series {
		kind, filename = "tv", "测试剧集 S01E02 1080p.mkv"
	}
	if quality {
		filename = "测试剧集 S01E02 2160p.mkv"
	}
	m := importFixture(t, a, kind)
	const hash = "0123456789abcdef0123456789abcdef01234567"
	const ownedTracker = "https://tracker.invalid/owned-private-rss-token"
	magnet := "magnet:?xt=urn:btih:" + hash + "&tr=" + url.QueryEscape(ownedTracker)
	const packMagnet = "magnet:?xt=urn:btih:1111111111111111111111111111111111111111"
	const extraFilename = "测试剧集 S01E01 2160p.mkv"
	var adds, feeds, singleReads, multiReads, packReads atomic.Int32
	var finished atomic.Bool
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rss.php":
			feeds.Add(1)
			if r.Header.Get("Cookie") != "" {
				t.Error("RSS feed inherited site login")
			}
			w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
			if full {
				_, _ = io.WriteString(w, `<rss version="2.0"><channel>
<item><title>测试剧集.S01E01-E02.2024.2160p.WEB-DL.x264-FULL</title><pubDate>Sat, 31 Oct 2026 00:00:00 GMT</pubDate><link>details.php?id=401</link><enclosure url="/owned-single-material" length="2048"/></item>
</channel></rss>`)
				return
			}
			if quality {
				_, _ = io.WriteString(w, `<rss version="2.0"><channel>
<item><title>测试剧集.S01.2024.2160p.WEB-DL.x264-PACK</title><pubDate>Sat, 31 Oct 2026 00:01:00 GMT</pubDate><link>details.php?id=301</link><enclosure url="/owned-quality-pack" length="2048"/></item>
<item><title>测试剧集.S01E02.2024.2160p.WEB-DL.x264-TEST</title><pubDate>Sat, 31 Oct 2026 00:00:00 GMT</pubDate><link>details.php?id=302</link><enclosure url="/owned-single-material" length="1024"/></item>
</channel></rss>`)
				return
			}
			if series {
				_, _ = io.WriteString(w, `<rss version="2.0"><channel>
<item><title>测试剧集.S01-S02.2024.2160p.WEB-DL.x264-MULTI</title><pubDate>Sat, 31 Oct 2026 00:01:00 GMT</pubDate><link>details.php?id=201</link><enclosure url="/owned-multi-material" length="2048"/></item>
<item><title>测试剧集.S01E02.2024.1080p.WEB-DL.x264-TEST</title><pubDate>Sat, 31 Oct 2026 00:00:00 GMT</pubDate><link>details.php?id=202</link><enclosure url="/owned-single-material" length="1024"/></item>
</channel></rss>`)
				return
			}
			xmlMagnet := strings.ReplaceAll(magnet, "&", "&amp;")
			_, _ = fmt.Fprintf(w, `<rss version="2.0"><channel><item><title>测试影片.2024.1080p.WEB-DL.x264-TEST</title><link>details.php?id=101</link><enclosure url="%s" length="1024" type="application/x-bittorrent"/></item></channel></rss>`, xmlMagnet)
		case "/owned-multi-material":
			multiReads.Add(1)
			http.NotFound(w, r)
		case "/owned-quality-pack":
			packReads.Add(1)
			_, _ = io.WriteString(w, packMagnet)
		case "/owned-single-material":
			singleReads.Add(1)
			if r.Header.Get("Cookie") != "fixture-cookie" {
				t.Error("same-origin enclosure did not retain the configured cookie")
			}
			w.Header().Set("Location", magnet)
			w.WriteHeader(http.StatusFound)
		case "/magnet-redirect":
			w.Header().Set("Location", magnet)
			w.WriteHeader(http.StatusFound)
		case "/magnet-body":
			_, _ = io.WriteString(w, magnet)
		default:
			t.Error("direct RSS magnet made an unexpected tracker request", r.URL.Path)
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
				total := 1024
				if full {
					total = 2048
					task["total_size"] = total
				}
				if finished.Load() {
					task["progress"], task["state"], task["completed"] = 1, "stalledUP", total
				}
				workflowJSON(w, []any{task})
			} else {
				workflowJSON(w, []any{})
			}
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(4 << 20); err != nil || r.FormValue("savepath") != source ||
				r.FormValue("urls") != magnet || len(r.MultipartForm.File["torrents"]) != 0 || r.Header.Get("Cookie") != "" || quality && r.FormValue("stopped") != "false" {
				t.Error("qB Add did not contain the exact typed magnet and local path")
				http.Error(w, "unexpected Add", 400)
				return
			}
			adds.Add(1)
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/torrents/files":
			if !finished.Load() || r.URL.Query().Get("hash") != hash {
				t.Error("files requested before known hash was complete")
			}
			files := []any{map[string]any{"index": 0, "name": filename, "size": 1024, "progress": 1}}
			if full {
				files = append(files, map[string]any{"index": 1, "name": extraFilename, "size": 1024, "progress": 1})
			}
			workflowJSON(w, files)
		case "/api/v2/torrents/filePrio", "/api/v2/torrents/start":
			t.Error("whole RSS magnet used daemon file selection or forced start", r.URL.Path)
			http.Error(w, "ungranted mutation", http.StatusBadRequest)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(qb.Close)
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/tv":
			workflowJSON(w, map[string]any{"page": 1, "total_pages": 1, "total_results": 1, "results": []map[string]any{
				{"id": m.ID, "name": m.Title, "original_name": m.Title, "first_air_date": "2024-01-01"},
			}})
		case "/3/tv/123":
			workflowJSON(w, map[string]any{"id": m.ID, "name": m.Title, "original_name": m.Title, "first_air_date": "2024-01-01",
				"original_language": m.OriginalLanguage, "origin_country": m.OriginCountry, "production_countries": m.ProductionCountries, "genres": m.Genres,
				"number_of_seasons": 1, "number_of_episodes": 2, "seasons": []any{map[string]any{"id": 340, "season_number": 1, "episode_count": 2, "air_date": "2024-01-01"}}})
		case "/3/tv/123/season/1":
			workflowJSON(w, map[string]any{"id": 340, "season_number": 1, "air_date": "2024-01-01", "episodes": []any{
				map[string]any{"id": 501, "season_number": 1, "episode_number": 1, "name": "第 1 集", "air_date": "2024-01-01"},
				map[string]any{"id": 502, "season_number": 1, "episode_number": 2, "name": "第 2 集", "air_date": "2024-01-01"},
			}})
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
	paths := []string{"/3/movie/123", "/3/search/movie"}
	if series {
		paths = []string{"/3/tv/123", "/3/tv/123/season/1", "/3/search/tv"}
	}
	installMetadataFixture(t, a, "owned-source-paused-tmdb", paths, transport.RoundTrip)
	workflowConfigure(t, a, tracker.URL, qb.URL)
	a.config.Sites[0].RSS = tracker.URL + "/rss.php?passkey=owned-rss-feed"
	a.config.Downloaders[0].DownloadPath, a.config.Downloaders[0].SharedPaths = source, true
	a.config.Directories = []DirectoryRule{{ID: "owned-magnet", Name: "磁力测试目录", SourcePath: source, LibraryPath: library,
		RootID: "root", MediaType: kind, Mode: "hardlink", MonitorType: "downloader", Enabled: true}}
	if quality {
		overwriteMode := "always"
		a.config.Directories[0].OverwriteModeKnown, a.config.Directories[0].OverwriteMode = true, &overwriteMode
		a.config.PriorityRules = &priorityrules.Configuration{SubscribeGroups: []string{"quality"}, Groups: []priorityrules.Group{{Name: "quality", RuleString: priorityText("WEBDL & 4K > WEBDL & 1080P")}}, Mode: priorityrules.Rust}
	}
	if err := a.saveSettings(t.Context(), a.Settings()); err != nil {
		t.Fatal(err)
	}
	if !series {
		client, err := sites.New(a.config.Sites[0].Config)
		if err != nil {
			t.Fatal(err)
		}
		defer client.CloseIdleConnections()
		for _, endpoint := range []string{"/magnet-redirect", "/magnet-body"} {
			content, invalid, err := client.SubscriptionRSSMaterial(t.Context(), tracker.URL+endpoint)
			if err != nil || invalid != "" || content.Magnet != magnet || len(content.Torrent) != 0 {
				t.Fatalf("RSS material %s did not return the exact magnet without invalidating it: %v", endpoint, err)
			}
		}
	}
	request := SubscriptionRequest{MediaType: kind, TMDBID: m.ID, DownloaderID: "fixture-qb", DownloadPath: source, DirectoryID: "owned-magnet", Schedule: Schedule{Mode: "manual"}}
	if series {
		request.Season, request.Episodes = 1, []int{1, 2}
	}
	var oldAsset Asset
	var oldSource string
	oldBytes := bytes.Repeat([]byte{'l'}, 1024)
	if quality {
		best := true
		request.BestVersion = &best
		request.Episodes, request.TotalEpisodes = nil, 2
		if full {
			request.BestVersionFull, request.StartEpisode = &best, 2
		}
		oldSource = filepath.Join(source, "测试剧集 S01E02 1080p.mkv")
		importWrite(t, oldSource, oldBytes)
		old, err := a.previewImport(t.Context(), ImportRequest{MediaType: "tv", TMDBID: m.ID, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: oldSource, Season: 1, Episodes: []int{2}}}})
		if err != nil {
			t.Fatal(err)
		}
		job, err := a.executeImport(t.Context(), old.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		reorgRunJob(t, a, job)
		if err := a.assetAtPath(t.Context(), old.Files[0].Target, &oldAsset); err != nil {
			t.Fatal(err)
		}
		reorgRunJob(t, a, reorgJob(t, a, "scrape", oldAsset.ID))
	}
	sub, err := a.saveSubscription(t.Context(), "", request)
	if err != nil {
		t.Fatal(err)
	}
	sub.SourceState = "P"
	sub.CreatedAt = time.Now().Add(-2 * time.Minute).Format(time.RFC3339Nano)
	if quality {
		priority := 0
		sub.CurrentPriority = &priority
		sub.EpisodePriority = map[string]int{"1": 0, "2": 50}
		if full {
			priority = 20
			sub.SourceState = "R"
			sub.EpisodePriority = nil
		}
	}
	if err := a.Store.Put(t.Context(), "subscription", sub.ID, sub); err != nil {
		t.Fatal(err)
	}

	type card struct{ method, content string }
	cards := make(chan card, 12)
	postAdd := make(chan string, 1)
	lark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/open-apis")
		switch {
		case path == "/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-magnet-fixture"}`)
		case strings.Contains(path, "/reactions"):
			_, _ = io.WriteString(w, `{"code":0,"data":{"reaction_id":"rx_fixture"}}`)
		case path == "/im/v1/messages" || path == "/im/v1/messages/om_magnet":
			var in struct{ Content string }
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			select {
			case cards <- card{r.Method, in.Content}:
			default:
				t.Error("unexpected extra Lark card")
			}
			if strings.Contains(in.Content, "owned-private-rss-token") {
				t.Error("Lark exposed a private tracker from the magnet")
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_magnet","chat_id":"oc_magnet"}}`)
		case path == "/owned-post-add" && r.Method == http.MethodPost:
			var call struct{ Kwargs map[string]json.RawMessage }
			var content string
			if err := json.NewDecoder(r.Body).Decode(&call); err != nil || json.Unmarshal(call.Kwargs["torrent_content"], &content) != nil {
				t.Error("download_added did not receive a typed magnet string")
				http.Error(w, "unexpected callback", 400)
				return
			}
			select {
			case postAdd <- content:
			default:
				t.Error("repeated download_added call")
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(lark.Close)
	a.pluginOriginalLarkTestOrigin = lark.URL + "/open-apis"
	admin := setupTestAdmin(t, a)
	observer := installOrderWorker(t, a, admin, os.Getenv("OWNED_TRANSFER_EVENTS_BINARY")).PluginID
	observerConfig := mustCleanSeedJSON(map[string]any{"enabled": true, "post_url": lark.URL + "/owned-post-add"})
	configured := callTest(t, a.Handler(), "PUT", "/api/plugins/"+observer, string(observerConfig), admin, "")
	assertOriginalRegistrySuccess(t, configured.Code, configured.Body.Bytes())
	a.Start(t.Context())
	installMergeSiteSwitchPackage(t, a, admin, bundle, "LarkMessager")
	api := a.Handler()
	const plugin = "/api/plugins/LarkMessager"
	result := callTest(t, api, "PUT", plugin, `{"enabled":true,"app_id":"owned-app","app_secret":"owned-secret","chat_id":"oc_magnet","verification_token":"owned-verification","switchs":["插件","订阅"]}`, admin, "")
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
			"message": map[string]any{"message_id": fmt.Sprintf("om_magnet_%d", index), "chat_id": "oc_magnet", "message_type": "text", "content": string(mustCleanSeedJSON(map[string]string{"text": text}))},
		})
	}
	next := func(method, fragment string) string {
		t.Helper()
		timer := time.NewTimer(12 * time.Second)
		defer timer.Stop()
		for {
			select {
			case got := <-cards:
				if full && got.method == "POST" && strings.Contains(got.content, "已完成洗版") {
					continue // Completion and the refresh card have separate senders.
				}
				if got.method != method || !strings.Contains(got.content, fragment) {
					t.Fatalf("wanted %s with %q, got %s: %s", method, fragment, got.method, got.content)
				}
				return got.content
			case <-timer.C:
				t.Fatalf("no %s card for %q", method, fragment)
				return ""
			}
		}
	}
	send(1, "/subscribes")
	stateLabel := "待定"
	if full {
		stateLabel = "订阅中"
	}
	first := next("POST", stateLabel)
	token := regexp.MustCompile(`subscribes:[0-9a-f]{12}:`).FindString(first)
	if token == "" {
		t.Fatal("subscription callback ID is missing")
	}
	post("card.action.trigger", map[string]any{
		"operator": map[string]any{"open_id": "ou_owner"}, "context": map[string]any{"open_message_id": "om_magnet", "open_chat_id": "oc_magnet"},
		"action": map[string]any{"value": map[string]string{"callback_data": token + "refresh"}},
	})
	next("POST", "开始刷新订阅")
	next("POST", "订阅刷新执行完成")
	if full {
		next("PATCH", originalSubscribeChatTitle)
	} else {
		next("PATCH", "待定")
	}
	select {
	case content := <-postAdd:
		if content != magnet {
			t.Fatal("download_added did not preserve the exact original magnet")
		}
	case <-time.After(12 * time.Second):
		t.Fatal("download_added did not run after the accepted magnet")
	}

	var intentID string
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT id FROM records WHERE kind='intent'`).Scan(&intentID); err != nil {
		t.Fatal(err)
	}
	var intent Intent
	if err := a.Store.Get(t.Context(), "intent", intentID, &intent); err != nil || !intent.RSSMagnet || intent.Hash != hash || intent.State != "accepted" || adds.Load() != 1 || feeds.Load() != 1 {
		t.Fatalf("RSS magnet did not produce one exact accepted native task: state=%s adds=%d feeds=%d err=%v", intent.State, adds.Load(), feeds.Load(), err)
	}
	if series && !quality {
		if intent.Season != 1 || len(intent.Episodes) != 1 || intent.Episodes[0] != 2 || len(intent.SelectedEpisodes) != 0 || singleReads.Load() != 1 || multiReads.Load() != 0 {
			t.Fatalf("RSS TV listing scope mismatch: season=%d episodes=%v selected=%v single=%d multi=%d", intent.Season, intent.Episodes, intent.SelectedEpisodes, singleReads.Load(), multiReads.Load())
		}
		t.Log("RSS refresh skipped the multi-season listing before its material endpoint and passed the exact S01E02 magnet through one same-origin redirect to qB and download_added")
		return
	}
	if full {
		proof, err := a.qualitySnapshot(t.Context(), a.Store.DB, "intent", intent.ID, intent.Hash)
		if err != nil || proof == nil || !proof.AllowWholeExtra || !proof.Resource.ConfirmedFullCoverage || proof.Resource.Priority != 100 ||
			intent.Season != 1 || !slices.Equal(intent.Episodes, []int{1, 2}) || intent.SelectedEpisodes != nil || singleReads.Load() != 1 {
			t.Fatalf("full quality magnet did not preserve the whole listed range: season=%d episodes=%v selected=%v reads=%d err=%v", intent.Season, intent.Episodes, intent.SelectedEpisodes, singleReads.Load(), err)
		}
		assertOriginalLarkRSSFullCompletion(t, a, sub, intent)
	} else if quality {
		if intent.SubscriptionAcquisition == "" || intent.Season != 1 || len(intent.Episodes) != 1 || intent.Episodes[0] != 2 || len(intent.SelectedEpisodes) != 0 || singleReads.Load() == 0 || packReads.Load() == 0 {
			t.Fatalf("quality magnet did not prove a whole listed episode after the pack check: acquisition=%q season=%d episodes=%v selected=%v episode reads=%d pack reads=%d", intent.SubscriptionAcquisition, intent.Season, intent.Episodes, intent.SelectedEpisodes, singleReads.Load(), packReads.Load())
		}
		var updated Subscription
		if err := a.Store.Get(t.Context(), "subscription", sub.ID, &updated); err != nil || updated.EpisodePriority["1"] != 0 || updated.EpisodePriority["2"] != 100 {
			t.Fatalf("accepted quality magnet advanced incorrect episode priorities: %v, %v", updated.EpisodePriority, err)
		}
	}
	if quality {
		if got, err := os.ReadFile(oldAsset.Path); err != nil || !bytes.Equal(got, oldBytes) {
			t.Fatal("quality acceptance changed the managed file before completion", err)
		}
	}
	var archived []byte
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT payload FROM records WHERE kind='material_blob' AND id=?`, intent.MaterialRef).Scan(&archived); err != nil || bytes.Contains(archived, []byte(magnet)) {
		t.Fatal("magnet was not kept in a sealed material record", err)
	}
	raw, err := a.resolveDownloadMaterial(t.Context(), intent.MaterialRef, materialPurpose, true)
	if err != nil || string(raw) != magnet {
		t.Fatal("sealed archive did not preserve original magnet", err)
	}
	var assets, cooled int
	wantAssets := 0
	if quality {
		wantAssets = 1
	}
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT
		(SELECT COUNT(*) FROM asset_slots), (SELECT COUNT(*) FROM records WHERE kind='subscription_download_failure')`).Scan(&assets, &cooled); err != nil || assets != wantAssets || cooled != 0 || len(a.originalChatRSSInvalidSnapshot()) != 0 {
		t.Fatalf("magnet incorrectly completed files or cooled the resource: assets=%d cooled=%d err=%v", assets, cooled, err)
	}
	owned := filepath.Join(source, filename)
	newBytes := bytes.Repeat([]byte{'h'}, 1024)
	importWrite(t, owned, newBytes)
	extra := filepath.Join(source, extraFilename)
	if full {
		importWrite(t, extra, bytes.Repeat([]byte{'e'}, 1024))
	}
	finished.Store(true)
	if err := a.pollDownloads(t.Context()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for {
		if err := a.Store.Get(t.Context(), "intent", intentID, &intent); err != nil {
			t.Fatal(err)
		}
		if intent.State == "imported" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("completed magnet did not organize its verified file: state=%s error=%s", intent.State, intent.Error)
		}
		time.Sleep(25 * time.Millisecond)
	}
	var assetID string
	if quality {
		var slots int
		if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT count(*), COALESCE(max(asset_id),'') FROM asset_slots WHERE media_key=? AND season=1 AND episode=2`, mediaKey(m)).Scan(&slots, &assetID); err != nil || slots != 1 {
			t.Fatalf("replacement did not leave exactly one managed S01E02 slot: %d, %v", slots, err)
		}
	} else if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT asset_id FROM asset_slots WHERE media_key=?`, mediaKey(m)).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	var asset Asset
	if err := a.Store.Get(t.Context(), "asset", assetID, &asset); err != nil {
		t.Fatal(err)
	}
	original, srcErr := os.Stat(owned)
	linked, dstErr := os.Stat(asset.Path)
	if srcErr != nil || dstErr != nil || !os.SameFile(original, linked) {
		t.Fatalf("completed RSS magnet did not create a verified local hardlink: %v %v", srcErr, dstErr)
	}
	if quality {
		if asset.ID == oldAsset.ID || asset.Path != oldAsset.Path || asset.IntentID != intent.ID || asset.Season != 1 || len(asset.Episodes) != 1 || asset.Episodes[0] != 2 {
			t.Fatal("quality magnet did not replace the original managed S01E02 slot")
		}
		for path, expected := range map[string][]byte{oldSource: oldBytes, owned: newBytes, asset.Path: newBytes} {
			if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, expected) {
				t.Fatalf("quality magnet replacement or source retention differs at %s: %v", path, err)
			}
		}
		if full {
			var extraAssetID string
			var extraSlots int
			if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT count(*), COALESCE(max(asset_id),'') FROM asset_slots WHERE media_key=? AND season=1 AND episode=1`, mediaKey(m)).Scan(&extraSlots, &extraAssetID); err != nil || extraSlots != 1 {
				t.Fatalf("full quality magnet did not organize its actual extra E01 file: %d, %v", extraSlots, err)
			}
			var extraAsset Asset
			if err := a.Store.Get(t.Context(), "asset", extraAssetID, &extraAsset); err != nil || extraAsset.IntentID != intent.ID || !slices.Equal(extraAsset.Episodes, []int{1}) {
				t.Fatalf("extra E01 slot lost its whole Add ownership: %v", err)
			}
			sourceFile, sourceErr := os.Stat(extra)
			targetFile, targetErr := os.Stat(extraAsset.Path)
			if sourceErr != nil || targetErr != nil || !os.SameFile(sourceFile, targetFile) {
				t.Fatalf("extra E01 file did not remain hardlinked to the verified source: %v, %v", sourceErr, targetErr)
			}
			t.Log("whole E01–E02 magnet retired the active E02-only full-quality subscription from its succeeded RSS root, then imported both verified files and replaced only the managed E02 slot")
			return
		}
		t.Log("RSS quality skipped a file-dependent pack magnet, passed only title-proven S01E02 through one whole Add and replaced the real managed asset after file verification")
		return
	}
	t.Log("RSS redirect and raw body decoded the exact magnet; a direct RSS magnet made one real qB Add, remained encrypted, reached download_added as a string and imported only after file confirmation")
}

func assertOriginalLarkRSSFullCompletion(t *testing.T, a *App, sub Subscription, intent Intent) {
	t.Helper()
	ctx := t.Context()
	root, err := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, intent.SubscriptionAcquisition))
	var request originalChatLatestJob
	if err != nil || root.Kind != "subscription" || root.State != "succeeded" || json.Unmarshal(root.Payload, &request) != nil ||
		!request.RSSFallback || request.ID != sub.ID || request.Revision != sub.Revision || request.LatestBatch == "" {
		t.Fatalf("whole magnet did not come from the succeeded exact RSS root: kind=%s state=%s code=%s err=%v", root.Kind, root.State, root.ErrorCode, err)
	}
	var batch originalChatLatestBatch
	if err := a.originalChatLatestRead(ctx, a.Store.DB, originalChatLatestBatchKind, request.LatestBatch, &batch); err != nil || !batch.ownsRSSRoot(root) {
		t.Fatalf("completed root is missing its sealed RSS batch ownership: %v", err)
	}
	prepare, err := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind='subscription_completion_prepare' AND json_extract(payload,'$.id')=?`, sub.ID))
	if err != nil || prepare.ParentJobID != root.ID || prepare.WorkflowID != root.WorkflowID {
		t.Fatalf("full quality retirement preparation was not enqueued by the succeeded RSS root: parent=%s root=%s err=%v", prepare.ParentJobID, root.ID, err)
	}
	var receipt subscriptionCompletionReceipt
	var finish Job
	for deadline := time.Now().Add(12 * time.Second); ; {
		finish, err = scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind='subscription_completion' AND json_extract(payload,'$._parent_job_id')=?`, prepare.ID))
		if err == nil && finish.State == "succeeded" {
			var payload struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(finish.Payload, &payload); err != nil || payload.ID == "" {
				t.Fatalf("invalid full quality completion job payload: %v", err)
			}
			if err := a.readQualitySealed(ctx, a.Store.DB, "subscription_completion", payload.ID, &receipt); err != nil {
				t.Fatal(err)
			}
			break
		}
		if finish.State == "failed" || time.Now().After(deadline) {
			t.Fatalf("whole quality completion did not finish: state=%s code=%s err=%v", finish.State, finish.ErrorCode, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	var retired Subscription
	if err := json.Unmarshal(receipt.Input.Source.Subscription, &retired); err != nil || receipt.Stage != "complete" || receipt.Identity.JobID != finish.ID ||
		receipt.Identity.SubscriptionID != sub.ID || receipt.Identity.Revision != sub.Revision || len(receipt.Input.Accepted) != 1 || receipt.Input.Accepted[0].ID != intent.ID ||
		retired.SourceState != "R" || !retired.BestVersionFull || retired.StartEpisode != 2 || retired.TotalEpisodes != 2 || retired.CurrentPriority == nil || *retired.CurrentPriority != 100 {
		t.Fatalf("retired subscription lost accepted full-quality scope or source state: stage=%s err=%v", receipt.Stage, err)
	}
	history, err := a.pluginStore.readSubscriptionCompletionSealed(ctx, a.Store.DB, "subscription_history", receipt.HistoryID)
	if err != nil || tokenHash(string(history)) != receipt.HistorySHA {
		t.Fatalf("retirement did not persist its exact source history: %v", err)
	}
	var live, events int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM records WHERE kind='subscription' AND id=?),
		(SELECT count(*) FROM records WHERE kind='source_subscribe_complete' AND id=?)`, sub.ID, receipt.Identity.ID).Scan(&live, &events); err != nil || live != 0 || events != 1 {
		t.Fatalf("completed source subscription was not retired before magnet file verification: live=%d event=%d err=%v", live, events, err)
	}
}
