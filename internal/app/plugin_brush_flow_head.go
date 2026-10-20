package app

import (
	"context"
	"database/sql"
	"errors"
)

// This independent seal selects the current lifecycle generation and exact root. The
// owner root and plaintext installation row cannot separately select an ancestor.
// It is not an external monotonic anchor: rolling back the whole DB, including
// this seal, remains indistinguishable without an external trusted checkpoint.
type brushFlowHead struct {
	Version                                 int
	Purpose, Instance, IdentitySHA, RootSHA string
	Generation                              int64
}

const brushFlowHeadKey = "brush-flow-current-head/v1"

func brushFlowRootIdentity(r brushFlowRoot) string {
	return tokenHash(string(mustCleanSeedJSON(struct {
		Purpose, Instance, Event, ParentSHA string
		Generation, ParentGeneration        int64
	}{r.Purpose, r.Instance, r.Event, r.ParentSHA, r.Generation, r.ParentGeneration})))
}
func (p *PluginStore) brushFlowHeadRead(ctx context.Context, q pluginRemoveLinkJournalReader, instance string) (brushFlowHead, []byte, error) {
	var h brushFlowHead
	var cell []byte
	if e := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_brush_flow_heads WHERE instance_id=?`, instance).Scan(&cell); e != nil {
		return h, nil, e
	}
	raw, e := p.unseal("run_result", instance, brushFlowHeadKey, cell)
	if e != nil || cleanupJournalStrict(raw, &h) != nil || h.Version != 1 || h.Purpose != brushFlowHeadKey || h.Instance != instance || h.Generation < 1 || (!pluginStorageDigest.MatchString(h.IdentitySHA) || !pluginStorageDigest.MatchString(h.RootSHA)) {
		return h, nil, brushFlowEvidenceError()
	}
	return h, cell, nil
}
func (p *PluginStore) brushFlowHeadWriteTx(ctx context.Context, tx *sql.Tx, r brushFlowRoot, prior []byte) error {
	h := brushFlowHead{Version: 1, Purpose: brushFlowHeadKey, Instance: r.Instance, Generation: r.Generation, IdentitySHA: brushFlowRootIdentity(r), RootSHA: tokenHash(string(mustCleanSeedJSON(r)))}
	cell, e := p.seal("run_result", r.Instance, brushFlowHeadKey, mustCleanSeedJSON(h))
	if e != nil {
		return e
	}
	var result sql.Result
	if prior == nil {
		if r.Generation != 1 {
			return brushFlowEvidenceError()
		}
		result, e = tx.ExecContext(ctx, `INSERT INTO plugin_brush_flow_heads(instance_id,sealed) VALUES(?,?)`, r.Instance, cell)
	} else {
		result, e = tx.ExecContext(ctx, `UPDATE plugin_brush_flow_heads SET sealed=? WHERE instance_id=? AND sealed=?`, cell, r.Instance, prior)
	}
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return brushFlowEvidenceError()
	}
	return nil
}
func brushFlowHeadMissing(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return brushFlowEvidenceError()
	}
	return err
}
