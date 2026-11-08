package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOriginalLarkRestartLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(key, "")
	}
	const (
		base     = "LarkMessager"
		clone    = "LarkMessagerrestart"
		sender   = "ou_owned_restart"
		expected = "Bearer owned-clone-token"
	)
	cards := make(chan string, 4)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch path := strings.TrimPrefix(r.URL.Path, "/open-apis"); {
		case path == "/auth/v3/tenant_access_token/internal":
			var config map[string]string
			if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
				t.Error("invalid restart fixture authorization", err)
				return
			}
			switch {
			case config["app_id"] == "cli_restart_clone" && config["app_secret"] == "owned-clone-secret":
				_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-clone-token","expire":7200}`)
			case config["app_id"] == "cli_restart_base" && config["app_secret"] == "owned-base-secret":
				// The ordinary processing hooks may authenticate the base.
				// Actual restart messages below must still use the clone token.
				_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-base-token","expire":7200}`)
			default:
				t.Error("unexpected account authenticated in restart fixture")
			}
		case strings.Contains(path, "/reactions"):
			_, _ = io.WriteString(w, `{"code":0,"data":{"reaction_id":"rx_restart"}}`)
		case path == "/im/v1/messages" && r.Method == http.MethodPost:
			var input struct {
				Recipient string `json:"receive_id"`
				Content   string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || r.Header.Get("Authorization") != expected ||
				input.Recipient != sender || r.URL.Query().Get("receive_id_type") != "open_id" {
				t.Error("restart notice did not bind to its verified sender and clone", err)
			}
			select {
			case cards <- input.Content:
			default:
				t.Error("repeated restart notice")
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_restart_notice"}}`)
		default:
			t.Error("unexpected Lark restart fixture route", path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(peer.Close)
	releases := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "api.github.com" || r.URL.Path != "/repos/landuo/light-media/releases" || r.Method != http.MethodGet {
			t.Error("restart version did not use the canonical release lookup", r.Method, r.Host, r.URL.Path)
		}
		_, _ = io.WriteString(w, `[{"tag_name":"v0.4.21"}]`)
	}))
	t.Cleanup(releases.Close)
	transport := releases.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = releases.Certificate().DNSNames[0]
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == "api.github.com:443" {
			address = releases.Listener.Addr().String()
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil || !net.ParseIP(host).IsLoopback() {
			return nil, errors.New("owned restart fixture refused an unrelated origin")
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	prior := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() {
		http.DefaultTransport = prior
		transport.CloseIdleConnections()
	})

	ctx := t.Context()
	dir := t.TempDir()
	open := func() *App {
		t.Helper()
		markets := []string{}
		a, err := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
		if err != nil {
			t.Fatal(err)
		}
		a.pluginOriginalLarkTestOrigin = peer.URL + "/open-apis"
		a.Start(ctx)
		return a
	}
	a := open()
	t.Cleanup(func() {
		if a != nil {
			_ = a.Close()
		}
	})
	admin := setupTestAdmin(t, a)
	installMergeSiteSwitchPackage(t, a, admin, bundle, base)
	configure := func(instance, account, secret string) {
		t.Helper()
		body := mustCleanSeedJSON(map[string]any{"enabled": true, "app_id": account, "app_secret": secret, "verification_token": "owned-restart-token", "FEISHU_ADMINS": sender})
		out := callTest(t, a.Handler(), http.MethodPut, "/api/plugins/"+instance, string(body), admin, "")
		assertOriginalRegistrySuccess(t, out.Code, out.Body.Bytes())
	}
	configure(base, "cli_restart_base", "owned-base-secret")
	out := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/"+base+"/clone", `{"suffix":"restart"}`, admin, "")
	assertOriginalRegistrySuccess(t, out.Code, out.Body.Bytes())
	configure(clone, "cli_restart_clone", "owned-clone-secret")
	originalPID := installedLarkFixturePID(t, a, clone)
	payload := mustCleanSeedJSON(map[string]any{
		"header": map[string]any{"event_type": "im.message.receive_v1"},
		"event": map[string]any{
			"sender": map[string]any{"sender_id": map[string]any{"open_id": sender}},
			"message": map[string]any{"message_id": "om_restart", "chat_id": "oc_restart", "message_type": "text",
				"content": `{"text":"/restart"}`},
		},
	})
	out = callTest(t, a.Handler(), http.MethodPost, "/api/plugins/"+clone+"/api/webhook?token=owned-restart-token", string(payload), nil, "")
	if out.Code != http.StatusOK || !strings.Contains(out.Body.String(), `"success":true`) {
		t.Fatalf("verified restart webhook returned %d: %s", out.Code, out.Body.String())
	}
	awaitCard := func(fragments ...string) {
		t.Helper()
		select {
		case body := <-cards:
			for _, text := range fragments {
				if !strings.Contains(body, text) {
					t.Fatalf("restart card lacks %q: %s", text, body)
				}
			}
		case <-time.After(8 * time.Second):
			t.Fatalf("missing restart card %q", fragments)
		}
	}
	awaitCard("系统正在重启，请耐心等候！")
	select {
	case <-a.RestartRequested:
	case <-time.After(8 * time.Second):
		t.Fatal("restart command did not notify the App supervisor")
	}
	var sealed []byte
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, originalChatRestartKind, originalChatRestartKey).Scan(&sealed); err != nil ||
		bytes.Contains(sealed, []byte(sender)) {
		t.Fatalf("sender was not durably protected before restart: %v", err)
	}
	var receipt originalChatRestartReceipt
	if err := a.readQualitySealed(ctx, a.Store.DB, originalChatRestartKind, originalChatRestartKey, &receipt); err != nil ||
		!receipt.Requested || receipt.Instance != clone || receipt.User != sender {
		t.Fatalf("pending restart did not preserve the exact initiating instance and sender: %v", err)
	}

	closeErr := a.Close()
	a = nil
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	a = open()
	awaitCard("系统已重启完成！", "当前后端版本："+appVersion, "当前前端版本："+appVersion, "远程版本：0.4.21")
	if pid := installedLarkFixturePID(t, a, clone); pid == originalPID {
		t.Fatal("restart did not start a new clone worker")
	}
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, originalChatRestartKind, originalChatRestartKey).Scan(&sealed); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("completed notice did not consume the exact restart receipt: %v", err)
	}
	select {
	case card := <-cards:
		t.Fatal("restart sent an extra card", card)
	default:
	}
	t.Log("verified /restart signaled the existing supervisor, persisted the encrypted clone sender, and delivered one versioned completion from a fresh native App")
}
