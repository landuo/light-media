package app

import (
	"context"
	"encoding/json"

	"light-media/internal/plugins"
	sr "light-media/internal/plugins/seedrescue"
)

// Old interrupted pure reads have no native source timeline to replay or
// reconcile. The encrypted manifest and immutable request establish the exact
// compiled read role; arbitrary error codes or worker results do not.
func (p *PluginStore) recoverSeedRescueReads(ctx context.Context) error {
	rows, e := p.store.DB.QueryContext(ctx, `SELECT r.id FROM plugin_runs r JOIN plugin_installations i ON i.instance_id=r.instance_id WHERE i.plugin_id='SeedRescue' AND i.version='1.6.3' AND r.version='1.6.3' AND r.action='api' AND r.state IN ('running','reconcile_pending') AND NOT EXISTS(SELECT 1 FROM plugin_deferred_operations d WHERE d.run_id=r.id)`)
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
		if !seedRescueReadRun(run) {
			tx.Rollback()
			continue
		}
		var sealed []byte
		if e = tx.QueryRowContext(ctx, `SELECT manifest FROM plugin_installations WHERE instance_id=? AND plugin_id='SeedRescue' AND version='1.6.3'`, run.InstanceID).Scan(&sealed); e != nil {
			tx.Rollback()
			return e
		}
		raw, e := p.unseal("manifest", run.InstanceID, "main", sealed)
		if e != nil {
			tx.Rollback()
			return e
		}
		var manifest plugins.Manifest
		if json.Unmarshal(raw, &manifest) != nil || manifest.PluginID != sr.PluginID || manifest.Version != sr.Version {
			tx.Rollback()
			return seedRescueEvidenceError()
		}
		if _, e = tx.ExecContext(ctx, `UPDATE plugin_runs SET state='failed',error_code='SEED_RESCUE_READ_INTERRUPTED',updated_at=? WHERE id=? AND state IN ('running','reconcile_pending') AND NOT EXISTS(SELECT 1 FROM plugin_deferred_operations d WHERE d.run_id=plugin_runs.id)`, now(), run.ID); e != nil {
			tx.Rollback()
			return e
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM plugin_run_startup_blocks WHERE run_id=?`, run.ID); e != nil {
			tx.Rollback()
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
	}
	return nil
}
