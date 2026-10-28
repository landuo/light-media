package app

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOriginalAPI35StaticCodeFilesAuthTypesAndCloneRemoval(t *testing.T) {
	a, cookie, repo := originalFrontendFixture(t)
	path := "/api/v1/plugin/file/" + originalFrontendFixtureID + "/frontend/assets/remoteEntry.js"
	call := func(method, path string, session bool, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		if session {
			r.AddCookie(cookie)
		}
		for key, value := range headers {
			r.Header.Set(key, value)
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	if w := call("GET", path, false, nil); w.Code != 403 {
		t.Fatal("static code accepted without resource/session authority", w.Code)
	}
	for _, name := range []string{"remoteEntry.js", "chunk.js", "style.css", "font.woff2"} {
		p := "/api/v2/plugin/file/" + strings.ToLower(originalFrontendFixtureID) + "/frontend/assets/" + name
		w := call("GET", p+"?inert=1", true, map[string]string{"Range": "bytes=0-1"})
		if w.Code != 200 || w.Body.Len() <= 2 || w.Header().Get("Content-Disposition") != "inline; filename="+name || w.Header().Get("Content-Range") != "" {
			t.Fatal("source full resource stream differs", name, w.Code, w.Header())
		}
		if strings.HasSuffix(name, ".js") && w.Header().Get("Content-Type") != "application/javascript" {
			t.Fatal("source JavaScript MIME differs", w.Header())
		}
	}
	for path, want := range map[string]int{"frontend/assets": 403, "frontend/assets/missing.bin": 404, "../outside": 403, ".acquired.json": 404} {
		if w := call("GET", "/api/v1/plugin/file/"+originalFrontendFixtureID+"/"+path, true, nil); w.Code != want {
			t.Fatal("source resource path error", path, w.Code, want)
		}
	}
	if w := call("HEAD", path, true, nil); w.Code != 405 || w.Header().Get("Allow") != "GET" {
		t.Fatal("source get-only method handling", w.Code, w.Header())
	}
	// This endpoint also serves code-package members outside frontend. It never
	// maps the configuration/KV or instance-data directory into that tree.
	code := call("GET", "/api/v1/plugin/file/"+originalFrontendFixtureID+"/owned-frontend-fixture", true, nil)
	if code.Code != 200 || code.Body.Len() < 1<<20 {
		t.Fatal("ordinary package file not streamed", code.Code, code.Body.Len())
	}
	body, _ := json.Marshal(map[string]string{"suffix": "static"})
	cloneResult := callTest(t, a.Handler(), "POST", "/api/plugins/"+originalFrontendFixtureID+"/clone", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, cloneResult.Code, cloneResult.Body.Bytes())
	a.wg.Wait()
	clone := originalFrontendFixtureID + "static"
	if w := call("GET", "/api/v1/plugin/file/"+clone+"/frontend/assets/chunk.js", true, nil); w.Code != 200 {
		t.Fatal("clone code resource missing", w.Code)
	}
	originalFrontendInstall(t, a, cookie, repo, true)
	result := callTest(t, a.Handler(), "DELETE", "/api/plugins/"+originalFrontendFixtureID, "", cookie, "")
	assertOriginalRegistrySuccess(t, result.Code, result.Body.Bytes())
	if w := call("GET", path, true, nil); w.Code != 200 {
		t.Fatal("primary uninstall incorrectly removed code resource", w.Code)
	}
	result = callTest(t, a.Handler(), "DELETE", "/api/plugins/"+clone, "", cookie, "")
	assertOriginalRegistrySuccess(t, result.Code, result.Body.Bytes())
	if w := call("GET", "/api/v1/plugin/file/"+clone+"/frontend/assets/chunk.js", true, nil); w.Code != 404 {
		t.Fatal("clone uninstall retained static code", w.Code)
	}
}

func TestOriginalAPI35StaticAnonymousProviderDirectoryOnly(t *testing.T) {
	a, cookie := originalAuthFrontendFixture(t)
	enabled := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedAuth", `{"enabled":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, enabled.Code, enabled.Body.Bytes())
	call := func(path string) *httptest.ResponseRecorder {
		return callTest(t, a.Handler(), "GET", path, "", nil, "")
	}
	// The source provider checks only enabled plugin declarations and the
	// declared remote directory. It grants neither arbitrary package code nor DB.
	if w := call("/api/v1/plugin/file/OwnedAuth/ui/remoteEntry.js"); w.Code != 200 {
		t.Fatal("declared anonymous authentication remote rejected", w.Code, w.Body.String())
	}
	if w := call("/api/v1/plugin/file/OwnedAuth/ui/style.css"); w.Code != 200 {
		t.Fatal("declared authentication sibling rejected", w.Code)
	}
	if w := call("/api/v1/plugin/file/OwnedAuth/owned-auth-fixture"); w.Code != 403 {
		t.Fatal("anonymous code outside declared directory accepted", w.Code)
	}
	result := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedAuth", `{"enabled":false}`, cookie, "")
	assertOriginalRegistrySuccess(t, result.Code, result.Body.Bytes())
	if w := call("/api/v1/plugin/file/OwnedAuth/ui/remoteEntry.js"); w.Code != 403 {
		t.Fatal("disabled provider retained anonymous file grant", w.Code)
	}
}
