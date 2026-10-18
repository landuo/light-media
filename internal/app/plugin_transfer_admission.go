package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	transfer "light-media/internal/plugins/torrenttransfer"
)

type transferAdmissionKey struct{}
type transferAdmission struct {
	Run, Instance, Version, Action, InputSHA, Method string
	Generation, Revision                             int64
}
type transferRunFact struct {
	Run, Version, Action, InputSHA, Method, Phase string
	ErrorCode                                     string
	Generation, Revision, ResultRevision          int64
	Result                                        json.RawMessage `json:",omitempty"`
}

func transferAdmissionFromRun(run PluginRun, method string) transferAdmission {
	return transferAdmission{run.ID, run.InstanceID, run.Version, run.Action, deferredInputSHA(run.Input), method, run.Generation, run.ConfigRevision}
}
func transferAdmissionMatches(ctx context.Context, run PluginRun, d pluginDeferredRecord) bool {
	a, ok := ctx.Value(transferAdmissionKey{}).(transferAdmission)
	return ok && a.Run == run.ID && a.Instance == run.InstanceID && a.Version == run.Version && a.Action == run.Action && a.InputSHA == d.InputSHA && a.Method == d.Method && a.Generation == run.Generation && a.Revision == d.Revision
}

// The independent root proves the exact accepted run set. The ordinary Root
// ledger and sealed deferred receipt must agree in both directions; missing
// evidence cannot be replaced with an empty set or a new admission.
func (p *PluginStore) transferValidateDeferredTx(ctx context.Context, tx *sql.Tx, root transferOwnerRoot) error {
	rows, e := tx.QueryContext(ctx, "SELECT id FROM plugin_runs WHERE instance_id=? AND generation=? ORDER BY id", root.Instance, root.Generation)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	unknown := false
	for _, id := range ids {
		run, e := p.scanPluginRun(tx.QueryRowContext(ctx, "SELECT "+pluginRunColumns+" FROM plugin_runs WHERE id=?", id))
		if e != nil {
			return transfer.ErrJournalIntegrity
		}
		fact, exists := root.Runs[id]
		d, _, e := p.readDeferred(ctx, tx, run)
		if errors.Is(e, sql.ErrNoRows) {
			if exists || run.State == "succeeded" || run.State == "reconcile_pending" {
				return transfer.ErrJournalIntegrity
			}
			continue
		}
		if e != nil {
			return transfer.ErrJournalIntegrity
		}
		method, payload, e := transferDeferredSpec(run)
		if e != nil || !isTorrentTransfer(d.Plugin) || d.Version != "1.10.3" || d.Run != run.ID || d.Instance != root.Instance || d.Generation != root.Generation || d.Version != run.Version || d.Action != run.Action || d.InputSHA != deferredInputSHA(run.Input) || d.Method != method || !deferredPayloadEqual(d.Raw, payload) {
			return transfer.ErrJournalIntegrity
		}
		if !exists {
			if d.Phase == "prepared" && run.State != "succeeded" {
				continue
			}
			if d.Phase == "started" && run.State == "running" && transferAdmissionMatches(ctx, run, d) {
				continue
			}
			return transfer.ErrJournalIntegrity
		}
		seen[id] = true
		if fact.Run != id || fact.Version != d.Version || fact.Generation != d.Generation || fact.Action != d.Action || fact.InputSHA != d.InputSHA || fact.Method != d.Method || fact.Revision != d.Revision {
			return transfer.ErrJournalIntegrity
		}
		if fact.Phase == "accepted" {
			if fact.ResultRevision != 0 || len(fact.Result) != 0 || fact.ErrorCode != "" || run.ConfigRevision != fact.Revision {
				return transfer.ErrJournalIntegrity
			}
			if (d.Phase == "started" || d.Phase == "uncertain") && transferAdmissionMatches(ctx, run, d) && run.State == "running" {
				continue
			}
			if d.Phase == "started" || d.Phase == "uncertain" {
				unknown = true
				continue // Validate every later run and missing root fact first.
			}
			return transfer.ErrJournalIntegrity
		}
		revisionOK := run.ConfigRevision == fact.ResultRevision
		if !revisionOK && fact.ResultRevision == run.ConfigRevision+1 && (run.Action == "init" || run.Action == "reload") && d.Phase == "started" && d.Method == transfer.ConfigureCapability && transferAdmissionMatches(ctx, run, d) {
			revisionOK = true
		}
		if fact.Phase != "confirmed" || fact.ErrorCode != transferResultErrorCode(fact.Result) || !json.Valid(fact.Result) || fact.ResultRevision < fact.Revision || !revisionOK {
			return transfer.ErrJournalIntegrity
		}
		switch d.Phase {
		case "started", "uncertain":
		case "confirmed":
			if !bytes.Equal(d.Result, fact.Result) || d.ErrorCode != "" || fact.ErrorCode != "" {
				return transfer.ErrJournalIntegrity
			}
		case "terminal_failed":
			if fact.ErrorCode != transferSourceFailed || d.ErrorCode != fact.ErrorCode || !bytes.Equal(d.Result, fact.Result) {
				return transfer.ErrJournalIntegrity
			}
		default:
			return transfer.ErrJournalIntegrity
		}
	}
	for id := range root.Runs {
		if !seen[id] {
			return transfer.ErrJournalIntegrity
		}
	}
	if unknown {
		return transfer.ErrUnconfirmed
	}
	return nil
}
func (p *PluginStore) transferSaveRootTx(ctx context.Context, tx *sql.Tx, root transferOwnerRoot, prior []byte) error {
	raw, e := json.Marshal(root)
	if e != nil {
		return e
	}
	sealed, e := p.seal("run_result", root.Instance, transferRootKey(root.Generation), raw)
	if e != nil {
		return e
	}
	changed, e := tx.ExecContext(ctx, "UPDATE plugin_transfer_roots SET sealed=? WHERE instance_id=? AND generation=? AND sealed=?", sealed, root.Instance, root.Generation, prior)
	if e != nil {
		return e
	}
	n, e := changed.RowsAffected()
	if e != nil || n != 1 {
		return transfer.ErrJournalIntegrity
	}
	return nil
}
func (p *PluginStore) transferAcceptRun(ctx context.Context, run PluginRun) error {
	tx, e := p.store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = p.transferAcceptRunTx(ctx, tx, run); e != nil {
		return e
	}
	return tx.Commit()
}

// Started and the accepted original input are one durable admission. A crash
// cannot leave a legal started row outside the independently sealed run set.
func (p *PluginStore) transferAcceptRunTx(ctx context.Context, tx *sql.Tx, run PluginRun) error {
	root, prior, e := p.transferRootReadTx(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		return e
	}
	if e = p.transferValidateDeferredTx(ctx, tx, root); e != nil {
		return e
	}
	current, e := p.scanPluginRun(tx.QueryRowContext(ctx, "SELECT "+pluginRunColumns+" FROM plugin_runs WHERE id=?", run.ID))
	if e != nil {
		return e
	}
	d, _, e := p.readDeferred(ctx, tx, current)
	if e != nil || d.Phase != "started" || !transferAdmissionMatches(ctx, current, d) {
		return transfer.ErrJournalIntegrity
	}
	if _, ok := root.Runs[run.ID]; ok {
		return nil
	}
	root.Runs[run.ID] = transferRunFact{Run: run.ID, Version: run.Version, Action: run.Action, InputSHA: d.InputSHA, Method: d.Method, Phase: "accepted", Generation: run.Generation, Revision: d.Revision}
	root.Sequence++
	if e = p.transferSaveRootTx(ctx, tx, root, prior); e != nil {
		return e
	}
	return nil
}
func (p *PluginStore) transferCompleteRunTx(ctx context.Context, tx *sql.Tx, run PluginRun, result json.RawMessage, revision int64) error {
	root, prior, e := p.transferRootReadTx(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		return e
	}
	fact, ok := root.Runs[run.ID]
	if !ok || fact.InputSHA != deferredInputSHA(run.Input) || fact.Action != run.Action || revision < fact.Revision || !json.Valid(result) {
		return transfer.ErrJournalIntegrity
	}
	if fact.Phase == "confirmed" {
		if !bytes.Equal(fact.Result, result) || fact.ResultRevision != revision {
			return transfer.ErrJournalIntegrity
		}
		return nil
	}
	if fact.Phase != "accepted" {
		return transfer.ErrJournalIntegrity
	}
	fact.Phase = "confirmed"
	fact.ErrorCode = transferResultErrorCode(result)
	fact.Result = append(json.RawMessage{}, result...)
	fact.ResultRevision = revision
	root.Runs[run.ID] = fact
	root.Sequence++
	return p.transferSaveRootTx(ctx, tx, root, prior)
}
func transferSourceResult(run PluginRun, state transfer.RuntimeCheckpoint) (json.RawMessage, error) {
	if state.SourceError != "" {
		if state.SourceError != transferSourceFailed {
			return nil, transfer.ErrReplayDiverged
		}
		return json.Marshal(map[string]any{"completed": false, "source_error": state.SourceError, "counts": state.Counts})
	}
	if run.Action == "init" || run.Action == "reload" {
		return json.RawMessage(`{"configured":true}`), nil
	}
	if run.Action == "event" {
		return json.RawMessage(`{"completed":true}`), nil
	}
	return json.Marshal(map[string]any{"completed": true, "counts": state.Counts})
}
