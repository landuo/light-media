package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	lark "light-media/internal/plugins/larkmessager"
)

func (a *App) larkCoreScopeTx(ctx context.Context, tx *sql.Tx, core *pluginLarkCoreExecution) (pluginLarkRootView, pluginLarkInbox, error) {
	if core == nil || core.a != a || ctx.Value(pluginLarkCoreKey{}) != core || !a.pluginStore.larkCoreIsHeld(core) {
		return pluginLarkRootView{}, pluginLarkInbox{}, lark.ErrDenied
	}
	root, e := a.pluginStore.larkRootRead(ctx, tx, core.inbox.Instance, core.inbox.Generation)
	if e != nil {
		return root, pluginLarkInbox{}, e
	}
	f, ok := root.Inboxes[core.inbox.ID]
	if !ok || f.Job != core.job.ID || f.State != "started" {
		return root, f, larkEvidenceError()
	}
	if e = a.pluginStore.larkInboxBackingTx(ctx, tx, f); e != nil {
		return root, f, e
	}
	actual, e := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, core.job.ID))
	if e != nil {
		return root, f, e
	}
	if actual.State != "running" || actual.Generation != core.job.Generation || !larkSameRaw(actual.Payload, core.job.Payload) {
		return root, f, lark.ErrDenied
	}
	var version string
	var generation, revision int64
	var installed, enabled bool
	e = tx.QueryRowContext(ctx, `SELECT i.version,i.generation,i.installed,i.desired_enabled,v.revision FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=? AND i.plugin_id='LarkMessager'`, f.Instance).Scan(&version, &generation, &installed, &enabled, &revision)
	if e != nil || version != "0.9.1" || !installed || !enabled || generation != f.Generation || revision != f.Revision {
		return root, f, lark.ErrDenied
	}
	return root, f, nil
}

// Each original MessageChain module call becomes an immutable child source
// fact. Retrying a core job can consume its existing result but never allocate
// a second send for an old source step, even after an acknowledgement is lost.
func (a *App) larkCoreModule(ctx context.Context, core *pluginLarkCoreExecution, module pluginLarkModuleInput) (json.RawMessage, error) {
	run, e := a.larkPrepareCoreModule(ctx, core, module)
	if e != nil {
		return nil, e
	}
	result, e := a.executeLarkSourceRun(ctx, run)
	if e != nil {
		if pluginErrorCodeInternal(e) == "LARK_SOURCE_FAILED" {
			core.ordinal++
		}
		return nil, e
	}
	core.ordinal++
	return result, nil
}
func (a *App) larkPrepareCoreModule(ctx context.Context, core *pluginLarkCoreExecution, module pluginLarkModuleInput) (PluginRun, error) {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return PluginRun{}, e
	}
	defer tx.Rollback()
	root, f, e := a.larkCoreScopeTx(ctx, tx, core)
	if e != nil {
		return PluginRun{}, e
	}
	steps, e := a.pluginStore.larkInboxStepsTx(ctx, tx, f)
	if e != nil {
		return PluginRun{}, e
	}
	var run PluginRun
	ordinal := core.ordinal
	if ordinal < len(steps) {
		step := steps[ordinal]
		call, ok := root.Calls[step.Call]
		if (step.Kind != "" && step.Kind != "module") || !ok || call.Kind != "core_message" || call.Origin != f.ID || call.Ordinal != ordinal || call.Run != step.Run || !larkSameRaw(mustCleanSeedJSON(step.Module), mustCleanSeedJSON(module)) || !larkSameRaw(mustCleanSeedJSON(call.Module), mustCleanSeedJSON(module)) {
			return PluginRun{}, larkEvidenceError()
		}
		run, e = a.pluginStore.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, step.Run))
		if e != nil {
			return PluginRun{}, e
		}
	} else {
		if ordinal != len(steps) {
			return PluginRun{}, larkEvidenceError()
		}
		sourceID := f.SourceID + "/module/" + notificationChannelRevisionKey(int64(ordinal))
		id := "lc_" + a.pluginStore.noticeID("lark-call", f.Instance, sourceID)
		run, e = a.pluginStore.enqueueRunTx(ctx, tx, f.Instance, "module", mustCleanSeedJSON(map[string]string{"call_id": id}))
		if e != nil {
			return PluginRun{}, e
		}
		call := pluginLarkSourceCall{Version: 1, ID: id, Instance: f.Instance, SourceID: sourceID, Kind: "core_message", Origin: f.ID, Ordinal: ordinal, Run: run.ID, Generation: f.Generation, Revision: f.Revision, InputSHA: deferredInputSHA(run.Input), ConfigSHA: f.ConfigSHA, Module: module}
		sealed, e := a.pluginStore.seal("run_result", f.Instance, larkCallKey(f.Generation, id), mustCleanSeedJSON(call))
		if e != nil {
			return PluginRun{}, e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO plugin_lark_calls VALUES(?,?,?,?,?,?)`, f.Instance, f.Generation, id, sourceID, run.ID, sealed); e != nil {
			return PluginRun{}, e
		}
		next := root.Root
		next.Sequence++
		if e = a.pluginStore.larkAppendRootTx(ctx, tx, root, next, "call", id, larkSourceCallSHA(call)); e != nil {
			return PluginRun{}, e
		}
		step := pluginLarkInboxStep{Version: 1, Inbox: f.ID, Ordinal: ordinal, Module: module, Call: id, Run: run.ID, PreviousSHA: f.LastSHA}
		sealed, e = a.pluginStore.seal("lark_inbox_step", f.Instance, f.ID+"/"+notificationChannelRevisionKey(int64(ordinal)), mustCleanSeedJSON(step))
		if e != nil {
			return PluginRun{}, e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO plugin_lark_inbox_steps VALUES(?,?,?)`, f.ID, ordinal, sealed); e != nil {
			return PluginRun{}, e
		}
		root, e = a.pluginStore.larkRootRead(ctx, tx, f.Instance, f.Generation)
		if e != nil {
			return PluginRun{}, e
		}
		f.Count++
		f.LastSHA = larkInboxStepSHA(step)
		if e = a.pluginStore.larkSaveInboxTx(ctx, tx, root, f); e != nil {
			return PluginRun{}, e
		}
	}
	if e = tx.Commit(); e != nil {
		return PluginRun{}, e
	}
	return run, nil
}

// Only a trusted native producer can call this entry. It claims precisely the
// persisted child run without selecting unrelated queue work. The SQL main
// queue and its existing admission gates remain authoritative. No outer SDK
// slot or owner lock is held while a child worker/owner is awaited.
func (a *App) executeLarkSourceRun(ctx context.Context, run PluginRun) (json.RawMessage, error) {
	return a.executeLarkBoundRun(ctx, run, lark.ModuleCapability)
}
func (a *App) executeLarkBoundRun(ctx context.Context, run PluginRun, method string) (json.RawMessage, error) {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	actual, e := a.pluginStore.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, run.ID))
	if e != nil {
		tx.Rollback()
		return nil, e
	}
	if actual.InstanceID != run.InstanceID || actual.Version != run.Version || actual.Generation != run.Generation || actual.ConfigRevision != run.ConfigRevision || actual.Action != run.Action || !larkSameRaw(actual.Input, run.Input) {
		tx.Rollback()
		return nil, lark.ErrDenied
	}
	claimed := false
	if actual.State == "pending" {
		var plugin string
		if e = tx.QueryRowContext(ctx, `SELECT plugin_id FROM plugin_installations WHERE instance_id=?`, run.InstanceID).Scan(&plugin); e == nil && plugin != "LarkMessager" {
			e = lark.ErrDenied
		}
		if e == nil {
			e = a.pluginStore.pluginNativeQueueAdmissionTx(ctx, tx, run.InstanceID, plugin)
		}
		if e == nil {
			_, _, e = a.larkSourceTx(ctx, tx, actual, method)
		}
		if e != nil {
			tx.Rollback()
			return nil, e
		}
		r, e := tx.ExecContext(ctx, `UPDATE plugin_runs SET state='running',updated_at=? WHERE id=? AND state='pending'`, now(), actual.ID)
		if e != nil {
			tx.Rollback()
			return nil, e
		}
		if n, _ := r.RowsAffected(); n != 1 {
			tx.Rollback()
			return nil, larkEvidenceError()
		}
		if e = a.pluginStore.markPluginExecutionStartedTx(ctx, tx, actual); e != nil {
			tx.Rollback()
			return nil, e
		}
		actual.State = "running"
		claimed = true
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	if claimed {
		a.executePluginRun(ctx, actual)
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		actual, e = a.pluginStore.Run(ctx, run.ID)
		if e != nil {
			return nil, e
		}
		switch actual.State {
		case "succeeded":
			return actual.Result, nil
		case "reconcile_pending", "failed", "cancelled":
			_, _, found, proofErr := a.pluginStore.larkOutcome(ctx, actual, method)
			if proofErr != nil {
				return nil, proofErr
			}
			if !found {
				return nil, larkUnknownError()
			}
			return nil, bad("LARK_SOURCE_FAILED", "Lark消息模块执行失败，原结果已保留")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
