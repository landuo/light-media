package app

import (
	"context"
	"database/sql"
	"errors"
)

// deleteOriginalPluginData maps PluginDataOper.del_data to the Go data namespace.
// It does not remove files, media, archives, or configured user directories.
// Native journals remain audit records under the old generation, while their
// new active namespace starts empty. Reading an authenticated root here is a
// storage-format operation, not permission from the old unknown-effect policy.
func (p *PluginStore) deleteOriginalPluginData(ctx context.Context, instance string) error {
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var plugin string
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT plugin_id,generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&plugin, &generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_, err = tx.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=? AND kind='data'`, instance)
			if err != nil {
				return err
			}
			return tx.Commit()
		}
		return err
	}
	var create func() error
	switch plugin {
	case "AutoBackup":
		root, _, e := p.autoBackupRootRead(ctx, tx, instance, generation)
		if e != nil {
			return e
		}
		parent := autoBackupParent{generation, tokenHash(string(mustCleanSeedJSON(root)))}
		create = func() error { return p.autoBackupGenesisTx(ctx, tx, instance, "reset", parent) }
	case "BrushFlowLanduo":
		root, _, e := p.brushFlowRootRead(ctx, tx, instance, generation)
		if e != nil {
			return e
		}
		parent := brushFlowParent{generation, tokenHash(string(mustCleanSeedJSON(root)))}
		create = func() error { return p.brushFlowGenesisTx(ctx, tx, instance, "reset", parent) }
	case "SeedRescue":
		root, _, e := p.seedRescueRootRead(ctx, tx, instance, generation)
		if e != nil {
			return e
		}
		parent := seedRescueParent{generation, tokenHash(string(mustCleanSeedJSON(root)))}
		create = func() error { return p.seedRescueGenesisTx(ctx, tx, instance, "reset", parent) }
	case "CloudflareSpeedTest":
		root, _, e := p.cloudflareRootRead(ctx, tx, instance, generation)
		if e != nil {
			return e
		}
		parent := cloudflareParent{generation, tokenHash(string(mustCleanSeedJSON(root)))}
		create = func() error { return p.cloudflareGenesisTx(ctx, tx, instance, "reset", parent) }
	case "IYUUAutoSeed":
		root, _, e := p.iyuuRootReadTx(ctx, tx, instance, generation)
		if e != nil {
			return e
		}
		parent := iyuuOwnerParent{generation, tokenHash(string(mustCleanSeedJSON(root)))}
		create = func() error { return p.iyuuGenesisTx(ctx, tx, instance, "reset", parent) }
	case "TorrentTransfer", "TorrentTransfermusic":
		root, _, e := p.transferRootReadTx(ctx, tx, instance, generation)
		if e != nil {
			return e
		}
		parent := transferOwnerParent{generation, tokenHash(string(mustCleanSeedJSON(root)))}
		create = func() error { return p.transferGenesisTx(ctx, tx, instance, "reset", parent) }
	case "ScrapeFileClean":
		create = func() error { return p.scrapeCleanGenesisTx(ctx, tx, instance, "reset") }
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=? AND kind='data'`, instance); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_installations SET generation=generation+1 WHERE instance_id=?`, instance); err != nil {
		return err
	}
	if err = p.recordPluginGenerationLifecycleTx(ctx, tx, instance); err != nil {
		return err
	}
	// The generation is an internal namespace binding; never an extra user step.
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_schedules SET active=0 WHERE instance_id=?`, instance); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_event_subscriptions SET active=0 WHERE instance_id=?`, instance); err != nil {
		return err
	}
	if create != nil {
		if err = create(); err != nil {
			return err
		}
	}
	return tx.Commit()
}
