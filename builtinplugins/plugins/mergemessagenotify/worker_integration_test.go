package mergemessagenotify

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "light-media/builtinplugins/internal/sdk"
)

const ownedWorkerMarker = "owned-merge-message-worker"

func TestMergeMessageNotifyWorkerProcess(t *testing.T) {
	args := os.Args
	if len(args) < 4 || args[len(args)-3] != ownedWorkerMarker {
		return
	}
	address, certFile := args[len(args)-2], args[len(args)-1]
	localHost, _, err := net.SplitHostPort(address)
	if err != nil || !net.ParseIP(localHost).IsLoopback() {
		t.Fatal("worker fixture requires a loopback listener")
	}
	cert, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(cert) {
		t.Fatal("cannot load worker fixture certificate")
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{RootCAs: pool}
	// Per-request transports keep this dialer, so the fixture redirect covers
	// both ordinary and timeout-bounded channel requests.
	base.DialContext = func(ctx context.Context, network, destination string) (net.Conn, error) {
		switch destination {
		case "iyuu.cn:443", "bots.qq.com:443", "api.sgroup.qq.com:443":
			return (&net.Dialer{}).DialContext(ctx, network, address)
		default:
			return nil, errors.New("non-fixture connection blocked")
		}
	}
	http.DefaultTransport = base

	// The owned fixture configures only the IYUU and QQ Bot channels; an SMTP
	// send would mean the worker ignored the channel strategy.
	noSMTP := func(context.Context, map[string]any, SMTPRequest) error {
		return errors.New("fixture has no SMTP channel")
	}
	if err := sdk.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, Manifest(), NewWorker(noSMTP).Handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

type mergeWireRequest struct {
	host, method, path, authorization string
	query                             url.Values
	body                              string
}

func TestMergeMessageNotifyActualWorkerRouting(t *testing.T) {
	var wireMu sync.Mutex
	var requests []mergeWireRequest
	provider := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("capture fixture request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		wireMu.Lock()
		requests = append(requests, mergeWireRequest{r.Host, r.Method, r.URL.Path, r.Header.Get("Authorization"), r.URL.Query(), string(body)})
		wireMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.Host {
		case "iyuu.cn":
			switch r.URL.Query().Get("text") {
			case "测试消息":
				io.WriteString(w, "{") // Invalid JSON must still clear test_once.
			case "IYUU first":
				w.WriteHeader(http.StatusInternalServerError)
				io.WriteString(w, `{"errcode":0}`) // The original accepts nonempty JSON even on HTTP errors.
			default:
				io.WriteString(w, `{}`)
			}
		case "bots.qq.com":
			io.WriteString(w, `{"access_token":"fixture-qq-token","expires_in":"7200"}`)
		case "api.sgroup.qq.com":
			io.WriteString(w, `{"id":"fixture-message"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		DNSNames:              []string{"iyuu.cn", "bots.qq.com", "api.sgroup.qq.com"},
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	provider.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	provider.StartTLS()
	defer provider.Close()
	certFile := filepath.Join(t.TempDir(), "provider.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: provider.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	manifest := Manifest()
	binding := sdk.SessionBinding{Identity: sdk.Identity{PluginID: manifest.PluginID, InstanceID: manifest.PluginID, Version: manifest.Version}, Generation: 1}
	command := sdk.Command{Path: exe, Args: []string{"-test.run=^TestMergeMessageNotifyWorkerProcess$", "--", ownedWorkerMarker, provider.Listener.Addr().String(), certFile}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := (sdk.Runner{ConcurrentCalls: true, RequireConcurrent: true, Timeout: 5 * time.Second}).Start(ctx, command, manifest, binding)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Stop(ctx)

	config := json.RawMessage(`{
	  "enable":true,
	  "enable_channels":["custom.iyuu","custom.qqbot"],
	  "channel_strategy":"ORDER_SUCCESS_ONE",
	  "custom.iyuu.token":"fixture-iyuu-token",
	  "custom.iyuu.enable_notify_types":["Download"],
	  "custom.iyuu.test_once":true,
	  "custom.qqbot.app_id":"123456789",
	  "custom.qqbot.app_secret":"fixture-qq-secret",
	  "custom.qqbot.target_type":"group",
	  "custom.qqbot.target_openid":"fixture-group",
	  "custom.qqbot.enable_notify_types":["Download"]
	}`)
	var configMu sync.Mutex
	stored, saveOrigin, saveCount := config, false, 0
	host := func(_ context.Context, id sdk.Identity, method string, payload json.RawMessage) (json.RawMessage, error) {
		if id != binding.Identity {
			return nil, errors.New("unexpected worker identity")
		}
		configMu.Lock()
		defer configMu.Unlock()
		switch method {
		case sdk.BaseGetConfig:
			return stored, nil
		case sdk.BaseConfigSaveOrigin:
			return json.Marshal(saveOrigin)
		case sdk.BaseUpdateConfig:
			var input struct{ Config json.RawMessage }
			if err := json.Unmarshal(payload, &input); err != nil || len(input.Config) == 0 {
				return nil, errors.New("invalid stored configuration")
			}
			stored = input.Config
			saveCount++
			return json.RawMessage(`true`), nil
		case sdk.NetworkProxyReadCapability:
			return json.RawMessage(`{"schema_version":1,"environment":[]}`), nil
		case sdk.LogWriteCapability:
			if strings.Contains(string(payload), "fixture-iyuu-token") || strings.Contains(string(payload), "fixture-qq-secret") || strings.Contains(string(payload), "fixture-qq-token") {
				t.Error("worker included fixture credentials in its log")
			}
			return json.RawMessage(`null`), nil
		default:
			return nil, fmt.Errorf("unexpected host operation %q", method)
		}
	}
	call := func(method string, payload json.RawMessage) json.RawMessage {
		t.Helper()
		res, err := client.Call(ctx, method, payload, host)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		return res.Result
	}
	checkRequests := func(count int) []mergeWireRequest {
		t.Helper()
		wireMu.Lock()
		defer wireMu.Unlock()
		if len(requests) != count {
			t.Fatalf("provider received %d requests, want %d: %+v", len(requests), count, requests)
		}
		return slices.Clone(requests)
	}

	call("init", config)
	checkRequests(0)
	var form []json.RawMessage
	if err := json.Unmarshal(call("get_form", nil), &form); err != nil || len(form) != 2 {
		t.Fatalf("worker did not return a Vuetify form pair: %v", err)
	}
	var elements []any
	if err := json.Unmarshal(form[0], &elements); err != nil {
		t.Fatal(err)
	}
	channels := mergeFormChannelValues(elements)
	slices.Sort(channels)
	for _, want := range []string{"custom.iyuu", "custom.qqbot"} {
		if !slices.Contains(channels, want) {
			t.Fatalf("enabled-channel choices lack %s: %v", want, channels)
		}
	}
	if raw := call("get_state", nil); string(raw) != "true" {
		t.Fatalf("worker state = %s", raw)
	}

	configMu.Lock()
	saveOrigin = true
	// The form save above already persisted channel defaults; count only the
	// writes caused by this save-request initialization.
	base := saveCount
	configMu.Unlock()
	call("init", config)
	trial := checkRequests(1)[0]
	if trial.host != "iyuu.cn" || trial.path != "/fixture-iyuu-token.send" || trial.method != http.MethodPost ||
		trial.query.Get("text") != "测试消息" || trial.query.Get("desp") != "这是一条测试消息，您收到此消息表示您的渠道配置无误。" || trial.body != "" {
		t.Fatalf("unexpected IYUU test-once request: %+v", trial)
	}
	configMu.Lock()
	saveOrigin = false
	var saved map[string]json.RawMessage
	err = json.Unmarshal(stored, &saved)
	updates := saveCount - base
	configMu.Unlock()
	if err != nil || updates != 1 || string(saved["custom.iyuu.test_once"]) != "false" {
		t.Fatalf("one-shot configuration did not reset after the failed trial: updates=%d reset=%s err=%v", updates, saved["custom.iyuu.test_once"], err)
	}
	call("init", config) // Retained true flags do not replay on ordinary initialization.
	checkRequests(1)

	call("listen_notice_message", json.RawMessage(`{"type":"notice.message","data":{"title":"ignored","mtype":"整理入库"}}`))
	call("listen_notice_message", json.RawMessage(`{"type":"notice.message","data":{"title":"ignored","mtype":"资源下载","channel":"system"}}`))
	checkRequests(1)

	call("listen_notice_message", json.RawMessage(`{"type":"notice.message","data":{"title":"IYUU first","mtype":"资源下载"}}`))
	first := checkRequests(2)[1]
	if first.host != "iyuu.cn" || first.query.Get("desp") != "IYUU first" {
		t.Fatalf("IYUU body fallback or ordered routing mismatch: %+v", first)
	}
	call("listen_notice_message", json.RawMessage(`{"type":"notice.message","data":{"title":"QQ fallback","text":"fixture body","mtype":"资源下载"}}`))
	got := checkRequests(5)
	if got[2].host != "iyuu.cn" || got[2].query.Get("desp") != "fixture body" {
		t.Fatalf("expected the IYUU attempt first: %+v", got[2])
	}
	auth, send := got[3], got[4]
	var authBody struct{ AppID, ClientSecret string }
	if auth.host != "bots.qq.com" || auth.method != http.MethodPost || auth.path != "/app/getAppAccessToken" ||
		json.Unmarshal([]byte(auth.body), &authBody) != nil || authBody.AppID != "123456789" || authBody.ClientSecret != "fixture-qq-secret" {
		t.Fatalf("unexpected QQ token request: %+v", auth)
	}
	var msg struct {
		Content string `json:"content"`
		MsgType *int   `json:"msg_type"`
	}
	if send.host != "api.sgroup.qq.com" || send.method != http.MethodPost || send.path != "/v2/groups/fixture-group/messages" ||
		send.authorization != "QQBot fixture-qq-token" || json.Unmarshal([]byte(send.body), &msg) != nil ||
		msg.MsgType == nil || *msg.MsgType != 0 || !strings.Contains(msg.Content, "QQ fallback") || !strings.Contains(msg.Content, "fixture body") {
		t.Fatalf("unexpected QQ message request: %+v", send)
	}
	if err := client.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func mergeFormChannelValues(elements []any) []string {
	for _, element := range elements {
		node, _ := element.(map[string]any)
		props, _ := node["props"].(map[string]any)
		if props["model"] == "enable_channels" {
			items, _ := props["items"].([]any)
			var values []string
			for _, item := range items {
				choice, _ := item.(map[string]any)
				if value, ok := choice["value"].(string); ok {
					values = append(values, value)
				}
			}
			return values
		}
		if children, ok := node["content"].([]any); ok {
			if values := mergeFormChannelValues(children); values != nil {
				return values
			}
		}
	}
	return nil
}
