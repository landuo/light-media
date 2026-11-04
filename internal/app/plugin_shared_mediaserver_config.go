package app

import (
	"context"
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
)

func originalSharedMediaServerConfigCapability(method string) bool {
	return method == plugins.MediaServerConfigsCapability
}

// The standard resource dispatcher admits the exact recognized contract and
// current object identity before entering here. This private reader neither
// chooses an endpoint nor exposes raw credentials through ordinary HTTP/events.
func (a *App) originalSharedMediaServerConfigResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if ctx == nil || !originalSharedMediaServerConfigCapability(method) || scope.validate == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "媒体服务器配置读取未授权")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var input plugins.MediaServerConfigsRequest
	if err := originalResourceDecode(raw, &input); err != nil {
		return nil, err
	}
	a.pluginMu.Lock()
	if err := scope.validate(ctx); err != nil {
		a.pluginMu.Unlock()
		return nil, err
	}
	state := a.originalMediaServerConfig
	if state == nil {
		a.pluginMu.Unlock()
		return nil, bad("MEDIA_SERVER_CONFIG_NOT_ESTABLISHED", "原媒体服务器配置尚未装配")
	}
	value, err := state.get(ctx)
	a.pluginMu.Unlock()
	if err != nil {
		return nil, err
	}
	result, parseErr := plugins.ParseMediaServerConfigs(value, input.IncludeDisabled)
	response := plugins.MediaServerConfigsResponse{Result: &result}
	if parseErr != nil {
		var source *plugins.MediaServerConfigSourceError
		if !errors.As(parseErr, &source) {
			return nil, parseErr
		}
		response = plugins.MediaServerConfigsResponse{SourceErrorClass: source.Class}
	}
	encoded, encodeErr := json.Marshal(response)
	// Parsing does not pin a stale object. Revalidate after work and before any
	// credential-bearing data is returned to its current authorized worker.
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	// Preserve the source warning/error sequence through the existing private
	// caller-attributed log pipeline, but never its raw ValidationError text.
	// The leaf's diagnostic type contains only index/class/field/kind metadata.
	for _, diagnostic := range result.Diagnostics {
		level := "WARN"
		if diagnostic.Severity == "error" {
			level = "ERROR"
		}
		message, err := json.Marshal(diagnostic)
		if err != nil {
			return nil, err
		}
		a.logOriginalPluginContext(ctx, level, "mediaserver_config", string(message))
	}
	if encodeErr != nil || len(encoded) > plugins.MaxPayloadBytes {
		return nil, bad("MEDIA_SERVER_CONFIG_RESULT_LIMIT", "媒体服务器配置结果超出既有SDK传输范围")
	}
	return encoded, nil
}
