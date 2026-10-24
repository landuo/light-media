package app

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	"light-media/internal/plugins"
)

// This is host-only attribution for one active SDK exchange. It is not a
// plugin_runs row or an authorization token and is never accepted in payloads.
type originalPluginLogCall struct {
	id    string
	phase string
}

func (a *App) pluginOriginalLogHost(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	if scope.validate == nil || scope.logCall.id == "" || scope.logCall.phase == "" {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "插件日志缺少实际调用范围")
	}
	if len(raw) > plugins.LogPayloadLimit || !utf8.Valid(raw) {
		return nil, bad("INVALID_PLUGIN_VALUE", "插件日志参数超过限额或编码无效")
	}
	var input struct {
		Level   plugins.LogLevel `json:"level"`
		Message *string          `json:"message"`
	}
	if err := originalResourceDecode(raw, &input); err != nil {
		return nil, err
	}
	if input.Message == nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "插件日志缺少消息")
	}
	entry := plugins.LogEntryV1{Level: input.Level, Message: *input.Message}
	if err := entry.Validate(); err != nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "插件日志级别或消息无效")
	}
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	level := string(entry.Level)
	if entry.Level == plugins.LogWarning {
		level = "WARN"
	}
	in := scope.installation
	// Reuse the sole writer/redactor, bounded queue, rotation, SSE, history and
	// export pipeline. Neither raw stderr nor arbitrary worker fields are copied.
	a.writeLog(map[string]any{
		"level": level, "module": "plugins", "event": "plugin_message", "message": entry.Message,
		"app_version": appVersion, "plugin_id": in.PluginID, "plugin_version": in.Version,
		"plugin_instance_id": in.InstanceID, "plugin_call_id": scope.logCall.id, "operation": scope.logCall.phase,
	})
	return json.RawMessage(`null`), nil
}
