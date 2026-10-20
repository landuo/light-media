package app

import (
	"context"
	"database/sql"
	"errors"
)

type brushFlowColdScopeKey struct{}
type brushFlowColdScope struct {
	p   *PluginStore
	tx  *sql.Tx
	run string
}

func brushFlowColdNoDeferred(ctx context.Context, p *PluginStore, q pluginRemoveLinkJournalReader, run PluginRun) bool {
	v, ok := ctx.Value(brushFlowColdScopeKey{}).(brushFlowColdScope)
	return ok && v.p == p && v.tx == q && (v.run == "" || v.run == run.ID) && run.State == "reconcile_pending"
}

// Accepted owner fact and deferred.started are one transaction. A cold run
// missing both has a sealed proof of zero native entry; it is closed, not replayed.
func (p *PluginStore) recoverBrushFlowUnentered(ctx context.Context) error {
	rows, e := p.store.DB.QueryContext(ctx, `SELECT r.id FROM plugin_runs r JOIN plugin_installations i ON i.instance_id=r.instance_id WHERE i.plugin_id='BrushFlowLanduo' AND r.version='5.2.15' AND r.state='reconcile_pending' AND NOT EXISTS(SELECT 1 FROM plugin_deferred_operations d WHERE d.run_id=r.id)`)
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
	for _, id := range ids {
		tx, e := p.store.DB.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		run, e := p.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, id))
		if e != nil {
			tx.Rollback()
			return e
		}
		if brushFlowReadRun(run) {
			tx.Rollback()
			continue
		}
		cold := context.WithValue(ctx, brushFlowColdScopeKey{}, brushFlowColdScope{p, tx, ""})
		s, _, proofErr := p.brushFlowRead(cold, tx, run.InstanceID, run.Generation)
		if proofErr != nil {
			tx.Rollback()
			if _, e = p.store.DB.ExecContext(ctx, `UPDATE plugin_runs SET error_code='BRUSH_FLOW_EVIDENCE_MISSING',updated_at=? WHERE id=? AND state='reconcile_pending'`, now(), id); e != nil {
				return e
			}
			continue
		}
		if _, exists := s.lookupRun(id); exists {
			tx.Rollback()
			return brushFlowEvidenceError()
		}
		var count int
		e = tx.QueryRowContext(ctx, `SELECT 1 FROM plugin_deferred_operations WHERE run_id=?`, id).Scan(&count)
		if !errors.Is(e, sql.ErrNoRows) {
			tx.Rollback()
			if e != nil {
				return e
			}
			return brushFlowEvidenceError()
		}
		if e = p.brushFlowCloseNotStartedTx(cold, tx, run); e == nil {
			_, e = tx.ExecContext(ctx, `DELETE FROM plugin_run_startup_blocks WHERE run_id=?`, id)
		}
		if e != nil {
			tx.Rollback()
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
	}
	return nil
}
