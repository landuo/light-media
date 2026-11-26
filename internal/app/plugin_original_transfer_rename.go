package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"light-media/internal/directorysource"

	"light-media/internal/noticetemplate"
	"light-media/internal/plugins"
)

// Fixed transhandler.get_rename_path: build only supplies rename_dict; changing
// template_string in the event cannot replace the actual template argument.
// The second event applies updated_str only when both flags are truthy.
func (a *App) originalTransferRenamePath(ctx context.Context, template string, variables map[string]any, base, source *string, item *plugins.ModuleFileItem, observe ...bool) (string, error) {
	dispatch := len(observe) == 0 || observe[0]
	// Most transfers have no rename hooks. Avoid copying the entire naming
	// context (credits, images and all season data) into an unused event.
	hasHandlers := func(name string) bool {
		if !dispatch {
			return false
		}
		a.pluginOriginalEventsMu.Lock()
		defer a.pluginOriginalEventsMu.Unlock()
		return len(a.pluginOriginalChainHandlers[name]) != 0
	}
	if hasHandlers("transfer.rename.build") {
		raw, err := json.Marshal(map[string]any{"template_string": template, "rename_dict": variables, "source_path": source, "source_item": item})
		if err != nil {
			return "", err
		}
		build := &plugins.MutableEvent{Type: "transfer.rename.build", Data: raw}
		a.sendOriginalChainEvent(ctx, build)
		if originalBaseJSONTruthy(build.Data) {
			value, err := decodeSourceJSON(build.Data)
			if err != nil || sourceMap(value) == nil {
				return "", originalTransferRenameInvalid()
			}
			dict, ok := sourceMap(value)["rename_dict"].(map[string]any)
			if !ok {
				return "", originalTransferRenameInvalid()
			}
			variables = dict
		}
	}
	renderVars := noticetemplate.Object(variables)
	if template == originalNativeTVNaming || template == originalNativeMovieNaming {
		// These two host templates only reference the ordinary naming fields.
		// Keep the original context for both hooks, but avoid a deep copy of
		// the raw MediaInfo (including all credits) in the Jinja renderer.
		renderVars = make(noticetemplate.Object, len(variables))
		for name, value := range variables {
			if !strings.HasPrefix(name, "__") {
				renderVars[name] = value
			}
		}
	}
	rendered, err := noticetemplate.RenderWithContext(ctx, template, renderVars)
	if err != nil {
		return "", err
	}
	if hasHandlers("transfer.rename") {
		raw, err := json.Marshal(map[string]any{"template_string": template, "rename_dict": variables, "render_str": rendered, "path": base, "source_path": source, "source_item": item, "updated": false, "updated_str": nil, "source": "未知拦截源"})
		if err != nil {
			return "", err
		}
		change := &plugins.MutableEvent{Type: "transfer.rename", Data: raw}
		a.sendOriginalChainEvent(ctx, change)
		if originalBaseJSONTruthy(change.Data) {
			value, err := decodeSourceJSON(change.Data)
			if err != nil || sourceMap(value) == nil {
				return "", originalTransferRenameInvalid()
			}
			fields := sourceMap(value)
			if sourceSubscriptionTruth(fields["updated"]) && sourceSubscriptionTruth(fields["updated_str"]) {
				text, ok := fields["updated_str"].(string)
				if !ok {
					return "", originalTransferRenameInvalid()
				}
				rendered = text
			}
		}
	}
	if base != nil && *base != "" && !filepath.IsAbs(rendered) {
		return directorysource.PathString(*base + "/" + rendered), nil
	}
	if rendered == "" {
		return ".", nil
	}
	return directorysource.PathString(rendered), nil
}

func originalTransferRenameInvalid() error {
	return bad("PLUGIN_EVENT_INVALID", "整理命名事件返回的字段无效")
}
