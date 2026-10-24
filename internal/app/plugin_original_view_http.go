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
	mode, _, _, err := object.originalViewRenderMode(ctx)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		if originalViewJSONTruth(stored) {
			var values map[string]json.RawMessage
			if json.Unmarshal(stored, &values) != nil || values == nil {
				return map[string]any{}, nil
			}
			for k, v := range values {
				model[k] = v
			}
		}
		if !originalObjectStillLoaded(object) {
			return nil, bad("PLUGIN_SNAPSHOT_STALE", "插件视图对象已替换")
		}
		return map[string]any{"render_mode": mode, "conf": pair[0], "model": model}, nil
	case "page":
		if !originalViewJSONTruth(raw) {
			raw = json.RawMessage(`[]`)
		}
		return map[string]any{"render_mode": mode, "page": raw}, nil
	default:
		if string(bytes.TrimSpace(raw)) == "null" {
			return nil, nil
		}
		var tuple []json.RawMessage
		if json.Unmarshal(raw, &tuple) != nil || len(tuple) != 3 {
			return nil, &APIError{"PLUGIN_VIEW_FAILED", "插件仪表板返回格式无效", 500}
		}
		if !originalViewJSONTruth(tuple[0]) {
			tuple[0] = json.RawMessage(`{}`)
		}
		if !originalViewJSONTruth(tuple[1]) {
			tuple[1] = json.RawMessage(`{}`)
		}
		return map[string]any{"id": object.installation.InstanceID, "name": object.manifest.Name, "key": key, "render_mode": mode, "cols": tuple[0], "attrs": tuple[1], "elements": tuple[2]}, nil
	}
}

func originalViewJSONTruth(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	return sourceSubscriptionTruth(value)
}
func originalViewOrdinaryMethodFailure(err error) bool {
	var denied *originalViewHostDenied
	if errors.As(err, &denied) {
		return false
	}
	var protocol *plugins.Error
	return errors.As(err, &protocol) && protocol.Code == "plugin_operation_failed"
}
