package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func pluginStoreFixture(t *testing.T) (*Store, *PluginStore) {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	p, err := NewPluginStore(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return s, p
}

func storedPluginFixture(instance string) PluginInstallation {
	return PluginInstallation{InstanceID: instance, PluginID: "SyntheticPlugin", Version: "1.2.3", ManifestSchemaVersion: 1,
		Manifest: json.RawMessage(`{"plugin_id":"SyntheticPlugin","name":"synthetic plugin","runtime":"go-executable"}`)}
}

func installStoredPlugin(t *testing.T, p *PluginStore, instance string) {
	t.Helper()
	if err := p.Install(context.Background(), storedPluginFixture(instance)); err != nil {
		t.Fatal(err)
	}
}

func pluginErrorCode(err error) string {
	var api *APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

func TestPluginStoreRestartPersistsEncryptedConfigAndNamespacedData(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	open := true
	t.Cleanup(func() {
		if open {
			_ = s.Close()
		}
	})
	p, err := NewPluginStore(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range []string{"SyntheticPlugin", "SyntheticPlugin.clone"} {
		installStoredPlugin(t, p, instance)
	}
	secret := "synthetic-private-password-not-visible-847291"
	config, err := p.SaveConfig(ctx, "SyntheticPlugin", 0, map[string]any{"password": secret, "enabled": true, "onlyonce": true})
	if err != nil || config.Revision != 1 {
		t.Fatal(config, err)
	}
	if _, err = p.SaveData(ctx, "SyntheticPlugin", "history", 0, map[string]string{"token": secret}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.SaveData(ctx, "SyntheticPlugin.clone", "history", 0, []string{"clone-only"}); err != nil {
		t.Fatal(err)
	}
	imported := pluginImportFixture(t, p, "d", "migrated")
	receipt, err := p.Import(ctx, imported)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.SaveConfig(ctx, "SyntheticPlugin", 0, map[string]bool{"stale": true}); pluginErrorCode(err) != "PLUGIN_REVISION_CONFLICT" {
		t.Fatal("stale config save was accepted", err)
	}
	for _, path := range []string{"light-media.db", "light-media.db-wal"} {
		raw, readErr := os.ReadFile(filepath.Join(dir, path))
		if readErr != nil && !os.IsNotExist(readErr) {
			t.Fatal(readErr)
		}
		if bytes.Contains(raw, []byte(secret)) || bytes.Contains(raw, []byte("clone-only")) || bytes.Contains(raw, []byte("synthetic-migration-private")) || bytes.Contains(raw, []byte("synthetic-history-private")) {
			t.Fatal("private plugin values leaked to database storage", path)
		}
	}
	public, _ := json.Marshal(config)
	if bytes.Contains(public, []byte(secret)) || bytes.Contains(public, []byte("value")) {
		t.Fatal("storage metadata serialization exposed a value", string(public))
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	open = false
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	open = true
	p, err = NewPluginStore(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Config(ctx, "SyntheticPlugin")
	if err != nil || !bytes.Equal(got.Value, config.Value) || got.Revision != config.Revision {
		t.Fatal("restart lost config", got, err)
	}
	clone, err := p.Data(ctx, "SyntheticPlugin.clone", "history")
	if err != nil || string(clone.Value) != `["clone-only"]` {
		t.Fatal("instances shared a data namespace", clone, err)
	}
	installation, err := p.Installation(ctx, "SyntheticPlugin")
	if err != nil || !installation.Installed || installation.DesiredEnabled || !bytes.Equal(installation.Manifest, storedPluginFixture("SyntheticPlugin").Manifest) {
		t.Fatal("prepared installation was changed or enabled", installation, err)
	}
	public, _ = json.Marshal(installation)
	if bytes.Contains(public, []byte("manifest\"")) || bytes.Contains(public, []byte("runtime_enabled")) {
		t.Fatal("installation projection exposed private manifest or claimed runtime state", string(public))
	}
	restartedRun, err := p.Migration(ctx, imported.RunID)
	if err != nil || restartedRun.State != "imported" {
		t.Fatal("restart lost migration state", restartedRun, err)
	}
	restartedReceipt, err := p.Import(ctx, imported)
	if err != nil || restartedReceipt != receipt {
		t.Fatal("restart lost idempotent migration receipt", restartedReceipt, err)
	}
}

func TestPluginStoreRejectsCiphertextMovedAcrossInstanceKindAndKey(t *testing.T) {
	ctx := context.Background()
	s, p := pluginStoreFixture(t)
	for _, instance := range []string{"one", "two"} {
		installStoredPlugin(t, p, instance)
		if _, err := p.SaveConfig(ctx, instance, 0, map[string]string{"secret": instance}); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"main", "other"} {
			if _, err := p.SaveData(ctx, instance, key, 0, map[string]string{"value": key}); err != nil {
				t.Fatal(err)
			}
		}
	}
	var cipherText []byte
	if err := s.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id='one' AND kind='config' AND item_key='main'`).Scan(&cipherText); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct{ instance, kind, key string }{{"two", "config", "main"}, {"one", "data", "main"}, {"one", "data", "other"}} {
		if _, err := s.DB.Exec(`UPDATE plugin_values SET sealed=? WHERE instance_id=? AND kind=? AND item_key=?`, cipherText, target.instance, target.kind, target.key); err != nil {
			t.Fatal(err)
		}
		_, err := p.readValue(ctx, target.instance, target.kind, target.key)
		if pluginErrorCode(err) != "PLUGIN_DATA_CORRUPT" {
			t.Fatal("ciphertext survived namespace substitution", target, err)
		}
	}
	// Isolate key binding from kind/instance binding.
	if err := s.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id='two' AND kind='data' AND item_key='main'`).Scan(&cipherText); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE plugin_values SET sealed=? WHERE instance_id='two' AND kind='data' AND item_key='other'`, cipherText); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Data(ctx, "two", "other"); pluginErrorCode(err) != "PLUGIN_DATA_CORRUPT" {
		t.Fatal("ciphertext survived key substitution", err)
	}
}

func pluginImportFixture(t *testing.T, p *PluginStore, source, target string) PluginImport {
	t.Helper()
	run, err := p.BeginMigration(context.Background(), strings.Repeat(source, 64), "OldSyntheticPlugin", target)
	if err != nil {
		t.Fatal(err)
	}
	installation := storedPluginFixture(target)
	installation.DesiredEnabled = true // Source intent cannot activate import.
	return PluginImport{RunID: run.ID, Installation: installation,
		Config: json.RawMessage(`{"password":"synthetic-migration-private","enabled":true,"onlyonce":true}`),
		Data:   map[string]json.RawMessage{"history": json.RawMessage(`[{"token":"synthetic-history-private"}]`)}}
}

func TestPluginStoreMigrationImportIsAtomicIdempotentAndNeverActivates(t *testing.T) {
	ctx := context.Background()
	s, p := pluginStoreFixture(t)
	installStoredPlugin(t, p, "unrelated")
	if _, err := p.SaveConfig(ctx, "unrelated", 0, map[string]string{"cookie": "unrelated-secret"}); err != nil {
		t.Fatal(err)
	}
	var unrelated []byte
	if err := s.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id='unrelated'`).Scan(&unrelated); err != nil {
		t.Fatal(err)
	}
	in := pluginImportFixture(t, p, "a", "converted")
	receipt, err := p.Import(ctx, in)
	if err != nil || receipt.State != "imported" {
		t.Fatal(receipt, err)
	}
	run, err := p.Migration(ctx, in.RunID)
	if err != nil || run.State != "imported" {
		t.Fatal(run, err)
	}
	if err := p.SetMigrationState(ctx, run.ID, "running", "", map[string]bool{"retry": true}); pluginErrorCode(err) != "PLUGIN_MIGRATION_TERMINAL" {
		t.Fatal("completed migration state was overwritten", err)
	}
	installation, _ := p.Installation(ctx, "converted")
	if installation.DesiredEnabled || !installation.Installed {
		t.Fatal("migration activated a plugin", installation)
	}
	var jobs int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("migration scheduled onlyonce or other execution", jobs, err)
	}
	got, _ := p.Config(ctx, "converted")
	if !bytes.Equal(got.Value, in.Config) {
		t.Fatal("import did not preserve source config", string(got.Value))
	}
	if _, err := p.SaveConfig(ctx, "converted", got.Revision, map[string]string{"password": "user-edited"}); err != nil {
		t.Fatal(err)
	}
	newRun, err := p.BeginMigration(ctx, run.SourceFingerprint, run.SourcePluginID, run.InstanceID)
	if err != nil || newRun.ID != run.ID || newRun.State != "imported" {
		t.Fatal("migration preview was not idempotent", newRun, err)
	}
	repeated, err := p.Import(ctx, in)
	if err != nil || repeated != receipt {
		t.Fatal("identical import did not return original receipt", repeated, err)
	}
	got, _ = p.Config(ctx, "converted")
	if !bytes.Contains(got.Value, []byte("user-edited")) || got.Revision != 2 {
		t.Fatal("replayed migration reverted user config", string(got.Value))
	}
	in.Config = json.RawMessage(`{"password":"changed-source"}`)
	if _, err := p.Import(ctx, in); pluginErrorCode(err) != "PLUGIN_MIGRATION_CONFLICT" {
		t.Fatal("same migration identity overwrote data", err)
	}
	var current []byte
	if err := s.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id='unrelated'`).Scan(&current); err != nil || !bytes.Equal(current, unrelated) {
		t.Fatal("import altered unrelated plugin", err)
	}
	for _, value := range []any{receipt, run} {
		public, _ := json.Marshal(value)
		if bytes.Contains(public, []byte("synthetic-migration-private")) || bytes.Contains(public, []byte("details")) || bytes.Contains(public, []byte("digest")) {
			t.Fatal("migration metadata exposed private input", string(public))
		}
	}
}

func TestPluginStoreMigrationReceiptFailureRollsBackWholeImport(t *testing.T) {
	ctx := context.Background()
	s, p := pluginStoreFixture(t)
	in := pluginImportFixture(t, p, "b", "rollback")
	if _, err := s.DB.Exec(`CREATE TRIGGER synthetic_receipt_failure BEFORE INSERT ON plugin_migration_receipts BEGIN SELECT RAISE(ABORT,'synthetic receipt failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Import(ctx, in); err == nil {
		t.Fatal("simulated late persistence error was ignored")
	}
	for _, table := range []string{"plugin_installations", "plugin_values", "plugin_migration_receipts"} {
		var count int
		if err := s.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("partial import survived rollback", table, count, err)
		}
	}
	run, err := p.Migration(ctx, in.RunID)
	if err != nil || run.State != "planned" {
		t.Fatal("failed import advanced its run", run, err)
	}
	if err := p.SetMigrationState(ctx, run.ID, "failed", "SYNTHETIC_FAILURE", map[string]string{"private": "synthetic-hidden-failure-detail"}); err != nil {
		t.Fatal(err)
	}
	run, err = p.Migration(ctx, in.RunID)
	if err != nil || run.State != "failed" || run.ErrorCode != "SYNTHETIC_FAILURE" || !bytes.Contains(run.Details, []byte("synthetic-hidden-failure-detail")) {
		t.Fatal("migration diagnostic state was lost", run, err)
	}
	var sealed []byte
	if err := s.DB.QueryRow(`SELECT details FROM plugin_migration_runs WHERE id=?`, run.ID).Scan(&sealed); err != nil || bytes.Contains(sealed, []byte("synthetic-hidden-failure-detail")) {
		t.Fatal("migration details stored as plaintext", err)
	}
	if _, err := s.DB.Exec(`DROP TRIGGER synthetic_receipt_failure`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Import(ctx, in); err != nil {
		t.Fatal("retry could not finish the rolled back import", err)
	}
}

func TestPluginStoreUninstallPreservesAndResetDeletesOnlyTargetValues(t *testing.T) {
	ctx := context.Background()
	_, p := pluginStoreFixture(t)
	for _, instance := range []string{"one", "two"} {
		installStoredPlugin(t, p, instance)
		if _, err := p.SaveConfig(ctx, instance, 0, map[string]string{"password": instance}); err != nil {
			t.Fatal(err)
		}
		if _, err := p.SaveData(ctx, instance, "history", 0, []int{1, 2, 3}); err != nil {
			t.Fatal(err)
		}
	}
	one, _ := p.Config(ctx, "one")
	two, _ := p.Config(ctx, "two")
	if err := p.Uninstall(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	retained, err := p.Config(ctx, "one")
	if err != nil || !reflect.DeepEqual(retained, one) {
		t.Fatal("uninstall deleted retained config", retained, err)
	}
	if _, err := p.SaveConfig(ctx, "one", retained.Revision, map[string]bool{"ignored": true}); pluginErrorCode(err) != "PLUGIN_NOT_INSTALLED" {
		t.Fatal("uninstalled plugin mutated retained data", err)
	}
	installStoredPlugin(t, p, "one")
	retained, _ = p.Config(ctx, "one")
	if !reflect.DeepEqual(retained, one) {
		t.Fatal("reinstall discarded retained config")
	}
	if err := p.Reset(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Config(ctx, "one"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("reset retained config", err)
	}
	if _, err := p.Data(ctx, "one", "history"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("reset retained KV", err)
	}
	got, err := p.Config(ctx, "two")
	if err != nil || !reflect.DeepEqual(got, two) {
		t.Fatal("reset affected another plugin", got, err)
	}
	installation, err := p.Installation(ctx, "one")
	if err != nil || !installation.Installed {
		t.Fatal("reset uninstalled the package", installation, err)
	}
	conflicting := storedPluginFixture("one")
	conflicting.PluginID = "UnrelatedPlugin"
	if err := p.Install(ctx, conflicting); pluginErrorCode(err) != "PLUGIN_INSTANCE_CONFLICT" {
		t.Fatal("another plugin took over an existing instance", err)
	}
}

func TestPluginStoreRejectsImportOverExistingTargetAndInvalidValues(t *testing.T) {
	ctx := context.Background()
	s, p := pluginStoreFixture(t)
	installStoredPlugin(t, p, "existing")
	if _, err := p.SaveConfig(ctx, "existing", 0, map[string]string{"password": "keep"}); err != nil {
		t.Fatal(err)
	}
	in := pluginImportFixture(t, p, "c", "existing")
	if _, err := p.Import(ctx, in); pluginErrorCode(err) != "PLUGIN_IMPORT_TARGET_EXISTS" {
		t.Fatal("migration overwrote a preexisting installation", err)
	}
	for _, value := range []any{nil, []string{"not-a-config"}, json.RawMessage(`{`), map[string]string{"oversized": strings.Repeat("x", pluginValueLimit)}} {
		if _, err := p.SaveConfig(ctx, "existing", 1, value); err == nil {
			t.Fatal("invalid config was accepted")
		}
	}
	for _, key := range []string{"", "line\nbreak", strings.Repeat("x", 257)} {
		if _, err := p.SaveData(ctx, "existing", key, 0, "invalid"); err == nil {
			t.Fatal("invalid key was accepted", key)
		}
	}
	got, err := p.Config(ctx, "existing")
	if err != nil || string(got.Value) != `{"password":"keep"}` || got.Revision != 1 {
		t.Fatal("rejected writes changed config", got, err)
	}
	if _, err := s.DB.Exec(`UPDATE plugin_storage_schema SET version=2`); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPluginStore(ctx, s); err == nil {
		t.Fatal("unsupported future plugin schema accepted")
	}
}
