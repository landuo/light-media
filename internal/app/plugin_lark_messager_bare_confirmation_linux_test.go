package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestInstalledLarkBareTestConfirmationLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	const (
		originalMessageID = "om_owned_connection"
		confirmationText  = "✅ 测试确认成功！LarkMessager 插件卡片交互正常工作。"
	)
	callbacks := make(chan string, 1)
	var replies atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/open-apis")
		if r.Method != http.MethodPost || path != "/auth/v3/tenant_access_token/internal" && r.Header.Get("Authorization") != "Bearer owned-bare-token" {
			t.Error("connection card used an unexpected provider method or account")
			http.Error(w, "unexpected provider call", http.StatusBadRequest)
			return
		}
		switch path {
		case "/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-bare-token","expire":7200}`)
		case "/im/v1/messages", "/im/v1/messages/" + originalMessageID + "/reply":
			var body struct {
				Kind      string `json:"msg_type"`
				Content   string `json:"content"`
				Recipient string `json:"receive_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error("provider message envelope is invalid", err)
				http.Error(w, "invalid message", http.StatusBadRequest)
				return
			}
			if path == "/im/v1/messages" {
				match := regexp.MustCompile(`"callback_data":"([^"]+)"`).FindStringSubmatch(body.Content)
				if body.Kind != "interactive" || body.Recipient != "oc_owned" || len(match) != 2 || match[1] != "test_ok" {
					t.Error("the real connection card did not carry the source's bare confirmation button")
					http.Error(w, "invalid card", http.StatusBadRequest)
					return
				}
				select {
				case callbacks <- match[1]:
				default:
					t.Error("connection test sent another card")
				}
				_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"`+originalMessageID+`"}}`)
				return
			}
			var content struct{ Text string }
			if err := json.Unmarshal([]byte(body.Content), &content); err != nil || body.Kind != "text" || body.Recipient != "" || content.Text != confirmationText {
				t.Error("bare confirmation did not send the exact direct reply", err)
				http.Error(w, "invalid reply", http.StatusBadRequest)
				return
			}
			replies.Add(1)
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_owned_confirmation"}}`)
		default:
			t.Error("unexpected connection test provider route", path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(peer.Close)

	markets := []string{}
	a, err := New(Options{DataDir: t.TempDir(), PluginMarketsOverride: &markets})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	a.pluginOriginalLarkTestOrigin = peer.URL + "/open-apis"
	admin := setupTestAdmin(t, a)
	a.Start(t.Context())
	installMergeSiteSwitchPackage(t, a, admin, bundle, "LarkMessager")
	const base = "/api/plugins/LarkMessager"
	config := callTest(t, a.Handler(), http.MethodPut, base, `{"enabled":true,"app_id":"cli_owned","app_secret":"owned-secret","chat_id":"oc_owned","verification_token":"owned-verification"}`, admin, "")
	assertOriginalRegistrySuccess(t, config.Code, config.Body.Bytes())
	test := callTest(t, a.Handler(), http.MethodGet, base+"/api/test", "", admin, "")
	if test.Code != http.StatusOK || !strings.Contains(test.Body.String(), originalMessageID) || !strings.Contains(test.Body.String(), `"ok":true`) {
		t.Fatalf("owned connection card was not accepted: %d %s", test.Code, test.Body)
	}
	var callback string
	select {
	case callback = <-callbacks:
	default:
		t.Fatal("provider did not receive the generated confirmation button")
	}
	payload := mustCleanSeedJSON(map[string]any{
		"header": map[string]any{"event_type": "card.action.trigger"},
		"event": map[string]any{
			"operator": map[string]any{"open_id": "ou_owned"},
			"context":  map[string]any{"open_message_id": originalMessageID, "open_chat_id": "oc_owned"},
			"action":   map[string]any{"tag": "button", "value": map[string]string{"callback_data": callback}},
		},
	})
	confirmed := callTest(t, a.Handler(), http.MethodPost, base+"/api/webhook?token=owned-verification", string(payload), nil, "")
	if confirmed.Code != http.StatusOK || strings.TrimSpace(confirmed.Body.String()) != `{"success":true}` || replies.Load() != 1 {
		t.Fatalf("bare callback did not acknowledge and reply exactly once: status=%d replies=%d", confirmed.Code, replies.Load())
	}
	t.Log("the actual generated bare test_ok button caused one source-shaped direct reply to the exact original provider message")
}
