package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStartupHTTPKeepsAuthenticatedDiagnosticsAvailable(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	a.web = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("static shell")) })
	started := make(chan struct{})
	handler := a.StartupHandler(started)
	// A deliberately unfinished initializer cannot hold the listener or shell.
	for _, path := range []string{"/", "/assets/app.js", "/light-media/"} {
		w := callTest(t, handler, "GET", path, "", nil, "")
		if w.Code != 200 || w.Body.String() != "static shell" || w.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatalf("shell %s: %d %s", path, w.Code, w.Body)
		}
	}
	for _, path := range []string{"/api/auth/status", "/api/status", "/api/settings", "/api/logs"} {
		w := callTest(t, handler, "GET", path, "", cookie, "")
		if w.Code != 200 {
			t.Fatalf("diagnostic %s: %d %s", path, w.Code, w.Body)
		}
		if path == "/api/status" && !strings.Contains(w.Body.String(), `"starting":true`) {
			t.Fatalf("missing starting flag: %s", w.Body)
		}
	}
	if w := callTest(t, handler, "GET", "/api/settings", "", nil, ""); w.Code != 401 {
		t.Fatalf("anonymous settings: %d", w.Code)
	}
	if w := callTest(t, handler, "POST", "/api/auth/login", `{}`, nil, "http://evil.invalid"); w.Code != 403 {
		t.Fatalf("cross-origin login: %d", w.Code)
	}
	for _, tc := range []struct{ method, path string }{{"GET", "/api/downloads"}, {"GET", "/api/plugins"}, {"PUT", "/api/settings"}, {"POST", "/api/control"}} {
		w := callTest(t, handler, tc.method, tc.path, `{}`, cookie, "")
		if w.Code != 503 || w.Header().Get("Retry-After") != "3" || !strings.Contains(w.Body.String(), "SERVICE_STARTING") {
			t.Fatalf("startup gate %s %s: %d %s", tc.method, tc.path, w.Code, w.Body)
		}
	}
	w := callTest(t, handler, "GET", "/healthz", "", nil, "")
	if w.Code != 503 || w.Result().Header.Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("startup health: %d %v", w.Code, w.Result().Header)
	}
	close(started)
	w = callTest(t, handler, "GET", "/api/status", "", cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"starting":false`) {
		t.Fatalf("ready status: %d %s", w.Code, w.Body)
	}
	if w = callTest(t, handler, "GET", "/api/subscriptions", "", cookie, ""); w.Code != 200 {
		t.Fatalf("ready business route: %d %s", w.Code, w.Body)
	}
	if w = callTest(t, handler, "GET", "/healthz", "", nil, ""); w.Code != 200 {
		t.Fatalf("ready health: %d", w.Code)
	}
}

func TestStartupHTTPProxyLoginRetainsCookieScope(t *testing.T) {
	a := newTestApp(t)
	setupTestAdmin(t, a)
	handler := a.StartupHandler(make(chan struct{}))
	w := proxyRequest(t, handler, "POST", "/light-media/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, "https://media.example.invalid:28443", nil, nil)
	cookies := w.Result().Cookies()
	if w.Code != 200 || len(cookies) != 1 || cookies[0].Name != "__Secure-lm_session" || cookies[0].Path != "/light-media/" || !cookies[0].Secure {
		t.Fatalf("startup proxy login: %d %v", w.Code, cookies)
	}
	if w = proxyRequest(t, handler, "GET", "/light-media/api/status", "", "", cookies[0], nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"starting":true`) {
		t.Fatalf("startup proxy status: %d %s", w.Code, w.Body)
	}
}

func TestNativeDisplayDeadlineBoundsCooperativeReads(t *testing.T) {
	for _, path := range []string{"/api/catalog/explore", "/api/downloads", "/api/subscriptions", "/api/directories/manage", "/api/files/list", "/api/history/transfers", "/api/jobs"} {
		t.Run(path, func(t *testing.T) {
			h := nativeDisplayDeadline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				<-r.Context().Done()
				if !errors.Is(r.Context().Err(), context.DeadlineExceeded) {
					t.Error(r.Context().Err())
				}
				w.WriteHeader(http.StatusGatewayTimeout)
			}), 5*time.Millisecond)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			if w.Code != http.StatusGatewayTimeout {
				t.Fatal(w.Code)
			}
		})
	}
}

func TestNativeDisplayDeadlinePreservesMutationsStreamsAndEarlierDeadline(t *testing.T) {
	for _, tc := range []struct{ method, path string }{{"POST", "/api/downloads/add"}, {"PUT", "/api/subscriptions/x"}, {"GET", "/api/logs/stream"}, {"GET", "/api/files/content"}, {"GET", "/api/plugins/x/custom"}} {
		h := nativeDisplayDeadline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := r.Context().Deadline(); ok {
				t.Errorf("changed original lifetime: %s %s", tc.method, tc.path)
			}
		}), time.Second)
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, tc.path, nil))
	}
	deadline := time.Now().Add(time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	h := nativeDisplayDeadline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, _ := r.Context().Deadline(); !got.Equal(deadline) {
			t.Errorf("extended caller deadline: %v", got)
		}
	}), time.Minute)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/downloads", nil).WithContext(ctx))
}

func TestNativeDisplayDeadlineReturnsExplicitTimeout(t *testing.T) {
	a := newTestApp(t)
	w := httptest.NewRecorder()
	a.writeError(w, httptest.NewRequest("GET", "/api/downloads", nil), context.DeadlineExceeded)
	if w.Code != 504 || !strings.Contains(w.Body.String(), "REQUEST_TIMEOUT") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestStartupHTTPRespondsWhilePluginInitializationIsBlocked(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	a.web = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("shell")) })
	started := make(chan struct{})
	// Initialization owns these locks while registering and calling ordered plugin
	// getters. Diagnostic requests must not enter those plugin paths.
	a.pluginOriginalOperationMu.Lock()
	defer a.pluginOriginalOperationMu.Unlock()
	a.pluginImplementationMu.Lock()
	defer a.pluginImplementationMu.Unlock()
	server := httptest.NewServer(a.StartupHandler(started))
	defer server.Close()
	client := &http.Client{Timeout: time.Second}
	for _, path := range []string{"/", "/api/status", "/api/settings", "/api/logs", "/api/downloads"} {
		req, _ := http.NewRequest("GET", server.URL+path, nil)
		req.AddCookie(cookie)
		response, err := client.Do(req)
		if err != nil {
			t.Fatalf("startup diagnostic blocked %s: %v", path, err)
		}
		_ = response.Body.Close()
		expected := 200
		if path == "/api/downloads" {
			expected = 503
		}
		if response.StatusCode != expected {
			t.Fatalf("%s: %d", path, response.StatusCode)
		}
	}
}
