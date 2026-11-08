package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/priorityrules"
)

func TestOriginalLarkManualSearchRejectedAndPausedQualityLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	a, source, _ := importTestApp(t)
	ordinary := importFixture(t, a, "movie")
	quality := ordinary
	quality.ID, quality.Title, quality.OriginalTitle = 124, "洗版影片", "Quality Film"
	importCache(t, a, "media:movie:124", quality)
	rejectedTorrent, _ := workflowTorrent("测试影片 (2024) 1080p.mkv")
	acceptedTorrent, hash := workflowTorrent("洗版影片 (2024) 1080p.mkv")
	var rejected, accepted atomic.Int32
	enteredReject, releaseReject := make(chan struct{}, 1), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(releaseReject) }) }
	defer release()

	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			query := r.URL.Query().Get("search")
			if strings.Contains(query, "洗版") || strings.Contains(query, "Quality") {
				_, _ = io.WriteString(w, strings.ReplaceAll(workflowHTML("洗版影片.2024.1080p.WEB-DL.x264-TEST", "", false), "id=101", "id=102"))
			} else {
				_, _ = io.WriteString(w, workflowHTML("测试影片.2024.1080p.WEB-DL.x264-TEST", "", false))
			}
		case "/download.php":
			switch r.URL.Query().Get("id") {
			case "101":
				_, _ = w.Write(rejectedTorrent)
			case "102":
				_, _ = w.Write(acceptedTorrent)
			default:
				http.NotFound(w, r)
			}
		default:
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
			if accepted.Load() > 0 {
				task := workflowTask(hash, 0)
				task["save_path"] = source
				workflowJSON(w, []any{task})
			} else {
				workflowJSON(w, []any{})
			}
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(4 << 20); err != nil || r.FormValue("savepath") != source {
				t.Error("unexpected Add save path", err)
				http.Error(w, "unexpected Add", 400)
				return
			}
			part, _, err := r.FormFile("torrents")
			if err != nil {
				t.Error(err)
				http.Error(w, "missing torrent", 400)
				return
			}
			defer part.Close()
			raw, err := io.ReadAll(part)
			switch {
			case err == nil && bytes.Equal(raw, rejectedTorrent):
				if rejected.Add(1) != 1 {
					t.Error("definite failure was resubmitted")
					http.Error(w, "repeated Add", 400)
					return
				}
				enteredReject <- struct{}{}
				select {
				case <-releaseReject:
				case <-r.Context().Done():
					return
				}
				_, _ = io.WriteString(w, "Fails.")
			case err == nil && bytes.Equal(raw, acceptedTorrent):
				accepted.Add(1)
				_, _ = io.WriteString(w, "Ok.")
			default:
				t.Error("Add did not contain an admitted torrent")
				http.Error(w, "unexpected material", 400)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(qb.Close)
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var item catalog.Media
		switch r.URL.Path {
		case "/3/movie/123":
			item = ordinary
		case "/3/movie/124":
			item = quality
		case "/3/search/movie":
			item = ordinary
			if query := r.URL.Query().Get("query"); strings.Contains(query, "洗版") || strings.Contains(query, "Quality") {
				item = quality
			}
			workflowJSON(w, map[string]any{"page": 1, "total_pages": 1, "total_results": 1, "results": []map[string]any{
				{"id": item.ID, "title": item.Title, "original_title": item.OriginalTitle, "release_date": "2024-01-01"},
			}})
			return
		default:
			http.NotFound(w, r)
			return
		}
		workflowJSON(w, map[string]any{"id": item.ID, "title": item.Title, "original_title": item.OriginalTitle, "release_date": "2024-01-01",
			"original_language": item.OriginalLanguage, "origin_country": item.OriginCountry, "production_countries": item.ProductionCountries, "genres": item.Genres,
			"poster_path": "/owned.jpg", "overview": item.Overview, "external_ids": map[string]string{"imdb_id": "tt1234567"}})
	}))
	t.Cleanup(metadata.Close)
	transport := ownedLoopbackFixtureTransport(t, metadata)
	installMetadataFixture(t, a, "owned-source-manual-tmdb", []string{"/3/movie/123", "/3/movie/124", "/3/search/movie"}, transport.RoundTrip)
	workflowConfigure(t, a, tracker.URL, qb.URL)
	a.config.Downloaders[0].DownloadPath, a.config.Downloaders[0].SharedPaths = source, true
	a.config.PriorityRules = &priorityrules.Configuration{
		SubscribeGroups: []string{"quality"}, Groups: []priorityrules.Group{{Name: "quality", RuleString: priorityText("WEBDL & 1080P")}}, Mode: priorityrules.Rust,
	}
	a.settingsMu.Lock()
	err := a.saveSettings(t.Context(), a.Settings())
	a.settingsMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	save := func(media catalog.Media, best *bool) Subscription {
		t.Helper()
		sub, err := a.saveSubscription(t.Context(), "", SubscriptionRequest{
			MediaType: "movie", TMDBID: media.ID, DownloaderID: "fixture-qb", DownloadPath: source,
			RootID: "root", Schedule: Schedule{Mode: "manual"}, BestVersion: best,
		})
		if err != nil {
			t.Fatal(err)
		}
		sub.CreatedAt = time.Now().Add(-2 * time.Minute).Format(time.RFC3339Nano)
		if err := a.Store.Put(t.Context(), "subscription", sub.ID, sub); err != nil {
			t.Fatal(err)
		}
		return sub
	}
	active, selected := save(ordinary, nil), save(quality, &yes)
	paused, err := a.saveSubscriptionStatus(t.Context(), selected.ID, "paused", selected.Revision)
	if err != nil {
		t.Fatal(err)
	}

	type card struct{ method, content string }
	cards := make(chan card, 12)
	lark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/open-apis")
		switch {
		case path == "/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-manual-fixture"}`)
		case strings.Contains(path, "/reactions"):
			_, _ = io.WriteString(w, `{"code":0,"data":{"reaction_id":"rx_fixture"}}`)
		case path == "/im/v1/messages" || path == "/im/v1/messages/om_manual":
			var in struct{ Content string }
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			select {
			case cards <- card{r.Method, in.Content}:
			default:
				t.Error("unexpected extra Lark card")
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_manual","chat_id":"oc_manual"}}`)
		default:
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
	result := callTest(t, api, "PUT", plugin, `{"enabled":true,"app_id":"owned-app","app_secret":"owned-secret","chat_id":"oc_manual","verification_token":"owned-verification","switchs":["插件","订阅"]}`, admin, "")
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
			"message": map[string]any{"message_id": fmt.Sprintf("om_manual_%d", index), "chat_id": "oc_manual", "message_type": "text", "content": string(mustCleanSeedJSON(map[string]string{"text": text}))},
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
	first := next("POST", "暂停")
	token := regexp.MustCompile(`subscribes:[0-9a-f]{12}:`).FindString(first)
	if token == "" {
		t.Fatal("subscription callback ID is missing")
	}
	post("card.action.trigger", map[string]any{
		"operator": map[string]any{"open_id": "ou_owner"}, "context": map[string]any{"open_message_id": "om_manual", "open_chat_id": "oc_manual"},
		"action": map[string]any{"value": map[string]string{"callback_data": token + "search"}},
	})
	next("PATCH", "当前操作：搜索订阅")
	send(2, "1 2")
	next("POST", "开始搜索订阅【测试影片】")
	select {
	case <-enteredReject:
	case <-time.After(12 * time.Second):
		t.Fatal("ordinary manual search did not reach the real Add")
	}
	select {
	case got := <-cards:
		t.Fatalf("next search started or replied before the first Add settled: %s", got.content)
	case <-time.After(250 * time.Millisecond):
	}
	release()
	next("POST", "开始搜索订阅【洗版影片】")
	next("POST", "已完成洗版")
	next("POST", "已完成 2 个订阅搜索")
	if final := next("POST", "测试影片"); strings.Contains(final, "洗版影片") {
		t.Fatal("completed quality subscription remained on the live card")
	}

	var live Subscription
	if err := a.Store.Get(t.Context(), "subscription", active.ID, &live); err != nil || live.State != "active" {
		t.Fatalf("definite Add rejection removed the ordinary subscription: %s %v", live.State, err)
	}
	var reservationCount int
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM reservations WHERE media_key=?`, mediaKey(ordinary)).Scan(&reservationCount); err != nil || reservationCount != 0 {
		t.Fatalf("definitively rejected Add retained a reservation: count=%d err=%v", reservationCount, err)
	}
	var rejectionCount, falseAccepts int
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT
		(SELECT COUNT(*) FROM records WHERE kind='subscription_download_rejection'),
		(SELECT COUNT(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='download.accepted'
			AND json_extract(payload,'$.subscription_id')=?)`, active.ID).Scan(&rejectionCount, &falseAccepts); err != nil || rejectionCount != 1 || falseAccepts != 0 {
		t.Fatalf("explicit Add failure did not produce exactly one rejection without false acceptance: rejections=%d accepted=%d err=%v", rejectionCount, falseAccepts, err)
	}
	var failureKey string
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT id FROM records WHERE kind='subscription_download_failure'`).Scan(&failureKey); err != nil {
		t.Fatal(err)
	}
	var failure subscriptionDownloadFailure
	if err := a.readQualitySealed(t.Context(), a.Store.DB, "subscription_download_failure", failureKey, &failure); err != nil {
		t.Fatal(err)
	}
	if left := time.Until(time.Unix(failure.RetryAt, 0)); failure.Count != 1 || left < 59*time.Minute || left > time.Hour {
		t.Fatalf("ordinary explicit daemon failure lacked the source transient cooldown: count=%d left=%s", failure.Count, left)
	}
	if err := a.Store.Get(t.Context(), "subscription", paused.ID, &live); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("accepted priority-100 paused acquisition did not retire the subscription: %v", err)
	}
	var key string
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT id FROM records WHERE kind='source_subscribe_complete'`).Scan(&key); err != nil {
		t.Fatal(err)
	}
	var receipt subscriptionCompletionReceipt
	if err := a.readQualitySealed(t.Context(), a.Store.DB, "subscription_completion", key, &receipt); err != nil {
		t.Fatal(err)
	}
	var origin Subscription
	if err := json.Unmarshal(receipt.Input.Source.Subscription, &origin); err != nil || origin.State != "paused" || origin.Revision != paused.Revision ||
		!origin.BestVersion || origin.CurrentPriority == nil || *origin.CurrentPriority != 100 || receipt.Stage != "complete" ||
		receipt.Input.Ordinary != nil || len(receipt.Input.Accepted) != 1 || rejected.Load() != 1 || accepted.Load() != 1 {
		t.Fatalf("incorrect manual settlement: state=%s best=%t receipt=%s facts=%d rejected=%d accepted=%d err=%v", origin.State, origin.BestVersion, receipt.Stage, len(receipt.Input.Accepted), rejected.Load(), accepted.Load(), err)
	}
	t.Log("first active ID waited for a definitive daemon rejection, released its reservation and stored a one-hour cooldown; second paused quality ID committed a priority-100 Add and completion before the shared search reply")
}
