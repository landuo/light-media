package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOriginalLarkTransferRetryLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	a, source, target := importTestApp(t)
	m := importFixture(t, a, "movie")
	const movie = "测试影片"
	const ownedData = "actual fixture transfer content"

	cards := make(chan string, 12)
	lark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch path := strings.TrimPrefix(r.URL.Path, "/open-apis"); {
		case path == "/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-transfer-fixture"}`)
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
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_transfer","chat_id":"oc_transfer"}}`)
		default:
			t.Errorf("unexpected Lark request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(lark.Close)
	a.pluginOriginalLarkTestOrigin = lark.URL + "/open-apis"

	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/movie", "/3/search/multi":
			var rows = []map[string]any{}
			if strings.Contains(r.URL.Query().Get("query"), movie) {
				rows = append(rows, map[string]any{"id": m.ID, "media_type": "movie", "title": m.Title, "original_title": m.OriginalTitle, "release_date": "2024-01-01"})
			}
			workflowJSON(w, map[string]any{"page": 1, "total_pages": 1, "total_results": len(rows), "results": rows})
		case "/3/movie/123/images", "/3/movie/124/images":
			workflowJSON(w, map[string]any{"posters": []any{}, "backdrops": []any{}, "logos": []any{}})
		case "/3/movie/123", "/3/movie/124":
			id, title := m.ID, m.Title
			if strings.HasSuffix(r.URL.Path, "/124") {
				id, title = 124, "手动识别影片"
			}
			workflowJSON(w, map[string]any{"id": id, "title": title, "original_title": title,
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
	metadataPaths := []string{"/3/search/movie", "/3/search/multi", "/3/movie/123", "/3/movie/123/images", "/3/movie/124", "/3/movie/124/images"}
	installMetadataFixture(t, a, "owned-source-tmdb", metadataPaths, ownedLoopbackFixtureTransport(t, metadata).RoundTrip)
	a.config.DefaultRoot = "root"
	overwriteRule(a, source, target, "never", true)
	a.config.AppDomain = "https://owned.invalid"
	a.Start(t.Context())
	admin := setupTestAdmin(t, a)
	installMergeSiteSwitchPackage(t, a, admin, bundle, "LarkMessager")
	api := a.Handler()
	const plugin = "/api/plugins/LarkMessager"
	configured := callTest(t, api, "PUT", plugin, `{"enabled":true,"app_id":"owned-app","app_secret":"owned-secret","chat_id":"oc_transfer","verification_token":"owned-verification","switchs":["手动处理"]}`, admin, "")
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
			"operator": map[string]any{"open_id": "ou_owner"}, "context": map[string]any{"open_message_id": "om_transfer", "open_chat_id": "oc_transfer"},
			"action": map[string]any{"value": map[string]string{"callback_data": value}},
		})
	}
	send := func(value string) {
		t.Helper()
		post("im.message.receive_v1", map[string]any{
			"sender":  map[string]any{"sender_id": map[string]any{"open_id": "ou_owner"}},
			"message": map[string]any{"message_id": "om_redo", "chat_id": "oc_transfer", "message_type": "text", "content": string(mustCleanSeedJSON(map[string]string{"text": value}))},
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
	waitHistory := func(id, status string) transferHistoryItem {
		t.Helper()
		deadline := time.Now().Add(12 * time.Second)
		for {
			history, err := a.transferHistoryItem(t.Context(), id)
			if err == nil && history.Status == status && (history.JobState == "failed" || history.JobState == "succeeded") {
				return history
			}
			if time.Now().After(deadline) {
				t.Fatalf("history did not reach %s: status=%s job=%s reason=%s err=%v", status, history.Status, history.JobState, history.Error, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	assertLink := func(from, to string) {
		t.Helper()
		src, err := os.Stat(from)
		if err != nil {
			t.Fatal(err)
		}
		dst, err := os.Stat(to)
		if err != nil || !os.SameFile(src, dst) {
			t.Fatalf("native retry did not publish the actual hardlink: %v", err)
		}
	}

	input := filepath.Join(source, movie+".2024.mkv")
	importWrite(t, input, []byte(ownedData))
	plan, err := a.previewImport(t.Context(), ImportRequest{Manual: true, MediaType: "movie", TMDBID: m.ID, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
		t.Fatalf("original executable plan unavailable: %+v, %v", plan.Files, err)
	}
	blocker := plan.Files[0].Target
	const blockerData = "unowned destination conflict"
	importWrite(t, blocker, []byte(blockerData))
	job, err := a.executeImport(t.Context(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	card := next(movie, "入库失败", "原因：", "/redo", "transfer_retry_", "#/history")
	token := regexp.MustCompile(`transfer_retry_([0-9]+)`).FindStringSubmatch(card)
	if len(token) != 2 || strings.Contains(card, "transfer_ai_retry_") {
		t.Fatalf("invalid ordinary retry button: %s", card)
	}
	failed := waitHistory(plan.Files[0].ID, "failed")
	bytes, err := os.ReadFile(blocker)
	if err != nil || string(bytes) != blockerData || failed.JobID != job.ID || !failed.CanRetry {
		t.Fatalf("failure did not preserve the unrelated destination and retryable history: %s %v", failed.Error, err)
	}
	var reservations int
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM reservations WHERE intent_id=?`, plan.ID).Scan(&reservations); err != nil || reservations != 0 {
		t.Fatalf("prepublication refusal kept this plan's media reservation: count=%d err=%v", reservations, err)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	click(token[0])
	next("开始重新整理记录 #" + token[1])
	next("整理记录 #"+token[1]+" 已重新整理", "#/history")
	fixed := waitHistory(failed.ID, "success")
	if fixed.ID != failed.ID || fixed.JobID == failed.JobID {
		t.Fatal("successful retry must replace the execution of the same visible history")
	}
	assertLink(input, fixed.Target)
	click(token[0])
	next("开始重新整理记录 #" + token[1])
	next("重新整理失败")
	var jobs int
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM jobs WHERE kind='import'`).Scan(&jobs); err != nil || jobs != 2 {
		t.Fatalf("stale button enqueued a duplicate import: count=%d err=%v", jobs, err)
	}

	unmatched := filepath.Join(source, "没有对应影视.2024.mkv")
	importWrite(t, unmatched, []byte(ownedData))
	missed, err := a.manualOrganize(t.Context(), manualOrganizeRequest{Paths: []string{unmatched}, TargetPath: target, Mode: "hardlink", Scraping: importFlag(false)})
	if err != nil || missed.Failed != 1 || len(missed.Items) != 1 || missed.Items[0].JobID == "" {
		t.Fatalf("native recognition failure was not recorded: %+v, %v", missed, err)
	}
	card = next("未识别到媒体信息", "自动重试或手动识别整理", "[tmdbid]|[类型]", "transfer_retry_")
	token = regexp.MustCompile(`transfer_retry_([0-9]+)`).FindStringSubmatch(card)
	if len(token) != 2 {
		t.Fatalf("recognition failure did not expose a durable history ID: %s", card)
	}
	var blockedID string
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT json_extract(payload,'$.file_ids[0]') FROM jobs WHERE id=?`, missed.Items[0].JobID).Scan(&blockedID); err != nil {
		t.Fatal(err)
	}
	send(fmt.Sprintf("/redo %s tmdb:124|电影", token[1]))
	redo := waitHistory(blockedID, "success")
	if redo.Media.ID != 124 || redo.JobID == missed.Items[0].JobID {
		t.Fatalf("explicit redo did not use the requested verified identity: %+v", redo)
	}
	assertLink(unmatched, redo.Target)
	select {
	case unexpected := <-cards:
		t.Fatalf("source /redo success is silent: %s", unexpected)
	default:
	}
	t.Log("verified indexed Lark failure card, native conflict preserving unrelated target, actual hardlink retry and stable history, stale-button rejection, and explicit TMDB /redo of native recognition failure")
}
