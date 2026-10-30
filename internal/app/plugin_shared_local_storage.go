package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"light-media/internal/plugins"
)

func originalSharedLocalStorageCapability(method string) bool {
	return method == plugins.LocalStorageExistsCapability || method == plugins.LocalStorageListCapability || method == plugins.LocalStorageDeleteCapability
}
func (a *App) originalSharedLocalStorageResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if !originalSharedLocalStorageCapability(method) || scope.validate == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "本地文件操作未获授权")
	}
	// The normal SDK frame limit applies, not the unrelated 128 KiB form limit.
	var in plugins.LocalStorageRequest
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(&in); e != nil || in.FileItem == nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "文件项必须是对象")
	}
	if e := d.Decode(new(any)); !errors.Is(e, io.EOF) {
		return nil, bad("INVALID_PLUGIN_VALUE", "文件项参数无效")
	}
	item, e := plugins.NewModuleFileItem(in.FileItem)
	if e != nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "文件项无效")
	}
	value, e := decodeSourceJSON(mustCleanSeedJSON(item))
	if e != nil {
		return nil, e
	}
	fields := sourceMap(value)
	if e = scope.validate(ctx); e != nil {
		return nil, e
	}
	storage := fields["storage"]
	if method == plugins.LocalStorageExistsCapability && fields["path"] == nil {
		return nil, bad("INVALID_PATH", "文件路径不能为 null")
	}
	// Explicit None selects no native provider, but StorageChain still dispatches
	// registered modules first. Keep their scalar/list results and callback args;
	// the absent native provider must never fall back to the local filesystem.
	if storage == nil {
		module := "list_files"
		kwargs := map[string]any{"fileitem": fields, "recursion": in.Recursion}
		if method == plugins.LocalStorageExistsCapability {
			module = "get_file_item"
			kwargs = map[string]any{"storage": nil, "path": filepath.Clean(sourceText(fields["path"]))}
		} else if method == plugins.LocalStorageDeleteCapability {
			module = "delete_file"
			delete(kwargs, "recursion")
		}
		result, e := a.originalMetadataStorage(ctx, module, kwargs, func() (any, error) {
			return nil, scope.validate(ctx)
		})
		if e != nil {
			return nil, e
		}
		if method == plugins.LocalStorageExistsCapability {
			return json.Marshal(originalModuleResultTruthy(result))
		}
		return result, nil
	}
	if storage != "local" {
		return nil, bad("UNSUPPORTED_STORAGE", "共享文件能力仅支持受管本地存储")
	}
	if e := plugins.ValidateLocalStoragePathEncoding(in.FileItem); e != nil {
		return nil, bad("INVALID_PATH", "文件路径编码无效")
	}
	path, isString := fields["path"].(string)
	if method == plugins.LocalStorageDeleteCapability && (!isString || path == "") {
		// The source no-path guard belongs to LocalStorage.delete, not the
		// StorageChain wrapper. Modules still observe the complete FileItem.
		return a.originalMetadataStorage(ctx, "delete_file", map[string]any{"fileitem": fields}, func() (any, error) {
			return false, scope.validate(ctx)
		})
	}
	if !isString {
		return nil, bad("INVALID_PATH", "文件路径不能为 null")
	}
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		path = "/"
	}
	if e = a.sharedLocalStorageAllowed(path); e != nil {
		return nil, e
	}
	if method == plugins.LocalStorageDeleteCapability {
		if within(path, a.Dir) {
			return nil, bad("FILE_PRIVATE_PROTECTED", "不能删除包含宿主私有数据的目录")
		}
		for _, root := range a.browserRoots() {
			if path == root.Path {
				return nil, bad("FILE_ROOT_PROTECTED", "不能删除受管根目录")
			}
		}
	}
	// Check the complete no-follow ancestor chain even when a module overrides
	// the native result. Module metadata must not expand the caller's authority.
	parent, _, e := sharedLocalStorageStat(path)
	if parent != nil {
		parent.Close()
	}
	if e != nil && !errors.Is(e, os.ErrNotExist) && !errors.Is(e, os.ErrPermission) && !errors.Is(e, unix.ENAMETOOLONG) {
		return nil, e
	}
	check := func() error {
		if e := scope.validate(ctx); e != nil {
			return e
		}
		return a.sharedLocalStorageAllowed(path)
	}
	if method == plugins.LocalStorageExistsCapability {
		raw, e := a.originalMetadataStorage(ctx, "get_file_item", map[string]any{"storage": "local", "path": filepath.Clean(path)}, func() (any, error) {
			if e := check(); e != nil {
				return nil, e
			}
			return sharedLocalStorageItem(path)
		})
		if e != nil {
			return nil, e
		}
		return json.Marshal(originalModuleResultTruthy(raw))
	}
	module := "list_files"
	kwargs := map[string]any{"fileitem": fields, "recursion": in.Recursion}
	if method == plugins.LocalStorageDeleteCapability {
		module = "delete_file"
		delete(kwargs, "recursion")
	}
	return a.originalMetadataStorage(ctx, module, kwargs, func() (any, error) {
		if e := check(); e != nil {
			return nil, e
		}
		if method == plugins.LocalStorageListCapability {
			// Module callbacks have completed. Lifecycle/settings writers use
			// pluginMu; stabilize that authority only during native read work.
			// No DB transaction or worker callback is held under this lock.
			a.pluginMu.Lock()
			defer a.pluginMu.Unlock()
			if e := check(); e != nil {
				return nil, e
			}
			result, readErr := a.sharedLocalStorageList(ctx, path, in.Recursion, ctx.Err)
			// No metadata is published if the exact call closed while reading.
			if e := check(); e != nil {
				return nil, e
			}
			return result, readErr
		}
		// Never hold lifecycle serialization while invoking a worker module. The
		// native effect then rechecks the exact caller and current roots under it.
		a.pluginMu.Lock()
		defer a.pluginMu.Unlock()
		if e := check(); e != nil {
			return nil, e
		}
		for _, root := range a.browserRoots() {
			if path == root.Path {
				return nil, bad("FILE_ROOT_PROTECTED", "不能删除受管根目录")
			}
		}
		return a.sharedLocalStorageDelete(ctx, path, check)
	})
}

func (a *App) sharedLocalStorageAllowed(path string) error {
	if within(a.Dir, path) {
		return bad("FILE_PRIVATE_PROTECTED", "共享文件能力不能访问宿主私有数据")
	}
	return a.originalMetadataAllowed(path)
}
