package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"light-media/internal/plugins"
)

// StorageChain ordinary wrappers used by metadata. Scalar/tuple returns stop
// dispatch; lists run the native provider and accumulate only its list values.
func (a *App) originalMetadataStorage(ctx context.Context, method string, kwargs map[string]any, native func() (any, error)) (json.RawMessage, error) {
	return a.originalMetadataStorageGroups(ctx, method, kwargs, native, nil)
}

func (a *App) originalMetadataStorageGroups(ctx context.Context, method string, kwargs map[string]any, native func() (any, error), groups []originalPluginModuleGroup) (json.RawMessage, error) {
	encoded, err := json.Marshal(kwargs)
	if err != nil {
		return nil, err
	}
	result, err := a.invokeOriginalPluginModuleGroups(ctx, method, nil, json.RawMessage(`[]`), encoded, nil, groups)
	if err != nil {
		return nil, err
	}
	core := func() (json.RawMessage, error) {
		value, err := native()
		var guard *APIError
		if errors.As(err, &guard) && !errors.Is(err, os.ErrPermission) {
			return nil, err
		}
		if err != nil && ctx.Err() == nil {
			a.reportOriginalPluginExecutionError(ctx, "system", "文件整理发生了错误", map[string]any{"type": "module", "module_id": "FileManagerModule", "module_name": "文件整理", "module_method": method}, err)
			return json.RawMessage(`null`), nil
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
	if originalModuleResultEmpty(result) {
		return core()
	}
	var list []json.RawMessage
	if json.Unmarshal(result, &list) == nil && list != nil {
		value, err := core()
		if err != nil {
			return nil, err
		}
		var extra []json.RawMessage
		if json.Unmarshal(value, &extra) == nil {
			list = append(list, extra...)
		}
		return json.Marshal(list)
	}
	return result, nil
}

func originalMetadataStorageItem(raw json.RawMessage) (map[string]any, error) {
	if !originalModuleResultTruthy(raw) {
		return nil, nil
	}
	if _, ok := plugins.DecodeModuleResultBytes(raw); ok {
		return nil, bad("PLUGIN_MODULE_RESULT_INVALID", "存储模块没有返回文件项")
	}
	value, err := decodeSourceJSON(raw)
	if err != nil || sourceMap(value) == nil {
		return nil, bad("PLUGIN_MODULE_RESULT_INVALID", "存储模块没有返回文件项")
	}
	if _, tuple := sourceMap(value)[plugins.ModuleTupleKey]; tuple && len(sourceMap(value)) == 1 {
		return nil, bad("PLUGIN_MODULE_RESULT_INVALID", "存储模块没有返回文件项")
	}
	return sourceMap(value), nil
}

func (a *App) originalMetadataFileItemResult(ctx context.Context, storage, path string) (json.RawMessage, error) {
	return a.originalMetadataStorage(ctx, "get_file_item", map[string]any{"storage": storage, "path": filepath.Clean(path)}, func() (any, error) {
		return a.originalMetadataLocalItem(storage, path)
	})
}

func (a *App) originalMetadataGetFileItem(ctx context.Context, storage, path string) (map[string]any, error) {
	raw, err := a.originalMetadataFileItemResult(ctx, storage, path)
	if err != nil {
		return nil, err
	}
	return originalMetadataStorageItem(raw)
}

func (a *App) originalMetadataParent(ctx context.Context, item, supplied map[string]any) (map[string]any, error) {
	if len(supplied) != 0 {
		return supplied, nil
	}
	raw, err := a.originalMetadataStorage(ctx, "get_parent_item", map[string]any{"fileitem": item}, func() (any, error) {
		return a.originalMetadataLocalItem(sourceText(item["storage"]), filepath.Dir(strings.TrimSuffix(sourceText(item["path"]), "/")))
	})
	if err != nil {
		return nil, err
	}
	return originalMetadataStorageItem(raw)
}

func (a *App) originalMetadataList(ctx context.Context, item map[string]any) ([]map[string]any, error) {
	raw, err := a.originalMetadataStorage(ctx, "list_files", map[string]any{"fileitem": item, "recursion": false}, func() (any, error) {
		return a.originalMetadataNativeList(ctx, item)
	})
	if err != nil || !originalModuleResultTruthy(raw) {
		return nil, err
	}
	var entries []map[string]any
	if json.Unmarshal(raw, &entries) != nil {
		return nil, bad("PLUGIN_MODULE_RESULT_INVALID", "存储模块没有返回文件列表")
	}
	return entries, nil
}

func (a *App) originalMetadataNativeList(ctx context.Context, item map[string]any) ([]map[string]any, error) {
	if item["storage"] != "local" {
		return nil, bad("UNSUPPORTED_STORAGE", "当前只支持本地存储刮削")
	}
	path := strings.TrimSuffix(sourceText(item["path"]), "/")
	if err := a.originalMetadataAllowed(path); err != nil {
		return nil, err
	}
	dir, err := importDir(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return []map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(entries))
	// LocalStorage.list emits directories first, then files, retaining the
	// filesystem's iteration order in each pass (SystemUtils helpers).
	for _, directory := range []bool{true, false} {
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() != directory || directory && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "@eaDir") || browserInternal(filepath.Join(path, entry.Name())) {
				continue
			}
			child, err := a.originalMetadataLocalItem("local", filepath.Join(path, entry.Name()))
			if err != nil {
				return nil, err
			}
			if child != nil {
				out = append(out, child)
			}
		}
	}
	return out, nil
}
