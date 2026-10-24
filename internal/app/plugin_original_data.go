package app

import "context"

// The original data deletion is a namespace operation, not a recovery or
// generation transition. It never reads or recreates native owner receipts.
func (p *PluginStore) deleteOriginalPluginData(ctx context.Context, instance string) error {
	_, err := p.store.DB.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=? AND kind='data'`, instance)
	return err
}
