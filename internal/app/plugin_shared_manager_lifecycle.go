package app

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"light-media/internal/plugins"
)

// Only the two implemented namespace mutations are admitted here. The other
// six S10 SDK method names are not host capabilities until their own slices
// implement the real lifecycle/scheduler contract.
func originalSharedManagerMutationCapability(method string) bool {
	return method == plugins.SharedPluginConfigDelete || method == plugins.SharedPluginDataDelete
}

func (a *App) originalSharedManagerMutation(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if !originalSharedManagerMutationCapability(method) || scope.validate == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "插件管理数据操作未获授权")
	}
	var input struct {
		PluginID *string `json:"plugin_id"`
		Force    bool    `json:"force"`
	}
	if !utf8.Valid(raw) || !sharedHistoryScalarEscapes(raw) {
		return nil, bad("INVALID_PLUGIN_VALUE", "插件管理参数必须是有效Unicode字符串")
	}
	if err := originalResourceDecode(raw, &input); err != nil {
		return nil, err
	}
	if input.PluginID == nil || !utf8.ValidString(*input.PluginID) || len(*input.PluginID) > 4096 || strings.ContainsRune(*input.PluginID, 0) {
		return nil, bad("INVALID_PLUGIN_VALUE", "插件管理数据命名空间无效")
	}
	target := *input.PluginID
	// Preserve the existing lock order. No worker, event, stop or schedule call
	// occurs while these cache/storage locks are held.
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !input.Force {
		if _, present := a.pluginOriginalClasses.Load(target); !present {
			return json.RawMessage(`false`), nil
		}
	}
	// Force bypasses only the class check above. The resource dispatcher already
	// binds identity/capability/current callback; neither target nor Force can
	// select arbitrary SystemConfig keys, filesystem paths or SQL tables.
	if method == plugins.SharedPluginDataDelete {
		if err := a.pluginStore.deleteOriginalPluginData(ctx, target); err != nil {
			return nil, err
		}
		return json.RawMessage(`true`), nil
	}
	a.pluginOriginalBase.mu.Lock()
	defer a.pluginOriginalBase.mu.Unlock()
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := a.originalSharedManagerDeleteConfigLocked(ctx, target); err != nil {
		return nil, err
	}
	return json.RawMessage(`true`), nil
}

// Caller holds pluginMu then pluginOriginalBase.mu and has admitted the call.
// SystemConfigOper.delete pops its hot value before the first SQL operation.
// Keep a cached miss with the observed row fingerprint, rather than deleting
// the Go map entry: a failed SQL delete must not lazily revive that old row.
func (a *App) originalSharedManagerDeleteConfigLocked(ctx context.Context, target string) error {
	state := &a.pluginOriginalBase
	if state.config == nil {
		state.config = make(map[string]originalBaseConfigValue)
	}
	cached := state.config[target]
	cached.value, cached.present = json.RawMessage(`null`), false
	state.config[target] = cached

	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	observed, _, err := readOriginalBaseConfigRow(ctx, tx, target)
	if err != nil {
		return err // Retain the last known observation on a read outage.
	}
	cached.observed = observed
	state.config[target] = cached
	if observed.present {
		if _, err = tx.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, target); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	cached.observed = originalBaseConfigFingerprint{}
	state.config[target] = cached
	return nil
}
