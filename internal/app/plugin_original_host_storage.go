package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"light-media/internal/plugins"
)

func originalHostStorageCapability(method string) bool {
	return method == plugins.StorageExistsCapability || method == plugins.StorageListCapability || method == plugins.StorageDeleteCapability || method == plugins.StorageLocalReadGrantCapability || method == originalScrapeCleanRemoveCapability
}

func originalHostNestedModuleScope(scope originalBaseCallScope) error {
	if scope.object == nil {
		return bad("PLUGIN_CALL_CLOSED", "插件对象不可用")
	}
	if !slices.Contains(scope.object.manifest.Methods, "get_module") {
		return nil
	}
	return originalHostRequireConcurrent(scope)
}

func originalHostRequireConcurrent(scope originalBaseCallScope) error {
	if scope.object == nil {
		return bad("PLUGIN_CALL_CLOSED", "插件对象不可用")
	}
	scope.object.sessionMu.Lock()
	concurrent := scope.object.session != nil && scope.object.session.SupportsConcurrent()
	scope.object.sessionMu.Unlock()
	if !concurrent {
		return bad("PLUGIN_CONCURRENT_REQUIRED", "宿主扩展重入需要并发worker")
	}
	return nil
}

func (a *App) originalHostStorageResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	result, err := a.originalHostStorageCall(ctx, scope, method, raw)
	if err != nil {
		var api *APIError
		if errors.As(err, &api) {
			a.writeOriginalPluginCallLog(scope.object, scope.installation, scope.logCall, "ERROR", "storage_host_"+api.Code, "存储宿主调用失败："+api.Code)
		} else if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			a.writeOriginalPluginCallLog(scope.object, scope.installation, scope.logCall, "ERROR", "storage_host_failed", "存储宿主文件操作失败")
		}
	}
	return result, err
}

func (a *App) originalHostStorageCall(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if method == plugins.StorageLocalReadGrantCapability {
		return a.originalHostLocalReadGrant(ctx, scope, raw)
	}
	if method == originalScrapeCleanRemoveCapability {
		return a.originalHostScrapeCleanResource(ctx, scope, raw)
	}
	var request plugins.StorageFilesRequest
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	if request.FileItem == nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "存储操作需要文件项")
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	if err := originalHostNestedModuleScope(scope); err != nil {
		return nil, err
	}
	value, err := decodeSourceJSON(request.RawItem())
	if err != nil {
		return nil, err
	}
	item := sourceMap(value)
	path := strings.TrimSuffix(sourceText(item["path"]), "/")
	if item["storage"] != "local" {
		return nil, bad("UNSUPPORTED_STORAGE", "当前仅支持本地存储")
	}
	if err = a.originalMetadataAllowed(path); err != nil {
		return nil, err
	}
	var result json.RawMessage
	switch method {
	case plugins.StorageExistsCapability:
		result, err = a.originalMetadataFileItemResult(ctx, "local", path)
		if err == nil {
			result, err = json.Marshal(originalModuleResultTruthy(result))
		}
	case plugins.StorageListCapability:
		result, err = a.originalMetadataStorage(ctx, "list_files", map[string]any{"fileitem": item, "recursion": request.Recursion}, func() (any, error) {
			return a.originalHostLocalList(ctx, item, request.Recursion)
		})
	case plugins.StorageDeleteCapability:
		result, err = a.originalMetadataStorage(ctx, "delete_file", map[string]any{"fileitem": item}, func() (any, error) {
			return a.originalHostLocalDelete(ctx, scope, path)
		})
	default:
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "未知存储资源方法")
	}
	if err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	if len(result) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "存储响应超过上限")
	}
	return result, nil
}

func (a *App) originalHostLocalList(ctx context.Context, item map[string]any, recursive bool) ([]map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := strings.TrimSuffix(sourceText(item["path"]), "/")
	info, err := browserStat(path)
	if errors.Is(err, os.ErrNotExist) {
		return []map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !os.FileMode(info.Mode).IsDir() {
		file, err := a.originalMetadataLocalItem("local", path)
		if err != nil {
			return nil, err
		}
		return []map[string]any{file}, nil
	}
	items, err := a.originalMetadataNativeList(ctx, item)
	if err != nil || !recursive {
		return items, err
	}
	result := []map[string]any{}
	for _, entry := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry["type"] == "dir" {
			children, err := a.originalHostLocalList(ctx, entry, true)
			if err != nil {
				return nil, err
			}
			result = append(result, children...)
		} else {
			result = append(result, entry)
		}
	}
	return result, nil
}

func (a *App) originalHostLocalDelete(ctx context.Context, scope originalBaseCallScope, path string) (bool, error) {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	if err := a.originalMetadataAllowed(path); err != nil {
		return false, err
	}
	if err := a.browserMutationAllowedWithReferences(ctx, path, false); err != nil {
		return false, err
	}
	for _, root := range a.browserRoots() {
		if filepath.Clean(root.Path) == path {
			return false, bad("FILE_PROTECTED", "不能删除授权根目录")
		}
	}
	before, err := browserManifestWithHardlinkGuard(ctx, path, false)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if err = a.guardBrowserDownloaderSources(ctx, []string{path}); err != nil {
		return false, err
	}
	after, err := browserManifestWithHardlinkGuard(ctx, path, false)
	if err != nil || !sameBrowserManifest(before, after) {
		return false, bad("FILE_SNAPSHOT_CHANGED", "删除前文件发生变化")
	}
	return a.originalHostLocalRemove(ctx, scope, path, after)
}
