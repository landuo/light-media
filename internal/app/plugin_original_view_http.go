package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
)

// This is composition only; the existing authenticated HTTP entry supplies the
// exact loaded object. It neither discovers nor starts a plugin.
func (a *App) originalExternalViewHTTP(ctx context.Context, object *originalPluginInstance, kind, key string, userAgent *string) (any, error) {
	if object == nil || !object.external || object.app != a || !originalObjectStillLoaded(object) {
		return nil, &APIError{"PLUGIN_NOT_LOADED", "插件不存在或未加载", 404}
	}
	// Original get_render_mode is outside the Form/Page method catch.
	render, _, err := object.originalViewRead(ctx, "get_render_mode", nil)
	if err != nil {
		return nil, err
	}
	var modes []json.RawMessage
	var mode, dist string
	if json.Unmarshal(render, &modes) != nil || len(modes) != 2 || json.Unmarshal(modes[0], &mode) != nil || json.Unmarshal(modes[1], &dist) != nil {
		return nil, &APIError{"PLUGIN_VIEW_FAILED", "插件渲染模式返回格式无效", 500}
	}
	var raw json.RawMessage
	switch kind {
	case "form":
		raw, _, err = object.originalViewRead(ctx, "get_form", nil)
	case "page":
		raw, _, err = object.originalViewRead(ctx, "get_page", nil)
	case "dashboard":
		raw, _, err = object.originalViewDashboard(ctx, key, userAgent)
	default:
		return nil, &APIError{"NOT_FOUND", "插件视图不存在", 404}
	}
	if err != nil {
		if kind != "dashboard" && originalViewOrdinaryMethodFailure(err) {
			return map[string]any{}, nil
		}
		if kind == "dashboard" && originalViewOrdinaryMethodFailure(err) {
			return nil, &APIError{"PLUGIN_VIEW_FAILED", "插件仪表板方法调用失败", 500}
		}
		return nil, err
	}
	switch kind {
	case "form":
		var pair []json.RawMessage
		var model map[string]json.RawMessage
		if json.Unmarshal(raw, &pair) != nil || len(pair) != 2 || json.Unmarshal(pair[1], &model) != nil || model == nil {
			return map[string]any{}, nil
		}
		stored, err := a.originalManagerConfiguration(ctx, object.installation.InstanceID)
		if err != nil {
			// Source get_plugin_config runs inside the form's method catch.
			// A truthy non-dictionary saved value has no .items(); storage or
			// authority errors remain outside this ordinary value failure.
			var invalidConfig *json.UnmarshalTypeError
			if errors.As(err, &invalidConfig) {
				return map[string]any{}, nil
			}
			return nil, err
		}
		var storedObject json.RawMessage
		if originalViewJSONTruth(stored) {
			var values map[string]json.RawMessage
			if json.Unmarshal(stored, &values) != nil || values == nil {
				return map[string]any{}, nil
			}
			storedObject = stored
			for k, v := range values {
				model[k] = v
			}
		}
		switch object.installation.PluginID {
		case "RemoteIdentifiers":
			maskRemoteIdentifiersURLs(model)
		case "SeedRescue":
			maskOriginalSeedRescueSecrets(model)
		}
		if !originalObjectStillLoaded(object) {
			return nil, bad("PLUGIN_SNAPSHOT_STALE", "插件视图对象已替换")
		}
		// Object keys would otherwise be normalized by encoding/json before
		// the final serializer sees them. Values are checked after overrides:
		// a bad default replaced by a valid saved value must still succeed.
		if originalViewInvalidObjectKey(pair[1]) || originalViewInvalidObjectKey(storedObject) {
			return nil, originalViewSerializationError()
		}
		return originalViewSerializedResponse(map[string]any{"render_mode": modes[0], "conf": pair[0], "model": model})
	case "page":
		if !originalViewJSONTruth(raw) {
			raw = json.RawMessage(`[]`)
		}
		return originalViewSerializedResponse(map[string]any{"render_mode": modes[0], "page": raw})
	default:
		if string(bytes.TrimSpace(raw)) == "null" {
			return nil, nil
		}
		tuple, err := originalDashboardTuple(raw)
		if err != nil {
			return nil, err
		}
		name, err := object.originalDisplayName(ctx)
		if err != nil {
			return nil, err
		}
		response := originalDashboardResponse(object.installation.InstanceID, name, key, mode, tuple)
		response["render_mode"] = modes[0]
		return originalViewSerializedResponse(response)
	}
}

func originalViewJSONTruth(raw json.RawMessage) bool {
	// Python's JSON decoder keeps arbitrary integers, while decimal/exponent
	// numbers are binary64 (including overflow and underflow). The existing
	// Base truth helper already implements that source conversion.
	return originalBaseJSONTruthy(raw)
}
func originalViewOrdinaryMethodFailure(err error) bool {
	var denied *originalViewHostDenied
	if errors.As(err, &denied) {
		return false
	}
	var protocol *plugins.Error
	return errors.As(err, &protocol) && protocol.Code == "plugin_operation_failed"
}
