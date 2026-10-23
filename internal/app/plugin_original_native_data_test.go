package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPluginOriginalNativeDataClearStartsEmptyNamespaceKeepsFiles(t *testing.T) {
	a := newTestApp(t)
	originalNativeDataInstallFixture(t, a)
	ctx := context.Background()
	in, err := a.pluginStore.Installation(ctx, "AutoBackup")
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := a.pluginStore.autoBackupRead(ctx, a.Store.DB, in.InstanceID, in.Generation)
	if err != nil || len(before.Runs) == 0 {
		t.Fatalf("real init native data missing: %v", err)
	}
	cfg, err := a.pluginStore.Config(ctx, in.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.SaveData(ctx, in.InstanceID, "owned-history", 0, []string{"old"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "existing-backup.zip")
	content := []byte("owned archive must not be removed by del_data")
	if err = os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.deleteOriginalPluginData(ctx, in.InstanceID); err != nil {
		t.Fatal(err)
	}
	after, err := a.pluginStore.Installation(ctx, in.InstanceID)
	if err != nil || after.Generation != in.Generation+1 {
		t.Fatalf("namespace generation %v %v", after, err)
	}
	state, _, err := a.pluginStore.autoBackupRead(ctx, a.Store.DB, in.InstanceID, after.Generation)
	if err != nil || len(state.Runs) != 0 {
		t.Fatalf("old logical data reappeared: %v", err)
	}
	if _, err = a.pluginStore.Data(ctx, in.InstanceID, "owned-history"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("KV not cleared: %v", err)
	}
	got, err := a.pluginStore.Config(ctx, in.InstanceID)
	if err != nil || !bytes.Equal(got.Value, cfg.Value) {
		t.Fatalf("del_data must not delete config: %v", err)
	}
	file, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(file, content) {
		t.Fatalf("del_data touched files: %v", err)
	}
	// Old sealed audit records are not the active plugin save_data namespace.
	old, _, err := a.pluginStore.autoBackupRead(ctx, a.Store.DB, in.InstanceID, in.Generation)
	if err != nil || len(old.Runs) != len(before.Runs) {
		t.Fatalf("old audit unexpectedly rewritten: %v", err)
	}
}

// This fixture follows the newly synchronous original lifecycle. The legacy
// autoTestInstall helper expects a pending init and is intentionally unchanged.
func originalNativeDataInstallFixture(t *testing.T, a *App) {
	t.Helper()
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"AutoBackup"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	// Explicit owned configuration is a separate user operation, not an injected
	// Form-default write during install. No once job or remote storage is enabled.
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/AutoBackup", `{"enabled":false,"onlyonce":false,"webdav_enabled":false,"back_path":""}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	var pending, succeeded int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id='AutoBackup' AND state IN ('pending','running')`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("synchronous initialization pending=%d: %v", pending, err)
	}
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id='AutoBackup' AND action='init' AND state='succeeded'`).Scan(&succeeded); err != nil || succeeded != 2 {
		t.Fatalf("install and explicit save init=%d: %v", succeeded, err)
	}
}
