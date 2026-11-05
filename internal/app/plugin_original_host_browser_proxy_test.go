package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/sites"
)

// The expected browser proxy dict comes from the independently executed fixed
// PROXY_SERVER getter, not requests' different PROXY property. Calls install a
// real SDK worker and reach an owned HTTP FlareSolverr endpoint.
func TestOriginalHostBrowserActualSDKSourceProxyServer(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-platform-final-20261104/settings/proxy-settings-independent-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name   string            `json:"name"`
			Host   *string           `json:"proxy_host"`
			Env    map[string]string `json:"environment"`
			Server map[string]any    `json:"proxy_server"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"http-upper", "split-schemes", "all-only", "explicit-encoded-auth", "explicit-ipv6"} {
		for _, row := range oracle.Cases {
			if row.Name != name {
				continue
			}
			t.Run(name, func(t *testing.T) {
				for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy", "NO_PROXY", "no_proxy"} {
					t.Setenv(key, "")
				}
				for key, value := range row.Env {
					t.Setenv(key, value)
				}
				t.Setenv("NO_PROXY", "127.0.0.1,localhost") // The FS control endpoint is owned loopback; this does not alter explicit browser proxy payloads.
				var mu sync.Mutex
				var calls []map[string]any
				peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if r.Method != "POST" || r.URL.Path != "/v1" || json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Error("invalid FS request")
						http.Error(w, "invalid", 400)
						return
					}
					mu.Lock()
					calls = append(calls, body)
					mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"status":"ok","solution":{"response":"<html>source-proxy</html>"}}`)
				}))
				defer peer.Close()
				t.Setenv("BROWSER_EMULATION", "flaresolverr")
				t.Setenv("FLARESOLVERR_URL", peer.URL)
				a := newTestApp(t)
				cookie := setupTestAdmin(t, a)
				binary := os.Getenv("OWNED_HOST_BROWSER_FIXTURE_BINARY")
				if binary == "" {
					t.Fatal("actual separately compiled SDK fixture required")
				}
				m := installOwnedResourceProcessWorker(t, a, cookie, binary)
				loaded, ok := a.pluginRunningInstances.Load(m.PluginID)
				if !ok {
					t.Fatal("worker absent")
				}
				settings := a.Settings()
				settings.ProxyHost = ""
				if row.Host != nil {
					settings.ProxyHost = *row.Host
				}
				settings.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-proxy-site", Kind: "nexusphp", BaseURL: "http://owned-site.invalid"}, SiteOptions: SiteOptions{Proxy: true}, Enabled: false}}
				if err := a.saveSettings(context.Background(), settings); err != nil {
					t.Fatal(err)
				}
				a.wg.Wait()
				headless, seconds := true, 3
				got, err := ownedBrowserSDK(t, loaded.(*originalPluginInstance), plugins.SiteRenderRequest{ID: "owned-proxy-site", Path: "/page", Headless: &headless, TimeoutSeconds: &seconds})
				if err != nil || got.HTML == nil || *got.HTML != "<html>source-proxy</html>" {
					t.Fatalf("actual SDK/FS failed: %v %#v", err, got.HTML)
				}
				mu.Lock()
				defer mu.Unlock()
				needAuth := row.Server != nil && (row.Server["username"] != nil || row.Server["password"] != nil)
				if needAuth {
					if len(calls) != 3 || calls[0]["cmd"] != "sessions.create" || calls[1]["cmd"] != "request.get" || calls[2]["cmd"] != "sessions.destroy" {
						t.Fatalf("source session sequence: %#v", calls)
					}
					want := map[string]any{"url": row.Server["server"]}
					for _, key := range []string{"username", "password"} {
						if v, ok := row.Server[key]; ok {
							want[key] = v
						}
					}
					if !reflect.DeepEqual(calls[0]["proxy"], want) {
						t.Fatalf("source literal auth proxy mismatch got=%#v want=%#v", calls[0]["proxy"], want)
					}
					session, _ := calls[0]["session"].(string)
					if len(session) != 36 || calls[1]["session"] != session || calls[2]["session"] != session {
						t.Fatal("source session lifetime mismatch")
					}
				} else {
					if len(calls) != 1 || calls[0]["cmd"] != "request.get" {
						t.Fatalf("source request sequence: %#v", calls)
					}
					proxy, exists := calls[0]["proxy"]
					if row.Server == nil {
						if exists {
							t.Fatalf("source PROXY_SERVER=None must omit env-only browser proxy: %#v", proxy)
						}
					} else if !reflect.DeepEqual(proxy, map[string]any{"url": row.Server["server"]}) {
						t.Fatalf("source server spelling mismatch got=%#v want=%#v", proxy, row.Server["server"])
					}
				}
				for _, call := range calls {
					if strings.Contains(fmt.Sprint(call), "env-") && row.Server == nil {
						t.Fatal("environment proxy leaked into browser payload")
					}
				}
			})
			break
		}
	}
}
