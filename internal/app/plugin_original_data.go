package app

import "context"

// The original data deletion is a namespace operation, not a recovery or
// generation transition. Brush material grants are part of its own plugin
// data lifetime and must be revoked on reset or clone removal.
func (p *PluginStore) deleteOriginalPluginData(ctx context.Context, instance string) error {
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=? AND kind='data'`, instance); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM records WHERE kind=? AND id LIKE ?`, originalBrushOwnerKind, originalBrushOwnerPrefix(instance)+"%"); err != nil {
		return err
	}
	return tx.Commit()
}
