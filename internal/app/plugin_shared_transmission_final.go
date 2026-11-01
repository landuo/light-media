package app

import (
	"context"
	"encoding/json"

	"light-media/internal/plugins"
	"light-media/internal/plugins/downloadercommands"
)

func originalTRFinalMethod(method string) (string, bool) {
	switch method {
	case plugins.TransmissionSetTagsCapability:
		return "set_tags", true
	case plugins.TransmissionChangeCapability:
		return "change", true
	default:
		return "", false
	}
}

func originalSharedTRFinalCapability(method string) bool {
	_, ok := originalTRFinalMethod(method)
	return ok
}

func (a *App) originalSharedTRFinalResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	// Only the fixed host-selected native TR client. No worker URL, credentials,
	// arbitrary settings/RPC, alternate production factory or transport policy.
	return a.originalTRFinalResourceWithFactory(ctx, scope, method, raw, newOriginalDownloaderCommandNative)
}

func (a *App) originalTRFinalResourceWithFactory(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage, factory originalDownloaderCommandNativeFactory) (json.RawMessage, error) {
	action, allowed := originalTRFinalMethod(method)
	if !allowed || ctx == nil || scope.validate == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "下载器设置方法缺少实际调用范围")
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
	keys := []string{"op", "downloader_id", "ids", "tags", "org_tags"}
	if action == "change" {
		keys = []string{"op", "downloader_id", "hash_string", "upload_limit", "download_limit", "ratio_limit", "seeding_time_limit"}
	}
	if !originalDownloaderCommandKeys(fields, keys...) {
		return nil, originalDownloaderUploadInvalid()
	}
	var downloaderID string
	if json.Unmarshal(fields["downloader_id"], &downloaderID) != nil {
		return nil, originalDownloaderUploadInvalid()
	}
	configured, err := a.originalDownloaderCommandSelect(ctx, scope, "transmission", downloaderID)
	if err != nil {
		return nil, err
	}
	values := make([]any, len(keys)-2)
	for i, key := range keys[2:] {
		values[i], err = decodeOriginalDownloaderCommandValue(fields[key])
		if err != nil {
			return nil, err
		}
	}
	snapshot := (&originalDownloaderCommandSnapshot{configured: configured}).clone()
	guard := a.originalDownloaderCommandGuard(scope, "transmission", snapshot)
	if err = guard(ctx); err != nil {
		return nil, err
	}
	if factory == nil {
		return nil, &downloadercommands.BoundaryError{Reason: "configured TR settings factory is unavailable"}
	}
	_, tr, closeClient, err := factory(snapshot.configured, guard)
	if closeClient != nil {
		defer closeClient()
	}
	if err != nil {
		return nil, err
	}
	if tr == nil {
		return nil, &downloadercommands.BoundaryError{Reason: "configured TR settings client is unavailable"}
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	t := downloadercommands.Transmission{Client: tr, Log: func(ctx context.Context, level, message string) {
		if level == "WARNING" {
			level = "WARN"
		}
		a.logOriginalPluginContext(ctx, level, "downloader_command", message)
	}}
	var value bool
	if action == "set_tags" {
		value, err = t.SetTorrentTag(ctx, values[0], values[1], values[2])
	} else {
		value, err = t.ChangeTorrent(ctx, values[0], values[1], values[2], values[3], values[4])
	}
	if guardErr := guard(ctx); guardErr != nil {
		return nil, guardErr
	}
	var result any = struct {
		Value bool `json:"value"`
	}{value}
	if err != nil {
		// Only the observed direct preparation ValueError may escape change's
		// source try. Never unwrap selected/unknown or other platform errors.
		source, direct := err.(*downloadercommands.SourceException)
		if action != "change" || !direct || source == nil || source.Class != "ValueError" {
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
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "下载器设置结果超过既有传输上限")
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
