package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"light-media/internal/directorysource"
	"light-media/internal/plugins"
)

// The native history is a projection of the committed execution plan/job. Read
// its stable ID after that commit; a preview never emits a completion event.
func (a *App) emitOriginalTransferResult(ctx context.Context, plan ImportPlan, file PlanFile, source originalTransferFileContext, success bool, message string, history any, refreshKey, refreshTarget string, provided ...map[string]any) {
	if len(source.Meta) == 0 || len(source.Media) == 0 {
		return
	}
	classifier, err := a.transferMediaClassifier(ctx)
	if err != nil {
		a.originalTransferEventError(ctx, err)
		return
	}
	fileitem, err := originalTransferSourceItem(source, file)
	if err != nil {
		a.originalTransferEventError(ctx, err)
		return
	}
	var fields map[string]any
	encoded, err := json.Marshal(fileitem)
	if err != nil || json.Unmarshal(encoded, &fields) != nil {
		a.originalTransferEventError(ctx, bad("PLUGIN_TRANSFER_CONTEXT", "整理事件的文件项无法读取"))
		return
	}
	item := originalTransferItemClass(fields)
	if item.Type == "dir" {
		var receipt importReceipt
		if a.Store.Get(ctx, "import_receipt", plan.ID+"/"+file.ID, &receipt) == nil && receipt.DirectoryStreamSize != nil {
			fileitem.Fields["size"], _ = json.Marshal(*receipt.DirectoryStreamSize)
		}
		item.BlurayKnown = true
		item.Bluray, err = a.originalTransferIsBluray(ctx, fields)
		if err != nil {
			a.originalTransferEventError(ctx, err)
			return
		}
		if len(provided) > 0 {
			if actual := sourceMap(provided[0]["fileitem"]); actual != nil {
				fileitem, err = plugins.NewModuleFileItem(actual)
				if err != nil {
					a.originalTransferEventError(ctx, err)
					return
				}
			}
		}
	}
	event := ""
	if media, _ := classifier.Media(item); media {
		event = "transfer"
	} else if classifier.Subtitle(item) {
		event = "transfer.subtitle"
	} else if classifier.Audio(item) {
		event = "transfer.audio"
	}
	if event == "" {
		return
	}
	if success {
		event += ".complete"
	} else {
		event += ".failed"
	}
	var target, dir any
	fileList, newList, failed := []string{}, []string{}, []string{}
	count, size := 0, int64(0)
	var errorMessage any
	if success && len(provided) == 0 {
		target, err = originalLocalFileItem(file.Target)
		if err != nil {
			a.originalTransferEventError(ctx, err)
			return
		}
		mediaRoot, rootErr := originalTransferMediaRoot(plan, file)
		if rootErr != nil {
			a.originalTransferEventError(ctx, rootErr)
			return
		}
		dir, err = originalLocalFileItem(mediaRoot)
		if err != nil {
			a.originalTransferEventError(ctx, err)
			return
		}
		fileList, newList = []string{file.Source}, []string{file.Target}
		count, size = 1, file.Size
	} else if !success {
		failed = []string{file.Source}
		errorMessage = message
	}
	var downloader, hash any
	if plan.IntentID != "" {
		var intent Intent
		if err := a.Store.Get(ctx, "intent", plan.IntentID, &intent); err == nil {
			// The source task inherits the downloader returned by download(),
			// frozen in download history, not the requested automatic selector
			// or the native host's stable configuration ID.
			snapshot, err := a.loadDownloadContext(ctx, "intent", intent.ID, intent.Hash)
			if err != nil {
				a.originalTransferEventError(ctx, err)
				return
			}
			if snapshot != nil && len(snapshot.OriginalDownloader) != 0 {
				downloader = snapshot.OriginalDownloader
			} else if intent.DownloaderID != "" {
				// Legacy native histories predate the frozen source projection.
				if configured, err := a.configuredDownloader(intent.DownloaderID); err == nil {
					downloader = configured.Name
				}
			}
			if intent.Hash != "" {
				hash = intent.Hash
			}
		}
	}
	result := map[string]any{"success": success, "fileitem": fileitem, "target_diritem": dir, "target_item": target, "transfer_type": originalTransferType(file.Method),
		"file_count": count, "file_list": fileList, "file_list_new": newList, "total_size": size, "fail_list": failed, "message": errorMessage,
		"need_scrape": success && importBool(plan.Scraping, true), "need_notify": true}
	if len(provided) > 0 {
		result = provided[0]
	}
	raw, err := json.Marshal(map[string]any{"fileitem": fileitem, "meta": source.Meta, "mediainfo": source.Media, "transferinfo": result, "downloader": downloader, "download_hash": hash, "transfer_history_id": history})
	if err != nil {
		a.originalTransferEventError(ctx, err)
		return
	}
	if refreshKey != "" && event == "transfer.complete" {
		a.enqueueOriginalTransferRefresh(ctx, raw, refreshKey, refreshTarget)
		return
	}
	a.broadcastOriginalPluginEvent(ctx, event, raw)
}

func (a *App) originalTransferEventError(ctx context.Context, err error) {
	a.reportOriginalPluginExecutionError(ctx, "system", "整理事件数据构造失败", map[string]any{"type": "event", "event_type": "transfer"}, err)
}

func originalTransferMetadataEvent(plan ImportPlan, file PlanFile, meta importMetadata, provided ...map[string]any) (json.RawMessage, error) {
	source, ok := meta.OriginalTransfer[file.ID]
	if !ok {
		return nil, nil
	}
	if len(provided) > 0 && provided[0] != nil {
		info := provided[0]
		item := sourceMap(info["target_diritem"])
		if item == nil {
			return nil, nil
		}
		path := strings.TrimSuffix(sourceText(item["path"]), "/")
		if sourceText(item["storage"]) != "local" || !importPath(path) || !within(meta.RootPath, path) {
			return nil, bad("REVIEW_ROOT_CHANGED", "插件刮削结果超出已授权本地媒体库")
		}
		return json.Marshal(map[string]any{"meta": source.Meta, "mediainfo": source.Media, "fileitem": item, "file_list": info["file_list_new"], "overwrite": false})
	}
	root, err := originalTransferMediaRoot(plan, file)
	if err != nil {
		return nil, err
	}
	if !within(meta.RootPath, root) {
		return nil, bad("REVIEW_ROOT_CHANGED", "刮削媒体根目录超出授权媒体库")
	}
	directory, err := originalLocalFileItem(root)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"meta": source.Meta, "mediainfo": source.Media, "fileitem": directory, "file_list": []string{file.Target}, "overwrite": false})
}

func originalTransferMediaRoot(plan ImportPlan, file PlanFile) (string, error) {
	if file.DirectoryDigest != "" {
		return file.Target, nil
	}
	if !importBool(plan.Renaming, true) {
		return filepath.Dir(file.Target), nil
	}
	format := originalNativeTransferNaming(plan.Media)
	root, err := directorysource.MediaRoot(&format, file.Target)
	if err != nil {
		return "", err
	}
	if root == nil {
		return "", bad("PLUGIN_EVENT_INVALID", "整理后的媒体根目录不匹配命名格式")
	}
	return *root, nil
}
