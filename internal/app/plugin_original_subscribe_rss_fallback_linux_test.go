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
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOriginalLarkRSSBatchFallbackLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" || os.Getenv("OWNED_TRANSFER_EVENTS_BINARY") == "" {
		t.Skip("requires indexed Linux amd64 LarkMessager and SDK event observer")
	}
	t.Setenv("MP_SERVER_HOST", "")
	t.Setenv("SUBSCRIBE_MODE", "rss")
	a, source, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	firstTorrent, rejectedHash := workflowTorrent("测试影片 (2024) 2160p.mkv")
	secondTorrent, acceptedHash := workflowTorrent("测试影片 (2024) 1080p.mkv")
	var firstAdds, secondAdds, feeds, firstMaterials, secondMaterials atomic.Int32
	const foreign = "https://foreign.invalid/details.php?id=101"
	const privateMarker = "owned-private-detail"
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rss.php":
			feeds.Add(1)
			if r.Header.Get("Cookie") != "" {
				t.Error("RSS feed inherited site login")
			}
			w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
			_, _ = fmt.Fprintf(w, `<rss version="2.0"><channel>
<item><title>测试影片.2024.2160p.WEB-DL.x264-FIRST</title><pubDate>Sat, 31 Oct 2026 00:01:00 GMT</pubDate><link>%s</link><enclosure url="download.php?id=101&amp;passkey=owned-private-enclosure" length="1024"/></item>
<item><title>测试影片.2024.1080p.WEB-DL.x264-SECOND</title><pubDate>Sat, 31 Oct 2026 00:00:00 GMT</pubDate><link>details.php?id=102&amp;hit=1&amp;passkey=%s#section</link><enclosure url="download.php?id=102&amp;passkey=owned-private-enclosure" length="1024"/></item>
</channel></rss>`, foreign, privateMarker)
		case "/download.php":
			if r.URL.Query().Get("passkey") != "owned-private-enclosure" || r.Header.Get("Cookie") != "fixture-cookie" {
				t.Error("RSS material lost its bound tracker authority")
			}
			switch r.URL.Query().Get("id") {
			case "101":
				firstMaterials.Add(1)
				_, _ = w.Write(firstTorrent)
			case "102":
				secondMaterials.Add(1)
				_, _ = w.Write(secondTorrent)
			default:
				http.NotFound(w, r)
			}
		default:
			t.Error("RSS fallback made an unexpected tracker request", r.URL.Path)
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
			if secondAdds.Load() > 0 {
				task := workflowTask(acceptedHash, 0)
				task["save_path"] = source
				workflowJSON(w, []any{task})
			} else {
				workflowJSON(w, []any{})
			}
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(4 << 20); err != nil || r.FormValue("savepath") != source {
				t.Error("qB Add did not contain the expected local path")
				http.Error(w, "unexpected Add", 400)
				return
			}
			file, _, err := r.FormFile("torrents")
			if err != nil {
				t.Error(err)
				http.Error(w, "missing torrent", 400)
				return
			}
			defer file.Close()
			raw, err := io.ReadAll(file)
			switch {
			case err == nil && bytes.Equal(raw, firstTorrent):
				if firstAdds.Add(1) != 1 || secondAdds.Load() != 0 {
					t.Error("first candidate did not submit exactly once before fallback")
				}
				_, _ = io.WriteString(w, "Fails.")
			case err == nil && bytes.Equal(raw, secondTorrent):
				if secondAdds.Add(1) != 1 || firstAdds.Load() != 1 {
					t.Error("second candidate did not follow exactly one definite refusal")
				}
				_, _ = io.WriteString(w, "Ok.")
			default:
				t.Error("Add used unexpected material")
				http.Error(w, "unexpected torrent", 400)
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
	a.config.Sites[0].RSS = tracker.URL + "/rss.php?passkey=owned-rss-feed"
	a.config.Downloaders[0].DownloadPath, a.config.Downloaders[0].SharedPaths = source, true
	a.config.APIToken = "owned-rss-fallback-events"
	if err := a.saveSettings(t.Context(), a.Settings()); err != nil {
		t.Fatal(err)
	}
	sub, err := a.saveSubscription(t.Context(), "", SubscriptionRequest{
		MediaType: "movie", TMDBID: m.ID, DownloaderID: "fixture-qb", DownloadPath: source, RootID: "root", Schedule: Schedule{Mode: "manual"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sub.SourceState = "R"
	sub.CreatedAt = time.Now().Add(-2 * time.Minute).Format(time.RFC3339Nano)
	if err := a.Store.Put(t.Context(), "subscription", sub.ID, sub); err != nil {
		t.Fatal(err)
	}

	type card struct{ method, content string }
	cards := make(chan card, 12)
	type torrentFields struct {
		Title     string
		PageURL   string `json:"page_url"`
		Enclosure string
	}
	postAdd := make(chan torrentFields, 1)
	releaseNotice := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(releaseNotice) }) }
	defer release()
	assertDetail := func(raw []byte, want bool) {
		t.Helper()
		var fields torrentFields
		if err := json.Unmarshal(raw, &fields); err != nil || fields.Enclosure != "" ||
			bytes.Contains(raw, []byte(privateMarker)) || bytes.Contains(raw, []byte("owned-private-enclosure")) || bytes.Contains(raw, []byte("fixture-cookie")) {
			t.Fatalf("trusted RSS context exposed raw material or was invalid: %v", err)
		}
		if !want {
			if fields.PageURL != "" {
				t.Fatal("foreign RSS details page reached the trusted context")
			}
			return
		}
		parsed, err := url.Parse(fields.PageURL)
		if err != nil || parsed.String() != tracker.URL+"/details.php?hit=1&id=102" {
			t.Fatalf("download context did not retain the bound RSS detail identity: %v", err)
		}
	}
	lark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/open-apis")
		switch {
		case path == "/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-rss-fallback-fixture"}`)
		case strings.Contains(path, "/reactions"):
			_, _ = io.WriteString(w, `{"code":0,"data":{"reaction_id":"rx_fixture"}}`)
		case path == "/im/v1/messages" || path == "/im/v1/messages/om_rss_fallback":
			var in struct{ Content string }
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			select {
			case cards <- card{r.Method, in.Content}:
			default:
				t.Error("unexpected extra Lark card")
			}
			if strings.Contains(in.Content, privateMarker) || strings.Contains(in.Content, "owned-private-enclosure") {
				t.Error("Lark exposed private tracker data")
			}
			if strings.Contains(in.Content, "已完成订阅") {
				select {
				case <-releaseNotice:
				case <-r.Context().Done():
					return
				}
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_rss_fallback","chat_id":"oc_rss_fallback"}}`)
		case path == "/owned-post-add" && r.Method == http.MethodPost:
			var call struct{ Kwargs map[string]json.RawMessage }
			var context struct {
				Torrent json.RawMessage `json:"torrent_info"`
			}
			var fields torrentFields
			if err := json.NewDecoder(r.Body).Decode(&call); err != nil || json.Unmarshal(call.Kwargs["context"], &context) != nil || json.Unmarshal(context.Torrent, &fields) != nil {
				t.Error("download_added did not receive an RSS torrent context")
				http.Error(w, "unexpected callback", 400)
				return
			}
			if bytes.Contains(context.Torrent, []byte(privateMarker)) || bytes.Contains(context.Torrent, []byte("owned-private-enclosure")) {
				t.Error("download_added exposed private RSS data")
			}
			select {
			case postAdd <- fields:
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
	result := callTest(t, api, "PUT", plugin, `{"enabled":true,"app_id":"owned-app","app_secret":"owned-secret","chat_id":"oc_rss_fallback","verification_token":"owned-verification","switchs":["插件","订阅"]}`, admin, "")
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
			"message": map[string]any{"message_id": fmt.Sprintf("om_rss_fallback_%d", index), "chat_id": "oc_rss_fallback", "message_type": "text", "content": string(mustCleanSeedJSON(map[string]string{"text": text}))},
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
	first := next("POST", "订阅中")
	token := regexp.MustCompile(`subscribes:[0-9a-f]{12}:`).FindString(first)
	if token == "" {
		t.Fatal("subscription callback ID is missing")
	}
	post("card.action.trigger", map[string]any{
		"operator": map[string]any{"open_id": "ou_owner"}, "context": map[string]any{"open_message_id": "om_rss_fallback", "open_chat_id": "oc_rss_fallback"},
		"action": map[string]any{"value": map[string]string{"callback_data": token + "refresh"}},
	})
	next("POST", "开始刷新订阅")
	next("POST", "已完成订阅")
	select {
	case got := <-cards:
		t.Fatalf("RSS refresh replied before the completion notification returned: %s", got.content)
	case <-time.After(250 * time.Millisecond):
	}
	release()
	next("POST", "订阅刷新执行完成")
	next("PATCH", "当前没有任何订阅")
	select {
	case fields := <-postAdd:
		assertDetail(mustCleanSeedJSON(fields), true)
	case <-time.After(12 * time.Second):
		t.Fatal("download_added did not run for the accepted RSS candidate")
	}
	if firstAdds.Load() != 1 || secondAdds.Load() != 1 || firstMaterials.Load() != 1 || secondMaterials.Load() != 1 || feeds.Load() != 1 {
		t.Fatalf("RSS did not use one fetched batch and two exact candidate attempts: feeds=%d first=%d/%d second=%d/%d",
			feeds.Load(), firstMaterials.Load(), firstAdds.Load(), secondMaterials.Load(), secondAdds.Load())
	}
	var jobs, roots, cooled, live int
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT
		(SELECT COUNT(*) FROM jobs WHERE kind='download' AND json_extract(payload,'$._parent_job_id') IN
			(SELECT id FROM jobs WHERE kind='subscription' AND json_extract(payload,'$.latest_batch') IS NOT NULL)),
		(SELECT COUNT(DISTINCT json_extract(payload,'$._parent_job_id')) FROM jobs WHERE kind='download'),
		(SELECT COUNT(*) FROM records WHERE kind='subscription_download_failure'),
		(SELECT COUNT(*) FROM records WHERE kind='subscription' AND id=?)`, sub.ID).Scan(&jobs, &roots, &cooled, &live); err != nil || jobs != 2 || roots != 1 || cooled != 1 || live != 0 {
		t.Fatalf("RSS outcomes did not belong to one latest batch: jobs=%d roots=%d cooled=%d live=%d err=%v", jobs, roots, cooled, live, err)
	}
	for wantHash, wantState := range map[string]string{rejectedHash: "failed", acceptedHash: "accepted"} {
		var got string
		if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT json_extract(payload,'$.state') FROM records WHERE kind='intent' AND json_extract(payload,'$.hash')=?`, wantHash).Scan(&got); err != nil || got != wantState && !(wantState == "accepted" && got == "downloading") {
			t.Fatalf("unexpected native intent state: got=%s want=%s err=%v", got, wantState, err)
		}
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+observer+"/observations", "")
		var events []struct {
			Type string
			Data struct{ Context originalDownloadContext }
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &events) != nil {
			t.Fatalf("could not read SDK event observations: %d", response.Code)
		}
		var first, second, accepted bool
		for _, event := range events {
			var fields torrentFields
			_ = json.Unmarshal(event.Data.Context.Torrent, &fields)
			switch {
			case event.Type == "resource.download" && strings.HasSuffix(fields.Title, "-FIRST"):
				assertDetail(event.Data.Context.Torrent, false)
				first = true
			case event.Type == "resource.download" && strings.HasSuffix(fields.Title, "-SECOND"):
				assertDetail(event.Data.Context.Torrent, true)
				second = true
			case event.Type == "download.added":
				assertDetail(event.Data.Context.Torrent, true)
				accepted = true
			}
		}
		if first && second && accepted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("missing RSS SDK event coverage: first=%t second=%t accepted=%t", first, second, accepted)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Log("one RSS refresh tried the next candidate after definite rejection, finished before replying, and preserved only the trusted RSS detail page in download hooks")
}
