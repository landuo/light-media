package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/sites"
)

func ownedBrowserFixture(t *testing.T) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	binary := os.Getenv("OWNED_HOST_BROWSER_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("OWNED_HOST_BROWSER_FIXTURE_BINARY must name an actual separately compiled worker")
	}
	executable := os.Getenv("BROWSER_RENDER_TEST_EXECUTABLE")
	if executable == "" && runtime.GOOS == "darwin" {
		executable = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	}
	if _, err := os.Stat(executable); err != nil {
		t.Fatal("actual native browser required for installed SDK proof")
	}
	t.Setenv("CLOAKBROWSER_BINARY_PATH", executable)
	t.Setenv("BROWSER_EMULATION", "cloakbrowser")
	t.Setenv("FLARESOLVERR_URL", "")
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	m := installOwnedResourceProcessWorker(t, a, cookie, binary)
	loaded, ok := a.pluginRunningInstances.Load(m.PluginID)
	if !ok {
		t.Fatal("installed browser worker absent")
	}
	return a, cookie, loaded.(*originalPluginInstance)
}

func ownedBrowserSDK(t *testing.T, worker *originalPluginInstance, request plugins.SiteRenderRequest) (plugins.SiteRenderResult, error) {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := worker.callOriginalMethod(ctx, "resource", raw)
	var out plugins.SiteRenderResult
	if err == nil {
		err = json.Unmarshal(result.Result, &out)
	}
	return out, err
}

// Expectations: fixed helper goto -> networkidle -> page.content; the owned
// HTML's post-navigation script must execute, rather than returning HTTP bytes.
func TestOriginalHostBrowserActualSDKJSCloneAndRecovery(t *testing.T) {
	a, cookie, worker := ownedBrowserFixture(t)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "owned-session=one" || r.Header.Get("User-Agent") != "OwnedRenderSDK/1" {
			t.Error("current configured site headers missing")
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body><script>document.body.dataset.result='owned-executed';</script></body></html>`)
	}))
	defer peer.Close()
	settings := a.Settings()
	settings.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-render-site", Kind: "nexusphp", BaseURL: peer.URL, Cookie: "owned-session=one", UserAgent: "OwnedRenderSDK/1"}, Enabled: false}}
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	headless := true
	seconds := 3
	request := plugins.SiteRenderRequest{ID: "owned-render-site", Path: "/page", Headless: &headless, TimeoutSeconds: &seconds}
	got, err := ownedBrowserSDK(t, worker, request)
	if err != nil || got.HTML == nil || !strings.Contains(*got.HTML, `data-result="owned-executed"`) {
		t.Fatalf("real installed browser chain failed: %v", err)
	}
	clone := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedHostBrowser/clone", `{"suffix":"Render"}`, cookie, "")
	assertOriginalRegistrySuccess(t, clone.Code, clone.Body.Bytes())
	a.wg.Wait()
	value, ok := a.pluginRunningInstances.Load("OwnedHostBrowserrender")
	if !ok {
		t.Fatal("clone worker missing")
	}
	got, err = ownedBrowserSDK(t, value.(*originalPluginInstance), request)
	if err != nil || got.HTML == nil || !strings.Contains(*got.HTML, `data-result="owned-executed"`) {
		t.Fatal("clone cannot use host browser", err)
	}
	a.closeOriginalSessions()
	if err := a.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	value, ok = a.pluginRunningInstances.Load("OwnedHostBrowser")
	if !ok {
		t.Fatal("cold worker absent")
	}
	got, err = ownedBrowserSDK(t, value.(*originalPluginInstance), request)
	if err != nil || got.HTML == nil {
		t.Fatal("session recovery browser failed", err)
	}
	request.Path = "https://other.invalid/"
	if _, err = ownedBrowserSDK(t, value.(*originalPluginInstance), request); err == nil {
		t.Fatal("foreign render target accepted")
	}
	request.ID = "missing"
	request.Path = ""
	if _, err = ownedBrowserSDK(t, value.(*originalPluginInstance), request); err == nil {
		t.Fatal("unknown configured site accepted")
	}
}

func TestOriginalHostBrowserActualSDKConfigurationRevocationBeforeScriptWrite(t *testing.T) {
	a, _, worker := ownedBrowserFixture(t)
	var posts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			return
		}
		a.cfgMu.Lock()
		a.config.Sites[0].Cookie = "changed after initial navigation"
		a.cfgMu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><script>fetch('/write',{method:'POST'})</script></body></html>`)
	}))
	defer peer.Close()
	settings := a.Settings()
	settings.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-render-site", Kind: "nexusphp", BaseURL: peer.URL, Cookie: "owned-session=before"}}}
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	headless := true
	seconds := 3
	got, err := ownedBrowserSDK(t, worker, plugins.SiteRenderRequest{ID: "owned-render-site", Headless: &headless, TimeoutSeconds: &seconds})
	if err == nil || got.HTML != nil {
		t.Fatal("stale scope returned rendered success")
	}
	if posts.Load() != 0 {
		t.Fatal("script write crossed site revocation")
	}
}
