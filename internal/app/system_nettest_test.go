package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSystemNetTestRoutes(t *testing.T) {
	a := newTestApp(t)
	h := a.Handler()
	list := "/api/system/nettest/targets"
	probe := "/api/system/nettest"
	cookie := setupTestAdmin(t, a)
	for _, path := range []string{list, probe + "?target_id=github_api"} {
		if w := callTest(t, h, "GET", path, "", nil, ""); w.Code != http.StatusUnauthorized {
			t.Fatal("anonymous endpoint", w.Code)
		}
	}
	var envelope struct {
		Data struct{ Targets []map[string]string }
	}
	w := callTest(t, h, "GET", list, "", cookie, "")
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || w.Code != 200 || len(envelope.Data.Targets) != 14 {
		t.Fatal("target list", w.Code, err)
	}
	for i, row := range envelope.Data.Targets {
		if len(row) != 3 || row["name"] != systemNetTestTargets[i].Name || row["id"] != systemNetTestTargets[i].ID || row["icon"] == "" {
			t.Fatal("unexpected public target", row)
		}
	}
	if w := callTest(t, h, "GET", probe+"?url=https://unlisted.invalid/", "", cookie, ""); w.Code != 400 {
		t.Fatal("unknown target", w.Code)
	}

	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodConnect || r.Host != "api.themoviedb.org:443" {
			t.Errorf("unexpected proxy request %s %s", r.Method, r.Host)
		}
		http.Error(w, "test proxy rejected tunnel", http.StatusBadGateway)
	}))
	defer proxy.Close()
	settings := a.Settings()
	settings.ProxyHost = proxy.URL
	settings.TMDB.APIKey = "fixture-tmdb-secret"
	a.settingsCache.Store(&settings)
	w = callTest(t, h, "GET", probe+"?target_id=tmdb_api", "", cookie, "")
	var outcome struct{ Data systemNetTestResult }
	if err := json.Unmarshal(w.Body.Bytes(), &outcome); err != nil || w.Code != 200 || calls.Load() != 1 ||
		outcome.Data.Success || outcome.Data.Message != "网络代理未建立连接隧道（HTTP 502）" || strings.Contains(w.Body.String(), settings.TMDB.APIKey) {
		t.Fatal("probe failed to report proxy failure safely", w.Code, calls.Load(), outcome.Data)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if !a.logs.flush(ctx) {
		t.Fatal("log flush timed out")
	}
	entries, _, _, _ := a.logs.snapshot(0)
	for _, entry := range entries {
		var record struct{ Event, Message, Level string }
		if err := json.Unmarshal(entry.Data, &record); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(entry.Data), settings.TMDB.APIKey) {
			t.Fatal("log exposed credentials")
		}
		if record.Event == "network_test" && record.Level == "WARN" && strings.Contains(record.Message, "api.themoviedb.org") &&
			strings.Contains(record.Message, outcome.Data.Message) && strings.Contains(record.Message, "ms") {
			return
		}
	}
	t.Fatal("missing per-target failure log")
}

func TestSystemNetTestResponseAndCredentials(t *testing.T) {
	for _, tc := range []struct {
		target, key, token, mirror, expectedHeader, expectedHost string
		code                                                     int
		body, want                                               string
	}{
		{target: "tmdb_api", key: "fixture-key", expectedHost: "api.themoviedb.org", code: 200, body: `{"id":550}`},
		{target: "tmdb_api_alt", token: "fixture-token", expectedHeader: "Bearer fixture-token", expectedHost: "api.tmdb.org", code: 200, body: `{"id":123}`, want: "预期的 TMDB 影片"},
		{target: "tmdb_api", expectedHost: "api.themoviedb.org", code: 401, want: "HTTP 401，未配置 TMDB 凭据"},
		{target: "github_api", expectedHeader: "Bearer fixture-github", expectedHost: "api.github.com", code: 429, want: "HTTP 429，请求过于频繁"},
		{target: "github_proxy_web", expectedHeader: "Bearer fixture-github", expectedHost: "github.com", code: 200, body: "<main>MoviePilot</main>"},
		{target: "github_proxy_raw", mirror: "https://mirror.example", expectedHost: "mirror.example", code: 200, body: "MoviePilot README"},
		{target: "github_proxy_web", mirror: "https://mirror.example", expectedHost: "mirror.example", code: 200, body: "<html>mirrored login</html>", want: "加速代理未返回预期"},
		{target: "pip_proxy", expectedHost: "pypi.org", code: 200, body: "PyPI:Repository-Version"},
		{target: "pip_proxy", expectedHost: "pypi.org", code: 200, body: "blocked", want: "内容与预期不符"},
	} {
		target := systemNetTestTargets[slices.IndexFunc(systemNetTestTargets, func(t systemNetTestTarget) bool { return t.ID == tc.target })]
		transport := imageTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != tc.expectedHost || r.URL.Query().Get("api_key") != tc.key || r.Header.Get("Authorization") != tc.expectedHeader {
				t.Fatalf("credential or origin mismatch on %s", tc.target)
			}
			if tc.target == "github_proxy_web" && !strings.HasSuffix(r.URL.Path, "/jxxghp/MoviePilot/blob/v2/README.md") {
				t.Fatal("GitHub web probe did not request the project README")
			}
			return &http.Response{StatusCode: tc.code, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})
		settings := Settings{TMDB: TMDBConfig{APIKey: tc.key, AccessToken: tc.token}, GitHubToken: "fixture-github", GitHubProxy: tc.mirror}
		got := target.probe(context.Background(), settings, transport)
		if tc.want == "" && got != "" || !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.target, got, tc.want)
		}
	}
}

func TestSystemNetTestConnectionErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, "连接超时"},
		{&net.DNSError{Err: "fixture", Name: "fixture.example"}, "域名解析失败"},
		{&tls.CertificateVerificationError{Err: errors.New("fixture verification failed")}, "TLS 证书验证失败"},
		{&net.OpError{Op: "proxyconnect", Err: errors.New("remote text")}, "无法连接网络代理"},
	} {
		if got := netTestConnectionError(&url.Error{Op: "Get", URL: "https://fixture.example/?api_key=fixture", Err: tc.err}); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
}

func TestSystemNetTestRedirectScopeAndCancellation(t *testing.T) {
	target := systemNetTestTargets[0]
	for _, tc := range []struct {
		location, want string
		calls          int
	}{
		{"https://api.themoviedb.org/3/movie/redirected", "", 2},
		{"https://api.themoviedb.org.evil.example/3/", "未授权跳转", 1},
		{"http://api.themoviedb.org/3/", "未授权跳转", 1},
		{"https://api.themoviedb.org:8443/3/", "未授权跳转", 1},
		{"https://api.themoviedb.org/3/%2e%2e/outside", "未授权跳转", 1},
		{"/redirect", "未授权跳转", 1},
		{"/3/loop", "超过 3 次", 4},
	} {
		calls := 0
		message := target.probe(context.Background(), Settings{TMDB: TMDBConfig{APIKey: "fixture-key"}}, imageTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Header.Get("Referer") != "" {
				t.Fatal("referer sent on redirect")
			}
			resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":550}`))}
			if calls == 1 || tc.location == "/3/loop" {
				resp.StatusCode = 302
				resp.Header.Set("Location", tc.location)
			}
			return resp, nil
		}))
		if calls != tc.calls || tc.want == "" && message != "" || !strings.Contains(message, tc.want) {
			t.Errorf("redirect %s: got %q / %d calls, want %q / %d", tc.location, message, calls, tc.want, tc.calls)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	message := target.probe(ctx, Settings{}, imageTransport(func(r *http.Request) (*http.Response, error) {
		cancel()
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	if message != "测试已取消" {
		t.Fatal(message)
	}
	for _, target := range systemNetTestTargets {
		transport, err := target.transport(Settings{ProxyHost: "https://proxy.example"})
		if err != nil {
			t.Fatal(err)
		}
		if target.direct && transport.Proxy != nil || !target.direct && transport.Proxy == nil {
			t.Fatalf("incorrect proxy route for %s", target.ID)
		}
		if !target.direct {
			proxy, err := transport.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: target.Name}})
			if err != nil || proxy.Host != "proxy.example" {
				t.Fatalf("%s: proxy selection failed", target.ID)
			}
		}
		transport.CloseIdleConnections()
	}
}
