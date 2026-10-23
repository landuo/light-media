package app

import (
	"context"
	"light-media/internal/plugins/builtin"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginOriginalReloadBroadcastInvokesActualCustomHosts(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	a.pluginHostsFile = filepath.Join(t.TempDir(), "hosts")
	base := []byte("127.0.0.1 localhost\n")
	if err := os.WriteFile(a.pluginHostsFile, base, 0600); err != nil {
		t.Fatal(err)
	}
	installCloneSource(t, a.pluginStore, builtin.CustomHostsManifest())
	ctx := context.Background()
	if _, err := a.pluginStore.SaveConfig(ctx, "CustomHosts", 0, map[string]any{"enabled": true, "hosts": "127.0.0.1 original-event.test", "err_hosts": ""}); err != nil {
		t.Fatal(err)
	}
	if a.startOriginalPlugin(ctx, "CustomHosts") == nil {
		t.Fatal("actual initializer failed")
	}
	var before int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id='CustomHosts'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	// Deliberately change only this synthetic hosts file. The original reload
	// callback must really invoke init and restore it, not just append a trace.
	if err := os.WriteFile(a.pluginHostsFile, base, 0600); err != nil {
		t.Fatal(err)
	}
	a.emitOriginalPluginReload("CustomHosts")
	a.wg.Wait()
	raw, err := os.ReadFile(a.pluginHostsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "original-event.test") {
		t.Fatalf("reload callback did not restore owned hosts: %q", raw)
	}
	var after int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id='CustomHosts' AND state='succeeded'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before+1 {
		t.Fatalf("reload init calls %d -> %d", before, after)
	}
}

func TestPluginOriginalStopEventFailureKeepsRunningObject(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	installCloneSource(t, a.pluginStore, builtin.SystemProcessManifest())
	ctx := context.Background()
	if _, err := a.pluginStore.SaveConfig(ctx, "SystemProcess", 0, map[string]any{"enable": false}); err != nil {
		t.Fatal(err)
	}
	object := a.startOriginalPlugin(ctx, "SystemProcess")
	if object == nil {
		t.Fatal("init failed")
	}
	// Force the language-adapter event-disable operation to fail before the
	// original __stop_plugin and map removal lines are reached.
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER original_stop_event_failure BEFORE UPDATE ON plugin_event_subscriptions BEGIN SELECT RAISE(ABORT,'owned-stop-event-failure'); END`); err != nil {
		t.Fatal(err)
	}
	// UPDATE triggers need an owned row. Reuse the normal subscription schema.
	_, err := a.Store.DB.Exec(`INSERT INTO plugin_event_subscriptions(instance_id,handler_key,generation,config_revision,event_types,active,delay_ms,cursor) VALUES('SystemProcess','owned',1,1,'[]',1,0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.stopOriginalPlugin(ctx, "SystemProcess"); err == nil {
		t.Fatal("event-disable failure was swallowed")
	}
	loaded, ok := a.pluginRunningInstances.Load("SystemProcess")
	if !ok || loaded != object {
		t.Fatal("failed event disable removed running object")
	}
}
