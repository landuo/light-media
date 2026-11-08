package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOriginalLarkProviderRedoLinuxAMD64(t *testing.T) {
	bundle := os.Getenv("SOURCE_LARK_MESSAGER_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 LarkMessager package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	t.Setenv("RECOGNIZE_SOURCE", "themoviedb")
	for _, env := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(env, "")
	}
	a, source, target := importTestApp(t)
	const title = "来源识别动画"
	var detailCalls, tmdbCalls atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Host != "api.bgm.tv" {
			t.Errorf("unexpected provider origin: %s %s", r.Method, r.Host)
		}
		switch r.URL.Path {
		case "/v0/subjects/777":
			detailCalls.Add(1)
			workflowJSON(w, map[string]any{"id": 777, "name": "Owned Source Redo", "name_cn": title,
				"date": "2024-01-01", "platform": "TV", "total_episodes": 2})
		case "/v0/subjects/777/characters":
			workflowJSON(w, []any{})
		default:
			t.Errorf("unexpected provider request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.Close)
	cert := provider.Certificate()
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: cert.DNSNames[0]}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if ip := net.ParseIP(host); err == nil && ip != nil && ip.IsLoopback() {
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}
		if err != nil || network != "tcp" || host != "api.bgm.tv" || port != "443" {
			return nil, errors.New("redo provider fixture refused unrelated origin")
		}
		return (&net.Dialer{}).DialContext(ctx, network, provider.Listener.Addr().String())
	}}
	priorTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = priorTransport; transport.CloseIdleConnections() })

	cards := make(chan string, 4)
	lark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"owned-transfer-redo"}`)
		case strings.Contains(r.URL.Path, "/reactions"):
			_, _ = io.WriteString(w, `{"code":0,"data":{"reaction_id":"rx_fixture"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/open-apis/im/v1/messages":
			var in struct{ Content string }
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			cards <- in.Content
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_redo","chat_id":"oc_redo"}}`)
		default:
			t.Errorf("unexpected Lark request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(lark.Close)
	a.pluginOriginalLarkTestOrigin = lark.URL + "/open-apis"
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tmdbCalls.Add(1)
		workflowJSON(w, map[string]any{"page": 1, "total_pages": 1, "total_results": 0, "results": []any{}})
	}))
	t.Cleanup(metadata.Close)
	installMetadataFixture(t, a, "owned-redo-tmdb", []string{"/3/search/movie", "/3/search/tv", "/3/search/multi"}, ownedLoopbackFixtureTransport(t, metadata).RoundTrip)
	a.config.DefaultRoot = "root"
	overwriteRule(a, source, target, "never", true)
	a.Start(t.Context())
	admin := setupTestAdmin(t, a)
	installMergeSiteSwitchPackage(t, a, admin, bundle, "LarkMessager")
	api := a.Handler()
	const plugin = "/api/plugins/LarkMessager"
	configured := callTest(t, api, "PUT", plugin, `{"enabled":true,"app_id":"owned-app","app_secret":"owned-secret","chat_id":"oc_redo","verification_token":"owned-verification","switchs":["手动处理"]}`, admin, "")
	assertOriginalRegistrySuccess(t, configured.Code, configured.Body.Bytes())

	input := filepath.Join(source, "无法自动识别的剧集.2024.S01E01.mkv")
	importWrite(t, input, []byte("owned fixture episode"))
	missed, err := a.manualOrganize(t.Context(), manualOrganizeRequest{Paths: []string{input}, TargetPath: target, Mode: "hardlink", Scraping: importFlag(false)})
	if err != nil || missed.Failed != 1 || len(missed.Items) != 1 || missed.Items[0].JobID == "" {
		t.Fatalf("could not produce original recognition failure: %+v, %v", missed, err)
	}
	var alias []string
	select {
	case card := <-cards:
		alias = regexp.MustCompile(`transfer_retry_([0-9]+)`).FindStringSubmatch(card)
	case <-time.After(12 * time.Second):
		t.Fatal("no failure card from installed Lark worker")
	}
	if len(alias) != 2 {
		t.Fatal("failure card lacked original redo alias")
	}
	var historyID string
	if err = a.Store.DB.QueryRowContext(t.Context(), `SELECT json_extract(payload,'$.file_ids[0]') FROM jobs WHERE id=?`, missed.Items[0].JobID).Scan(&historyID); err != nil {
		t.Fatal(err)
	}
	initialTMDB := tmdbCalls.Load()
	payload := mustCleanSeedJSON(map[string]any{
		"header": map[string]any{"event_type": "im.message.receive_v1"},
		"event": map[string]any{
			"sender": map[string]any{"sender_id": map[string]any{"open_id": "ou_owner"}},
			"message": map[string]any{"message_id": "om_redo", "chat_id": "oc_redo", "message_type": "text",
				"content": string(mustCleanSeedJSON(map[string]string{"text": "/redo " + alias[1] + " bangumi:777|电视剧"}))},
		},
	})
	result := callTest(t, api, "POST", plugin+"/api/webhook?token=owned-verification", string(payload), nil, "")
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"success":true`) {
		t.Fatalf("webhook admission: %d %s", result.Code, result.Body.String())
	}
	deadline := time.Now().Add(12 * time.Second)
	var history transferHistoryItem
	for time.Now().Before(deadline) {
		select {
		case card := <-cards:
			t.Fatalf("source /redo success should be silent: %s", card)
		default:
		}
		history, err = a.transferHistoryItem(t.Context(), historyID)
		if err == nil && history.Status == "success" && history.JobState == "succeeded" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if history.Status != "success" || history.JobState != "succeeded" || history.JobID == missed.Items[0].JobID ||
		mediaKey(history.Media) != "tv:bangumi:777" || history.Media.ID != 0 || history.Season != 1 || len(history.Episodes) != 1 || history.Episodes[0] != 1 {
		t.Fatalf("provider redo failed to retain the specified source and actual file scope: %+v, %v", history, err)
	}
	src, srcErr := os.Stat(input)
	dst, dstErr := os.Stat(history.Target)
	if srcErr != nil || dstErr != nil || !os.SameFile(src, dst) || !strings.Contains(history.Target, title) {
		t.Fatalf("provider redo did not publish its actual authorized hardlink: %v, %v", srcErr, dstErr)
	}
	if detailCalls.Load() != 1 || tmdbCalls.Load() != initialTMDB {
		t.Fatalf("expected one selected provider lookup and no extra TMDB lookup: provider=%d TMDB=%d (before %d)", detailCalls.Load(), tmdbCalls.Load(), initialTMDB)
	}
	t.Log("indexed Lark webhook /redo resolved Bangumi 777, kept the source-native history, published S01E01 as a hardlink, and made no TMDB conversion request")
}
