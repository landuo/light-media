package app

import (
	"context"
	"encoding/json"
	"slices"
)

// Keep raw values: Python setdefault only fills absent keys, including when an
// explicit value is null, false or an empty string. The API applies enabled
// filtering separately; anonymous auth assets use the unfiltered registry.
func (a *App) originalPluginAuthProviders(ctx context.Context) ([]map[string]json.RawMessage, error) {
	result := []map[string]json.RawMessage{}
	for _, object := range a.originalLoadedPluginSnapshot() {
		providers, err := a.originalObjectAuthProviders(ctx, object)
		if err != nil {
			return nil, err
		}
		result = append(result, providers...)
	}
	return result, nil
}

func (a *App) originalObjectAuthProviders(ctx context.Context, object *originalPluginInstance) ([]map[string]json.RawMessage, error) {
	result := []map[string]json.RawMessage{}
	enabled, err := object.getState(ctx)
	if err != nil {
		return nil, err
	} // Source get_state is outside its getter try.
	if !enabled || !slices.Contains(object.manifest.Methods, "get_auth_providers") {
		return result, nil
	}
	response, err := object.callOriginalMethod(ctx, "get_auth_providers", nil)
	if err != nil {
		a.logOriginalPluginContext(ctx, "ERROR", "auth_provider_declaration_failed", "读取插件认证提供方失败")
		return result, nil
	}
	var mode, dist string
	if slices.Contains(object.manifest.Methods, "get_render_mode") {
		mode, dist, _, err = object.originalViewRenderMode(ctx)
		if err != nil {
			return nil, err
		} // Source rendering errors propagate.
	}
	if !originalBaseJSONTruthy(response.Result) {
		return result, nil
	}
	var rows []json.RawMessage
	if json.Unmarshal(response.Result, &rows) != nil {
		// Python iterates dict keys/string characters, which are all skipped.
		var objectRows map[string]json.RawMessage
		var textRows string
		if json.Unmarshal(response.Result, &objectRows) == nil || json.Unmarshal(response.Result, &textRows) == nil {
			return result, nil
		}
		return nil, bad("PLUGIN_AUTH_PROVIDER_INVALID", "插件认证提供方必须为可迭代声明")
	}
	name := ""
	nameLoaded := false
	encode := func(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
	id := object.installation.InstanceID
	for _, row := range rows {
		var provider map[string]json.RawMessage
		if !originalBaseJSONTruthy(row) || json.Unmarshal(row, &provider) != nil || len(provider) == 0 {
			continue
		}
		// The fixed loop does not read plugin_name for skipped declarations.
		// Metadata storage is the Go equivalent of that class attribute.
		if !nameLoaded {
			name, err = object.originalDisplayName(ctx)
			if err != nil {
				return nil, err
			}
			nameLoaded = true
		}
		provider["type"], provider["plugin_id"] = encode("plugin"), encode(id)
		for key, value := range map[string]any{"id": "plugin:" + id, "name": name, "enabled": true} {
			if _, exists := provider[key]; !exists {
				provider[key] = encode(value)
			}
		}
		if mode == "vue" && dist != "" {
			frontend := object.manifest.Frontend
			if frontend == nil || frontend.Root != dist || object.frontendPackageHash() == "" {
				return nil, bad("PLUGIN_FRONTEND_UNAVAILABLE", "认证界面与已安装制品不一致")
			}
			if _, exists := provider["component"]; !exists {
				provider["component"] = encode("AuthPage")
			}
			provider["remote"] = encode(map[string]string{"id": id, "name": name, "url": "/plugins/" + id + "/frontend/" + object.frontendPackageHash() + "/assets/" + frontend.Entry})
		}
		result = append(result, provider)
	}
	return result, nil
}

func (a *App) originalAuthProviders(ctx context.Context) ([]map[string]json.RawMessage, error) {
	providers, err := a.originalPluginAuthProviders(ctx)
	if err != nil {
		return nil, err
	}
	result := []map[string]json.RawMessage{}
	for _, provider := range providers {
		if enabled, exists := provider["enabled"]; !exists || originalBaseJSONTruthy(enabled) {
			result = append(result, provider)
		}
	}
	// This application's core has no passkey store. Do not advertise a fake
	// system provider. Plugin entries retain original order and duplicate IDs.
	return result, nil
}
