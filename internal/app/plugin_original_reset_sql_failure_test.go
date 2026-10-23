package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"light-media/internal/plugins/builtin"
)

func TestPluginOriginalResetDataDeleteFailureKeepsEarlierConfigDeletion(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	installCloneSource(t, a.pluginStore, builtin.SystemProcessManifest())
	id := "SystemProcess"
	if _, err := a.pluginStore.SaveConfig(ctx, id, 0, map[string]any{"enable": true, "owned": "before-delete"}); err != nil {
		t.Fatal(err)
	}
	before, err := a.pluginStore.SaveData(ctx, id, "owned-history", 0, []string{"must-remain"})
	if err != nil {
		t.Fatal(err)
	}
	if a.startOriginalPlugin(ctx, id) == nil {
		t.Fatal("real start failed")
	}
	var runsBefore int
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id=?`, id).Scan(&runsBefore); err != nil {
		t.Fatal(err)
	}
	// Only owned temporary DB; fail the second original force-delete operation.
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_reset_data_failure BEFORE DELETE ON plugin_values WHEN OLD.instance_id='SystemProcess' AND OLD.kind='data' BEGIN SELECT RAISE(ABORT,'owned data deletion failure'); END`); err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+id+"/reset", `{}`, cookie, "")
	if response.Code < 400 {
		t.Fatalf("data deletion error hidden as success: %d %s", response.Code, response.Body.String())
	}
	if _, err = a.pluginStore.Config(ctx, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("earlier config deletion rolled back or skipped: %v", err)
	}
	after, err := a.pluginStore.Data(ctx, id, "owned-history")
	if err != nil || after.Revision != before.Revision || !bytes.Equal(after.Value, before.Value) {
		t.Fatalf("failed data deletion changed KV: %v", err)
	}
	if _, loaded := a.pluginRunningInstances.Load(id); loaded {
		t.Fatal("failure incorrectly reloaded stopped instance")
	}
	var runsAfter int
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id=?`, id).Scan(&runsAfter); err != nil || runsAfter != runsBefore {
		t.Fatalf("failure unexpectedly initialized instance: before=%d after=%d error=%v", runsBefore, runsAfter, err)
	}
}
