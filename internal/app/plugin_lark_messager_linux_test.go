package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLarkMessagerInstalledLinuxAMD64MessagePath(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	type request struct {
		path, method, receiveType, authorization string
		payload                                  map[string]any
	}
	received := make(chan request, 16)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		select {
		case received <- request{r.URL.Path, r.Method, r.URL.Query().Get("receive_id_type"), r.Header.Get("Authorization"), body}:
		default:
			t.Error("unexpected additional Lark API request")
		}
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-tenant","expire":7200}`)
		case "/open-apis/im/v1/messages":
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_fixture_sent","chat_id":"oc_fixture"}}`)
		case "/open-apis/im/v1/messages/om_fixture_sent/reply":
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_fixture_reply","chat_id":"oc_fixture"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(peer.Close)
	a := newTestApp(t)
	a.pluginOriginalLarkTestOrigin = peer.URL + "/open-apis"
	cookie := setupTestAdmin(t, a)
	a.Start(t.Context())
	installMergeSiteSwitchPackage(t, a, cookie, bundle, "LarkMessager")
	const base = "/api/plugins/LarkMessager"
	const api = base + "/api"
	call := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		return callTest(t, a.Handler(), method, path, body, cookie, "")
	}
	next := func(path string) request {
		t.Helper()
		select {
		case got := <-received:
			if got.path != "/open-apis"+path {
				t.Fatalf("unexpected provider call %s %s, wanted %s", got.method, got.path, path)
			}
			return got
		case <-time.After(5 * time.Second):
			t.Fatalf("provider did not receive %s", path)
			return request{}
		}
	}
	form := call("GET", base+"/form", "", cookie)
	if form.Code != 200 || !strings.Contains(form.Body.String(), `"verification_token"`) {
		t.Fatalf("native form unavailable: %d %s", form.Code, form.Body)
	}
	config := call("PUT", base, `{"enabled":true,"app_id":"cli_fixture","app_secret":"owned-fixture-secret","chat_id":"oc_fixture","verification_token":"owned-verification"}`, cookie)
	assertOriginalRegistrySuccess(t, config.Code, config.Body.Bytes())
	if unauthorized := call("GET", api+"/test", "", nil); unauthorized.Code != 401 {
		t.Fatalf("anonymous connection test returned %d", unauthorized.Code)
	}
	if result := call("GET", api+"/test", "", cookie); result.Code != 200 || !strings.Contains(result.Body.String(), "om_fixture_sent") || !strings.Contains(result.Body.String(), `"ok":true`) {
		t.Fatalf("connection test did not persist an accepted card: %d %s", result.Code, result.Body)
	}
	token := next("/auth/v3/tenant_access_token/internal")
	if token.method != "POST" || token.authorization != "" || token.payload["app_id"] != "cli_fixture" || token.payload["app_secret"] != "owned-fixture-secret" {
		t.Fatal("unexpected token request")
	}
	sent := next("/im/v1/messages")
	card, _ := sent.payload["content"].(string)
	if sent.method != "POST" || sent.authorization != "Bearer owned-tenant" || sent.receiveType != "chat_id" ||
		sent.payload["receive_id"] != "oc_fixture" || sent.payload["msg_type"] != "interactive" || !strings.Contains(card, `"callback_data":"test_ok"`) {
		t.Fatalf("connection card diverged from fixed source: %s %s", sent.receiveType, card)
	}
	if page := call("GET", base+"/page", "", cookie); page.Code != 200 || !strings.Contains(page.Body.String(), "om_fixture_sent") {
		t.Fatalf("connection result missing from page: %d %s", page.Code, page.Body)
	}
	if page := call("GET", base+"/page", "", cookie); page.Code != 200 || strings.Contains(page.Body.String(), "om_fixture_sent") {
		t.Fatalf("the page should display a result once: %d %s", page.Code, page.Body)
	}
	const callback = `{"schema":"2.0","header":{"event_type":"card.action.trigger"},"event":{"operator":{"open_id":"ou_fixture"},"context":{"open_message_id":"om_fixture_sent","open_chat_id":"oc_fixture"},"action":{"tag":"button","value":{"callback_data":"[PLUGIN]LarkMessager|test_ok"}}}}`
	if denied := call("POST", api+"/webhook?token=wrong", callback, nil); denied.Code != 403 {
		t.Fatalf("incorrect callback token returned %d %s", denied.Code, denied.Body)
	}
	if accepted := call("POST", api+"/webhook?token=owned-verification", callback, nil); accepted.Code != 200 || !strings.Contains(accepted.Body.String(), `"success":true`) {
		t.Fatalf("actual callback was rejected: %d %s", accepted.Code, accepted.Body)
	}
	reply := next("/im/v1/messages/om_fixture_sent/reply")
	replyText, _ := reply.payload["content"].(string)
	if reply.method != "POST" || reply.authorization != "Bearer owned-tenant" || reply.payload["msg_type"] != "text" || !strings.Contains(replyText, "测试确认成功") {
		t.Fatalf("callback did not reply to the original message: %s", replyText)
	}
	if _, err := a.postOriginalPluginMessage(t.Context(), map[string]any{"channel": "飞书", "source": "Lark", "title": "Owned ordinary notice", "save_history": false}); err != nil {
		t.Fatal(err)
	}
	notice := next("/im/v1/messages")
	noticeText, _ := notice.payload["content"].(string)
	if notice.payload["receive_id"] != "oc_fixture" || !strings.Contains(noticeText, "Owned ordinary notice") {
		t.Fatalf("ordinary host notice had unexpected target or content: %s", noticeText)
	}
	t.Log("fixed Lark routes and native form passed; bearer test delivered source card; page consumed saved result once; wrong token rejected; anonymous callback parsed and replied through MessageAction; ordinary host notice reached the isolated peer")
}
