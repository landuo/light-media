package app

import (
	"context"
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
	native "light-media/internal/plugins/iyuuautoseed/native"
)

func originalSharedQBittorrentCompletedCapability(method string) bool {
	return method == plugins.QBittorrentCompletedQueryCapability
}

func (a *App) originalSharedQBittorrentCompletedResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if !originalSharedQBittorrentCompletedCapability(method) || scope.validate == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "qB已完成任务查询未获授权")
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	request, err := decodeSharedQBittorrentCompleted(raw)
	if err != nil {
		return nil, err
	}
	configured, err := a.downloader(request.downloaderID)
	if err != nil {
		return nil, err
	}
	if configured.Kind != "qb" && configured.Kind != "qbittorrent" {
		return nil, bad("DOWNLOADER_UNSUPPORTED", "此查询仅支持qBittorrent")
	}
	guard := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := scope.validate(ctx); err != nil {
			return err
		}
		current, err := a.downloader(request.downloaderID)
		if err != nil {
			return err
		}
		if current.Config != configured.Config {
			return bad("DOWNLOADER_CHANGED", "下载器连接配置已变化，请重新查询")
		}
		return nil
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	client, err := native.NewQBittorrentCompletedClient(native.Config{ID: configured.ID, Kind: configured.Kind, BaseURL: configured.BaseURL, Username: configured.Username, Password: configured.Password}, configured.APIKey, guard)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	rows, queryErr := client.Rows(ctx, request.hashes)
	if err = guard(ctx); err != nil {
		return nil, err
	}
	out := plugins.QBittorrentCompletedResult{SchemaVersion: 1, DownloaderID: configured.ID, Torrents: []plugins.QBittorrentTorrent{}}
	if queryErr != nil {
		var nativeError *native.Error
		if errors.As(queryErr, &nativeError) && nativeError.Code == "body_limit" {
			return nil, bad("PLUGIN_VALUE_TOO_LARGE", "qB已完成任务响应超过共享传输上限")
		}
		out.Error = true // source get_torrents catches query/auth/JSON failure.
	} else {
		out.Torrents, out.Error, err = sharedQBittorrentCompletedRows(ctx, rows, request.tags, guard)
		if err != nil {
			return nil, err
		}
	}
	output, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	if len(output) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "qB已完成任务响应超过共享传输上限")
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	return output, nil
}
