package app

import (
	"context"
	"encoding/json"
)

// StorageChain.download_file is also used by file details and image preview.
// The existing browser route has already authorized and opened the requested
// file. A returned Path crosses the Go SDK as an absolute UTF-8 JSON string.
func (a *App) originalBrowserDownload(ctx context.Context, path string) (string, error) {
	item, err := originalLocalFileItem(path)
	if err != nil {
		return "", err
	}
	result, err := a.originalMetadataStorage(ctx, "download_file", map[string]any{"fileitem": item, "path": nil}, func() (any, error) {
		// Fixed LocalStorage.download ignores the optional destination.
		return path, nil
	})
	if err != nil {
		return "", err
	}
	if !originalModuleResultTruthy(result) {
		return "", &APIError{"PLUGIN_DOWNLOAD_FAILED", "存储插件未返回可下载文件", 500}
	}
	var target string
	if json.Unmarshal(result, &target) != nil || !importPath(target) {
		return "", &APIError{"PLUGIN_DOWNLOAD_INVALID", "存储插件未返回有效的本地文件路径", 500}
	}
	// An override must obey the same local content boundary as the original
	// request; installing a module does not grant reads outside those roots.
	for _, root := range a.browserRoots() {
		if _, err := a.browserRoot(root.ID, target); err == nil {
			return target, nil
		}
	}
	return "", bad("INVALID_PATH", "插件下载路径不在当前允许目录内")
}
