package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"light-media/internal/plugins/builtin"
	"testing"
)

func TestPluginLarkNativeSourceChildCannotBeStolenByGenericWorker(t *testing.T) {
	a := newTestApp(t)
	installUnregisteredLarkConfig(t, a, map[string]any{"enabled": true})
	core, ctx := larkStartedCoreFixture(t, a)
	child, e := a.larkPrepareCoreModule(ctx, core, pluginLarkModuleInput{Name: "message_parser", Source: "Lark", Metadata: core.inbox.Payload})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.pluginStore.ClaimRun(context.Background()); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("generic worker stole private source child", e)
	}
	manifest, _, ok := builtin.Lookup("RemoteIdentifiers")
	if !ok {
		t.Fatal("existing native handler disappeared")
	}
	raw, _ := json.Marshal(manifest)
	unrelated, e := a.pluginStore.InstallAndQueue(ctx, PluginInstallation{InstanceID: "RemoteIdentifiers.owned", PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: manifest.SchemaVersion, Manifest: raw}, map[string]any{"enable": false})
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := a.pluginStore.ClaimRun(context.Background())
	if e != nil || claimed.ID != unrelated.ID {
		t.Fatal("private child starved unrelated native task", claimed, e)
	}
	actual, e := a.pluginStore.Run(ctx, child.ID)
	if e != nil || actual.State != "pending" {
		t.Fatal(actual, e)
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, _, e = a.larkSourceTx(ctx, tx, child, "lark.source.module"); e == nil {
		t.Fatal("incorrect private capability accepted")
	}
	method, _, e := larkDeferredSpec(child)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = a.larkSourceTx(ctx, tx, child, method); e != nil {
		t.Fatal("exact live producer lost original source", e)
	}
	if _, _, e = a.larkSourceTx(context.Background(), tx, child, method); e == nil {
		t.Fatal("generic context recovered private source")
	}
}
