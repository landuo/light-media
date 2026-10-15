package app

import (
	"bytes"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"light-media/web"
)

func proxyRequest(t *testing.T, handler http.Handler, method, target, body, origin string, cookie *http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	// TLS terminates at the proxy; the backend receives ordinary HTTP with the
	// original external Host. The test must not accidentally rely on r.TLS.
	r.Host = "media.example.invalid:28443"
	r.Header.Set("Content-Type", "application/json")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func proxyLogin(t *testing.T, a *App) *http.Cookie {
	t.Helper()
	w := proxyRequest(t, a.Handler(), "POST", "/light-media/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, "https://media.example.invalid:28443", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"authenticated":true`) {
		t.Fatalf("proxy login failed: %d %s", w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Secure-lm_session" || cookies[0].Path != "/light-media/" || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Domain != "" {
		t.Fatal("incorrect proxy cookie scope or security")
	}
	return cookies[0]
}

func TestProxyMountAuthenticationKeepsLANSessionAndRejectsSpoofedOrigins(t *testing.T) {
	a := newTestApp(t)
	lanCookie := setupTestAdmin(t, a)
	proxyCookie := proxyLogin(t, a)
	h := a.Handler()
	for _, tc := range []struct {
		path   string
		cookie *http.Cookie
		status int
	}{
		{"/api/settings", lanCookie, 200},
		{"/light-media/api/settings", proxyCookie, 200},
		{"/light-media/api/settings", lanCookie, 401},
		{"/api/settings", proxyCookie, 401},
		{"/light-media/api/settings", nil, 401},
	} {
		if w := proxyRequest(t, h, "GET", tc.path, "", "", tc.cookie, nil); w.Code != tc.status {
			t.Fatalf("%s: got %d want %d", tc.path, w.Code, tc.status)
		}
	}
	for _, tc := range []struct {
		origin  string
		headers map[string]string
		status  int
	}{
		{"https://media.example.invalid:28443", nil, 404},
		{"http://media.example.invalid:28443", nil, 403},
		{"https://media.example.invalid", nil, 403},
		{"https://other.example.invalid:28443", map[string]string{"X-Forwarded-Host": "other.example.invalid:28443", "X-Forwarded-Proto": "https"}, 403},
		{"https://media.example.invalid:28443", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"https://user@media.example.invalid:28443", nil, 403},
		{"https://media.example.invalid:28443/path", nil, 403},
		{"null", nil, 403},
	} {
		w := proxyRequest(t, h, "POST", "/light-media/api/does-not-exist", "{}", tc.origin, proxyCookie, tc.headers)
		if w.Code != tc.status {
			t.Fatalf("origin %q: got %d want %d", tc.origin, w.Code, tc.status)
		}
	}
	w := proxyRequest(t, h, "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, "http://media.example.invalid:28443", nil, map[string]string{"X-Forwarded-Proto": "https"})
	if w.Code != 200 || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].Secure {
		t.Fatal("untrusted forwarded protocol changed the LAN cookie")
	}
	w = proxyRequest(t, h, "POST", "/api/does-not-exist", "{}", "https://media.example.invalid:28443", lanCookie, map[string]string{"X-Forwarded-Proto": "https"})
	if w.Code != 403 {
		t.Fatal("untrusted forwarded protocol bypassed origin validation")
	}
	w = proxyRequest(t, h, "POST", "/light-media/api/auth/logout", "{}", "https://media.example.invalid:28443", proxyCookie, nil)
	if w.Code != 200 || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].Name != proxyCookie.Name || w.Result().Cookies()[0].Path != proxyCookie.Path || !w.Result().Cookies()[0].Secure || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("proxy logout did not expire the exact secure cookie")
	}
	if w = proxyRequest(t, h, "GET", "/light-media/api/settings", "", "", proxyCookie, nil); w.Code != 401 {
		t.Fatal("revoked proxy session remains valid")
	}
	if w = proxyRequest(t, h, "GET", "/api/settings", "", "", lanCookie, nil); w.Code != 200 {
		t.Fatal("proxy logout revoked unrelated LAN session")
	}
}

func TestProxyMountServesActualBuildAssetsAtBothEntrypoints(t *testing.T) {
	a := newTestApp(t)
	a.web = web.Handler()
	h := a.Handler()
	for _, prefix := range []string{"", "/light-media"} {
		w := callTest(t, h, "GET", prefix+"/", "", nil, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `id="app"`) {
			t.Fatalf("UI missing at %s", prefix)
		}
		assets := regexp.MustCompile(`(?:src|href)="(\./assets/[^\"]+)"`).FindAllStringSubmatch(w.Body.String(), -1)
		if len(assets) < 2 {
			t.Fatal("build must use relative JS and CSS assets; run npm run build")
		}
		for _, asset := range assets {
			w = callTest(t, h, "GET", prefix+"/"+strings.TrimPrefix(asset[1], "./"), "", nil, "")
			if w.Code != 200 || w.Body.Len() < 20 || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
				t.Fatalf("asset unavailable at %s: %s", prefix, asset[1])
			}
		}
	}
	for _, target := range []string{"/light-media", "/light-media/api"} {
		w := callTest(t, h, "GET", target+"?test=1", "", nil, "")
		if w.Code != http.StatusPermanentRedirect || w.Header().Get("Location") != target+"/?test=1" {
			t.Fatal("mount redirect lost query or escaped origin", target)
		}
	}
	for _, target := range []string{"/light-media/../api/auth/status", "/light-media//api/auth/status", "/light-media/%2e%2e/api/auth/status"} {
		if w := callTest(t, h, "GET", target, "", nil, ""); w.Code != 400 || w.Header().Get("Location") != "" {
			t.Fatal("noncanonical path escaped mount", target, w.Code)
		}
	}
}

func TestProxyFrontendReportLimitUsesOnlyItsOwnSession(t *testing.T) {
	a := newTestApp(t)
	setupTestAdmin(t, a)
	cookie := proxyLogin(t, a)
	for i := 0; i < 11; i++ {
		w := proxyRequest(t, a.Handler(), "POST", "/light-media/api/frontend-errors", `{"page":"home","code":"SCRIPT_ERROR","build":"test"}`, "https://media.example.invalid:28443", cookie, map[string]string{"Cookie": "another_service=" + strings.Repeat("x", i+1) + "; " + cookie.Name + "=" + cookie.Value})
		want := 200
		if i == 10 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("report %d: got %d want %d", i, w.Code, want)
		}
	}
}

func TestSuccessfulLoginClearsFailedAttemptsWithoutTrustingForwardedIP(t *testing.T) {
	a := newTestApp(t)
	setupTestAdmin(t, a)
	const peer = "192.0.2.1" // httptest's connection address, not a forwarded IP.
	for i := 0; i < 2; i++ {
		// A normal login still succeeds after nine failures and does not consume
		// the last failure slot for later logins through this same proxy.
		a.authAttempts[peer] = make([]time.Time, 9)
		for j := range a.authAttempts[peer] {
			a.authAttempts[peer][j] = time.Now()
		}
		proxyLogin(t, a)
		if len(a.authAttempts[peer]) != 0 {
			t.Fatal("successful login consumed the failed-attempt budget")
		}
	}
	for i := 0; i < 11; i++ {
		w := proxyRequest(t, a.Handler(), "POST", "/light-media/api/auth/login", `{"username":"wrong","password":"not-the-password"}`, "https://media.example.invalid:28443", nil, map[string]string{"X-Forwarded-For": "198.51.100." + strings.Repeat("1", i+1)})
		want := 401
		if i == 10 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("failed attempt %d: got %d want %d", i, w.Code, want)
		}
	}
}

func TestProxyMountAuthenticatedImagesDownloadsAndSSE(t *testing.T) {
	a, _, _ := importTestApp(t)
	setupTestAdmin(t, a)
	cookie := proxyLogin(t, a)
	h := a.Handler()
	var jpegData bytes.Buffer
	if err := jpeg.Encode(&jpegData, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	old := assetImageClient
	t.Cleanup(func() { assetImageClient = old })
	assetImageClient = &http.Client{Transport: imageTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"image/jpeg"}}, Body: io.NopCloser(bytes.NewReader(jpegData.Bytes()))}, nil
	})}
	for _, tc := range []struct {
		path, contentType, contains string
	}{
		{"/catalog/image?path=%2Ffixture.jpg&size=w342", "image/jpeg", string(jpegData.Bytes())},
		{"/logs/export", "application/x-ndjson", `"type":"export_meta"`},
		{"/logs/stream?last_event_id=instance_other:12", "text/event-stream", "event: gap"},
	} {
		path := "/light-media/api" + tc.path
		if w := proxyRequest(t, h, "GET", path, "", "", nil, nil); w.Code != 401 {
			t.Fatal("private endpoint served without authentication", tc.path)
		}
		w := proxyRequest(t, h, "GET", path, "", "", cookie, nil)
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), tc.contentType) || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("proxy endpoint failed %s: status=%d type=%s", tc.path, w.Code, w.Header().Get("Content-Type"))
		}
		if strings.Contains(tc.path, "/logs/stream") && (!w.Flushed || w.Header().Get("X-Accel-Buffering") != "no") {
			t.Fatal("SSE streaming was buffered by mount wrapper")
		}
	}
}
