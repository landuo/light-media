package app

import (
	"context"
	"encoding/json"
	"errors"

	"light-media/internal/download"
)

// Source DownloadChain.set_downloading/remove_downloading forwards exact hash
// lists and downloader names. Native ownership/path checks run before this
// extension; verify is an existing native operation with no source wrapper.
// applied is separate from source truth: [] still calls core but its bool
// return is ignored by list accumulation, so the API reports failure after an
// actual mutation. Owned cancellation bookkeeping must retain that fact.
func (a *App) originalDownloaderControl(ctx context.Context, d DownloaderConfig, hash, action string, native func() error) (applied bool, err error) {
	return a.originalDownloaderControlValues(ctx, d, action, map[string]any{"hashs": []string{hash}, "downloader": d.Name}, native)
}

func (a *App) originalDownloaderControlValues(ctx context.Context, d DownloaderConfig, action string, kwargs map[string]any, native func() error) (applied bool, err error) {
	method := ""
	switch action {
	case "start":
		method = "start_torrents"
	case "pause":
		method = "stop_torrents"
	case "remove_keep_data", "remove_with_data":
		method = "remove_torrents"
		kwargs["delete_file"] = action == "remove_with_data"
	default:
		err = native()
		return err == nil, err
	}
	groups, err := a.originalPluginModules(ctx, "")
	if err != nil {
		return false, err
	}
	if !originalModuleGroupsHaveMethod(groups, method) {
		err = native()
		return err == nil, err
	}
	raw, _ := json.Marshal(kwargs)
	result, err := a.invokeOriginalPluginModuleGroups(ctx, method, nil, json.RawMessage(`[]`), raw, nil, groups)
	if err != nil {
		return false, err
	}
	if originalModuleResultEmpty(result) {
		err = native()
		return err == nil, err
	}
	var list []json.RawMessage
	if json.Unmarshal(result, &list) == nil {
		err = native()
		applied = err == nil
		var protocol *download.Error
		if err != nil && !errors.As(err, &protocol) {
			module := "Qbittorrent"
			if d.Kind == "tr" || d.Kind == "transmission" {
				module = "Transmission"
			}
			a.reportOriginalPluginExecutionError(ctx, "system", module+"发生了错误", map[string]any{"type": "module", "module_id": module, "module_name": module, "module_method": method}, err)
		}
	} else {
		applied = originalModuleResultTruthy(result)
	}
	if !originalModuleResultTruthy(result) {
		return applied, bad("PLUGIN_DOWNLOADER_REJECTED", "下载器插件未确认操作成功")
	}
	return applied, nil
}
