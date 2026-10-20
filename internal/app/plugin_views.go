package app

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

// View RPCs are read-only: no queued business operation or config write is
// permitted. They share the same process/concurrency limit with actual runs.
func (a *App) pluginView(ctx context.Context, installation PluginInstallation, method, key string) (json.RawMessage, error) {
	manifest, _, exists := builtin.Lookup(installation.PluginID)
	if !exists || manifest.Version != installation.Version || !installation.Installed {
		return nil, bad("PLUGIN_IMPLEMENTATION_UNAVAILABLE", "此插件的Go实现尚未完成")
	}
	if (method != "page" && method != "dashboard") || (method == "page" && !manifest.HasPage) || (method == "dashboard" && !slices.Contains(manifest.Dashboards, key)) {
		return nil, &APIError{"NOT_FOUND", "插件未提供此页面或仪表板", 404}
	}
	budget := pluginViewBudget(installation.PluginID)
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	config, release, err := a.acquirePluginViewSnapshot(ctx, installation)
	if err != nil {
		return nil, err
	}
	defer release()
	input, _ := json.Marshal(map[string]string{"key": key})
	payload, _ := json.Marshal(plugins.Invocation{Action: method, Config: config.Value, Input: input})
	command, err := a.pluginExecutable(installation.PluginID)
	if err != nil {
		return nil, err
	}
	hostErrorCode := ""
	hostErrorMessage := "读取当前容器进程失败，请重试"
	result, err := (plugins.Runner{Timeout: budget}).Run(ctx, command, manifest, installation.InstanceID, method, payload, func(ctx context.Context, identity plugins.Identity, capability string, raw json.RawMessage) (json.RawMessage, error) {
		if identity.PluginID != installation.PluginID || identity.InstanceID != installation.InstanceID || identity.Version != installation.Version || !slices.Contains(manifest.Capabilities, capability) || (capability != "process.list" && capability != "site.statistics.read" && capability != "subscriptions.rules.history" && capability != "server.status.snapshot") {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "插件页面只能调用已授权的只读查询")
		}
		if err := a.pluginViewCurrent(ctx, installation, config.Revision); err != nil {
			return nil, err
		}
		if capability == "server.status.snapshot" {
			return a.pluginServerStatusSnapshot(ctx, installation, config.Revision, config.Value, method, raw)
		}
		if capability == "subscriptions.rules.history" {
			if installation.PluginID != "SubscribeGroup" || !pluginStrictJSON(raw, &struct{}{}) {
				return nil, bad("PLUGIN_CAPABILITY_DENIED", "订阅规则页面只能查询自己的历史")
			}
			a.pluginMu.Lock()
			defer a.pluginMu.Unlock()
			if err := a.pluginViewCurrent(ctx, installation, config.Revision); err != nil {
				return nil, err
			}
			history, err := a.pluginStore.Data(ctx, installation.InstanceID, "history")
			if missingPluginValue(err) {
				return json.RawMessage(`null`), nil
			}
			if err != nil {
				hostErrorCode = "PLUGIN_HISTORY_READ_FAILED"
				hostErrorMessage = "读取订阅规则历史失败，请重试"
				return nil, err
			}
			// Data() already verifies the complete encrypted KV value bound.
			// Transport handles fragmentation; never truncate history here.
			return history.Value, nil
		}
		if capability == "site.statistics.read" {
			if installation.PluginID != "SiteStatistic" || string(raw) != "{}" {
				return nil, bad("PLUGIN_CAPABILITY_DENIED", "站点统计只能查询宿主快照")
			}
			a.pluginMu.Lock()
			defer a.pluginMu.Unlock()
			if err := a.pluginViewCurrent(ctx, installation, config.Revision); err != nil {
				return nil, err
			}
			snapshot, err := a.siteStatisticsSnapshot(ctx)
			if err != nil {
				hostErrorCode = "SITE_STATISTICS_READ_FAILED"
				return nil, err
			}
			return json.Marshal(snapshot)
		}
		items, err := a.pluginProcessStats.snapshot(ctx)
		if err != nil {
			hostErrorCode = "PLUGIN_PROCESS_READ_FAILED"
			var api *APIError
			if errors.As(err, &api) {
				hostErrorCode = api.Code
			}
			return nil, err
		}
		return json.Marshal(items)
	})
	if err != nil {
		if hostErrorCode != "" {
			return nil, bad(hostErrorCode, hostErrorMessage)
		}
		var protocol *plugins.Error
		if errors.As(err, &protocol) {
			switch protocol.Code {
			case "message_limit", "frame_limit", "input_limit", "output_limit", "transport_chunk_limit":
				return nil, bad("PLUGIN_VIEW_TOO_LARGE", "插件页面超过允许大小或传输额度，未截断加载")
			}
		}
		return nil, bad("PLUGIN_VIEW_FAILED", "加载插件页面失败，请重试")
	}
	if err := a.pluginViewCurrent(ctx, installation, config.Revision); err != nil {
		return nil, err
	}
	if !json.Valid(result.Result) {
		return nil, bad("PLUGIN_VIEW_FAILED", "插件页面返回格式无效")
	}
	return result.Result, nil
}

func (a *App) acquirePluginViewSnapshot(ctx context.Context, installation PluginInstallation) (PluginStoredValue, func(), error) {
	for {
		select {
		case a.pluginProcess <- struct{}{}:
		case <-ctx.Done():
			return PluginStoredValue{}, nil, bad("PLUGIN_BUSY", "插件正在执行，请稍后重试")
		}
		release := func() { <-a.pluginProcess }
		config, err := a.pluginStore.Config(ctx, installation.InstanceID)
		if err != nil && !missingPluginValue(err) {
			release()
			return PluginStoredValue{}, nil, err
		}
		if len(config.Value) == 0 {
			config.Value = json.RawMessage(`{}`)
		}
		var pending int
		if installation.PluginID == "MPServerStatus" {
			err = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND generation=? AND config_revision=? AND action IN ('init','reload') AND state IN ('pending','running')`, installation.InstanceID, installation.Generation, config.Revision).Scan(&pending)
		}
		if err != nil && !missingPluginValue(err) {
			release()
			return PluginStoredValue{}, nil, err
		}
		if pending == 0 {
			return config, release, nil
		}
		// The initialization worker needs this same semaphore. Never wait for
		// its completion while retaining it, or publish a pre-init sample that
		// its later reset would erase.
		release()
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return PluginStoredValue{}, nil, bad("PLUGIN_BUSY", "插件配置仍在应用，请稍后重试")
		}
	}
}

func (a *App) pluginViewCurrent(ctx context.Context, installation PluginInstallation, revision int64) error {
	current, err := a.pluginStore.Installation(ctx, installation.InstanceID)
	if err != nil || !current.Installed || current.Generation != installation.Generation || current.Version != installation.Version {
		return bad("PLUGIN_SNAPSHOT_STALE", "插件已卸载、重置或更新，请重新打开")
	}
	config, err := a.pluginStore.Config(ctx, installation.InstanceID)
	if err != nil && !missingPluginValue(err) || config.Revision != revision {
		return bad("PLUGIN_REVISION_CONFLICT", "插件配置已变化，请重新打开")
	}
	return nil
}

func (a *App) pluginDashboardMeta(ctx context.Context) (any, error) {
	cards, err := a.pluginCards(ctx, false)
	if err != nil {
		return nil, err
	}
	items := []map[string]string{}
	for _, card := range cards {
		if !card.State {
			continue
		}
		for _, key := range card.Dashboards {
			// Source single-dashboard metadata uses an empty public key.
			// The SDK's registered permission key remains "default".
			if card.PluginID == "SystemProcess" && card.Version == "1.1" {
				key = ""
			}
			items = append(items, map[string]string{"id": card.InstanceID, "name": card.Name, "key": key})
		}
	}
	return items, nil
}
