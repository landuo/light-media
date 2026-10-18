package app

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"light-media/internal/plugins/seedrescue"
)

func TestPluginOriginalFrontendGraphHTTP(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	base := "/api/plugins/SeedRescue/frontend/assets/"
	if w := callTest(t, a.Handler(), "GET", base+"remoteEntry.js", "", nil, ""); w.Code != 401 {
		t.Fatal("private plugin assets became public", w.Code)
	}
	if w := callTest(t, a.Handler(), "GET", base+"remoteEntry.js", "", cookie, ""); w.Code != 404 {
		t.Fatal("uninstalled plugin published assets", w.Code)
	}
	if w := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"SeedRescue"}`, cookie, ""); w.Code != 200 {
		t.Fatal("actual native SeedRescue installation required", w.Code, w.Body.String())
	}
	form := callTest(t, a.Handler(), "GET", "/api/plugins/SeedRescue/form", "", cookie, "")
	var formResponse struct {
		Data struct {
			RenderMode string `json:"render_mode"`
		} `json:"data"`
	}
	if form.Code != http.StatusOK || json.Unmarshal(form.Body.Bytes(), &formResponse) != nil || formResponse.Data.RenderMode != "vue" {
		t.Fatal("actual SDK form lost original Vue Config render mode", form.Code)
	}
	for name, contentType := range pluginFrontendFiles {
		w := callTest(t, a.Handler(), "GET", base+name, "", cookie, "")
		original, err := fs.ReadFile(seedrescue.FrontendAssets(), "assets/"+name)
		if err != nil || w.Code != 200 || !bytes.Equal(w.Body.Bytes(), original) || w.Header().Get("Content-Type") != contentType {
			t.Fatal("original asset response changed", name, w.Code, err)
		}
		if w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
			t.Fatal("private resource headers missing", name)
		}
		head := callTest(t, a.Handler(), "HEAD", base+name, "", cookie, "")
		if head.Code != 200 || head.Body.Len() != 0 {
			t.Fatal("HEAD changed", name, head.Code)
		}
	}
	for _, path := range []string{"unknown.js", "../reference/source/__init__.py", "%2e%2e%2fconfig.json", "remoteEntry.js/extra"} {
		w := callTest(t, a.Handler(), "GET", base+path, "", cookie, "")
		if w.Code == http.StatusTemporaryRedirect || w.Code == http.StatusMovedPermanently {
			// ServeMux canonicalizes literal dot segments before routing. Follow only
			// its relative canonical URL, then require the private asset allowlist.
			location, err := url.Parse(w.Header().Get("Location"))
			if err != nil || location.IsAbs() || location.Host != "" || !strings.HasPrefix(location.Path, "/api/plugins/") {
				t.Fatal("unexpected canonical redirect", path, w.Code)
			}
			w = callTest(t, a.Handler(), "GET", location.String(), "", cookie, "")
		}
		if w.Code != http.StatusNotFound {
			t.Fatal("non-graph asset exposed", path, w.Code)
		}
	}
	request := httptest.NewRequest("GET", base+"remoteEntry.js", nil)
	request.AddCookie(cookie)
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()
	a.Handler().ServeHTTP(response, request)
	if response.Code != 404 {
		t.Fatal("cross-site module request accepted", response.Code)
	}
	if w := callTest(t, a.Handler(), "POST", base+"remoteEntry.js", `{}`, cookie, ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatal("resource mutation request accepted", w.Code)
	}
	if _, err := a.Store.DB.Exec(`UPDATE plugin_installations SET version='0.0' WHERE instance_id='SeedRescue'`); err != nil {
		t.Fatal(err)
	}
	if w := callTest(t, a.Handler(), "GET", base+"remoteEntry.js", "", cookie, ""); w.Code != 404 || strings.Contains(w.Body.String(), "import") {
		t.Fatal("unavailable native version published source", w.Code)
	}
}
