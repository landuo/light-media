package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"light-media/internal/plugins/builtin"
)

func TestPluginAutoBackupStartupDoesNotHideSQLFailure(t *testing.T) {
	a := newTestApp(t)
	autoTestInstall(t, a)
	if _, err := a.Store.DB.Exec(`DROP TABLE plugin_auto_backup_roots`); err != nil {
		t.Fatal(err)
	}
	err := a.initializeNativePlugins(context.Background())
	if err == nil || autoBackupErrorCode(err) == "AUTO_BACKUP_EVIDENCE_MISSING" || !strings.Contains(err.Error(), "plugin_auto_backup_roots") {
		t.Fatalf("actual startup hid or reclassified a root query SQL failure: %v", err)
	}
}

func TestPluginAutoBackupStartupMissingEvidenceKeepsOtherPluginAvailable(t *testing.T) {
	a := newTestApp(t)
	autoTestInstall(t, a)
	ctx := context.Background()
	manifest, _, found := builtin.Lookup("SystemProcess")
	if !found {
		t.Fatal("real SystemProcess implementation missing")
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.Install(ctx, PluginInstallation{InstanceID: manifest.PluginID, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: manifest.SchemaVersion, Manifest: raw, DesiredEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.SaveConfig(ctx, manifest.PluginID, 0, map[string]any{"enabled": true}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`DELETE FROM plugin_auto_backup_roots WHERE instance_id='AutoBackup'`); err != nil {
		t.Fatal(err)
	}
	var countBefore int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id='AutoBackup'`).Scan(&countBefore); err != nil {
		t.Fatal(err)
	}
	if err = a.initializeNativePlugins(ctx); err != nil {
		t.Fatal("bounded missing backup evidence should leave other plugins available", err)
	}
	var countAfter int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id='AutoBackup'`).Scan(&countAfter); err != nil || countBefore != countAfter {
		t.Fatal("startup added a new backup admission despite missing evidence", countBefore, countAfter, err)
	}
	run, err := a.pluginStore.claimRunForInstance(ctx, "SystemProcess")
	if err != nil || run.Action != "init" {
		t.Fatal("unrelated actual plugin initialization was blocked", run.Action, err)
	}
	result := autoTestExecute(t, a, run)
	if result.State != "succeeded" {
		t.Fatal("unrelated real plugin could not execute", result.State, result.ErrorCode)
	}
}
