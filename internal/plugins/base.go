package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

const (
	BaseGetConfig           = "plugin.base.get_config"
	BaseUpdateConfig        = "plugin.base.update_config"
	BaseGetDataPath         = "plugin.base.get_data_path"
	BaseSaveData            = "plugin.base.save_data"
	BaseGetData             = "plugin.base.get_data"
	BaseDelData             = "plugin.base.del_data"
	BaseGetInstanceMetadata = "plugin.base.get_instance_metadata"
)

// BaseMethods is the finite method vocabulary, not an authorization grant.
// Only the host's recognized contract may grant any of these operations.
func BaseMethods() []string {
	return []string{BaseGetConfig, BaseUpdateConfig, BaseGetDataPath, BaseSaveData, BaseGetData, BaseDelData, BaseGetInstanceMetadata}
}
func IsBaseMethod(method string) bool {
	switch method {
	case BaseGetConfig, BaseUpdateConfig, BaseGetDataPath, BaseSaveData, BaseGetData, BaseDelData, BaseGetInstanceMetadata:
		return true
	}
	return false
}

// Base is scoped by the current invocation's HostCaller. It must not be retained
// for background use. Omitting a target resolves the real instance on the host,
// so a plugin requires no clone-specific code or process-global namespace.
type Base struct{ Host HostCaller }

// InstanceMetadata describes the current calling instance. DisplayVersion may
// be customized for a clone; PluginID and Version remain the authenticated
// executable identity and must never be replaced with presentation values.
type InstanceMetadata struct {
	InstanceID       string `json:"instance_id"`
	SourceInstanceID string `json:"source_instance_id"`
	PluginID         string `json:"plugin_id"`
	Version          string `json:"plugin_version"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	DisplayVersion   string `json:"display_version"`
	Icon             string `json:"icon"`
}

// GetInstanceMetadata is read-only and always resolves the actual caller.
// Unlike config/data helpers, it accepts no target namespace override.
func (b Base) GetInstanceMetadata(ctx context.Context) (InstanceMetadata, error) {
	var result InstanceMetadata
	raw, err := b.call(ctx, BaseGetInstanceMetadata, map[string]any{}, nil)
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}

// GetName is the Go equivalent of _PluginBase.get_name for generated clones.
func (b Base) GetName(ctx context.Context) (string, error) {
	metadata, err := b.GetInstanceMetadata(ctx)
	return metadata.Name, err
}

type BaseDataRow struct {
	ID       int64           `json:"id"`
	PluginID string          `json:"plugin_id"`
	Key      string          `json:"key"`
	Value    json.RawMessage `json:"value"`
}

func (b Base) call(ctx context.Context, method string, arguments map[string]any, target []string) (json.RawMessage, error) {
	if b.Host == nil {
		return nil, errors.New("plugin base requires an active host caller")
	}
	if len(target) > 1 {
		return nil, errors.New("plugin base accepts at most one plugin_id")
	}
	if len(target) == 1 {
		arguments["plugin_id"] = target[0]
	}
	raw, err := json.Marshal(arguments)
	if err != nil {
		return nil, err
	}
	return b.Host(ctx, method, raw)
}

func (b Base) GetConfig(ctx context.Context, target ...string) (json.RawMessage, error) {
	return b.call(ctx, BaseGetConfig, map[string]any{}, target)
}

// UpdateConfig returns nil for Python None/no change, or a pointer to the
// original bool result. It does not initialize or reload the plugin.
func (b Base) UpdateConfig(ctx context.Context, config any, target ...string) (*bool, error) {
	raw, err := b.call(ctx, BaseUpdateConfig, map[string]any{"config": config}, target)
	if err != nil {
		return nil, err
	}
	var result *bool
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}
func (b Base) GetDataPath(ctx context.Context, target ...string) (string, error) {
	raw, err := b.call(ctx, BaseGetDataPath, map[string]any{}, target)
	if err != nil {
		return "", err
	}
	var path string
	if err = json.Unmarshal(raw, &path); err != nil {
		return "", err
	}
	return path, nil
}
func (b Base) SaveData(ctx context.Context, key string, value any, target ...string) error {
	_, err := b.call(ctx, BaseSaveData, map[string]any{"key": key, "value": value}, target)
	return err
}

// A nil or empty key selects the original all-data row-list behavior. A keyed
// miss is JSON null. The raw result preserves false, zero and null distinctly.
func (b Base) GetData(ctx context.Context, key *string, target ...string) (json.RawMessage, error) {
	return b.call(ctx, BaseGetData, map[string]any{"key": key}, target)
}

// A nil or empty key deletes all data for the resolved plugin namespace, not
// its configuration, files, package, or any other plugin's namespace.
func (b Base) DelData(ctx context.Context, key *string, target ...string) error {
	_, err := b.call(ctx, BaseDelData, map[string]any{"key": key}, target)
	return err
}
