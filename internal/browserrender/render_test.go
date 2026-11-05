package browserrender

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func ownedBrowser(t *testing.T) Config {
	t.Helper()
	path := os.Getenv("BROWSER_RENDER_TEST_EXECUTABLE")
	if path == "" && runtime.GOOS == "darwin" {
		path = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		t.Skip("real native browser unavailable; set BROWSER_RENDER_TEST_EXECUTABLE")
	}
	return Config{Executable: path, TempDir: t.TempDir()}
}

func TestNativeOwnedJavaScriptHeadersAndCleanup(t *testing.T) {
	config := ownedBrowser(t)
	var mu sync.Mutex
	var externalHeaders []http.Header
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		externalHeaders = append(externalHeaders, r.Header.Clone())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, `document.body.dataset.cross='yes';`)
	}))
	defer external.Close()
	var ownedHeaders []http.Header
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ownedHeaders = append(ownedHeaders, r.Header.Clone())
		mu.Unlock()
		if r.URL.Path == "/data" {
			time.Sleep(30 * time.Millisecond)
			fmt.Fprint(w, `{"name":"owned-js"}`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!DOCTYPE html><html><body><script src=%q></script><script>fetch('/data').then(r=>r.json()).then(x=>document.body.dataset.result=x.name)</script></body></html>`, external.URL+"/public.js")
	}))
	defer peer.Close()
	// Paired Playwright allows launch180s + navigation30s + networkidle5s;
	// retain room for its bounded final profile cleanup as well.
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	html, err := config.Render(ctx, Request{URL: peer.URL + "/page", Cookie: "owned-cookie=a=b", UserAgent: "OwnedBrowserSource/1", Headless: true, Timeout: 5 * time.Second})
	if err != nil || html == nil {
		t.Fatalf("real browser: html=%v err=%v", html, err)
	}
	for _, expected := range []string{"<!DOCTYPE html>", `data-result="owned-js"`, `data-cross="yes"`} {
		if !strings.Contains(*html, expected) {
			t.Errorf("missing rendered marker %s", expected)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ownedHeaders) < 2 || len(externalHeaders) == 0 {
		t.Fatal("real JS/subresource calls missing")
	}
	for _, h := range ownedHeaders {
		if h.Get("Cookie") != "owned-cookie=a=b" || h.Get("User-Agent") != "OwnedBrowserSource/1" {
			t.Error("configured owned headers not applied")
		}
	}
	for _, h := range externalHeaders {
		if h.Get("Cookie") != "" || h.Get("Authorization") != "" {
			t.Error("site secret crossed origin")
		}
	}
	entries, err := os.ReadDir(config.TempDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("browser profile not removed")
	}
}

func TestNativeOwnedTimeoutAndGuard(t *testing.T) {
	config := ownedBrowser(t)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/pending" {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><script>fetch('/pending')</script></body></html>`)
	}))
	defer peer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	html, err := config.Render(ctx, Request{URL: peer.URL, Headless: true, Timeout: 100 * time.Millisecond})
	if html != nil || err == nil {
		t.Fatal("networkidle timeout became success")
	}
	var timedOut *Error
	if !errors.As(err, &timedOut) || timedOut.Code != "browser_networkidle_failed" {
		t.Fatalf("expected a real networkidle timeout, got %v", err)
	}
	ownedFailure := errors.New("owned revoked")
	html, err = config.Render(ctx, Request{URL: peer.URL, Headless: true, Guard: func(context.Context) error { return ownedFailure }})
	var revoked *GuardError
	if html != nil || !errors.As(err, &revoked) || !errors.Is(err, ownedFailure) {
		t.Fatalf("scope was swallowed: %v", err)
	}
}

func TestNativeOwnedHeadedDefaultAndDisplayCleanup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("source Docker virtual display is Linux-only")
	}
	config := ownedBrowser(t)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><script>document.body.dataset.headed='executed';</script></body></html>`)
	}))
	defer peer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	html, err := config.Render(ctx, Request{URL: peer.URL, Timeout: 5 * time.Second})
	if err != nil || html == nil || !strings.Contains(*html, `data-headed="executed"`) {
		t.Fatalf("source default headed browser failed: %v", err)
	}
}

func TestNativeOwnedConfiguredAuthenticatedProxy(t *testing.T) {
	config := ownedBrowser(t)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "owned-site=cookie" {
			t.Error("proxy lost site cookie")
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><script>document.body.dataset.proxy='executed';</script></body></html>`)
	}))
	defer peer.Close()
	var mu sync.Mutex
	count := 0
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owned, _ := url.Parse(peer.URL)
		if r.Method == http.MethodConnect || r.URL.Host != owned.Host {
			w.WriteHeader(502)
			return
		}
		if r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("owned-user:owned-password")) {
			t.Error("upstream proxy auth missing")
			w.WriteHeader(407)
			return
		}
		mu.Lock()
		count++
		mu.Unlock()
		r.Header.Del("Proxy-Authorization")
		r.RequestURI = ""
		response, err := transport.RoundTrip(r)
		if err != nil {
			t.Error("owned proxy forward failed")
			w.WriteHeader(502)
			return
		}
		defer response.Body.Close()
		for k, values := range response.Header {
			for _, v := range values {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	defer proxy.Close()
	address, _ := url.Parse(proxy.URL)
	address.User = url.UserPassword("owned-user", "owned-password")
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	html, err := config.Render(ctx, Request{URL: peer.URL, Cookie: "owned-site=cookie", Proxy: address.String(), Headless: true, Timeout: 5 * time.Second})
	if err != nil || html == nil || !strings.Contains(*html, `data-proxy="executed"`) {
		t.Fatalf("actual proxy render failed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if count == 0 {
		t.Fatal("proxy bypassed")
	}
}

func TestFlareSolverrSourceBranches(t *testing.T) {
	for _, sample := range []struct {
		name     string
		replies  []map[string]any
		proxy    string
		want     *string
		commands []string
	}{
		{"direct", []map[string]any{{"status": "ok", "solution": map[string]any{"response": "owned"}}}, "", textPointer("owned"), []string{"request.get"}},
		{"truthy-without-response", []map[string]any{{"status": "ok", "solution": map[string]any{"cookies": []any{}}}}, "", nil, []string{"request.get"}},
		{"authenticated", []map[string]any{{"status": "ok"}, {"status": "ok", "solution": map[string]any{"response": "owned"}}, {"status": "ok"}}, "http://owned-user:owned-password@proxy.invalid:8080", textPointer("owned"), []string{"sessions.create", "request.get", "sessions.destroy"}},
		{"create-fails-cleanup", []map[string]any{{"status": "error"}, {"status": "ok"}}, "http://owned-user@proxy.invalid:8080", nil, []string{"sessions.create", "sessions.destroy"}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			var mu sync.Mutex
			calls := []map[string]any{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/owned/v1" {
					t.Error("FS base path lost")
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid FS payload")
				}
				mu.Lock()
				pos := len(calls)
				calls = append(calls, body)
				mu.Unlock()
				if pos >= len(sample.replies) {
					t.Error("extra FS call")
					w.WriteHeader(500)
					return
				}
				json.NewEncoder(w).Encode(sample.replies[pos])
			}))
			defer peer.Close()
			config := Config{Emulation: "FLARESOLVERR", FlareSolverrURL: peer.URL + "/owned/", Executable: filepath.Join(t.TempDir(), "missing")}
			got, err := config.Render(context.Background(), Request{URL: "https://owned.invalid/", Proxy: sample.proxy, Timeout: 3 * time.Second, Cookie: "key=a=b;plus=a+b;dup=one;dup=two"})
			if sample.want != nil {
				if err != nil || got == nil || *got != *sample.want {
					t.Fatalf("FS result %v %v", got, err)
				}
			} else if got != nil {
				t.Fatal("None became HTML")
			}
			mu.Lock()
			defer mu.Unlock()
			if len(calls) != len(sample.commands) {
				t.Fatalf("calls=%d", len(calls))
			}
			for i, cmd := range sample.commands {
				if calls[i]["cmd"] != cmd {
					t.Error("FS source order changed")
				}
			}
			if sample.proxy != "" && len(calls) > 1 && sample.commands[1] == "request.get" {
				if calls[0]["session"] != calls[1]["session"] || calls[1]["session"] != calls[2]["session"] {
					t.Error("session changed")
				}
			}
		})
	}
}

func TestCookieParseSourceLatinOneAndDuplicates(t *testing.T) {
	got := parseCookies("a=first;a=two;space=%20;unicode=%E9;plus=a+b;equal=a=b;raw=中文%20")
	want := `[{"name":"a","value":"two"},{"name":"space","value":" "},{"name":"unicode","value":"%E9"},{"name":"plus","value":"a+b"},{"name":"equal","value":"a=b"},{"name":"raw","value":"中文%20"}]`
	raw, _ := json.Marshal(got)
	if string(raw) != want {
		t.Fatalf("source cookie parse: %s", raw)
	}
}

func textPointer(text string) *string { return &text }
