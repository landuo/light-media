package app

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"

	"light-media/internal/plugins"
)

func originalSharedManagerQueryCapability(method string) bool {
	switch method {
	case plugins.SharedPluginIDs, plugins.SharedRunningPluginIDs, plugins.SharedPluginConfig:
		return true
	}
	return false
}

func (a *App) originalSharedManagerQuery(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if !originalSharedManagerQueryCapability(method) || scope.validate == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "插件管理查询未授权")
	}
	var target string
	if method == plugins.SharedPluginConfig {
		var input struct {
			PluginID json.RawMessage `json:"plugin_id"`
		}
		if err := originalResourceDecode(raw, &input); err != nil {
			return nil, err
		}
		value := bytes.TrimSpace(input.PluginID)
		if len(value) == 0 {
			return nil, bad("INVALID_PLUGIN_VALUE", "插件管理查询缺少 plugin_id")
		}
		if value[0] == '[' || value[0] == '{' {
			return nil, bad("INVALID_PLUGIN_VALUE", "插件标识不是可散列值")
		}
		if value[0] == '"' {
			if err := json.Unmarshal(value, &target); err != nil {
				return nil, err
			}
		}
	} else {
		if err := originalResourceDecode(raw, &struct{}{}); err != nil {
			return nil, err
		}
	}
	// Revalidate after waiting for ordinary config access, without taking a
	// method lock or calling any worker. The exact calling scope still controls
	// admission, including initializer and detached cleanup phases.
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	if method == plugins.SharedPluginConfig {
		return a.originalManagerConfiguration(ctx, target)
	}
	a.pluginOriginalRunningMu.Lock()
	defer a.pluginOriginalRunningMu.Unlock()
	var ids []string
	if method == plugins.SharedPluginIDs {
		ids = slices.Clone(a.pluginOriginalClassOrder)
	} else {
		ids = slices.Clone(a.pluginOriginalRunningOrder)
	}
	if ids == nil {
		ids = []string{}
	}
	return json.Marshal(ids)
}
