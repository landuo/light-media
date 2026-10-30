package app

import (
	"context"
	"crypto/sha256"
)

// SystemConfigOper constructs its process cache before any plugin is loaded.
// Prefetch only ordinary plugin configuration, never host Settings or credentials
// from other namespaces. A mid-start SQL outage must not make a later imported
// class lose configuration that was available at construction time.
func (a *App) prefetchOriginalPluginConfig(ctx context.Context) error {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	a.pluginOriginalBase.mu.Lock()
	defer a.pluginOriginalBase.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	rows, err := a.pluginStore.store.DB.QueryContext(ctx, `SELECT instance_id,revision,sealed FROM plugin_values WHERE kind='config' AND item_key='main'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	values := make(map[string]originalBaseConfigValue)
	for rows.Next() {
		var target string
		var revision int64
		var sealed []byte
		if err := rows.Scan(&target, &revision, &sealed); err != nil {
			return err
		}
		raw, err := a.pluginStore.unseal("config", target, "main", sealed)
		if err != nil {
			return err
		}
		values[target] = originalBaseConfigValue{
			value: copyOriginalBaseJSON(raw), present: true,
			observed: originalBaseConfigFingerprint{present: true, revision: revision, sealedDigest: sha256.Sum256(sealed)},
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// A failed/cancelled read or invalid seal never publishes a partial snapshot.
	a.pluginOriginalBase.config = values
	a.pluginOriginalBase.snapshotReady = true
	return nil
}
