package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Fixed helper/plugin.py:1643 prefers the configured proxy and retries the
// ordinary request on HTTP/network failure. system.py:835 supplies PROXY to
// Wiki directly, without that repository fallback. No global transport changes.
func TestOriginalMarketProxyMetadataInstallFallbackAndWikiFailure(t *testing.T) {
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}
	index, binary := originalMarketOwnedIndex(t, "OWNED_STANDARD_FIXTURE_BINARY")
	var originIndexes, originPayloads, proxyGets, proxyConnects atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index" {
			originIndexes.Add(1)
			json.NewEncoder(w).Encode(index)
		} else if r.URL.Path == "/worker" {
			originPayloads.Add(1)
			w.Write(binary)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer origin.Close()
	index.Releases[0].Artifacts[0].URL = origin.URL + "/worker"
	var proxyFails atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			proxyConnects.Add(1)
			if r.Host != "raw.githubusercontent.com:443" {
				t.Error("Wiki proxy received an unrelated destination")
			}
			http.Error(w, "synthetic proxy failure body must stay private", 502)
			return
		}
		proxyGets.Add(1)
		if proxyFails.Load() {
			http.Error(w, "synthetic proxy failure body must stay private", 502)
			return
		}
		json.NewEncoder(w).Encode(index)
	}))
	defer proxy.Close()
	// An equal environment proxy must still allow the ordinary NO_PROXY
	// fallback after the explicitly configured route fails.
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "127.0.0.1")
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	settings := a.Settings()
	settings.PluginMarkets = []string{origin.URL + "/index"}
	settings.ProxyHost = proxy.URL
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	read := func() {
		t.Helper()
		response := callTest(t, a.Handler(), "GET", "/api/plugins/market?details=true&force=true", "", cookie, "")
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"plugin_id":"`+index.PluginID+`"`) {
			t.Fatal("actual proxied metadata was not returned")
		}
	}
	read()
	if originIndexes.Load() != 0 || proxyGets.Load() != 1 {
		t.Fatal("configured proxy did not supply successful metadata")
	}
	proxyFails.Store(true)
	read()
	if originIndexes.Load() != 1 || proxyGets.Load() != 2 {
		t.Fatal("repository HTTP failure did not use ordinary fallback")
	}
	input, _ := json.Marshal(map[string]any{"plugin_id": index.PluginID, "repo_url": origin.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(input), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	loaded, ok := a.pluginRunningInstances.Load(index.PluginID)
	if !ok || ownedStandardVersionRead(t, loaded.(*originalPluginInstance)).Version != "1.0" || originPayloads.Load() != 1 || proxyGets.Load() != 4 {
		t.Fatal("proxy fallback did not acquire and execute the actual Go artifact")
	}
	before := a.Settings().PluginMarkets
	response = callTest(t, a.Handler(), "POST", "/api/plugins/markets/sync-wiki", `{}`, cookie, "")
	var result struct{ Data struct{ Success bool } }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Data.Success || proxyConnects.Load() != 1 || !reflect.DeepEqual(before, a.Settings().PluginMarkets) || strings.Contains(response.Body.String(), "failure body") {
		t.Fatal("Wiki proxy failure changed settings, escaped its direct proxy policy or leaked a remote body")
	}
	response = callTest(t, a.Handler(), "POST", "/api/plugins/markets/sync-wiki", `{"wiki_url":"https://other.invalid/wiki"}`, cookie, "")
	if proxyConnects.Load() != 1 || !strings.Contains(response.Body.String(), "不支持的 Wiki") {
		t.Fatal("unapproved Wiki destination reached the network")
	}
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
