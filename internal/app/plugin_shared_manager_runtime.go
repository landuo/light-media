package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"unicode/utf8"

	"light-media/internal/plugins"
)

func originalSharedManagerRuntimeCapability(method string) bool {
	return method == plugins.SharedPluginInit || method == plugins.SharedPluginState || method == plugins.SharedLocalPlugins
}

// Only an actual self-targeted nested call needs this negotiation check.
// Idle serial targets in another worker are not blanket-rejected. Ordinary
// concurrent SDK targets already have no methodMu/process-slot serialization.
func originalManagerNestedTarget(scope originalBaseCallScope, target *originalPluginInstance) error {
	if scope.object != target {
		return nil
	}
	target.sessionMu.Lock()
	concurrent := target.session != nil && target.session.SupportsConcurrent()
	target.sessionMu.Unlock()
	if !concurrent {
		return bad("PLUGIN_CONCURRENT_REQUIRED", "同步自身管理调用需要实际协商的并发worker")
	}
	return nil
}

func (p *originalPluginInstance) originalManagerRawState(ctx context.Context) (json.RawMessage, error) {
	unlock := p.lockOriginalMethod()
	defer unlock()
	var raw json.RawMessage
	if err := p.readSession(ctx, "get_state", &raw); err != nil {
		return nil, err
	}
	if len(raw) > plugins.MaxPayloadBytes || !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, bad("INVALID_PLUGIN_VALUE", "插件状态不是可传输的标准JSON值")
	}
	return append(json.RawMessage(nil), raw...), nil
}

func (a *App) originalSharedManagerRuntime(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if !originalSharedManagerRuntimeCapability(method) || scope.validate == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "插件运行管理方法未获授权")
	}
	if method == plugins.SharedLocalPlugins {
		if err := originalResourceDecode(raw, &struct{}{}); err != nil {
			return nil, err
		}
		return a.originalManagerLocalDescriptors(ctx, scope)
	}
	var input struct {
		PluginID *string         `json:"plugin_id"`
		Conf     json.RawMessage `json:"conf,omitempty"`
	}
	if !utf8.Valid(raw) {
		return nil, bad("INVALID_PLUGIN_VALUE", "插件管理参数必须是有效Unicode JSON")
	}
	if err := originalResourceDecode(raw, &input); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if !sharedHistoryScalarEscapes(fields["plugin_id"]) {
		return nil, bad("INVALID_PLUGIN_VALUE", "插件标识必须是有效Unicode字符串")
	}
	if input.PluginID == nil || method == plugins.SharedPluginInit && !json.Valid(input.Conf) || method == plugins.SharedPluginState && len(input.Conf) != 0 {
		return nil, bad("INVALID_PLUGIN_VALUE", "插件运行管理参数无效")
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	object, operation, err := a.pinOriginalRunningObject(ctx, *input.PluginID)
	if err != nil {
		return nil, err
	}
	if object == nil {
		if method == plugins.SharedPluginState {
			return json.RawMessage(`false`), nil
		}
		return json.RawMessage(`null`), nil
	}
	defer object.releaseOriginalRetainedOwner(operation)
	ctx, err = object.originalRetainedParentContext(ctx, operation)
	if err != nil {
		return nil, err
	}
	if err := originalManagerNestedTarget(scope, object); err != nil {
		return nil, err
	}
	if method == plugins.SharedPluginInit {
		if err := object.initWithConfiguration(ctx, &input.Conf); err != nil {
			return nil, err
		}
	}
	// One actual getter, including the post-init event decision. Do not call
	// getState/updateOriginalPluginEventState again and lose raw JSON values.
	state, err := object.originalManagerRawState(ctx)
	if err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	if method == plugins.SharedPluginInit {
		object.configMu.Lock()
		object.eventsEnabled = originalBaseJSONTruthy(state)
		object.configMu.Unlock()
		return json.RawMessage(`null`), nil
	}
	return state, nil
}

// The source schema defaults are part of the API. Do not marshal pluginCard,
// which exposes host manifest/instance fields and omits source null/defaults.
func originalManagerDescriptorDefaults() map[string]json.RawMessage {
	row := make(map[string]json.RawMessage)
	for _, key := range []string{"id", "plugin_name", "plugin_desc", "plugin_icon", "plugin_version", "plugin_label", "plugin_author", "author_url", "plugin_config_prefix", "system_version_message", "system_version", "repo_url", "plugin_public_key"} {
		row[key] = json.RawMessage(`null`)
	}
	for _, key := range []string{"plugin_order", "auth_level", "install_count", "add_time"} {
		row[key] = json.RawMessage(`0`)
	}
	for _, key := range []string{"installed", "state", "has_page", "has_update", "release"} {
		row[key] = json.RawMessage(`false`)
	}
	row["system_version_compatible"], row["is_local"], row["history"] = json.RawMessage(`true`), json.RawMessage(`false`), json.RawMessage(`{}`)
	return row
}

func (a *App) originalManagerLocalDescriptors(ctx context.Context, scope originalBaseCallScope) (json.RawMessage, error) {
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	a.pluginOriginalRunningMu.Lock()
	ids := slices.Clone(a.pluginOriginalClassOrder)
	a.pluginOriginalRunningMu.Unlock()
	type descriptor struct {
		order int
		row   map[string]json.RawMessage
	}
	rows := []descriptor{}
	for _, id := range ids {
		if err := scope.validate(ctx); err != nil {
			return nil, err
		}
		value, exists := a.pluginOriginalClasses.Load(id)
		if !exists {
			continue
		}
		class := value.(originalPluginClass)
		var manifest plugins.Manifest
		var attributes map[string]json.RawMessage
		if err := json.Unmarshal(class.installation.Manifest, &manifest); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(class.installation.Manifest, &attributes); err != nil {
			return nil, err
		}
		row := originalManagerDescriptorDefaults()
		row["is_local"] = json.RawMessage(`true`)
		row["id"], _ = json.Marshal(id)
		for target, source := range map[string]string{"plugin_name": "name", "plugin_desc": "description", "plugin_icon": "icon", "plugin_version": "version", "plugin_author": "author", "author_url": "author_url", "plugin_order": "plugin_order", "auth_level": "auth_level", "plugin_public_key": "plugin_public_key"} {
			if value, present := attributes[source]; present {
				row[target] = copyOriginalBaseJSON(value)
			}
		}
		// Metadata retains excluded official-level fields as data only; no level
		// gate is introduced. The existing authentication-provider path is untouched.
		in, err := a.pluginStore.Installation(ctx, id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			row["installed"], _ = json.Marshal(in.Installed)
			if class.isClone {
				metadata, err := a.pluginStore.instanceMetadata(ctx, a.Store.DB, id)
				if err != nil {
					return nil, err
				}
				for key, value := range map[string]string{"plugin_name": metadata.Name, "plugin_desc": metadata.Description, "plugin_icon": metadata.Icon, "plugin_version": metadata.DisplayVersion} {
					row[key], _ = json.Marshal(value)
				}
			}
		}
		row["has_page"], _ = json.Marshal(slices.Contains(manifest.Methods, "get_page"))
		object, operation, pinErr := a.pinOriginalRunningObject(ctx, id)
		if pinErr != nil {
			return nil, pinErr
		}
		if object != nil {
			if err := originalManagerNestedTarget(scope, object); err != nil {
				object.releaseOriginalRetainedOwner(operation)
				return nil, err
			}
			readCtx, err := object.originalRetainedParentContext(ctx, operation)
			var state json.RawMessage
			if err == nil {
				state, err = object.originalManagerRawState(readCtx)
			}
			object.releaseOriginalRetainedOwner(operation)
			if err == nil {
				row["state"] = state
			} else {
				a.log("ERROR", "plugins", "state_read_failed", "获取插件状态失败", "", "")
			}
		}
		if err := scope.validate(ctx); err != nil {
			return nil, err
		}
		rows = append(rows, descriptor{manifest.PluginOrder, row})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].order < rows[j].order })
	out := make([]map[string]json.RawMessage, len(rows))
	for i, row := range rows {
		out[i] = row.row
	}
	return json.Marshal(out)
}
