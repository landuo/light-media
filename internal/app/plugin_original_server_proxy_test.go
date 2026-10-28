package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestOriginalPluginServerConfiguredProxyAndSameOriginRedirect(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	var centerHits, proxyHits atomic.Int32
	var broken atomic.Bool
	center := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		centerHits.Add(1)
		if r.Header.Get("Accept") != "*/*" || r.Header.Get("X-MoviePilot-User-Uid") == "" || r.Header.Get("User-Agent") != originalPluginServerUserAgent() {
			t.Error("fixed source request header contract differs")
		}
		if r.URL.Path == "/prefix/plugin/statistic" {
			http.Redirect(w, r, "/prefix/actual-statistic", http.StatusFound)
			return
		}
		if r.URL.Path == "/prefix/actual-statistic" {
			_ = json.NewEncoder(w).Encode(map[string]int{"Owned": 7})
			return
		}
		if r.URL.Path == "/prefix/plugin/rating" {
			if r.URL.Query().Get("plugin_ids") != "Last" {
				t.Error("scalar source query did not keep last value")
			}
			_, _ = w.Write([]byte(`{}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer center.Close()
	direct := &http.Transport{Proxy: nil}
	defer direct.CloseIdleConnections()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits.Add(1)
		if broken.Load() {
			http.Error(w, "owned failed explicit proxy", 503)
			return
		}
		if r.URL.Scheme != "http" || "http://"+r.URL.Host != center.URL {
			http.Error(w, "owned proxy rejects other destinations", 403)
			return
		}
		request := r.Clone(r.Context())
		request.RequestURI = ""
		request.Header = request.Header.Clone()
		request.Header.Del("Proxy-Connection")
		response, err := direct.RoundTrip(request)
		if err != nil {
			http.Error(w, "owned proxy forward failed", 502)
			return
		}
		defer response.Body.Close()
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	defer proxy.Close()
	settings := a.Settings()
	settings.PluginServerURL = originalServerURLPointer(center.URL + "/prefix")
	settings.ProxyHost = proxy.URL
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), "GET", "/api/plugins/statistic", "", cookie, "")
	var output struct{ Data map[string]int }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &output) != nil || output.Data["Owned"] != 7 || centerHits.Load() != 2 || proxyHits.Load() != 2 {
		t.Fatal("configured source proxy/same-origin redirect not used", response.Code, centerHits.Load(), proxyHits.Load())
	}
	response = callTest(t, a.Handler(), "GET", "/api/plugins/rating?plugin_ids=First&plugin_ids=Last", "", cookie, "")
	if response.Code != 200 {
		t.Fatal("source scalar rating query failed")
	}
	before := centerHits.Load()
	proxyBefore := proxyHits.Load()
	broken.Store(true)
	response = callTest(t, a.Handler(), "GET", "/api/plugins/statistic", "", cookie, "")
	var empty struct{ Data map[string]json.RawMessage }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &empty) != nil || empty.Data == nil || len(empty.Data) != 0 || centerHits.Load() != before || proxyHits.Load() != proxyBefore+1 {
		t.Fatal("center client invented repository-style direct fallback after explicit proxy failure")
	}
}
