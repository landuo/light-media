package app

import (
	"context"
	"database/sql"
	"errors"
	"light-media/internal/plugins/builtin"
	"testing"
)

func TestPluginOriginalStartupLoadsDisabledAndAbsentConfig(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	ctx := context.Background()
	installCloneSource(t, a.pluginStore, builtin.SystemProcessManifest())
	if _, err := a.pluginStore.SaveConfig(ctx, "SystemProcess", 0, map[string]any{"enable": false, "marker": 0}); err != nil {
		t.Fatal(err)
	}
	clone, err := a.pluginStore.Clone(ctx, "SystemProcess", PluginCloneRequest{Suffix: "Startup"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`DELETE FROM plugin_values WHERE instance_id=?`, clone.InstanceID); err != nil {
		t.Fatal(err)
	}
	// Simulate an installed row from the previous staging implementation. This
	// bit is not the original object's enabled state and cannot suppress init.
	if _, err = a.Store.DB.Exec(`UPDATE plugin_installations SET desired_enabled=0 WHERE instance_id=?`, clone.InstanceID); err != nil {
		t.Fatal(err)
	}
	source, err := a.pluginStore.Config(ctx, "SystemProcess")
	if err != nil {
		t.Fatal(err)
	}
	a.Start(ctx)
	for _, instance := range []string{"SystemProcess", clone.InstanceID} {
		loaded, ok := a.pluginRunningInstances.Load(instance)
		if !ok {
			t.Fatalf("installed disabled instance not loaded: %s", instance)
		}
		state, err := loaded.(*originalPluginInstance).getState(ctx)
		if err != nil || state {
			t.Fatalf("state %s=%v %v", instance, state, err)
		}
		if _, ok := a.pluginRegisteredAPIs.Load(instance); !ok {
			t.Fatalf("API registration absent: %s", instance)
		}
		var count int
		if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id=? AND action='init' AND state='succeeded'`, instance).Scan(&count); err != nil || count != 1 {
			t.Fatalf("init %s=%d %v", instance, count, err)
		}
	}
	after, err := a.pluginStore.Config(ctx, "SystemProcess")
	if err != nil || after.Revision != source.Revision || string(after.Value) != string(source.Value) {
		t.Fatalf("disabled config changed %v", err)
	}
	if _, err = a.pluginStore.Config(ctx, clone.InstanceID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("absent config fabricated: %v", err)
	}
}
