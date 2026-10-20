package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	bl "light-media/internal/plugins/brushflow/ledger"
)

// The actual App run and private deferred receipt are checked with the selected
// authenticated owner action, never an unauthenticated SQL membership index.
func (p *PluginStore) brushFlowValidateAction(ctx context.Context, q pluginRemoveLinkJournalReader, s brushFlowState, r brushFlowAction) error {
	if !pluginStorageID.MatchString(r.RunID) || !pluginStorageDigest.MatchString(r.InputSHA) || r.Revision < 1 || r.EffectiveRevision < r.Revision || (r.Phase != "started" && r.Phase != "confirmed" && r.Phase != "failed") {
		return brushFlowEvidenceError()
	}
	run, e := p.brushFlowLoadRun(ctx, q, r.RunID)
	if e != nil {
		return brushFlowEvidenceError()
	}
	if run.Version != "5.2.15" || run.InstanceID != s.Instance || run.Generation != s.Generation || run.Action != r.Action || run.ConfigRevision != r.Revision || deferredInputSHA(run.Input) != r.InputSHA {
		return brushFlowEvidenceError()
	}
	d, _, e := p.readDeferred(ctx, q, run)
	if errors.Is(e, sql.ErrNoRows) && brushFlowKnownNotStarted(r, run) {
		return s.check()
	}
	if e != nil {
		return brushFlowEvidenceError()
	}
	if r.ErrorCode == "BRUSH_FLOW_NOT_STARTED" && !brushFlowKnownNotStarted(r, run) {
		return brushFlowEvidenceError()
	}
	method, raw, e := p.brushFlowActionSpec(ctx, run)
	if e != nil || d.Plugin != "BrushFlowLanduo" || d.Version != "5.2.15" || d.Generation != run.Generation || d.Revision != r.Revision || d.InputSHA != r.InputSHA || d.Method != method || r.Method != method || d.Action != r.Action || !deferredPayloadEqual(d.Raw, raw) {
		return brushFlowEvidenceError()
	}
	switch d.Phase {
	case "started", "uncertain":
	case "confirmed":
		if r.Phase != "confirmed" || r.ErrorCode != "" || !bytes.Equal(r.Result, d.Result) {
			return brushFlowEvidenceError()
		}
	case "terminal_failed":
		if r.Phase != "failed" || r.ErrorCode != d.ErrorCode || !bytes.Equal(r.Result, d.Result) {
			return brushFlowEvidenceError()
		}
	default:
		return brushFlowEvidenceError()
	}
	for _, f := range r.Effects {
		if f.Kind == "" || (f.Phase != "started" && f.Phase != "confirmed") || !json.Valid(f.Args) {
			return brushFlowEvidenceError()
		}
	}
	return s.check()
}
func (p *PluginStore) brushFlowValueProof(ctx context.Context, tx *sql.Tx, s brushFlowState, key string) (json.RawMessage, bool, error) {
	proof, exists := s.lookupValue(key)
	if e := s.check(); e != nil {
		return nil, false, e
	}
	var rev int64
	var cell []byte
	e := tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key=?`, s.Instance, key).Scan(&rev, &cell)
	if errors.Is(e, sql.ErrNoRows) {
		if exists {
			return nil, false, brushFlowEvidenceError()
		}
		return nil, false, nil
	}
	if e != nil {
		return nil, false, e
	}
	if !exists || rev != proof.Revision {
		return nil, false, brushFlowEvidenceError()
	}
	raw, e := p.unseal("data", s.Instance, key, cell)
	if e != nil || tokenHash(string(raw)) != proof.SHA {
		return nil, false, brushFlowEvidenceError()
	}
	return raw, true, nil
}

// Claim must inspect this exact identity even when no unresolved mutations
// exist. A rolled-back plaintext terminal row cannot become a fresh request.
func (p *PluginStore) brushFlowClaimIdentityTx(ctx context.Context, tx *sql.Tx, plugin string, run PluginRun) error {
	if plugin != "BrushFlowLanduo" || run.Version != "5.2.15" {
		return nil
	}
	s, _, e := p.brushFlowReadCurrent(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		return e
	}
	if _, _, exists := s.lookupRunSummary(run.ID); exists {
		return brushFlowEvidenceError()
	}
	if run.Action == "task" {
		token, e := brushFlowSourceToken(run)
		if e != nil {
			return e
		}
		if s.pages != nil && s.pages.liveSourceIndex {
			var source brushFlowScanSource
			ok, e := s.pages.get(bl.Unresolved, s.pages.roots.Unresolved, brushFlowPendingSourcePrefix+token, &source)
			if e != nil || !ok || source.Run != run.ID || source.InputSHA != deferredInputSHA(run.Input) {
				return brushFlowEvidenceError()
			}
		}
	}
	return s.check()
}

// A live native call has already passed the complete strict invocation parser
// and accepted private deferred binding. Reuse that exact specification only
// for this same immutable input; SQL identities, ciphertext and receipts are
// still validated above on every point read. Cold/history paths parse normally.
func (p *PluginStore) brushFlowActionSpec(ctx context.Context, run PluginRun) (string, json.RawMessage, error) {
	c, ok := ctx.Value(brushFlowCallKey{}).(*brushFlowCall)
	d, bound := ctx.Value(pluginDeferredExecutionKey{}).(*pluginDeferredCall)
	lease, leased := ctx.Value(brushFlowLeaseKey{}).(brushFlowLease)
	if ok && bound && leased && lease.d == d && c != nil && lease.o == c.o && d != nil && c.o != nil && c.o.a.pluginStore == p && d.a == c.o.a && d.run.ID == c.run.ID && run.ID == c.run.ID && run.InstanceID == c.run.InstanceID && run.Version == c.run.Version && run.Generation == c.run.Generation && run.ConfigRevision == c.run.ConfigRevision && run.Action == c.run.Action && bytes.Equal(run.Input, c.run.Input) && bytes.Equal(d.run.Input, c.run.Input) && d.record.Method == c.method {
		return d.record.Method, d.record.Raw, nil
	}
	return brushFlowDeferredSpec(run)
}
