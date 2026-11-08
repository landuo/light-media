package app

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"time"

	"light-media/internal/plugins"
	seed "light-media/internal/plugins/cleaninvalidseed"
)

type originalCleanSeedActionKey struct{}

type originalCleanSeedCall struct {
	mu        sync.Mutex
	action    string
	applied   json.RawMessage
	init      *originalInitializationCall
	config    map[string]any
	downloads originalCleanSeedDownloads
	files     originalCleanSeedFileScan
}

func withOriginalCleanSeedCommand(ctx context.Context, input json.RawMessage) context.Context {
	var event plugins.MutableEvent
	var payload struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(input, &event) != nil || event.Type != "plugin.action" || json.Unmarshal(event.Data, &payload) != nil {
		return ctx
	}
	switch payload.Action {
	case "detect_invalid_torrents", "delete_invalid_torrents", "detect_invalid_files", "delete_invalid_files":
		return context.WithValue(ctx, originalCleanSeedActionKey{}, payload.Action)
	}
	return ctx
}

func (p *originalPluginInstance) originalCleanSeedCallFor(ctx context.Context, phase string) *originalCleanSeedCall {
	if p.installation.PluginID != "CleanInvalidSeed" {
		return nil
	}
	var action string
	switch phase {
	case "handle_commands":
		action, _ = ctx.Value(originalCleanSeedActionKey{}).(string)
	case "clean_invalid_seed":
		service, _ := ctx.Value(originalServiceCallKey{}).(*originalServiceCall)
		if service != nil && service.job != nil && service.job.object == p &&
			service.job.definition.Handler == phase && p.originalPublicServiceScopeAllowed(ctx) {
			action = "clean_invalid_seed"
		}
	case "private_once":
		if p.originalPrivateSchedulerScopeAllowed(ctx) {
			action = "clean_invalid_seed"
		}
	}
	if action == "" {
		return nil
	}
	p.configMu.RLock()
	raw := copyOriginalBaseJSON(p.appliedConfig)
	p.configMu.RUnlock()
	initialization := originalLiveInitialization(ctx, p)
	if phase == "private_once" && initialization != nil && len(initialization.config) != 0 {
		raw = copyOriginalBaseJSON(initialization.config)
	}
	value, err := decodeSourceJSON(raw)
	if err != nil || sourceMap(value) == nil {
		return nil
	}
	config := sourceMap(value)
	if phase == "private_once" && !seed.Truth(config["onlyonce"]) || phase == "clean_invalid_seed" && !seed.Truth(config["enabled"]) {
		return nil
	}
	return &originalCleanSeedCall{action: action, applied: raw, config: config, init: initialization}
}

func originalCleanSeedCapability(method string) bool {
	switch method {
	case "clean_seed.tasks", "clean_seed.trackers", "clean_seed.control", "clean_seed.files", "clean_seed.remove":
		return true
	}
	return false
}

func (a *App) originalCleanSeedResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	call := scope.cleanSeed
	if call == nil || scope.object == nil || scope.object.app != a || scope.installation.PluginID != "CleanInvalidSeed" {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "做种清理须绑定实际命令或调度")
	}
	call.mu.Lock()
	defer call.mu.Unlock()
	if err := originalCleanSeedValidate(ctx, scope); err != nil {
		return nil, err
	}
	switch method {
	case "clean_seed.tasks":
		return a.originalCleanSeedTasks(ctx, scope, raw)
	case "clean_seed.trackers":
		return a.originalCleanSeedTrackers(ctx, scope, raw)
	case "clean_seed.control":
		return a.originalCleanSeedControl(ctx, scope, raw)
	case "clean_seed.files":
		return a.originalCleanSeedFiles(ctx, scope, raw)
	case "clean_seed.remove":
		return a.originalCleanSeedRemove(ctx, scope, raw)
	}
	return nil, bad("PLUGIN_CAPABILITY_DENIED", "未知做种清理资源")
}

func originalCleanSeedValidate(ctx context.Context, scope originalBaseCallScope) error {
	if err := scope.validate(ctx); err != nil {
		return err
	}
	if call := scope.cleanSeed.init; call != nil && call.active.Load() && bytes.Equal(scope.cleanSeed.applied, call.config) {
		return nil
	}
	scope.object.configMu.RLock()
	defer scope.object.configMu.RUnlock()
	if !bytes.Equal(scope.cleanSeed.applied, scope.object.appliedConfig) {
		return bad("PLUGIN_SNAPSHOT_STALE", "做种清理期间插件配置已变化")
	}
	return nil
}

func (c *originalCleanSeedCall) filesAllowed(write bool) bool {
	switch c.action {
	case "detect_invalid_files":
		return !write
	case "delete_invalid_files":
		return true
	case "clean_invalid_seed":
		return seed.Truth(c.config["detect_invalid_files"]) && (!write || seed.Truth(c.config["delete_invalid_files"]))
	default:
		return false
	}
}

// Seal the intent before an external or irreversible effect. Unknown results
// retain the started row, so a later schedule cannot repeat the same effect.
func (a *App) executeOriginalCleanSeedEffect(ctx context.Context, scope originalBaseCallScope, key string, effect func() (bool, error)) (bool, error) {
	const kind = "plugin_clean_seed_effect"
	key = tokenHash(scope.installation.InstanceID + "\x00" + key)
	if err := originalCleanSeedValidate(ctx, scope); err != nil {
		return false, err
	}
	// Store.Put writes the confirmed JSON value as bytes.
	result, err := a.Store.DB.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at)
		VALUES(?,?,'"started"',?) ON CONFLICT(kind,id) DO UPDATE
		SET payload=excluded.payload,updated_at=excluded.updated_at WHERE CAST(records.payload AS TEXT)='"confirmed"'`, kind, key, now())
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if changed == 0 {
		return false, bad("CLEAN_SEED_EFFECT_UNCERTAIN", "上一轮清理结果需核对，已停止重复操作")
	}
	applied, err := effect()
	if err != nil {
		return false, err
	}
	settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if applied {
		err = a.Store.Put(settle, kind, key, "confirmed")
	} else {
		_, err = a.Store.DB.ExecContext(settle, `DELETE FROM records WHERE kind=? AND id=?`, kind, key)
	}
	if err != nil {
		return false, bad("CLEAN_SEED_EFFECT_UNCERTAIN", "清理结果未能保存，需核对实际状态")
	}
	return applied, nil
}
