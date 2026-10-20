package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"

	"light-media/internal/plugins"
	bl "light-media/internal/plugins/brushflow/ledger"
)

const brushFlowPendingSourcePrefix = "source:"

func brushFlowSourceToken(run PluginRun) (string, error) {
	var inv plugins.Invocation
	var input struct {
		ID string `json:"event_id"`
	}
	if cleanupJournalStrict(run.Input, &inv) != nil || cleanupJournalStrict(inv.Input, &input) != nil || !pluginStorageID.MatchString(input.ID) {
		return "", brushFlowEvidenceError()
	}
	return input.ID, nil
}

// Both membership and absence come from the sealed Unresolved collection.
// SQL is only a cross-check of each authenticated source, never its selector.
func (s brushFlowState) pendingSources(ctx context.Context, q pluginRemoveLinkJournalReader) func(func(string, brushFlowScanSource) bool) {
	return func(yield func(string, brushFlowScanSource) bool) {
		if s.pages == nil || !s.pages.liveSourceIndex {
			for key, source := range s.allSources() {
				var phase string
				if q.QueryRowContext(ctx, `SELECT state FROM plugin_runs WHERE id=?`, source.Run).Scan(&phase) != nil {
					return
				}
				if phase == "pending" || phase == "running" || phase == "reconcile_pending" {
					if !yield(key, source) {
						return
					}
				}
			}
			return
		}
		v := s.pages
		e := v.iterate(bl.Unresolved, v.roots.Unresolved, func(key string, raw []byte) error {
			if !strings.HasPrefix(key, brushFlowPendingSourcePrefix) {
				return nil
			}
			token := strings.TrimPrefix(key, brushFlowPendingSourcePrefix)
			if v.closedSources[token] {
				return nil
			}
			var source brushFlowScanSource
			if cleanupJournalStrict(raw, &source) != nil || source.Token != token {
				return brushFlowEvidenceError()
			}
			exact, ok := s.lookupSource(token)
			if !ok || !bytes.Equal(mustCleanSeedJSON(source), mustCleanSeedJSON(exact)) {
				return brushFlowEvidenceError()
			}
			if e := v.p.brushFlowValidateSource(ctx, q, s, token, source); e != nil {
				return e
			}
			var phase string
			if q.QueryRowContext(ctx, `SELECT state FROM plugin_runs WHERE id=?`, source.Run).Scan(&phase) != nil {
				return brushFlowEvidenceError()
			}
			if phase != "pending" && phase != "running" && phase != "reconcile_pending" {
				return brushFlowEvidenceError()
			}
			if !yield(token, source) {
				return errBrushFlowPageStop
			}
			return nil
		})
		if !errors.Is(e, errBrushFlowPageStop) {
			v.fail(e)
		}
	}
}

// Called only by Claim's exact private canceled branch, before its SQL state
// transition in the same transaction. Retired generations remain immutable.
func (p *PluginStore) brushFlowCancelQueuedClaimTx(ctx context.Context, tx *sql.Tx, run PluginRun) error {
	if run.Action != "task" || run.Version != "5.2.15" || !p.isBrushFlowRun(ctx, tx, run) {
		return nil
	}
	h, _, e := p.brushFlowHeadRead(ctx, tx, run.InstanceID)
	if e != nil {
		return brushFlowHeadMissing(e)
	}
	if h.Generation > run.Generation {
		return nil
	}
	if h.Generation != run.Generation {
		return brushFlowEvidenceError()
	}
	s, prior, e := p.brushFlowReadCurrent(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		return e
	}
	if _, _, exists := s.lookupRunSummary(run.ID); exists {
		return brushFlowEvidenceError()
	}
	token, e := brushFlowSourceToken(run)
	if e != nil {
		return e
	}
	source, ok := s.lookupSource(token)
	if !ok || source.Run != run.ID || source.InputSHA != deferredInputSHA(run.Input) {
		return brushFlowEvidenceError()
	}
	if e = p.brushFlowValidateSource(ctx, tx, s, token, source); e != nil {
		return e
	}
	if s.pages == nil {
		return nil
	} // legacy state conversion follows its full audit
	s.pages.closedSources[token] = true
	return p.brushFlowSave(ctx, tx, s, prior)
}

// A private worker terminal failure before the atomic started+owner transaction
// has an authenticated absence proof. Publish its fixed zero-effect terminal
// identity and close the queued source together, rather than trust run.state.
func (p *PluginStore) brushFlowFinishUnenteredTaskTx(ctx context.Context, tx *sql.Tx, run PluginRun, state string) (bool, error) {
	if state != "failed" || run.Action != "task" || run.Version != "5.2.15" || !p.isBrushFlowRun(ctx, tx, run) {
		return false, nil
	}
	s, _, e := p.brushFlowReadCurrent(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		return false, e
	}
	if _, _, exists := s.lookupRunSummary(run.ID); exists {
		return false, s.check()
	}
	if e = s.check(); e != nil {
		return false, e
	}
	d, _, e := p.readDeferred(ctx, tx, run)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return false, e
	}
	if e == nil && d.Phase != "prepared" {
		return false, brushFlowEvidenceError()
	}
	if e = p.brushFlowCloseNotStartedTx(ctx, tx, run); e != nil {
		return false, e
	}
	return true, nil
}

// Full cold/lifecycle audit checks both directions: every retained source's
// live/terminal owner proof agrees with its authenticated live membership.
func (p *PluginStore) brushFlowAuditSourceMembership(ctx context.Context, q pluginRemoveLinkJournalReader, s brushFlowState, source brushFlowScanSource) error {
	if s.pages == nil || !s.pages.liveSourceIndex {
		return nil
	}
	var phase string
	if q.QueryRowContext(ctx, `SELECT state FROM plugin_runs WHERE id=?`, source.Run).Scan(&phase) != nil {
		return brushFlowEvidenceError()
	}
	owner, _, exists := s.lookupRunSummary(source.Run)
	terminal := exists && (owner.Phase == "confirmed" || owner.Phase == "failed")
	if terminal && phase == "pending" {
		return brushFlowEvidenceError()
	}
	expected := (phase == "pending" || phase == "running" || phase == "reconcile_pending") && !terminal
	var indexed brushFlowScanSource
	found, e := s.pages.get(bl.Unresolved, s.pages.roots.Unresolved, brushFlowPendingSourcePrefix+source.Token, &indexed)
	if e != nil || found != expected || found && !bytes.Equal(mustCleanSeedJSON(source), mustCleanSeedJSON(indexed)) {
		return brushFlowEvidenceError()
	}
	return s.check()
}
