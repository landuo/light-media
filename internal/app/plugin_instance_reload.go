package app

import (
	"context"
	"database/sql"
)

// Explicit reload loads this authenticated instance's existing configuration.
// Business enabled flags remain unchanged and its existing init/reload handler
// alone decides event/schedule registration. This adds no second state machine.
func (p *PluginStore) preparePluginReloadTx(ctx context.Context, tx *sql.Tx, instance string) error {
	in, err := p.pluginLifecycleInstallation(ctx, tx, instance)
	if err != nil {
		return err
	}
	if !in.Installed {
		return bad("PLUGIN_INACTIVE", "插件已卸载")
	}
	if err = p.requirePluginUpdateActivation(ctx, tx, instance); err != nil {
		return err
	}
	if in.DesiredEnabled {
		return nil
	}
	state, err := pluginLifecycleState(in)
	if err != nil {
		return err
	}
	if state != pluginLifecycleCold {
		return bad("PLUGIN_MIGRATION_STAGED", "实例尚无已认证的可重新加载配置")
	}
	// Preserve the old imported-data hold only for actual migration records,
	// never infer imported/staged status merely from a new clone being disabled.
	var imported int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_migration_receipts WHERE instance_id=?`, instance).Scan(&imported); err != nil {
		return err
	}
	if imported != 0 {
		return bad("PLUGIN_MIGRATION_STAGED", "导入配置仍需明确核对保存")
	}
	_, err = tx.ExecContext(ctx, `UPDATE plugin_installations SET desired_enabled=1,updated_at=? WHERE instance_id=? AND generation=? AND installed=1`, now(), instance, in.Generation)
	return err
}
