package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"golang.org/x/sys/unix"
	"light-media/internal/fileextensions"
	"light-media/internal/plugins"
	"light-media/internal/transferextensions"
	"light-media/internal/transferoverwrite"
)

type originalTransferStorage struct {
	definition plugins.StorageOperation
	object     *originalPluginInstance
}

func (a *App) originalSelectTransferStorage(ctx context.Context) (*originalTransferStorage, error) {
	event := &plugins.MutableEvent{Type: "storage.operation", Data: json.RawMessage(`{"storage":"local","storage_oper":null}`)}
	var selected *originalTransferStorage
	var prior json.RawMessage
	a.sendOriginalChainEventObserved(ctx, event, func(handler originalMutableEventHandler, event *plugins.MutableEvent) {
		var fields map[string]json.RawMessage
		if json.Unmarshal(event.Data, &fields) != nil {
			return
		}
		raw := fields["storage_oper"]
		if !originalBaseJSONTruthy(raw) {
			selected = nil
			prior = append(prior[:0], raw...)
			return
		}
		if selected != nil && originalBaseJSONEqual(raw, prior) {
			return
		}
		var definition plugins.StorageOperation
		if json.Unmarshal(raw, &definition) != nil || definition.Kind != "local-storage" || definition.InstanceID != handler.Instance || definition.Key == "" || len(definition.Key) > 255 || handler.object == nil {
			selected = nil
			prior = append(prior[:0], raw...)
			return
		}
		selected = &originalTransferStorage{definition: definition, object: handler.object}
		prior = append(prior[:0], raw...)
	})
	if !originalBaseJSONTruthy(event.Data) {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(event.Data, &fields) != nil {
		return nil, bad("PLUGIN_STORAGE_INVALID", "插件存储选择事件数据无效")
	}
	if !originalBaseJSONTruthy(fields["storage_oper"]) {
		return nil, nil
	}
	if selected == nil || !originalObjectStillLoaded(selected.object) {
		return nil, bad("PLUGIN_STORAGE_INVALID", "插件返回的本地存储对象无法绑定到当前实例")
	}
	return selected, nil
}

func (s *originalTransferStorage) call(ctx context.Context, operation string, item *plugins.ModuleFileItem, path string, name *string, publication string) (json.RawMessage, error) {
	method := s.definition.Methods[operation]
	if method == "" || !slices.Contains(s.object.manifest.Methods, method) || !originalObjectStillLoaded(s.object) {
		return nil, bad("PLUGIN_STORAGE_METHOD", "插件存储对象缺少可调用方法："+operation)
	}
	raw, err := json.Marshal(plugins.StorageOperationCall{Key: s.definition.Key, Operation: operation, FileItem: item, Path: path, NewName: name, PublicationPath: publication})
	if err != nil {
		return nil, err
	}
	result, err := s.object.callOriginalMethod(ctx, method, raw)
	return result.Result, err
}

type originalTransferStorageKey struct{}
type originalTransferStoragePair struct{ Source, Target *originalTransferStorage }

func originalTransferStorageFromContext(ctx context.Context) *originalTransferStoragePair {
	pair, _ := ctx.Value(originalTransferStorageKey{}).(*originalTransferStoragePair)
	return pair
}

func (a *App) originalPrepareTransferTarget(ctx context.Context, path string) error {
	pair := originalTransferStorageFromContext(ctx)
	if pair == nil || pair.Target == nil {
		return nil
	}
	raw, err := pair.Target.call(ctx, "get_folder", nil, filepath.Dir(path), nil, "")
	if err != nil {
		return err
	}
	if !originalModuleResultTruthy(raw) {
		return bad("PLUGIN_STORAGE_FOLDER", "插件存储未取得目标目录")
	}
	return nil
}

func (a *App) originalCheckTransferTargetAfterIntercept(ctx context.Context, path string, over bool) error {
	pair := originalTransferStorageFromContext(ctx)
	if pair == nil || pair.Target == nil {
		return nil
	}
	if _, err := os.Lstat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	raw, err := pair.Target.call(ctx, "get_item", nil, path, nil, "")
	if err != nil {
		return err
	}
	if !originalModuleResultTruthy(raw) {
		return nil
	}
	if !over {
		return bad("IMPORT_CONFLICT", "【local】"+path+" 已存在")
	}
	var item plugins.ModuleFileItem
	if json.Unmarshal(raw, &item) != nil {
		return bad("PLUGIN_STORAGE_INVALID", "插件存储目标信息无效")
	}
	var itemPath string
	if json.Unmarshal(item.Fields["path"], &itemPath) != nil || itemPath != path {
		return bad("REVIEW_OVERWRITE_TARGET", "插件删除目标与授权整理目标不同")
	}
	_, err = pair.Target.call(ctx, "delete", &item, path, nil, "")
	return err
}

func (a *App) originalStorageLatestCandidates(ctx context.Context, target string, extensions fileextensions.Snapshot, want transferoverwrite.Identity, classifier transferextensions.Classifier) ([]string, bool, error) {
	pair := originalTransferStorageFromContext(ctx)
	if pair == nil || pair.Target == nil {
		return nil, false, nil
	}
	parent := filepath.Dir(target)
	raw, err := pair.Target.call(ctx, "get_item", nil, parent, nil, "")
	if err != nil {
		return nil, true, err
	}
	if !originalModuleResultTruthy(raw) {
		return []string{}, true, nil
	}
	var item plugins.ModuleFileItem
	if json.Unmarshal(raw, &item) != nil {
		return nil, true, bad("PLUGIN_STORAGE_INVALID", "插件存储目录信息无效")
	}
	raw, err = pair.Target.call(ctx, "list", &item, parent, nil, "")
	if err != nil {
		return nil, true, err
	}
	if !originalModuleResultTruthy(raw) {
		return []string{}, true, nil
	}
	var items []*plugins.ModuleFileItem
	if json.Unmarshal(raw, &items) != nil || len(items) > 2000 {
		return nil, true, bad("PLUGIN_STORAGE_INVALID", "插件存储文件列表无效或超过读取边界")
	}
	paths := []string{}
	for _, item := range items {
		if item == nil {
			return nil, true, bad("PLUGIN_STORAGE_INVALID", "插件存储文件项无效")
		}
		var path, typ, extension string
		if json.Unmarshal(item.Fields["path"], &path) != nil || json.Unmarshal(item.Fields["type"], &typ) != nil {
			return nil, true, bad("PLUGIN_STORAGE_INVALID", "插件存储文件路径无效")
		}
		if path == target || typ != "file" {
			continue
		}
		if !importPath(path) || filepath.Dir(path) != parent {
			return nil, true, bad("REVIEW_OVERWRITE_TARGET", "插件存储版本文件不在当前授权目录")
		}
		if json.Unmarshal(item.Fields["extension"], &extension) != nil {
			return nil, true, bad("PLUGIN_STORAGE_INVALID", "插件存储扩展名无效")
		}
		candidate := transferextensions.PathFile(path)
		candidate.Extension = &extension
		media, _ := classifier.Media(candidate)
		if !media {
			continue
		}
		identity, err := a.overwritePathIdentity(ctx, path, extensions)
		if err != nil {
			return nil, true, err
		}
		if transferoverwrite.VersionMatches(want, identity) {
			paths = append(paths, path)
		}
	}
	return paths, true, nil
}

// Preserve the host's recoverable backup while calling the selected object's
// real delete on the original path. Source ignores delete's return value;
// retain a version if that call leaves the original file unchanged.
func (a *App) originalStorageQuarantineVersion(ctx context.Context, old directoryOverwriteTarget) (bool, error) {
	pair := originalTransferStorageFromContext(ctx)
	if pair == nil || pair.Target == nil {
		return true, quarantineDirectoryOverwrite(old)
	}
	parent, err := importDir(filepath.Dir(old.Path), false)
	if err != nil {
		return false, err
	}
	defer parent.Close()
	backup := filepath.Join(filepath.Dir(old.Path), old.Backup)
	if stamp, err := snapshotPath(backup); err == nil {
		if stamp != old.Snapshot {
			return false, bad("REVIEW_OVERWRITE_BACKUP", "覆盖暂存文件身份已变化")
		}
		if _, err := os.Lstat(old.Path); errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, bad("REVIEW_OVERWRITE_TARGET", "版本原路径出现另一文件")
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	stamp, err := snapshotPath(old.Path)
	if err != nil || stamp != old.Snapshot {
		return false, bad("REVIEW_OVERWRITE_TARGET", "版本文件身份已变化")
	}
	item, err := originalLocalFileItem(old.Path)
	if err != nil {
		return false, err
	}
	if err := unix.Linkat(int(parent.Fd()), filepath.Base(old.Path), int(parent.Fd()), old.Backup, 0); err != nil {
		return false, err
	}
	if err := parent.Sync(); err != nil {
		return false, err
	}
	_, callErr := pair.Target.call(ctx, "delete", item, old.Path, nil, "")
	actual, err := snapshotPath(old.Path)
	if errors.Is(err, os.ErrNotExist) {
		return true, callErr
	}
	if err == nil && actual == old.Snapshot {
		_ = unix.Unlinkat(int(parent.Fd()), old.Backup, 0)
		if callErr != nil {
			return false, callErr
		}
		return false, parent.Sync()
	}
	return false, bad("REVIEW_OVERWRITE_TARGET", "插件删除时版本文件发生变化，已保留暂存")
}
