package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

func TestOriginalLarkDownloadFailureLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	a, source, _ := importTestApp(t)
	media := importFixture(t, a, "movie")
	const owner = "ou_failure_owner"
	type receivedCard struct{ Content, ReceiveID string }
	cards := make(chan receivedCard, 12)
	lark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch path := strings.TrimPrefix(r.URL.Path, "/open-apis"); {
		case path == "/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-failure-fixture"}`)
		case strings.Contains(path, "/reactions"):
			_, _ = io.WriteString(w, `{"code":0,"data":{"reaction_id":"rx_failure"}}`)
		case path == "/im/v1/messages" && r.Method == http.MethodPost:
			var card struct {
				Content   string `json:"content"`
				ReceiveID string `json:"receive_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&card); err != nil || r.URL.Query().Get("receive_id_type") != "open_id" {
				t.Error("invalid Lark request", err)
			}
			select {
			case cards <- receivedCard(card):
			default:
				t.Error("unexpected extra Lark notice")
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_failure","chat_id":"oc_failure"}}`)
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
			workflowJSON(w, map[string]any{"page": 1, "total_pages": 1, "total_results": 1, "results": []map[string]any{
				{"id": media.ID, "media_type": "movie", "title": media.Title, "original_title": media.OriginalTitle, "release_date": "2024-01-01"},
			}})
		case "/3/movie/123":
			workflowJSON(w, map[string]any{"id": media.ID, "title": media.Title, "original_title": media.OriginalTitle,
				"release_date": "2024-01-01", "original_language": media.OriginalLanguage, "origin_country": media.OriginCountry,
				"production_countries": media.ProductionCountries, "genres": media.Genres, "overview": media.Overview,
				"external_ids": map[string]any{"imdb_id": "tt1234567"},
			})
		default:
			t.Errorf("unexpected metadata request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(metadata.Close)
	installMetadataFixture(t, a, "owned-source-tmdb", []string{"/3/search/movie", "/3/search/multi", "/3/movie/123"}, ownedLoopbackFixtureTransport(t, metadata).RoundTrip)

	const release = "测试影片.2024.1080p.WEB-DL.x264-"
	const rowStart = "<tr><td>类别"
	tags := []string{"UNAVAILABLE", "REFUSED", "UNCERTAIN", "MUSTNOTTRY"}
	var listing strings.Builder
	for i, tag := range tags {
		row := workflowHTML(release+tag, "", false)
		row = strings.ReplaceAll(row, "id=101", fmt.Sprintf("id=%d", 101+i))
		row = strings.Replace(row, ">12</a>", fmt.Sprintf(">%d</a>", 400-i*100), 1)
		if i != 0 {
			row = row[strings.Index(row, rowStart):]
		}
		listing.WriteString(strings.TrimSuffix(row, "</table>"))
	}
	listing.WriteString("</table>")
	// The actual file names are independently recognized during preview. Use
	// technical release tags so both materials resolve to the selected film.
	refusedTorrent, refusedHash := workflowTorrent("测试影片 (2024) 2160p.mkv")
	unknownTorrent, unknownHash := workflowTorrent("测试影片 (2024) 1080p.mkv")
	var material [3]atomic.Int32
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			_, _ = io.WriteString(w, listing.String())
		case "/download.php":
			switch r.URL.Query().Get("id") {
			case "101":
				material[0].Add(1)
				http.NotFound(w, r)
			case "102":
				material[1].Add(1)
				_, _ = w.Write(refusedTorrent)
			case "103":
				material[2].Add(1)
				_, _ = w.Write(unknownTorrent)
			default:
				t.Error("ambiguous Add did not stop the next candidate")
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(tracker.Close)
	var adds atomic.Int32
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v5.0.0")
		case "/api/v2/app/preferences":
			workflowJSON(w, map[string]string{"save_path": source})
		case "/api/v2/torrents/info":
			workflowJSON(w, []any{})
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(4 << 20); err != nil || r.FormValue("savepath") != source {
				t.Error("invalid Add path or multipart body", err)
				http.Error(w, "unexpected Add", http.StatusBadRequest)
				return
			}
			defer r.MultipartForm.RemoveAll()
			part, _, err := r.FormFile("torrents")
			if err != nil {
				t.Error("missing Add material", err)
				http.Error(w, "missing torrent", http.StatusBadRequest)
				return
			}
			defer part.Close()
			raw, err := io.ReadAll(part)
			switch seq := adds.Add(1); {
			case seq == 1 && err == nil && bytes.Equal(raw, refusedTorrent):
				_, _ = io.WriteString(w, "Fails.")
			case seq == 2 && err == nil && bytes.Equal(raw, unknownTorrent):
				_, _ = io.WriteString(w, "unexpected reply")
			default:
				t.Error("downloader received unexpected or repeated material", seq, err)
				http.Error(w, "unexpected torrent", http.StatusBadRequest)
			}
		default:
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
	configured := callTest(t, api, "PUT", plugin, `{"enabled":true,"app_id":"owned-app","app_secret":"owned-secret","chat_id":"oc_failure","verification_token":"owned-verification","switchs":["手动处理","资源下载","插件"]}`, admin, "")
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
			"operator": map[string]any{"open_id": owner}, "context": map[string]any{"open_message_id": "om_failure", "open_chat_id": "oc_failure"},
			"action": map[string]any{"value": map[string]string{"callback_data": value}},
		})
	}
	next := func(fragments ...string) string {
		t.Helper()
		select {
		case card := <-cards:
			if card.ReceiveID != owner {
				t.Fatal("notice did not reach the initiating user")
			}
			for _, fragment := range fragments {
				if !strings.Contains(card.Content, fragment) {
					t.Fatalf("card missing %q: %s", fragment, card.Content)
				}
			}
			return card.Content
		case <-time.After(12 * time.Second):
			t.Fatalf("no card for %v", fragments)
			return ""
		}
	}
	post("im.message.receive_v1", map[string]any{
		"sender":  map[string]any{"sender_id": map[string]any{"open_id": owner}},
		"message": map[string]any{"message_id": "om_search", "chat_id": "oc_failure", "message_type": "text", "content": `{"text":"搜索 测试影片"}`},
	})
	request := regexp.MustCompile(`media:[0-9a-f]{12}:`).FindString(next("共找到1条相关信息"))
	if request == "" {
		t.Fatal("no media selection callback")
	}
	click(request + "select:1")
	next("开始搜索")
	resources := next("共找到4条相关资源", request+"download:0")
	last := -1
	for _, tag := range tags {
		pos := strings.Index(resources, release+tag)
		if pos <= last {
			t.Fatalf("resource %s did not follow descending source seeder priority: %s", tag, resources)
		}
		last = pos
	}
	click(request + "download:0")
	next(release+"UNAVAILABLE", "种子下载失败！", "站点：fixture")
	next("添加下载任务失败：", "种子名称："+release+"REFUSED", "站点：fixture")
	if card := next("所选资源的下载结果待确认"); strings.Contains(card, "添加下载任务失败") {
		t.Fatal("unknown Add was presented as a definite refusal")
	}
	if adds.Load() != 2 || material[0].Load() != 1 || material[1].Load() != 1 || material[2].Load() != 1 {
		t.Fatal("unexpected Add or material count", adds.Load(), material[0].Load(), material[1].Load(), material[2].Load())
	}
	assertOutcome := func(hash, intentState, code string, wantReservations int) {
		t.Helper()
		var intent, state, actualCode string
		var reservations, attempts int
		err := a.Store.DB.QueryRowContext(t.Context(), `SELECT json_extract(r.payload,'$.state'), j.state, j.error_code, j.attempt,
			(SELECT COUNT(*) FROM reservations WHERE intent_id=r.id) FROM records r JOIN jobs j ON json_extract(j.payload,'$.id')=r.id
			WHERE r.kind='intent' AND j.kind='download' AND json_extract(r.payload,'$.hash')=?`, hash).Scan(&intent, &state, &actualCode, &attempts, &reservations)
		if err != nil || intent != intentState || state != "failed" || actualCode != code || attempts != 1 || reservations != wantReservations {
			t.Fatalf("unexpected durable attempt: intent=%s state=%s code=%s attempts=%d reservations=%d err=%v", intent, state, actualCode, attempts, reservations, err)
		}
	}
	assertOutcome(refusedHash, "failed", originalChatAddRejectedCode, 0)
	assertOutcome(unknownHash, "reconcile_pending", originalChatAddUnknownCode, 1)
	var accepted int
	if err := a.Store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='download.accepted'`).Scan(&accepted); err != nil || accepted != 0 {
		t.Fatal("failed or ambiguous Add produced an accepted fact", accepted, err)
	}
	t.Log("tracker material failure and definite Add refusal sent distinct cards; unknown Add kept its reservation and stopped the remaining candidate")
}
