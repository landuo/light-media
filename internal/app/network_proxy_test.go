package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/sites"
)

func withoutProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		t.Setenv(k, "")
	}
}

func TestProxySettingsAuthenticatedProjectionEncryptedRetentionAndClear(t *testing.T) {
	withoutProxyEnv(t)
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	request := func(body map[string]any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		w := callTest(t, a.Handler(), "PUT", "/api/settings", string(raw), cookie, "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if body["clear_proxy_host"] != true {
			if host, _ := body["proxy_host"].(string); host != "" && (!strings.Contains(w.Body.String(), "owned-proxy-password") || !strings.Contains(w.Body.String(), "owned-proxy-user")) {
				t.Fatal("proxy auth not echoed")
			}
		}
		var result struct{ Data map[string]any }
		if json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal("invalid public settings")
		}
		return result.Data
	}
	public := request(map[string]any{"proxy_host": " http://owned-proxy-user:owned-proxy-password@127.0.0.1:7890 "})
	if public["proxy_endpoint"] != "http://127.0.0.1:7890" || public["secret_state"].(map[string]any)["proxy_host"] != true {
		t.Fatal("proxy state missing")
	}
	delete(public, "secret_state")
	request(public)
	if a.Settings().ProxyHost != "http://owned-proxy-user:owned-proxy-password@127.0.0.1:7890" {
		t.Fatal("unrelated section lost proxy credential")
	}
	for _, secret := range []string{"owned-proxy-user", "owned-proxy-password"} {
		if strings.Contains(a.clean("failed proxy "+secret), secret) {
			t.Fatal("proxy log leak")
		}
	}
	var sealed []byte
	if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='config' AND id='main'`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "owned-proxy-") {
		t.Fatal("proxy persisted unencrypted")
	}
	var restored Settings
	if err := a.Store.Unseal(sealed, &restored); err != nil || restored.ProxyHost != a.Settings().ProxyHost {
		t.Fatal("proxy encrypted restore failed", err)
	}
	public["clear_proxy_host"] = true
	request(public)
	if a.Settings().ProxyHost != "" || a.Settings().ClearProxyHost {
		t.Fatal("clear command not consumed")
	}
	w := callTest(t, a.Handler(), "PUT", "/api/settings", `{"proxy_host":"ftp://127.0.0.1"}`, cookie, "")
	if w.Code == 200 || a.Settings().ProxyHost != "" {
		t.Fatal("invalid proxy saved")
	}
}

func TestProxyActualCatalogCONNECTAndRouteReplacement(t *testing.T) {
	withoutProxyEnv(t)
	a := newTestApp(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "CONNECT" || r.Host != "api.themoviedb.org:443" || r.Header.Get("Proxy-Authorization") == "" {
			t.Error("wrong catalog proxy binding")
		}
		http.Error(w, "owned proxy refuses tunnel", http.StatusProxyAuthRequired)
	}))
	defer server.Close()
	a.cfgMu.Lock()
	a.config.TMDB = TMDBConfig{APIKey: "owned_tmdb_key"}
	a.config.ProxyHost = strings.Replace(server.URL, "://", "://owned-proxy-user:owned-proxy-password@", 1)
	a.cfgMu.Unlock()
	first := a.catalog()
	_, err := first.Search(context.Background(), "Owned movie", "movie", 1)
	if err == nil || calls.Load() != 1 {
		t.Fatal("catalog did not use actual proxy", err, calls.Load())
	}
	a.cfgMu.Lock()
	a.config.ProxyHost = ""
	a.cfgMu.Unlock()
	if a.catalog() == first {
		t.Fatal("proxy change did not invalidate catalog transport")
	}
}

func TestProxyActualSiteFlagAndChangeGuard(t *testing.T) {
	withoutProxyEnv(t)
	a := newTestApp(t)
	var direct, proxied atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		direct.Add(1)
		http.Error(w, "owned direct endpoint", 403)
	}))
	defer origin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		if !strings.HasPrefix(r.URL.String(), origin.URL) || r.Header.Get("Cookie") != "owned-site-cookie" {
			t.Error("proxy site scope changed")
		}
		http.Error(w, "owned proxied endpoint", 403)
	}))
	defer proxy.Close()
	site := SiteConfig{Config: sites.Config{ID: "owned-proxy-site", Kind: "ubits", Name: "Owned", BaseURL: origin.URL, Cookie: "owned-site-cookie"}, Enabled: true}
	a.cfgMu.Lock()
	a.config.Sites = []SiteConfig{site}
	a.config.ProxyHost = proxy.URL
	a.cfgMu.Unlock()
	run := func(site SiteConfig) {
		t.Helper()
		_ = a.withSite(context.Background(), site, false, func(client *sites.Client) error {
			_, err := client.Search(context.Background(), "Owned", 1)
			return err
		})
	}
	run(site)
	if direct.Load() != 1 || proxied.Load() != 0 {
		t.Fatal("unchecked site routed through explicit global proxy")
	}
	// The earlier 403 creates a cooldown; this test isolates only transport selection.
	a.Store.DB.Exec(`DELETE FROM records WHERE kind='site_cooldown'`)
	site.Proxy = true
	a.cfgMu.Lock()
	a.config.Sites = []SiteConfig{site}
	a.cfgMu.Unlock()
	run(site)
	if direct.Load() != 1 || proxied.Load() != 1 {
		t.Fatal("selected site did not use configured proxy")
	}
	a.Store.DB.Exec(`DELETE FROM records WHERE kind='site_cooldown'`)
	_ = a.withSite(context.Background(), site, false, func(client *sites.Client) error {
		a.cfgMu.Lock()
		a.config.ProxyHost = "http://127.0.0.1:1"
		a.cfgMu.Unlock()
		_, err := client.Search(context.Background(), "Owned", 1)
		return err
	})
	if proxied.Load() != 1 {
		t.Fatal("stale route used after global proxy changed")
	}
}

func TestProxyMetadataImageUsesActualRoute(t *testing.T) {
	withoutProxyEnv(t)
	a := newTestApp(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Host != "owned-image.invalid" {
			t.Error("wrong metadata origin")
		}
		io.WriteString(w, "owned image")
	}))
	defer server.Close()
	a.cfgMu.Lock()
	a.config.ProxyHost = server.URL
	a.cfgMu.Unlock()
	req, _ := http.NewRequest("GET", "http://owned-image.invalid/poster.jpg", nil)
	response, err := a.metadataImageRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "owned image" || calls.Load() != 1 {
		t.Fatal("image route missing")
	}
}
