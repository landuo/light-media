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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func TestOriginalLarkDownloadAddedLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" || os.Getenv("OWNED_TRANSFER_EVENTS_BINARY") == "" {
		t.Skip("requires indexed Linux amd64 LarkMessager and SDK event observer")
	}
	t.Setenv("MP_SERVER_HOST", "")
	a, source, target := importTestApp(t)
	m := importFixture(t, a, "movie")
	const owner = "ou_owned_download"
	const release = "测试影片.2024.1080p.WEB-DL.x264-TEST"
	torrent, hash := workflowTorrent("测试影片 (2024) 1080p.mkv")
	folderA, folderB := filepath.Join(source, "first"), filepath.Join(source, "second")
	for _, path := range []string{folderA, folderB} {
		if err := os.Mkdir(path, 0750); err != nil {
			t.Fatal(err)
		}
	}

	type receivedCard struct {
		Content, Receiver, ReceiverType string
	}
	cards := make(chan receivedCard, 12)
	postAdd := make(chan []byte, 2)
	releaseNotice := make(chan struct{})
	var unblockOnce sync.Once
	unblock := func() { unblockOnce.Do(func() { close(releaseNotice) }) }
	lark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch path := strings.TrimPrefix(r.URL.Path, "/open-apis"); {
		case path == "/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-acceptance-fixture"}`)
		case strings.Contains(path, "/reactions"):
			_, _ = io.WriteString(w, `{"code":0,"data":{"reaction_id":"rx_fixture"}}`)
		case path == "/im/v1/messages" && r.Method == http.MethodPost:
			var in struct {
				Content   string `json:"content"`
				ReceiveID string `json:"receive_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			select {
			case cards <- receivedCard{in.Content, in.ReceiveID, r.URL.Query().Get("receive_id_type")}:
			default:
				t.Error("unexpected extra Lark notice")
			}
			if strings.Contains(in.Content, "开始下载") {
				select {
				case <-releaseNotice:
				case <-r.Context().Done():
					return
				}
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_download","chat_id":"oc_download"}}`)
		case path == "/owned-post-add" && r.Method == http.MethodPost:
			raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
			if err != nil {
				t.Error(err)
			}
			select {
			case postAdd <- raw:
			default:
				t.Error("unexpected repeated post-Add")
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected Lark request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(lark.Close)
	defer unblock()
	a.pluginOriginalLarkTestOrigin = lark.URL + "/open-apis"

	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/movie", "/3/search/multi":
			workflowJSON(w, map[string]any{"page": 1, "total_pages": 1, "total_results": 1, "results": []map[string]any{
				{"id": m.ID, "media_type": "movie", "title": m.Title, "original_title": m.OriginalTitle, "release_date": "2024-01-01"},
			}})
		case "/3/movie/123":
			workflowJSON(w, map[string]any{"id": m.ID, "title": m.Title, "original_title": m.OriginalTitle,
				"release_date": "2024-01-01", "original_language": m.OriginalLanguage, "origin_country": m.OriginCountry,
				"production_countries": m.ProductionCountries, "genres": m.Genres, "overview": m.Overview,
				"external_ids": map[string]any{"imdb_id": "tt1234567"},
			})
		default:
			t.Errorf("unexpected metadata request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(metadata.Close)
	installMetadataFixture(t, a, "owned-source-tmdb", []string{"/3/search/movie", "/3/search/multi", "/3/movie/123"}, ownedLoopbackFixtureTransport(t, metadata).RoundTrip)

	var materials, adds atomic.Int32
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
			task := workflowTask(hash, 0)
			task["save_path"] = folderB
			workflowJSON(w, []any{task})
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(4 << 20); err != nil || r.FormValue("savepath") != folderB {
				t.Errorf("selected directory did not reach downloader: path=%q err=%v", r.FormValue("savepath"), err)
			}
			file, _, err := r.FormFile("torrents")
			if err != nil {
				t.Error(err)
			} else {
				defer file.Close()
				data, err := io.ReadAll(file)
				if err != nil || !bytes.Equal(data, torrent) {
					t.Error("downloader did not receive exact fixture material")
				}
			}
			adds.Add(1)
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/app/preferences":
			workflowJSON(w, map[string]string{"save_path": folderA})
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v5.0.0")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(qb.Close)
	workflowConfigure(t, a, tracker.URL, qb.URL)
	a.config.Downloaders[0].Name = "Owned qBittorrent"
	a.config.Downloaders[0].DownloadPath, a.config.Downloaders[0].SharedPaths = folderA, true
	a.config.AppDomain = "https://owned.invalid"
	a.config.Directories = []DirectoryRule{
		{ID: "owned-first", Name: "第一个目录", SourcePath: folderA, LibraryPath: target, RootID: "root", MediaType: "movie", Mode: "hardlink", MonitorType: "none", Enabled: true},
		{ID: "owned-second", Name: "第二个目录", SourcePath: folderB, LibraryPath: target, RootID: "root", MediaType: "movie", Mode: "hardlink", MonitorType: "none", Enabled: true},
	}
	a.config.APIToken = "owned-download-events-test-key"
	if err := a.saveSettings(t.Context(), a.Settings()); err != nil {
		t.Fatal(err)
	}
	admin := setupTestAdmin(t, a)
	observer := installOrderWorker(t, a, admin, os.Getenv("OWNED_TRANSFER_EVENTS_BINARY")).PluginID
	observerConfig := mustCleanSeedJSON(map[string]any{"enabled": true, "post_url": lark.URL + "/owned-post-add"})
	configuredObserver := callTest(t, a.Handler(), "PUT", "/api/plugins/"+observer, string(observerConfig), admin, "")
	assertOriginalRegistrySuccess(t, configuredObserver.Code, configuredObserver.Body.Bytes())
	a.Start(t.Context())
	installMergeSiteSwitchPackage(t, a, admin, bundle, "LarkMessager")
	api := a.Handler()
	const plugin = "/api/plugins/LarkMessager"
	configured := callTest(t, api, "PUT", plugin, `{"enabled":true,"app_id":"owned-app","app_secret":"owned-secret","chat_id":"oc_download","verification_token":"owned-verification","switchs":["资源下载"]}`, admin, "")
	assertOriginalRegistrySuccess(t, configured.Code, configured.Body.Bytes())

	post := func(eventType string, fields map[string]any) {
		t.Helper()
		raw := mustCleanSeedJSON(map[string]any{"header": map[string]any{"event_type": eventType}, "event": fields})
		result := callTest(t, api, "POST", plugin+"/api/webhook?token=owned-verification", string(raw), nil, "")
		if result.Code != 200 || !strings.Contains(result.Body.String(), `"success":true`) {
			t.Fatalf("webhook admission failed: %d", result.Code)
		}
	}
	click := func(value string) {
		t.Helper()
		post("card.action.trigger", map[string]any{
			"operator": map[string]any{"open_id": owner}, "context": map[string]any{"open_message_id": "om_download", "open_chat_id": "oc_download"},
			"action": map[string]any{"value": map[string]string{"callback_data": value}},
		})
	}
	next := func(fragments ...string) receivedCard {
		t.Helper()
		select {
		case card := <-cards:
			for _, fragment := range fragments {
				if !strings.Contains(card.Content, fragment) {
					t.Fatalf("card missing %q: %s", fragment, card.Content)
				}
			}
			return card
		case <-time.After(12 * time.Second):
			t.Fatalf("no card for %v", fragments)
			return receivedCard{}
		}
	}
	events := func() []struct {
		Type string
		Data json.RawMessage
	} {
		t.Helper()
		response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+observer+"/observations", "")
		var out []struct {
			Type string
			Data json.RawMessage
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &out) != nil {
			t.Fatalf("could not read SDK events: %d %s", response.Code, response.Body.String())
		}
		return out
	}

	post("im.message.receive_v1", map[string]any{
		"sender":  map[string]any{"sender_id": map[string]any{"open_id": owner}},
		"message": map[string]any{"message_id": "om_search", "chat_id": "oc_download", "message_type": "text", "content": `{"text":"搜索 测试影片"}`},
	})
	card := next("共找到1条相关信息")
	request := regexp.MustCompile(`media:[0-9a-f]{12}:`).FindString(card.Content)
	if request == "" {
		t.Fatal("no media callback")
	}
	click(request + "select:1")
	next("开始搜索")
	next("自动选择下载", request+"download:0")
	click(request + "download:0")
	next("请选择下载目录", "自动匹配目录", "第一个目录", "第二个目录", request+"download-dir:3")
	if adds.Load() != 0 || materials.Load() != 0 {
		t.Fatal("auto download ran before explicit directory selection")
	}
	click(request + "download-dir:3")
	card = next("测试影片", "开始下载", "站点：fixture", "种子："+release, "#/downloading")
	if card.Receiver != owner || card.ReceiverType != "open_id" || adds.Load() != 1 || materials.Load() != 1 {
		t.Fatalf("wrong recipient or Add: receiver=%s type=%s materials=%d adds=%d", card.Receiver, card.ReceiverType, materials.Load(), adds.Load())
	}
	var intentID, state string
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT id,json_extract(payload,'$.state') FROM records WHERE kind='intent'`).Scan(&intentID, &state); err != nil || state != "accepted" {
		t.Fatalf("Lark success started before durable acceptance: %s %v", state, err)
	}
	var facts int
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events WHERE event_id=?`, domainEventID("download.accepted", intentID)).Scan(&facts); err != nil || facts != 1 {
		t.Fatalf("atomic acceptance fact is missing: %d %v", facts, err)
	}
	frozen, err := a.loadDownloadContext(t.Context(), "intent", intentID, hash)
	if err != nil || frozen == nil || frozen.OriginalOrigin != "Lark" || frozen.ChatOrigin == nil ||
		frozen.ChatOrigin.Channel != "飞书" || frozen.ChatOrigin.Source != "Lark" || frozen.ChatOrigin.UserID != owner || frozen.ChatOrigin.Username != owner {
		t.Fatalf("accepted user's attribution is missing from sealed context: %v", err)
	}
	var sealed []byte
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT payload FROM records WHERE kind='download_context' AND id=?`, "intent:"+intentID).Scan(&sealed); err != nil || bytes.Contains(sealed, []byte(owner)) {
		t.Fatal("persisted chat attribution must remain sealed")
	}
	var selected, submitted bool
	for _, event := range events() {
		switch event.Type {
		case "download.added":
			t.Fatal("DownloadAdded was observed before notification completed")
		case "resource.selection":
			selected = true
		case "resource.download":
			var data struct {
				Channel, Origin string
				Options         struct {
					UserID   string `json:"userid"`
					Username string `json:"username"`
					SavePath string `json:"save_path"`
				}
			}
			if json.Unmarshal(event.Data, &data) != nil || data.Channel != "飞书" || data.Origin != "Lark" || data.Options.UserID != owner || data.Options.Username != owner || data.Options.SavePath != folderB {
				t.Fatalf("wrong ResourceDownload attribution: %s", event.Data)
			}
			submitted = true
		}
	}
	if !selected || !submitted {
		t.Fatalf("auto selection and download did not call both original hooks: selection=%t submission=%t", selected, submitted)
	}
	select {
	case <-postAdd:
		t.Fatal("post-Add module ran while the notification was still open")
	default:
	}
	unblock()
	select {
	case raw := <-postAdd:
		var call struct{ Kwargs map[string]json.RawMessage }
		if json.Unmarshal(raw, &call) != nil || string(call.Kwargs["download_dir"]) != string(mustCleanSeedJSON(folderB)) {
			t.Fatalf("post-Add callback lost the actual selected directory: %s", raw)
		}
		material, ok := plugins.DecodeModuleResultBytes(call.Kwargs["torrent_content"])
		if !ok || !bytes.Equal(material, torrent) {
			t.Fatal("post-Add callback lost the exact accepted material")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no actual post-Add module call")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		added := 0
		for _, event := range events() {
			if event.Type != "download.added" {
				continue
			}
			var data struct {
				Hash, Source, Username, Downloader string
			}
			if json.Unmarshal(event.Data, &data) != nil || data.Hash != hash || data.Source != "Lark" || data.Username != owner || data.Downloader != "Owned qBittorrent" {
				t.Fatalf("wrong DownloadAdded attribution: %s", event.Data)
			}
			added++
		}
		if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT state FROM jobs WHERE kind='download'`).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if added == 1 && state == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("did not observe one DownloadAdded and terminal job: added=%d state=%s", added, state)
		}
		time.Sleep(30 * time.Millisecond)
	}
	select {
	case unexpected := <-cards:
		t.Fatalf("unexpected duplicate acceptance card: %s", unexpected.Content)
	default:
	}
	t.Log("auto selection and explicit local directory reached one real Add; sealed acceptance and source attribution preceded owner notice; notice returned before SDK post-Add material and DownloadAdded event")
}
