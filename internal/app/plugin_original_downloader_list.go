package app

import (
	"context"
	"encoding/json"
	"math"
	"strings"

	"light-media/internal/download"
)

// DownloadChain.downloading invokes list_torrents once, including when the
// downloader selector is absent. A list continues into core; a falsey scalar
// terminates with an empty page. The existing page then groups tasks by its
// configured downloader tabs and enforces the application's ownership scope.
func (a *App) originalDownloadingTorrents(ctx context.Context, selected string) ([]json.RawMessage, bool, error) {
	groups, err := a.originalPluginModules(ctx, "")
	if err != nil || !originalModuleGroupsHaveMethod(groups, "list_torrents") {
		return nil, true, err
	}
	var name any
	if selected != "" {
		for _, d := range a.Settings().Downloaders {
			if d.ID == selected {
				name = d.Name
				break
			}
		}
	}
	kwargs, _ := json.Marshal(map[string]any{"status": "下载中", "hashs": nil, "downloader": name, "include_all_tags": false})
	result, err := a.invokeOriginalPluginModuleGroups(ctx, "list_torrents", nil, json.RawMessage(`[]`), kwargs, nil, groups)
	if err != nil {
		return nil, false, err
	}
	if originalModuleResultEmpty(result) {
		return nil, true, nil
	}
	var rows []json.RawMessage
	if json.Unmarshal(result, &rows) == nil && rows != nil {
		return rows, true, nil
	}
	if !originalModuleResultTruthy(result) {
		return nil, false, nil
	}
	return nil, false, bad("PLUGIN_MODULE_RESULT_INVALID", "下载器插件没有返回任务列表")
}

func originalDownloadingTask(raw json.RawMessage, d DownloaderConfig, selected ...bool) (download.Task, bool, error) {
	var value struct {
		Downloader *string  `json:"downloader"`
		Hash       *string  `json:"hash"`
		Title      *string  `json:"title"`
		Name       *string  `json:"name"`
		SavePath   *string  `json:"save_path"`
		Progress   *float64 `json:"progress"`
		Size       *float64 `json:"size"`
		State      *string  `json:"state"`
		Tags       *string  `json:"tags"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return download.Task{}, false, bad("PLUGIN_MODULE_RESULT_INVALID", "下载器插件的任务字段无效")
	}
	// DownloadingListView displays the response for the selected downloader;
	// it does not re-filter plugin models by their optional downloader field.
	if (len(selected) == 0 || !selected[0]) && (value.Downloader == nil || *value.Downloader != d.Name) {
		return download.Task{}, false, nil
	}
	task := download.Task{InstanceID: d.ID, Status: "downloading", Tags: []string{}, SourceTorrent: append(json.RawMessage(nil), raw...)}
	if value.Hash != nil {
		task.ID, task.Hash = *value.Hash, *value.Hash
	}
	if value.Title != nil {
		task.Name = *value.Title
	} else if value.Name != nil {
		task.Name = *value.Name
	}
	if value.SavePath != nil {
		task.DownloadDir = *value.SavePath
	}
	if value.State != nil {
		task.Status = *value.State
	} else {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if _, exists := fields["state"]; exists {
			task.Status = ""
		}
	}
	if value.Progress != nil {
		if math.IsNaN(*value.Progress) || math.IsInf(*value.Progress, 0) {
			return download.Task{}, false, bad("PLUGIN_MODULE_RESULT_INVALID", "下载器插件的任务进度无效")
		}
		task.Progress = *value.Progress / 100
	}
	if value.Size != nil {
		// This optional source field is a float, not a native byte counter.
		// Cards read its original value; never reject a legal source model only
		// because the existing native Task counter has a narrower integer range.
		if *value.Size >= float64(math.MinInt64) && *value.Size < float64(math.MaxInt64) {
			task.TotalSize = int64(*value.Size)
		}
	}
	if value.Tags != nil {
		task.Tags = strings.Split(*value.Tags, ",")
	}
	return task, true, nil
}
