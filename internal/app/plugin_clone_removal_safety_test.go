package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"light-media/internal/plugins/builtin"
)

func TestPluginCloneRemovalIgnoresBusinessCloneFlags(t *testing.T) {
	a := newTestApp(t)
	p := a.pluginStore
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	config, err := p.SaveConfig(ctx, m.PluginID, 0, map[string]any{"enabled": false, "is_clone": true, "source_instance_id": "Other", "host_instance": map[string]any{"purpose": "instance-clone/v1", "source_instance_id": "Other"}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := p.SaveData(ctx, m.PluginID, "history", 0, []string{"must-retain"})
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Uninstall(ctx, m.PluginID); err != nil {
		t.Fatal(err)
	}
	got, err := p.Config(ctx, m.PluginID)
	if err != nil || !bytes.Equal(config.Value, got.Value) {
		t.Fatalf("business flags made ordinary uninstall destructive: %v", err)
	}
	gotData, err := p.Data(ctx, m.PluginID, "history")
	if err != nil || !bytes.Equal(data.Value, gotData.Value) {
		t.Fatalf("business flags deleted ordinary KV: %v", err)
	}
}

func TestPluginCloneRemovalRejectsMetadataSourceMismatchAtomically(t *testing.T) {
	a := newTestApp(t)
	p := a.pluginStore
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	clone, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "Mismatch"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.SaveData(ctx, clone.InstanceID, "history", 0, []string{"retain-on-error"}); err != nil {
		t.Fatal(err)
	}
	// Synthetic authenticated corruption: metadata is locally sealed for this
	// namespace but disagrees with the separate host manifest marker.
	meta, err := p.instanceMetadata(ctx, a.Store.DB, clone.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	meta.SourceInstanceID = "UnrelatedSource"
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := p.seal("instance_metadata", clone.InstanceID, "main", raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_instance_metadata SET sealed=? WHERE instance_id=?`, sealed, clone.InstanceID); err != nil {
		t.Fatal(err)
	}
	before := instanceReadTables(t, a)
	if err = p.Uninstall(ctx, clone.InstanceID); pluginErrorCode(err) != "PLUGIN_INSTANCE_METADATA_INVALID" {
		t.Errorf("mismatched clone source not rejected: %v", err)
	}
	if after := instanceReadTables(t, a); !reflect.DeepEqual(before, after) {
		t.Fatal("failed metadata validation partially changed lifecycle/data")
	}
}

func TestPluginCloneRemovalRejectsCrossNamespaceMetadata(t *testing.T) {
	a := newTestApp(t)
	p := a.pluginStore
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	one, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "One"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "Two"})
	if err != nil {
		t.Fatal(err)
	}
	var ciphertext []byte
	if err = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_instance_metadata WHERE instance_id=?`, one.InstanceID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_instance_metadata SET sealed=? WHERE instance_id=?`, ciphertext, two.InstanceID); err != nil {
		t.Fatal(err)
	}
	before := instanceReadTables(t, a)
	if err = p.Uninstall(ctx, two.InstanceID); err == nil {
		t.Error("foreign namespace metadata accepted")
	}
	if after := instanceReadTables(t, a); !reflect.DeepEqual(before, after) {
		t.Fatal("cross-namespace metadata failure changed database")
	}
}

func TestPluginCloneRemovalDeleteFailureRollsBackWholeTransaction(t *testing.T) {
	a := newTestApp(t)
	p := a.pluginStore
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	clone, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "Rollback"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.SaveData(ctx, clone.InstanceID, "history", 0, []string{"must-survive"}); err != nil {
		t.Fatal(err)
	}
	// Literal is the fixed, self-created synthetic ID, not user SQL input.
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER parity_clone_delete_fail BEFORE DELETE ON plugin_values WHEN OLD.instance_id='SystemProcessrollback' BEGIN SELECT RAISE(ABORT,'synthetic delete failure'); END;`); err != nil {
		t.Fatal(err)
	}
	before := instanceReadTables(t, a)
	if err = p.Uninstall(ctx, clone.InstanceID); err == nil {
		t.Error("injected clone deletion failure did not reject uninstall")
	}
	if after := instanceReadTables(t, a); !reflect.DeepEqual(before, after) {
		t.Fatal("delete failure leaked generation/install/config/KV/lifecycle writes")
	}
}

func TestPluginCloneRemovalPreservesSourceSiblingAndReferencedFiles(t *testing.T) {
	a := newTestApp(t)
	p := a.pluginStore
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	if _, err := p.SaveConfig(ctx, m.PluginID, 0, map[string]any{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	one, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "Remove"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "Keep"})
	if err != nil {
		t.Fatal(err)
	}
	// All directories are owned temp fixtures referenced by configuration.
	// No generic host-owned directory deletion API is assumed by this test.
	files := map[string][]byte{}
	configs := map[string]PluginStoredValue{}
	values := map[string]PluginStoredValue{}
	for _, id := range []string{m.PluginID, one.InstanceID, two.InstanceID} {
		dir := filepath.Join(t.TempDir(), id)
		if err = os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "sentinel.txt")
		contents := []byte("synthetic-owned-" + id)
		if err = os.WriteFile(path, contents, 0600); err != nil {
			t.Fatal(err)
		}
		files[path] = contents
		old, e := p.Config(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		conf, e := p.SaveConfig(ctx, id, old.Revision, map[string]any{"enabled": false, "owned_directory": dir})
		if e != nil {
			t.Fatal(e)
		}
		configs[id] = conf
		val, e := p.SaveData(ctx, id, "history", 0, []string{"owned-" + id})
		if e != nil {
			t.Fatal(e)
		}
		values[id] = val
	}
	if err = p.Uninstall(ctx, one.InstanceID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{m.PluginID, two.InstanceID} {
		conf, e := p.Config(ctx, id)
		if e != nil || !bytes.Equal(conf.Value, configs[id].Value) || conf.Revision != configs[id].Revision {
			t.Errorf("changed peer config %s: %v", id, e)
		}
		val, e := p.Data(ctx, id, "history")
		if e != nil || !bytes.Equal(val.Value, values[id].Value) || val.Revision != values[id].Revision {
			t.Errorf("changed peer KV %s: %v", id, e)
		}
		in, e := p.Installation(ctx, id)
		if e != nil || !in.Installed {
			t.Errorf("uninstalled peer %s: %v", id, e)
		}
	}
	for path, want := range files {
		got, e := os.ReadFile(path)
		if e != nil || !bytes.Equal(got, want) {
			t.Errorf("uninstall touched referenced file %s: %v", filepath.Base(path), e)
		}
	}
}
