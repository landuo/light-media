package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	bf "light-media/internal/plugins/brushflow"
)

// Called only by the native branch which never acquired the SDK process slot.
// Absence is proved by the sealed owner set, never by SQL NoRows alone. The
// actual run, optional prepared handshake, absence proof and terminal transition
// share one transaction, so another execution cannot enter Host in between.
func (p *PluginStore) brushFlowCancelBeforeSDK(ctx context.Context, run PluginRun) error {
	if run.Version != bf.Version {
		return brushFlowEvidenceError()
	}
	tx, e := p.store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = p.brushFlowCloseNotStartedTx(ctx, tx, run); e != nil {
		return e
	}
	return tx.Commit()
}
func (p *PluginStore) brushFlowCloseNotStartedTx(ctx context.Context, tx *sql.Tx, run PluginRun) error {
	if run.Version != bf.Version {
		return brushFlowEvidenceError()
	}
	actual, e := p.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, run.ID))
	if e != nil {
		return e
	}
	var plugin string
	if tx.QueryRowContext(ctx, `SELECT plugin_id FROM plugin_installations WHERE instance_id=?`, run.InstanceID).Scan(&plugin) != nil || plugin != bf.PluginID || (actual.State != "running" && !brushFlowColdNoDeferred(ctx, p, tx, actual)) || actual.InstanceID != run.InstanceID || actual.Version != run.Version || actual.Generation != run.Generation || actual.ConfigRevision != run.ConfigRevision || actual.Action != run.Action || !bytes.Equal(actual.Input, run.Input) {
		return brushFlowEvidenceError()
	}
	s, previous, e := p.brushFlowRead(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		return e
	}
	if _, ok := s.Runs[run.ID]; ok {
		return brushFlowEvidenceError()
	}
	method, raw, specErr := brushFlowDeferredSpec(actual)
	if specErr != nil && !brushFlowReadRun(actual) {
		return specErr
	}
	d, priorDeferred, e := p.readDeferred(ctx, tx, actual)
	if e == nil {
		if specErr != nil || d.Phase != "prepared" || d.Plugin != bf.PluginID || d.Version != run.Version || d.Generation != run.Generation || d.Revision != run.ConfigRevision || d.Action != run.Action || d.InputSHA != deferredInputSHA(run.Input) || d.Method != method || !deferredPayloadEqual(d.Raw, raw) {
			return brushFlowEvidenceError()
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	var currentGeneration int64
	if e = tx.QueryRowContext(ctx, `SELECT generation FROM plugin_installations WHERE instance_id=?`, run.InstanceID).Scan(&currentGeneration); e != nil {
		return e
	}
	// Retirement freezes the parent root and its prepared record. Current
	// generation scope rejects this old identity; do not rewrite an ancestor.
	if currentGeneration < run.Generation {
		return brushFlowEvidenceError()
	}
	if currentGeneration > run.Generation {
		if _, _, e = p.brushFlowRead(ctx, tx, run.InstanceID, currentGeneration); e != nil {
			return e
		}
	}
	// Known cancellation has its own authenticated terminal identity. It must
	// never become a fresh authorization if a plaintext run.state is rolled back.
	if !brushFlowReadRun(actual) && currentGeneration == run.Generation {
		s.Runs[run.ID] = brushFlowAction{RunID: run.ID, Action: run.Action, InputSHA: deferredInputSHA(run.Input), Method: method, Phase: "failed", ErrorCode: "BRUSH_FLOW_NOT_STARTED", Decision: "not_started", Revision: run.ConfigRevision, EffectiveRevision: run.ConfigRevision, Result: brushFlowNotStartedResult()}
		if e = p.brushFlowSave(ctx, tx, s, previous); e != nil {
			return e
		}
	}
	if priorDeferred != nil && currentGeneration == run.Generation {
		d.Phase = "terminal_failed"
		d.ErrorCode = "BRUSH_FLOW_NOT_STARTED"
		d.Result = brushFlowNotStartedResult()
		if e = p.writeDeferred(ctx, tx, d, priorDeferred); e != nil {
			return e
		}
	}
	result, e := p.seal("run_result", run.InstanceID, run.ID, brushFlowNotStartedResult())
	if e != nil {
		return e
	}
	r, e := tx.ExecContext(ctx, `UPDATE plugin_runs SET state='failed',error_code='BRUSH_FLOW_NOT_STARTED',result=?,updated_at=? WHERE id=? AND state=?`, result, now(), run.ID, actual.State)
	if e != nil {
		return e
	}
	if n, e := r.RowsAffected(); e != nil || n != 1 {
		return brushFlowEvidenceError()
	}
	if _, e = tx.ExecContext(ctx, `UPDATE plugin_schedule_occurrences SET state='failed' WHERE run_id=? AND state='running'`, run.ID); e != nil {
		return e
	}
	return nil
}

func brushFlowNotStartedResult() []byte {
	return []byte(`{"version":1,"decision":"not_started","effects_started":false}`)
}
func brushFlowKnownNotStarted(r brushFlowAction, run PluginRun) bool {
	method, _, e := brushFlowDeferredSpec(run)
	return e == nil && method != "" && r.RunID == run.ID && r.Action == run.Action && r.InputSHA == deferredInputSHA(run.Input) && r.Method == method && r.Revision == run.ConfigRevision && r.EffectiveRevision == r.Revision && r.Phase == "failed" && r.ErrorCode == "BRUSH_FLOW_NOT_STARTED" && r.Decision == "not_started" && !r.Entered && len(r.Effects) == 0 && bytes.Equal(r.Result, brushFlowNotStartedResult()) && run.State == "failed" && run.ErrorCode == r.ErrorCode && bytes.Equal(run.Result, r.Result)
}
