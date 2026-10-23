package app

import (
	"context"
	"database/sql"
)

// A started private host operation may clear one-shot settings and continue
// under its original immutable receipt. This is not admission for a prepared
// operation and never grants permission to replay a started external effect.
func (p *PluginStore) validateDeferredRunRevisionTx(ctx context.Context, tx *sql.Tx, receipt pluginDeferredRecord, run PluginRun) error {
	var successors int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_run_config_transitions WHERE run_id=? AND from_revision>=?`, run.ID, run.ConfigRevision).Scan(&successors); err != nil {
		return err
	}
	if successors != 0 {
		return bad("PLUGIN_RUN_PROOF_INVALID", "插件执行修订发生回退")
	}
	if run.ConfigRevision == receipt.Revision {
		return nil
	}
	if receipt.Phase != "started" || receipt.ConfigSHA == "" || run.ConfigRevision < receipt.Revision {
		return bad("PLUGIN_SNAPSHOT_STALE", "插件操作尚未取得修订接续授权")
	}
	configSHA, err := p.verifyRunConfigChainTx(ctx, tx, receipt.Plugin, run, receipt.Revision, receipt.InputSHA, receipt.ConfigSHA)
	if err != nil {
		return err
	}
	if _, err = p.instanceMetadata(ctx, tx, run.InstanceID); err != nil {
		return err
	}
	var sealed []byte
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, run.InstanceID).Scan(&revision, &sealed); err != nil {
		return err
	}
	raw, err := p.unseal("config", run.InstanceID, "main", sealed)
	if err != nil {
		return err
	}
	if revision != run.ConfigRevision || deferredInputSHA(raw) != configSHA {
		return bad("PLUGIN_RUN_PROOF_INVALID", "插件当前配置与修订证明不匹配")
	}
	return nil
}
