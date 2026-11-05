package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"light-media/internal/plugins"
	native "light-media/internal/plugins/iyuuautoseed/native"
	"light-media/internal/releasemeta/legacyanime"
)

func originalHostDownloaderCapability(method string) bool {
	switch method {
	case plugins.DownloaderConnectionCapability, plugins.DownloaderDeleteFilesCapability, plugins.DownloaderCompletedCapability, plugins.DownloaderDownloadingCapability, plugins.DownloaderAddCapability, plugins.DownloaderControlCapability, plugins.DownloaderTagsCapability, plugins.DownloaderFindByTagCapability, plugins.DownloaderDeleteTagsCapability, plugins.DownloaderListCapability, plugins.DownloaderTrackersCapability:
		return true
	}
	return false
}

func (a *App) originalHostDownloaderClient(ctx context.Context, scope originalBaseCallScope, id string) (*native.IYUUClient, string, func(context.Context) error, error) {
	return a.originalHostDownloaderBoundClient(ctx, scope, id, true)
}

func (a *App) originalHostDownloaderMutationClient(ctx context.Context, scope originalBaseCallScope, id string) (*native.IYUUClient, string, func(context.Context) error, error) {
	return a.originalHostDownloaderBoundClient(ctx, scope, id, true)
}

func (a *App) originalHostDownloaderBoundClient(ctx context.Context, scope originalBaseCallScope, id string, requireEnabled bool) (*native.IYUUClient, string, func(context.Context) error, error) {
	if _, err := (plugins.DownloaderTasksRequestV1{DownloaderID: id, Hashes: []string{}}).CanonicalSelection(); err != nil {
		return nil, "", nil, bad("INVALID_PLUGIN_VALUE", "必须指定下载器实例 ID")
	}
	configured, err := a.configuredDownloader(id)
	if err != nil {
		return nil, "", nil, err
	}
	enabled := downloaderEnabled(configured)
	kind := configured.Kind
	switch kind {
	case "qb", "qbittorrent":
		kind = "qbittorrent"
	case "tr", "transmission":
		kind = "transmission"
	default:
		return nil, "", nil, bad("DOWNLOADER_UNSUPPORTED", "下载器类型不支持插件操作")
	}
	guard := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if scope.validate == nil {
			return bad("PLUGIN_CAPABILITY_DENIED", "插件下载器操作不在当前调用范围内")
		}
		if err := scope.validate(ctx); err != nil {
			return err
		}
		current, err := a.configuredDownloader(id)
		if err != nil {
			return err
		}
		if current.Config != configured.Config || downloaderEnabled(current) != enabled {
			return bad("DOWNLOADER_CHANGED", "下载器连接配置已变化，请重新调用")
		}
		if requireEnabled && !downloaderEnabled(current) {
			return bad("DOWNLOADER_DISABLED", "此下载器已停用，不能执行插件写操作")
		}
		return nil
	}
	if err := guard(ctx); err != nil {
		return nil, "", nil, err
	}
	client, err := native.NewIYUUClient(native.Config{ID: id, Kind: kind, BaseURL: configured.BaseURL, Username: configured.Username, Password: configured.Password}, native.IYUUOptions{APIKey: configured.APIKey, AutoCategory: configured.AutoCategory, Sequential: configured.Sequential, FirstLast: configured.FirstLast, Guard: guard})
	return client, kind, guard, err
}

// Torrent materials have the existing SDK transport ceiling. The 128KiB
// metadata ceiling remains unchanged for every other resource operation.
func originalHostDownloaderAddDecode(raw json.RawMessage, out any) error {
	if len(raw) == 0 || len(raw) > plugins.MaxPayloadBytes || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return bad("INVALID_PLUGIN_VALUE", "种子材料参数超过共享传输上限或无效")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return bad("INVALID_PLUGIN_VALUE", "种子材料参数无效")
	}
	return nil
}

func (a *App) originalHostDownloaderResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	switch method {
	case plugins.DownloaderConnectionCapability:
		return a.originalHostDownloaderConnection(ctx, scope, raw)
	case plugins.DownloaderDeleteFilesCapability:
		return a.originalHostDownloaderDeleteFiles(ctx, scope, raw)
	case plugins.DownloaderTrackersCapability:
		return a.originalHostDownloaderTrackers(ctx, scope, raw)
	case plugins.DownloaderListCapability:
		var request plugins.DownloaderListRequestV1
		if err := originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		client, kind, guard, err := a.originalHostDownloaderClient(ctx, scope, request.DownloaderID)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		body, err := client.Torrents(ctx, nil, false)
		if current := guard(ctx); current != nil {
			return nil, current
		}
		if err != nil {
			return nil, err
		}
		var rows []map[string]json.RawMessage
		if json.Unmarshal(body, &rows) != nil || rows == nil {
			return nil, bad("DOWNLOADER_INVALID_RESPONSE", "下载器列表响应无效")
		}
		key := "hash"
		if kind == "transmission" {
			key = "hashString"
		}
		hashes := []string{}
		for _, row := range rows {
			var hash string
			if json.Unmarshal(row[key], &hash) != nil || !plugins.DownloaderTaskHashValid(hash) {
				return nil, bad("DOWNLOADER_INVALID_RESPONSE", "下载器任务Hash无效")
			}
			hashes = append(hashes, strings.ToLower(hash))
		}
		tasks, err := originalDownloaderTaskProjection(kind, hashes, body)
		if err != nil {
			return nil, err
		}
		return originalHostDownloaderMarshal(plugins.DownloaderTasksV1{SchemaVersion: 1, DownloaderID: request.DownloaderID, Kind: kind, Tasks: tasks})
	case plugins.DownloaderCompletedCapability, plugins.DownloaderDownloadingCapability:
		var request plugins.DownloaderCompletedRequestV1
		if err := originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		if request.Validate() != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "已完成任务筛选无效")
		}
		return a.originalHostDownloaderFiltered(ctx, scope, request, method == plugins.DownloaderDownloadingCapability)
	case plugins.DownloaderAddCapability:
		var request plugins.DownloaderAddRequestV1
		if err := originalHostDownloaderAddDecode(raw, &request); err != nil {
			return nil, err
		}
		if request.Validate() != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "种子添加参数无效")
		}
		return a.originalHostDownloaderAdd(ctx, scope, request)
	case plugins.DownloaderControlCapability:
		var request plugins.DownloaderControlRequestV1
		if err := originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		if request.Validate() != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "下载器控制必须指定准确任务与受支持操作")
		}
		return a.originalHostDownloaderControl(ctx, scope, request)
	case plugins.DownloaderTagsCapability:
		var request plugins.DownloaderTagsRequestV1
		if err := originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		if request.Hash != "" && !plugins.DownloaderTaskHashValid(request.Hash) {
			return nil, bad("INVALID_PLUGIN_VALUE", "标签读取 Hash 无效")
		}
		return a.originalHostDownloaderTags(ctx, scope, request)
	case plugins.DownloaderFindByTagCapability:
		var request plugins.DownloaderFindByTagRequestV1
		if err := originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		if request.Validate() != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "标签轮询必须指定下载器和标签")
		}
		client, kind, guard, err := a.originalHostDownloaderMutationClient(ctx, scope, request.DownloaderID)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		if kind != "qbittorrent" {
			return nil, bad("DOWNLOADER_UNSUPPORTED", "标签轮询仅适用qBittorrent")
		}
		hash, err := client.PluginHostFindByTag(ctx, request.Tags, request.Status)
		if current := guard(ctx); current != nil {
			return nil, current
		}
		if err != nil {
			return nil, err
		}
		out := plugins.DownloaderFindByTagV1{SchemaVersion: 1, DownloaderID: request.DownloaderID}
		if hash != "" {
			out.Hash = &hash
		}
		return originalHostDownloaderMarshal(out)
	case plugins.DownloaderDeleteTagsCapability:
		var request plugins.DownloaderDeleteTagsRequestV1
		if err := originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		if len(request.Tags) == 0 {
			return nil, bad("INVALID_PLUGIN_VALUE", "必须指定要删除的标签定义")
		}
		client, kind, guard, err := a.originalHostDownloaderMutationClient(ctx, scope, request.DownloaderID)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		if kind != "qbittorrent" {
			return nil, bad("DOWNLOADER_UNSUPPORTED", "全局标签定义仅适用qBittorrent")
		}
		result, err := client.PluginHostDeleteTagDefinitions(ctx, request.Tags)
		if current := guard(ctx); current != nil {
			if result.Accepted || result.Outcome == "unknown" {
				return originalHostDownloaderMarshal(plugins.DownloaderMutationV1{SchemaVersion: 1, DownloaderID: request.DownloaderID, Accepted: result.Accepted, Outcome: result.Outcome, FailureCode: "CALL_CHANGED_AFTER_MUTATION"})
			}
			return nil, current
		}
		if err != nil {
			return nil, err
		}
		return originalHostDownloaderMarshal(plugins.DownloaderMutationV1{SchemaVersion: 1, DownloaderID: request.DownloaderID, Accepted: result.Accepted, Outcome: result.Outcome})
	}
	return nil, bad("PLUGIN_CAPABILITY_DENIED", "插件下载器资源未获授权")
}

func originalHostDownloaderMarshal(value any) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err == nil && len(raw) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "下载器响应超出共享传输上限")
	}
	return raw, err
}

func (a *App) originalHostDownloaderCompleted(ctx context.Context, scope originalBaseCallScope, request plugins.DownloaderCompletedRequestV1) (json.RawMessage, error) {
	return a.originalHostDownloaderFiltered(ctx, scope, request, false)
}
func (a *App) originalHostDownloaderFiltered(ctx context.Context, scope originalBaseCallScope, request plugins.DownloaderCompletedRequestV1, downloading bool) (json.RawMessage, error) {
	client, kind, guard, err := a.originalHostDownloaderClient(ctx, scope, request.DownloaderID)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	out := plugins.DownloaderTasksV1{SchemaVersion: 1, DownloaderID: request.DownloaderID, Kind: kind, Tasks: []plugins.DownloaderTaskV1{}}
	var hashes []string
	if request.Hashes != nil {
		hashes, _ = (plugins.DownloaderTasksRequestV1{DownloaderID: request.DownloaderID, Hashes: request.Hashes}).CanonicalSelection()
		if len(hashes) == 0 {
			return originalHostDownloaderMarshal(out)
		}
	}
	var body json.RawMessage
	if downloading {
		body, err = client.PluginHostDownloading(ctx, hashes, request.Tags)
	} else {
		body, err = client.PluginHostCompleted(ctx, hashes, request.Tags)
	}
	if current := guard(ctx); current != nil {
		return nil, current
	}
	if err != nil {
		return nil, err
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(body, &rows) != nil || rows == nil {
		return nil, bad("DOWNLOADER_INVALID_RESPONSE", "已完成任务响应无效")
	}
	// Preserve daemon order for enumeration, as get_completed_torrents does.
	// Projection's exact list is derived only after the fixed completion filter.
	selected := map[string]bool{}
	for _, hash := range hashes {
		selected[hash] = true
	}
	projectionHashes := []string{}
	filteredRows := []map[string]json.RawMessage{}
	key := "hash"
	if kind == "transmission" {
		key = "hashString"
	}
	for _, row := range rows {
		var hash string
		if json.Unmarshal(row[key], &hash) != nil || !plugins.DownloaderTaskHashValid(hash) {
			return nil, bad("DOWNLOADER_INVALID_RESPONSE", "已完成任务 Hash 无效")
		}
		hash = strings.ToLower(hash)
		if hashes != nil && !selected[hash] {
			continue
		}
		projectionHashes = append(projectionHashes, hash)
		filteredRows = append(filteredRows, row)
	}
	body, err = json.Marshal(filteredRows)
	if err == nil {
		out.Tasks, err = originalDownloaderTaskProjection(kind, projectionHashes, body)
	}
	if err != nil {
		return nil, err
	}
	return originalHostDownloaderMarshal(out)
}

func (a *App) originalHostDownloaderAdd(ctx context.Context, scope originalBaseCallScope, request plugins.DownloaderAddRequestV1) (json.RawMessage, error) {
	client, kind, guard, err := a.originalHostDownloaderMutationClient(ctx, scope, request.DownloaderID)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	if kind != "qbittorrent" && (request.UploadLimitBytes != nil || request.DownloadLimitBytes != nil) {
		return nil, bad("INVALID_PLUGIN_VALUE", "Transmission限速使用set_limits的KB/s字段")
	}
	ignoreCategory := true // fixed wrapper kwargs.pop default
	if request.IgnoreCategoryCheck != nil {
		ignoreCategory = *request.IgnoreCategoryCheck
	}
	spec := native.IYUUAddSpec{Torrent: request.Torrent, Magnet: request.Magnet, SavePath: request.SavePath, Tags: request.Tags, Category: request.Category, SkipChecking: request.SkipChecking, IgnoreCategoryCheck: ignoreCategory}
	// Reuse generic bytes/magnet transport only, with ordinary source defaults.
	// No legacy business selection, journal, ownership or queue is invoked.
	result, err := client.PluginHostAddMaterial(ctx, spec, request.Paused, request.UploadLimitBytes, request.DownloadLimitBytes, request.URL, request.Cookie)
	if current := guard(ctx); current != nil {
		if result.Accepted || result.Outcome == "unknown" {
			return originalHostDownloaderMarshal(plugins.DownloaderMutationV1{SchemaVersion: 1, DownloaderID: request.DownloaderID, Accepted: result.Accepted, Outcome: result.Outcome, Hash: result.Hash, AddedIDs: result.AddedIDs, FailureCode: "CALL_CHANGED_AFTER_MUTATION"})
		}
		return nil, current
	}
	if err != nil {
		if result.Outcome == "unknown" {
			return nil, bad("DOWNLOADER_OUTCOME_UNKNOWN", "种子添加已发送，结果未知；请先查询任务，勿自动重试")
		}
		return nil, err
	}
	return originalHostDownloaderMarshal(plugins.DownloaderMutationV1{SchemaVersion: 1, DownloaderID: request.DownloaderID, Accepted: result.Accepted, Outcome: result.Outcome, Hash: result.Hash, AddedIDs: result.AddedIDs})
}

func (a *App) originalHostDownloaderControl(ctx context.Context, scope originalBaseCallScope, request plugins.DownloaderControlRequestV1) (json.RawMessage, error) {
	client, kind, guard, err := a.originalHostDownloaderMutationClient(ctx, scope, request.DownloaderID)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	hashes, _ := (plugins.DownloaderTasksRequestV1{DownloaderID: request.DownloaderID, Hashes: request.Hashes}).CanonicalSelection()
	if kind != "qbittorrent" && (request.Action == "set_category" || request.Action == "reannounce" || request.Action == "remove_tags" || request.Action == "delete_tags") || kind == "qbittorrent" && request.Action == "set_limits" {
		return nil, bad("DOWNLOADER_UNSUPPORTED", "该下载器不支持此操作")
	}
	var result native.IYUUReceipt
	partial := false
	switch request.Action {
	case "set_limits":
		result, err = client.PluginHostTransmissionLimits(ctx, hashes[0], request.UploadLimitKB, request.DownloadLimitKB)
	case "start":
		result, err = client.Start(ctx, hashes)
	case "recheck":
		result, err = client.Recheck(ctx, hashes)
	case "pause", "reannounce":
		result, err = client.BrushFlowControl(ctx, request.Action, hashes, nil, nil, nil)
	case "remove_task":
		result, err = client.PluginHostRemoveTasks(ctx, hashes)
	case "set_tags", "remove_tags", "delete_tags":
		tags := request.Tags
		if kind == "transmission" {
			if len(tags) == 0 {
				return originalHostDownloaderMarshal(plugins.DownloaderMutationV1{SchemaVersion: 1, DownloaderID: request.DownloaderID, Outcome: "rejected"})
			}
			tags = []string{}
			seen := map[string]bool{}
			for _, tag := range append(append([]string{}, request.OriginalTags...), request.Tags...) {
				if !seen[tag] {
					tags = append(tags, tag)
					seen[tag] = true
				}
			}
		} else if request.OriginalTags != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "qBittorrent原标签由下载器保留，无需传入")
		}
		result, err = client.PluginHostTags(ctx, request.Action, hashes, tags)
	case "set_category":
		previousAccepted := false
		for _, hash := range hashes {
			result, err = client.SetCategory(ctx, hash, *request.Category)
			if err != nil || !result.Accepted {
				if previousAccepted {
					partial = true
					result.Accepted = false
					result.Outcome = "unknown"
				}
				break
			}
			previousAccepted = true
		}
	}
	if current := guard(ctx); current != nil {
		if result.Accepted || result.Outcome == "unknown" {
			return originalHostDownloaderMarshal(plugins.DownloaderMutationV1{SchemaVersion: 1, DownloaderID: request.DownloaderID, Accepted: result.Accepted, Outcome: result.Outcome, FailureCode: "CALL_CHANGED_AFTER_MUTATION"})
		}
		return nil, current
	}
	if partial {
		return originalHostDownloaderMarshal(plugins.DownloaderMutationV1{SchemaVersion: 1, DownloaderID: request.DownloaderID, Outcome: "unknown", FailureCode: "PARTIAL_MUTATION"})
	}
	if err != nil {
		if result.Outcome == "unknown" {
			return nil, bad("DOWNLOADER_OUTCOME_UNKNOWN", "下载器操作已发送，结果未知；请先查询任务，勿自动重试")
		}
		return nil, err
	}
	return originalHostDownloaderMarshal(plugins.DownloaderMutationV1{SchemaVersion: 1, DownloaderID: request.DownloaderID, Accepted: result.Accepted, Outcome: result.Outcome})
}

func (a *App) originalHostDownloaderTags(ctx context.Context, scope originalBaseCallScope, request plugins.DownloaderTagsRequestV1) (json.RawMessage, error) {
	client, kind, guard, err := a.originalHostDownloaderClient(ctx, scope, request.DownloaderID)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	tags := []string{}
	if kind == "qbittorrent" && request.Hash == "" {
		tags, err = client.BrushFlowTags(ctx)
	} else {
		if request.Hash == "" {
			return nil, bad("INVALID_PLUGIN_VALUE", "Transmission 标签读取必须指定任务 Hash")
		}
		var raw json.RawMessage
		raw, err = client.Torrents(ctx, []string{strings.ToLower(request.Hash)}, false)
		if err == nil {
			var rows []map[string]json.RawMessage
			if json.Unmarshal(raw, &rows) != nil {
				return nil, bad("DOWNLOADER_INVALID_RESPONSE", "任务标签响应无效")
			}
			key := "hash"
			if kind == "transmission" {
				key = "hashString"
			}
			for _, row := range rows {
				var hash string
				if json.Unmarshal(row[key], &hash) != nil || !strings.EqualFold(hash, request.Hash) {
					continue
				}
				if kind == "transmission" {
					if len(row["labels"]) > 0 && json.Unmarshal(row["labels"], &tags) != nil {
						return nil, bad("DOWNLOADER_INVALID_RESPONSE", "任务标签响应无效")
					}
					for i := range tags {
						tags[i] = legacyanime.Strip(tags[i])
					}
				} else {
					var text string
					if json.Unmarshal(row["tags"], &text) != nil {
						return nil, bad("DOWNLOADER_INVALID_RESPONSE", "任务标签响应无效")
					}
					if text != "" {
						for _, value := range strings.Split(text, ",") {
							tags = append(tags, strings.TrimSpace(value))
						}
					}
				}
			}
		}
	}
	if current := guard(ctx); current != nil {
		return nil, current
	}
	if err != nil {
		return nil, err
	}
	if tags == nil {
		tags = []string{}
	}
	return originalHostDownloaderMarshal(plugins.DownloaderTagsV1{SchemaVersion: 1, DownloaderID: request.DownloaderID, Tags: tags})
}
