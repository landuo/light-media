package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"light-media/internal/catalog"
	"light-media/internal/download"
	"light-media/internal/plugins"
)

type originalDownloadOptions struct {
	CategorySet                       bool
	Cookie, Episodes, Category, Label any
	Downloader                        json.RawMessage
	BeforeInvoke                      func() error
	Media                             *catalog.Media
}

type originalDownloadResult struct {
	Downloader, Layout                           json.RawMessage
	Added                                        download.AddResult
	Declared, Override, CoreCalled, CoreAccepted bool
	Raw                                          json.RawMessage
}

func sourceDownloadName(d DownloaderConfig, requested string) json.RawMessage {
	if requested == "" {
		return json.RawMessage(`null`)
	}
	raw, _ := json.Marshal(d.Name)
	return raw
}

// download_single chooses its explicit downloader first, then the torrent's
// site's downloader. The source uses a display name; the native host has stable
// IDs, so resolve that existing setting before freezing either queued caller.
func (a *App) originalDownloadSelection(requested string, site SiteConfig) (DownloaderConfig, json.RawMessage, error) {
	selected := requested
	if selected == "" && site.Downloader != "" {
		selected = site.Downloader
		for _, candidate := range a.Settings().Downloaders {
			if candidate.Name == site.Downloader || candidate.ID == site.Downloader {
				selected = candidate.ID
				break
			}
		}
	}
	d, err := a.downloader(selected)
	if err != nil {
		return d, nil, err
	}
	return d, sourceDownloadName(d, selected), nil
}

// The fixed download wrapper passes seven kwargs. A plugin tuple terminates
// the chain, while a plugin list still invokes the seven-argument core method
// and ignores its tuple return. Preserve the actual core Add receipt separately
// from the caller's result; [] can leave a real task after reporting failure.
func (a *App) originalDownload(ctx context.Context, d DownloaderConfig, request download.AddRequest, options originalDownloadOptions, native func(originalDownloadOptions) (download.AddResult, error)) (originalDownloadResult, error) {
	out := originalDownloadResult{}
	out.Downloader, _ = json.Marshal(d.Name)
	out.Layout = json.RawMessage(`"Original"`)
	core := func() error {
		out.CoreCalled = true
		var err error
		out.Added, err = native(options)
		out.CoreAccepted = err == nil
		return err
	}
	groups, err := a.originalPluginModules(ctx, "")
	if err != nil {
		return out, err
	}
	if !originalModuleGroupsHaveMethod(groups, "download") {
		return out, core()
	}
	out.Declared = true
	if !options.CategorySet && options.Category == nil && options.Media != nil {
		_, category, categoryErr := a.importMediaCategory(ctx, *options.Media)
		if categoryErr != nil {
			return out, categoryErr
		}
		options.Category = category
	}
	if options.BeforeInvoke != nil {
		if err = options.BeforeInvoke(); err != nil {
			return out, err
		}
	}
	content, err := plugins.ModuleResultBytes(request.Torrent)
	if err != nil {
		return out, err
	}
	if len(options.Downloader) == 0 {
		options.Downloader, _ = json.Marshal(d.Name)
	} // legacy queued host records already resolved their choice
	kwargs, _ := json.Marshal(map[string]any{"content": content, "download_dir": request.SavePath, "cookie": options.Cookie, "episodes": options.Episodes, "category": options.Category, "label": options.Label, "downloader": options.Downloader})
	out.Raw, err = a.invokeOriginalPluginModuleGroups(ctx, "download", nil, json.RawMessage(`[]`), kwargs, nil, groups)
	if err != nil {
		return out, err
	}
	if originalModuleResultEmpty(out.Raw) {
		return out, core()
	}
	out.Override = true
	var items []json.RawMessage
	if json.Unmarshal(out.Raw, &items) == nil {
		if err := core(); err != nil {
			var protocol *download.Error
			if !errors.As(err, &protocol) {
				module := "Qbittorrent"
				if d.Kind == "tr" || d.Kind == "transmission" {
					module = "Transmission"
				}
				a.reportOriginalPluginExecutionError(ctx, "system", module+"发生了错误", map[string]any{"type": "module", "module_id": module, "module_name": module, "module_method": "download"}, err)
			}
		}
	} else if tuple, ok := plugins.DecodeModuleResultTuple(out.Raw); ok {
		items = tuple
	}
	if !originalModuleResultTruthy(out.Raw) {
		return out, bad("REVIEW_PLUGIN_DOWNLOAD_REJECTED", "未找到下载器")
	}
	if len(items) != 4 {
		return out, &APIError{"REVIEW_PLUGIN_DOWNLOAD_RESULT_INVALID", "下载插件返回值不是下载器名称、Hash、布局、错误的四元组", 500}
	}
	out.Downloader = append(json.RawMessage(nil), items[0]...)
	out.Layout = append(json.RawMessage(nil), items[2]...)
	var hash, message string
	if json.Unmarshal(items[1], &hash) != nil || json.Unmarshal(items[3], &message) != nil {
		return out, &APIError{"REVIEW_PLUGIN_DOWNLOAD_RESULT_INVALID", "下载插件返回的Hash或错误字段无效", 500}
	}
	if hash == "" {
		if message == "" {
			message = "下载插件未返回任务Hash"
		}
		return out, bad("REVIEW_PLUGIN_DOWNLOAD_REJECTED", a.clean(message))
	}
	if out.CoreAccepted && !sameHash(hash, out.Added.Hash) {
		return out, bad("REVIEW_PLUGIN_DOWNLOAD_HASH_CHANGED", "插件返回Hash与本次实际核心添加结果不同，请核对已产生的任务")
	}
	if !out.CoreAccepted {
		out.Added = download.AddResult{Hash: hash}
	}
	return out, nil
}

// Module return data cannot mint Client.Add's private receipt. An override owns
// all of its original post-Add work. The existing native acceptance path may
// adopt only the real requested hash at the frozen local destination, as it
// already does for an observed pre-existing duplicate.
func verifyOriginalDownloadTask(ctx context.Context, client *download.Client, expected, returned, directory string) error {
	if !sameHash(expected, returned) {
		return bad("REVIEW_PLUGIN_DOWNLOAD_HASH_CHANGED", "插件返回的种子Hash与已核对材料不一致")
	}
	tasks, err := client.List(ctx)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if sameHash(task.Hash, returned) {
			if filepath.Clean(task.DownloadDir) != filepath.Clean(directory) {
				return bad("REVIEW_PLUGIN_DOWNLOAD_PATH_CHANGED", "插件下载任务未位于已冻结的保存目录")
			}
			return nil
		}
	}
	return bad("REVIEW_PLUGIN_DOWNLOAD_UNCONFIRMED", "未在已选下载器观察到插件返回的真实任务")
}
