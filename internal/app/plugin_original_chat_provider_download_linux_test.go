package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
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

func TestOriginalLarkProviderDownloadLinuxAMD64(t *testing.T) {
	bundle, observerBinary := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR"), os.Getenv("OWNED_TRANSFER_EVENTS_BINARY")
	if runtime.GOARCH != "amd64" || bundle == "" || observerBinary == "" {
		t.Skip("requires indexed Linux amd64 LarkMessager and SDK event observer")
	}
	t.Setenv("MP_SERVER_HOST", "")
	t.Setenv("SEARCH_MULTIPLE_NAME", "false")
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(key, "")
	}
	a, source, target := importTestApp(t)
	m := importFixture(t, a, "movie")
	const owner, title, providerID, imdbID = "ou_owned_provider_add", "来源独有片名", "771", "tt7654321"
	const release = title + ".2024.1080p.WEB-DL.x264-TEST"
	torrent, hash := workflowTorrent(title + " (2024) 1080p.mkv")
	var exactDetails atomic.Int32
	douban := map[string]any{"id": providerID, "type": "movie", "title": title, "original_title": "Source Display Title", "year": "2024", "imdb_id": imdbID, "intro": "豆瓣保留简介", "genres": []string{"科幻"}}
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Host != "frodo.douban.com" {
			t.Error("invalid owned Douban request")
			http.NotFound(w, r)
			return
		}
		switch r.URL.Path {
		case "/api/v2/search/weixin":
			if r.URL.Query().Get("q") != title {
				t.Error("Douban did not receive the chat search title")
			}
			workflowJSON(w, map[string]any{"items": []any{map[string]any{"type_name": "电影", "target": douban}}})
		case "/api/v2/movie/" + providerID:
			exactDetails.Add(1)
			workflowJSON(w, douban)
		case "/api/v2/movie/" + providerID + "/celebrities":
			workflowJSON(w, map[string]any{"directors": []any{}, "actors": []any{}})
		default:
			t.Errorf("unexpected Douban path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.Close)
	cert := provider.Certificate()
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: cert.DNSNames[0]}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if ip := net.ParseIP(host); network == "tcp" && err == nil && ip != nil && ip.IsLoopback() {
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}
		if network != "tcp" || err != nil || port != "443" || host != "frodo.douban.com" {
			return nil, errors.New("owned provider fixture refused an unrelated origin")
		}
		return (&net.Dialer{}).DialContext(ctx, network, provider.Listener.Addr().String())
	}}
	prior := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = prior; transport.CloseIdleConnections() })

	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := map[string]any{"id": m.ID, "title": m.Title, "original_title": m.OriginalTitle, "release_date": "2024-01-01", "original_language": m.OriginalLanguage}
		switch r.URL.Path {
		case "/3/find/" + imdbID:
			if exactDetails.Load() != 1 || r.URL.Query().Get("external_source") != "imdb_id" {
				t.Error("native mapping must use the selected exact Douban detail")
			}
			workflowJSON(w, map[string]any{"movie_results": []any{info}, "tv_results": []any{}})
		case "/3/movie/123":
			info["overview"] = "TMDB auxiliary overview"
			info["external_ids"] = map[string]any{"imdb_id": "tt1111111", "tvdb_id": 876}
			info["genres"] = []any{map[string]any{"id": 878, "name": "科幻"}}
			workflowJSON(w, info)
		default:
			t.Errorf("unexpected metadata path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(metadata.Close)
	installMetadataFixture(t, a, "owned-provider-download-tmdb", []string{"/3/find/" + imdbID, "/3/movie/123"}, ownedLoopbackFixtureTransport(t, metadata).RoundTrip)

	var queries, materials, adds atomic.Int32
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			if r.URL.Query().Get("search") != title {
				t.Errorf("tracker query switched away from selected primary title: %q", r.URL.Query().Get("search"))
			}
			queries.Add(1)
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
				t.Errorf("wrong configured download path: %q, %v", r.FormValue("savepath"), err)
			}
			file, _, err := r.FormFile("torrents")
			if err != nil {
				t.Error(err)
			} else {
				defer file.Close()
				raw, err := io.ReadAll(file)
				if err != nil || !bytes.Equal(raw, torrent) {
					t.Error("qB did not receive exact tracker material")
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

	cards := make(chan string, 8)
	lark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch path := strings.TrimPrefix(r.URL.Path, "/open-apis"); {
		case path == "/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-provider-fixture"}`)
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
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_provider_download","chat_id":"oc_provider_download"}}`)
		case path == "/owned-post-add" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected Lark path: %s", path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(lark.Close)
	a.pluginOriginalLarkTestOrigin = lark.URL + "/open-apis"
	workflowConfigure(t, a, tracker.URL, qb.URL)
	searchSource := "douban"
	a.config.SearchSource = &searchSource
	a.config.Downloaders[0].DownloadPath, a.config.Downloaders[0].SharedPaths = source, true
	a.config.Directories = []DirectoryRule{{ID: "provider-owned", Name: "来源测试目录", SourcePath: source, LibraryPath: target, RootID: "root", MediaType: "movie", Mode: "hardlink", MonitorType: "none", Enabled: true}}
	a.config.APIToken = "owned-provider-download-events"
	if err := a.saveSettings(t.Context(), a.Settings()); err != nil {
		t.Fatal(err)
	}
	admin := setupTestAdmin(t, a)
	observer := installOrderWorker(t, a, admin, observerBinary).PluginID
	configuredObserver := callTest(t, a.Handler(), "PUT", "/api/plugins/"+observer, string(mustCleanSeedJSON(map[string]any{"enabled": true, "post_url": lark.URL + "/owned-post-add"})), admin, "")
	assertOriginalRegistrySuccess(t, configuredObserver.Code, configuredObserver.Body.Bytes())
	a.Start(t.Context())
	installMergeSiteSwitchPackage(t, a, admin, bundle, "LarkMessager")
	api := a.Handler()
	const plugin = "/api/plugins/LarkMessager"
	configured := callTest(t, api, "PUT", plugin, `{"enabled":true,"app_id":"owned-app","app_secret":"owned-secret","chat_id":"oc_provider_download","verification_token":"owned-verification","switchs":["资源下载"]}`, admin, "")
	assertOriginalRegistrySuccess(t, configured.Code, configured.Body.Bytes())
	post := func(eventType string, fields map[string]any) {
		t.Helper()
		raw := mustCleanSeedJSON(map[string]any{"header": map[string]any{"event_type": eventType}, "event": fields})
		result := callTest(t, api, "POST", plugin+"/api/webhook?token=owned-verification", string(raw), nil, "")
		var ack struct{ Success bool }
		if err := json.Unmarshal(result.Body.Bytes(), &ack); result.Code != 200 || err != nil || !ack.Success {
			t.Fatalf("webhook admission failed: %d %s", result.Code, result.Body.String())
		}
	}
	click := func(value string) {
		t.Helper()
		post("card.action.trigger", map[string]any{
			"operator": map[string]any{"open_id": owner}, "context": map[string]any{"open_message_id": "om_provider_download", "open_chat_id": "oc_provider_download"},
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
		"sender":  map[string]any{"sender_id": map[string]any{"open_id": owner}},
		"message": map[string]any{"message_id": "om_provider_search", "chat_id": "oc_provider_download", "message_type": "text", "content": string(mustCleanSeedJSON(map[string]string{"text": "搜索 " + title}))},
	})
	card := next(title, "共找到1条相关信息")
	request := regexp.MustCompile(`media:[0-9a-f]{12}:`).FindString(card)
	if request == "" {
		t.Fatal("no source selection callback")
	}
	click(request + "select:1")
	next("开始搜索", title)
	next(title, "共找到1条相关资源", release)
	click(request + "download:1")
	next(title+" (2024)", "开始下载", release)
	if queries.Load() != 3 || materials.Load() != 1 || adds.Load() != 1 || exactDetails.Load() != 1 {
		t.Fatalf("incorrect provider download effects: queries=%d materials=%d adds=%d detail=%d", queries.Load(), materials.Load(), adds.Load(), exactDetails.Load())
	}
	var intentID string
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT id FROM records WHERE kind='intent'`).Scan(&intentID); err != nil {
		t.Fatal(err)
	}
	var intent Intent
	if err := a.Store.Get(t.Context(), "intent", intentID, &intent); err != nil || intent.State != "accepted" || intent.Media.ID != m.ID || intent.Media.Title != m.Title || intent.Hash != hash {
		t.Fatalf("native identity or real receipt changed: %+v %v", intent, err)
	}
	assertMedia := func(raw json.RawMessage) {
		t.Helper()
		var media struct {
			Source, Title, Overview string
			MediaID                 string `json:"media_id"`
			DoubanID                string `json:"douban_id"`
			TMDBID                  int64  `json:"tmdb_id"`
			IMDbID                  string `json:"imdb_id"`
			TVDBID                  int64  `json:"tvdb_id"`
			TMDBInfo                struct {
				ID    int64
				Title string
			} `json:"tmdb_info"`
		}
		if err := json.Unmarshal(raw, &media); err != nil || media.Source != "douban" || media.MediaID != providerID || media.DoubanID != providerID || media.Title != title || media.Overview != "豆瓣保留简介" || media.IMDbID != imdbID || media.TMDBID != m.ID || media.TVDBID != 876 || media.TMDBInfo.ID != m.ID || media.TMDBInfo.Title != m.Title {
			t.Fatalf("primary media and auxiliary TMDB were not preserved: %+v %v", media, err)
		}
	}
	frozen, err := a.loadDownloadContext(t.Context(), "intent", intentID, hash)
	if err != nil || frozen == nil || frozen.Original == nil || frozen.ChatProvider == nil || *frozen.ChatProvider != (originalChatMediaProvider{Source: "douban", ID: providerID, TMDBID: m.ID}) {
		t.Fatalf("no accepted provider context: %v", err)
	}
	assertMedia(frozen.Original.Media)
	deadline := time.Now().Add(10 * time.Second)
	for {
		response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+observer+"/observations", "")
		var events []struct {
			Type string
			Data struct{ Context originalDownloadContext }
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &events) != nil {
			t.Fatalf("could not read real SDK event observations: %d", response.Code)
		}
		var before, after bool
		for _, event := range events {
			switch event.Type {
			case "resource.download":
				assertMedia(event.Data.Context.Media)
				before = true
			case "download.added":
				assertMedia(event.Data.Context.Media)
				after = true
			}
		}
		if before && after {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("missing native download events: resource=%t added=%t", before, after)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Log("Douban primary identity/title reached tracker search, exact qB Add, the Lark notice, both SDK events and the sealed native TMDB-linked context")
}
