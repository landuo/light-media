package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

// NormalizeInstanceDefaults is the framework's pure missing-default write path.
// Callers are trusted host adapters using reviewed business declarations, never
// arbitrary worker patches. In particular it is NOT SaveConfigAndQueue: reading
// a form must not activate a paused clone or trigger an init/one-shot side effect.
// A form without a real run cannot borrow a running invocation's authority.
// Actual writes require the exact compiled implementation to declare pure/v1;
// native business owners need a separate transactional normalization hook.
func (p *PluginStore) NormalizeInstanceDefaults(ctx context.Context, snapshot PluginInstallation, expectedRevision int64, defaults map[string]json.RawMessage) (PluginStoredValue, error) {
	var out PluginStoredValue
	if expectedRevision < 0 || !pluginStorageID.MatchString(snapshot.InstanceID) || snapshot.Generation < 1 {
		return out, bad("INVALID_PLUGIN_VALUE", "插件默认值快照无效")
	}
	for key, value := range defaults {
		if !validPluginKey(key) || !json.Valid(value) {
			return out, bad("INVALID_PLUGIN_VALUE", "插件默认字段无效")
		}
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var plugin, version string
	var generation int64
	var installed bool
	if err = tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation,installed FROM plugin_installations WHERE instance_id=?`, snapshot.InstanceID).Scan(&plugin, &version, &generation, &installed); err != nil {
		return out, err
	}
	if !installed || plugin != snapshot.PluginID || version != snapshot.Version || generation != snapshot.Generation {
		return out, bad("PLUGIN_SNAPSHOT_STALE", "插件实例已重置、卸载或更换实现")
	}
	if _, err = p.instanceMetadata(ctx, tx, snapshot.InstanceID); err != nil {
		return out, err
	}
	var sealed []byte
	err = tx.QueryRowContext(ctx, `SELECT revision,sealed,updated_at FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, snapshot.InstanceID).Scan(&out.Revision, &sealed, &out.UpdatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	config := map[string]json.RawMessage{}
	if err == nil {
		out.Value, err = p.unseal("config", snapshot.InstanceID, "main", sealed)
		if err != nil {
			return out, err
		}
		if json.Unmarshal(out.Value, &config) != nil || config == nil {
			return out, bad("INVALID_PLUGIN_CONFIG", "插件配置不是有效对象")
		}
	} else {
		out.Value = json.RawMessage(`{}`)
	}
	if out.Revision != expectedRevision {
		return out, bad("PLUGIN_REVISION_CONFLICT", "插件配置已变化，请重新打开表单")
	}
	changed := false
	for key, value := range defaults {
		if _, exists := config[key]; !exists {
			config[key] = append(json.RawMessage(nil), value...)
			changed = true
		}
	}
	// Idempotent reads do not increment revision, obtain write capability, affect
	// active work, or create a synthetic management run.
	if !changed {
		return out, tx.Commit()
	}
	// Read-only no-ops remain available to every plugin, but mutable stored
	// manifests cannot opt an implementation into configuration propagation.
	manifest, _, known := builtin.Lookup(plugin)
	if !known || manifest.Version != version || manifest.ConfigDefaults != plugins.ConfigDefaultsPure {
		return out, bad("PLUGIN_DEFAULTS_UNSUPPORTED", "插件实现未声明支持纯默认值保存")
	}
	var active int
	// Even an opted-in implementation must not rewrite an immutable prepared
	// receipt's input hash/revision. Do not re-seal the receipt for a new snapshot.
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs r WHERE r.instance_id=? AND (r.state IN ('running','reconcile_pending') OR (r.state='pending' AND EXISTS(SELECT 1 FROM plugin_deferred_operations d WHERE d.run_id=r.id)))`, snapshot.InstanceID).Scan(&active); err != nil {
		return out, err
	}
	if active != 0 {
		return out, bad("PLUGIN_BUSY", "插件存在执行或待恢复的工作，默认值未保存，请稍后重新打开表单")
	}
	if err = p.requireSiteReferenceProtocolTx(ctx, tx, snapshot.InstanceID); err != nil {
		return out, err
	}
	raw, err := p.pluginSnapshotJSON(ctx, tx, snapshot.InstanceID, config, true)
	if err != nil {
		return out, err
	}
	previousConfig := append(json.RawMessage(nil), out.Value...)
	out, err = p.saveValueTx(ctx, tx, snapshot.InstanceID, "config", "main", expectedRevision, raw)
	if err != nil {
		return out, err
	}
	if err = p.propagateInstanceConfigTx(ctx, tx, snapshot.InstanceID, generation, expectedRevision, out.Revision, previousConfig, raw); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return PluginStoredValue{}, err
	}
	return out, nil
}
