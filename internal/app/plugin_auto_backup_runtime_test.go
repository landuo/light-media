package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	ab "light-media/internal/plugins/autobackup"
	seed "light-media/internal/plugins/cleaninvalidseed"
	"light-media/internal/plugins/uninstall"
)

func autoTestClaim(t *testing.T, a *App) PluginRun {
	t.Helper()
	run, e := a.pluginStore.claimRunForInstance(context.Background(), "AutoBackup")
	if e != nil {
		t.Fatal(e)
	}
	return run
}
func autoTestExecute(t *testing.T, a *App, run PluginRun) PluginRun {
	t.Helper()
	a.executePluginRun(context.Background(), run)
	got, e := a.pluginStore.Run(context.Background(), run.ID)
	if e != nil {
		t.Fatal(e)
	}
	if len(a.pluginProcess) != 0 {
		t.Fatal("SDK process slot retained")
	}
	return got
}
func autoTestSuccess(t *testing.T, a *App, run PluginRun) PluginRun {
	t.Helper()
	got := autoTestExecute(t, a, run)
	if got.State != "succeeded" {
		t.Fatal("actual App execution", got.Action, got.State, got.ErrorCode)
	}
	return got
}
func autoTestInstall(t *testing.T, a *App) *http.Cookie {
	t.Helper()
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	w := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"AutoBackup"}`, cookie, "")
	if w.Code != 200 {
		t.Fatal("actual install", w.Code, w.Body.String())
	}
	autoTestSuccess(t, a, autoTestClaim(t, a))
	return cookie
}
func autoTestConfig(t *testing.T, a *App, cookie *http.Cookie, config map[string]any) {
	t.Helper()
	stored, e := a.pluginStore.Config(context.Background(), "AutoBackup")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(map[string]any{"revision": stored.Revision, "config": config})
	w := callTest(t, a.Handler(), "PUT", "/api/plugins/AutoBackup/config", string(raw), cookie, "")
	if w.Code != 200 {
		t.Fatal("actual config", w.Code, w.Body.String())
	}
	autoTestSuccess(t, a, autoTestClaim(t, a))
}
func autoTestRun(t *testing.T, a *App) PluginRun {
	t.Helper()
	if _, e := a.pluginStore.EnqueueRun(context.Background(), "AutoBackup", "run", nil); e != nil {
		t.Fatal(e)
	}
	return autoTestSuccess(t, a, autoTestClaim(t, a))
}
func autoTestFiles(t *testing.T, a *App) []string {
	t.Helper()
	v, e := filepath.Glob(filepath.Join(a.Dir, "plugin-private", "AutoBackup", "backups", "bk*.zip"))
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestPluginAutoBackupActualInstallOnceCronAndOpaqueAdmission(t *testing.T) {
	a := newTestApp(t)
	cookie := autoTestInstall(t, a)
	if w := callTest(t, a.Handler(), "GET", "/api/plugins/AutoBackup/form", "", cookie, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "webdav_max_count") {
		t.Fatal("full original form", w.Code)
	}
	// Due instant is real and stable; onlyonce works with enabled=false.
	now := time.Now()
	a.pluginAutoBackup.now = func() time.Time { return now }
	autoTestConfig(t, a, cookie, map[string]any{"enabled": false, "onlyonce": true})
	stored, e := a.pluginStore.Config(context.Background(), "AutoBackup")
	if e != nil || !strings.Contains(string(stored.Value), `"onlyonce":false`) {
		t.Fatal("once flag", e)
	}
	var due string
	if e = a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE instance_id='AutoBackup' AND service_id='AutoBackup.once' AND active=1`).Scan(&due); e != nil {
		t.Fatal(e)
	}
	at, e := time.Parse(time.RFC3339Nano, due)
	if e != nil || !at.Equal(now.Add(3*time.Second)) {
		t.Fatal("once deadline changed")
	}
	if e = a.initializeNativePlugins(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e = a.pluginStore.claimRunForInstance(context.Background(), "AutoBackup"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("startup recreated once", e)
	}
	n, e := a.pluginStore.enqueueDuePluginSchedules(context.Background(), at)
	if e != nil || n != 1 {
		t.Fatal("once dispatch", n, e)
	}
	run := autoTestClaim(t, a)
	if run.Action != "once" {
		t.Fatal("wrong once action")
	}
	got := autoTestSuccess(t, a, run)
	if len(autoTestFiles(t, a)) != 1 {
		t.Fatal("once did not archive")
	}
	if e = a.pluginStore.recoverDeferredRuns(context.Background()); e != nil {
		t.Fatal(e)
	}
	again, _ := a.pluginStore.Run(context.Background(), got.ID)
	if again.State != "succeeded" {
		t.Fatal("once receipt lost")
	}
	autoTestConfig(t, a, cookie, map[string]any{"enabled": true, "cron": "*/15 * * * *"})
	var count int
	e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id='AutoBackup' AND service_id='AutoBackup' AND active=1 AND next_due IS NOT NULL`).Scan(&count)
	if e != nil || count != 1 {
		t.Fatal("cron registration", e)
	}
	manual, e := a.pluginStore.enqueueManualPluginService(context.Background(), "AutoBackup", "AutoBackup", now)
	if e != nil {
		t.Fatal(e)
	}
	claimed := autoTestClaim(t, a)
	if claimed.ID != manual.ID {
		t.Fatal("manual service admission")
	}
	autoTestSuccess(t, a, claimed)
	i, _ := a.pluginStore.Installation(context.Background(), "AutoBackup")
	if _, e = a.pluginAutoBackupHost(context.Background(), i, &claimed, ab.ExecuteCapability, json.RawMessage(`{}`)); e == nil {
		t.Fatal("SDK bypass accepted")
	}
}
func TestPluginAutoBackupConsistentSnapshotRestoreActualApp(t *testing.T) {
	a := newTestApp(t)
	cookie := autoTestInstall(t, a)
	settings := a.Settings()
	settings.TMDB.APIKey = "owned-synthetic-key-never-a-real-credential"
	settings.APIToken = "owned-synthetic-api"
	if e := a.saveSettings(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	if e := a.Store.Put(context.Background(), "owned-backup-stable", "baseline", map[string]string{"value": "稳定业务记录"}); e != nil {
		t.Fatal(e)
	}
	if _, e := a.Store.DB.Exec(`CREATE TABLE owned_backup_pair(id INTEGER PRIMARY KEY,value INTEGER NOT NULL);INSERT INTO owned_backup_pair VALUES(1,0),(2,0);`); e != nil {
		t.Fatal(e)
	}
	if e := a.writePluginCategory(customJapaneseMovieCategory); e != nil {
		t.Fatal(e)
	}
	autoTestConfig(t, a, cookie, map[string]any{"enabled": false})
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var writes atomic.Int64
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; ctx.Err() == nil; i++ {
			tx, e := a.Store.DB.BeginTx(ctx, nil)
			if e != nil {
				return
			}
			_, e = tx.ExecContext(ctx, `UPDATE owned_backup_pair SET value=?`, i)
			if e == nil {
				e = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
			if e != nil {
				return
			}
			writes.Add(1)
		}
	}()
	autoTestRun(t, a)
	cancel()
	wg.Wait()
	files := autoTestFiles(t, a)
	if len(files) != 1 || writes.Load() == 0 {
		t.Fatal("no actual concurrent backup", len(files), writes.Load())
	}
	st, e := os.Stat(files[0])
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal("archive mode", e)
	}
	restoredDir := t.TempDir()
	reader, e := zip.OpenReader(files[0])
	if e != nil {
		t.Fatal(e)
	}
	names := map[string]bool{}
	for _, f := range reader.File {
		if f.Name != "light-media.db" && f.Name != "secret.key" && f.Name != "category.yaml" {
			t.Fatal("unexpected archive entry")
		}
		names[f.Name] = true
		if f.Mode().Perm() != 0600 {
			t.Fatal("entry mode")
		}
		in, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		out, e := os.OpenFile(filepath.Join(restoredDir, f.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		_, e = io.Copy(out, in)
		_ = in.Close()
		_ = out.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
	_ = reader.Close()
	if len(names) != 3 {
		t.Fatal("actual app layout omitted database/key")
	}
	restored, e := New(Options{DataDir: restoredDir})
	if e != nil {
		t.Fatal("restored actual New", e)
	}
	defer restored.Close()
	category, e := os.ReadFile(filepath.Join(restored.Dir, "category.yaml"))
	if e != nil || string(category) != customJapaneseMovieCategory {
		t.Fatal("required category config lost", e)
	}
	if restored.Settings().TMDB.APIKey != settings.TMDB.APIKey || restored.Settings().APIToken != settings.APIToken {
		t.Fatal("restored encrypted config cannot decrypt")
	}
	var stable map[string]string
	if e = restored.Store.Get(context.Background(), "owned-backup-stable", "baseline", &stable); e != nil || stable["value"] != "稳定业务记录" {
		t.Fatal("stable record lost", e)
	}
	var low, high int
	if e = restored.Store.DB.QueryRow(`SELECT MIN(value),MAX(value) FROM owned_backup_pair`).Scan(&low, &high); e != nil || low != high {
		t.Fatal("inconsistent snapshot", e)
	}
	var integrity string
	if e = restored.Store.DB.QueryRow(`PRAGMA quick_check`).Scan(&integrity); e != nil || integrity != "ok" {
		t.Fatal("snapshot integrity", e)
	}
}
func TestPluginAutoBackupActualHTTPAPIAuthAndOriginalResponse(t *testing.T) {
	a := newTestApp(t)
	autoTestInstall(t, a)
	settings := a.Settings()
	settings.APIToken = "owned-api-test-only"
	if e := a.saveSettings(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	if w := callTest(t, a.Handler(), "GET", "/api/v1/plugin/AutoBackup/backup?apikey=wrong", "", nil, ""); w.Code != 401 {
		t.Fatal("unauthorized route", w.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); a.pluginWorker(ctx) }()
	w := callTest(t, a.Handler(), "GET", "/api/v1/plugin/AutoBackup/backup?apikey="+settings.APIToken, "", nil, "")
	cancel()
	<-done
	if w.Code != 200 {
		t.Fatal("actual API", w.Code, w.Body.String())
	}
	var result []any
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result) != 2 || result[0] != true {
		t.Fatal("source tuple response missing")
	}
	if len(autoTestFiles(t, a)) != 1 {
		t.Fatal("API did not archive")
	}
	var count int
	if e := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_api_calls`).Scan(&count); e != nil || count != 1 {
		t.Fatal("actual API authority missing", e)
	}
}
func TestPluginAutoBackupUnknownArchiveReceiptNeverReplayed(t *testing.T) {
	dir := t.TempDir()
	a, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if a != nil {
			_ = a.Close()
		}
	}()
	autoTestInstall(t, a)
	a.pluginAutoBackup.afterEffect = func(kind string) error {
		if kind == "archive_write" {
			return errors.New("owned lost archive acknowledgment")
		}
		return nil
	}
	_, e = a.pluginStore.EnqueueRun(context.Background(), "AutoBackup", "run", nil)
	if e != nil {
		t.Fatal(e)
	}
	run := autoTestExecute(t, a, autoTestClaim(t, a))
	if run.State != "reconcile_pending" || len(autoTestFiles(t, a)) != 1 {
		t.Fatal("ambiguous archive did not remain unknown", run.State)
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a, e = New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	useBuiltInPluginProcess(t, a)
	if e = a.initializeNativePlugins(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e = a.pluginStore.claimRunForInstance(context.Background(), "AutoBackup"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("startup replayed backup", e)
	}
	_, e = a.pluginStore.EnqueueRun(context.Background(), "AutoBackup", "run", nil)
	if e != nil {
		t.Fatal(e)
	}
	next := autoTestExecute(t, a, autoTestClaim(t, a))
	if next.State != "failed" || next.ErrorCode != autoBackupBlockedByUnknown {
		t.Fatal("zero-effect new action lacks a terminal rejection", next.State, next.ErrorCode)
	}
	old, err := a.pluginStore.Run(context.Background(), run.ID)
	if err != nil || old.State != "reconcile_pending" || !bytes.Equal(old.Input, run.Input) {
		t.Fatal("original unknown admission changed", err)
	}
	if len(autoTestFiles(t, a)) != 1 {
		t.Fatal("unknown archive repeated")
	}
	if _, e = a.retryPluginDeferred(context.Background(), "AutoBackup", run.ID); e == nil {
		t.Fatal("unknown backup retry allowed")
	}
}
func TestPluginAutoBackupKnownSourceFailureAndHistoricalProof(t *testing.T) {
	a := newTestApp(t)
	cookie := autoTestInstall(t, a)
	autoTestConfig(t, a, cookie, map[string]any{"cnt": "invalid-int"})
	_, e := a.pluginStore.EnqueueRun(context.Background(), "AutoBackup", "run", nil)
	if e != nil {
		t.Fatal(e)
	}
	run := autoTestExecute(t, a, autoTestClaim(t, a))
	if run.State != "failed" || run.ErrorCode != "AUTO_BACKUP_SOURCE_FAILED" || len(autoTestFiles(t, a)) != 1 {
		t.Fatal("known failure misclassified", run.State, run.ErrorCode)
	}
	if _, e = a.Store.DB.Exec(`UPDATE plugin_runs SET state='running' WHERE id=?`, run.ID); e != nil {
		t.Fatal(e)
	}
	if e = a.pluginStore.recoverDeferredRuns(context.Background()); e != nil {
		t.Fatal(e)
	}
	got, _ := a.pluginStore.Run(context.Background(), run.ID)
	if got.State != "failed" || got.ErrorCode != run.ErrorCode {
		t.Fatal("known failure receipt lost")
	}
}
func TestPluginAutoBackupLifecycleWaitsOnlyInstanceGate(t *testing.T) {
	a := newTestApp(t)
	cookie := autoTestInstall(t, a)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	a.pluginAutoBackup.afterEffect = func(kind string) error {
		if kind == "sqlite_snapshot" {
			once.Do(func() { close(entered); <-release })
		}
		return nil
	}
	_, e := a.pluginStore.EnqueueRun(context.Background(), "AutoBackup", "run", nil)
	if e != nil {
		t.Fatal(e)
	}
	run := autoTestClaim(t, a)
	done := make(chan struct{})
	go func() { a.executePluginRun(context.Background(), run); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("backup never reached snapshot hook")
	}
	// A settings save requires pluginMu. It must finish while backup is paused.
	saved := make(chan error, 1)
	go func() { saved <- a.saveSettings(context.Background(), a.Settings()) }()
	select {
	case e := <-saved:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("long I/O retained global lock")
	}
	changed := make(chan int, 1)
	go func() {
		w := callTest(t, a.Handler(), "POST", "/api/plugins/AutoBackup/reset", "", cookie, "")
		changed <- w.Code
	}()
	select {
	case <-changed:
		close(release)
		t.Fatal("reset bypassed source gate")
	case <-time.After(40 * time.Millisecond):
	}
	close(release)
	<-done
	select {
	case code := <-changed:
		if code != 200 {
			t.Fatal("reset", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reset stuck")
	}
}
func TestPluginAutoBackupOwnedWebDAVRetentionAndNoticePipeline(t *testing.T) {
	var mu sync.Mutex
	files := map[string][]byte{"bk_20240101000000.zip": []byte("owned-old")}
	var writes, deletes int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		login, password, ok := r.BasicAuth()
		if !ok || login != "owned" || password != "owned-only" {
			w.WriteHeader(401)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case "HEAD":
			w.WriteHeader(200)
		case "PROPFIND":
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(207)
			fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:href>/dav/</d:href></d:response>`)
			for name := range files {
				fmt.Fprintf(w, `<d:response><d:href>/dav/%s</d:href></d:response>`, name)
			}
			fmt.Fprint(w, `</d:multistatus>`)
		case "PUT":
			body, e := io.ReadAll(r.Body)
			if e != nil {
				t.Error(e)
			}
			files[filepath.Base(r.URL.Path)] = body
			writes++
		case "DELETE":
			delete(files, filepath.Base(r.URL.Path))
			deletes++
		default:
			w.WriteHeader(405)
		}
	}))
	defer server.Close()
	a := newTestApp(t)
	cookie := autoTestInstall(t, a)
	autoTestConfig(t, a, cookie, map[string]any{"webdav_enabled": true, "webdav_hostname": server.URL + "/dav", "webdav_login": "owned", "webdav_password": "owned-only", "webdav_max_count": 1, "notify": true})
	run := autoTestRun(t, a)
	mu.Lock()
	actualWrites, actualDeletes := writes, deletes
	mu.Unlock()
	if actualWrites != 1 || actualDeletes != 1 {
		t.Fatal("real WebDAV side effects", actualWrites, actualDeletes)
	}
	s, _, e := a.pluginStore.autoBackupRead(context.Background(), a.Store.DB, run.InstanceID, run.Generation)
	if e != nil {
		t.Fatal(e)
	}
	kinds := map[string]bool{}
	for _, effect := range s.Runs[run.ID].Effects {
		kinds[effect.Kind] = effect.Phase == "confirmed"
	}
	if !kinds["webdav_put"] || !kinds["webdav_delete"] || !kinds["notice"] {
		t.Fatal("durable side effects missing")
	}
	var route string
	if e = a.Store.DB.QueryRow(`SELECT id FROM plugin_notice_routes WHERE instance_id='AutoBackup'`).Scan(&route); e != nil {
		t.Fatal(e)
	}
	r, _, e := a.pluginStore.readNoticeRoute(context.Background(), a.Store.DB, route)
	if e != nil || !r.Complete || r.Kind != "auto_backup" {
		t.Fatal("actual source notice route", e)
	}
	// Read state changes remain mutable; the original full private source is fixed.
	var notification Notification
	if e = a.Store.Get(context.Background(), "notification", r.HistoryID, &notification); e != nil {
		t.Fatal(e)
	}
	notification.Read = true
	if e = a.Store.Put(context.Background(), "notification", notification.ID, notification); e != nil {
		t.Fatal(e)
	}
	if _, _, e = a.pluginStore.readNoticeRoute(context.Background(), a.Store.DB, route); e != nil {
		t.Fatal("read state rejected", e)
	}
	notification.Message += "forged"
	if e = a.Store.Put(context.Background(), "notification", notification.ID, notification); e != nil {
		t.Fatal(e)
	}
	if _, _, e = a.pluginStore.readNoticeRoute(context.Background(), a.Store.DB, route); e == nil {
		t.Fatal("forged public history accepted")
	}
}
func TestPluginAutoBackupJournalTamperAndPathGrant(t *testing.T) {
	a := newTestApp(t)
	cookie := autoTestInstall(t, a)
	outside := t.TempDir()
	autoTestConfig(t, a, cookie, map[string]any{"back_path": outside})
	_, e := a.pluginStore.EnqueueRun(context.Background(), "AutoBackup", "run", nil)
	if e != nil {
		t.Fatal(e)
	}
	run := autoTestExecute(t, a, autoTestClaim(t, a))
	if run.State != "failed" {
		t.Fatal("ungranted path accepted", run.State)
	}
	entries, e := os.ReadDir(outside)
	if e != nil || len(entries) != 0 {
		t.Fatal("ungranted path written")
	}
	_, e = a.Store.DB.Exec(`UPDATE plugin_auto_backup_state SET sealed=x'000102' WHERE instance_id='AutoBackup'`)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = a.pluginStore.autoBackupRead(context.Background(), a.Store.DB, run.InstanceID, run.Generation); e == nil {
		t.Fatal("journal tampering accepted")
	}
}

func TestPluginAutoBackupLocalRetentionAndWarmColdSourceState(t *testing.T) {
	dir := t.TempDir()
	a, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if a != nil {
			_ = a.Close()
		}
	}()
	cookie := autoTestInstall(t, a)
	var ticks atomic.Int64
	anchor := time.Now()
	a.pluginAutoBackup.now = func() time.Time { return anchor.Add(time.Duration(ticks.Load()) * time.Second) }
	autoTestConfig(t, a, cookie, map[string]any{"cnt": 1, "enabled": true, "cron": "0 0 * * *"})
	autoTestRun(t, a)
	ticks.Add(1)
	autoTestRun(t, a)
	if len(autoTestFiles(t, a)) != 1 {
		t.Fatal("local retention not applied")
	}
	// The original falsey config retains its source object's previous fields.
	autoTestConfig(t, a, cookie, map[string]any{})
	stored, e := a.pluginStore.Config(context.Background(), "AutoBackup")
	if e != nil || string(stored.Value) != "{}" {
		t.Fatal("stored falsey config normalized")
	}
	s, _, e := a.pluginStore.autoBackupRead(context.Background(), a.Store.DB, "AutoBackup", 1)
	if e != nil {
		t.Fatal(e)
	}
	var warm map[string]any
	_ = json.Unmarshal(s.Config, &warm)
	if warm["enabled"] != true || warm["cnt"] != float64(1) {
		t.Fatal("warm source config erased")
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a, e = New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	useBuiltInPluginProcess(t, a)
	ticks.Add(1)
	autoTestRun(t, a)
	if len(autoTestFiles(t, a)) != 1 {
		t.Fatal("cold owner did not restore original source object")
	}
}
func TestPluginAutoBackupAtomicOnceSQLFailureDoesNotPublish(t *testing.T) {
	a := newTestApp(t)
	cookie := autoTestInstall(t, a)
	stored, e := a.pluginStore.Config(context.Background(), "AutoBackup")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(map[string]any{"revision": stored.Revision, "config": map[string]any{"onlyonce": true}})
	w := callTest(t, a.Handler(), "PUT", "/api/plugins/AutoBackup/config", string(raw), cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if _, e = a.Store.DB.Exec(`CREATE TRIGGER owned_abort_backup_schedule BEFORE INSERT ON plugin_schedules WHEN NEW.instance_id='AutoBackup' BEGIN SELECT RAISE(ABORT,'owned schedule failure'); END`); e != nil {
		t.Fatal(e)
	}
	run := autoTestExecute(t, a, autoTestClaim(t, a))
	if run.State != "failed" {
		t.Fatal("known SQL rollback", run.State)
	}
	latest, e := a.pluginStore.Config(context.Background(), "AutoBackup")
	if e != nil || latest.Revision != stored.Revision+1 || !strings.Contains(string(latest.Value), `"onlyonce":true`) {
		t.Fatal("once flag consumed despite rolled back schedule", e)
	}
	var count int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id='AutoBackup' AND active=1`).Scan(&count); e != nil || count != 0 {
		t.Fatal("partial schedule published", e)
	}
}
func TestPluginAutoBackupBulkUninstallUsesRealOwnerAndKeepsArtifacts(t *testing.T) {
	a := newTestApp(t)
	autoTestInstall(t, a)
	autoTestRun(t, a)
	files := autoTestFiles(t, a)
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"AutoBackup"}, "clear_config": true, "clear_data": true}, true)
	got := autoTestExecute(t, a, run)
	if got.State != "succeeded" {
		t.Fatal("actual bulk uninstall", got.State, got.ErrorCode)
	}
	i, e := a.pluginStore.Installation(context.Background(), "AutoBackup")
	if e != nil || i.Installed {
		t.Fatal("bulk uninstall did not publish", e)
	}
	if _, e = os.Stat(files[0]); e != nil {
		t.Fatal("uninstall deleted backup artifacts", e)
	}
}
func TestPluginAutoBackupQueuedAPIRevocationAndWrongQueryOriginalResult(t *testing.T) {
	a := newTestApp(t)
	autoTestInstall(t, a)
	settings := a.Settings()
	settings.APIToken = "owned-initial-key"
	if e := a.saveSettings(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	installation, _ := a.pluginStore.Installation(context.Background(), "AutoBackup")
	request := plugins.HTTPRequest{RequestID: "req_ownedAutoBackup", Key: "backup", Method: "GET", Path: "/backup", Params: map[string]string{}, CredentialMatches: map[string]bool{"apikey": true}}
	scope := pluginAPIScope{Key: "backup", Method: "GET", RequestID: request.RequestID, Authority: pluginAPIAuthority{Kind: "key", Proof: a.pluginAPIKeyProof(settings.APIToken)}}
	run, e := a.enqueuePluginHTTP(context.Background(), installation, request, scope)
	if e != nil {
		t.Fatal(e)
	}
	settings.APIToken = "owned-rotated-key"
	if e = a.saveSettings(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	claimed := autoTestClaim(t, a)
	if claimed.ID != run.ID {
		t.Fatal("queued API identity")
	}
	got := autoTestExecute(t, a, claimed)
	if got.ErrorCode != "PLUGIN_API_AUTH_REVOKED" || len(autoTestFiles(t, a)) != 0 {
		t.Fatal("revoked API wrote", got.State, got.ErrorCode)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); a.pluginWorker(ctx) }()
	req := httptest.NewRequest("GET", "/api/v1/plugin/AutoBackup/backup?apikey=incorrect-query", nil)
	req.Header.Set("X-API-KEY", settings.APIToken)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	cancel()
	<-done
	if w.Code != 200 {
		t.Fatal("original explicit query check", w.Code, w.Body.String())
	}
	var response map[string]any
	if json.Unmarshal(w.Body.Bytes(), &response) != nil || response["success"] != false || response["message"] != "API密钥错误" || len(autoTestFiles(t, a)) != 0 {
		t.Fatal("source invalid query response changed")
	}
}

func TestPluginAutoBackupConfigureCommitLostReplyKeepsOncePlan(t *testing.T) {
	dir := t.TempDir()
	a, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if a != nil {
			_ = a.Close()
		}
	}()
	cookie := autoTestInstall(t, a)
	a.pluginAutoBackup.afterEffect = func(kind string) error {
		if kind == "configure_commit" {
			return errors.New("owned configure commit reply lost")
		}
		return nil
	}
	autoTestConfig(t, a, cookie, map[string]any{"onlyonce": true, "cron": "15 4 * * *", "cnt": 3})
	var due string
	if e = a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE instance_id='AutoBackup' AND service_id='AutoBackup.once'`).Scan(&due); e != nil {
		t.Fatal(e)
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a, e = New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	useBuiltInPluginProcess(t, a)
	if e = a.initializeNativePlugins(context.Background()); e != nil {
		t.Fatal(e)
	}
	var after string
	if e = a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE instance_id='AutoBackup' AND service_id='AutoBackup.once'`).Scan(&after); e != nil || after != due {
		t.Fatal("once deadline reset after accepted configure", e)
	}
	if _, e = a.pluginStore.claimRunForInstance(context.Background(), "AutoBackup"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("accepted configure replayed", e)
	}
	i, _ := a.pluginStore.Installation(context.Background(), "AutoBackup")
	form, e := a.pluginForm(context.Background(), i)
	if e != nil {
		t.Fatal(e)
	}
	model := form["model"].(map[string]json.RawMessage)
	if string(model["cron"]) != `"15 4 * * *"` || string(model["cnt"]) != "3" || string(model["onlyonce"]) != "false" {
		t.Fatal("source form lost persisted fields")
	}
}

func TestPluginAutoBackupSnapshotCutCancelsOnlyOwnRunAndPreservesUnknown(t *testing.T) {
	for _, action := range []string{"run", "once", "api"} {
		t.Run(action, func(t *testing.T) {
			a := newTestApp(t)
			cookie := autoTestInstall(t, a)
			settings := a.Settings()
			settings.APIToken = "owned-cut-test-key"
			if e := a.saveSettings(context.Background(), settings); e != nil {
				t.Fatal(e)
			}
			// Another real plugin has an ambiguous native write. Its encrypted journal
			// and source effect evidence must survive the AutoBackup-only cut untouched.
			daemon := cleanSeedOwnerDaemonNew(t)
			daemon.settings(a)
			row := func(hash string) map[string]any {
				return map[string]any{"hash": hash, "name": "owned", "content_path": "/owned-no-data-delete", "category": "movies", "tags": "", "state": "uploading", "size": 4, "total_size": 4, "completed": 4, "progress": 1, "ratio": 1}
			}
			daemon.rows, daemon.failWrite = []map[string]any{row(strings.Repeat("a", 40)), row(strings.Repeat("b", 40))}, true
			cleanInit := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cleanSeedOwnerConfig(map[string]any{"delete_invalid_torrents": true}), true)
			autoTestSuccess(t, a, cleanInit)
			if _, e := a.pluginStore.EnqueueRun(context.Background(), "CleanInvalidSeed", "run", nil); e != nil {
				t.Fatal(e)
			}
			other, e := a.pluginStore.claimRunForInstance(context.Background(), "CleanInvalidSeed")
			if e != nil {
				t.Fatal(e)
			}
			other = autoTestExecute(t, a, other)
			if other.State != "reconcile_pending" {
				t.Fatal("owned other plugin fixture not unknown", other.State)
			}
			digest := func(db *sql.DB) string {
				t.Helper()
				rows, e := db.Query(`SELECT sealed FROM plugin_clean_seed_meta WHERE instance_id='CleanInvalidSeed' UNION ALL SELECT sealed FROM plugin_clean_seed_rows WHERE instance_id='CleanInvalidSeed' UNION ALL SELECT sealed FROM plugin_clean_seed_core_receipts WHERE instance_id='CleanInvalidSeed' ORDER BY sealed`)
				if e != nil {
					t.Fatal(e)
				}
				defer rows.Close()
				h := sha256.New()
				for rows.Next() {
					var b []byte
					if e = rows.Scan(&b); e != nil {
						t.Fatal(e)
					}
					h.Write(b)
				}
				if e = rows.Err(); e != nil {
					t.Fatal(e)
				}
				return fmt.Sprintf("%x", h.Sum(nil))
			}
			before := digest(a.Store.DB)
			var current PluginRun
			switch action {
			case "run":
				if _, e = a.pluginStore.EnqueueRun(context.Background(), "AutoBackup", "run", nil); e != nil {
					t.Fatal(e)
				}
				current = autoTestClaim(t, a)
			case "once":
				autoTestConfig(t, a, cookie, map[string]any{"onlyonce": true})
				var due string
				if e = a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE instance_id='AutoBackup' AND service_id='AutoBackup.once'`).Scan(&due); e != nil {
					t.Fatal(e)
				}
				at, _ := time.Parse(time.RFC3339Nano, due)
				if n, e := a.pluginStore.enqueueDuePluginSchedules(context.Background(), at); e != nil || n != 1 {
					t.Fatal("once dispatch", n, e)
				}
				current = autoTestClaim(t, a)
			case "api":
				installation, _ := a.pluginStore.Installation(context.Background(), "AutoBackup")
				request := plugins.HTTPRequest{RequestID: "req_ownedCut", Key: "backup", Method: "GET", Path: "/backup", Params: map[string]string{}, CredentialMatches: map[string]bool{"apikey": true}}
				scope := pluginAPIScope{Key: "backup", Method: "GET", RequestID: request.RequestID, Authority: pluginAPIAuthority{Kind: "key", Proof: a.pluginAPIKeyProof(settings.APIToken)}}
				if _, e = a.enqueuePluginHTTP(context.Background(), installation, request, scope); e != nil {
					t.Fatal(e)
				}
				current = autoTestClaim(t, a)
			}
			autoTestSuccess(t, a, current)
			files := autoTestFiles(t, a)
			if len(files) != 1 {
				t.Fatal("owned snapshot missing")
			}
			target := t.TempDir()
			archive, e := zip.OpenReader(files[0])
			if e != nil {
				t.Fatal(e)
			}
			for _, entry := range archive.File {
				if entry.Name != "light-media.db" && entry.Name != "secret.key" {
					t.Fatal("unexpected archive entry")
				}
				in, e := entry.Open()
				if e != nil {
					t.Fatal(e)
				}
				out, e := os.OpenFile(filepath.Join(target, entry.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if e != nil {
					t.Fatal(e)
				}
				_, e = io.Copy(out, in)
				_ = in.Close()
				_ = out.Close()
				if e != nil {
					t.Fatal(e)
				}
			}
			_ = archive.Close()
			restored, e := New(Options{DataDir: target})
			if e != nil {
				t.Fatal(e)
			}
			defer restored.Close()
			useBuiltInPluginProcess(t, restored)
			if before != digest(restored.Store.DB) {
				t.Fatal("other unknown native effect evidence changed")
			}
			unknown, e := restored.pluginStore.Run(context.Background(), other.ID)
			if e != nil || unknown.State != "reconcile_pending" {
				t.Fatal("other unknown action changed", e)
			}
			old, e := restored.pluginStore.Run(context.Background(), current.ID)
			if e != nil || old.State != "canceled" || old.ErrorCode != "AUTO_BACKUP_SNAPSHOT_CUT" {
				t.Fatal("self snapshot cut not retained", e)
			}
			deferred, _, e := restored.pluginStore.readDeferred(context.Background(), restored.Store.DB, old)
			if e != nil || deferred.Phase != "snapshot_cut" || !validAutoBackupDeferredCut(deferred) {
				t.Fatal("cut deferred tombstone invalid", e)
			}
			state, _, e := restored.pluginStore.autoBackupRead(context.Background(), restored.Store.DB, "AutoBackup", current.Generation)
			if e != nil || !validAutoBackupCut(state.Runs[current.ID], "AutoBackup", current.Generation) {
				t.Fatal("cut owner tombstone invalid", e)
			}
			if action == "once" {
				var status string
				if e = restored.Store.DB.QueryRow(`SELECT state FROM plugin_schedule_occurrences WHERE run_id=?`, old.ID).Scan(&status); e != nil || status != "canceled" {
					t.Fatal("once occurrence replayable", e)
				}
			}
			if e = restored.initializeNativePlugins(context.Background()); e != nil {
				t.Fatal(e)
			}
			if _, e = restored.pluginStore.claimRunForInstance(context.Background(), "AutoBackup"); !errors.Is(e, sql.ErrNoRows) {
				t.Fatal("old source invocation replayed", e)
			}
			// This restore uses a different DataDir. Preserve the original source
			// path verbatim, then explicitly choose the new default through the
			// real configuration API; never silently write into the old root.
			autoTestConfig(t, restored, cookie, map[string]any{"back_path": nil})
			autoTestRun(t, restored)
			if len(autoTestFiles(t, restored)) != 1 {
				t.Fatal("explicit new backup denied after cut")
			}
			still, _ := restored.pluginStore.Run(context.Background(), old.ID)
			if still.State != "canceled" {
				t.Fatal("old invocation revived")
			}
		})
	}
}

func TestPluginAutoBackupOwnedDirectoryRetentionPinsLeaves(t *testing.T) {
	a := newTestApp(t)
	cookie := autoTestInstall(t, a)
	dest := filepath.Join(a.Dir, "plugin-private", "AutoBackup", "backups")
	old := filepath.Join(dest, "bk_old_staging")
	if e := os.MkdirAll(filepath.Join(old, "nested"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(old, "nested", "owned.txt"), []byte("owned old staging"), 0600); e != nil {
		t.Fatal(e)
	}
	outside := filepath.Join(t.TempDir(), "owned-keep.txt")
	if e := os.WriteFile(outside, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(old, "outside-link")); e != nil {
		t.Fatal(e)
	}
	autoTestConfig(t, a, cookie, map[string]any{"cnt": 1})
	run := autoTestRun(t, a)
	if _, e := os.Lstat(old); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("old staging tree not removed", e)
	}
	if b, e := os.ReadFile(outside); e != nil || string(b) != "keep" {
		t.Fatal("retention followed symlink", e)
	}
	state, _, e := a.pluginStore.autoBackupRead(context.Background(), a.Store.DB, run.InstanceID, run.Generation)
	if e != nil {
		t.Fatal(e)
	}
	steps := 0
	for _, effect := range state.Runs[run.ID].Effects {
		if effect.Kind == "local_retention_step" && effect.Phase == "confirmed" {
			steps++
		}
	}
	if steps != 4 {
		t.Fatal("missing pinned leaf receipts", steps)
	}
}

func TestPluginAutoBackupRetentionRejectsSameInodeModifiedLeaf(t *testing.T) {
	a := newTestApp(t)
	cookie := autoTestInstall(t, a)
	dest := filepath.Join(a.Dir, "plugin-private", "AutoBackup", "backups")
	old := filepath.Join(dest, "bk_owned_staging")
	if e := os.MkdirAll(filepath.Join(old, "nested"), 0700); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(old, "nested", "owned.txt")
	if e := os.WriteFile(target, []byte("original"), 0600); e != nil {
		t.Fatal(e)
	}
	// Keep a real owned file descriptor across the parent's atomic quarantine
	// rename. A same-inode write must not be mistaken for the listed old leaf.
	held, e := os.OpenFile(target, os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer held.Close()
	original, e := held.Stat()
	if e != nil {
		t.Fatal(e)
	}
	changed := []byte("new owned bytes after original retention manifest")
	wrote := false
	a.pluginAutoBackup.beforeRetentionLeaf = func(path string) {
		if filepath.Base(path) != "owned.txt" || wrote {
			return
		}
		wrote = true
		if _, e := held.WriteAt(changed, 0); e != nil {
			t.Fatal(e)
		}
		if e := held.Sync(); e != nil {
			t.Fatal(e)
		}
		current, e := held.Stat()
		if e != nil || !os.SameFile(original, current) {
			t.Fatal("test did not mutate same inode", e)
		}
	}
	autoTestConfig(t, a, cookie, map[string]any{"cnt": 1})
	if _, e := a.pluginStore.EnqueueRun(context.Background(), "AutoBackup", "run", nil); e != nil {
		t.Fatal(e)
	}
	run := autoTestExecute(t, a, autoTestClaim(t, a))
	if !wrote || run.State != "reconcile_pending" {
		t.Fatal("changed leaf was not quarantined as unknown", wrote, run.State)
	}
	found := false
	if e := filepath.WalkDir(dest, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Name() == "owned.txt" {
			b, e := os.ReadFile(path)
			if e != nil || !bytes.Equal(b, changed) {
				t.Fatal("changed bytes not preserved", e)
			}
			pin, e := os.Stat(path)
			if e != nil || !os.SameFile(original, pin) {
				t.Fatal("changed inode not preserved", e)
			}
			found = true
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if !found {
		t.Fatal("changed leaf deleted")
	}
	state, _, e := a.pluginStore.autoBackupRead(context.Background(), a.Store.DB, run.InstanceID, run.Generation)
	if e != nil {
		t.Fatal(e)
	}
	pending := false
	for _, effect := range state.Runs[run.ID].Effects {
		if effect.Kind == "local_retention" && effect.Phase == "started" {
			pending = true
		}
	}
	if !pending {
		t.Fatal("unknown retention evidence lost")
	}
}

func TestPluginAutoBackupHostTimezoneAndVersionedHistoricalNotice(t *testing.T) {
	t.Setenv("TZ", "America/New_York")
	dir := t.TempDir()
	a, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if a != nil {
			_ = a.Close()
		}
	}()
	cookie := autoTestInstall(t, a)
	now := time.Date(2026, 11, 1, 5, 58, 0, 0, time.UTC)
	a.pluginAutoBackup.now = func() time.Time { return now }
	autoTestConfig(t, a, cookie, map[string]any{"onlyonce": true, "enabled": true, "cron": "0 1 * * *", "notify": true})
	rows, e := a.Store.DB.Query(`SELECT service_id,definition FROM plugin_schedules WHERE instance_id='AutoBackup' AND active=1`)
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for rows.Next() {
		var id string
		var b []byte
		if e = rows.Scan(&id, &b); e != nil {
			t.Fatal(e)
		}
		raw, e := a.pluginStore.unseal("schedule", "AutoBackup", id, b)
		if e != nil {
			t.Fatal(e)
		}
		var def pluginScheduleDefinition
		if json.Unmarshal(raw, &def) != nil || def.Trigger.Timezone != "America/New_York" {
			t.Fatal("host date/cron TZ not bound")
		}
		count++
	}
	_ = rows.Close()
	if count != 2 {
		t.Fatal("date+cron not registered")
	}
	autoTestRun(t, a)
	var token, route string
	var sealed []byte
	if e = a.Store.DB.QueryRow(`SELECT token,sealed FROM plugin_auto_backup_notices WHERE instance_id='AutoBackup'`).Scan(&token, &sealed); e != nil {
		t.Fatal(e)
	}
	raw, e := a.pluginStore.unseal("run_result", "AutoBackup", "auto-backup-notice:"+token, sealed)
	if e != nil {
		t.Fatal(e)
	}
	var box autoBackupNotice
	if json.Unmarshal(raw, &box) != nil || box.Version != 2 || box.Timezone != "America/New_York" || box.CurrentTime != "2026-11-01 01:58:00" {
		t.Fatal("new version did not persist original timezone context")
	}
	if e = a.Store.DB.QueryRow(`SELECT id FROM plugin_notice_routes WHERE instance_id='AutoBackup'`).Scan(&route); e != nil {
		t.Fatal(e)
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	t.Setenv("TZ", "Europe/Berlin")
	a, e = New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = a.pluginStore.readNoticeRoute(context.Background(), a.Store.DB, route); e != nil {
		t.Fatal("historical timezone context rejected after cold restart", e)
	}
}
func TestPluginAutoBackupAuthenticatedAbandonKeepsUnknownEvidenceAndUnlocksNewRun(t *testing.T) {
	for _, kind := range []string{"manual", "once", "api"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			a, e := New(Options{DataDir: dir})
			if e != nil {
				t.Fatal(e)
			}
			defer func() {
				if a != nil {
					_ = a.Close()
				}
			}()
			cookie := autoTestInstall(t, a)
			settings := a.Settings()
			settings.APIToken = "owned-abandon-key"
			if e = a.saveSettings(context.Background(), settings); e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "manual":
				_, e = a.pluginStore.EnqueueRun(context.Background(), "AutoBackup", "run", nil)
			case "once":
				autoTestConfig(t, a, cookie, map[string]any{"onlyonce": true})
				var due string
				_ = a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE instance_id='AutoBackup' AND service_id='AutoBackup.once'`).Scan(&due)
				at, _ := time.Parse(time.RFC3339Nano, due)
				_, e = a.pluginStore.enqueueDuePluginSchedules(context.Background(), at)
			case "api":
				i, _ := a.pluginStore.Installation(context.Background(), "AutoBackup")
				req := plugins.HTTPRequest{RequestID: "req_ownedAbandon", Key: "backup", Method: "GET", Path: "/backup", Params: map[string]string{}, CredentialMatches: map[string]bool{"apikey": true}}
				_, e = a.enqueuePluginHTTP(context.Background(), i, req, pluginAPIScope{Key: "backup", Method: "GET", RequestID: req.RequestID, Authority: pluginAPIAuthority{Kind: "key", Proof: a.pluginAPIKeyProof(settings.APIToken)}})
			}
			if e != nil {
				t.Fatal(e)
			}
			a.pluginAutoBackup.afterEffect = func(stage string) error {
				if stage == "archive_write" {
					return errors.New("owned acknowledgment lost")
				}
				return nil
			}
			old := autoTestExecute(t, a, autoTestClaim(t, a))
			if old.State != "reconcile_pending" {
				t.Fatal("unknown fixture", old.State)
			}
			a.pluginAutoBackup.afterEffect = nil
			history := callTest(t, a.Handler(), "GET", "/api/plugins/AutoBackup/runs", "", cookie, "")
			if history.Code != 200 || !strings.Contains(history.Body.String(), `"abandon_allowed":true`) || !strings.Contains(history.Body.String(), "ZIP归档写入") || strings.Contains(history.Body.String(), a.Dir) || strings.Contains(history.Body.String(), settings.APIToken) {
				t.Fatal("bounded public recovery diagnosis missing or private values exposed", history.Code)
			}
			body, _ := json.Marshal(map[string]any{"run_id": old.ID, "confirm_abandon": true})
			for _, auth := range []struct {
				cookie *http.Cookie
				origin string
			}{{nil, ""}, {cookie, "http://evil.invalid"}} {
				w := callTest(t, a.Handler(), "POST", "/api/plugins/AutoBackup/abandon-unknown", string(body), auth.cookie, auth.origin)
				if w.Code == 200 {
					t.Fatal("unauthorized abandonment accepted")
				}
			}
			badBody, _ := json.Marshal(map[string]any{"run_id": old.ID, "confirm_abandon": false})
			if w := callTest(t, a.Handler(), "POST", "/api/plugins/AutoBackup/abandon-unknown", string(badBody), cookie, ""); w.Code == 200 {
				t.Fatal("missing explicit acknowledgment accepted")
			}
			before, _, e := a.pluginStore.autoBackupRead(context.Background(), a.Store.DB, old.InstanceID, old.Generation)
			if e != nil {
				t.Fatal(e)
			}
			effects := mustCleanSeedJSON(before.Runs[old.ID].Effects)
			if w := callTest(t, a.Handler(), "POST", "/api/plugins/AutoBackup/abandon-unknown", string(body), cookie, ""); w.Code != 200 {
				t.Fatal("actual abandonment", w.Code, w.Body.String())
			}
			after, _, e := a.pluginStore.autoBackupRead(context.Background(), a.Store.DB, old.InstanceID, old.Generation)
			r := after.Runs[old.ID]
			if e != nil || r.Phase != "abandoned_unknown" || !validAutoBackupResolution(r, old.InstanceID, old.Generation) || !bytes.Equal(effects, mustCleanSeedJSON(r.Effects)) {
				t.Fatal("unknown private effect evidence discarded", e)
			}
			if kind == "once" {
				var status string
				if e = a.Store.DB.QueryRow(`SELECT state FROM plugin_schedule_occurrences WHERE run_id=?`, old.ID).Scan(&status); e != nil || status != "canceled" {
					t.Fatal("old occurrence remained runnable")
				}
			}
			if e = a.Close(); e != nil {
				t.Fatal(e)
			}
			a, e = New(Options{DataDir: dir})
			if e != nil {
				t.Fatal(e)
			}
			useBuiltInPluginProcess(t, a)
			if e = a.initializeNativePlugins(context.Background()); e != nil {
				t.Fatal(e)
			}
			if _, e = a.pluginStore.claimRunForInstance(context.Background(), "AutoBackup"); !errors.Is(e, sql.ErrNoRows) {
				t.Fatal("abandoned run replayed", e)
			}
			autoTestRun(t, a)
			oldAgain, _ := a.pluginStore.Run(context.Background(), old.ID)
			if oldAgain.State != "canceled" || oldAgain.ErrorCode != "AUTO_BACKUP_ABANDONED_UNKNOWN" {
				t.Fatal("old run revived")
			}
			if w := callTest(t, a.Handler(), "POST", "/api/plugins/AutoBackup/reset", "", cookie, ""); w.Code != 200 {
				t.Fatal("resolved unknown still blocked configuration/reset", w.Code, w.Body.String())
			}
		})
	}
}
