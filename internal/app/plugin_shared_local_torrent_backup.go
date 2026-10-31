package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"unicode/utf8"
)

func originalSharedLocalTorrentBackupCapability(method string) bool {
	return method == plugins.LocalTorrentBackupCapability
}

// This framing adapter exposes one purpose-bound local material operation. It
// neither opens files nor accepts a reusable pathname handle. The filesystem
// component independently validates current downloader/root/input authority.
func (a *App) originalSharedLocalTorrentBackupResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	if ctx == nil || scope.validate == nil || scope.torrentTransfers == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "本机种子材料缺少实际调用范围")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	if !utf8.Valid(raw) || !sharedHistoryScalarEscapes(raw) {
		return nil, bad("INVALID_PLUGIN_VALUE", "本机种子材料参数编码无效")
	}
	var request struct {
		Op     string                             `json:"op"`
		Input  *plugins.LocalTorrentBackupRequest `json:"input,omitempty"`
		Handle *string                            `json:"handle,omitempty"`
		Offset *int                               `json:"offset,omitempty"`
	}
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "本机种子材料操作必须是对象")
	}
	marshal := func(value any, allocated string) (json.RawMessage, error) {
		out, err := json.Marshal(value)
		if err == nil && len(out) > plugins.MaxPayloadBytes {
			err = bad("PLUGIN_VALUE_TOO_LARGE", "本机种子材料结果超过已有传输上限")
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = scope.validate(ctx)
		}
		if err != nil && allocated != "" {
			// Reclaim only the newly allocated invocation byte buffer. This must
			// never delete a rebuilt file or either backup input on disk.
			_ = scope.torrentTransfers.release(context.Background(), allocated)
		}
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	switch request.Op {
	case "read":
		if len(fields) != 3 || request.Input != nil || request.Handle == nil || request.Offset == nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "本机种子材料读取参数无效")
		}
		chunk, err := scope.torrentTransfers.read(ctx, *request.Handle, *request.Offset)
		if err != nil {
			return nil, err
		}
		return marshal(chunk, "")
	case "release":
		if len(fields) != 2 || request.Input != nil || request.Handle == nil || request.Offset != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "本机种子材料释放参数无效")
		}
		if err := scope.torrentTransfers.release(ctx, *request.Handle); err != nil {
			return nil, err
		}
		return marshal(map[string]bool{"released": true}, "")
	case "prepare":
		if len(fields) != 2 || request.Input == nil || request.Handle != nil || request.Offset != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "本机种子材料准备参数无效")
		}
	default:
		return nil, bad("INVALID_PLUGIN_VALUE", "未知本机种子材料操作")
	}
	if !a.admitOriginalBackground() {
		return nil, bad("APP_STOPPING", "服务正在停止")
	}
	defer a.wg.Done()
	result, err := a.prepareOriginalLocalTorrentBackup(ctx, scope, *request.Input)
	if guardErr := scope.validate(ctx); guardErr != nil {
		return nil, guardErr
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	if result.Skipped {
		if result.Temporary || len(result.Content) != 0 {
			return nil, bad("INVALID_PLUGIN_VALUE", "本机种子材料跳过结果无效")
		}
		return marshal(map[string]bool{"skipped": true, "temporary": false}, "")
	}
	// Prepare already completed the source operation and its filesystem guards.
	// Pages transfer immutable authorized bytes, checking invocation authority;
	// they do not reopen backup files or repeat filesystem/downloader requests.
	prepared, err := scope.torrentTransfers.publish(ctx, result.Content)
	if err != nil {
		return nil, err
	}
	return marshal(map[string]any{"skipped": false, "temporary": result.Temporary,
		"handle": prepared.Handle, "total": prepared.Total, "sha256": prepared.SHA256}, prepared.Handle)
}
