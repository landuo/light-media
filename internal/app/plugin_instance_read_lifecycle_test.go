package app

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	bf "light-media/internal/plugins/brushflow"
)

func TestPluginInstanceConfigurationLoadAfterInstallCloneAndReset(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	p := a.pluginStore
	m := bf.NativeManifest()
	raw, _ := json.Marshal(m)
	in := PluginInstallation{InstanceID: m.PluginID, PluginID: m.PluginID, Version: m.Version, ManifestSchemaVersion: m.SchemaVersion, Manifest: raw}
	// The real common installation transaction creates configuration plus init.
	// Merely opening the registered configuration interface must not execute it.
	if _, e := p.InstallAndQueue(ctx, in, bf.DefaultConfig()); e != nil {
		t.Fatal(e)
	}
	check := func(t *testing.T, id string) {
		t.Helper()
		before := instanceReadTables(t, a)
		r := callTest(t, a.Handler(), "GET", "/api/plugins/"+id+"/api/status", "", cookie, "")
		if r.Code != 200 {
			t.Fatalf("%s status %d: %s", id, r.Code, r.Body.String())
		}
		var body struct {
			Success bool                       `json:"success"`
			Data    map[string]json.RawMessage `json:"data"`
		}
		if json.Unmarshal(r.Body.Bytes(), &body) != nil || !body.Success || body.Data == nil {
			t.Fatal("original response envelope missing", r.Body.String())
		}
		if after := instanceReadTables(t, a); !reflect.DeepEqual(before, after) {
			t.Fatal("load executed or persisted", id)
		}
	}
	t.Run("installed", func(t *testing.T) { check(t, m.PluginID) })
	clone, e := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "LifecycleRead"})
	if e != nil {
		t.Fatal(e)
	}
	t.Run("clone", func(t *testing.T) { check(t, clone.InstanceID) })
	sourceBefore, e := p.Config(ctx, m.PluginID)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Reset(ctx, clone.InstanceID); e != nil {
		t.Fatal(e)
	}
	t.Run("reset", func(t *testing.T) { check(t, clone.InstanceID) })
	sourceAfter, e := p.Config(ctx, m.PluginID)
	if e != nil || !reflect.DeepEqual(sourceBefore, sourceAfter) {
		t.Fatal("reset changed source", e)
	}
}
