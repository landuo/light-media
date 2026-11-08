package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
)

func TestOriginalLarkDownloadCommandsLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	a, source, target := importTestApp(t)
	identity := importFixture(t, a, "movie")
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/movie/123":
			workflowJSON(w, map[string]any{"id": identity.ID, "title": identity.Title, "original_title": identity.OriginalTitle,
				"release_date": "2024-01-01", "original_language": identity.OriginalLanguage})
		case "/3/movie/123/images":
			workflowJSON(w, map[string]any{"posters": []any{}, "backdrops": []any{}, "logos": []any{}})
		default:
			t.Errorf("unexpected metadata request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(metadata.Close)
	installMetadataFixture(t, a, "owned-command-tmdb", []string{"/3/movie/123", "/3/movie/123/images"}, ownedLoopbackFixtureTransport(t, metadata).RoundTrip)
	const file = "Owned.Movie.2024.mkv"
	input := filepath.Join(source, file)
	importWrite(t, input, make([]byte, 1024))
	hash, unrelated := strings.Repeat("a", 40), strings.Repeat("b", 40)
	var complete atomic.Bool
	var downloadsRead, filesRead atomic.Int32
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v5.0.0")
		case "/api/v2/app/preferences":
			workflowJSON(w, map[string]any{"save_path": source})
		case "/api/v2/torrents/info":
			task := workflowTask(hash, .625)
			task["name"], task["save_path"], task["state"], task["tags"] = file, source, "downloading", "light-media"
			task["content_path"] = input
			if complete.Load() {
				task["progress"], task["state"], task["completed"] = 1, "uploading", 1024
			}
			old := workflowTask(unrelated, .5)
			old["name"], old["state"], old["tags"] = "Old.MoviePilot.mkv", "downloading", "MOVIEPILOT"
			if r.URL.Query().Get("filter") == "downloading" {
				downloadsRead.Add(1)
			}
			if r.URL.Query().Get("hashes") == hash {
				workflowJSON(w, []any{task})
			} else {
				workflowJSON(w, []any{task, old})
			}
		case "/api/v2/torrents/files":
			if r.URL.Query().Get("hash") != hash {
				t.Error("organize queried unowned task")
			}
			filesRead.Add(1)
			workflowJSON(w, []any{map[string]any{"index": 0, "name": file, "size": 1024, "progress": 1, "priority": 1}})
		case "/api/v2/torrents/addTags":
			if err := r.ParseForm(); err != nil || r.Form.Get("hashes") != hash {
				t.Error("completion tagged an unrelated task")
			}
		default:
			t.Errorf("unexpected downloader request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()

	cards := make(chan string, 12)
	lark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-command-fixture"}`)
		case strings.Contains(r.URL.Path, "/reactions"):
			_, _ = io.WriteString(w, `{"code":0,"data":{"reaction_id":"rx_fixture"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/open-apis/im/v1/messages":
			var in struct{ Content string }
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			cards <- in.Content
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_commands","chat_id":"oc_commands"}}`)
		default:
			t.Errorf("unexpected Lark request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer lark.Close()
	a.pluginOriginalLarkTestOrigin = lark.URL + "/open-apis"
	d := DownloaderConfig{Config: download.Config{ID: "owned-qb", Kind: "qb", BaseURL: qb.URL}, Name: "Owned QB", DownloadPath: source}
	a.config.Downloaders = []DownloaderConfig{d}
	a.config.DefaultDownloader, a.config.DefaultRoot, a.config.AppDomain = d.ID, "root", "https://owned.invalid"
	rule := overwriteRule(a, source, target, "never", true)
	rule.MonitorType = "downloader"
	a.config.Directories[0] = rule
	intent := Intent{
		ID: "owned-command-intent", WorkflowID: "wf_owned_command", State: "accepted", Hash: hash, Media: identity, RootID: "root",
		DownloaderID: d.ID, DownloadPath: source, DownloaderSnapshot: downloaderSnapshot(d), ImportMode: "hardlink",
		DirectoryID: rule.ID, DirectorySnapshot: directoryFingerprint(rule), CreatedAt: now(), UpdatedAt: now(),
	}
	if err := a.Store.Put(t.Context(), "intent", intent.ID, intent); err != nil {
		t.Fatal(err)
	}
	a.Start(t.Context())
	admin := setupTestAdmin(t, a)
	installMergeSiteSwitchPackage(t, a, admin, bundle, "LarkMessager")
	api := a.Handler()
	const plugin = "/api/plugins/LarkMessager"
	configured := callTest(t, api, "PUT", plugin, `{"enabled":true,"app_id":"owned-app","app_secret":"owned-secret","chat_id":"oc_commands","verification_token":"owned-verification","switchs":["资源下载"]}`, admin, "")
	assertOriginalRegistrySuccess(t, configured.Code, configured.Body.Bytes())
	send := func(messageID, command string) {
		t.Helper()
		payload := mustCleanSeedJSON(map[string]any{
			"header": map[string]any{"event_type": "im.message.receive_v1"},
			"event": map[string]any{
				"sender": map[string]any{"sender_id": map[string]any{"open_id": "ou_owner"}},
				"message": map[string]any{"message_id": messageID, "chat_id": "oc_commands", "message_type": "text",
					"content": string(mustCleanSeedJSON(map[string]string{"text": command}))},
			},
		})
		result := callTest(t, api, "POST", plugin+"/api/webhook?token=owned-verification", string(payload), nil, "")
		if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"success":true`) {
			t.Fatalf("webhook admission: %d %s", result.Code, result.Body.String())
		}
	}
	next := func(fragments ...string) string {
		t.Helper()
		select {
		case card := <-cards:
			for _, fragment := range fragments {
				if !strings.Contains(card, fragment) {
					t.Fatalf("missing %q from card: %s", fragment, card)
				}
			}
			return card
		case <-time.After(6 * time.Second):
			t.Fatalf("no card for %v", fragments)
			return ""
		}
	}

	send("om_active", "/downloading")
	card := next("共 1 个任务正在下载", "1. "+file+" 1.0K 62.5%", "#/downloading")
	if strings.Contains(card, "Old.MoviePilot") {
		t.Fatal("downloading command disclosed another application's task")
	}
	complete.Store(true)
	send("om_empty", "/downloading")
	next("没有正在下载的任务", "#/downloading")
	send("om_transfer", "/transfer")
	next("开始执行 下载文件整理 ...")
	next("下载文件整理 执行完成")

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if err := a.Store.Get(t.Context(), "intent", intent.ID, &intent); err != nil {
			t.Fatal(err)
		}
		if intent.State == "imported" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var plan ImportPlan
	if intent.State != "imported" || intent.ImportPlanID == "" || a.Store.Get(t.Context(), "import_plan", intent.ImportPlanID, &plan) != nil || len(plan.Files) != 1 {
		t.Fatalf("transfer did not organize owned intent: state=%s error=%s plan=%s", intent.State, intent.Error, intent.ImportPlanID)
	}
	src, srcErr := os.Stat(input)
	dst, dstErr := os.Stat(plan.Files[0].Target)
	if srcErr != nil || dstErr != nil || !os.SameFile(src, dst) || !strings.HasPrefix(plan.Files[0].Target, target+string(filepath.Separator)) {
		t.Fatalf("transfer did not publish the real authorized hardlink: %v / %v", srcErr, dstErr)
	}
	if downloadsRead.Load() != 2 || filesRead.Load() != 1 {
		t.Fatalf("unexpected downloader calls: downloading=%d files=%d", downloadsRead.Load(), filesRead.Load())
	}
	t.Log("the installed Lark worker reported the owned active task and empty list, and /transfer admitted and published one completed owned download")
}
