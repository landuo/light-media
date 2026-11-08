package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"light-media/internal/notice"
	"light-media/internal/plugins/artifacts"
)

func TestInstalledLarkCloneAndTrustedAttachmentsLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	const (
		baseID           = "LarkMessager"
		cloneID          = "LarkMessagerownedattachments"
		key              = "last_test_result"
		ambiguousCaption = "Owned caption with an ambiguous result"
	)
	type peerCall struct {
		kind, name, mime, auth, target, content string
		data                                    []byte
	}
	calls := make(chan peerCall, 10)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := peerCall{auth: r.Header.Get("Authorization")}
		path := strings.TrimPrefix(r.URL.Path, "/open-apis")
		if r.Method != http.MethodPost {
			t.Error("unexpected Lark method", r.Method, path)
			http.NotFound(w, r)
			return
		}
		switch path {
		case "/auth/v3/tenant_access_token/internal":
			var credentials map[string]string
			if json.NewDecoder(r.Body).Decode(&credentials) != nil || credentials["app_id"] != "cli_clone" || credentials["app_secret"] != "owned-clone-secret" || call.auth != "" {
				t.Error("notification did not authenticate the clone")
			}
			call.kind = "token"
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-clone-token","expire":7200}`)
		case "/im/v1/images", "/im/v1/files":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error("invalid provider multipart upload", err)
				http.Error(w, "invalid upload", http.StatusBadRequest)
				return
			}
			defer r.MultipartForm.RemoveAll()
			field := "file"
			response := `{"code":0,"data":{"file_key":"file_owned"}}`
			if path == "/im/v1/images" {
				field = "image"
				response = `{"code":0,"data":{"image_key":"img_owned"}}`
				if r.FormValue("image_type") != "message" {
					t.Error("image_type did not follow the source multipart field")
				}
			} else if r.URL.Query().Get("file_type") != "stream" || len(r.MultipartForm.Value) != 0 {
				t.Error("file_type did not follow the source query")
			}
			part, header, err := r.FormFile(field)
			if err != nil {
				t.Error("missing source multipart field", field, err)
				http.Error(w, "invalid upload", http.StatusBadRequest)
				return
			}
			call.data, err = io.ReadAll(part)
			_ = part.Close()
			if err != nil {
				t.Error("read provider attachment", err)
			}
			call.kind, call.name, call.mime = "upload-"+field, header.Filename, header.Header.Get("Content-Type")
			_, _ = io.WriteString(w, response)
		case "/im/v1/messages":
			var message struct {
				Kind    string `json:"msg_type"`
				Target  string `json:"receive_id"`
				Content string `json:"content"`
			}
			if json.NewDecoder(r.Body).Decode(&message) != nil || r.URL.Query().Get("receive_id_type") != "chat_id" || !json.Valid([]byte(message.Content)) {
				t.Error("invalid Lark attachment message")
			}
			call.kind, call.target, call.content = message.Kind, message.Target, message.Content
			if message.Kind == "text" && strings.Contains(message.Content, ambiguousCaption) {
				_, _ = io.WriteString(w, `{"code":`)
			} else {
				_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_clone_attachment","chat_id":"oc_clone"}}`)
			}
		default:
			t.Error("unexpected provider path", path)
			http.NotFound(w, r)
			return
		}
		select {
		case calls <- call:
		default:
			t.Error("unexpected extra provider requests")
		}
	}))
	t.Cleanup(peer.Close)

	a := newTestApp(t)
	a.pluginOriginalLarkTestOrigin = peer.URL + "/open-apis"
	root := t.TempDir()
	settings := a.Settings()
	settings.AllowedRoots = []string{root}
	ctx := t.Context()
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	admin := setupTestAdmin(t, a)
	a.Start(ctx)
	installMergeSiteSwitchPackage(t, a, admin, bundle, baseID)
	request := func(method, path string, value any) *httptest.ResponseRecorder {
		t.Helper()
		body := ""
		if value != nil {
			body = string(mustCleanSeedJSON(value))
		}
		return callTest(t, a.Handler(), method, "/api/plugins/"+path, body, admin, "")
	}
	save := func(id, account, secret, chat string, enabled bool) {
		t.Helper()
		r := request(http.MethodPut, id, map[string]any{"enabled": enabled, "app_id": account, "app_secret": secret, "chat_id": chat})
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	}
	config := func(id string) PluginStoredValue {
		t.Helper()
		stored, err := a.pluginStore.Config(ctx, id)
		if err != nil {
			t.Fatal("missing stored instance configuration", id, err)
		}
		return stored
	}
	assertState := func(object *originalPluginInstance, expected bool) {
		t.Helper()
		var enabled bool
		if err := object.readSession(ctx, "get_state", &enabled); err != nil || enabled != expected {
			t.Fatal("unexpected real Lark worker state", object.installation.InstanceID, enabled, err)
		}
	}
	writeMarker := func(id, message string) {
		t.Helper()
		if err := a.pluginStore.originalBaseSaveData(ctx, id, key, mustCleanSeedJSON(map[string]any{"ok": true, "msg": message, "displayed": false})); err != nil {
			t.Fatal(err)
		}
	}
	assertMarker := func(id, message string, displayed bool) {
		t.Helper()
		value, err := a.pluginStore.Data(ctx, id, key)
		var got struct {
			Message   string `json:"msg"`
			Displayed bool
		}
		if err != nil || json.Unmarshal(value.Value, &got) != nil || got.Message != message || got.Displayed != displayed {
			t.Fatal("unexpected instance result marker", id, err)
		}
	}

	save(baseID, "cli_base", "owned-base-secret", "oc_base", true)
	writeMarker(baseID, "Owned base marker")
	r := request(http.MethodPost, baseID+"/clone", map[string]string{"suffix": "OwnedAttachments"})
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	clone := originalBaseSDKLoaded(t, a, cloneID)
	assertState(clone, false)
	var inherited struct {
		AppID  string `json:"app_id"`
		ChatID string `json:"chat_id"`
	}
	if json.Unmarshal(config(cloneID).Value, &inherited) != nil || inherited.AppID != "cli_base" || inherited.ChatID != "oc_base" {
		t.Fatal("new Lark clone did not inherit source configuration")
	}
	if _, err := a.pluginStore.Data(ctx, cloneID, key); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("source result was copied into the clone", err)
	}
	save(baseID, "cli_base", "owned-base-secret", "oc_base", false)
	base := originalBaseSDKLoaded(t, a, baseID)
	assertState(base, false)
	baseConfig := config(baseID)
	basePID := installedLarkFixturePID(t, a, baseID)
	save(cloneID, "cli_clone", "owned-clone-secret", "oc_clone", true)
	clone = originalBaseSDKLoaded(t, a, cloneID)
	assertState(clone, true)
	clonePID := installedLarkFixturePID(t, a, cloneID)
	if clonePID == basePID {
		t.Fatal("source and clone share a process")
	}
	cloneConfig := config(cloneID)
	writeMarker(cloneID, "Owned clone marker")
	r = request(http.MethodPost, cloneID+"/reload", map[string]any{})
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	reloaded := originalBaseSDKLoaded(t, a, cloneID)
	assertState(reloaded, true)
	privateAwait(t, "old Lark clone process exits", func() bool { return syscall.Kill(clonePID, 0) == syscall.ESRCH })
	if reloaded == clone || installedLarkFixturePID(t, a, cloneID) == clonePID || originalBaseSDKLoaded(t, a, baseID) != base || installedLarkFixturePID(t, a, baseID) != basePID {
		t.Fatal("clone reload did not isolate process replacement")
	}
	for id, before := range map[string]PluginStoredValue{baseID: baseConfig, cloneID: cloneConfig} {
		after := config(id)
		if after.Revision != before.Revision || !bytes.Equal(after.Value, before.Value) {
			t.Fatal("clone save or reload changed persisted configuration", id)
		}
	}
	assertMarker(baseID, "Owned base marker", false)
	assertMarker(cloneID, "Owned clone marker", false)
	page := request(http.MethodGet, cloneID+"/page", nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Owned clone marker") || strings.Contains(page.Body.String(), "Owned base marker") {
		t.Fatal("reloaded clone page read outside its own result namespace")
	}
	assertMarker(cloneID, "Owned clone marker", true)
	assertMarker(baseID, "Owned base marker", false)

	writeAttachment := func(directory, name string, content []byte) string {
		t.Helper()
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	image, document, voice := []byte("\x89PNG\r\n\x1a\nowned image"), []byte("owned document\x00"), []byte("OggS owned voice")
	imagePath := writeAttachment(root, "owned.png", image)
	filePath := writeAttachment(root, "owned.bin", document)
	voicePath := writeAttachment(root, "owned.opus", voice)
	post := func(fields map[string]any) {
		t.Helper()
		fields["channel"], fields["source"], fields["save_history"] = "飞书", "Lark", false
		result, err := a.postOriginalPluginMessage(ctx, fields)
		if err != nil || result.Attempted != 1 || result.Queued != 0 || result.Failed != 0 {
			t.Fatal("trusted Lark notification did not dispatch", result, err)
		}
	}
	next := func(kind string) peerCall {
		t.Helper()
		select {
		case call := <-calls:
			if call.kind != kind || kind != "token" && call.auth != "Bearer owned-clone-token" {
				t.Fatal("unexpected provider sequence or instance", call.kind, kind)
			}
			return call
		case <-time.After(5 * time.Second):
			t.Fatal("provider did not observe", kind)
			return peerCall{}
		}
	}
	upload := func(kind, name, mime string, data []byte) {
		t.Helper()
		call := next("upload-" + kind)
		if call.name != name || call.mime != mime || !bytes.Equal(call.data, data) {
			t.Fatal("provider attachment did not match the granted local file", kind, call.name, call.mime)
		}
	}
	message := func(kind, fragment string) {
		t.Helper()
		call := next(kind)
		if call.target != "oc_clone" || !strings.Contains(call.content, fragment) {
			t.Fatal("attachment message used an unexpected destination or content", kind)
		}
	}
	post(map[string]any{"title": "Owned image", "file_path": imagePath})
	next("token")
	upload("image", "owned.png", "image/png", image)
	message("interactive", "img_owned")
	post(map[string]any{"title": "Owned image and document", "image": "feishu://image/img_owned", "file_path": filePath, "file_name": "owned-export.txt"})
	message("interactive", "img_owned")
	upload("file", "owned-export.txt", "application/octet-stream", document)
	message("file", "file_owned")
	post(map[string]any{"voice_path": voicePath, "voice_caption": "Owned voice caption"})
	upload("file", "owned.opus", "application/octet-stream", voice)
	message("audio", "file_owned")
	message("text", "Owned voice caption")

	// The native delivery count distinguishes an opaque caption result from
	// the audio the provider already accepted. Never submit the audio again.
	attempts, err := a.sendPluginMessageModules(ctx, pluginNoticeQueueEntry{Call: notice.Call{
		Args: []any{"post_message"},
		Kwargs: notice.Object{"message": notice.Object{
			"channel": "飞书", "source": "Lark", "voice_path": voicePath, "voice_caption": ambiguousCaption,
		}},
	}})
	if attempts != 1 || pluginErrorCodeInternal(err) != "PLUGIN_NOTIFICATION_SEND_FAILED" {
		t.Fatal("unknown caption did not remain a failed delivery observation", attempts, err)
	}
	upload("file", "owned.opus", "application/octet-stream", voice)
	message("audio", "file_owned")
	message("text", ambiguousCaption)
	privateFile := writeAttachment(a.Dir, "owned-private.opus", []byte("private fixture content"))
	post(map[string]any{"voice_path": privateFile})
	if len(calls) != 0 {
		t.Fatal("protected path or disabled base produced an extra provider write")
	}
	t.Logf("Lark base PID %d stayed disabled and stable; clone PID %d was replaced and kept its own KV. The clone sent the exact granted image, document and voice bytes in source order.", basePID, clonePID)
	t.Log("The accepted audio was followed by one unconfirmed caption, preserved as a failed module delivery without repeating either write. A protected file never reached the provider.")
}

func installedLarkFixturePID(t *testing.T, a *App, instance string) int {
	t.Helper()
	artifact, err := artifacts.ReadInstalled(a.pluginPackageRoot(), instance, validatePluginArtifactContract)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.Stat(artifact.ExecutablePath)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	pid := 0
	for _, entry := range entries {
		candidate, err := strconv.Atoi(entry.Name())
		if err != nil || candidate <= 0 {
			continue
		}
		actual, err := os.Stat(filepath.Join("/proc", entry.Name(), "exe"))
		if err == nil && os.SameFile(actual, expected) {
			if pid != 0 {
				t.Fatal("more than one process used the instance executable", instance)
			}
			pid = candidate
		}
	}
	if pid == 0 {
		t.Fatal("no process used the installed instance executable", instance)
	}
	return pid
}
