package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins/artifacts"
)

func TestOriginalPluginCodeBackupActualArtifactRestoreAndIndependentClone(t *testing.T) {
	oracleRaw, oracleErr := os.ReadFile("../../testdata/standard-market/backup-oracle.json")
	var oracle struct {
		Cases []struct {
			Kind         string
			BackupExists bool `json:"backup_exists"`
		}
	}
	if oracleErr != nil || json.Unmarshal(oracleRaw, &oracle) != nil {
		t.Fatal("fixed backup source oracle unavailable", oracleErr)
	}
	consumed := 0
	for _, row := range oracle.Cases {
		if (row.Kind == "reset_restores_then_consumes_backup" || row.Kind == "ordinary_start_consumes_without_restore") && !row.BackupExists {
			consumed++
		}
	}
	if consumed != 2 {
		t.Fatal("fixed reset/non-reset snapshot consumption expectations unavailable")
	}
	a, cookie, _ := originalArtifactCloneFixture(t)
	ctx := context.Background()
	const source = "OwnedModuleFixture"
	const clone = "OwnedModuleFixturebackup"
	for _, step := range []struct{ method, path, body string }{
		{"PUT", "/api/plugins/" + source, `{"enabled":false,"marker":"source"}`},
		{"POST", "/api/plugins/" + source + "/clone", `{"suffix":"Backup"}`},
		{"PUT", "/api/plugins/" + clone, `{"enabled":false,"marker":"independent-clone"}`},
	} {
		r := callTest(t, a.Handler(), step.method, step.path, step.body, cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	}
	a.wg.Wait()
	if err := a.pluginStore.originalBaseSaveData(ctx, clone, "history", json.RawMessage(`{"owned":"retained"}`)); err != nil {
		t.Fatal(err)
	}
	configBefore, err := a.pluginStore.Config(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	dataBefore, err := a.pluginStore.Data(ctx, clone, "history")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.backupOriginalPluginCodeFiles(ctx); err != nil {
		t.Fatal(err)
	}
	backupFiles := map[string]os.FileInfo{}
	for _, id := range []string{source, clone} {
		backup, err := artifacts.ReadInstalled(a.pluginCodeBackupRoot(), id, validatePluginArtifactContract)
		if err != nil {
			t.Fatal(err)
		}
		backupFiles[id], _ = os.Stat(backup.ExecutablePath)
	}
	a.closeOriginalSessions()
	if err := os.RemoveAll(a.pluginPackageRoot()); err != nil {
		t.Fatal(err)
	}
	if err := a.restoreOriginalPluginCodeFiles(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.initializeOriginalPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	for id, marker := range map[string]string{source: "source", clone: "independent-clone"} {
		value, ok := a.pluginRunningInstances.Load(id)
		if !ok {
			t.Fatal("restored executable did not start", id)
		}
		state := originalArtifactSnapshot(t, value.(*originalPluginInstance))
		cfg, _ := state["config"].(map[string]any)
		if state["version"] != "1.0" || cfg["marker"] != marker {
			t.Fatalf("restored process identity/config %s: %+v", id, state)
		}
		code, err := artifacts.ReadInstalled(a.pluginPackageRoot(), id, validatePluginArtifactContract)
		if err != nil {
			t.Fatal(err)
		}
		aFile, _ := os.Stat(code.ExecutablePath)
		if os.SameFile(aFile, backupFiles[id]) {
			t.Fatal("restored runtime shares backup file")
		}
	}
	if _, err := os.Stat(a.pluginCodeBackupRoot()); !os.IsNotExist(err) {
		t.Fatal("startup did not consume persistent backup", err)
	}
	configAfter, err := a.pluginStore.Config(ctx, clone)
	if err != nil || configAfter.Revision != configBefore.Revision || !bytes.Equal(configAfter.Value, configBefore.Value) {
		t.Fatal("restore changed persistent config", err)
	}
	dataAfter, err := a.pluginStore.Data(ctx, clone, "history")
	if err != nil || !bytes.Equal(dataAfter.Value, dataBefore.Value) {
		t.Fatal("restore changed instance data", err)
	}
	// Corrupt backups cannot replace code or silently fill a missing package.
	a.closeOriginalSessions()
	if err := a.backupOriginalPluginCodeFiles(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(a.pluginPackageRoot(), clone)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.pluginCodeBackupRoot(), clone, "owned-module-fixture"), []byte("corrupt"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.restoreOriginalPluginCodeFiles(ctx); err == nil {
		t.Fatal("corrupt backup accepted")
	}
	if _, err := os.Stat(filepath.Join(a.pluginPackageRoot(), clone)); !os.IsNotExist(err) {
		t.Fatal("corrupt backup became runtime code")
	}
	if _, err := artifacts.ReadInstalled(a.pluginPackageRoot(), source, validatePluginArtifactContract); err != nil {
		t.Fatal("clone failure changed source", err)
	}
	if _, err := os.Stat(a.pluginCodeBackupRoot()); !os.IsNotExist(err) {
		t.Fatal("failed startup restore did not consume backup as fixed source", err)
	}
}

func TestOriginalPluginCodeBackupIncludesNormallyUninstalledCode(t *testing.T) {
	a, cookie, _ := originalArtifactCloneFixture(t)
	const id = "OwnedModuleFixture"
	ctx := context.Background()
	// Remove any install-time Docker refresh so this checks a newly requested
	// shutdown backup, independently of the machine running this test.
	if err := os.RemoveAll(a.pluginCodeBackupRoot()); err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), "DELETE", "/api/plugins/"+id, "", cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	if _, err := artifacts.ReadInstalled(a.pluginPackageRoot(), id, validatePluginArtifactContract); err != nil {
		t.Fatal("normal uninstall did not retain its code", err)
	}
	if err := a.backupOriginalPluginCodeFiles(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := artifacts.ReadInstalled(a.pluginCodeBackupRoot(), id, validatePluginArtifactContract); err != nil {
		t.Fatal("normal uninstall retained code was not backed up", err)
	}
	in, err := a.pluginStore.Installation(ctx, id)
	if err != nil || in.Installed {
		t.Fatal("backing up retained code changed installation membership", err)
	}
	if err := a.restoreOriginalPluginCodeFiles(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.pluginCodeBackupRoot()); !os.IsNotExist(err) {
		t.Fatal("non-reset startup did not consume unused backup", err)
	}
	if _, err := artifacts.ReadInstalled(a.pluginPackageRoot(), id, validatePluginArtifactContract); err != nil {
		t.Fatal("consuming unused backup changed retained code", err)
	}
}
