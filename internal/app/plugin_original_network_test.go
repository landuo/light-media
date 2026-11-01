package app

import (
	"io"
	"light-media/internal/networkproxy"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// The historical test name is retained, but its incorrect received502 ->
// successful origin/install expectation is retired. This finite replacement
// checks only the shared HTTP response boundary and Wiki's no-fallback route;
// it does not claim to install a worker or exercise a Wiki public endpoint.
func TestOriginalMarketProxyMetadataInstallFallbackAndWikiFailure(t *testing.T) {
	var originHits, proxyHits atomic.Int32
	origin := originalMarketProxyOwnedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits.Add(1)
		_, _ = io.WriteString(w, "unexpected origin")
	}))
	proxy := originalMarketProxyOwnedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Scheme != "http" || r.URL.Host != origin.Listener.Addr().String() || r.URL.Path != "/index" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("fixed HTTP response peer rejected unknown route")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		proxyHits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "owned received502")
	}))
	request, err := http.NewRequest(http.MethodGet, origin.URL+"/index", nil)
	if err != nil {
		t.Fatal(err)
	}
	// If a broken candidate enters its inherited fallback, that path must
	// still target only our owned origin. Do not rewrite NO_PROXY or swap a
	// route to make the test pass; fail before dispatch if env differs.
	nextProxy, err := networkproxy.RequestsProxy(request)
	if err != nil || nextProxy != nil {
		t.Fatal("owned fallback guard blocked by inherited environment; no request sent")
	}
	empty := ""
	a := &App{config: Settings{ProxyHost: proxy.URL, PluginServerURL: &empty}}
	material, err := a.originalPluginMaterialHTTPClient(3 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer material.CloseIdleConnections()
	wiki, err := a.originalPluginHTTPClient(3*time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	defer wiki.CloseIdleConnections()
	for _, client := range []*http.Client{material, wiki} {
		response, err := client.Do(request.Clone(request.Context()))
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil || response.StatusCode != 502 || string(body) != "owned received502" {
			t.Fatal("received proxy response changed", response.StatusCode, readErr)
		}
	}
	if proxyHits.Load() != 2 || originHits.Load() != 0 {
		t.Fatal("received HTTP502 entered origin fallback", proxyHits.Load(), originHits.Load())
	}
}

func originalMarketProxyOwnedHTTP(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peer := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	peer.Start()
	t.Cleanup(peer.Close)
	return peer
}

// The pinned no-PROXY_HOST strategy invokes ordinary requests, including
// ALL_PROXY and its explicit NO_PROXY behavior (no implicit localhost bypass).
func TestOriginalMarketEnvironmentOnlyAllProxyAndExplicitBypass(t *testing.T) {
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}
	var directHits, proxyHits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { directHits.Add(1); w.WriteHeader(204) }))
	defer origin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxyHits.Add(1); w.WriteHeader(204) }))
	defer proxy.Close()
	t.Setenv("ALL_PROXY", proxy.URL)
	t.Setenv("all_proxy", proxy.URL)
	a := newTestApp(t)
	fetch := func() {
		t.Helper()
		client, err := a.originalPluginHTTPClient(5*time.Second, true)
		if err != nil {
			t.Fatal(err)
		}
		defer client.CloseIdleConnections()
		response, err := client.Get(origin.URL)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 204 {
			t.Fatal("owned route did not succeed")
		}
	}
	fetch()
	if proxyHits.Load() != 1 || directHits.Load() != 0 {
		t.Fatal("ordinary source ALL_PROXY or localhost routing changed")
	}
	t.Setenv("NO_PROXY", "127.0.0.1")
	fetch()
	if proxyHits.Load() != 1 || directHits.Load() != 1 {
		t.Fatal("ordinary source explicit NO_PROXY bypass changed")
	}
}
