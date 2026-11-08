package app

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// MoviePilot 8a83efe app/api/endpoints/plugin.py:764-839 selects the file
// from the path independently of query parameters. RemoveLink 2.18's exact
// remoteEntry imports both its stylesheet and view with ?v=2.18, even when
// the native package version changes. Actual isolated source observations
// also cover empty, duplicate, unknown and unparseable query parameters.
func TestPluginOriginalFrontendQueryPreservesBytesAndGuards(t *testing.T) {
	a, cookie, _ := originalFrontendFixture(t)
	d := originalFrontendReadDescriptor(t, a, cookie, originalFrontendFixtureID)
	entryPath := "/api" + d.EntryPath
	queries := []string{"", "?v=2.18", "?v=2.18.1", "?v=", "?v=one&v=two", "?other=value", "?v=2.18&other=value", "?%76=2.18", "?v=a%2Fb", "?v=%ZZ", "?v=%00", "?v=one;other=two"}
	for _, file := range []string{"remoteEntry.js", "chunk.js", "style.css"} {
		target := strings.TrimSuffix(entryPath, "remoteEntry.js") + file
		plain := callTest(t, a.Handler(), "GET", target, "", cookie, "")
		if plain.Code != 200 {
			t.Fatal("baseline asset unavailable", file, plain.Code)
		}
		for _, query := range queries {
			t.Run(file+query, func(t *testing.T) {
				got := callTest(t, a.Handler(), "GET", target+query, "", cookie, "")
				if got.Code != 200 || !bytes.Equal(got.Body.Bytes(), plain.Body.Bytes()) || got.Header().Get("ETag") != plain.Header().Get("ETag") || got.Header().Get("Content-Type") != plain.Header().Get("Content-Type") {
					t.Fatalf("query changed file identity/bytes: %d", got.Code)
				}
			})
		}
	}
	query := "?v=2.18&other=value&v=older"
	entry := callTest(t, a.Handler(), "GET", entryPath+query, "", cookie, "")
	head := callTest(t, a.Handler(), "HEAD", entryPath+query, "", cookie, "")
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != entry.Header().Get("Content-Length") {
		t.Fatal("query changed HEAD semantics")
	}
	conditional := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", entryPath+query, nil)
		r.AddCookie(cookie)
		r.Header.Set("If-None-Match", entry.Header().Get("ETag"))
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	if got := conditional(); got.Code != 304 || got.Body.Len() != 0 {
		t.Fatal("query changed conditional response", got.Code)
	}
	if got := callTest(t, a.Handler(), "GET", entryPath+query+"&token=synthetic", "", nil, ""); got.Code != 401 {
		t.Fatal("query bypassed session authentication", got.Code)
	}
	wrong := strings.Replace(entryPath, d.PackageSHA256, strings.Repeat("0", 64), 1)
	if got := callTest(t, a.Handler(), "GET", wrong+query, "", cookie, ""); got.Code != 409 {
		t.Fatal("query bypassed package pin", got.Code)
	}
	for _, suffix := range []string{"../owned-frontend-fixture", "%2e%2e/owned-frontend-fixture", "%252e%252e/x.js", "dir%2fchunk.js", ".acquired.json", "missing.js"} {
		target := strings.TrimSuffix(entryPath, "remoteEntry.js") + suffix + query
		if got := callTest(t, a.Handler(), "GET", target, "", cookie, ""); got.Code < 400 {
			t.Fatal("query bypassed file/path guard", suffix, got.Code)
		}
	}
	if err := os.WriteFile(filepath.Join(a.pluginPackageRoot(), originalFrontendFixtureID, "frontend/assets/style.css"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := conditional(); got.Code == 200 || got.Code == 304 {
		t.Fatal("query bypassed whole graph verification", got.Code)
	}
}
