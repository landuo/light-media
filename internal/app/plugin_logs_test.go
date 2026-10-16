package app

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func pluginLogsFixture(t *testing.T, count int) (*App, *http.Cookie) {
	t.Helper()
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	installStoredPlugin(t, a.pluginStore, "one")
	installStoredPlugin(t, a.pluginStore, "two")
	stamp := time.Now().UTC().Add(-24 * time.Hour)
	for n := 0; n < count; n++ {
		a.writeLog(map[string]any{"time": stamp.Add(time.Duration(n) * time.Millisecond).Format(time.RFC3339Nano), "level": "INFO", "module": "plugins", "event": "fixture", "message": fmt.Sprintf("one-record-%04d", n), "plugin_id": "Probe", "plugin_instance_id": "one"})
	}
	a.writeLog(map[string]any{"level": "ERROR", "module": "plugins", "event": "fixture", "message": "two-private-record", "plugin_id": "Probe", "plugin_instance_id": "two"})
	return a, cookie
}

func assertNoPluginLogTemps(t *testing.T, a *App) {
	t.Helper()
	files, err := os.ReadDir(a.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasPrefix(f.Name(), ".plugin-log-") {
			t.Fatal("export left a named temporary log")
		}
	}
}

func TestPluginLogsScopedHistoryAndRealSSE(t *testing.T) {
	a, cookie := pluginLogsFixture(t, 61)
	response := callTest(t, a.Handler(), "GET", "/api/plugins/one/logs", "", cookie, "")
	var body struct {
		Data struct {
			History logPage `json:"history"`
			Resume  string  `json:"resume_id"`
		} `json:"data"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Data.History.Entries) != 50 || body.Data.History.Entries[0]["message"] != "one-record-0060" || !strings.Contains(body.Data.Resume, ":") || bytes.Contains(response.Body.Bytes(), []byte("two-private-record")) {
		t.Fatal("plugin history was not isolated/full-age/newest-first", response.Code)
	}
	// These records occur after the history's captured boundary, before SSE
	// connects. Resume must retain them and exclude a sibling instance.
	a.writeLog(map[string]any{"level": "INFO", "module": "plugins", "event": "fixture", "message": "one-after-history", "plugin_instance_id": "one"})
	a.writeLog(map[string]any{"level": "ERROR", "module": "plugins", "event": "fixture", "message": "two-after-history", "plugin_instance_id": "two"})
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/plugins/one/logs/stream?last_event_id="+url.QueryEscape(body.Data.Resume), nil)
	request.AddCookie(cookie)
	stream, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != 200 || !strings.Contains(stream.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatal("plugin SSE was not a real stream", stream.StatusCode)
	}
	reader := bufio.NewReader(stream.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal("fresh scoped SSE record missing", err)
		}
		if strings.HasPrefix(line, "data:") {
			if !strings.Contains(line, "one-after-history") || strings.Contains(line, "two-after-history") {
				t.Fatal("SSE mixed instance history", line)
			}
			break
		}
	}
}

func TestPluginLogsFullTextAndZipCrossPagesWithoutTruncation(t *testing.T) {
	a, cookie := pluginLogsFixture(t, 1003)
	plain := callTest(t, a.Handler(), "GET", "/api/plugins/one/logs/file?q=no-match&limit=1&plugin_id=two", "", cookie, "")
	if plain.Code != 200 || !strings.HasPrefix(plain.Header().Get("Content-Type"), "text/plain") || strings.Count(plain.Body.String(), "one-record-") != 1003 || strings.Contains(plain.Body.String(), "two-private") || strings.Index(plain.Body.String(), "one-record-1002") > strings.Index(plain.Body.String(), "one-record-0000") {
		t.Fatal("complete newest-first text was filtered, truncated or mixed", plain.Code)
	}
	archive := callTest(t, a.Handler(), "GET", "/api/plugins/one/logs/download", "", cookie, "")
	if archive.Code != 200 || archive.Header().Get("Content-Type") != "application/zip" || !strings.Contains(archive.Header().Get("Content-Disposition"), "one-logs-") {
		t.Fatal("actual ZIP download unavailable", archive.Code)
	}
	z, err := zip.NewReader(bytes.NewReader(archive.Body.Bytes()), int64(archive.Body.Len()))
	if err != nil || len(z.File) != 1 || !strings.HasSuffix(z.File[0].Name, "/one.log") || strings.Contains(z.File[0].Name, "..") {
		t.Fatal("archive name or integrity invalid", err)
	}
	f, err := z.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(f)
	f.Close()
	if err != nil || strings.Count(string(raw), "one-record-") != 1003 || strings.Contains(string(raw), "two-private") || bytes.Index(raw, []byte("one-record-0000")) > bytes.Index(raw, []byte("one-record-1002")) {
		t.Fatal("complete chronological archive changed rows", err)
	}
	assertNoPluginLogTemps(t, a)
}

func TestPluginLogsAuthenticationScopeMethodsAndBoundedExport(t *testing.T) {
	a, cookie := pluginLogsFixture(t, 2)
	for _, path := range []string{"/api/plugins/one/logs", "/api/plugins/one/logs/file", "/api/plugins/one/logs/download", "/api/plugins/one/logs/stream"} {
		if response := callTest(t, a.Handler(), "GET", path, "", nil, ""); response.Code != 401 {
			t.Fatal("anonymous plugin logs allowed", path, response.Code)
		}
	}
	for _, test := range []struct {
		method, path string
		code         int
	}{
		{"GET", "/api/plugins/one/logs?plugin_instance_id=two", 400},
		{"GET", "/api/plugins/missing/logs/file", 404},
		{"GET", "/api/plugins/one/logs/unknown", 404},
		{"POST", "/api/plugins/one/logs/download", 405},
	} {
		response := callTest(t, a.Handler(), test.method, test.path, "", cookie, "")
		if response.Code != test.code || strings.Contains(response.Body.String(), "one-record") || strings.Contains(response.Body.String(), "two-private") {
			t.Fatal("invalid log request accepted or leaked rows", test.path, response.Code)
		}
	}
	a.logs.exportSlots <- struct{}{}
	busy := callTest(t, a.Handler(), "GET", "/api/plugins/one/logs/download", "", cookie, "")
	<-a.logs.exportSlots
	if busy.Code != 429 {
		t.Fatal("concurrent full exports bypassed their limit", busy.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if f, _, err := a.preparePluginLogFile(ctx, "one", true); err == nil || f != nil {
		t.Fatal("canceled export returned an incomplete archive")
	}
	assertNoPluginLogTemps(t, a)
}

func TestPluginLogsCorruptHistoryIsNotReturnedAsCompleteArchive(t *testing.T) {
	a, cookie := pluginLogsFixture(t, 3)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !a.logs.flush(ctx) {
		t.Fatal("fixture logger did not flush")
	}
	files, err := a.logFiles()
	if err != nil || len(files) == 0 {
		t.Fatal("fixture logs unavailable", err)
	}
	f, err := os.OpenFile(a.Dir+"/logs/"+files[0].Name, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("synthetic-corrupt-json\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	history := callTest(t, a.Handler(), "GET", "/api/plugins/one/logs", "", cookie, "")
	var boundary struct {
		Data struct {
			History logPage `json:"history"`
			Resume  string  `json:"resume_id"`
		} `json:"data"`
	}
	if history.Code != 200 || json.Unmarshal(history.Body.Bytes(), &boundary) != nil || !boundary.Data.History.Gap || !strings.Contains(boundary.Data.Resume, ":") {
		t.Fatal("a retained-history gap discarded the boundary for fresh live logs", history.Code)
	}
	response := callTest(t, a.Handler(), "GET", "/api/plugins/one/logs/download", "", cookie, "")
	if response.Code != 409 || response.Header().Get("Content-Type") == "application/zip" || !strings.Contains(response.Body.String(), "PLUGIN_LOG_HISTORY_CHANGED") {
		t.Fatal("corrupt snapshot was returned as a successful complete archive", response.Code)
	}
	assertNoPluginLogTemps(t, a)
}

func TestPluginLogsRetainedSnapshotDoesNotConfuseCollectionHealthWithDiskGap(t *testing.T) {
	a, cookie := pluginLogsFixture(t, 3)
	a.logs.mu.Lock()
	a.logs.dropped[1]++ // Historical loss elsewhere does not remove retained rows.
	a.logs.mu.Unlock()
	response := callTest(t, a.Handler(), "GET", "/api/plugins/one/logs/download", "", cookie, "")
	if response.Code != 200 || response.Header().Get("Content-Type") != "application/zip" {
		t.Fatal("collection health hid the full retained snapshot", response.Code)
	}
	assertNoPluginLogTemps(t, a)
}

func TestPluginLogsProxyMountKeepsFileAndStreamResponsesUnwrapped(t *testing.T) {
	a, _ := pluginLogsFixture(t, 3)
	cookie := proxyLogin(t, a)
	for _, item := range []struct{ suffix, contentType, contains string }{
		{"", "application/json", "one-record-0002"},
		{"/file", "text/plain", "one-record-0002"},
		{"/download", "application/zip", "PK"},
		{"/stream?last_event_id=instance_other:12", "text/event-stream", "event: gap"},
	} {
		path := "/light-media/api/plugins/one/logs" + item.suffix
		if response := proxyRequest(t, a.Handler(), "GET", path, "", "", nil, nil); response.Code != 401 {
			t.Fatal("mounted plugin logs bypassed authentication", item.suffix, response.Code)
		}
		response := proxyRequest(t, a.Handler(), "GET", path, "", "", cookie, nil)
		if response.Code != 200 || !strings.HasPrefix(response.Header().Get("Content-Type"), item.contentType) || !strings.Contains(response.Body.String(), item.contains) {
			t.Fatal("mounted plugin log response changed type or content", item.suffix, response.Code)
		}
		if strings.HasPrefix(item.suffix, "/stream") && (!response.Flushed || response.Header().Get("X-Accel-Buffering") != "no") {
			t.Fatal("plugin log stream was buffered")
		}
	}
}
