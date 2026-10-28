package app

import (
	"context"
	"encoding/json"

	"light-media/internal/plugins"
)

// The native provider advertises only the existing Go transfer operations.
// Keep the source key "link" at the plugin boundary (UI maps it to hardlink).
var originalLocalTransferTypes = json.RawMessage(`{"link":"硬链接","copy":"复制","auto":"自动"}`)

func (a *App) originalTransferTypes(ctx context.Context) (any, error) {
	return a.originalTransferTypesWithNative(ctx, func() (any, error) {
		return originalLocalTransferTypes, nil
	})
}

func (a *App) originalTransferTypesWithNative(ctx context.Context, native func() (any, error)) (any, error) {
	raw, err := a.originalMetadataStorage(ctx, "support_transtype", map[string]any{"storage": "local"}, native)
	if err != nil {
		return nil, err
	}
	if tuple, ok := plugins.DecodeModuleResultTuple(raw); ok {
		if len(tuple) != 0 {
			return nil, originalTransferTypesInvalid()
		}
		raw = json.RawMessage(`{}`)
	} else if !originalModuleResultTruthy(raw) {
		raw = json.RawMessage(`{}`)
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, originalTransferTypesInvalid()
	}
	if _, encoded := plugins.DecodeModuleResultBytes(raw); encoded {
		return nil, originalTransferTypesInvalid()
	}
	// Preserve the plugin dictionary's key order, used by DirectoryCard for
	// the first available selection; marshaling a Go map would sort its keys.
	return struct {
		Types json.RawMessage `json:"transtype"`
	}{raw}, nil
}

func originalTransferTypesInvalid() error {
	return &APIError{"PLUGIN_TRANSFER_TYPES_INVALID", "插件整理方式返回格式无效", 500}
}
