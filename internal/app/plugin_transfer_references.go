package app

import (
	"context"
	"database/sql"
	"encoding/json"
	transfer "light-media/internal/plugins/torrenttransfer"
)

type transferOriginalImportKey struct{}
type transferOriginalImportAdmission struct{ Instance, Migration, ConfigSHA string }

// Original downloader names belong to the original snapshot. A coincidentally
// equal current native name is not a migration receipt or an activation grant.
func (p *PluginStore) stageImportedTransferReferencesTx(ctx context.Context, tx *sql.Tx, instance, migration string) error {
	var plugin string
	var g int64
	if e := tx.QueryRowContext(ctx, "SELECT plugin_id,generation FROM plugin_installations WHERE instance_id=?", instance).Scan(&plugin, &g); e != nil {
		return e
	}
	if !isTorrentTransfer(plugin) {
		return nil
	}
	root, prior, e := p.transferRootReadTx(ctx, tx, instance, g)
	if e != nil {
		return e
	}
	if root.ReferenceProtocol != "native-downloader-name/v1" || len(root.Runs) != 0 {
		return transfer.ErrReplayDiverged
	}
	var count int
	if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM plugin_migration_runs WHERE id=? AND instance_id=? AND source_plugin_id=? AND state='planned'", migration, instance, plugin).Scan(&count); e != nil {
		return e
	}
	if count != 1 {
		return transfer.ErrReplayDiverged
	}
	root.ReferenceProtocol = "moviepilot-reference-unclaimed/v1"
	root.ReferenceMigration = migration
	root.Sequence++
	return p.transferSaveRootTx(ctx, tx, root, prior)
}
func (p *PluginStore) transferConfigAdmissionTx(ctx context.Context, tx *sql.Tx, instance string, revision int64, raw json.RawMessage) error {
	if _, e := p.transferTransitionParentTx(ctx, tx, instance); e != nil {
		return e
	}
	var g int64
	if e := tx.QueryRowContext(ctx, "SELECT generation FROM plugin_installations WHERE instance_id=?", instance).Scan(&g); e != nil {
		return e
	}
	root, _, e := p.transferRootReadTx(ctx, tx, instance, g)
	if e != nil {
		return e
	}
	if root.ReferenceProtocol == "native-downloader-name/v1" || root.ReferenceProtocol == reviewedTransferProtocol {
		return nil
	}
	grant, ok := ctx.Value(transferOriginalImportKey{}).(transferOriginalImportAdmission)
	if !ok || grant.Instance != instance || grant.Migration != root.ReferenceMigration || grant.ConfigSHA != tokenHash(string(raw)) || revision != 0 {
		return bad("TORRENT_TRANSFER_REFERENCES_UNCLAIMED", "原版下载器引用尚未确认映射")
	}
	var count int
	if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM plugin_migration_runs WHERE id=? AND instance_id=? AND source_plugin_id=? AND state='planned'", grant.Migration, instance, root.Plugin).Scan(&count); e != nil {
		return e
	}
	if count != 1 {
		return transfer.ErrJournalIntegrity
	}
	return nil
}
