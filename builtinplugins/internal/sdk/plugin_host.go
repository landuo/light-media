package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

const PluginManagerReadCapability = "plugin.manager.read"
const PluginManagerControlCapability = "plugin.manager.control"

type PluginHost struct{ Host HostCaller }
type PluginManagerRequest struct {
	InstanceID   string          `json:"instance_id,omitempty"`
	Action       string          `json:"action,omitempty"`
	View         string          `json:"view,omitempty"`
	Config       json.RawMessage `json:"config,omitempty"`
	Force        bool            `json:"force,omitempty"`
	JobID        string          `json:"job_id,omitempty"`
	IncludeState bool            `json:"include_state,omitempty"`
}
type PluginHostState struct {
	InstanceID string `json:"instance_id"`
	PluginID   string `json:"plugin_id"`
	Version    string `json:"version"`
	Installed  bool   `json:"installed"`
	Loaded     bool   `json:"loaded"`
	Enabled    *bool  `json:"enabled,omitempty"`
}
type PluginControlResult struct {
	Accepted  bool            `json:"accepted"`
	Completed bool            `json:"completed"`
	Result    json.RawMessage `json:"result,omitempty"`
}

func (m PluginHost) DeleteConfig(ctx context.Context, instance string, force bool) (bool, error) {
	result, err := m.ControlRequest(ctx, PluginManagerRequest{InstanceID: instance, Action: "config.delete", Force: force})
	if err != nil {
		return false, err
	}
	var deleted bool
	if err = json.Unmarshal(result.Result, &deleted); err != nil {
		return false, err
	}
	return deleted, nil
}

func (m PluginHost) DeleteData(ctx context.Context, instance string, force bool) (bool, error) {
	result, err := m.ControlRequest(ctx, PluginManagerRequest{InstanceID: instance, Action: "data.delete", Force: force})
	if err != nil {
		return false, err
	}
	var deleted bool
	if err = json.Unmarshal(result.Result, &deleted); err != nil {
		return false, err
	}
	return deleted, nil
}

func (m PluginHost) States(ctx context.Context, instance string) ([]PluginHostState, error) {
	var value []PluginHostState
	err := hostServiceCall(ctx, m.Host, PluginManagerReadCapability, PluginManagerRequest{InstanceID: instance, IncludeState: true}, &value)
	return value, err
}

func (m PluginHost) State(ctx context.Context, instance string) (bool, error) {
	values, err := m.Read(ctx, PluginManagerRequest{InstanceID: instance, View: "running", IncludeState: true})
	if err != nil {
		return false, err
	}
	if len(values) == 0 {
		return false, nil
	}
	if values[0].Enabled == nil {
		return false, errors.New("missing explicit plugin state")
	}
	return *values[0].Enabled, nil
}
func (m PluginHost) Control(ctx context.Context, instance, action string) (PluginControlResult, error) {
	return m.ControlRequest(ctx, PluginManagerRequest{InstanceID: instance, Action: action})
}

func (m PluginHost) Read(ctx context.Context, request PluginManagerRequest) ([]PluginHostState, error) {
	var value []PluginHostState
	err := hostServiceCall(ctx, m.Host, PluginManagerReadCapability, request, &value)
	return value, err
}

// Local mirrors the source manager's get_local_plugins metadata traversal.
// It includes imported but uninstalled classes and performs their ordinary
// state getters before sorting; it does not consult any plugin market.
// Raw source-shaped rows preserve metadata independently of runtime state DTOs.
func (m PluginHost) Local(ctx context.Context) ([]json.RawMessage, error) {
	var value []json.RawMessage
	err := hostServiceCall(ctx, m.Host, PluginManagerReadCapability, PluginManagerRequest{View: "local"}, &value)
	return value, err
}

func (m PluginHost) ControlRequest(ctx context.Context, request PluginManagerRequest) (PluginControlResult, error) {
	var value PluginControlResult
	err := hostServiceCall(ctx, m.Host, PluginManagerControlCapability, request, &value)
	return value, err
}
