package app

import (
	"context"
	"encoding/json"

	"light-media/internal/plugins"
	"light-media/internal/plugins/downloadercommands"
)

func originalDownloaderControlMethod(method string) (kind, operation string, ok bool) {
	switch method {
	case plugins.QBittorrentSetTagsCapability:
		return "qbittorrent", "set_tags", true
	case plugins.QBittorrentSetCategoryCapability:
		return "qbittorrent", "set_category", true
	case plugins.QBittorrentStopCapability:
		return "qbittorrent", "stop", true
	case plugins.TransmissionStopCapability:
		return "transmission", "stop", true
	}
	return "", "", false
}

func originalSharedDownloaderControlCapability(method string) bool {
	_, _, ok := originalDownloaderControlMethod(method)
	return ok
}

func (a *App) originalSharedDownloaderControlResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	// Exactly the already installed, host-selected S03 native factory. There is
	// no worker-supplied endpoint, credentials, callback, retry or generic RPC.
	return a.originalDownloaderControlResourceWithFactory(ctx, scope, method, raw, newOriginalDownloaderCommandNative)
}

func (a *App) originalDownloaderControlResourceWithFactory(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage, factory originalDownloaderCommandNativeFactory) (json.RawMessage, error) {
	kind, action, allowed := originalDownloaderControlMethod(method)
	if !allowed || ctx == nil || scope.validate == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "下载器控制命令缺少实际调用范围")
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
	// None of these four operations has an upload envelope or larger bound.
	if len(raw) > 128<<10 {
		return nil, originalDownloaderUploadInvalid()
	}
	fields, operation, err := originalDownloaderCommandFields(raw)
	if err != nil || operation != "invoke" {
		return nil, originalDownloaderUploadInvalid()
	}
	keys := []string{"op", "downloader_id", "ids"}
	switch action {
	case "set_tags":
		keys = append(keys, "tags")
	case "set_category":
		keys = []string{"op", "downloader_id", "hash_string", "category"}
	}
	if !originalDownloaderCommandKeys(fields, keys...) {
		return nil, originalDownloaderUploadInvalid()
	}
	var downloaderID string
	if json.Unmarshal(fields["downloader_id"], &downloaderID) != nil {
		return nil, originalDownloaderUploadInvalid()
	}
	configured, err := a.originalDownloaderCommandSelect(ctx, scope, kind, downloaderID)
	if err != nil {
		return nil, err
	}
	selectorField := "ids"
	if action == "set_category" {
		selectorField = "hash_string"
	}
	selector, err := decodeOriginalDownloaderCommandValue(fields[selectorField])
	if err != nil {
		return nil, err
	}
	var extra any
	if action == "set_tags" || action == "set_category" {
		extraField := "tags"
		if action == "set_category" {
			extraField = "category"
		}
		extra, err = decodeOriginalDownloaderCommandValue(fields[extraField])
		if err != nil {
			return nil, err
		}
	}
	snapshot := (&originalDownloaderCommandSnapshot{configured: configured}).clone()
	guard := a.originalDownloaderCommandGuard(scope, kind, snapshot)
	if err = guard(ctx); err != nil {
		return nil, err
	}
	if factory == nil {
		return nil, &downloadercommands.BoundaryError{Reason: "configured native control factory is unavailable"}
	}
	qb, tr, closeClient, err := factory(snapshot.configured, guard)
	if closeClient != nil {
		defer closeClient()
	}
	if err != nil {
		return nil, err
	}
	if kind == "qbittorrent" && qb == nil || kind == "transmission" && tr == nil {
		return nil, &downloadercommands.BoundaryError{Reason: "configured native control client is unavailable"}
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	log := func(ctx context.Context, level, message string) {
		a.logOriginalPluginContext(ctx, level, "downloader_command", message)
	}
	q := downloadercommands.QBittorrent{Client: qb, Config: downloadercommands.QBConfig{Category: snapshot.configured.AutoCategory, Sequential: snapshot.configured.Sequential, FirstLastPiece: snapshot.configured.FirstLast}, Log: log}
	t := downloadercommands.Transmission{Client: tr, Log: log}
	var value any
	switch action {
	case "set_tags":
		// Source None is not a receipt that labels were successfully changed.
		err = q.SetTorrentsTag(ctx, selector, extra)
	case "set_category":
		value, err = q.SetTorrentCategory(ctx, selector, extra)
	case "stop":
		if kind == "qbittorrent" {
			value, err = q.StopTorrents(ctx, selector)
		} else {
			value, err = t.StopTorrents(ctx, selector)
		}
	}
	if checkErr := guard(ctx); checkErr != nil {
		return nil, checkErr
	}
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(struct {
		Value any `json:"value"`
	}{value})
	if err != nil {
		return nil, err
	}
	if len(out) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "下载器控制结果超过既有传输上限")
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
