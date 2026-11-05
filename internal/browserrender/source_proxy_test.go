package browserrender

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourceProxyIPv6PreservesConfiguredProxyBoundary(t *testing.T) {
	path := os.Getenv("BROWSER_PROXY_SEMANTICS_SOURCE_ORACLE")
	if path == "" {
		path = "../../testdata/plugin-platform-final-20261104/browser-ocr/cloakbrowser-proxy-semantics-source-oracle.json"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Case  string `json:"case"`
			Proxy struct {
				Server string `json:"server"`
			} `json:"source_proxy_server"`
			Normalized struct {
				Result struct {
					Server string `json:"server"`
				} `json:"result"`
			} `json:"actual_playwright_normalizer"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	var server string
	for _, row := range oracle.Cases {
		if row.Case == "explicit-ipv6" {
			if row.Normalized.Result.Server != "http://http" {
				t.Fatal("paired native wrong-target normalization not established", row.Normalized.Result.Server)
			}
			server = row.Proxy.Server
		}
	}
	if server == "" {
		t.Fatal("fixed source IPv6 case missing")
	}
	configuration := ownedBrowser(t)
	directory := configuration.TempDir
	html, err := configuration.Render(context.Background(), Request{URL: "http://owned.invalid/page", Headless: true, SourceProxy: &ProxySettings{Server: server}})
	var failed *Error
	if html != nil || !errors.As(err, &failed) || failed.Code != "browser_proxy_invalid" {
		t.Fatalf("source malformed native proxy unexpectedly repaired: %v %v", html, err)
	}
	entries, err := filepath.Glob(filepath.Join(directory, "*"))
	if err != nil || len(entries) != 0 {
		t.Fatal("failed native launch left profile", entries, err)
	}
	if strings.Contains(server, "[") {
		t.Fatal("oracle accidentally repaired source IPv6")
	}
}

// Paired wrapper auth resolution returns only --proxy-server args. The paired
// Chromium launcher adds <-loopback> only for an options.proxy dictionary.
func TestSourceProxyNativeLoopbackFollowsPairedLaunchMode(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("actual pinned Linux amd64 inline-auth behavior")
	}
	path := os.Getenv("BROWSER_PROXY_SEMANTICS_SOURCE_ORACLE")
	if path == "" {
		path = "../../testdata/plugin-platform-final-20261104/browser-ocr/cloakbrowser-proxy-semantics-source-oracle.json"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "options.proxyOverride || options.proxy") || !strings.Contains(string(raw), "owned%2540name:synthetic%253Avalue") {
		t.Fatal("actual paired launch mode evidence missing")
	}
	for _, auth := range []bool{false, true} {
		t.Run(fmt.Sprint("auth-", auth), func(t *testing.T) {
			var calls atomic.Int32
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				fmt.Fprint(w, `<!DOCTYPE html><body><script>document.body.dataset.route='proxy';</script>`)
			}))
			defer proxy.Close()
			site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, `<!DOCTYPE html><body><script>document.body.dataset.route='direct';</script>`)
			}))
			defer site.Close()
			configuration := ownedBrowser(t)
			p := &ProxySettings{Server: proxy.URL}
			want := "proxy"
			if auth {
				p.Username = "owned%40name"
				p.Password = "synthetic%3Avalue"
				want = "direct"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
			defer cancel()
			html, err := configuration.Render(ctx, Request{URL: site.URL, Headless: true, Timeout: 3 * time.Second, SourceProxy: p})
			if err != nil || html == nil || !strings.Contains(*html, `data-route="`+want+`"`) {
				t.Fatalf("source native proxy mode changed: %v %v", html, err)
			}
			if auth && calls.Load() != 0 || !auth && calls.Load() == 0 {
				t.Fatal("source implicit loopback bypass changed", auth, calls.Load())
			}
			entries, err := filepath.Glob(filepath.Join(configuration.TempDir, "*"))
			if err != nil || len(entries) != 0 {
				t.Fatal("proxy mode left profile", entries, err)
			}
		})
	}
}

func TestSourceProxyNativePasswordOnlyUsesPairedCredentialMode(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("paired runtime is Linux amd64")
	}
	path := os.Getenv("BROWSER_PROXY_SEMANTICS_SOURCE_ORACLE")
	if path == "" {
		path = "../../testdata/plugin-platform-final-20261104/browser-ocr/cloakbrowser-proxy-semantics-source-oracle.json"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Case   string            `json:"case"`
			Proxy  ProxySettings     `json:"source_proxy_server"`
			Launch []json.RawMessage `json:"actual_paired_launch_proxy"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	for _, row := range oracle.Cases {
		if row.Case == "explicit-password-only" {
			var opts map[string]any
			if err = json.Unmarshal(row.Launch[0], &opts); err != nil {
				t.Fatal(err)
			}
			if opts["proxy"] == nil || row.Proxy.Username != "" || row.Proxy.Password == "" {
				t.Fatal("actual paired password-only options missing")
			}
			if row.Proxy.inlineOnNativeRuntime() {
				t.Fatal("password-only source does not use inline credentials")
			}
			got, err := row.Proxy.nativeURL()
			if err != nil || got != row.Proxy.Server {
				t.Fatal("source password without username must not become native credentials", got, err)
			}
			return
		}
	}
	t.Fatal("actual paired password-only source case missing")
}

func TestSourceProxySocks5hRetainsPinnedNativeUnsupportedScheme(t *testing.T) {
	path := os.Getenv("BROWSER_PROXY_SCHEME_SOURCE_PROBE_LOG")
	if path == "" {
		path = "../../testdata/plugin-platform-final-20261104/browser-ocr/paired-native-socks-scheme-source-probe.log"
	}
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "scheme=socks5 proxy_handshakes=5") || !strings.Contains(string(source), "scheme=socks5h proxy_handshakes=0 navigate_error=page load error net::ERR_NO_SUPPORTED_PROXIES") {
		t.Fatal("actual pinned native source scheme probe missing")
	}
	configuration := ownedBrowser(t)
	html, err := configuration.Render(context.Background(), Request{URL: "http://owned.invalid/page", Headless: true, SourceProxy: &ProxySettings{Server: "socks5h://127.0.0.1:1", Username: "owned", Password: "synthetic"}})
	var failed *Error
	if html != nil || !errors.As(err, &failed) || failed.Code != "browser_proxy_invalid" {
		t.Fatalf("source unsupported native scheme was repaired: %v %v", html, err)
	}
	entries, err := filepath.Glob(filepath.Join(configuration.TempDir, "*"))
	if err != nil || len(entries) != 0 {
		t.Fatal("unsupported scheme left profile", entries, err)
	}
}
