package app

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/sites"
)

func TestInstalledLarkEncryptedAdminAndFetchChatsLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	const encryptKey = "owned-encrypted-webhook-key"
	const providerSecret = "owned-secret-in-provider-error"
	type peerCall struct {
		method, path, token, target, content, reaction string
	}
	calls := make(chan peerCall, 24)
	var tokens, chatCalls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/open-apis")
		if path == "/auth/v3/tenant_access_token/internal" {
			var credentials map[string]string
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&credentials) != nil ||
				credentials["app_id"] != "owned-worker-id" || credentials["app_secret"] != "owned-worker-secret" {
				t.Error("unexpected worker token request")
			}
			_, _ = fmt.Fprintf(w, `{"code":0,"tenant_access_token":"owned-token-%d","expire":7200}`, tokens.Add(1))
			return
		}
		got := peerCall{method: r.Method, path: path, token: r.Header.Get("Authorization")}
		switch {
		case path == "/im/v1/chats":
			if r.Method != http.MethodGet || r.URL.Query().Get("user_id_type") != "open_id" || r.URL.Query().Get("page_size") != "100" {
				t.Error("group query differed from installed source")
			}
			switch chatCalls.Add(1) {
			case 1:
				_, _ = io.WriteString(w, `{"code":0,"data":{"items":[{"chat_id":"oc_owned_disabled","name":"Owned Disabled Group","description":"fixture"}]}}`)
			case 2:
				_, _ = fmt.Fprintf(w, `{"code":99991672,"msg":%q}`, providerSecret)
			case 3:
				_, _ = io.WriteString(w, `{"code":0,"data":{"items":[{"chat_id":"oc_partial","name":"Partial Group"},7]}}`)
			default:
				t.Error("unexpected duplicate group fetch")
				http.NotFound(w, r)
			}
		case strings.Contains(path, "/reactions"):
			if r.Method == http.MethodPost {
				var reaction struct {
					ReactionType struct {
						EmojiType string `json:"emoji_type"`
					} `json:"reaction_type"`
				}
				if err := json.NewDecoder(r.Body).Decode(&reaction); err != nil {
					t.Error(err)
				}
				got.reaction = reaction.ReactionType.EmojiType
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"reaction_id":"rx_owned"}}`)
		case path == "/im/v1/messages":
			var message struct {
				Target  string `json:"receive_id"`
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
				t.Error(err)
			}
			got.target, got.content = message.Target, message.Content
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_owned_response","chat_id":"oc_owned"}}`)
		default:
			t.Error("unexpected Lark API route", r.Method, path)
			http.NotFound(w, r)
		}
		select {
		case calls <- got:
		default:
			t.Error("unexpected extra Lark API requests")
		}
	}))
	t.Cleanup(peer.Close)
	a := newTestApp(t)
	a.pluginOriginalLarkTestOrigin = peer.URL + "/open-apis"
	settings := a.Settings()
	settings.Sites = []SiteConfig{{Enabled: true, Config: sites.Config{
		ID: "owned-encrypted-site", Name: "Owned Encrypted Site", Kind: "ubits", BaseURL: "http://127.0.0.1:43101",
	}}}
	if err := a.saveSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	identity, err := a.Store.nativeSiteByAppID(t.Context(), settings.Sites[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	admin := setupTestAdmin(t, a)
	a.Start(t.Context())
	installMergeSiteSwitchPackage(t, a, admin, bundle, "LarkMessager")
	const base = "/api/plugins/LarkMessager"
	const api = base + "/api"
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		return callTest(t, a.Handler(), method, path, body, admin, "")
	}
	configure := func(enabled bool) {
		t.Helper()
		config := mustCleanSeedJSON(map[string]any{
			"enabled": enabled, "app_id": "owned-worker-id", "app_secret": "owned-worker-secret", "chat_id": "oc_owned",
			"encrypt_key": encryptKey, "admin_users": "ou_admin", "switchs": []string{"插件"},
		})
		result := call("PUT", base, string(config))
		assertOriginalRegistrySuccess(t, result.Code, result.Body.Bytes())
	}
	next := func(method, path string) peerCall {
		t.Helper()
		select {
		case got := <-calls:
			if got.method != method || got.path != path || got.token == "" {
				t.Fatalf("unexpected provider request %s %s, wanted %s %s", got.method, got.path, method, path)
			}
			return got
		case <-time.After(8 * time.Second):
			t.Fatalf("provider did not receive %s %s", method, path)
			return peerCall{}
		}
	}
	type chatResult struct {
		OK        bool
		Msg, Time string
		Chats     []struct{ Name string }
		Displayed *bool
	}
	fetch := func(ok bool, message string) chatResult {
		t.Helper()
		result := call("GET", api+"/fetch_chats", "")
		var data chatResult
		if err := json.Unmarshal(result.Body.Bytes(), &data); err != nil || result.Code != 200 || data.OK != ok ||
			!strings.Contains(data.Msg, message) || data.Time == "" || data.Displayed == nil || *data.Displayed ||
			strings.Contains(result.Body.String(), providerSecret) || !ok && len(data.Chats) != 0 {
			t.Fatalf("fetch_chats did not store the expected source result: HTTP=%d ok=%t err=%v", result.Code, data.OK, err)
		}
		return data
	}
	pageOnce := func(message string) {
		t.Helper()
		first, second := call("GET", base+"/page", ""), call("GET", base+"/page", "")
		if first.Code != 200 || second.Code != 200 || !strings.Contains(first.Body.String(), message) ||
			strings.Contains(second.Body.String(), message) || strings.Contains(first.Body.String(), providerSecret) {
			t.Fatal("group result did not appear exactly once on the native page")
		}
	}
	configure(false)
	groups := fetch(true, "Owned Disabled Group")
	disabledToken := next(http.MethodGet, "/im/v1/chats").token
	if len(groups.Chats) != 1 || groups.Chats[0].Name != "Owned Disabled Group" || tokens.Load() != 1 {
		t.Fatal("disabled endpoint did not use one temporary provider")
	}
	pageOnce("Owned Disabled Group")
	configure(true)

	postEncrypted := func(user, messageID, command string, signed bool) {
		t.Helper()
		plain := mustCleanSeedJSON(map[string]any{
			"schema": "2.0", "header": map[string]any{"event_type": "im.message.receive_v1", "event_id": messageID},
			"event": map[string]any{
				"sender": map[string]any{"sender_id": map[string]any{"open_id": user}},
				"message": map[string]any{"message_id": messageID, "chat_id": "oc_owned", "message_type": "text",
					"content": string(mustCleanSeedJSON(map[string]string{"text": command}))},
			},
		})
		key := sha256.Sum256([]byte(encryptKey))
		block, err := aes.NewCipher(key[:])
		if err != nil {
			t.Fatal(err)
		}
		iv := bytes.Repeat([]byte{0x42}, aes.BlockSize)
		padding := aes.BlockSize - len(plain)%aes.BlockSize
		plain = append(plain, bytes.Repeat([]byte{byte(padding)}, padding)...)
		encrypted := make([]byte, len(plain))
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(encrypted, plain)
		raw := mustCleanSeedJSON(map[string]string{"encrypt": base64.StdEncoding.EncodeToString(append(iv, encrypted...))})
		request := httptest.NewRequest(http.MethodPost, api+"/webhook", bytes.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		if signed {
			timestamp, nonce := strconv.FormatInt(time.Now().Unix(), 10), "owned-nonce-"+messageID
			signature := sha256.Sum256(append([]byte(timestamp+nonce+encryptKey), raw...))
			request.Header.Set("X-Lark-Request-Timestamp", timestamp)
			request.Header.Set("X-Lark-Request-Nonce", nonce)
			request.Header.Set("X-Lark-Signature", hex.EncodeToString(signature[:]))
		}
		response := httptest.NewRecorder()
		a.Handler().ServeHTTP(response, request)
		var reply struct{ Success bool }
		if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil || response.Code != 200 || !reply.Success {
			t.Fatalf("encrypted webhook was not acknowledged: HTTP=%d err=%v", response.Code, err)
		}
	}

	sid := strconv.FormatInt(identity.SourceID, 10)
	postEncrypted("ou_admin", "om_signed_admin", "/sites disable "+sid, true)
	const adminReaction = "/im/v1/messages/om_signed_admin/reactions"
	warm := next(http.MethodPost, adminReaction)
	if warm.reaction != "GLANCE" || warm.token == disabledToken {
		t.Fatal("signed administrator did not start processing with the active provider")
	}
	action := next(http.MethodPost, "/im/v1/messages")
	list := next(http.MethodPost, "/im/v1/messages")
	stopped := next(http.MethodDelete, adminReaction+"/rx_owned")
	if !strings.Contains(action.content, "已禁用 1 个站点") || action.target != "ou_admin" || !strings.Contains(list.content, "Owned Encrypted Site") ||
		stopped.token != warm.token || a.Settings().Sites[0].Enabled {
		t.Fatal("signed administrator did not disable only the owned site and finish processing")
	}
	postEncrypted("ou_other", "om_signed_denied", "/sites enable "+sid, true)
	denial := next(http.MethodPost, "/im/v1/messages")
	if denial.target != "ou_other" || !strings.Contains(denial.content, "只有管理员才有权限执行此命令") || a.Settings().Sites[0].Enabled {
		t.Fatal("non-administrator command bypassed the configured allowlist")
	}
	postEncrypted("ou_admin", "om_unsigned_admin", "/sites enable "+sid, false)
	const unsignedReaction = "/im/v1/messages/om_unsigned_admin/reactions"
	next(http.MethodPost, unsignedReaction)
	next(http.MethodDelete, unsignedReaction+"/rx_owned")
	if a.Settings().Sites[0].Enabled {
		t.Fatal("decryption alone authorized a privileged native action")
	}

	const rejection = "Lark API 错误：请求被拒绝（code=99991672）"
	fetch(false, rejection)
	groupToken := next(http.MethodGet, "/im/v1/chats").token
	pageOnce(rejection)
	if groupToken == warm.token || groupToken == disabledToken {
		t.Fatal("fetch_chats reused a previous provider token")
	}
	if _, err := a.postOriginalPluginMessage(t.Context(), map[string]any{
		"channel": "飞书", "source": "Lark", "title": "Owned warm-cache probe", "save_history": false,
	}); err != nil {
		t.Fatal(err)
	}
	probe := next(http.MethodPost, "/im/v1/messages")
	if probe.token != warm.token || !strings.Contains(probe.content, "Owned warm-cache probe") {
		t.Fatal("temporary fetch replaced the active provider token")
	}
	const malformed = "请求失败：Lark 返回的群聊数据无效"
	fetch(false, malformed)
	malformedToken := next(http.MethodGet, "/im/v1/chats").token
	pageOnce(malformed)
	if malformedToken == groupToken || malformedToken == warm.token || tokens.Load() != 4 || chatCalls.Load() != 3 {
		t.Fatalf("unexpected provider lifecycle: token=%d groups=%d", tokens.Load(), chatCalls.Load())
	}
	t.Log("indexed worker fetched groups while disabled, isolated temporary credentials and consumed results once; signed encrypted admin controlled the owned site, while other and unsigned callers could not")
}
