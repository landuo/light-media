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

func TestPluginStoreRejectsCiphertextMovedAcrossInstanceKindAndKey(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	s, p := a.Store, a.pluginStore
	for _, instance := range []string{"one", "two"} {
		installStoredPlugin(t, p, instance)
		config, err := json.Marshal(map[string]string{"secret": instance})
		if err != nil {
			t.Fatal(err)
		}
		a.pluginOriginalBase.mu.Lock()
		_, err = a.originalBaseUpdateConfigLocked(ctx, instance, config)
		a.pluginOriginalBase.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"main", "other"} {
			value, err := json.Marshal(map[string]string{"value": key})
			if err != nil {
				t.Fatal(err)
			}
			if err := p.originalBaseSaveData(ctx, instance, key, value); err != nil {
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

// Use the real shared Base writer, not the retired native CAS/config queue.
func retainedStoreSaveConfig(t *testing.T, a *App, instance string, value any) PluginStoredValue {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	a.pluginOriginalBase.mu.Lock()
	_, err = a.originalBaseUpdateConfigLocked(context.Background(), instance, raw)
	a.pluginOriginalBase.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	stored, err := a.pluginStore.Config(context.Background(), instance)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func TestPluginStoreRestartPersistsEncryptedConfigAndNamespacedData(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	dir, p := a.Dir, a.pluginStore
	for _, instance := range []string{"SyntheticPlugin", "SyntheticPlugin.clone"} {
		installStoredPlugin(t, p, instance)
	}
	secret := "synthetic-private-password-not-visible-847291"
	config := retainedStoreSaveConfig(t, a, "SyntheticPlugin", map[string]any{"password": secret, "enabled": true, "onlyonce": true})
	if config.Revision != 1 {
		t.Fatal(config)
	}
	data, err := json.Marshal(map[string]string{"token": secret})
	if err != nil {
		t.Fatal(err)
	}
	if err = p.originalBaseSaveData(ctx, "SyntheticPlugin", "history", data); err != nil {
		t.Fatal(err)
	}
	if err = p.originalBaseSaveData(ctx, "SyntheticPlugin.clone", "history", json.RawMessage(`["clone-only"]`)); err != nil {
		t.Fatal(err)
	}
	// The standard Base takes no expected-revision grant. An unchanged value
	// must retain the original durable row/revision instead of issuing a write.
	a.pluginOriginalBase.mu.Lock()
	unchanged, err := a.originalBaseUpdateConfigLocked(ctx, "SyntheticPlugin", config.Value)
	a.pluginOriginalBase.mu.Unlock()
	if err != nil || string(unchanged) != "null" {
		t.Fatal("unchanged Base write", string(unchanged), err)
	}
	current, err := p.Config(ctx, "SyntheticPlugin")
	if err != nil || current.Revision != config.Revision || !bytes.Equal(current.Value, config.Value) {
		t.Fatal("unchanged Base write replaced config", current, err)
	}
	for _, path := range []string{"light-media.db", "light-media.db-wal"} {
		raw, readErr := os.ReadFile(filepath.Join(dir, path))
		if readErr != nil && !os.IsNotExist(readErr) {
			t.Fatal(readErr)
		}
		if bytes.Contains(raw, []byte(secret)) || bytes.Contains(raw, []byte("clone-only")) {
			t.Fatal("private plugin values leaked to database storage", path)
		}
	}
	public, _ := json.Marshal(config)
	if bytes.Contains(public, []byte(secret)) || bytes.Contains(public, []byte("value")) {
		t.Fatal("storage metadata serialization exposed a value", string(public))
	}
	beforeRestart, err := p.Installation(ctx, "SyntheticPlugin")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	p = reopened.pluginStore
	got, err := p.Config(ctx, "SyntheticPlugin")
	if err != nil || !bytes.Equal(got.Value, config.Value) || got.Revision != config.Revision {
		t.Fatal("restart lost config", got, err)
	}
	clone, err := p.Data(ctx, "SyntheticPlugin.clone", "history")
	if err != nil || string(clone.Value) != `["clone-only"]` {
		t.Fatal("instances shared a data namespace", clone, err)
	}
	installation, err := p.Installation(ctx, "SyntheticPlugin")
	expected := storedPluginFixture("SyntheticPlugin")
	if err != nil || !installation.Installed || installation.DesiredEnabled || installation.InstanceID != expected.InstanceID || installation.PluginID != expected.PluginID || installation.Version != expected.Version || installation.ManifestSchemaVersion != expected.ManifestSchemaVersion || installation.Generation != 1 {
		t.Fatal("prepared installation was changed or enabled", installation, err)
	}
	if !bytes.Equal(installation.Manifest, beforeRestart.Manifest) {
		t.Fatal("restart changed the persisted manifest")
	}
	var actualFields, expectedFields map[string]json.RawMessage
	if err = json.Unmarshal(installation.Manifest, &actualFields); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(expected.Manifest, &expectedFields); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actualFields, expectedFields) {
		t.Fatal("restart changed plugin-owned manifest fields", actualFields, expectedFields)
	}
	public, _ = json.Marshal(installation)
	if bytes.Contains(public, []byte("manifest\"")) || bytes.Contains(public, []byte("runtime_enabled")) {
		t.Fatal("installation projection exposed private manifest or claimed runtime state", string(public))
	}
}

func TestPluginStoreUninstallPreservesAndResetDeletesOnlyTargetValues(t *testing.T) {
	ctx := context.Background()
	a, cookie, _ := originalBaseSDKFixture(t)
	p := a.pluginStore
	oneID, twoID := ownedBaseFixtureID, ownedBaseFixtureID+"storagepeer"
	clone := callTest(t, a.Handler(), "POST", "/api/plugins/"+oneID+"/clone", `{"suffix":"StoragePeer"}`, cookie, "")
	assertOriginalRegistrySuccess(t, clone.Code, clone.Body.Bytes())
	for _, instance := range []string{oneID, twoID} {
		retainedStoreSaveConfig(t, a, instance, map[string]string{"password": instance})
		if err := p.originalBaseSaveData(ctx, instance, "history", json.RawMessage(`[1,2,3]`)); err != nil {
			t.Fatal(err)
		}
	}
	one, _ := p.Config(ctx, oneID)
	two, _ := p.Config(ctx, twoID)
	uninstall := callTest(t, a.Handler(), "DELETE", "/api/plugins/"+oneID, "", cookie, "")
	assertOriginalRegistrySuccess(t, uninstall.Code, uninstall.Body.Bytes())
	retained, err := p.Config(ctx, oneID)
	if err != nil || !reflect.DeepEqual(retained, one) {
		t.Fatal("uninstall deleted retained config", retained, err)
	}
	installation, err := p.Installation(ctx, oneID)
	if err != nil || installation.Installed {
		t.Fatal("uninstall kept installed membership", installation, err)
	}
	// The independent neutral installation record can be reinstalled without
	// rewriting retained Base config or data; no old lifecycle grant is created.
	if err = p.Install(ctx, installation); err != nil {
		t.Fatal(err)
	}
	retained, _ = p.Config(ctx, oneID)
	if !reflect.DeepEqual(retained, one) {
		t.Fatal("reinstall discarded retained config")
	}
	reset := callTest(t, a.Handler(), "POST", "/api/plugins/"+oneID+"/reset", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, reset.Code, reset.Body.Bytes())
	if _, err := p.Config(ctx, oneID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("reset retained config", err)
	}
	if _, err := p.Data(ctx, oneID, "history"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("reset retained KV", err)
	}
	got, err := p.Config(ctx, twoID)
	if err != nil || !reflect.DeepEqual(got, two) {
		t.Fatal("reset affected another plugin", got, err)
	}
	installation, err = p.Installation(ctx, oneID)
	if err != nil || !installation.Installed {
		t.Fatal("reset uninstalled the package", installation, err)
	}
	conflicting := installation
	conflicting.PluginID = "UnrelatedPlugin"
	if err := p.Install(ctx, conflicting); pluginErrorCode(err) != "PLUGIN_INSTANCE_CONFLICT" {
		t.Fatal("another plugin took over an existing instance", err)
	}
}

func TestPluginStoreNeutralInstallationAndValueValidation(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	s, p := a.Store, a.pluginStore
	installStoredPlugin(t, p, "existing")
	before := retainedStoreSaveConfig(t, a, "existing", map[string]string{"password": "keep"})
	conflicting := storedPluginFixture("existing")
	conflicting.PluginID = "UnrelatedPlugin"
	if err := p.Install(ctx, conflicting); pluginErrorCode(err) != "PLUGIN_INSTANCE_CONFLICT" {
		t.Fatal("another plugin took over a preexisting installation", err)
	}
	for _, raw := range []json.RawMessage{json.RawMessage(`{`), json.RawMessage(`{"oversized":"` + strings.Repeat("x", pluginValueLimit) + `"}`)} {
		a.pluginOriginalBase.mu.Lock()
		_, err := a.originalBaseUpdateConfigLocked(ctx, "existing", raw)
		a.pluginOriginalBase.mu.Unlock()
		if err == nil {
			t.Fatal("invalid config was accepted")
		}
	}
	for _, key := range []string{"line\nbreak", strings.Repeat("x", 257)} {
		if err := p.originalBaseSaveData(ctx, "existing", key, json.RawMessage(`"invalid"`)); err == nil {
			t.Fatal("invalid key was accepted", key)
		}
	}
	got, err := p.Config(ctx, "existing")
	if err != nil || string(got.Value) != `{"password":"keep"}` || got.Revision != before.Revision {
		t.Fatal("rejected writes changed config", got, err)
	}
	// Object-only validation remains a real manifest requirement, not a
	// fabricated restriction on the original Base's arbitrary JSON values.
	for _, value := range []any{nil, []string{"not-a-manifest"}} {
		in := storedPluginFixture("invalid")
		in.Manifest, err = json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := validatePluginInstallation(in); err == nil {
			t.Fatal("invalid manifest was accepted")
		}
	}
	if _, err := s.DB.Exec(`UPDATE plugin_storage_schema SET version=2`); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPluginStore(ctx, s); err == nil {
		t.Fatal("unsupported future plugin schema accepted")
	}
}
