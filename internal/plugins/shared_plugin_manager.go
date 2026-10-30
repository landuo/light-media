package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

const (
	SharedPluginIDs        = "plugin.manager.get_plugin_ids"
	SharedRunningPluginIDs = "plugin.manager.get_running_plugin_ids"
	SharedPluginConfig     = "plugin.manager.get_plugin_config"
)

// SharedPluginManager reads the actual host registry and manager configuration.
// It carries no lifecycle, arbitrary-method, global-settings or persistent
// callback authority. Each operation requires its own declared capability and
// the current invocation's HostCaller. Class and running IDs include clones.
type SharedPluginManager struct{ Host HostCaller }

func (m SharedPluginManager) call(ctx context.Context, method string, input any) (json.RawMessage, error) {
	if m.Host == nil {
		return nil, errors.New("plugin manager requires an active host caller")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return m.Host(ctx, method, raw)
}

func (m SharedPluginManager) GetPluginIDs(ctx context.Context) ([]string, error) {
	raw, err := m.call(ctx, SharedPluginIDs, struct{}{})
	if err != nil {
		return nil, err
	}
	var ids []string
	err = json.Unmarshal(raw, &ids)
	return ids, err
}

func (m SharedPluginManager) GetRunningPluginIDs(ctx context.Context) ([]string, error) {
	raw, err := m.call(ctx, SharedRunningPluginIDs, struct{}{})
	if err != nil {
		return nil, err
	}
	var ids []string
	err = json.Unmarshal(raw, &ids)
	return ids, err
}

// GetPluginConfig is not Base.GetConfig: an absent class or falsey stored value
// returns {}, and empty keys are removed. It reads saved configuration, never
// reinitializes a running object. Normal IDs are exact, case-sensitive strings.
// JSON scalar non-string IDs cannot identify a class and return {}; containers
// fail like unhashable Python dict keys. The host grants ordinary plugin
// namespace access only; this is not a general settings/credential reader.
func (m SharedPluginManager) GetPluginConfig(ctx context.Context, pluginID any) (json.RawMessage, error) {
	return m.call(ctx, SharedPluginConfig, struct {
		PluginID any `json:"plugin_id"`
	}{pluginID})
}
