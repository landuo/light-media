package app

import (
	"context"
	"light-media/internal/plugins/builtin"
	"testing"
)

func TestPluginOriginalStartSetsEventsFromAppliedObject(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	installCloneSource(t, a.pluginStore, builtin.SystemProcessManifest())
	ctx := context.Background()
	cfg, err := a.pluginStore.SaveConfig(ctx, "SystemProcess", 0, map[string]any{"enable": true})
	if err != nil {
		t.Fatal(err)
	}
	p := a.startOriginalPlugin(ctx, "SystemProcess")
	if p == nil {
		t.Fatal("start failed")
	}
	p.configMu.RLock()
	enabled := p.eventsEnabled
	p.configMu.RUnlock()
	if !enabled {
		t.Fatal("original get_state was not enabled")
	}
	if _, err = a.pluginStore.SaveConfig(ctx, "SystemProcess", cfg.Revision, map[string]any{"enable": false}); err != nil {
		t.Fatal(err)
	}
	if state, err := p.getState(ctx); err != nil || !state {
		t.Fatalf("unapplied DB configuration altered loaded state: %v %v", state, err)
	}
	if err = a.stopOriginalPlugin(ctx, "SystemProcess"); err != nil {
		t.Fatal(err)
	}
	p.configMu.RLock()
	enabled = p.eventsEnabled
	p.configMu.RUnlock()
	if enabled {
		t.Fatal("stop left handler enabled")
	}
}

func TestPluginOriginalStartStateFailureRetainsStoredObject(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	installCloneSource(t, a.pluginStore, builtin.SystemProcessManifest())
	ctx := context.Background()
	if _, err := a.pluginStore.SaveConfig(ctx, "SystemProcess", 0, map[string]any{"enable": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`INSERT INTO plugin_event_subscriptions(instance_id,handler_key,generation,config_revision,event_types,active,delay_ms,cursor) VALUES('SystemProcess','owned',1,1,'[]',0,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER original_start_event_failure BEFORE UPDATE ON plugin_event_subscriptions BEGIN SELECT RAISE(ABORT,'owned-start-event-failure'); END`); err != nil {
		t.Fatal(err)
	}
	if p := a.startOriginalPlugin(ctx, "SystemProcess"); p != nil {
		t.Fatal("event exception not caught")
	}
	if _, ok := a.pluginRunningInstances.Load("SystemProcess"); !ok {
		t.Fatal("original stored object rolled back after get_state/event failure")
	}
}
