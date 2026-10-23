package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"light-media/internal/plugins/builtin"
)

// An offer is metadata only. The write endpoint rechecks the compiled target,
// authenticated current installation and config revision inside its transaction.
func (p *PluginStore) builtinUpdateOffer(ctx context.Context, in PluginInstallation) (*pluginUpdateRequest, string, error) {
	target, _, ok := builtin.Lookup(in.PluginID)
	if !ok || !in.Installed || target.Version == in.Version {
		return nil, "", nil
	}
	migration, ok := builtin.UpdateMigrationFor(in.PluginID, in.Version)
	if !ok || migration.ToVersion != target.Version || migration.ValidateConfig == nil {
		return nil, "当前版本尚无已验证的迁移声明", nil
	}
	config, err := p.Config(ctx, in.InstanceID)
	if errors.Is(err, sql.ErrNoRows) {
		config = PluginStoredValue{Value: json.RawMessage(`{}`)}
	} else if err != nil {
		return nil, "", err
	}
	if migration.ValidateConfig(config.Value) != nil {
		return nil, "原配置需要先核对，可能包含尚未确认的一次性操作", nil
	}
	return &pluginUpdateRequest{FromVersion: in.Version, TargetVersion: target.Version, Generation: in.Generation, Revision: config.Revision}, "", nil
}
