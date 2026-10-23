package app

import (
	"context"
)

// Long source scans execute after releasing the SDK slot. These six fixed HTTP
// APIs may claim their own already authorized row, so status and a busy reply
// do not wait behind the application's sequential schedule worker. They use the
// same real Runner, sealed API grant and deferred owner checks as queued runs.
func (a *App) executeSeedRescueHTTP(ctx context.Context, run PluginRun) error {
	window, e := a.pluginStore.beginSupervisedClaim(ctx)
	if e != nil {
		return e
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	var lease *pluginExecutionLease
	commitAttempted := false
	defer func() {
		// Once Commit was attempted, only exact durable evidence may resolve
		// the claim. In particular, ErrTxDone is not an abort proof.
		if !commitAttempted {
			if err := tx.Rollback(); err == nil && lease != nil {
				lease.rollbackClaim()
			}
		}
	}()
	actual, e := a.pluginStore.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, run.ID))
	if e != nil {
		return e
	}
	if actual.State != "pending" {
		return nil
	}
	if !a.pluginStore.isSeedRescueRun(ctx, tx, actual) || actual.Action != "api" {
		return seedRescueEvidenceError()
	}
	if _, e = a.validatePluginHTTPRunTx(ctx, tx, actual); e != nil {
		return e
	}
	changed, e := tx.ExecContext(ctx, `UPDATE plugin_runs SET state='running',updated_at=? WHERE id=? AND state='pending'`, now(), run.ID)
	if e != nil {
		return e
	}
	n, e := changed.RowsAffected()
	if e != nil || n != 1 {
		return seedRescueEvidenceError()
	}
	if e = a.pluginStore.markPluginExecutionStartedTx(ctx, tx, actual); e != nil {
		return e
	}
	lease, e = a.pluginStore.reserveSupervisedClaimTx(ctx, tx, window, actual)
	if e != nil {
		return e
	}
	commitAttempted = true
	if e = a.pluginStore.commitSupervisedClaim(ctx, lease, tx.Commit()); e != nil {
		return e
	}
	actual.State = "running"
	actual.executionLease = lease
	return a.executePluginRun(ctx, actual)
}
