package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var originalMetadataScrapingLock sync.Mutex

// This narrow set of calls retains MediaChain.scrape_metadata_event's actual
// ordering. Its local implementation uses the existing configured path roots;
// it does not register assets, scan libraries or create another work queue.
type originalMetadataEventStorage struct {
	getItem     func(map[string]any) (bool, error)
	getFileItem func(string, string) (map[string]any, error)
	getParent   func(map[string]any) (map[string]any, error)
	isBluray    func(map[string]any) (bool, error)
	scrape      func(map[string]any) error
}

func walkOriginalMetadataEvent(data map[string]any, storage originalMetadataEventStorage) error {
	item := sourceMap(data["fileitem"])
	if len(item) == 0 {
		return nil
	}
	present, err := storage.getItem(item)
	if err != nil || !present {
		return err
	}
	call := func(item map[string]any, init bool, extra map[string]any) error {
		kwargs := map[string]any{"fileitem": item, "mediainfo": data["mediainfo"], "init_folder": init, "overwrite": false}
		if value, ok := data["overwrite"]; ok {
			kwargs["overwrite"] = value
		}
		for key, value := range extra {
			kwargs[key] = value
		}
		return storage.scrape(kwargs)
	}
	if item["type"] == "file" {
		parent, err := storage.getParent(item)
		if err != nil {
			return err
		}
		return call(item, true, map[string]any{"parent": parent})
	}
	var files []string
	if value := data["file_list"]; sourceSubscriptionTruth(value) {
		seen := map[string]bool{}
		list, ok := value.([]any)
		if !ok {
			return bad("PLUGIN_EVENT_INVALID", "刮削文件列表必须是路径数组")
		}
		for _, entry := range list {
			path, ok := entry.(string)
			if !ok {
				return bad("PLUGIN_EVENT_INVALID", "刮削文件路径必须是字符串")
			}
			if !seen[path] {
				files, seen[path] = append(files, path), true
			}
		}
	}
	if len(files) == 0 {
		return call(item, true, map[string]any{"meta": data["meta"]})
	}
	bluray, err := storage.isBluray(item)
	if err != nil {
		return err
	}
	if bluray {
		return call(item, true, map[string]any{"recursive": false})
	}
	root := filepath.Clean(sourceText(item["path"]))
	dirs := map[string]bool{root: true}
	for _, file := range files {
		for parent := filepath.Dir(file); parent != root && within(root, parent); parent = filepath.Dir(parent) {
			dirs[parent] = true
		}
	}
	ordered := make([]string, 0, len(dirs))
	for path := range dirs {
		ordered = append(ordered, path)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := strings.Count(ordered[i], "/"), strings.Count(ordered[j], "/")
		return a < b || a == b && ordered[i] < ordered[j]
	})
	for _, path := range ordered {
		child, err := storage.getFileItem(sourceText(item["storage"]), path)
		if err != nil {
			return err
		}
		if child != nil {
			if err = call(child, true, map[string]any{"recursive": false}); err != nil {
				return err
			}
		}
	}
	sort.Strings(files)
	for _, path := range files {
		child, err := storage.getFileItem(sourceText(item["storage"]), path)
		if err != nil {
			return err
		}
		if child != nil {
			if err = call(child, false, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *App) originalMetadataAllowed(path string) error {
	if !importPath(path) || browserInternal(path) {
		return bad("INVALID_PATH", "刮削路径不在当前允许目录内")
	}
	for _, root := range a.browserRoots() {
		if within(root.Path, path) {
			return nil
		}
	}
	return bad("INVALID_PATH", "刮削路径不在当前允许目录内")
}

func (a *App) originalMetadataLocalItem(storage, path string) (map[string]any, error) {
	if storage != "local" {
		return nil, bad("UNSUPPORTED_STORAGE", "当前只支持本地存储刮削")
	}
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		path = "/"
	}
	if err := a.originalMetadataAllowed(path); err != nil {
		return nil, err
	}
	item, err := originalLocalFileItem(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	value, err := decodeSourceJSON(raw)
	return sourceMap(value), err
}

func (a *App) consumeOriginalCoreMetadataScrape(ctx context.Context, raw json.RawMessage) error {
	value, err := decodeSourceJSON(raw)
	if err != nil {
		return err
	}
	if !sourceSubscriptionTruth(value) {
		return nil
	}
	data := sourceMap(value)
	if data == nil {
		return bad("PLUGIN_EVENT_INVALID", "刮削事件数据必须是对象")
	}
	originalMetadataScrapingLock.Lock()
	defer originalMetadataScrapingLock.Unlock()
	get := func(storage, path string) (map[string]any, error) {
		return a.originalMetadataGetFileItem(ctx, storage, path)
	}
	return walkOriginalMetadataEvent(data, originalMetadataEventStorage{
		getItem: func(item map[string]any) (bool, error) {
			actual, err := a.originalMetadataFileItemResult(ctx, sourceText(item["storage"]), sourceText(item["path"]))
			return originalModuleResultTruthy(actual), err
		},
		getFileItem: get,
		getParent: func(item map[string]any) (map[string]any, error) {
			return a.originalMetadataParent(ctx, item, nil)
		},
		isBluray: func(item map[string]any) (bool, error) {
			return a.originalMetadataBluray(ctx, item)
		},
		scrape: func(kwargs map[string]any) error {
			return a.scrapeOriginalLocalMetadata(ctx, kwargs)
		},
	})
}

func (a *App) originalMetadataBluray(ctx context.Context, item map[string]any) (bool, error) {
	if item["type"] != "dir" {
		return false, nil
	}
	for _, name := range []string{"BDMV", "CERTIFICATE"} {
		child, err := a.originalMetadataFileItemResult(ctx, sourceText(item["storage"]), filepath.Join(sourceText(item["path"]), name))
		if err != nil {
			return false, err
		}
		if originalModuleResultTruthy(child) {
			return true, nil
		}
	}
	return false, nil
}

func (a *App) validateOriginalAssetMetadataEvent(asset Asset, raw json.RawMessage) error {
	value, err := decodeSourceJSON(raw)
	if err != nil || sourceMap(value) == nil {
		return bad("REVIEW_METADATA_CONTEXT", "整理刮削上下文无效")
	}
	fields := sourceMap(value)
	item, info := sourceMap(fields["fileitem"]), sourceMap(fields["mediainfo"])
	root, err := a.importRoot(asset.RootID)
	if err != nil {
		return err
	}
	path := strings.TrimSuffix(sourceText(item["path"]), "/")
	if item["storage"] != "local" || !importPath(path) || !within(root.Path, path) || !within(path, asset.Path) {
		return bad("REVIEW_METADATA_CONTEXT", "整理刮削目录不属于当前资产")
	}
	kind := "电影"
	if asset.Media.MediaType == "tv" {
		kind = "电视剧"
	}
	if info["type"] != kind || sourceNumberString(info["tmdb_id"]) != strconv.FormatInt(asset.Media.ID, 10) {
		return bad("REVIEW_METADATA_CONTEXT", "整理刮削影视身份与当前资产不符")
	}
	files, ok := fields["file_list"].([]any)
	if !ok {
		return bad("REVIEW_METADATA_CONTEXT", "整理刮削文件清单缺失")
	}
	owned := false
	stamp, err := snapshotTransferPath(asset.Path)
	if err != nil {
		return err
	}
	for _, value := range files {
		file, ok := value.(string)
		if !ok || !importPath(file) || !within(path, file) || !within(root.Path, file) {
			return bad("REVIEW_METADATA_CONTEXT", "整理刮削文件清单越界")
		}
		owned = owned || file == asset.Path || stamp.Directory != "" && within(asset.Path, file)
	}
	// An empty Blu-ray directory still takes the original root-only scrape
	// branch. Native tree ownership is checked before consuming the event.
	owned = owned || stamp.Directory != "" && len(files) == 0 && path == asset.Path
	if !owned {
		return bad("REVIEW_METADATA_CONTEXT", "整理刮削文件清单没有当前资产")
	}
	return nil
}
