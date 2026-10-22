package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func systemMessageTitle(s string) *string { return &s }
func readSystemFrame(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	blank, err := r.ReadString('\n')
	if err != nil || blank != "\n" || !strings.HasPrefix(line, "data: ") {
		t.Fatal("invalid SSE frame", line, blank, err)
	}
	return strings.TrimSuffix(strings.TrimPrefix(line, "data: "), "\n")
}
func openSystemStream(t *testing.T, a *App, cookie *http.Cookie, path string) (*http.Response, *bufio.Reader) {
	t.Helper()
	server := httptest.NewServer(a.Handler())
	t.Cleanup(server.Close)
	req, err := http.NewRequest("GET", server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	// Native HTTP clients supply Origin when Fetch Metadata is unavailable.
	origin := server.URL
	if strings.HasPrefix(path, "/light-media/") {
		origin = "https://" + req.URL.Host
	}
	req.Header.Set("Origin", origin)
	client := server.Client()
	client.Timeout = 12 * time.Second
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatal("stream unavailable", resp.StatusCode)
	}
	return resp, bufio.NewReader(resp.Body)
}
func TestSystemMessageHelperSharedFIFOAndShortDedup(t *testing.T) {
	var m systemMessageRuntime
	at := time.Date(2026, 10, 22, 10, 20, 0, 0, time.FixedZone("fixture", 8*3600))
	if m.put("user", nil, "ignored", nil, at) {
		t.Fatal("unsupported role accepted")
	}
	if !m.put("plugin", nil, "one", nil, at) || m.put("plugin", systemMessageTitle("插件通知"), "one", json.RawMessage(`{}`), at.Add(time.Second)) {
		t.Fatal("default title or note truthiness changed")
	}
	if !m.put("system", systemMessageTitle("system"), "two", json.RawMessage(`{"safe":"value"}`), at) {
		t.Fatal("system put failed")
	}
	for _, want := range []struct{ role, title, text string }{{"plugin", "插件通知", "one"}, {"system", "system", "two"}} {
		raw := m.get("not-a-filter")
		var got systemMessage
		if json.Unmarshal(raw, &got) != nil || got.Type != want.role || got.Title == nil || *got.Title != want.title || got.Text != want.text {
			t.Fatal("not shared FIFO", string(raw))
		}
	}
	if len(m.get("plugin")) != 0 {
		t.Fatal("queue broadcasts or replays")
	}
	if !m.put("plugin", nil, "one", nil, at.Add(time.Minute)) {
		t.Fatal("minute/TTL did not permit new notification")
	}
	m.get("")
	if !m.put("plugin", nil, "edge", nil, at.Add(59*time.Second)) || !m.put("plugin", nil, "edge", nil, at.Add(time.Minute)) {
		t.Fatal("minute bucket lost")
	}
	// Cache eviction must not discard any of the queued payloads.
	var many systemMessageRuntime
	for i := 0; i < 701; i++ {
		if !many.put("system", nil, fmt.Sprint(i), nil, at) {
			t.Fatal(i)
		}
	}
	if len(many.recent) != 500 || len(many.queue) != 701 {
		t.Fatal("cache cap became queue cap", len(many.recent), len(many.queue))
	}
	for i := 0; i < 701; i++ {
		var got systemMessage
		if json.Unmarshal(many.get("system"), &got) != nil || got.Text != fmt.Sprint(i) {
			t.Fatal("lost FIFO item", i)
		}
	}
	many.close()
	many.close()
	select {
	case <-many.done():
	default:
		t.Fatal("close did not stop stream")
	}
	if many.put("system", nil, "late", nil, at) || len(many.get("")) != 0 {
		t.Fatal("stopped queue accepted data")
	}
}
func TestSystemMessageSSERealThreeSecondHeartbeat(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	resp, r := openSystemStream(t, a, cookie, "/api/system/message")
	defer resp.Body.Close()
	start := time.Now()
	if got := readSystemFrame(t, r); got != "" {
		t.Fatal("initial empty data frame changed", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatal("first frame was not immediate", elapsed)
	}
	start = time.Now()
	a.systemMessages.put("system", systemMessageTitle("owned"), "next", nil, time.Now())
	var got systemMessage
	if json.Unmarshal([]byte(readSystemFrame(t, r)), &got) != nil || got.Text != "next" {
		t.Fatal("no message at next tick")
	}
	elapsed := time.Since(start)
	if elapsed < 2800*time.Millisecond || elapsed > 6*time.Second || systemMessageInterval != 3*time.Second {
		t.Fatal("real three-second cadence changed", elapsed)
	}
}
func TestSystemMessageSSETwoClientsConsumeRatherThanBroadcast(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	a.systemMessages.put("system", nil, "first", nil, time.Now())
	a.systemMessages.put("plugin", nil, "second", nil, time.Now())
	one, r1 := openSystemStream(t, a, cookie, "/api/system/message?role=plugin")
	defer one.Body.Close()
	two, r2 := openSystemStream(t, a, cookie, "/api/system/message?role=system&last_event_id=owned")
	defer two.Body.Close()
	var first, second systemMessage
	if json.Unmarshal([]byte(readSystemFrame(t, r1)), &first) != nil || json.Unmarshal([]byte(readSystemFrame(t, r2)), &second) != nil || first.Text != "first" || second.Text != "second" {
		t.Fatal("streams filtered or broadcast", first, second)
	}
	if len(a.systemMessages.get("")) != 0 {
		t.Fatal("consumed messages retained")
	}
}
func TestSystemMessageSSEAuthenticationPrefixAndDisconnect(t *testing.T) {
	a := newTestApp(t)
	lan := setupTestAdmin(t, a)
	proxy := proxyLogin(t, a)
	a.systemMessages.put("system", nil, "private", nil, time.Now())
	for _, path := range []string{"/api/system/message", "/api/system/message?token=synthetic&apikey=synthetic", "/light-media/api/system/message"} {
		w := callTest(t, a.Handler(), "GET", path, "", nil, "")
		if w.Code != 401 {
			t.Fatal("URL token or absent session authenticated", path, w.Code)
		}
	}
	w := callTest(t, a.Handler(), "GET", "/api/system/message", "", lan, "https://foreign.invalid")
	if w.Code != 403 {
		t.Fatal("cross-site GET consumed message", w.Code)
	}
	r := httptest.NewRequest("GET", "http://media.example.invalid:28443/light-media/api/system/message", nil)
	r.AddCookie(proxy)
	r.Header.Set("Origin", "https://media.example.invalid:28443")
	ctx, cancel := context.WithCancel(r.Context())
	r = r.WithContext(ctx)
	frame := make(chan string, 1)
	done := make(chan struct{})
	writer := &systemStreamRecorder{header: make(http.Header), frames: frame}
	go func() { a.Handler().ServeHTTP(writer, r); close(done) }()
	select {
	case raw := <-frame:
		if !strings.Contains(raw, "private") {
			t.Fatal("prefix lost authenticated payload", raw)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("prefix did not stream")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disconnected stream not stopped")
	}
}

// Explicit concurrent recorder: tests do not inspect ResponseRecorder while a
// streaming handler mutates it. Each Flush transfers an immutable string.
type systemStreamRecorder struct {
	header http.Header
	buf    strings.Builder
	frames chan string
}

func (w *systemStreamRecorder) Header() http.Header         { return w.header }
func (w *systemStreamRecorder) WriteHeader(int)             {}
func (w *systemStreamRecorder) Write(b []byte) (int, error) { return w.buf.Write(b) }
func (w *systemStreamRecorder) Flush() {
	s := w.buf.String()
	w.buf.Reset()
	select {
	case w.frames <- s:
	default:
	}
}
func TestSystemMessageSSESessionRevocationAndAppStop(t *testing.T) {
	for _, mode := range []string{"logout", "stop"} {
		t.Run(mode, func(t *testing.T) {
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			resp, r := openSystemStream(t, a, cookie, "/api/system/message")
			if readSystemFrame(t, r) != "" {
				t.Fatal("initial frame")
			}
			if mode == "logout" {
				if _, err := a.Store.DB.Exec(`DELETE FROM sessions WHERE token_hash=?`, tokenHash(cookie.Value)); err != nil {
					t.Fatal(err)
				}
			} else {
				a.systemMessages.close()
			}
			if mode == "logout" {
				a.systemMessages.put("system", nil, "must-remain-private", nil, time.Now())
			}
			rest, err := io.ReadAll(resp.Body)
			if err != nil || len(rest) != 0 {
				t.Fatal("closed stream delivered another frame", string(rest), err)
			}
			if mode == "logout" && len(a.systemMessages.get("")) == 0 {
				t.Fatal("revoked session consumed queued message")
			}
		})
	}
}

func TestSystemMessageSSEFetchMetadataRejectsSiblingWithoutOrigin(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	proxyCookie := proxyLogin(t, a)
	a.systemMessages.put("system", nil, "private", nil, time.Now())
	a.systemMessages.mu.Lock()
	original := string(a.systemMessages.queue[0])
	a.systemMessages.mu.Unlock()
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	for _, target := range []struct {
		path   string
		cookie *http.Cookie
	}{{"/api/system/message", cookie}, {"/light-media/api/system/message", proxyCookie}} {
		for _, metadata := range []struct{ site, origin string }{{"", ""}, {"", "https://foreign.invalid"}, {"same-site", ""}, {"cross-site", ""}, {"unexpected", ""}, {"none", "https://foreign.invalid"}} {
			fetchSite := metadata.site
			request, err := http.NewRequest("GET", server.URL+target.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.AddCookie(target.cookie)
			if fetchSite != "" {
				request.Header.Set("Sec-Fetch-Site", fetchSite)
			}
			if metadata.origin != "" {
				request.Header.Set("Origin", metadata.origin)
			}
			// Browser subresources need not carry Origin. A sibling's Strict cookie
			// must not make the destructively-read queue available to that request.
			if request.Header.Get("Origin") != metadata.origin {
				t.Fatal("fixture Origin changed")
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusForbidden {
				t.Fatal("unsafe metadata consumed SSE", target.path, fetchSite, response.StatusCode)
			}
			a.systemMessages.mu.Lock()
			unchanged := len(a.systemMessages.queue) == 1 && string(a.systemMessages.queue[0]) == original
			a.systemMessages.mu.Unlock()
			if !unchanged {
				t.Fatal("forbidden request consumed or changed FIFO", target.path, fetchSite)
			}
		}
	}
	// Both roots retain same-origin metadata and explicit-Origin native clients.
	// Double-missing clients were rejected above, not silently labeled native.
	first := true
	for _, target := range []struct {
		path   string
		cookie *http.Cookie
	}{{"/api/system/message", cookie}, {"/light-media/api/system/message", proxyCookie}} {
		for _, fetchSite := range []string{"", "same-origin", "none"} {
			want := "private"
			if !first {
				want = "allowed-" + target.path + "-" + fetchSite
				a.systemMessages.put("system", nil, want, nil, time.Now())
			}
			first = false
			request, err := http.NewRequest("GET", server.URL+target.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.AddCookie(target.cookie)
			if fetchSite != "" {
				request.Header.Set("Sec-Fetch-Site", fetchSite)
			} else {
				origin := server.URL
				if strings.HasPrefix(target.path, "/light-media/") {
					origin = "https://" + request.URL.Host
				}
				request.Header.Set("Origin", origin)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusOK {
				response.Body.Close()
				t.Fatal("allowed session rejected", target.path, fetchSite, response.StatusCode)
			}
			var message systemMessage
			raw := readSystemFrame(t, bufio.NewReader(response.Body))
			response.Body.Close()
			if json.Unmarshal([]byte(raw), &message) != nil || message.Text != want || len(a.systemMessages.get("")) != 0 {
				t.Fatal("allowed metadata did not consume exactly one", target.path, fetchSite, raw)
			}
		}
	}
}
