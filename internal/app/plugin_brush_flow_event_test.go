package app

import (
	"context"
	"encoding/json"
	"testing"
)

// Called after the real SDK/native add produced its source event. No test-only
// event producer or installed listener stands in for the source receipt.
func brushFlowVerifyOriginalTrigger(t *testing.T, a *App) {
	t.Helper()
	ctx := context.Background()
	var event DomainEvent
	var raw []byte
	var seq int64
	if e := a.Store.DB.QueryRow(`SELECT seq,payload FROM domain_events WHERE json_extract(payload,'$.event_type')='plugin.triggered'`).Scan(&seq, &raw); e != nil {
		t.Fatal(e)
	}
	if e := json.Unmarshal(raw, &event); e != nil {
		t.Fatal(e)
	}
	event.Sequence = seq
	payload, e := a.pluginStore.brushFlowTriggeredPayload(ctx, a.Store.DB, event)
	var original map[string]any
	if e != nil || json.Unmarshal(payload, &original) != nil || original["event_name"] != "brushflow_download_added" || original["plugin_id"] != "BrushFlowLanduo" || original["data"] == nil {
		t.Fatal("outbox cursor cannot change the authenticated source fact", e, string(payload))
	}
	for _, query := range []string{
		`DELETE FROM plugin_trigger_sources WHERE id=?`,
		`UPDATE plugin_trigger_sources SET ordinal=ordinal+1 WHERE id=?`,
		`DELETE FROM domain_events WHERE json_extract(payload,'$.plugin_trigger_id')=?`,
		`UPDATE domain_events SET payload=json_set(payload,'$.subject_id','forged') WHERE json_extract(payload,'$.plugin_trigger_id')=?`,
	} {
		tx, e := a.Store.DB.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		result, e := tx.ExecContext(ctx, query, event.PluginTriggerID)
		if e != nil {
			tx.Rollback()
			t.Fatal(e)
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			tx.Rollback()
			t.Fatal("damage fixture did not touch source row", query, n)
		}
		_, e = a.pluginStore.brushFlowTriggeredPayload(ctx, tx, event)
		tx.Rollback()
		if e == nil {
			t.Fatal("deleted or changed evidence exposed an original event", query)
		}
	}
	if _, e = a.pluginStore.brushFlowTriggeredPayload(ctx, a.Store.DB, event); e != nil {
		t.Fatal("rollback did not restore authenticated source fact", e)
	}
}
