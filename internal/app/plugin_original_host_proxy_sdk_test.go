package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins"
)

func ownedNativeProxy(t *testing.T) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasSuffix(strings.ToLower(name), "_proxy") || name == "REQUEST_METHOD" {
			t.Setenv(name, "")
		}
	}
	path := os.Getenv("OWNED_HOST_PROXY_FIXTURE_BINARY")
	if path == "" {
		t.Fatal("OWNED_HOST_PROXY_FIXTURE_BINARY must name separately compiled trusted Go worker")
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	m := installOwnedResourceProcessWorker(t, a, cookie, path)
	value, _ := a.pluginRunningInstances.Load(m.PluginID)
	return a, cookie, value.(*originalPluginInstance)
}

// The routing expectations follow source settings.PROXY and the independently
// source-tested requests environment merge. The native worker reads the current
// host snapshot after its process starts, not its inherited stale environment.
func TestOriginalHostProxyActualSDKNativePostRoutes(t *testing.T) {
	for _, kind := range []string{"configured-auth-no-proxy-retained", "source-getter-uppercase", "environment-lowercase", "environment-cgi", "environment-host-specific", "environment-no-proxy"} {
		t.Run(kind, func(t *testing.T) {
			a, _, worker := ownedNativeProxy(t)
			var targetCalls, proxyA, proxyB atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				targetCalls.Add(1)
				if r.Header.Get("Proxy-Authorization") != "" {
					t.Error("proxy authentication forwarded to target")
				}
				body, _ := io.ReadAll(r.Body)
				fmt.Fprint(w, "owned-native:"+string(body))
			}))
			defer target.Close()
			proxy := func(counter *atomic.Int32) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					counter.Add(1)
					if r.Method != "POST" || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("owned-proxy-user:owned-proxy-password")) {
						t.Error("private configured authentication or native method missing")
						w.WriteHeader(400)
						return
					}
					body, _ := io.ReadAll(r.Body)
					req, _ := http.NewRequest("POST", target.URL+"/owned-native", strings.NewReader(string(body)))
					client := &http.Client{Transport: &http.Transport{Proxy: nil}}
					response, err := client.Do(req)
					if err != nil {
						t.Error(err)
						w.WriteHeader(502)
						return
					}
					defer response.Body.Close()
					w.WriteHeader(response.StatusCode)
					_, _ = io.Copy(w, response.Body)
				}))
			}
			first, second := proxy(&proxyA), proxy(&proxyB)
			defer first.Close()
			defer second.Close()
			auth := func(address string) string {
				return strings.Replace(address, "://", "://owned-proxy-user:owned-proxy-password@", 1)
			}
			firstURL, secondURL := auth(first.URL), auth(second.URL)
			environmentOnly := false
			address := "http://owned-native.invalid/owned-native"
			expected := "A"
			switch kind {
			case "configured-auth-no-proxy-retained":
				settings := a.Settings()
				settings.ProxyHost = firstURL
				if err := a.saveSettings(context.Background(), settings); err != nil {
					t.Fatal(err)
				}
				t.Setenv("HTTP_PROXY", secondURL)
				t.Setenv("NO_PROXY", "*")
			case "source-getter-uppercase":
				t.Setenv("HTTP_PROXY", firstURL)
				t.Setenv("http_proxy", secondURL)
			case "environment-lowercase":
				environmentOnly = true
				t.Setenv("HTTP_PROXY", firstURL)
				t.Setenv("http_proxy", secondURL)
				expected = "B"
			case "environment-cgi":
				environmentOnly = true
				t.Setenv("HTTP_PROXY", firstURL)
				t.Setenv("ALL_PROXY", secondURL)
				t.Setenv("REQUEST_METHOD", "POST")
				expected = "B"
			case "environment-host-specific":
				environmentOnly = true
				t.Setenv("http://owned-native.invalid_proxy", firstURL)
				t.Setenv("ALL_PROXY", secondURL)
			case "environment-no-proxy":
				environmentOnly = true
				t.Setenv("ALL_PROXY", firstURL)
				t.Setenv("NO_PROXY", "*")
				address = target.URL + "/owned-native"
				expected = "direct"
			}
			raw, _ := json.Marshal(map[string]any{"url": address, "body": "owned-post-body", "environment_only": environmentOnly})
			result, err := worker.callOriginalMethod(context.Background(), "post", raw)
			if err != nil {
				t.Fatal(err)
			}
			var value struct {
				Status int
				Body   string
			}
			if json.Unmarshal(result.Result, &value) != nil || value.Status != 200 || value.Body != "owned-native:owned-post-body" || targetCalls.Load() != 1 {
				t.Fatal("native worker did not reach real owned HTTP target", err)
			}
			wantA, wantB := int32(0), int32(0)
			if expected == "A" {
				wantA = 1
			}
			if expected == "B" {
				wantB = 1
			}
			if proxyA.Load() != wantA || proxyB.Load() != wantB {
				t.Fatal("source proxy/environment route differs", proxyA.Load(), proxyB.Load(), expected)
			}
			for _, secret := range []string{"owned-proxy-user", "owned-proxy-password", firstURL, secondURL} {
				if strings.Contains(string(result.Result), secret) {
					t.Fatal("private proxy projected into worker public result")
				}
			}
		})
	}
}

func TestOriginalHostProxyActualSDKCloneRevocationAndPublicBoundary(t *testing.T) {
	a, cookie, worker := ownedNativeProxy(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "owned-direct") }))
	defer target.Close()
	raw, _ := json.Marshal(map[string]any{"url": target.URL, "body": "owned", "environment_only": true})
	if _, err := worker.callOriginalMethod(context.Background(), "post", raw); err != nil {
		t.Fatal(err)
	}
	clone := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedHostProxy/clone", `{"suffix":"copy"}`, cookie, "")
	assertOriginalRegistrySuccess(t, clone.Code, clone.Body.Bytes())
	value, _ := a.pluginRunningInstances.Load("OwnedHostProxycopy")
	if _, err := value.(*originalPluginInstance).callOriginalMethod(context.Background(), "post", raw); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/network/proxy", "/api/network.proxy.read", "/api/v1/plugin/OwnedHostProxy/network.proxy.read"} {
		r := callTest(t, a.Handler(), "GET", path, "", cookie, "")
		if r.Code != 404 {
			t.Fatal("private snapshot accidentally became public HTTP route", path, r.Code)
		}
	}
	callback, revoke := worker.originalBaseSessionHost(context.Background(), "post", nil)
	revoke()
	identity := plugins.Identity{PluginID: worker.installation.PluginID, InstanceID: worker.installation.InstanceID, Version: worker.installation.Version}
	if _, err := callback(context.Background(), identity, plugins.NetworkProxyReadCapability, json.RawMessage(`{}`)); err == nil {
		t.Fatal("revoked ordinary caller read private snapshot")
	}
	if err := a.stopOriginalPlugin(context.Background(), "OwnedHostProxy"); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.callOriginalMethod(context.Background(), "post", raw); err == nil {
		t.Fatal("stopped worker retained native request capability")
	}
}
