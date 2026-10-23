package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"os"
	"path/filepath"
	"testing"
)

func TestOriginalBasePythonJSONEquality(t *testing.T) {
	for _, pair := range [][2]string{{`{"x":0.1}`, `{"x":0.10000000000000001}`}, {`{"x":true}`, `{"x":1}`}, {`{"x":1e3}`, `{"x":1000}`}, {`{"a":[false,1.0],"b":{}}`, `{"b":{},"a":[0,1]}`}} {
		if !originalBaseJSONEqual(json.RawMessage(pair[0]), json.RawMessage(pair[1])) {
			t.Fatalf("Python equality differs: %v", pair)
		}
	}
	for _, pair := range [][2]string{{`{"x":9007199254740992}`, `{"x":9007199254740993}`}, {`{"x":null}`, `{}`}, {`{"x":[]}`, `{"x":{}}`}} {
		if originalBaseJSONEqual(json.RawMessage(pair[0]), json.RawMessage(pair[1])) {
			t.Fatalf("unequal values collapsed: %v", pair)
		}
	}
}

// Storage semantics tests use an actually installed owned SDK artifact through
// the real App fixture. They do not substitute a HostHandler or create private
// receipts. Full per-call authorization acceptance belongs in manager wiring.
func TestOriginalBaseOwnedStorageRowsAndConfigCache(t *testing.T) {
	a, _, object := originalArtifactCloneFixture(t)
	ctx := context.Background()
	target := object.installation.InstanceID
	if err := a.pluginStore.originalBaseSaveData(ctx, target, "one", json.RawMessage(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	all, err := a.pluginStore.originalBaseGetData(ctx, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	var rows []originalBaseDataRow
	if err = json.Unmarshal(all, &rows); err != nil || len(rows) != 1 || rows[0].ID <= 0 || rows[0].PluginID != target || rows[0].Key != "one" {
		t.Fatalf("all-data row shape %s %v", all, err)
	}
	firstID := rows[0].ID
	if err = a.pluginStore.originalBaseSaveData(ctx, target, "one", json.RawMessage(`false`)); err != nil {
		t.Fatal(err)
	}
	all, err = a.pluginStore.originalBaseGetData(ctx, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(all, &rows); err != nil || len(rows) != 1 || rows[0].ID != firstID || string(rows[0].Value) != "false" {
		t.Fatalf("upsert changed row identity: %s %v", all, err)
	}
	missing := "absent"
	got, err := a.pluginStore.originalBaseGetData(ctx, target, &missing)
	if err != nil || string(got) != "null" {
		t.Fatalf("missing key %s %v", got, err)
	}
	empty := ""
	if err = a.pluginStore.originalBaseDeleteData(ctx, target, &empty); err != nil {
		t.Fatal(err)
	}
	got, err = a.pluginStore.originalBaseGetData(ctx, target, nil)
	if err != nil || string(got) != "[]" {
		t.Fatalf("delete-all %s %v", got, err)
	}
	// Whole dict replacement, unchanged None, then falsy DB deletion with a
	// retained cache value. Configuration update does not invoke init/reload.
	a.pluginOriginalBase.mu.Lock()
	got, err = a.originalBaseUpdateConfigLocked(ctx, target, json.RawMessage(`{"a":1,"old":true}`))
	if err != nil || string(got) != "true" {
		a.pluginOriginalBase.mu.Unlock()
		t.Fatalf("set %s %v", got, err)
	}
	got, err = a.originalBaseUpdateConfigLocked(ctx, target, json.RawMessage(`{"a":1,"old":true}`))
	if err != nil || string(got) != "null" {
		a.pluginOriginalBase.mu.Unlock()
		t.Fatalf("same set %s %v", got, err)
	}
	got, err = a.originalBaseUpdateConfigLocked(ctx, target, json.RawMessage(`{}`))
	if err != nil || string(got) != "true" {
		a.pluginOriginalBase.mu.Unlock()
		t.Fatalf("falsy set %s %v", got, err)
	}
	got, err = a.originalBaseConfigLocked(ctx, target)
	a.pluginOriginalBase.mu.Unlock()
	if err != nil || string(got) != "{}" {
		t.Fatalf("falsy cache %s %v", got, err)
	}
	initRaw, present, initErr := a.readOriginalBaseConfigForInit(ctx, target)
	if initErr != nil || !present || string(initRaw) != "{}" {
		t.Fatalf("initializer lost cached empty config: %s %v %v", initRaw, present, initErr)
	}
	if _, err = a.pluginStore.Config(ctx, target); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("falsy update retained DB row: %v", err)
	}
	a.invalidateOriginalBaseConfig(target)
	a.pluginOriginalBase.mu.Lock()
	got, err = a.originalBaseConfigLocked(ctx, target)
	a.pluginOriginalBase.mu.Unlock()
	if err != nil || string(got) != "null" {
		t.Fatalf("cache invalidation %s %v", got, err)
	}
}

// This is intentionally a real failure until the owner migrates the old
// installation FK. Never create a fake installation to make this test green.
func TestOriginalBaseUnknownNamespaceRequiresSchemaSupport(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	target := "OwnedAbsentNamespace"
	if _, err := a.pluginStore.Installation(ctx, target); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("fixture namespace already installed %v", err)
	}
	if err := a.pluginStore.originalBaseSaveData(ctx, target, "key", json.RawMessage(`0`)); err != nil {
		t.Fatalf("original arbitrary namespace is blocked by current schema: %v", err)
	}
	key := "key"
	got, err := a.pluginStore.originalBaseGetData(ctx, target, &key)
	if err != nil || string(got) != "0" {
		t.Fatalf("namespace value %s %v", got, err)
	}
	if _, err = a.pluginStore.Installation(ctx, target); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("base write fabricated an installation: %v", err)
	}
}

func TestOriginalBaseOwnedDataPathBoundaries(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	first, err := a.originalBaseDataPath(ctx, "OwnedA")
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.originalBaseDataPath(ctx, "OwnedB")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("clone directories collided")
	}
	if info, err := os.Stat(first); err != nil || !info.IsDir() {
		t.Fatalf("directory not created %v", err)
	}
	if _, err = a.originalBaseDataPath(ctx, "../escape"); err == nil {
		t.Fatal("path escape accepted")
	}
	outside := t.TempDir()
	if err = os.Symlink(outside, filepath.Join(a.Dir, "plugin-data", "OwnedSymlink")); err != nil {
		t.Fatal(err)
	}
	if _, err = a.originalBaseDataPath(ctx, "OwnedSymlink"); err == nil {
		t.Fatal("symlink target accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = a.originalBaseDataPath(canceled, "OwnedCanceled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled creation %v", err)
	}
	if _, err = os.Stat(filepath.Join(a.Dir, "plugin-data", "OwnedCanceled")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled call created directory %v", err)
	}
}

func TestOriginalBaseHostRejectsUnboundScopeBeforeEffects(t *testing.T) {
	a := newTestApp(t)
	_, err := a.pluginOriginalBaseHost(context.Background(), originalBaseCallScope{}, plugins.Identity{}, plugins.BaseSaveData, json.RawMessage(`{"plugin_id":"OwnedDenied","key":"one","value":1}`))
	if err == nil {
		t.Fatal("unbound base scope admitted")
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_values WHERE instance_id='OwnedDenied'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("denied call touched storage %d %v", count, err)
	}
}

func TestOriginalBaseDataPathRejectsInsideRootAliases(t *testing.T) {
	t.Run("target_to_sibling", func(t *testing.T) {
		a := newTestApp(t)
		ctx := context.Background()
		sibling, err := a.originalBaseDataPath(ctx, "OwnedSibling")
		if err != nil {
			t.Fatal(err)
		}
		alias := filepath.Join(a.Dir, "plugin-data", "OwnedAlias")
		if err = os.Symlink("OwnedSibling", alias); err != nil {
			t.Fatal(err)
		}
		if _, err = a.originalBaseDataPath(ctx, "OwnedAlias"); err == nil {
			t.Fatal("within-root sibling link accepted")
		}
		if info, err := os.Lstat(alias); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("failed lookup replaced alias %v", err)
		}
		if entries, err := os.ReadDir(sibling); err != nil || len(entries) != 0 {
			t.Fatalf("failed lookup modified sibling %v", err)
		}
	})
	t.Run("data_root_to_other_inside_app", func(t *testing.T) {
		a := newTestApp(t)
		ctx := context.Background()
		other := filepath.Join(a.Dir, "owned-other")
		if err := os.Mkdir(other, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("owned-other", filepath.Join(a.Dir, "plugin-data")); err != nil {
			t.Fatal(err)
		}
		if _, err := a.originalBaseDataPath(ctx, "OwnedBlocked"); err == nil {
			t.Fatal("plugin-data link inside app root accepted")
		}
		if _, err := os.Stat(filepath.Join(other, "OwnedBlocked")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("created directory in wrong namespace %v", err)
		}
	})
	t.Run("data_root_to_external", func(t *testing.T) {
		a := newTestApp(t)
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(a.Dir, "plugin-data")); err != nil {
			t.Fatal(err)
		}
		if _, err := a.originalBaseDataPath(context.Background(), "OwnedBlocked"); err == nil {
			t.Fatal("plugin-data external link accepted")
		}
		if _, err := os.Stat(filepath.Join(outside, "OwnedBlocked")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("created external directory %v", err)
		}
	})
	t.Run("existing_permissions_unchanged", func(t *testing.T) {
		a := newTestApp(t)
		ctx := context.Background()
		path, err := a.originalBaseDataPath(ctx, "OwnedExisting")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.Chmod(path, 0500); err != nil {
			t.Fatal(err)
		}
		same, err := a.originalBaseDataPath(ctx, "OwnedExisting")
		if err != nil || same != path {
			t.Fatalf("existing path %s %v", same, err)
		}
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0500 {
			t.Fatalf("permissions expanded %v", err)
		}
	})
}

// Exercise real Store writes outside Base rather than simulating invalidation.
func TestOriginalBaseConfigObservesExternalWrites(t *testing.T) {
	a, _, object := originalArtifactCloneFixture(t)
	ctx := context.Background()
	target := object.installation.InstanceID
	write := func(raw string) {
		t.Helper()
		if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	read := func(want string) {
		t.Helper()
		raw, present, err := a.readOriginalBaseConfigForInit(ctx, target)
		if err != nil || !present || string(raw) != want {
			t.Fatalf("config = %s, present %v, error %v; want %s", raw, present, err, want)
		}
	}
	write(`{"external":1}`)
	read(`{"external":1}`)
	write(`{"external":2}`)
	read(`{"external":2}`)
	// Revision alone is insufficient when a row is deleted/recreated or restored.
	var revision int64
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT revision FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, target).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	write(`{"external":3}`)
	if _, err := a.Store.DB.ExecContext(ctx, `UPDATE plugin_values SET revision=? WHERE instance_id=? AND kind='config' AND item_key='main'`, revision, target); err != nil {
		t.Fatal(err)
	}
	read(`{"external":3}`)
	if _, err := a.Store.DB.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=? AND kind='config'`, target); err != nil {
		t.Fatal(err)
	}
	raw, present, err := a.readOriginalBaseConfigForInit(ctx, target)
	if err != nil || present || string(raw) != "null" {
		t.Fatalf("external delete = %s, %v, %v", raw, present, err)
	}
	write(`{"external":4}`)
	read(`{"external":4}`)
}

func TestOriginalBaseFailedWriteRetainsCacheUntilExternalChange(t *testing.T) {
	a, _, object := originalArtifactCloneFixture(t)
	ctx := context.Background()
	target := object.installation.InstanceID
	if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, json.RawMessage(`{"db":"before"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.ExecContext(ctx, `CREATE TRIGGER owned_base_config_fail BEFORE UPDATE ON plugin_values WHEN NEW.kind='config' BEGIN SELECT RAISE(ABORT, 'owned config failure'); END`); err != nil {
		t.Fatal(err)
	}
	a.pluginOriginalBase.mu.Lock()
	_, err := a.originalBaseUpdateConfigLocked(ctx, target, json.RawMessage(`{"cache":"failed"}`))
	a.pluginOriginalBase.mu.Unlock()
	if err == nil {
		t.Fatal("expected real DB failure")
	}
	raw, present, err := a.readOriginalBaseConfigForInit(ctx, target)
	if err != nil || !present || string(raw) != `{"cache":"failed"}` {
		t.Fatalf("failed-set cache = %s, %v, %v", raw, present, err)
	}
	stored, err := a.pluginStore.Config(ctx, target)
	if err != nil || string(stored.Value) != `{"db":"before"}` {
		t.Fatalf("failed-set DB = %s, %v", stored.Value, err)
	}
	if _, err = a.Store.DB.ExecContext(ctx, `DROP TRIGGER owned_base_config_fail`); err != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, json.RawMessage(`{"db":"after"}`)); err != nil {
		t.Fatal(err)
	}
	raw, present, err = a.readOriginalBaseConfigForInit(ctx, target)
	if err != nil || !present || string(raw) != `{"db":"after"}` {
		t.Fatalf("external-write cache = %s, %v, %v", raw, present, err)
	}
	// A successful falsy deletion must update only the DB observation, not erase
	// the cached falsy value. A subsequent genuine external write refreshes it.
	a.pluginOriginalBase.mu.Lock()
	_, err = a.originalBaseUpdateConfigLocked(ctx, target, json.RawMessage(`{}`))
	a.pluginOriginalBase.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	raw, present, err = a.readOriginalBaseConfigForInit(ctx, target)
	if err != nil || !present || string(raw) != `{}` {
		t.Fatalf("deleted-row cache = %s, %v, %v", raw, present, err)
	}
	if _, err = a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, json.RawMessage(`{"db":"recreated"}`)); err != nil {
		t.Fatal(err)
	}
	raw, present, err = a.readOriginalBaseConfigForInit(ctx, target)
	if err != nil || !present || string(raw) != `{"db":"recreated"}` {
		t.Fatalf("recreated-row cache = %s, %v, %v", raw, present, err)
	}
}
