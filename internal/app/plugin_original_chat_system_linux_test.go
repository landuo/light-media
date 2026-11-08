package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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
	"sync/atomic"
	"testing"
	"time"
)

func TestOriginalLarkSystemCommandsLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	for _, env := range []string{"MP_SERVER_HOST", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(env, "")
	}
	const localFrontend = "0.3.17"
	major, _, _ := strings.Cut(appVersion, ".")
	releasePrefix := "v" + major
	remoteVersion := major + ".811.10"
	var releaseReads atomic.Int32
	github := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Host != "api.github.com" || r.URL.RequestURI() != "/repos/landuo/light-media/releases" {
			t.Errorf("unexpected release request: %s %s %s", r.Method, r.Host, r.URL.RequestURI())
			http.NotFound(w, r)
			return
		}
		releaseReads.Add(1)
		workflowJSON(w, []map[string]string{
			{"tag_name": "v999.999.999"},
			{"tag_name": releasePrefix + ".811.10"},
			{"tag_name": releasePrefix + ".811.9"},
		})
	}))
	t.Cleanup(github.Close)
	cert := github.Certificate()
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	dialer := &net.Dialer{}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: cert.DNSNames[0]},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if ip := net.ParseIP(host); err == nil && ip != nil && ip.IsLoopback() {
				return dialer.DialContext(ctx, network, address)
			}
			if err != nil || network != "tcp" || host != "api.github.com" || port != "443" {
				return nil, errors.New("system command fixture refused unrelated origin")
			}
			return dialer.DialContext(ctx, network, github.Listener.Addr().String())
		},
	}
	priorTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = priorTransport; transport.CloseIdleConnections() })

	cards := make(chan string, 4)
	lark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-system-fixture"}`)
		case strings.Contains(r.URL.Path, "/reactions"):
			_, _ = io.WriteString(w, `{"code":0,"data":{"reaction_id":"rx_fixture"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/open-apis/im/v1/messages":
			var message struct {
				Kind    string `json:"msg_type"`
				Target  string `json:"receive_id"`
				Content string `json:"content"`
			}
			var card struct {
				Header struct {
					Title struct{ Content string } `json:"title"`
				} `json:"header"`
			}
			if json.NewDecoder(r.Body).Decode(&message) != nil || json.Unmarshal([]byte(message.Content), &card) != nil ||
				message.Kind != "interactive" || message.Target != "ou_owner" || r.URL.Query().Get("receive_id_type") != "open_id" {
				t.Error("unexpected Lark system command response")
			}
			select {
			case cards <- card.Header.Title.Content:
			default:
				t.Error("unexpected extra Lark system card")
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_system","chat_id":"oc_system"}}`)
		default:
			t.Errorf("unexpected Lark request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(lark.Close)
	markets := []string{}
	a, err := New(Options{DataDir: t.TempDir(), FrontendVersion: localFrontend, PluginMarketsOverride: &markets})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	a.pluginOriginalLarkTestOrigin = lark.URL + "/open-apis"
	ctx := t.Context()
	const cacheKey = "owned-system-cache"
	var generic string
	if _, err := a.cached(ctx, cacheKey, time.Hour, &generic, func() (any, error) { return "owned cache", nil }); err != nil || generic != "owned cache" {
		t.Fatal("could not seed generic cache", err)
	}
	if err := a.originalHostMediaCachePut(ctx, cacheKey, map[string]any{"id": json.Number("777"), "media_type": "电影", "title": "Owned recognition"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveOriginalHostMediaCache(ctx); err != nil {
		t.Fatal(err)
	}
	var persistedCache json.RawMessage
	if err := a.Store.Get(ctx, "plugin_host_tmdb_cache", "main", &persistedCache); err != nil || !strings.Contains(string(persistedCache), cacheKey) {
		t.Fatal("could not seed persisted recognition cache", err)
	}
	provider, err := a.originalHostMediaProviderCached(ctx, "bangumi", "detail", cacheKey, false, func() (any, error) { return "owned provider cache", nil })
	if err != nil || provider != "owned provider cache" {
		t.Fatal("could not seed provider cache", err)
	}
	const preservedID = "notice_owned_system_fixture"
	if err := a.saveProtectedNotification(ctx, Notification{ID: preservedID, Title: "Owned persistent notice", CreatedAt: now()}); err != nil {
		t.Fatal(err)
	}

	admin := setupTestAdmin(t, a)
	a.Start(ctx)
	installMergeSiteSwitchPackage(t, a, admin, bundle, "LarkMessager")
	api := a.Handler()
	const plugin = "/api/plugins/LarkMessager"
	configured := callTest(t, api, http.MethodPut, plugin, `{"enabled":true,"app_id":"owned-app","app_secret":"owned-secret","chat_id":"oc_system","verification_token":"owned-verification"}`, admin, "")
	assertOriginalRegistrySuccess(t, configured.Code, configured.Body.Bytes())
	send := func(messageID, command, expectedTitle string) {
		t.Helper()
		payload := mustCleanSeedJSON(map[string]any{
			"header": map[string]any{"event_type": "im.message.receive_v1"},
			"event": map[string]any{
				"sender": map[string]any{"sender_id": map[string]any{"open_id": "ou_owner"}},
				"message": map[string]any{"message_id": messageID, "chat_id": "oc_system", "message_type": "text",
					"content": string(mustCleanSeedJSON(map[string]string{"text": command}))},
			},
		})
		result := callTest(t, api, http.MethodPost, plugin+"/api/webhook?token=owned-verification", string(payload), nil, "")
		if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"success":true`) {
			t.Fatalf("webhook admission: %d %s", result.Code, result.Body.String())
		}
		select {
		case title := <-cards:
			if title != expectedTitle {
				t.Fatalf("unexpected %s card title: %q", command, title)
			}
		case <-time.After(6 * time.Second):
			t.Fatalf("no card for %s", command)
		}
	}

	send("om_owned_version", "/version", "当前后端版本："+appVersion+"，远程版本："+remoteVersion+"\n当前前端版本："+localFrontend+"，远程版本："+remoteVersion)
	if got := releaseReads.Load(); got != 1 {
		t.Fatalf("version command made %d release requests; want one for the bundled release", got)
	}
	send("om_owned_clear_cache", "/clear_cache", "缓存清理完成！")
	if err := a.Store.Get(ctx, "cache", cacheKey, &persistedCache); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("clear_cache retained the generic metadata record", err)
	}
	if err := a.Store.Get(ctx, "plugin_host_tmdb_cache", "main", &persistedCache); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("clear_cache retained persisted recognition cache", err)
	}
	if value, err := a.originalHostMediaCacheGet(ctx, cacheKey); err != nil || value != nil {
		t.Fatal("clear_cache retained in-memory recognition data", value, err)
	}
	provider, err = a.originalHostMediaProviderCached(ctx, "bangumi", "detail", cacheKey, false, func() (any, error) { return "refetched after clear", nil })
	if err != nil || provider != "refetched after clear" {
		t.Fatal("clear_cache retained the provider result", provider, err)
	}
	var kept notificationRecord
	if err := a.Store.Get(ctx, "notification", preservedID, &kept); err != nil {
		t.Fatal("clear_cache removed a persisted non-cache record", err)
	}
	notification, err := a.openNotification(kept)
	if err != nil || notification.Title != "Owned persistent notice" {
		t.Fatal("clear_cache changed a persisted non-cache record", err)
	}
	var noticeCount int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM records WHERE kind='notification'`).Scan(&noticeCount); err != nil || noticeCount != 1 {
		t.Fatal("system command responses should not be added to notification history", noticeCount, err)
	}
	t.Log("indexed Lark webhook returned both exact version lines from one isolated GitHub release request and cleared persistent and in-memory metadata caches without changing notification history")
}
