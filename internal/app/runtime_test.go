package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	a, e := New(Options{DataDir: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	a.autoSignInEnvironment = func() []string { return nil }
	t.Cleanup(func() { _ = a.Close() })
	return a
}
func callTest(t *testing.T, h http.Handler, method, path, body string, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func setupTestAdmin(t *testing.T, a *App) *http.Cookie {
	t.Helper()
	w := callTest(t, a.Handler(), "POST", "/api/auth/setup", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	if w.Code != 200 {
		t.Fatalf("setup %d %s", w.Code, w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "lm_session" {
			return c
		}
	}
	t.Fatal("no session")
	return nil
}
func TestAuthenticationSecretStorageAndOrigin(t *testing.T) {
	a := newTestApp(t)
	h := a.Handler()
	if w := callTest(t, h, "GET", "/api/settings", "", nil, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	cookie := setupTestAdmin(t, a)
	secret := "fake-not-live-secret-value-123456789"
	body := `{"tmdb":{"api_key":"` + secret + `"},"sites":[],"downloaders":[],"roots":[],"allowed_roots":[]}`
	if w := callTest(t, h, "PUT", "/api/settings", body, cookie, "http://evil.example"); w.Code != 403 {
		t.Fatal("cross-origin accepted", w.Code)
	}
	w := callTest(t, h, "PUT", "/api/settings", body, cookie, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), secret) {
		t.Fatalf("settings leaked or failed %d %s", w.Code, w.Body)
	}
	var raw []byte
	if e := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='config'`).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatal("plaintext at rest")
	}
	var c Settings
	if e := a.Store.Unseal(raw, &c); e != nil || c.TMDB.APIKey != secret {
		t.Fatal("could not decrypt own config", e)
	}
	st, e := os.Stat(filepath.Join(a.Dir, "secret.key"))
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal("key permissions", e)
	}
	if w = callTest(t, h, "POST", "/api/auth/setup", `{"username":"other","password":"other-password"}`, nil, ""); w.Code != 409 {
		t.Fatal("second setup accepted", w.Code)
	}
	if w = callTest(t, h, "POST", "/api/auth/logout", "{}", cookie, ""); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w = callTest(t, h, "GET", "/api/settings", "", cookie, ""); w.Code != 401 {
		t.Fatal("revoked session accepted")
	}
}
func TestSingleProcessLockAndDurablePause(t *testing.T) {
	dir := t.TempDir()
	a, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	if b, e := New(Options{DataDir: dir}); e == nil {
		b.Close()
		t.Fatal("two instances opened same data")
	}
	release, e := a.beginWrite()
	if e != nil {
		t.Fatal(e)
	}
	if e = a.setControl(context.Background(), true); e != nil {
		t.Fatal(e)
	}
	if a.control != "pausing" {
		t.Fatal(a.control)
	}
	release()
	if a.control != "paused" {
		t.Fatal(a.control)
	}
	a.Close()
	a, e = New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if !a.paused() {
		t.Fatal("pause lost on restart")
	}
	if _, e = a.beginWrite(); e == nil {
		t.Fatal("write allowed while paused")
	}
}
func TestReservationAndJobCommitRollback(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	tx, e := a.Store.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Reserve(ctx, tx, "tv:1418", 7, []int{17}, "intent1"); e != nil {
		t.Fatal(e)
	}
	j, e := enqueueTx(ctx, tx, "download", map[string]string{"id": "intent1"}, "wf1")
	if e != nil {
		t.Fatal(e)
	}
	tx.Rollback()
	var count int
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE id=?`, j.ID).Scan(&count)
	if count != 0 {
		t.Fatal("job escaped rollback")
	}
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM reservations`).Scan(&count)
	if count != 0 {
		t.Fatal("reservation escaped rollback")
	}
	tx, _ = a.Store.DB.Begin()
	if e = a.Store.Reserve(ctx, tx, "tv:1418", 7, []int{17}, "intent1"); e != nil {
		t.Fatal(e)
	}
	if _, e = enqueueTx(ctx, tx, "download", map[string]string{"id": "intent1"}, "wf1"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	tx, _ = a.Store.DB.Begin()
	if e = a.Store.Reserve(ctx, tx, "tv:1418", 7, []int{17}, "intent2"); e == nil {
		t.Fatal("duplicate slot allowed")
	}
	tx.Rollback()
	job, e := a.claim(ctx)
	if e != nil || job.Generation != 1 || job.State != "running" {
		t.Fatalf("claim %+v %v", job, e)
	}
	_, _ = a.Store.DB.Exec(`UPDATE jobs SET state='cancelled',generation=generation+1 WHERE id=?`, job.ID)
	if e = a.jobCancelled(ctx, job); e == nil {
		t.Fatal("old worker generation accepted")
	}
}
func TestConsistentBackupAndRestoreKeepsPause(t *testing.T) {
	dir := t.TempDir()
	a, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if e = a.saveSettings(ctx, Settings{TMDB: TMDBConfig{APIKey: "test-only-key"}}); e != nil {
		t.Fatal(e)
	}
	_ = a.Store.Put(ctx, "example", "record", map[string]string{"state": "before"})
	backup, e := a.Store.Backup(ctx, filepath.Join(dir, "backups"))
	if e != nil {
		t.Fatal(e)
	}
	_ = a.Store.Put(ctx, "example", "record", map[string]string{"state": "after"})
	if e = a.setControl(ctx, true); e != nil {
		t.Fatal(e)
	}
	if _, e = a.prepareRestore(ctx, backup, true); e != nil {
		t.Fatal(e)
	}
	a.Close()
	if e = ApplyPendingRestore(dir); e != nil {
		t.Fatal(e)
	}
	a, e = New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	var v map[string]string
	if e = a.Store.Get(ctx, "example", "record", &v); e != nil || v["state"] != "before" {
		t.Fatal("snapshot not restored", v, e)
	}
	if !a.paused() {
		t.Fatal("restored automation running")
	}
	if a.Settings().TMDB.APIKey != "test-only-key" {
		t.Fatal("encrypted config lost")
	}
}
func TestLogRedactionAndFiltering(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	secret := "test-only-sensitive-value"
	_ = a.saveSettings(ctx, Settings{TMDB: TMDBConfig{APIKey: secret}})
	a.log("WARN", "test", "failed", "url=https://example.invalid/?token=x secret="+secret, "wf1", "job1")
	entries, e := a.readLogs(map[string]string{"level": "WARN", "job_id": "job1"})
	if e != nil || len(entries) != 1 {
		t.Fatal(entries, e)
	}
	b, _ := json.Marshal(entries)
	if strings.Contains(string(b), secret) || strings.Contains(string(b), "example.invalid") {
		t.Fatal("sensitive diagnostic")
	}
	if _, e = time.Parse(time.RFC3339Nano, entries[0]["time"].(string)); e != nil {
		t.Fatal(e)
	}
	_, _ = io.Discard.Write(b)
}
