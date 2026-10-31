package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

const (
	SharedPluginInit               = "plugin.manager.init_plugin"
	SharedPluginRemove             = "plugin.manager.remove_plugin"
	SharedPluginConfigDelete       = "plugin.manager.delete_plugin_config"
	SharedPluginDataDelete         = "plugin.manager.delete_plugin_data"
	SharedPluginState              = "plugin.manager.get_plugin_state"
	SharedLocalPlugins             = "plugin.manager.get_local_plugins"
	SharedSchedulerRemovePluginJob = "plugin.scheduler.remove_plugin_job"
	SharedSchedulerUpdatePluginJob = "plugin.scheduler.update_plugin_job"
)

// PluginManagerDeleteRequest defaults Force to false. Force only bypasses the
// source manager's class-presence check; it never supplies host authority or
// permission to delete files, installations, other kinds of data or all rows.
type PluginManagerDeleteRequest struct {
	PluginID string `json:"plugin_id"`
	Force    bool   `json:"force"`
}

// PluginSchedulerRemoveRequest preserves omitted, explicit null and empty
// string job_id. A nonempty string is the exact global service key, including
// a default service; neither SDK nor caller should prefix it with PluginID.
type PluginSchedulerRemoveRequest struct {
	PluginID string          `json:"plugin_id"`
	JobID    json.RawMessage `json:"job_id,omitempty"`
}

// SharedPluginScheduler is distinct from Manager.RefreshServices: these are
// the two source scheduler methods, not a self-only refresh shortcut. The
// current HostCaller and a separate declared capability remain mandatory.
type SharedPluginScheduler struct{ Host HostCaller }

func managerLifecycleJSON(raw json.RawMessage) bool {
	return len(raw) <= MaxPayloadBytes && utf8.Valid(raw) && json.Valid(raw)
}

func managerLifecycleCall(ctx context.Context, host HostCaller, method string, input any) (json.RawMessage, error) {
	if ctx == nil || host == nil {
		return nil, errors.New("plugin manager/scheduler requires an active context and host caller")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	// Same finite envelope as the existing ordinary resource host decoder.
	if len(raw) > 128<<10 {
		return nil, errors.New("plugin manager/scheduler resource input exceeds host limit")
	}
	raw, err = host(ctx, method, raw)
	if err != nil {
		return nil, err
	}
	if !managerLifecycleJSON(raw) {
		return nil, errors.New("invalid plugin manager/scheduler JSON reply")
	}
	// A HostCaller may reuse its buffer; the caller owns the returned value.
	return append(json.RawMessage(nil), raw...), nil
}

func managerLifecycleNone(raw json.RawMessage, err error) error {
	if err != nil {
		return err
	}
	if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("plugin manager/scheduler expected a None reply")
	}
	return nil
}

func managerLifecycleBoolean(raw json.RawMessage, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errors.New("plugin manager deletion expected a boolean reply")
	}
}

// InitPlugin supplies conf directly to the existing running object. It does
// not save configuration, install/reload an object or update its schedule.
// Conf must be an explicit standard JSON value; nil is not implicit {} or null.
func (m SharedPluginManager) InitPlugin(ctx context.Context, pluginID string, conf json.RawMessage) error {
	if !utf8.ValidString(pluginID) || !managerLifecycleJSON(conf) {
		return errors.New("invalid plugin manager init input")
	}
	return managerLifecycleNone(managerLifecycleCall(ctx, m.Host, SharedPluginInit, struct {
		PluginID string          `json:"plugin_id"`
		Conf     json.RawMessage `json:"conf"`
	}{pluginID, conf}))
}

// RemovePlugin removes an object from memory, not its package or stored data.
// An empty source ID means all loaded objects; it is never rewritten to self.
func (m SharedPluginManager) RemovePlugin(ctx context.Context, pluginID string) error {
	if !utf8.ValidString(pluginID) {
		return errors.New("invalid plugin manager ID")
	}
	return managerLifecycleNone(managerLifecycleCall(ctx, m.Host, SharedPluginRemove, struct {
		PluginID string `json:"plugin_id"`
	}{pluginID}))
}

func (m SharedPluginManager) DeletePluginConfig(ctx context.Context, input PluginManagerDeleteRequest) (bool, error) {
	if !utf8.ValidString(input.PluginID) {
		return false, errors.New("invalid plugin manager ID")
	}
	return managerLifecycleBoolean(managerLifecycleCall(ctx, m.Host, SharedPluginConfigDelete, input))
}

func (m SharedPluginManager) DeletePluginData(ctx context.Context, input PluginManagerDeleteRequest) (bool, error) {
	if !utf8.ValidString(input.PluginID) {
		return false, errors.New("invalid plugin manager ID")
	}
	return managerLifecycleBoolean(managerLifecycleCall(ctx, m.Host, SharedPluginDataDelete, input))
}

// GetPluginState preserves the actual source get_state result; the Python bool
// annotation is not coercion. Raw JSON retains large numbers, containers and
// string escapes without a float64/truthiness conversion. Non-JSON source
// objects and NaN/Infinity need an explicit host error, not invented JSON tags,
// null, strings or truthy booleans. This is not arbitrary Python serialization.
func (m SharedPluginManager) GetPluginState(ctx context.Context, pluginID string) (json.RawMessage, error) {
	if !utf8.ValidString(pluginID) {
		return nil, errors.New("invalid plugin manager ID")
	}
	return managerLifecycleCall(ctx, m.Host, SharedPluginState, struct {
		PluginID string `json:"plugin_id"`
	}{pluginID})
}

// GetLocalPlugins returns source-shaped object rows, not Go manifests or a
// fresh market/directory scan. Each row stays raw JSON to preserve value types.
func (m SharedPluginManager) GetLocalPlugins(ctx context.Context) ([]json.RawMessage, error) {
	raw, err := managerLifecycleCall(ctx, m.Host, SharedLocalPlugins, struct{}{})
	if err != nil {
		return nil, err
	}
	if bytes.TrimSpace(raw)[0] != '[' {
		return nil, errors.New("plugin manager expected a local-plugin array")
	}
	var rows []json.RawMessage
	if err = json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if bytes.TrimSpace(row)[0] != '{' {
			return nil, errors.New("plugin manager expected local-plugin object rows")
		}
	}
	return rows, nil
}

func (s SharedPluginScheduler) RemovePluginJob(ctx context.Context, input PluginSchedulerRemoveRequest) error {
	if !utf8.ValidString(input.PluginID) {
		return errors.New("invalid plugin scheduler ID")
	}
	if len(input.JobID) != 0 {
		if !managerLifecycleJSON(input.JobID) {
			return errors.New("invalid plugin scheduler job_id")
		}
		job := bytes.TrimSpace(input.JobID)
		if !bytes.Equal(job, []byte("null")) && job[0] != '"' {
			return errors.New("plugin scheduler job_id must be absent, null or a string")
		}
	}
	return managerLifecycleNone(managerLifecycleCall(ctx, s.Host, SharedSchedulerRemovePluginJob, input))
}

func (s SharedPluginScheduler) UpdatePluginJob(ctx context.Context, pluginID string) error {
	if !utf8.ValidString(pluginID) {
		return errors.New("invalid plugin scheduler ID")
	}
	return managerLifecycleNone(managerLifecycleCall(ctx, s.Host, SharedSchedulerUpdatePluginJob, struct {
		PluginID string `json:"plugin_id"`
	}{pluginID}))
}
