package app

import (
	"context"
)

// Long source scans execute after releasing the SDK slot. These six fixed HTTP
// APIs may claim their own already authorized row, so status and a busy reply
// do not wait behind the application's sequential schedule worker. They use the
// same real Runner, sealed API grant and deferred owner checks as queued runs.
func (a *App) executeSeedRescueHTTP(ctx context.Context, run PluginRun) error {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
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
	if e = tx.Commit(); e != nil {
		return e
	}
	actual.State = "running"
	a.executePluginRun(ctx, actual)
	return nil
}
