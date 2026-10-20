package app

import (
	"context"
	"database/sql"

	"light-media/internal/plugins"
)

// The native MessageChain job, not the generic plugin worker, owns the exact
// claim of its child call. A cold pending child waits for that original job's
// authenticated continuation; generic claiming cannot discard its private
// source scope or steal a shared worker slot while the parent awaits it.
func (p *PluginStore) larkPrivateCoreRunTx(ctx context.Context, tx *sql.Tx, run PluginRun) (bool, error) {
	if run.Action != "module" {
		return false, nil
	}
	var plugin string
	if e := tx.QueryRowContext(ctx, `SELECT plugin_id FROM plugin_installations WHERE instance_id=?`, run.InstanceID).Scan(&plugin); e != nil {
		return false, e
	}
	if plugin != "LarkMessager" {
		return false, nil
	}
	var inv plugins.Invocation
	var ref struct {
		ID string `json:"call_id"`
	}
	if cleanupJournalStrict(run.Input, &inv) != nil || cleanupJournalStrict(inv.Input, &ref) != nil {
		return false, larkEvidenceError()
	}
	root, e := p.larkRootRead(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		return false, e
	}
	call, ok := root.Calls[ref.ID]
	if !ok || call.Run != run.ID || call.InputSHA != deferredInputSHA(run.Input) || call.ConfigSHA != tokenHash(string(inv.Config)) {
		return false, larkEvidenceError()
	}
	if call.Kind != "core_message" {
		return false, nil
	}
	inbox, ok := root.Inboxes[call.Origin]
	if !ok {
		return false, larkEvidenceError()
	}
	if e = p.larkInboxBackingTx(ctx, tx, inbox); e != nil {
		return false, e
	}
	steps, e := p.larkInboxStepsTx(ctx, tx, inbox)
	if e != nil {
		return false, e
	}
	if call.Ordinal < 0 || call.Ordinal >= len(steps) || steps[call.Ordinal].Call != call.ID || steps[call.Ordinal].Run != run.ID || !larkSameRaw(mustCleanSeedJSON(steps[call.Ordinal].Module), mustCleanSeedJSON(call.Module)) {
		return false, larkEvidenceError()
	}
	return true, nil
}
