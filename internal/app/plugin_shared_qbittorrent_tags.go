package app

import (
	"context"
	"encoding/json"

	"light-media/internal/plugins"
	"light-media/internal/plugins/downloadercommands"
)

func originalQBTagMethod(method string) (string, bool) {
	switch method {
	case plugins.QBittorrentDeleteTagsCapability:
		return "delete_tags", true
	case plugins.QBittorrentGetIDByTagCapability:
		return "get_id_by_tag", true
	default:
		return "", false
	}
}

func originalSharedQBTagCapability(method string) bool {
	_, ok := originalQBTagMethod(method)
	return ok
}

func (a *App) originalSharedQBTagResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	// Reuse the fixed host-selected independent qB client. No worker-provided
	// endpoint, credentials, arbitrary RPC, upload or alternate native factory.
	return a.originalQBTagResourceWithFactory(ctx, scope, method, raw, newOriginalDownloaderCommandNative)
}

func (a *App) originalQBTagResourceWithFactory(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage, factory originalDownloaderCommandNativeFactory) (json.RawMessage, error) {
	action, allowed := originalQBTagMethod(method)
	if !allowed || ctx == nil || scope.validate == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "下载器标签方法缺少实际调用范围")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	if !a.admitOriginalBackground() {
		return nil, bad("APP_STOPPING", "服务正在停止")
	}
	defer a.wg.Done()
	if len(raw) > 128<<10 {
		return nil, originalDownloaderUploadInvalid()
	}
	fields, operation, err := originalDownloaderCommandFields(raw)
	if err != nil || operation != "invoke" {
		return nil, originalDownloaderUploadInvalid()
	}
	first, second := "ids", "tag"
	if action == "get_id_by_tag" {
		first, second = "tags", "status"
	}
	if !originalDownloaderCommandKeys(fields, "op", "downloader_id", first, second) {
		return nil, originalDownloaderUploadInvalid()
	}
	var downloaderID string
	if json.Unmarshal(fields["downloader_id"], &downloaderID) != nil {
		return nil, originalDownloaderUploadInvalid()
	}
	configured, err := a.originalDownloaderCommandSelect(ctx, scope, "qbittorrent", downloaderID)
	if err != nil {
		return nil, err
	}
	left, err := decodeOriginalDownloaderCommandValue(fields[first])
	if err != nil {
		return nil, err
	}
	right, err := decodeOriginalDownloaderCommandValue(fields[second])
	if err != nil {
		return nil, err
	}
	snapshot := (&originalDownloaderCommandSnapshot{configured: configured}).clone()
	guard := a.originalDownloaderCommandGuard(scope, "qbittorrent", snapshot)
	if err := guard(ctx); err != nil {
		return nil, err
	}
	if factory == nil {
		return nil, &downloadercommands.BoundaryError{Reason: "configured qB tag factory is unavailable"}
	}
	qb, _, closeClient, err := factory(snapshot.configured, guard)
	if closeClient != nil {
		defer closeClient()
	}
	if err != nil {
		return nil, err
	}
	if qb == nil {
		return nil, &downloadercommands.BoundaryError{Reason: "configured qB tag client is unavailable"}
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	q := downloadercommands.QBTagCommands{QBittorrent: downloadercommands.QBittorrent{
		Client: qb,
		Log: func(ctx context.Context, level, message string) {
			a.logOriginalPluginContext(ctx, level, "downloader_command", message)
		},
	}}
	var value any
	if action == "delete_tags" {
		value, err = q.DeleteTorrentsTag(ctx, left, right)
	} else {
		value, err = q.GetTorrentIDByTag(ctx, left, right)
	}
	if guardErr := guard(ctx); guardErr != nil {
		return nil, guardErr
	}
	var result any = struct {
		Value any `json:"value"`
	}{value}
	if err != nil {
		// Only the independently observed escaping source class can cross this
		// explicit lookup envelope. Never unwrap a platform/unknown-effect error
		// containing SourceException; never publish its message or traceback.
		source, direct := err.(*downloadercommands.SourceException)
		if action != "get_id_by_tag" || !direct || source == nil || source.Class != "AttributeError" {
			return nil, err
		}
		result = struct {
			Kind        string `json:"kind"`
			SourceError string `json:"source_error"`
		}{"source_error", source.Class}
	}
	out, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if len(out) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "下载器标签结果超过既有传输上限")
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
