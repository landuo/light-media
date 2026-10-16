package app

import (
	"context"
	"encoding/json"
)

// The private adapters take pluginMu themselves and fence persisted run/batch
// state. Route before pluginHostCall's general lock to avoid recursive locking.
func (a *App) pluginSubscribeGroupHost(ctx context.Context, installation PluginInstallation, run *PluginRun, method string, raw json.RawMessage) (json.RawMessage, error) {
	if installation.PluginID != "SubscribeGroup" || run == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "订阅规则写入必须属于当前执行")
	}
	switch method {
	case "subscriptions.rules.configure":
		if !pluginStrictJSON(raw, &struct{}{}) {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "订阅规则初始化不接受外部配置或补丁")
		}
		return a.configureSubscribeGroup(ctx, run)
	case "subscriptions.rules.apply":
		var input struct {
			BatchID string `json:"batch_id"`
		}
		if !pluginStrictJSON(raw, &input) || input.BatchID == "" {
			return nil, bad("PLUGIN_BATCH_SCOPE_DENIED", "订阅规则只能处理当前投递批次")
		}
		var handler string
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT handler_key FROM plugin_event_batches WHERE id=? AND run_id=? AND instance_id=? AND generation=? AND config_revision=?`, input.BatchID, run.ID, run.InstanceID, run.Generation, run.ConfigRevision).Scan(&handler); err != nil {
			return nil, bad("PLUGIN_BATCH_SCOPE_DENIED", "订阅规则批次不属于当前执行")
		}
		switch handler {
		case "added":
			return a.applySubscribeGroupAdded(ctx, *run, input.BatchID)
		case "downloaded":
			return a.applySubscribeGroupDownload(ctx, *run, input.BatchID)
		default:
			return nil, bad("PLUGIN_BATCH_SCOPE_DENIED", "订阅规则未注册此批次处理器")
		}
	default:
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "订阅规则未获授权执行此操作")
	}
}
