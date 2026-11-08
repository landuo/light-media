package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
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
)

func TestOriginalLarkChatMagnetLinuxAMD64(t *testing.T) {
	testOriginalLarkChatMagnet(t, "movie", false)
}

func TestOriginalLarkChatMultiSeasonMagnetLinuxAMD64(t *testing.T) {
	testOriginalLarkChatMagnet(t, "tv", true)
}

func TestOriginalLarkChatEpisodeMagnetLinuxAMD64(t *testing.T) {
	testOriginalLarkChatMagnet(t, "tv", false)
}

func testOriginalLarkChatMagnet(t *testing.T, mediaType string, multiSeason bool) {
	t.Helper()
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	t.Setenv("SEARCH_MULTIPLE_NAME", "false")
	const (
		plugin        = "/api/plugins/LarkMessager"
		owner         = "ou_owned_chat_magnet"
		privateMarker = "owned-private-chat-magnet-tracker"
	)
	hash := "456789abcdef0123456789abcdef0123456789ab"
	release := "测试影片.2024.1080p.WEB-DL.x264-CHATMAGNET"
	files := []string{"测试影片 (2024) 1080p.mkv"}
	isTV := mediaType == "tv"
	if multiSeason {
		t.Setenv("SUBSCRIBE_MODE", "spider")
		hash = "789abcdef0123456789abcdef0123456789abcde"
		release = "测试剧集.S01-S02.2024.1080p.WEB-DL.x264-CHATMAGNET"
		files = []string{"测试剧集 S01-S02/测试剧集 S01E01 1080p.mkv", "测试剧集 S01-S02/测试剧集 S02E01 1080p.mkv"}
	} else if isTV {
		hash = "456789abcdef0123456789abcdef0123456789ac"
		release = "测试剧集.S01E01.2024.1080p.WEB-DL.x264-CHATMAGNET"
		files = []string{"测试剧集 S01E01 1080p.mkv"}
	}
	magnet := "magnet:?xt=urn:btih:" + hash + "&tr=" + url.QueryEscape("https://tracker.invalid/"+privateMarker)
	a, source, library := importTestApp(t)
	media := importFixture(t, a, mediaType)
	ctx := t.Context()
	var searches, materials, adds, fileReads atomic.Int32
	var inventory atomic.Int32
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			query := r.URL.Query().Get("search")
			if r.Method != http.MethodGet || query != media.Title && (!isTV || !strings.Contains(query, media.Title)) {
				t.Error("ordinary tracker search did not use the chosen title")
			}
			searches.Add(1)
			_, _ = io.WriteString(w, workflowHTML(release, "", false))
		case "/download.php":
			if r.Method != http.MethodGet || r.URL.Query().Get("id") != "101" || r.Header.Get("Cookie") != "fixture-cookie" {
				t.Error("magnet redirect was not fetched as authenticated exact site material")
			}
			materials.Add(1)
			w.Header().Set("Location", magnet)
			w.WriteHeader(http.StatusFound)
		default:
			t.Error("unexpected chat tracker route", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(tracker.Close)
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			if r.Method != http.MethodPost || r.Header.Get("Cookie") != "" {
				t.Error("unexpected qB login request", r.Method)
			}
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v5.0.0")
		case "/api/v2/app/preferences":
			workflowJSON(w, map[string]string{"save_path": source})
		case "/api/v2/torrents/info":
			if adds.Load() == 0 {
				workflowJSON(w, []any{})
				return
			}
			task := workflowTask(hash, 0)
			task["save_path"], task["state"] = source, "downloading"
			task["total_size"] = len(files) * 1024
			if inventory.Load() > 0 {
				task["progress"], task["completed"], task["state"] = 1, len(files)*1024, "stalledUP"
			}
			workflowJSON(w, []any{task})
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error("qB did not receive a multipart Add", err)
				http.Error(w, "unexpected Add", http.StatusBadRequest)
				return
			}
			defer r.MultipartForm.RemoveAll()
			if r.Method != http.MethodPost || r.FormValue("urls") != magnet || r.FormValue("savepath") != source || r.FormValue("stopped") != "false" ||
				len(r.MultipartForm.File) != 0 || r.Header.Get("Cookie") != "" {
				t.Error("qB did not receive a whole unpaused Add of the exact URI and local path")
				http.Error(w, "unexpected Add", http.StatusBadRequest)
				return
			}
			adds.Add(1)
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/torrents/files":
			fileReads.Add(1)
			count := int(inventory.Load())
			if count == 0 || r.URL.Query().Get("hash") != hash {
				t.Error("qB files were queried before known completion")
				http.Error(w, "metadata unavailable", http.StatusConflict)
				return
			}
			listing := make([]map[string]any, 0, count)
			for i, path := range files[:count] {
				listing = append(listing, map[string]any{"index": i, "name": path, "size": 1024, "progress": 1})
			}
			workflowJSON(w, listing)
		case "/api/v2/torrents/addTags":
			if err := r.ParseForm(); err != nil || r.Method != http.MethodPost || int(inventory.Load()) != len(files) ||
				r.Form.Get("hashes") != hash || r.Form.Get("tags") != "已整理" {
				t.Error("unexpected post-import qB tag request", err)
				http.Error(w, "unexpected tag", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Error("unexpected qB request, including file selection or Start", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(qb.Close)
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/movie", "/3/search/tv", "/3/search/multi":
			results := []map[string]any{}
			if r.URL.Path == "/3/search/multi" || r.URL.Path == "/3/search/"+mediaType {
				result := map[string]any{"id": media.ID, "media_type": mediaType}
				if isTV {
					result["name"], result["original_name"], result["first_air_date"] = media.Title, media.Title, "2024-01-01"
				} else {
					result["title"], result["original_title"], result["release_date"] = media.Title, media.OriginalTitle, "2024-01-01"
				}
				results = append(results, result)
			}
			workflowJSON(w, map[string]any{"page": 1, "total_pages": 1, "total_results": len(results), "results": results})
		case "/3/movie/123":
			workflowJSON(w, map[string]any{"id": media.ID, "title": media.Title, "original_title": media.OriginalTitle, "release_date": "2024-01-01",
				"original_language": media.OriginalLanguage, "origin_country": media.OriginCountry, "production_countries": media.ProductionCountries,
				"genres": media.Genres, "overview": media.Overview, "external_ids": map[string]string{"imdb_id": "tt1234567"}})
		case "/3/tv/123":
			workflowJSON(w, map[string]any{"id": media.ID, "name": media.Title, "original_name": media.Title, "first_air_date": "2024-01-01",
				"original_language": media.OriginalLanguage, "origin_country": media.OriginCountry, "production_countries": media.ProductionCountries,
				"genres": media.Genres, "overview": media.Overview, "number_of_seasons": 2, "number_of_episodes": 2,
				"seasons": []map[string]any{
					{"id": 341, "season_number": 1, "episode_count": 1, "air_date": "2024-01-01"},
					{"id": 342, "season_number": 2, "episode_count": 1, "air_date": "2024-02-01"},
				}, "external_ids": map[string]string{"imdb_id": "tt1234567"}})
		case "/3/tv/123/season/1", "/3/tv/123/season/2":
			season := 1
			if strings.HasSuffix(r.URL.Path, "/2") {
				season = 2
			}
			episodes := []map[string]any{{"id": 501 + season, "season_number": season, "episode_number": 1, "name": "测试分集", "air_date": "2024-01-01"}}
			workflowJSON(w, map[string]any{"id": 340 + season, "season_number": season, "air_date": "2024-01-01", "episodes": episodes})
		case "/3/tv/123/images":
			workflowJSON(w, map[string]any{"id": media.ID, "posters": []any{}, "backdrops": []any{}, "logos": []any{}})
		default:
			t.Error("unexpected movie metadata route", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(metadata.Close)
	paths := []string{"/3/search/movie", "/3/search/multi", "/3/movie/123"}
	if isTV {
		paths = append(paths, "/3/search/tv", "/3/tv/123", "/3/tv/123/season/1", "/3/tv/123/season/2", "/3/tv/123/images")
	}
	installMetadataFixture(t, a, "owned-chat-magnet-tmdb", paths, ownedLoopbackFixtureTransport(t, metadata).RoundTrip)

	type card struct{ Content, Recipient string }
	cards := make(chan card, 8)
	lark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/open-apis")
		switch {
		case path == "/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-chat-magnet-fixture"}`)
		case strings.Contains(path, "/reactions"):
			_, _ = io.WriteString(w, `{"code":0,"data":{"reaction_id":"rx_fixture"}}`)
		case path == "/im/v1/messages" && r.Method == http.MethodPost:
			var message struct {
				Content   string `json:"content"`
				Recipient string `json:"receive_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&message); err != nil || strings.Contains(message.Content, privateMarker) || strings.Contains(message.Content, "fixture-cookie") {
				t.Error("invalid Lark message or leaked private material")
			}
			select {
			case cards <- card{message.Content, message.Recipient}:
			default:
				t.Error("unexpected extra Lark card")
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_chat_magnet","chat_id":"oc_chat_magnet"}}`)
		default:
			t.Error("unexpected Lark route", r.Method, path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(lark.Close)
	a.pluginOriginalLarkTestOrigin = lark.URL + "/open-apis"
	workflowConfigure(t, a, tracker.URL, qb.URL)
	a.config.Downloaders[0].DownloadPath, a.config.Downloaders[0].SharedPaths = source, true
	a.config.Directories = []DirectoryRule{{ID: "owned-chat-magnet", Name: "聊天磁力测试目录", SourcePath: source, LibraryPath: library,
		RootID: "root", MediaType: mediaType, Mode: "hardlink", MonitorType: "downloader", Enabled: true}}
	if err := a.saveSettings(ctx, a.Settings()); err != nil {
		t.Fatal(err)
	}
	admin := setupTestAdmin(t, a)
	a.Start(ctx)
	installMergeSiteSwitchPackage(t, a, admin, bundle, "LarkMessager")
	api := a.Handler()
	configured := callTest(t, api, http.MethodPut, plugin, `{"enabled":true,"app_id":"owned-app","app_secret":"owned-secret","chat_id":"oc_chat_magnet","verification_token":"owned-verification","switchs":["资源下载"]}`, admin, "")
	assertOriginalRegistrySuccess(t, configured.Code, configured.Body.Bytes())
	post := func(kind string, fields map[string]any) {
		t.Helper()
		raw := mustCleanSeedJSON(map[string]any{"header": map[string]any{"event_type": kind}, "event": fields})
		reply := callTest(t, api, http.MethodPost, plugin+"/api/webhook?token=owned-verification", string(raw), nil, "")
		var ack struct{ Success bool }
		if reply.Code != http.StatusOK || json.Unmarshal(reply.Body.Bytes(), &ack) != nil || !ack.Success {
			t.Fatal("Lark webhook did not acknowledge", reply.Code)
		}
	}
	click := func(value string) {
		t.Helper()
		post("card.action.trigger", map[string]any{
			"operator": map[string]any{"open_id": owner}, "context": map[string]any{"open_message_id": "om_chat_magnet", "open_chat_id": "oc_chat_magnet"},
			"action": map[string]any{"value": map[string]string{"callback_data": value}},
		})
	}
	next := func(parts ...string) string {
		t.Helper()
		select {
		case got := <-cards:
			if got.Recipient != owner {
				t.Fatal("chat response did not target the originating user")
			}
			for _, part := range parts {
				if !strings.Contains(got.Content, part) {
					content := strings.NewReplacer(magnet, "[material]", privateMarker, "[private]", "fixture-cookie", "[cookie]").Replace(got.Content)
					t.Fatalf("Lark card missed %q: %s", part, content)
				}
			}
			return got.Content
		case <-time.After(12 * time.Second):
			t.Fatalf("no Lark card with %v", parts)
			return ""
		}
	}
	query, selection := "搜索 "+media.Title, "download:1"
	if multiSeason {
		query, selection = media.Title, "download:0"
	} else if isTV {
		query += " S01"
	}
	post("im.message.receive_v1", map[string]any{
		"sender":  map[string]any{"sender_id": map[string]any{"open_id": owner}},
		"message": map[string]any{"message_id": "om_chat_magnet_search", "chat_id": "oc_chat_magnet", "message_type": "text", "content": string(mustCleanSeedJSON(map[string]string{"text": query}))},
	})
	session := regexp.MustCompile(`media:[0-9a-f]{12}:`).FindString(next("共找到1条相关信息"))
	if session == "" {
		t.Fatal("missing media selection callback")
	}
	click(session + "select:1")
	if multiSeason {
		next("第 1 季", "第 2 季")
	} else if isTV {
		next("第 1 季")
	}
	next("开始搜索")
	next("共找到1条相关资源", release, session+selection)
	if searches.Load() == 0 || materials.Load() != 0 || adds.Load() != 0 || fileReads.Load() != 0 {
		t.Fatal("material or downloader was accessed before explicit resource selection")
	}
	click(session + selection)
	next("开始下载", release)
	var intentID string
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT id FROM records WHERE kind='intent'`).Scan(&intentID); err != nil {
		t.Fatal(err)
	}
	var intent Intent
	if err := a.Store.Get(ctx, "intent", intentID, &intent); err != nil || intent.State != "accepted" || !intent.ChatDownload || !intent.ChatMagnet || intent.LatestResource || intent.RSSMagnet ||
		intent.Media.ID != media.ID || intent.Media.MediaType != mediaType || intent.Hash != hash || len(intent.SelectedEpisodes) != 0 || adds.Load() != 1 || materials.Load() != 1 || fileReads.Load() != 0 {
		t.Fatalf("missing exact accepted ordinary magnet: state=%s, Adds=%d, material=%d, file reads=%d, err=%v", intent.State, adds.Load(), materials.Load(), fileReads.Load(), err)
	}
	if isTV && !multiSeason && (intent.Season != 1 || !slices.Equal(intent.Episodes, []int{1}) || intent.SeasonScopeSHA != "") {
		t.Fatal("listed single-season Add did not retain its exact episode scope")
	}
	var sealed []byte
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='material_blob' AND id=?`, intent.MaterialRef).Scan(&sealed); err != nil || bytes.Contains(sealed, []byte(privateMarker)) {
		t.Fatal("ordinary magnet material was not sealed", err)
	}
	material, err := a.resolveDownloadMaterial(ctx, intent.MaterialRef, materialPurpose, true)
	if err != nil || string(material) != magnet {
		t.Fatal("sealed material did not preserve the exact ordinary magnet", err)
	}
	assertNoAssets := func() {
		t.Helper()
		var assets int
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_slots`).Scan(&assets); err != nil || assets != 0 {
			t.Fatal("magnet acceptance or incomplete inventory invented library files", assets, err)
		}
	}
	assertNoAssets()
	if multiSeason {
		proof, err := a.intentSeasonProof(ctx, a.Store.DB, intent)
		if err != nil || proof == nil || !proof.Magnet || len(proof.Files) != 0 || len(proof.Scopes) != 2 ||
			proof.Scopes[0].Season != 1 || proof.Scopes[1].Season != 2 ||
			!slices.Equal(proof.Scopes[0].Episodes, []int{1}) || !slices.Equal(proof.Scopes[1].Episodes, []int{1}) {
			t.Fatal("one whole Add did not seal both title-proven seasons without a file inventory", err)
		}
		importWrite(t, filepath.Join(source, files[0]), bytes.Repeat([]byte{0}, 1024))
		inventory.Store(1)
		if err = a.pollDownloads(ctx); err != nil {
			t.Fatal(err)
		}
		if err = a.Store.Get(ctx, "intent", intentID, &intent); err != nil || intent.State != "downloaded" || intent.ImportPlanID != "" ||
			!strings.Contains(intent.Error, "多季下载") || fileReads.Load() == 0 {
			t.Fatalf("incomplete daemon inventory was not rejected before organization: state=%s error=%s err=%v", intent.State, a.clean(intent.Error), err)
		}
		assertNoAssets()
		var measured []downloadScopedFile
		if err := a.readQualitySealed(ctx, a.Store.DB, completedSeasonMagnetKind, completedSeasonMagnetKey(intent), &measured); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("incomplete inventory produced a completion proof", err)
		}
	}
	for _, file := range files {
		importWrite(t, filepath.Join(source, file), bytes.Repeat([]byte{0}, 1024))
	}
	inventory.Store(int32(len(files)))
	if err = a.pollDownloads(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for {
		if err = a.Store.Get(ctx, "intent", intentID, &intent); err != nil {
			t.Fatal(err)
		}
		if intent.State == "imported" {
			if intent.Error != "" {
				t.Fatalf("native import did not clear the previous review: %s", a.clean(intent.Error))
			}
			break
		}
		if time.Now().After(deadline) {
			var plan ImportPlan
			planErr := a.Store.Get(ctx, "import_plan", intent.ImportPlanID, &plan)
			var jobState, jobCode string
			jobErr := a.Store.DB.QueryRowContext(ctx, `SELECT state,error_code FROM jobs WHERE kind='import' AND json_extract(payload,'$.id')=? ORDER BY created_at DESC LIMIT 1`, intent.ImportPlanID).Scan(&jobState, &jobCode)
			t.Fatalf("completed chat magnet was not organized: intent=%s review=%s plan=%s (%v) job=%s/%s (%v)",
				intent.State, a.clean(intent.Error), plan.State, planErr, jobState, jobCode, jobErr)
		}
		time.Sleep(25 * time.Millisecond)
	}
	for i, file := range files {
		var assetID string
		query, args := `SELECT asset_id FROM asset_slots WHERE media_key=?`, []any{mediaKey(media)}
		if isTV {
			query += ` AND season=? AND episode=1`
			args = append(args, i+1)
		}
		if err = a.Store.DB.QueryRowContext(ctx, query, args...).Scan(&assetID); err != nil {
			t.Fatal(err)
		}
		var asset Asset
		if err = a.Store.Get(ctx, "asset", assetID, &asset); err != nil {
			t.Fatal(err)
		}
		original, sourceErr := os.Stat(filepath.Join(source, file))
		linked, libraryErr := os.Stat(asset.Path)
		if sourceErr != nil || libraryErr != nil || !within(library, asset.Path) || !os.SameFile(original, linked) {
			t.Fatalf("completed chat magnet did not organize its verified file: source=%v library=%v", sourceErr, libraryErr)
		}
	}
	if fileReads.Load() == 0 || adds.Load() != 1 || materials.Load() != 1 {
		t.Fatal("completion did not verify files or duplicated the accepted Add or tracker request")
	}
	if multiSeason {
		var measured []downloadScopedFile
		if err := a.readQualitySealed(ctx, a.Store.DB, completedSeasonMagnetKind, completedSeasonMagnetKey(intent), &measured); err != nil || len(measured) != 2 {
			t.Fatal("actual files did not create a separate completion proof", err)
		}
		for i, file := range measured {
			if file.Path != files[i] || file.Size != 1024 || file.Season != i+1 || !slices.Equal(file.Episodes, []int{1}) {
				t.Fatal("unexpected measured season file", i)
			}
		}
		proof, err := a.intentSeasonProof(ctx, a.Store.DB, intent)
		if err != nil || proof == nil || len(proof.Files) != 0 {
			t.Fatal("completion rewrote the provisional Add proof", err)
		}
		var subscriptions int
		if err = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='subscription'`).Scan(&subscriptions); err != nil || subscriptions != 0 {
			t.Fatal("whole-pack acceptance created a fallback subscription", subscriptions, err)
		}
		t.Log("one unpaused magnet Add kept two fileless provisional seasons; a partial daemon inventory created no import, then the full inventory produced two scoped hardlinks")
		return
	}
	t.Log("ordinary verified Lark selection fetched one authenticated tracker redirect, added its exact unpaused magnet, and organized a real local hardlink only after downloader file confirmation")
}
