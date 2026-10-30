package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/sourcejson"
)

// Publish the source helper/event sequence using a safe transport error code.
// The worker's arbitrary exception text and a nonexistent Python traceback are
// never reconstructed. Fields are host-selected identity, not worker payload.
func (a *App) reportOriginalPluginExecutionError(ctx context.Context, role, title string, fields map[string]any, err error) {
	code := "plugin_callback_failed"
	var sdk *plugins.Error
	if errors.As(err, &sdk) && len(sdk.Code) > 0 && len(sdk.Code) <= 64 {
		valid := true
		for _, c := range sdk.Code {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
				valid = false
				break
			}
		}
		if valid {
			code = sdk.Code
		}
	}
	title = a.clean(title)
	message := "Go 插件执行失败：" + code
	a.systemMessages.put(role, &title, message, nil, time.Now())
	if fields == nil {
		return
	}
	fields["error"], fields["error_code"], fields["traceback"], fields["diagnostic_source"] = message, code, "", "go-sdk-error-code/v1"
	raw, marshalErr := json.Marshal(fields)
	if marshalErr != nil {
		return
	}
	a.broadcastOriginalPluginEvent(context.WithoutCancel(ctx), "system.error", raw)
}

func (a *App) reportOriginalEventError(ctx context.Context, object *originalPluginInstance, event, method string, err error) {
	name := object.manifest.Name
	if current, nameErr := object.originalDisplayName(ctx); nameErr == nil {
		name = current
	}
	a.reportOriginalPluginExecutionError(ctx, "system", name+" 处理事件 "+event+" 时出错", map[string]any{"type": "event", "event_type": event, "event_handle": object.installation.InstanceID + "." + method}, err)
}

// Checked reset alone follows the source exception-handler .name access.
// Mutations have already been applied. Missing/failed access stops dispatch
// before the original notification/SystemError and before reset's stop/delete.
func (a *App) reportOriginalResetEventError(ctx context.Context, object *originalPluginInstance, event, method string, err error) error {
	var abort originalResetAbort
	if errors.As(err, &abort) {
		return abort
	}
	var failure originalMutableCallbackFailure
	if !errors.As(err, &failure) {
		return originalResetAbort{bad("PLUGIN_EVENT_CALLBACK_BOUNDARY", "插件重置事件未返回有效失败快照")}
	}
	switch failure.name.State {
	case "missing":
		return originalResetAbort{bad("PLUGIN_EVENT_NAME_MISSING", "插件重置事件错误处理所需的 name 属性不存在")}
	case "failed":
		return originalResetAbort{bad("PLUGIN_EVENT_NAME_FAILED", "插件重置事件错误处理读取 name 属性失败")}
	case "value":
		name, nameErr := sourcejson.String(failure.name.Value)
		if nameErr != nil {
			return originalResetAbort{bad("PLUGIN_EVENT_SNAPSHOT_INVALID", "插件重置事件失败快照无效")}
		}
		a.reportOriginalPluginExecutionError(ctx, "system", name+" 处理事件 "+event+" 时出错", map[string]any{"type": "event", "event_type": event, "event_handle": object.installation.InstanceID + "." + method}, err)
		return err
	default:
		return originalResetAbort{bad("PLUGIN_EVENT_SNAPSHOT_INVALID", "插件重置事件失败快照无效")}
	}
}
