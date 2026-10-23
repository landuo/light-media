package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/web"
)

// Opt-in loopback-only fixture. All database, keys and archives belong to the
// explicit synthetic stage; no real account or notification is configured.
func TestAutoBackupCurrentBrowserFixture(t *testing.T) {
	stage := os.Getenv("AUTOBACKUP_CURRENT_BROWSER_STAGE")
	if stage == "" {
		t.Skip("owned browser stage not requested")
	}
	if !filepath.IsAbs(stage) || !strings.HasPrefix(filepath.Base(stage), "ui-fixture-stage.") {
		t.Fatal("invalid owned stage")
	}
	marker, err := os.ReadFile(filepath.Join(stage, ".owner"))
	if err != nil || len(strings.TrimSpace(string(marker))) < 20 {
		t.Fatal("missing owner marker")
	}
	worker := os.Getenv("AUTOBACKUP_CURRENT_BROWSER_WORKER")
	if !filepath.IsAbs(worker) {
		t.Fatal("absolute owned server binary required")
	}
	binary, err := os.ReadFile(worker)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(binary)
	if hex.EncodeToString(digest[:]) != os.Getenv("AUTOBACKUP_CURRENT_BROWSER_WORKER_SHA256") {
		t.Fatal("server binary identity changed")
	}
	data, err := os.MkdirTemp(stage, "owned-data-")
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{DataDir: data, Web: web.Handler()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.pluginCommand = func(id string) (plugins.Command, error) {
		return plugins.Command{Path: worker, Args: []string{"--plugin-worker", id}}, nil
	}
	cookie := setupTestAdmin(t, a)
	w := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"AutoBackup"}`, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	autoTestSuccess(t, a, autoTestClaim(t, a))
	a.pluginAutoBackup.afterEffect = func(kind string) error {
		if kind == "archive_write" {
			return errors.New("owned UI lost archive acknowledgment")
		}
		return nil
	}
	if _, err = a.pluginStore.EnqueueRun(t.Context(), "AutoBackup", "run", nil); err != nil {
		t.Fatal(err)
	}
	unknown := autoTestExecute(t, a, autoTestClaim(t, a))
	if unknown.State != "reconcile_pending" {
		t.Fatal("unknown setup", unknown.State)
	}
	a.pluginAutoBackup.afterEffect = nil
	a.Start(context.Background())
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	ready, _ := json.Marshal(map[string]string{"url": server.URL, "run_id": unknown.ID, "data_dir": data, "worker_sha256": hex.EncodeToString(digest[:])})
	if err = os.WriteFile(filepath.Join(stage, "ready.json"), ready, 0600); err != nil {
		t.Fatal(err)
	}
	for until := time.Now().Add(20 * time.Minute); time.Now().Before(until); time.Sleep(100 * time.Millisecond) {
		if _, err = os.Stat(filepath.Join(stage, "stop")); err != nil {
			continue
		}
		run, e := a.pluginStore.Run(context.Background(), unknown.ID)
		if e != nil {
			t.Fatal(e)
		}
		archives := autoTestFiles(t, a)
		summary, _ := json.Marshal(map[string]any{"unknown_state": run.State, "unknown_error": run.ErrorCode, "archives": len(archives), "synthetic_only": true})
		if err = os.WriteFile(filepath.Join(stage, "final.json"), summary, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatal("owned browser fixture expired without stop")
}
