package app

import "context"

// resetOriginalPluginData implements the force-delete portion of the original
// endpoint. Configuration and plugin KV are separate operations, in that order;
// a later failure does not roll back an earlier successful original operation.
// Native adapters with storage outside plugin_values still require their
// original PluginDataReset compensation/data deletion binding before this slice
// can be described as full-catalog Reset support.
func (a *App) resetOriginalPluginData(ctx context.Context, instance string) error {
	if err := a.sendOriginalPluginDataResetChecked(ctx, instance); err != nil {
		return err
	}
	if err := a.stopOriginalPlugin(ctx, instance); err != nil {
		return err
	}
	a.pluginMu.Lock()
	_, err := a.Store.DB.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=? AND kind='config'`, instance)
	if err == nil {
		a.invalidateOriginalBaseConfig(instance)
	}
	a.pluginMu.Unlock()
	if err != nil {
		return err
	}
	return a.pluginStore.deleteOriginalPluginData(ctx, instance)
}
