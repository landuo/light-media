package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func passiveLogs(a *App) *runtimeLogger {
	return &runtimeLogger{app: a, boot: "instance_test", born: time.Now(), wake: make(chan struct{}, 1), barriers: make(chan logBarrier, 16), stop: make(chan struct{}), done: make(chan struct{}), overrides: map[string]debugOverride{}, stdout: io.Discard, frontend: map[string][]time.Time{}, streamSlots: make(chan struct{}, 8)}
}
func flushTestLogs(t *testing.T, a *App) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if !a.logs.flush(ctx) {
		t.Fatal("logger did not flush")
	}
}

func TestLoggingBoundedQueueWarnReserveAndSanitization(t *testing.T) {
	a := newTestApp(t)
	a.cfgMu.Lock()
	a.config.TMDB.APIKey = "fixture-secret-key"
	a.cfgMu.Unlock()
	l := passiveLogs(a)
	payload := strings.Repeat("x", 2048)
	for i := 0; i < 1800; i++ {
		l.enqueue(map[string]any{"level": "INFO", "module": "test", "message": payload})
	}
	before := l.health()
	if before.DroppedInfo == 0 || before.QueuedBytes > logQueueLimit-logWarnReserve {
		t.Fatal("INFO did not respect reserve", before)
	}
	l.enqueue(map[string]any{"level": "WARN", "message": "important warning"})
	if l.health().QueuedBytes <= before.QueuedBytes {
		t.Fatal("WARN reserve unusable")
	}
	for i := 0; i < 600; i++ {
		l.enqueue(map[string]any{"level": "WARN", "message": payload})
	}
	h := l.health()
	if h.QueuedBytes > logQueueLimit || h.RingBytes > logRingLimit || h.DroppedWarn == 0 {
		t.Fatal("unbounded logger", h)
	}
	v := l.encode(map[string]any{"level": "ERROR", "module": "test", "message": "fixture-secret-key https://example.invalid/?token=private password=unknown-secret", "stack": strings.Repeat("stack line\n", 2000), "cookie": "never-log", "unknown": map[string]any{"raw": "bad"}, "request_id": "fixture-secret-key"}, a.Settings())
	if len(v.Data) > logRecordLimit || !json.Valid(bytes.TrimSpace(v.Data)) {
		t.Fatal("record budget/encoding")
	}
	for _, secret := range []string{"fixture-secret-key", "example.invalid", "unknown-secret", "never-log", "\"unknown\""} {
		if bytes.Contains(v.Data, []byte(secret)) {
			t.Fatal("sensitive diagnostic field", secret)
		}
	}
	if !bytes.Contains(v.Data, []byte(`"stack"`)) {
		t.Fatal("stack field dropped")
	}
}

type waitingLogWriter struct {
	release <-chan struct{}
	entered chan struct{}
}

func (w waitingLogWriter) Write(b []byte) (int, error) {
	select {
	case w.entered <- struct{}{}:
	default:
	}
	<-w.release
	return len(b), nil
}
func TestLoggingSlowSinkDoesNotBlockProducer(t *testing.T) {
	a := newTestApp(t)
	l := passiveLogs(a)
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	l.stdout = waitingLogWriter{release: release, entered: entered}
	defer func() { close(release); l.close() }()
	f, e := os.CreateTemp(a.Dir, "sink-")
	if e != nil {
		t.Fatal(e)
	}
	l.file = f
	go l.run()
	l.enqueue(map[string]any{"level": "INFO", "message": "start sink"})
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("sink not exercised")
	}
	start := time.Now()
	for i := 0; i < 40; i++ {
		l.enqueue(map[string]any{"level": "INFO", "module": "test", "message": strings.Repeat("a", 2048)})
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("producer blocked on slow sink")
	}
	if l.health().QueuedBytes > logQueueLimit {
		t.Fatal("queue unbounded")
	}
}

func TestLoggingHistoryCursorSurvivesRotationAndAppend(t *testing.T) {
	a := newTestApp(t)
	for i := 0; i < 7; i++ {
		a.log("INFO", "cursor", "fixture", fmt.Sprintf("original-%d", i), "", "")
	}
	flushTestLogs(t, a)
	first, e := a.queryLogs(context.Background(), map[string]string{"module": "cursor", "limit": "2"})
	if e != nil || len(first.Entries) != 2 || first.Complete || first.NextCursor == "" {
		t.Fatal(first, e)
	}
	a.logs.fileMu.Lock()
	_ = a.logs.file.Sync()
	_ = a.logs.file.Close()
	a.logs.file = nil
	path := filepath.Join(a.Dir, "logs", "app.jsonl")
	rotated := filepath.Join(a.Dir, "logs", fmt.Sprintf("app-%020d-fixture.jsonl", time.Now().UnixNano()))
	if e = os.Rename(path, rotated); e == nil {
		e = a.logs.openFile()
	}
	a.logs.fileMu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	a.log("INFO", "cursor", "fixture", "new-after-snapshot", "", "")
	flushTestLogs(t, a)
	all := append([]map[string]any{}, first.Entries...)
	cursor := first.NextCursor
	for i := 0; cursor != "" && i < 10; i++ {
		page, err := a.queryLogs(context.Background(), map[string]string{"module": "cursor", "limit": "2", "cursor": cursor})
		if err != nil || page.Gap {
			t.Fatal(page, err)
		}
		all = append(all, page.Entries...)
		cursor = page.NextCursor
	}
	if cursor != "" || len(all) != 7 {
		t.Fatal("cursor lost or duplicated records", len(all))
	}
	for i, v := range all {
		if v["message"] != fmt.Sprintf("original-%d", 6-i) {
			t.Fatal("unstable snapshot", all)
		}
	}
	_, e = a.queryLogs(context.Background(), map[string]string{"cursor": first.NextCursor, "module": "other"})
	if e == nil {
		t.Fatal("cursor allowed changed filter")
	}
	_, e = a.queryLogs(context.Background(), map[string]string{"cursor": first.NextCursor + "tamper"})
	if e == nil {
		t.Fatal("unsigned cursor accepted")
	}
}
func TestLoggingHistoryMissingSegmentIsExplicitGap(t *testing.T) {
	a := newTestApp(t)
	for i := 0; i < 3; i++ {
		a.log("INFO", "gap", "fixture", fmt.Sprint(i), "", "")
	}
	flushTestLogs(t, a)
	first, e := a.queryLogs(context.Background(), map[string]string{"module": "gap", "limit": "1"})
	if e != nil {
		t.Fatal(e)
	}
	a.logs.fileMu.Lock()
	_ = a.logs.file.Close()
	a.logs.file = nil
	if e = os.Remove(filepath.Join(a.Dir, "logs", "app.jsonl")); e == nil {
		e = a.logs.openFile()
	}
	a.logs.fileMu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	next, e := a.queryLogs(context.Background(), map[string]string{"cursor": first.NextCursor})
	if e != nil || !next.Gap || !next.Complete {
		t.Fatal("missing segment not surfaced", next, e)
	}
}
func TestLoggingHistoryScanBudgetIsResumable(t *testing.T) {
	a := newTestApp(t)
	record, _ := json.Marshal(map[string]any{"time": now(), "level": "INFO", "module": "budget", "message": strings.Repeat("x", 4000)})
	record = append(record, '\n')
	path := filepath.Join(a.Dir, "logs", fmt.Sprintf("app-%020d-budget.jsonl", time.Now().UnixNano()))
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	block := bytes.Repeat(record, 128)
	for written := 0; written < 34<<20; written += len(block) {
		if _, e = f.Write(block); e != nil {
			t.Fatal(e)
		}
	}
	_ = f.Close()
	page, e := a.queryLogs(context.Background(), map[string]string{"module": "budget", "q": "does-not-occur"})
	if e != nil || page.Complete || !page.Truncated || page.NextCursor == "" || page.ScannedBytes > logScanBudget || len(page.Entries) != 0 {
		t.Fatalf("budget false coverage: complete=%v trunc=%v scanned=%d err=%v", page.Complete, page.Truncated, page.ScannedBytes, e)
	}
	for i := 0; !page.Complete && i < 40; i++ {
		page, e = a.queryLogs(context.Background(), map[string]string{"cursor": page.NextCursor})
		if e != nil {
			t.Fatal(e)
		}
	}
	if !page.Complete {
		t.Fatal("cursor made no progress")
	}
}
func TestLoggingWriteFailureAndRecovery(t *testing.T) {
	a := newTestApp(t)
	l := passiveLogs(a)
	f, e := os.CreateTemp(a.Dir, "closed-sink-")
	if e != nil {
		t.Fatal(e)
	}
	_ = f.Close()
	l.file = f
	entry := l.encode(map[string]any{"level": "ERROR", "message": "test"}, a.Settings())
	l.write(entry)
	if l.health().WriteError == "" {
		t.Fatal("failed write reported healthy")
	}
	l.file = nil
	l.write(entry)
	if l.health().WriteError != "" || l.health().LastWriteAt == "" {
		t.Fatal("sink did not recover", l.health())
	}
	_ = l.file.Close()
}
func TestLoggingRetentionBudgetsAndAge(t *testing.T) {
	a := newTestApp(t)
	old := filepath.Join(a.Dir, "logs", "app-00000000000000000001-old.jsonl")
	recent := filepath.Join(a.Dir, "logs", "app-00000000000000000002-recent.jsonl")
	if e := os.WriteFile(old, bytes.Repeat([]byte("x"), 700<<10), 0600); e != nil {
		t.Fatal(e)
	}
	_ = os.Chtimes(old, time.Now().Add(-8*24*time.Hour), time.Now().Add(-8*24*time.Hour))
	if e := os.WriteFile(recent, bytes.Repeat([]byte("x"), 700<<10), 0600); e != nil {
		t.Fatal(e)
	}
	a.logs.pruneFilesBudget(7, 1)
	if _, e := os.Stat(old); !os.IsNotExist(e) {
		t.Fatal("expired segment retained")
	}
	if _, e := os.Stat(recent); e != nil {
		t.Fatal("recent segment unexpectedly deleted")
	}
	active, e := os.Stat(filepath.Join(a.Dir, "logs", "app.jsonl"))
	if e != nil {
		t.Fatal(e)
	}
	if a.logs.totalBytes > 1<<20 || active == nil {
		t.Fatal("total cap includes active")
	}
}
func TestLoggingDebugScopeExpiresAndDoesNotRestart(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	h := a.Handler()
	res := callTest(t, h, "PATCH", "/api/settings/logging", `{"debug":{"scope":"module","id":"scoped","enabled":true}}`, cookie, "")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body)
	}
	a.log("DEBUG", "other", "ignored", "not enabled", "", "")
	a.log("DEBUG", "scoped.child", "enabled", "temporary detail", "", "")
	flushTestLogs(t, a)
	page, e := a.queryLogs(context.Background(), map[string]string{"level": "DEBUG"})
	if e != nil || len(page.Entries) != 1 || page.Entries[0]["module"] != "scoped.child" {
		t.Fatal(page, e)
	}
	a.logs.mu.Lock()
	d := a.logs.overrides["module:scoped"]
	d.ExpiresAt = time.Now().Add(-time.Second)
	a.logs.overrides["module:scoped"] = d
	a.logs.mu.Unlock()
	a.log("DEBUG", "scoped", "expired", "must not appear", "", "")
	if len(a.loggingSettings()["debug_overrides"].([]debugOverride)) != 0 {
		t.Fatal("expired DEBUG retained")
	}
	// No debug expiry is persisted even through the legacy settings surface.
	cfg := a.Settings()
	cfg.DebugUntil = time.Now().Add(time.Hour).Format(time.RFC3339)
	if e = a.saveSettings(context.Background(), cfg); e != nil {
		t.Fatal(e)
	}
	if a.Settings().DebugUntil != "" {
		t.Fatal("temporary DEBUG persisted")
	}
	raw := []byte{}
	if e = a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='config' AND id='main'`).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var saved Settings
	if e = a.Store.Unseal(raw, &saved); e != nil || saved.DebugUntil != "" {
		t.Fatal("DEBUG written to config", e)
	}
}
func TestLoggingFrontendWhitelistRateAndDiagnosticExport(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	h := a.Handler()
	if w := callTest(t, h, "POST", "/api/frontend-errors", `{"page":"home","code":"SCRIPT_ERROR","build":"0.1.0","stack":"secret"}`, cookie, ""); w.Code != 400 {
		t.Fatal("unknown frontend field accepted", w.Code)
	}
	body := `{"page":"detail","code":"SCRIPT_ERROR","build":"0.1.0","message":"password=client-secret /Users/private/file"}`
	for i := 0; i < 10; i++ {
		if w := callTest(t, h, "POST", "/api/frontend-errors", body, cookie, ""); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
	}
	if w := callTest(t, h, "POST", "/api/frontend-errors", body, cookie, ""); w.Code != 429 {
		t.Fatal("frontend rate limit missing", w.Code)
	}
	flushTestLogs(t, a)
	page, e := a.queryLogs(context.Background(), map[string]string{"module": "frontend"})
	if e != nil || len(page.Entries) != 10 {
		t.Fatal(len(page.Entries), e)
	}
	b, _ := json.Marshal(page.Entries)
	if bytes.Contains(b, []byte("client-secret")) || bytes.Contains(b, []byte("/Users/private")) {
		t.Fatal("client exception text stored")
	}
	a.log("WARN", "export_fixture", "fixture", "192.168.8.129 private.example.top /Users/private/media/episode.mkv", "wf_export", "")
	a.log("WARN", "other_fixture", "fixture", "outside filter", "", "")
	flushTestLogs(t, a)
	w := callTest(t, h, "GET", "/api/logs/export?module=export_fixture", "", cookie, "")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "ndjson") {
		t.Fatal(w.Code, w.Body)
	}
	output := w.Body.String()
	for _, s := range []string{"192.168.8.129", "private.example.top", "/Users/private/media", "outside filter"} {
		if strings.Contains(output, s) {
			t.Fatal("diagnostic disclosure or filter ignored", s)
		}
	}
	if !strings.Contains(output, `"type":"export_meta"`) || !strings.Contains(output, "[IP_") || !strings.Contains(output, "[PATH_") {
		t.Fatal("export omitted metadata/redaction", output)
	}
	if w := callTest(t, h, "GET", "/api/logs", "", nil, ""); w.Code != 401 {
		t.Fatal("logs unauthenticated")
	}
}
func TestLoggingStreamRestartGapAndResumeEviction(t *testing.T) {
	a := newTestApp(t)
	r := httptest.NewRequest("GET", "/api/logs/stream", nil)
	r.Header.Set("Last-Event-ID", "instance_other:12")
	w := httptest.NewRecorder()
	a.streamLogs(w, r)
	if !strings.Contains(w.Body.String(), "event: gap") || !strings.Contains(w.Body.String(), "instance_changed") {
		t.Fatal("restart gap missing", w.Body)
	}
	a.logs.mu.Lock()
	a.logs.seq = 100
	a.logs.ring = []encodedLog{{Seq: 95, Data: []byte(`{"time":"` + now() + `","message":"new"}`)}}
	a.logs.ringBytes = len(a.logs.ring[0].Data)
	a.logs.mu.Unlock()
	r = httptest.NewRequest("GET", "/api/logs/stream", nil)
	r.Header.Set("Last-Event-ID", a.logs.boot+":1")
	w = httptest.NewRecorder()
	a.streamLogs(w, r)
	if !strings.Contains(w.Body.String(), "history_evicted") {
		t.Fatal("resume eviction silent", w.Body)
	}
}
func TestLoggingHistoricalCleanupPreservesPendingAndRecovery(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	old := time.Now().Add(-200 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	for _, state := range []string{"succeeded", "cancelled", "running", "needs_review", "failed"} {
		key := "job_" + state
		_, e := a.Store.DB.ExecContext(ctx, `INSERT INTO jobs(id,workflow_id,kind,state,payload,run_at,created_at,updated_at) VALUES(?,?,?,?,?,0,?,?)`, key, "wf_fixture", "scrape", state, []byte(`{}`), old, old)
		if e != nil {
			t.Fatal(e)
		}
		_, e = a.Store.DB.ExecContext(ctx, `INSERT INTO events(time,workflow_id,job_id,level,module,event,message) VALUES(?,?,?,?,?,?,?)`, old, "wf_fixture", key, "INFO", "test", "fixture", "record")
		if e != nil {
			t.Fatal(e)
		}
	}
	_, e := a.Store.DB.ExecContext(ctx, `INSERT INTO records VALUES('audit','old',?,?)`, []byte(`{"time":"old"}`), old)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Put(ctx, "import_receipt", "recovery", map[string]string{"state": "published"}); e != nil {
		t.Fatal(e)
	}
	a.logs.cleanupHistory()
	var n int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n); e != nil || n != 3 {
		t.Fatal("pending event cleanup unsafe", n, e)
	}
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='audit'`).Scan(&n)
	if n != 0 {
		t.Fatal("old audit not cleaned")
	}
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='import_receipt'`).Scan(&n)
	if n != 1 {
		t.Fatal("recovery state removed")
	}
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n)
	if n != 5 {
		t.Fatal("job recovery records removed")
	}
}

func TestLoggingLegacyHistoryScrubAndEscapedSearch(t *testing.T) {
	a := newTestApp(t)
	c := a.Settings()
	c.TMDB.APIKey = "legacy-secret-value"
	if e := a.saveSettings(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	flushTestLogs(t, a)
	raw := fmt.Sprintf(`{"time":%q,"id":"legacy","level":"WARN","module":"legacy","message":"does-\u006eot-occur legacy-secret-value","stack":"legacy-secret-value","raw_config":{"token":"must-never-export"}}`+"\n", now())
	path := filepath.Join(a.Dir, "logs", fmt.Sprintf("app-%020d-legacy.jsonl", time.Now().UnixNano()))
	if e := os.WriteFile(path, []byte(raw), 0600); e != nil {
		t.Fatal(e)
	}
	page, e := a.queryLogs(context.Background(), map[string]string{"module": "legacy", "q": "does-not-occur"})
	if e != nil || len(page.Entries) != 1 {
		t.Fatal("escaped search lost historical record", page, e)
	}
	b, _ := json.Marshal(page.Entries)
	if bytes.Contains(b, []byte("legacy-secret-value")) || bytes.Contains(b, []byte("must-never-export")) || bytes.Contains(b, []byte("raw_config")) {
		t.Fatal("legacy history was not scrubbed")
	}
}
func TestLoggingRestartClearsAllTemporaryDebug(t *testing.T) {
	dir := t.TempDir()
	a, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	c := a.Settings()
	c.LogLevel = "DEBUG"
	if e = a.saveSettings(context.Background(), c); e != nil {
		a.Close()
		t.Fatal(e)
	}
	a.logs.mu.Lock()
	a.logs.overrides["module:fixture"] = debugOverride{Scope: "module", ID: "fixture", ExpiresAt: time.Now().Add(time.Hour)}
	a.logs.mu.Unlock()
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	b, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	if b.Settings().LogLevel != "INFO" || b.Settings().DebugUntil != "" || len(b.loggingSettings()["debug_overrides"].([]debugOverride)) != 0 {
		t.Fatal("DEBUG scope survived restart")
	}
}
