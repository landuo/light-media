package app

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"light-media/web"
)

// An explicit owned development fixture for real browser interaction. The
// ordinary test suite creates no server or data when its private env is absent.
func TestAutoBackupBrowserFixture(t *testing.T) {
	stage := os.Getenv("LIGHT_MEDIA_OWNED_AUTO_BACKUP_UI_STAGE")
	if stage == "" {
		return
	}
	if !filepath.IsAbs(stage) || !strings.HasPrefix(filepath.Base(stage), "ui-fixture-stage.") {
		t.Fatal("unowned browser fixture stage")
	}
	owner, e := os.ReadFile(filepath.Join(stage, ".owner"))
	if e != nil || len(strings.TrimSpace(string(owner))) < 20 {
		t.Fatal("owned fixture marker missing")
	}
	data, e := os.MkdirTemp(stage, "owned-data-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(data)
	a, e := New(Options{DataDir: data, Web: web.Handler()})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	autoTestInstall(t, a)
	a.pluginAutoBackup.afterEffect = func(kind string) error {
		if kind == "archive_write" {
			return errors.New("owned fixture lost archive reply")
		}
		return nil
	}
	if _, e = a.pluginStore.EnqueueRun(t.Context(), "AutoBackup", "run", nil); e != nil {
		t.Fatal(e)
	}
	run := autoTestExecute(t, a, autoTestClaim(t, a))
	if run.State != "reconcile_pending" {
		t.Fatal("fixture not unknown")
	}
	a.pluginAutoBackup.afterEffect = nil
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	ready, _ := json.Marshal(map[string]string{"url": server.URL, "run_id": run.ID, "data_dir": data})
	if e = os.WriteFile(filepath.Join(stage, "ready.json"), ready, 0600); e != nil {
		t.Fatal(e)
	}
	for deadline := time.Now().Add(10 * time.Minute); time.Now().Before(deadline); {
		if _, e = os.Stat(filepath.Join(stage, "stop")); e == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("owned browser fixture expired without completion")
}
