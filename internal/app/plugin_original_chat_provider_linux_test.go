package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/priorityrules"
	"light-media/internal/sites"
)

func TestOriginalLarkProviderSelectionLinuxAMD64(t *testing.T) {
	testOriginalLarkProviderSelection(t, "mapped")
}

func TestOriginalLarkProviderWithoutTMDBLinuxAMD64(t *testing.T) {
	t.Setenv("SEARCH_MULTIPLE_NAME", "false")
	t.Setenv("RECOGNIZE_SOURCE", "themoviedb")
	testOriginalLarkProviderSelection(t, "unmapped")
}

func TestOriginalLarkProviderSubscriptionRSSDownloadLinuxAMD64(t *testing.T) {
	t.Setenv("RECOGNIZE_SOURCE", "bangumi")
	t.Setenv("SUBSCRIBE_MODE", "rss")
	testOriginalLarkProviderSelection(t, "provider_subscription")
}

func TestOriginalLarkProviderBestVersionRSSLinuxAMD64(t *testing.T) {
	t.Setenv("RECOGNIZE_SOURCE", "bangumi")
	t.Setenv("SUBSCRIBE_MODE", "rss")
	testOriginalLarkProviderSelection(t, "provider_subscription_quality")
}

func TestOriginalLarkProviderNativeDownloadLinuxAMD64(t *testing.T) {
	t.Setenv("SEARCH_MULTIPLE_NAME", "false")
	t.Setenv("RECOGNIZE_SOURCE", "themoviedb")
	testOriginalLarkProviderSelection(t, "provider_download")
}

func TestOriginalLarkProviderUnscopedDownloadLinuxAMD64(t *testing.T) {
	t.Setenv("SEARCH_MULTIPLE_NAME", "false")
	t.Setenv("RECOGNIZE_SOURCE", "themoviedb")
	testOriginalLarkProviderSelection(t, "provider_unscoped")
}

func TestOriginalLarkProviderLateScopeLinuxAMD64(t *testing.T) {
	t.Setenv("SEARCH_MULTIPLE_NAME", "false")
	t.Setenv("RECOGNIZE_SOURCE", "themoviedb")
	testOriginalLarkProviderSelection(t, "provider_late_scope")
}

func TestOriginalLarkProviderScrapeSidecarsLinuxAMD64(t *testing.T) {
	t.Setenv("SEARCH_MULTIPLE_NAME", "false")
	t.Setenv("RECOGNIZE_SOURCE", "bangumi")
	t.Setenv("SCRAP_SOURCE", "anilist")
	testOriginalLarkProviderSelection(t, "provider_scrape")
}

func testOriginalLarkProviderSelection(t *testing.T, mode string) {
	t.Helper()
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	for _, env := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(env, "")
	}
	providerQuality := mode == "provider_subscription_quality"
	providerEpisodeQuality := mode == "provider_subscription_episode_quality"
	qualitySubscription := providerQuality || providerEpisodeQuality
	providerBrowser := mode == "provider_subscription_browser"
	providerOnly, providerSubscription := mode != "mapped", mode == "provider_subscription" || qualitySubscription || providerBrowser
	providerScrape := mode == "provider_scrape"
	providerLateScope := mode == "provider_late_scope"
	providerUnscoped := mode == "provider_unscoped" || providerLateScope
	providerDownload := mode == "provider_download" || providerScrape || providerUnscoped
	var a *App
	var source, library string
	var closeBrowserApp func() error
	if providerBrowser {
		a, closeBrowserApp = openOriginalLarkProviderBrowserApp(t, t.TempDir())
		defer closeBrowserApp()
	} else {
		a, source, library = importTestApp(t)
	}
	m := importFixture(t, a, "tv")
	episodeCount, wantEpisodes := 4, []int{1, 2, 3, 4}
	if providerQuality {
		episodeCount, wantEpisodes = 1, []int{1}
	}
	sources := "anilist,bangumi,douban" // The settings list enables; priorities still order results.
	if providerOnly {
		sources = "anilist"
	}
	a.config.SearchSource = &sources
	if providerBrowser {
		if err := a.saveSettings(t.Context(), a.Settings()); err != nil {
			t.Fatal(err)
		}
	}
	const sourceID = 777
	var detailCalls, fallbackTitles, fallbackDetails atomic.Int32
	var lateEpisodes atomic.Bool
	var orderMu sync.Mutex
	var order []string
	record := func(source string) {
		orderMu.Lock()
		defer orderMu.Unlock()
		order = append(order, source)
	}
	anime := map[string]any{
		"id": sourceID, "format": "TV", "title": map[string]string{"native": m.Title, "romaji": "Owned Anime", "english": "Owned Anime"},
		"startDate": map[string]int{"year": 2024, "month": 1, "day": 1}, "endDate": map[string]int{},
		"episodes": episodeCount, "coverImage": map[string]string{}, "genres": []string{"Action"},
		"characters": map[string]any{"edges": []any{}}, "staff": map[string]any{"edges": []any{}}, "studios": map[string]any{"nodes": []any{}},
		"synonyms": []any{}, "externalLinks": []any{},
	}
	if providerUnscoped {
		delete(anime, "episodes")
	}
	providers := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Host == "frodo.douban.com" && r.URL.Path == "/api/v2/search/weixin" && r.Method == http.MethodGet:
			record("douban")
			if r.URL.Query().Get("q") != m.Title {
				t.Error("wrong signed Douban query")
			}
			target := map[string]any{"id": "771", "type": "tv", "title": m.Title, "year": "2024", "genres": []string{"动画"}}
			workflowJSON(w, map[string]any{"items": []any{map[string]any{"type_name": "电视剧", "target": target}}})
		case (r.Host == "api.bgm.tv" || r.Host == "api.bangumi.tv") && r.URL.Path == "/search/subject/"+m.Title && r.Method == http.MethodGet:
			record("bangumi")
			id := 772
			if providerSubscription || providerScrape {
				if detailCalls.Load() == 0 {
					t.Error("configured fallback did not follow the selected AniList detail")
				}
				fallbackTitles.Add(1)
				id = sourceID
				if providerScrape {
					id = 778
				}
			}
			workflowJSON(w, map[string]any{"list": []any{map[string]any{"id": id, "type": 2, "name": m.Title, "name_cn": m.Title, "air_date": "2024-01-01", "platform": "TV"}}})
		case providerSubscription && (r.Host == "api.bgm.tv" || r.Host == "api.bangumi.tv") && r.URL.Path == "/v0/subjects/777" && r.Method == http.MethodGet:
			fallbackDetails.Add(1)
			workflowJSON(w, map[string]any{"id": sourceID, "type": 2, "name": "Owned Bangumi", "name_cn": m.Title, "date": "2024-01-01", "platform": "TV", "total_episodes": episodeCount, "summary": "owned source subscription fallback", "infobox": []any{map[string]any{"key": "别名", "value": m.Title}}})
		case providerSubscription && (r.Host == "api.bgm.tv" || r.Host == "api.bangumi.tv") && r.URL.Path == "/v0/subjects/777/characters" && r.Method == http.MethodGet:
			workflowJSON(w, []any{})
		case providerScrape && (r.Host == "api.bgm.tv" || r.Host == "api.bangumi.tv") && r.URL.Path == "/v0/subjects/778" && r.Method == http.MethodGet:
			fallbackDetails.Add(1)
			workflowJSON(w, map[string]any{"id": 778, "name": "Owned Episode Recognition", "name_cn": m.Title, "date": "2024-01-01", "platform": "TV", "total_episodes": episodeCount, "summary": "owned episode recognition plot"})
		case providerScrape && (r.Host == "api.bgm.tv" || r.Host == "api.bangumi.tv") && r.URL.Path == "/v0/subjects/778/characters" && r.Method == http.MethodGet:
			workflowJSON(w, []any{})
		case r.Host == "raw.githubusercontent.com" && r.URL.Path == "/soruly/anilist-chinese/master/anilist-chinese.json" && r.Method == http.MethodGet:
			workflowJSON(w, []any{})
		case (r.Host == "trace.moe" && r.URL.Path == "/anilist/" || r.Host == "graphql.anilist.co" && r.URL.Path == "/") && r.Method == http.MethodPost:
			var query struct {
				Query     string
				Variables struct {
					ID     int64
					Search string
					Count  int
				}
			}
			if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
				t.Error(err)
			}
			if query.Variables.ID != 0 {
				if query.Variables.ID != sourceID {
					t.Errorf("selected the wrong AniList identity %d", query.Variables.ID)
				}
				detailCalls.Add(1)
				detail := anime
				if lateEpisodes.Load() {
					detail = make(map[string]any, len(anime)+1)
					for key, value := range anime {
						detail[key] = value
					}
					detail["episodes"] = 8
				}
				workflowJSON(w, map[string]any{"data": map[string]any{"Media": detail}})
				return
			}
			record("anilist")
			if query.Variables.Search != m.Title || query.Variables.Count != 20 || !strings.Contains(query.Query, "SEARCH_MATCH") {
				t.Errorf("unexpected AniList search variables: %+v", query.Variables)
			}
			workflowJSON(w, map[string]any{"data": map[string]any{"Page": map[string]any{"media": []any{anime}}}})
		default:
			t.Errorf("unexpected source provider request: %s %s %s", r.Method, r.Host, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(providers.Close)
	allowed := []string{"frodo.douban.com", "api.bgm.tv", "api.bangumi.tv", "trace.moe", "graphql.anilist.co", "raw.githubusercontent.com"}
	cert := providers.Certificate()
	if len(cert.DNSNames) == 0 {
		t.Fatal("owned HTTPS peer has no certificate name")
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: cert.DNSNames[0]}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if ip := net.ParseIP(host); network == "tcp" && err == nil && ip != nil && ip.IsLoopback() {
			// Lark and the indexed package installer also use the isolated
			// container's own loopback test listeners.
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}
		if network != "tcp" || err != nil || port != "443" || !slices.Contains(allowed, host) {
			return nil, errors.New("owned provider fixture refused an unrelated origin")
		}
		return (&net.Dialer{}).DialContext(ctx, network, providers.Listener.Addr().String())
	}}
	prior := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = prior; transport.CloseIdleConnections() })

	var metadataLookups atomic.Int32
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if providerOnly && r.URL.Path != "/3/search/tv" {
			t.Errorf("provider-only selection requested native metadata: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		switch r.URL.Path {
		case "/3/search/tv":
			if detailCalls.Load() == 0 {
				t.Error("native identity lookup ran before selected provider detail")
			}
			metadataLookups.Add(1)
			if providerOnly {
				workflowJSON(w, map[string]any{"page": 1, "total_pages": 1, "total_results": 0, "results": []any{}})
				return
			}
			workflowJSON(w, map[string]any{"page": 1, "total_pages": 1, "total_results": 1, "results": []map[string]any{
				{"id": m.ID, "name": m.Title, "original_name": "Owned Anime", "first_air_date": "2024-01-01"},
			}})
		case "/3/tv/123":
			workflowJSON(w, map[string]any{"id": m.ID, "name": m.Title, "original_name": "Owned Anime", "first_air_date": "2024-01-01",
				"original_language": m.OriginalLanguage, "origin_country": m.OriginCountry, "production_countries": m.ProductionCountries,
				"genres": m.Genres, "overview": m.Overview, "number_of_seasons": 1, "number_of_episodes": 4,
				"seasons": []any{map[string]any{"id": 340, "season_number": 1, "episode_count": 4, "air_date": "2024-01-01"}},
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
			t.Errorf("unexpected TMDB request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(metadata.Close)
	paths := []string{"/3/search/tv", "/3/tv/123", "/3/tv/123/season/1", "/3/tv/123/images"}
	installMetadataFixture(t, a, "owned-source-tmdb", paths, ownedLoopbackFixtureTransport(t, metadata).RoundTrip)
	const unrelated = "无关剧集.2024.S01E01.1080p.WEB-DL.x264-TEST"
	release := m.Title + ".2024.S01E01.1080p.WEB-DL.x264-TEST"
	wrongRelease := m.Title + ".2024.S01E01.1080p.WEB-DL.x264-WRONG"
	file := m.Title + " S01E01 1080p.mkv"
	if providerUnscoped {
		release = m.Title + ".2024.S03E07.1080p.WEB-DL.x264-TEST"
		file = m.Title + " S03E07 1080p.mkv"
		if providerLateScope {
			release = m.Title + ".2024.S01E07-E08.1080p.WEB-DL.x264-TEST"
			file = m.Title + " S01E07-E08 1080p.mkv"
		}
	}
	torrent, hash := workflowTorrent(file)
	var trackerURL string
	var searchCalls, materialCalls, addCalls, fileCalls, rssCalls atomic.Int32
	var completed atomic.Bool
	if providerOnly && !providerBrowser {
		matching := workflowHTML(release, "", false)
		_, row, _ := strings.Cut(workflowHTML(unrelated, "", false), "</th></tr>")
		result := strings.TrimSuffix(matching, "</table>") + strings.ReplaceAll(row, "id=101", "id=102")
		tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/rss.php":
				rssCalls.Add(1)
				w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
				_, _ = io.WriteString(w, `<rss version="2.0"><channel>
<item><title>`+wrongRelease+`</title><pubDate>Sat, 31 Oct 2026 00:01:00 GMT</pubDate><link>details.php?id=201</link><enclosure url="/owned-provider-wrong" length="1024"/></item>
<item><title>`+release+`</title><pubDate>Sat, 31 Oct 2026 00:00:00 GMT</pubDate><link>details.php?id=202</link><enclosure url="/owned-provider-match" length="1024"/></item>
</channel></rss>`)
			case "/torrents.php":
				searchCalls.Add(1)
				if r.URL.Query().Get("search") != m.Title {
					t.Error("tracker did not receive the selected provider title")
				}
				_, _ = io.WriteString(w, result)
			case "/download.php":
				materialCalls.Add(1)
				if providerDownload {
					if r.Method != http.MethodGet || r.URL.Query().Get("id") != "101" || r.Header.Get("Cookie") != "fixture-cookie" {
						t.Error("tracker did not receive the authenticated request for the selected torrent")
						http.NotFound(w, r)
						return
					}
					w.Header().Set("Content-Type", "application/x-bittorrent")
					_, _ = w.Write(torrent)
					return
				}
				http.NotFound(w, r)
			case "/owned-provider-match":
				materialCalls.Add(1)
				if r.Method != http.MethodGet || r.Header.Get("Cookie") != "fixture-cookie" {
					t.Error("provider RSS enclosure did not use the owned tracker credentials")
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/x-bittorrent")
				_, _ = w.Write(torrent)
			case "/owned-provider-wrong":
				t.Error("RSS fetched material from the different provider with the same numeric ID")
				http.NotFound(w, r)
			case "/api/v2/torrents/add":
				addCalls.Add(1)
				http.NotFound(w, r)
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(tracker.Close)
		trackerURL = tracker.URL
		downloaderURL := tracker.URL
		if providerDownload || providerSubscription {
			downloaderURL = originalLarkNativeProviderDownloadPeer(t, source, file, hash, torrent, &addCalls, &fileCalls, &completed)
		}
		workflowConfigure(t, a, tracker.URL, downloaderURL)
		if providerSubscription {
			a.config.Sites[0].RSS = tracker.URL + "/rss.php"
		}
		if providerDownload || providerSubscription {
			a.config.Downloaders[0].DownloadPath, a.config.Downloaders[0].SharedPaths = source, true
			a.config.Directories = []DirectoryRule{{ID: "owned-provider-native", Name: "来源下载测试目录",
				SourcePath: source, LibraryPath: library, RootID: "root", MediaType: "tv", Mode: "hardlink", MonitorType: "downloader", Enabled: true}}
			if providerScrape {
				a.config.Directories[0].Scraping = importFlag(true)
			}
			if qualitySubscription {
				overwrite := "always"
				a.config.Directories[0].OverwriteModeKnown, a.config.Directories[0].OverwriteMode = true, &overwrite
				a.config.DefaultTVSubscribeConfig = mustCleanSeedJSON(map[string]bool{"best_version_full": providerQuality})
				priority := "WEBDL & 1080P"
				if providerEpisodeQuality {
					priority += " > WEBDL & 720P"
				}
				a.config.PriorityRules = &priorityrules.Configuration{SubscribeGroups: []string{"quality"}, Groups: []priorityrules.Group{{Name: "quality", RuleString: priorityText(priority)}}, Mode: priorityrules.Rust}
			}
			if err := a.saveSettings(t.Context(), a.Settings()); err != nil {
				t.Fatal(err)
			}
		}
	}

	cards := make(chan string, 8)
	lark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch path := strings.TrimPrefix(r.URL.Path, "/open-apis"); {
		case path == "/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-provider-fixture"}`)
		case strings.Contains(path, "/reactions"):
			_, _ = io.WriteString(w, `{"code":0,"data":{"reaction_id":"rx_fixture"}}`)
		case path == "/im/v1/messages" && r.Method == http.MethodPost || providerSubscription && path == "/im/v1/messages/om_provider" && r.Method == http.MethodPatch:
			var in struct{ Content string }
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			select {
			case cards <- in.Content:
			default:
				t.Error("unexpected extra Lark card")
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_provider","chat_id":"oc_provider"}}`)
		default:
			t.Errorf("unexpected Lark request: %s %s", r.Method, r.URL.Path)
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
	switchName := "订阅"
	if providerDownload {
		switchName = "资源下载"
	}
	configured := callTest(t, api, "PUT", plugin, `{"enabled":true,"app_id":"owned-app","app_secret":"owned-secret","chat_id":"oc_provider","verification_token":"owned-verification","switchs":["`+switchName+`"]}`, admin, "")
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
	next := func(fragments ...string) string {
		t.Helper()
		timer := time.NewTimer(12 * time.Second)
		defer timer.Stop()
		for {
			select {
			case card := <-cards:
				if providerQuality && strings.Contains(card, "已完成洗版") {
					continue // Completion and refresh have separate senders.
				}
				for _, fragment := range fragments {
					if !strings.Contains(card, fragment) {
						t.Fatalf("card missing %q: %s", fragment, card)
					}
				}
				return card
			case <-timer.C:
				t.Fatalf("no card for %v", fragments)
				return ""
			}
		}
	}
	send := func(messageID, value string) {
		post("im.message.receive_v1", map[string]any{
			"sender":  map[string]any{"sender_id": map[string]any{"open_id": "ou_provider"}},
			"message": map[string]any{"message_id": messageID, "chat_id": "oc_provider", "message_type": "text", "content": string(mustCleanSeedJSON(map[string]string{"text": value}))},
		})
	}
	click := func(value string) {
		post("card.action.trigger", map[string]any{
			"operator": map[string]any{"open_id": "ou_provider"}, "context": map[string]any{"open_message_id": "om_provider", "open_chat_id": "oc_provider"},
			"action": map[string]any{"value": map[string]string{"callback_data": value}},
		})
	}
	callbackPrefix := regexp.MustCompile(`media:[0-9a-f]{12}:`)
	if providerSubscription {
		command := "订阅"
		if qualitySubscription {
			command = "洗版"
		}
		send("om_subscribe", command+" "+m.Title)
		selection := callbackPrefix.FindString(next("共找到1条相关信息", m.Title, "select:1"))
		if selection == "" {
			t.Fatal("missing AniList selection callback")
		}
		click(selection + "select:1")
		next(m.Title, "订阅")
		saved := awaitOriginalLarkProviderFixtureSubscription(t, a)
		if saved.Media.ID != 0 || saved.Media.ProviderSource != "bangumi" || saved.Media.ProviderID != "777" || mediaKey(saved.Media) != "tv:bangumi:777" ||
			saved.Season != 1 || !slices.Equal(saved.ProviderEpisodes, wantEpisodes) || detailCalls.Load() != 1 || fallbackTitles.Load() != 1 || fallbackDetails.Load() == 0 {
			t.Fatalf("configured title fallback did not persist the true provider-only identity: media=%s id=%d season=%d episodes=%v network=%d/%d/%d", mediaKey(saved.Media), saved.Media.ID, saved.Season, saved.ProviderEpisodes, detailCalls.Load(), fallbackTitles.Load(), fallbackDetails.Load())
		}
		var eventMedia string
		if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT json_extract(payload,'$.media_id') FROM domain_events
			WHERE json_extract(payload,'$.event_type')='subscription.added' AND json_extract(payload,'$.subscription_id')=?`, saved.ID).Scan(&eventMedia); err != nil || eventMedia != "tv:bangumi:777" {
			t.Fatalf("SubscribeAdded lost the verified provider namespace: media=%q err=%v", eventMedia, err)
		}
		origin, err := a.sourceSubscriptionOrigin(t.Context(), saved)
		if err != nil || origin == nil || origin.Source != "Lark" || origin.State != "N" {
			t.Fatalf("missing actual Lark subscription creation origin: %v", err)
		}
		raw, err := decodeSourceJSON(origin.MediaInfo)
		fields := sourceMap(raw)
		if err != nil || sourceText(fields["source"]) != "bangumi" || sourceStringValue(fields["bangumi_id"]) != "777" || sourceSubscriptionTruth(fields["tmdb_id"]) || sourceSubscriptionTruth(fields["anilist_id"]) {
			t.Fatalf("sealed MediaInfo mixed selected and verified identities: %v", err)
		}
		if providerBrowser {
			// This scenario exercises editing and reopening. No scheduled search
			// or tracker/downloader effects belong to it.
			saved.Schedule, saved.NextCheck = Schedule{Mode: "manual"}, ""
			if err := a.Store.Put(t.Context(), "subscription", saved.ID, saved); err != nil {
				t.Fatal(err)
			}
			originalLarkProviderBrowserRestart(t, a, closeBrowserApp, saved)
			return
		}
		var oldAsset Asset
		if providerEpisodeQuality {
			oldAsset = seedOriginalLarkProviderEpisodeAsset(t, a, source, saved, origin.MediaInfo)
		}
		// Synthetic historical recognition for an existing RSS row. This lets
		// one configured provider still exercise an actual cross-provider match.
		site := a.Settings().Sites[0]
		wrongID := "rss_" + tokenHash(trackerURL+"/owned-provider-wrong")
		goodID := "rss_" + tokenHash(trackerURL+"/owned-provider-match")
		cached := originalChatLatestSite{SiteID: site.ID, Key: a.originalChatLatestSiteKey(site, a.Settings().ProxyHost), Rows: []originalChatLatestRow{{
			Resource: sites.Resource{ID: wrongID, SiteID: site.ID, Title: wrongRelease, Size: 1024, PublishedAt: "2026-10-31 08:01:00", DownloadFactor: 1, UploadFactor: 1},
			Media:    mustCleanSeedJSON(map[string]any{"source": "anilist", "media_id": sourceID, "anilist_id": sourceID, "type": "电视剧", "title": m.Title}),
		}}}
		tx, err := a.Store.DB.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err := a.originalChatLatestWrite(t.Context(), tx, originalChatLatestCacheKind, "rss:"+site.ID, cached); err != nil {
			t.Fatal(err)
		}
		// The source refresh only matches running subscriptions. Advance the
		// fixture's new record to that state without a second search or refresh.
		saved.SourceState, saved.NextCheck, saved.Schedule = "R", "", Schedule{Mode: "manual"}
		if providerQuality {
			if !saved.BestVersion || !saved.BestVersionFull || saved.StartEpisode != 1 || saved.TotalEpisodes != 1 {
				t.Fatalf("Lark did not create full quality for the sole known episode: best=%t full=%t start=%d total=%d", saved.BestVersion, saved.BestVersionFull, saved.StartEpisode, saved.TotalEpisodes)
			}
			initialPriority := 20
			saved.CurrentPriority = &initialPriority
		} else if providerEpisodeQuality {
			initialPriority := 0
			saved.CurrentPriority = &initialPriority
			saved.EpisodePriority = map[string]int{"1": 99}
			saved.SourceNote = []int{1}
		}
		if err := txPut(t.Context(), tx, "subscription", saved.ID, saved); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		send("om_subscribes", "/subscribes")
		panel := regexp.MustCompile(`subscribes:[0-9a-f]{12}:`).FindString(next(m.Title, "订阅中"))
		if panel == "" {
			t.Fatal("missing subscription refresh callback")
		}
		click(panel + "refresh")
		next("开始刷新订阅")
		next("订阅刷新执行完成")
		if providerQuality {
			next(originalSubscribeChatTitle)
		} else {
			next(m.Title, "订阅中")
		}
		root, err := scanJob(a.Store.DB.QueryRowContext(t.Context(), `SELECT `+jobColumns+` FROM jobs
			WHERE kind='subscription' AND json_extract(payload,'$.id')=? AND json_extract(payload,'$._chat_rss_fallback')=1`, saved.ID))
		var input originalChatLatestJob
		if err != nil || root.State != "succeeded" || json.Unmarshal(root.Payload, &input) != nil || input.Revision != saved.Revision {
			t.Fatalf("missing successful provider-only RSS match root: state=%s code=%s err=%v", root.State, root.ErrorCode, err)
		}
		var batch originalChatLatestBatch
		if err := a.originalChatLatestRead(t.Context(), a.Store.DB, originalChatLatestBatchKind, input.LatestBatch, &batch); err != nil || !batch.ownsRSSRoot(root) {
			t.Fatalf("native root did not own the actual sealed RSS batch: %v", err)
		}
		recognized := map[string]string{}
		for _, entry := range batch.Sites {
			for _, row := range entry.Rows {
				recognized[row.Resource.ID] = sourceText(originalChatLatestMediaFields(row.Media).Get("source"))
			}
		}
		if recognized[wrongID] != "anilist" || recognized[goodID] != "bangumi" {
			t.Fatal("RSS batch did not contain both distinct provider recognition facts", recognized)
		}
		if rssCalls.Load() != 1 || searchCalls.Load() != 0 {
			t.Fatalf("RSS acquisition did not use only its source batch: rss=%d search=%d", rssCalls.Load(), searchCalls.Load())
		}
		if providerQuality {
			assertOriginalLarkProviderRSSQualityAcceptance(t, a, saved, root, input.LatestBatch, goodID)
		} else if providerEpisodeQuality {
			assertOriginalLarkProviderEpisodeQualityAccepted(t, a, saved, oldAsset, root, hash)
		}
		asset := assertOriginalLarkNativeProviderDownload(t, a, source, library, file, hash, torrent, &materialCalls, &addCalls, &fileCalls, &completed, &saved, goodID, providerEpisodeQuality)
		if providerEpisodeQuality {
			assertOriginalLarkProviderEpisodeReplacement(t, a, oldAsset, asset)
		} else if providerQuality {
			t.Log("verified one full-quality Lark RSS root reached priority 100, retired Bangumi by its sealed accepted download, and organized S01E01")
		} else {
			t.Log("verified one ordinary Lark RSS root downloaded and organized only the exact Bangumi candidate, excluding AniList with the same numeric ID")
		}
		return
	}
	if providerOnly && !providerSubscription {
		send("om_search", "搜索 "+m.Title)
		first := callbackPrefix.FindString(next("共找到1条相关信息", m.Title, "select:1"))
		if first == "" {
			t.Fatal("missing provider selection callback")
		}
		click(first + "select:1")
		if !providerUnscoped {
			next("第 1 季总 4 集")
		}
		next("开始搜索", m.Title)
		if card := next("共找到1条相关资源", release, "download:1"); strings.Contains(card, unrelated) {
			t.Fatal("unrelated release passed selected provider title matching")
		}
		wantDetails := int32(1)
		if providerUnscoped {
			wantDetails++ // The empty selected season map must trigger one fresh provider retry.
		}
		if detailCalls.Load() != wantDetails || metadataLookups.Load() == 0 || searchCalls.Load() != 1 {
			t.Fatalf("wanted %d selected provider details and one tracker query after missing TMDB mapping, got %d/%d/%d", wantDetails, detailCalls.Load(), metadataLookups.Load(), searchCalls.Load())
		}
		if providerDownload {
			if materialCalls.Load() != 0 || addCalls.Load() != 0 || fileCalls.Load() != 0 {
				t.Fatal("search fetched material or mutated the downloader before explicit resource selection")
			}
			click(first + "download:1")
			next("开始下载", release)
			if providerLateScope {
				assertOriginalLarkProviderLateScope(t, a, source, library, file, &lateEpisodes, &completed, &detailCalls, &materialCalls, &addCalls, &fileCalls)
				return
			}
			if providerUnscoped {
				assertOriginalLarkUnscopedProviderDownload(t, a, source, file, hash, torrent, &materialCalls, &addCalls, &fileCalls, &completed)
				return
			}
			if providerScrape && (fallbackTitles.Load() != 0 || fallbackDetails.Load() != 0) {
				t.Fatal("the episode recognizer ran before the downloader supplied a completed file")
			}
			asset := assertOriginalLarkNativeProviderDownload(t, a, source, library, file, hash, torrent, &materialCalls, &addCalls, &fileCalls, &completed, nil, "", false)
			if providerScrape {
				assertOriginalLarkProviderScrape(t, a, asset, m.Title)
				if fallbackTitles.Load() != 1 || fallbackDetails.Load() != 1 {
					t.Fatalf("expected one Bangumi recognition from the local episode path: title=%d detail=%d", fallbackTitles.Load(), fallbackDetails.Load())
				}
			}
			return
		}
		send("om_subscribe", "订阅 "+m.Title)
		second := callbackPrefix.FindString(next("共找到1条相关信息", m.Title, "select:1"))
		if second == "" || second == first {
			t.Fatal("missing distinct subscription selection callback")
		}
		click(second + "select:1")
		if card := next(); !strings.Contains(card, "未识别到媒体信息") {
			t.Fatalf("unverified configured title fallback did not report its identity failure: %s", card)
		}
		var records int
		if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT count(*) FROM records
			WHERE kind IN ('download_preview','intent','subscription','resource')
			OR (kind IN ('workflow','decision') AND json_extract(payload,'$.media_key') IN ('movie:0','tv:0'))`).Scan(&records); err != nil || records != 0 || materialCalls.Load() != 0 || addCalls.Load() != 0 {
			t.Fatalf("provider-only selection crossed a native write boundary: records=%d material=%d add=%d error=%v", records, materialCalls.Load(), addCalls.Load(), err)
		}
		t.Log("selected provider TV searched with its own episode scope, returned only the matching release, and did not create a subscription when configured title recognition found no media")
		return
	}
	send("om_search", "订阅 测试剧集")
	first := next("共找到3条相关信息", "测试剧集", "select:3")
	request := callbackPrefix.FindString(first)
	orderMu.Lock()
	gotOrder := slices.Clone(order)
	orderMu.Unlock()
	if request == "" || !slices.Equal(gotOrder, []string{"douban", "bangumi", "anilist"}) {
		t.Fatalf("wrong source module order or callback: %v", gotOrder)
	}
	click(request + "select:3")
	next("测试剧集", "订阅")
	saved := awaitOriginalLarkProviderFixtureSubscription(t, a)
	if saved.Media.ID != m.ID || saved.Season != 1 || detailCalls.Load() != 1 {
		t.Fatalf("native subscription used wrong identity or source detail count: media=%d season=%d calls=%d", saved.Media.ID, saved.Season, detailCalls.Load())
	}
	origin, err := a.sourceSubscriptionOrigin(t.Context(), saved)
	if err != nil || origin == nil || origin.MediaProvider == nil || *origin.MediaProvider != (originalChatMediaProvider{Source: "anilist", ID: "777", TMDBID: m.ID}) {
		t.Fatalf("sealed subscription did not preserve selected source and native identity: %v", err)
	}
	var fields struct {
		AniListID int64 `json:"anilist_id"`
	}
	if err := json.Unmarshal(origin.MediaInfo, &fields); err != nil || fields.AniListID != sourceID {
		t.Fatalf("source MediaInfo lost selected alternate ID: %v %v", fields, err)
	}
	t.Log("three same-title source cards used Douban → Bangumi → AniList priority; only AniList detail 777 resolved to native TMDB 123 and persisted alongside the actual subscription")
}

func assertOriginalLarkUnscopedProviderDownload(t *testing.T, a *App, source, file, hash string, torrent []byte, materials, adds, fileReads *atomic.Int32, completed *atomic.Bool) {
	t.Helper()
	ctx := t.Context()
	const media = "tv:anilist:777"
	var intentID string
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT id FROM records WHERE kind='intent'`).Scan(&intentID); err != nil {
		t.Fatal("no intent from explicit whole download", err)
	}
	var intent Intent
	if err := a.Store.Get(ctx, "intent", intentID, &intent); err != nil || intent.State != "accepted" || !intent.ChatDownload || !intent.ChatWholeUnscoped || intent.Auto || intent.ChatMagnet ||
		mediaKey(intent.Media) != media || intent.Season != 0 || len(intent.Episodes) != 0 || len(intent.SelectedEpisodes) != 0 ||
		intent.Hash != hash || adds.Load() != 1 || materials.Load() != 1 || fileReads.Load() != 0 {
		t.Fatalf("whole Add did not preserve unscoped provider authority: state=%s media=%s season=%d episodes=%v material=%d add=%d files=%d err=%v",
			intent.State, mediaKey(intent.Media), intent.Season, intent.Episodes, materials.Load(), adds.Load(), fileReads.Load(), err)
	}
	frozen, err := a.loadDownloadContext(ctx, "intent", intentID, hash)
	if err != nil || frozen == nil || !frozen.ChatWholeUnscoped || frozen.Original == nil || frozen.ChatProvider == nil ||
		*frozen.ChatProvider != (originalChatMediaProvider{Source: "anilist", ID: "777"}) {
		t.Fatal("accepted whole Add did not bind the original provider and missing scope", err)
	}
	material, err := a.resolveDownloadMaterial(ctx, intent.MaterialRef, materialPurpose, false)
	if err != nil || !bytes.Equal(material, torrent) {
		t.Fatal("accepted whole Add did not retain the exact selected torrent", err)
	}
	var eventMedia string
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT json_extract(payload,'$.media_id') FROM domain_events WHERE event_id=?`,
		domainEventID("download.accepted", intentID)).Scan(&eventMedia); err != nil || eventMedia != media {
		t.Fatalf("whole Add event lost its exact provider identity: %q, %v", eventMedia, err)
	}

	// Even a plausible numbered file from the whole Add cannot create an
	// episode reservation or library slot without a verified provider scope.
	importWrite(t, filepath.Join(source, file), bytes.Repeat([]byte{1}, 1024))
	completed.Store(true)
	if err := a.pollDownloads(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Get(ctx, "intent", intentID, &intent); err != nil || intent.State != "downloaded" || intent.Error == "" || intent.ImportPlanID != "" {
		t.Fatalf("unscoped completion did not remain available for review: state=%s error=%q plan=%q read=%v", intent.State, intent.Error, intent.ImportPlanID, err)
	}
	var reservations, slots, assets int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM reservations), (SELECT count(*) FROM asset_slots), (SELECT count(*) FROM records WHERE kind='asset')`).Scan(&reservations, &slots, &assets); err != nil ||
		reservations != 0 || slots != 0 || assets != 0 || adds.Load() != 1 || materials.Load() != 1 {
		t.Fatalf("whole Add invented a scope or repeated a write: reservations=%d slots=%d assets=%d material=%d add=%d err=%v", reservations, slots, assets, materials.Load(), adds.Load(), err)
	}
	t.Log("explicit AniList-only whole Add accepted the S03E07 torrent with no guessed season or episode; completed material stayed unorganized for review")
}

func assertOriginalLarkProviderRSSQualityAcceptance(t *testing.T, a *App, owner Subscription, root Job, batchID, resourceID string) {
	t.Helper()
	ctx := t.Context()
	key := mediaKey(owner.Media)
	batch, err := a.readAcquisition(ctx, a.Store.DB, root.ID)
	if err != nil || !batch.Completed || batch.RSSBatchID != batchID || batch.Subscription.ID != owner.ID || batch.Subscription.Revision != owner.Revision ||
		batch.Request.originalSearchInfo == nil || mediaKey(batch.Request.originalSearchInfo.Media) != key || len(batch.Steps) != 1 {
		t.Fatalf("full-quality RSS acquisition lost its exact provider and parent scope: %v", err)
	}
	step := batch.Steps[0]
	var intent Intent
	if err = a.Store.Get(ctx, "intent", step.IntentID, &intent); err != nil || step.Phase != "accepted" || step.IntentSHA != acquisitionIntentSHA(intent) ||
		intent.SubscriptionAcquisition != root.ID || intent.ResourceID != resourceID || !slices.Equal(intent.Episodes, []int{1}) || intent.SelectedEpisodes != nil {
		t.Fatalf("full-quality RSS did not accept the exact whole E01 release: step=%s err=%v", step.Phase, err)
	}
	var accepted subscriptionQualityAccepted
	if err = a.readQualitySealed(ctx, a.Store.DB, "subscription_quality_accepted", intent.ID, &accepted); err != nil ||
		accepted.Disposition != "accepted" || accepted.Intent != intent.ID || accepted.Subscription != owner.ID || accepted.Revision != owner.Revision ||
		!accepted.Resource.ConfirmedFullCoverage || accepted.Resource.Priority != 100 {
		t.Fatalf("accepted whole release did not prove priority 100 and full coverage: priority=%d err=%v", accepted.Resource.Priority, err)
	}
	prepare, err := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind='subscription_completion_prepare' AND json_extract(payload,'$.id')=?`, owner.ID))
	if err != nil || prepare.ParentJobID != root.ID || prepare.WorkflowID != root.WorkflowID {
		t.Fatalf("retirement preparation was not enqueued by the exact RSS root: %v", err)
	}
	for deadline := time.Now().Add(8 * time.Second); ; {
		finish, err := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind='subscription_completion' AND json_extract(payload,'$._parent_job_id')=?`, prepare.ID))
		if err == nil && finish.State == "succeeded" {
			var input struct{ ID string }
			if err = json.Unmarshal(finish.Payload, &input); err != nil || input.ID == "" {
				t.Fatalf("invalid provider retirement job payload: %v", err)
			}
			var receipt subscriptionCompletionReceipt
			if err = a.readQualitySealed(ctx, a.Store.DB, "subscription_completion", input.ID, &receipt); err != nil || receipt.Stage != "complete" ||
				receipt.Identity.JobID != finish.ID || receipt.Identity.SubscriptionID != owner.ID || receipt.Identity.Revision != owner.Revision ||
				len(receipt.Input.Accepted) != 1 || receipt.Input.Accepted[0].ID != intent.ID || sourceSubscriptionMediaKey(receipt.Input.Source.Subscription) != key {
				t.Fatalf("provider retirement receipt lost the exact accepted download: stage=%s err=%v", receipt.Stage, err)
			}
			var live int
			var eventKey string
			if err = a.Store.DB.QueryRowContext(ctx, `SELECT
				(SELECT count(*) FROM records WHERE kind='subscription' AND id=?),
				(SELECT json_extract(payload,'$.media_id') FROM domain_events WHERE event_id=?)`, owner.ID,
				domainEventID("subscription.completed", receipt.Identity.ID)).Scan(&live, &eventKey); err != nil || live != 0 || eventKey != key {
				t.Fatalf("source retirement did not delete the exact subscription and commit its provider event: live=%d key=%q err=%v", live, eventKey, err)
			}
			return
		}
		if finish.State == "failed" || finish.State == "needs_review" || time.Now().After(deadline) {
			t.Fatalf("provider full-quality completion did not finish: state=%s code=%s err=%v", finish.State, finish.ErrorCode, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func originalLarkNativeProviderDownloadPeer(t *testing.T, source, file, hash string, torrent []byte, adds, fileReads *atomic.Int32, completed *atomic.Bool) string {
	t.Helper()
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
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
			if completed.Load() {
				task["progress"], task["completed"], task["state"] = 1, 1024, "stalledUP"
			}
			workflowJSON(w, []any{task})
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error("qB Add did not receive a multipart torrent", err)
				http.Error(w, "invalid Add", http.StatusBadRequest)
				return
			}
			defer r.MultipartForm.RemoveAll()
			if r.Method != http.MethodPost || r.FormValue("savepath") != source || r.FormValue("stopped") != "false" || r.FormValue("urls") != "" || r.Header.Get("Cookie") != "" {
				t.Error("qB Add did not preserve the whole torrent, owned save directory and credential boundary")
				http.Error(w, "invalid Add", http.StatusBadRequest)
				return
			}
			part, _, err := r.FormFile("torrents")
			if err != nil {
				t.Error("missing qB torrent attachment", err)
				http.Error(w, "invalid Add", http.StatusBadRequest)
				return
			}
			defer part.Close()
			raw, err := io.ReadAll(part)
			if err != nil || !bytes.Equal(raw, torrent) {
				t.Error("qB did not receive the exact selected tracker bytes", err)
				http.Error(w, "invalid Add", http.StatusBadRequest)
				return
			}
			adds.Add(1)
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/torrents/files":
			fileReads.Add(1)
			if !completed.Load() || r.URL.Query().Get("hash") != hash {
				t.Error("qB file listing requested before completion or with the wrong hash")
				http.Error(w, "files unavailable", http.StatusConflict)
				return
			}
			workflowJSON(w, []any{map[string]any{"index": 0, "name": file, "size": 1024, "progress": 1}})
		case "/api/v2/torrents/addTags":
			if err := r.ParseForm(); err != nil || !completed.Load() || r.Form.Get("hashes") != hash || r.Form.Get("tags") != "已整理" {
				t.Error("invalid post-import qB tag", err)
				http.Error(w, "invalid tag", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected qB request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(qb.Close)
	return qb.URL
}

func assertOriginalLarkNativeProviderDownload(t *testing.T, a *App, source, library, file, hash string, torrent []byte, materials, adds, fileReads *atomic.Int32, completed *atomic.Bool, owner *Subscription, rssResource string, existingSlot bool) Asset {
	t.Helper()
	ctx := t.Context()
	provider, key := "anilist", "tv:anilist:777"
	if owner != nil {
		provider, key = owner.Media.ProviderSource, mediaKey(owner.Media)
	}
	var intentID string
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT id FROM records WHERE kind='intent'`).Scan(&intentID); err != nil {
		t.Fatal("no durable provider intent after qB acceptance", err)
	}
	var intent Intent
	if err := a.Store.Get(ctx, "intent", intentID, &intent); err != nil || intent.State != "accepted" || intent.ChatDownload != (owner == nil) || intent.ChatMagnet ||
		intent.Media.ID != 0 || mediaKey(intent.Media) != key || intent.Season != 1 || !slices.Equal(intent.Episodes, []int{1}) ||
		intent.Hash != hash || adds.Load() != 1 || materials.Load() != 1 || fileReads.Load() != 0 {
		t.Fatalf("ordinary acceptance lost provider identity, scope or real effects: state=%s media=%s season=%d episodes=%v material=%d add=%d files=%d err=%v",
			intent.State, mediaKey(intent.Media), intent.Season, intent.Episodes, materials.Load(), adds.Load(), fileReads.Load(), err)
	}
	if owner != nil && (!intent.Auto || !intent.LatestResource || intent.ResourceID != rssResource || intent.SubscriptionID != owner.ID || intent.SubscriptionRevision != owner.Revision) {
		t.Fatal("accepted provider RSS intent was not bound to the actual subscription revision and enclosure")
	}
	var eventMedia string
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT json_extract(payload,'$.media_id') FROM domain_events WHERE event_id=?`,
		domainEventID("download.accepted", intentID)).Scan(&eventMedia); err != nil || eventMedia != key {
		t.Fatalf("download.accepted did not retain the namespaced identity: key=%q err=%v", eventMedia, err)
	}
	frozen, err := a.loadDownloadContext(ctx, "intent", intentID, hash)
	if err != nil || frozen == nil || frozen.Original == nil {
		t.Fatal("accepted context did not freeze the verified provider-only selection", err)
	}
	if owner == nil {
		if frozen.ChatProvider == nil || *frozen.ChatProvider != (originalChatMediaProvider{Source: provider, ID: "777"}) {
			t.Fatal("accepted context lost the direct chat authority")
		}
	} else if frozen.ProviderSubscription == nil || *frozen.ProviderSubscription != (subscriptionIdentity{ID: owner.ID, Revision: owner.Revision}) || frozen.ChatProvider != nil {
		t.Fatal("accepted context lost the ordinary subscription authority")
	}
	var rawMedia struct {
		Source  string          `json:"source"`
		MediaID json.RawMessage `json:"media_id"`
		TMDBID  int64           `json:"tmdb_id"`
	}
	if err := json.Unmarshal(frozen.Original.Media, &rawMedia); err != nil || rawMedia.Source != provider ||
		strings.Trim(string(rawMedia.MediaID), `"`) != "777" || rawMedia.TMDBID != 0 {
		t.Fatal("original download context mixed provider and TMDB identities", err)
	}
	material, err := a.resolveDownloadMaterial(ctx, intent.MaterialRef, materialPurpose, false)
	if err != nil || !bytes.Equal(material, torrent) {
		t.Fatal("accepted material did not preserve exact torrent bytes", err)
	}
	var assetCount int
	wantAssetCount := 0
	if existingSlot {
		wantAssetCount = 1
	}
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM asset_slots`).Scan(&assetCount); err != nil || assetCount != wantAssetCount {
		t.Fatal("qB acceptance changed library slots before verified local completion", assetCount, err)
	}

	input := filepath.Join(source, file)
	importWrite(t, input, bytes.Repeat([]byte{1}, 1024))
	completed.Store(true)
	if err := a.pollDownloads(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for {
		if err := a.Store.Get(ctx, "intent", intentID, &intent); err != nil {
			t.Fatal(err)
		}
		if intent.State == "imported" {
			break
		}
		if time.Now().After(deadline) {
			var jobState, jobCode string
			jobErr := a.Store.DB.QueryRowContext(ctx, `SELECT state,error_code FROM jobs WHERE kind='import' AND json_extract(payload,'$.id')=? ORDER BY created_at DESC LIMIT 1`, intent.ImportPlanID).Scan(&jobState, &jobCode)
			t.Fatalf("completed provider torrent was not organized: intent=%s review=%s job=%s/%s (%v)", intent.State, a.clean(intent.Error), jobState, jobCode, jobErr)
		}
		time.Sleep(25 * time.Millisecond)
	}
	var assetID, slotKey string
	var season, episode int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT asset_id,media_key,season,episode FROM asset_slots`).Scan(&assetID, &slotKey, &season, &episode); err != nil || slotKey != key || season != 1 || episode != 1 {
		t.Fatalf("organized slot lost source identity or verified episode: key=%q season=%d episode=%d err=%v", slotKey, season, episode, err)
	}
	var asset Asset
	if err := a.Store.Get(ctx, "asset", assetID, &asset); err != nil || mediaKey(asset.Media) != key || asset.Media.ID != 0 ||
		asset.SourcePath != input || asset.Season != 1 || !slices.Equal(asset.Episodes, []int{1}) {
		t.Fatal("organized asset did not preserve the accepted provider and local source", err)
	}
	original, sourceErr := os.Stat(input)
	linked, libraryErr := os.Stat(asset.Path)
	if sourceErr != nil || libraryErr != nil || !within(library, asset.Path) || !os.SameFile(original, linked) ||
		fileReads.Load() == 0 || adds.Load() != 1 || materials.Load() != 1 {
		t.Fatalf("completion did not verify and hardlink the exact file without another Add: source=%v library=%v reads=%d add=%d material=%d",
			sourceErr, libraryErr, fileReads.Load(), adds.Load(), materials.Load())
	}
	t.Logf("verified provider key %s across exact tracker bytes, qB Add, accepted event, and real S01E01 hardlink", key)
	return asset
}

func assertOriginalLarkProviderScrape(t *testing.T, a *App, asset Asset, title string) {
	t.Helper()
	ctx := t.Context()
	var scrapeJobID string
	for deadline := time.Now().Add(8 * time.Second); ; {
		job, err := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs
			WHERE kind='scrape' AND json_extract(payload,'$.id')=?`, asset.ID))
		if err == nil && job.State == "succeeded" {
			scrapeJobID = job.ID
			break
		}
		if job.State == "failed" || job.State == "needs_review" || job.State == "cancelled" || time.Now().After(deadline) {
			t.Fatalf("original provider scrape job did not finish: state=%s code=%s err=%v", job.State, job.ErrorCode, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	var saved Asset
	if err := a.Store.Get(ctx, "asset", asset.ID, &saved); err != nil || saved.ScrapeState != "scraped" || mediaKey(saved.Media) != "tv:anilist:777" {
		t.Fatalf("sidecar processing changed the original asset identity or failed to finish: %s %s %v", saved.ScrapeState, mediaKey(saved.Media), err)
	}
	var eventMedia string
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT json_extract(payload,'$.media_id') FROM domain_events WHERE event_id=?`,
		domainEventID("scrape.completed", asset.ID+":"+scrapeJobID)).Scan(&eventMedia); err != nil || eventMedia != "tv:anilist:777" {
		t.Fatalf("scrape completion lost the acquired identity: %q %v", eventMedia, err)
	}
	for _, item := range []struct {
		file, root, provider, id, season, episode string
	}{
		{filepath.Join(filepath.Dir(asset.Path), "season.nfo"), "season", "anilist", "777", "1", ""},
		{strings.TrimSuffix(asset.Path, filepath.Ext(asset.Path)) + ".nfo", "episodedetails", "bangumi", "778", "1", "1"},
	} {
		raw, err := os.ReadFile(item.file)
		if err != nil {
			t.Fatal("read native provider NFO", item.file, err)
		}
		var doc struct {
			XMLName      xml.Name
			Title        string  `xml:"title"`
			ShowTitle    string  `xml:"showtitle"`
			Season       string  `xml:"season"`
			SeasonNumber string  `xml:"seasonnumber"`
			Episode      string  `xml:"episode"`
			TMDB         *string `xml:"tmdbid"`
			IDs          []struct {
				Type    string `xml:"type,attr"`
				Default string `xml:"default,attr"`
				Value   string `xml:",chardata"`
			} `xml:"uniqueid"`
		}
		if err = xml.Unmarshal(raw, &doc); err != nil || doc.XMLName.Local != item.root || doc.Title != title || doc.TMDB != nil ||
			len(doc.IDs) != 1 || doc.IDs[0].Type != item.provider || doc.IDs[0].Value != item.id || doc.IDs[0].Default != "true" {
			t.Fatalf("unexpected native provider NFO %s: %s, %v", item.file, raw, err)
		}
		if item.episode == "" {
			if doc.SeasonNumber != item.season || doc.Season != "" || doc.Episode != "" {
				t.Fatalf("season NFO did not carry exactly its season number: %s", raw)
			}
		} else if doc.Season != item.season || doc.Episode != item.episode || doc.SeasonNumber != "" || doc.ShowTitle != title {
			t.Fatalf("episode NFO did not carry its recognized season, episode and show: %s", raw)
		}
	}
	t.Log("native sidecars: AniList:777 season from imported media, Bangumi:778 S01E01 from episode path recognition, both with the configured AniList scraper")
}

func awaitOriginalLarkProviderFixtureSubscription(t *testing.T, a *App) Subscription {
	t.Helper()
	var subID string
	deadline := time.Now().Add(4 * time.Second)
	for {
		err := a.Store.DB.QueryRowContext(t.Context(), `SELECT json_extract(payload,'$.subscription_id') FROM domain_events
			WHERE json_extract(payload,'$.event_type')='subscription.added'`).Scan(&subID)
		if err == nil && subID != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("missing durable SubscribeAdded: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	var saved Subscription
	if err := a.Store.Get(t.Context(), "subscription", subID, &saved); err != nil {
		t.Fatalf("missing native subscription for SubscribeAdded: %v", err)
	}
	return saved
}
