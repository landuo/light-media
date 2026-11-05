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
	return method == plugins.StorageExistsCapability || method == plugins.StorageListCapability || method == plugins.StorageDeleteCapability
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
			a.writeOriginalPluginCallLog(scope.installation, scope.logCall, "ERROR", "storage_host_"+api.Code, "存储宿主调用失败："+api.Code)
		} else if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			a.writeOriginalPluginCallLog(scope.installation, scope.logCall, "ERROR", "storage_host_failed", "存储宿主文件操作失败")
		}
	}
	return result, err
}

func (a *App) originalHostStorageCall(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
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
	if err := a.browserMutationAllowed(ctx, path); err != nil {
		return false, err
	}
	for _, root := range a.browserRoots() {
		if filepath.Clean(root.Path) == path {
			return false, bad("FILE_PROTECTED", "不能删除授权根目录")
		}
	}
	before, err := browserManifest(ctx, path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if err = a.guardBrowserDownloaderSources(ctx, []string{path}); err != nil {
		return false, err
	}
	after, err := browserManifest(ctx, path)
	if err != nil || !sameBrowserManifest(before, after) {
		return false, bad("FILE_SNAPSHOT_CHANGED", "删除前文件发生变化")
	}
	parent, err := importDir(filepath.Dir(path), false)
	if err != nil {
		return false, err
	}
	defer parent.Close()
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return false, err
	}
	defer root.Close()
	parentInfo, err := parent.Stat()
	if err != nil {
		return false, err
	}
	rootInfo, err := root.Stat(".")
	if err != nil || !os.SameFile(parentInfo, rootInfo) {
		return false, bad("FILE_PARENT_CHANGED", "删除前父目录发生变化")
	}
	if err = scope.validate(ctx); err != nil {
		return false, err
	}
	release, err := a.beginWrite()
	if err != nil {
		return false, err
	}
	defer release()
	if err = root.RemoveAll(filepath.Base(path)); err != nil {
		return false, nil
	}
	_, err = root.Lstat(filepath.Base(path))
	return errors.Is(err, os.ErrNotExist), nil
}
