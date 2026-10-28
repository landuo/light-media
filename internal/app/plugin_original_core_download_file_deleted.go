package app

import (
	"context"
	"encoding/json"
	"errors"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

// This is the ordinary EventManager consumer, separate from the native
// history-delete operation's private durable evidence. An SDK event is not a
// history deletion fact and cannot mint any privileged domain receipt.
func (a *App) consumeOriginalCoreDownloadFileDeleted(ctx context.Context, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return bad("PLUGIN_EVENT_INVALID", "下载文件删除事件不是对象")
	}
	if !originalModuleResultTruthy(fields["hash"]) {
		return nil
	}
	hashValue := fields["hash"]
	var hash string
	_ = json.Unmarshal(hashValue, &hash)
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	groups, err := a.originalPluginModules(ctx, "")
	if err != nil {
		return err
	}
	kwargs, _ := json.Marshal(map[string]any{"status": nil, "hashs": []json.RawMessage{hashValue}, "downloader": nil, "include_all_tags": false})
	var rows []json.RawMessage
	core, invalid := true, false
	if originalModuleGroupsHaveMethod(groups, "list_torrents") {
		result, callErr := a.invokeOriginalPluginModuleGroups(ctx, "list_torrents", nil, json.RawMessage(`[]`), kwargs, nil, groups)
		if callErr != nil {
			return callErr
		}
		if !originalModuleResultEmpty(result) {
			if json.Unmarshal(result, &rows) == nil && rows != nil {
				// Source lists continue into core and concatenate its list.
			} else if tuple, ok := plugins.DecodeModuleResultTuple(result); ok {
				rows, core = tuple, false
			} else if !originalModuleResultTruthy(result) {
				return nil
			} else {
				core, invalid = false, true
			}
		}
	}
	settings := a.Settings()
	if core {
		for _, kind := range []string{"qbittorrent", "transmission"} {
			kindRows := []json.RawMessage{}
			for _, d := range settings.Downloaders {
				if !downloaderEnabled(d) || coreDownloaderKind(d.Kind) != kind {
					continue
				}
				client, e := download.New(d.Config)
				if e != nil {
					continue
				}
				snapshots, e := client.SourceTorrentSnapshots(ctx, hash)
				if e != nil {
					continue
				}
				projected, e := a.coreDeletedTorrentSnapshots(ctx, d, hash, snapshots)
				if e != nil {
					kindRows = nil
					break
				}
				for _, row := range projected {
					body, _ := json.Marshal(row)
					kindRows = append(kindRows, body)
				}
			}
			rows = append(rows, kindRows...)
		}
	}
	if len(rows) == 0 && !invalid {
		return nil
	}
	var target DownloaderConfig
	if settings.DefaultDownloader != "" {
		for _, d := range settings.Downloaders {
			if d.ID == settings.DefaultDownloader {
				target = d
				break
			}
		}
	} else if len(settings.Downloaders) > 0 {
		target = settings.Downloaders[0]
	}
	// Source removes using the default selection, regardless of which list
	// instance matched. The existing native ownership guard still applies to
	// the actual target; the operation always keeps local data.
	_, removeErr := a.originalDownloaderControlValues(ctx, target, "remove_keep_data", map[string]any{"hashs": []json.RawMessage{hashValue}, "downloader": nil}, func() error {
		if target.ID == "" || !downloaderEnabled(target) {
			return &download.Error{Operation: "remove", Code: "default_unavailable"}
		}
		client, e := download.New(target.Config)
		if e != nil {
			return e
		}
		tasks, e := client.List(ctx)
		if e != nil {
			return e
		}
		owned, e := a.downloaderTaskOwnership(ctx, target.ID)
		if e != nil {
			return e
		}
		for _, task := range tasks {
			if hash != "" && sameHash(task.Hash, hash) && owned.contains(task) {
				return client.SourceRemoveKeepData(ctx, hash)
			}
		}
		return &download.Error{Operation: "remove", Code: "not_owned"}
	})
	if removeErr != nil {
		// False/None from remove_torrents never suppresses DownloadDeleted.
		// Keep the failure separate from the original source observation.
		var api *APIError
		var protocol *download.Error
		if !errors.As(removeErr, &api) && !errors.As(removeErr, &protocol) {
			return removeErr
		}
	}
	if invalid {
		return bad("PLUGIN_MODULE_RESULT_INVALID", "下载器插件任务列表不能投影为DownloaderTorrent")
	}
	for _, row := range rows {
		var object map[string]json.RawMessage
		if json.Unmarshal(row, &object) != nil || object == nil {
			return bad("PLUGIN_MODULE_RESULT_INVALID", "下载器插件任务不能投影为DownloaderTorrent")
		}
	}
	a.emitStandardBusinessEvent(ctx, "download.deleted", map[string]any{"hash": hashValue, "torrents": rows})
	return nil
}
