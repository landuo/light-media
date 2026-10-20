package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	lark "light-media/internal/plugins/larkmessager"
)

// A source call is created by a real native producer in the same transaction as
// its immutable main-queue run. Neither an SDK method nor a public API accepts
// this struct. Keeping source_id unique across generations prevents an old
// queue item from acquiring a new send merely by reinstalling the plugin.
type pluginLarkSourceCall struct {
	Version                                                        int
	ID, Instance, SourceID, Kind, Origin, Run, InputSHA, ConfigSHA string
	Ordinal                                                        int
	Generation, Revision                                           int64
	Module                                                         pluginLarkModuleInput
}

func larkCallKey(g int64, id string) string           { return larkRootKey(g) + "/call/" + id }
func larkSourceCallSHA(c pluginLarkSourceCall) string { return tokenHash(string(mustCleanSeedJSON(c))) }

func larkNotificationFromSource(v any) (lark.Notification, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return lark.Notification{}, e
	}
	data, e := lark.DecodeSourceObject(raw)
	if e != nil || data == nil {
		return lark.Notification{}, lark.ErrDenied
	}
	// Source Notification accepts integer IDs; Lark's own target resolution uses
	// str(id). Preserve that conversion rather than float64 coercion.
	for _, key := range []string{"userid", "original_message_id"} {
		if value, ok := data[key]; ok && value != nil {
			converted, e := lark.PythonString(value)
			if e != nil {
				return lark.Notification{}, e
			}
			data[key] = converted
		}
	}
	raw, e = json.Marshal(data)
	if e != nil {
		return lark.Notification{}, e
	}
	var out lark.Notification
	if e = json.Unmarshal(raw, &out); e != nil {
		return out, e
	}
	return out, nil
}

// This producer consumes only the authenticated original queue message already
// in its durable intent phase. Repeated calls recover the same bound run; they
// never allocate another source identity or change its recipients.
func (a *App) enqueueLarkNotice(ctx context.Context, instance, queueID string) (PluginRun, error) {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return PluginRun{}, e
	}
	defer tx.Rollback()
	q, e := a.pluginStore.readPluginMessage(ctx, tx, queueID)
	if e != nil {
		return PluginRun{}, e
	}
	if q.State != "intent" || len(q.Call.Args) != 1 || q.Call.Args[0] != "post_message" {
		return PluginRun{}, lark.ErrDenied
	}
	n, e := larkNotificationFromSource(q.Call.Kwargs["message"])
	if e != nil {
		return PluginRun{}, e
	}
	sourceID := "notice-queue/" + q.ID
	var priorGen int64
	var priorID, priorRun string
	e = tx.QueryRowContext(ctx, `SELECT generation,call_id,run_id FROM plugin_lark_calls WHERE instance_id=? AND source_id=?`, instance, sourceID).Scan(&priorGen, &priorID, &priorRun)
	if e == nil {
		root, e := a.pluginStore.larkRootRead(ctx, tx, instance, priorGen)
		if e != nil {
			return PluginRun{}, e
		}
		fact, ok := root.Calls[priorID]
		if !ok || fact.SourceID != sourceID || fact.Run != priorRun || fact.Kind != "notice_queue" || fact.Origin != q.ID {
			return PluginRun{}, larkEvidenceError()
		}
		run, e := a.pluginStore.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, priorRun))
		if e != nil {
			return run, e
		}
		return run, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return PluginRun{}, e
	}
	var plugin, version string
	var installed, enabled bool
	var generation int64
	if e = tx.QueryRowContext(ctx, `SELECT plugin_id,version,installed,desired_enabled,generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&plugin, &version, &installed, &enabled, &generation); e != nil {
		return PluginRun{}, e
	}
	if plugin != "LarkMessager" || version != "0.9.1" || !installed || !enabled {
		return PluginRun{}, lark.ErrDenied
	}
	root, e := a.pluginStore.larkRootRead(ctx, tx, instance, generation)
	if e != nil {
		return PluginRun{}, e
	}
	id := "lc_" + a.pluginStore.noticeID("lark-call", instance, sourceID)
	input := mustCleanSeedJSON(map[string]string{"call_id": id})
	run, e := a.pluginStore.enqueueRunTx(ctx, tx, instance, "module", input)
	if e != nil {
		return run, e
	}
	var invocation struct {
		Action string
		Config json.RawMessage
		Input  json.RawMessage
	}
	if cleanupJournalStrict(run.Input, &invocation) != nil {
		return run, larkEvidenceError()
	}
	call := pluginLarkSourceCall{Version: 1, ID: id, Instance: instance, Generation: generation, Revision: run.ConfigRevision, SourceID: sourceID, Kind: "notice_queue", Origin: q.ID, Run: run.ID, InputSHA: deferredInputSHA(run.Input), ConfigSHA: tokenHash(string(invocation.Config)), Module: pluginLarkModuleInput{Name: "post_message", Notification: n}}
	value, e := a.pluginStore.seal("run_result", instance, larkCallKey(generation, id), mustCleanSeedJSON(call))
	if e != nil {
		return run, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO plugin_lark_calls VALUES(?,?,?,?,?,?)`, instance, generation, id, sourceID, run.ID, value); e != nil {
		return run, e
	}
	next := root.Root
	next.Sequence++
	if e = a.pluginStore.larkAppendRootTx(ctx, tx, root, next, "call", id, larkSourceCallSHA(call)); e != nil {
		return run, e
	}
	return run, tx.Commit()
}
