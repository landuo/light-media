package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"

	"light-media/internal/notice"
	"light-media/internal/plugins/artifacts"
)

func TestInstalledLarkRejectionAndUninstallLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_REJECTION_PACKAGE_DIR")
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the changed indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	const (
		instance      = "LarkMessager"
		target        = "oc_owned"
		rejectedTitle = "Owned definite rejection"
		opaqueTitle   = "Owned opaque acknowledgment"
		providerMsg   = "owned private provider diagnostic"
		filename      = "owned.txt"
		fileContents  = "owned Lark rejection fixture"
		resultKey     = "last_test_result"
	)
	var operations []string
	var peerMu sync.Mutex
	record := func(operation string) {
		peerMu.Lock()
		operations = append(operations, operation)
		peerMu.Unlock()
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Error("unexpected Lark provider method", r.Method)
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/open-apis")
		if path != "/auth/v3/tenant_access_token/internal" && r.Header.Get("Authorization") != "Bearer owned-lark-rejection-token" {
			t.Error("Lark provider request lacked owned authorization")
		}
		switch path {
		case "/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-lark-rejection-token","expire":7200}`)
		case "/im/v1/files":
			record("upload")
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error("invalid fixture upload", err)
				http.Error(w, "invalid fixture upload", http.StatusBadRequest)
				return
			}
			defer r.MultipartForm.RemoveAll()
			part, header, err := r.FormFile("file")
			if err != nil {
				t.Error("missing granted file", err)
				http.Error(w, "missing granted file", http.StatusBadRequest)
				return
			}
			defer part.Close()
			data, err := io.ReadAll(part)
			if err != nil || string(data) != fileContents || header.Filename != filename {
				t.Error("uploaded file did not match the exact local fixture", err)
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"file_key":"file_owned"}}`)
		case "/im/v1/messages":
			var message struct {
				Kind    string `json:"msg_type"`
				Target  string `json:"receive_id"`
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&message); err != nil || message.Target != target || r.URL.Query().Get("receive_id_type") != "chat_id" {
				t.Error("unexpected Lark request target or body", err)
			}
			switch {
			case message.Kind == "interactive" && strings.Contains(message.Content, rejectedTitle):
				record("rejected-card")
				_, _ = io.WriteString(w, `{"code":230001,"msg":"`+providerMsg+`"}`)
			case message.Kind == "interactive" && strings.Contains(message.Content, opaqueTitle):
				record("opaque-card")
				_, _ = io.WriteString(w, `{"code":"230001"}`)
			case message.Kind == "file" && strings.Contains(message.Content, "file_owned"):
				record("file")
				_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_owned","chat_id":"oc_owned"}}`)
			default:
				t.Error("unexpected Lark message kind or fixture content", message.Kind)
				http.NotFound(w, r)
			}
		default:
			t.Error("unexpected Lark provider path", path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(peer.Close)

	a := newTestApp(t)
	ctx := t.Context()
	a.pluginOriginalLarkTestOrigin = peer.URL + "/open-apis"
	root := t.TempDir()
	path := filepath.Join(root, filename)
	if err := os.WriteFile(path, []byte(fileContents), 0600); err != nil {
		t.Fatal(err)
	}
	settings := a.Settings()
	settings.AllowedRoots = []string{root}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	admin := setupTestAdmin(t, a)
	a.Start(ctx)
	installMergeSiteSwitchPackage(t, a, admin, bundle, instance)
	api := "/api/plugins/" + instance
	configured := callTest(t, a.Handler(), http.MethodPut, api, `{"enabled":true,"app_id":"cli_owned","app_secret":"owned-fixture-secret","chat_id":"oc_owned"}`, admin, "")
	assertOriginalRegistrySuccess(t, configured.Code, configured.Body.Bytes())
	pid := installedLarkFixturePID(t, a, instance)
	config, err := a.pluginStore.Config(ctx, instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.pluginStore.originalBaseSaveData(ctx, instance, resultKey, json.RawMessage(`{"ok":true,"msg":"Owned result","displayed":false}`)); err != nil {
		t.Fatal(err)
	}
	result, err := a.pluginStore.Data(ctx, instance, resultKey)
	if err != nil {
		t.Fatal(err)
	}

	notify := func(title string) notice.Object {
		return notice.Object{"channel": "飞书", "source": "Lark", "title": title, "image": "feishu://image/img_owned", "file_path": path, "save_history": false}
	}
	dispatched, err := a.postOriginalPluginMessage(ctx, notify(rejectedTitle))
	if err != nil || dispatched.Attempted != 1 || dispatched.Queued != 0 || dispatched.Failed != 0 {
		t.Fatal("definite card rejection should continue to the attachment", dispatched, err)
	}
	// MoviePilot consumes ordinary module errors in the public dispatcher.
	// Read the existing native observation to distinguish an opaque provider
	// acknowledgment from an ordinary, logged rejection.
	attempts, err := a.sendPluginMessageModules(ctx, pluginNoticeQueueEntry{Call: notice.Call{
		Args: []any{"post_message"}, Kwargs: notice.Object{"message": notify(opaqueTitle)},
	}})
	if attempts != 1 || pluginErrorCodeInternal(err) != "PLUGIN_NOTIFICATION_SEND_FAILED" {
		t.Fatal("opaque card acknowledgment was not preserved as a failed delivery observation", attempts, err)
	}
	logs, err := a.queryLogs(ctx, map[string]string{"from": "0001-01-01T00:00:00Z", "limit": "500"})
	if err != nil {
		t.Fatal(err)
	}
	rejectionLogs := 0
	for _, row := range logs.Entries {
		message, _ := row["message"].(string)
		if strings.Contains(message, providerMsg) {
			t.Fatal("provider diagnostics escaped the log boundary")
		}
		if row["plugin_instance_id"] == instance && row["event"] == "plugin_message" && message == "Lark 通知发送失败" {
			rejectionLogs++
		}
	}
	if rejectionLogs != 1 {
		t.Fatalf("expected one plugin-attributed redacted rejection log, got %d", rejectionLogs)
	}

	webhook := "/api/v1/plugin/" + instance + "/webhook"
	challenge := func() *httptest.ResponseRecorder {
		return callTest(t, a.Handler(), http.MethodPost, webhook, `{"type":"url_verification","challenge":"owned-lifecycle"}`, nil, "")
	}
	before := challenge()
	if before.Code != http.StatusOK || !strings.Contains(before.Body.String(), "owned-lifecycle") {
		t.Fatal("installed anonymous webhook did not answer the source challenge", before.Code)
	}
	uninstalled := callTest(t, a.Handler(), http.MethodDelete, api, "", admin, "")
	assertOriginalRegistrySuccess(t, uninstalled.Code, uninstalled.Body.Bytes())
	privateAwait(t, "uninstalled Lark worker exits", func() bool { return syscall.Kill(pid, 0) == syscall.ESRCH })
	if _, loaded := a.pluginRunningInstances.Load(instance); loaded {
		t.Fatal("uninstall retained the live Lark worker")
	}
	if gone := challenge(); gone.Code != http.StatusNotFound {
		t.Fatal("uninstalled webhook is still reachable", gone.Code)
	}
	if installed, err := a.pluginStore.Installation(ctx, instance); err != nil || installed.Installed {
		t.Fatal("uninstall did not clear persisted installed membership", err)
	}
	keptConfig, configErr := a.pluginStore.Config(ctx, instance)
	keptResult, resultErr := a.pluginStore.Data(ctx, instance, resultKey)
	if configErr != nil || resultErr != nil || keptConfig.Revision != config.Revision || !bytes.Equal(keptConfig.Value, config.Value) ||
		keptResult.Revision != result.Revision || !bytes.Equal(keptResult.Value, result.Value) {
		t.Fatal("ordinary base uninstall did not retain configuration and data", configErr, resultErr)
	}
	if _, err := artifacts.ReadInstalled(a.pluginPackageRoot(), instance, validatePluginArtifactContract); err != nil {
		t.Fatal("ordinary base uninstall did not retain code", err)
	}
	_, err = a.postOriginalPluginMessage(ctx, notify(rejectedTitle))
	if err != nil {
		t.Fatal("post-uninstall ordinary notification did not return normally", err)
	}
	peerMu.Lock()
	got := slices.Clone(operations)
	peerMu.Unlock()
	if want := []string{"rejected-card", "upload", "file", "opaque-card"}; !slices.Equal(got, want) {
		t.Fatalf("expected one attachment after definite rejection and no replay or post-uninstall write: got %v", got)
	}
	t.Log("indexed Lark sent exact file bytes after one logged card rejection, fenced the ambiguous card without upload or replay, then uninstalled its live worker and webhook while retaining base code, configuration and data")
}
