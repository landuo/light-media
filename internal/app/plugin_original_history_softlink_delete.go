package app

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"light-media/internal/plugins"
)

// Extend the same sealed SourceOperation envelope. Ordinary parent cleanup can
// still decode its embedded browserOperation; generic browser admissions remain
// unchanged and never acquire permission to follow a symlink.
type historySoftlinkDeleteOperation struct {
	browserOperation
	SoftlinkDelete *historySoftlinkDeleteState `json:"softlink_delete,omitempty"`
}
type historySoftlinkDeleteState struct {
	Proof         *importSoftlinkProof `json:"proof"`
	Snapshot      importSnapshot       `json:"snapshot"`
	Returned      bool                 `json:"returned"`
	Result        json.RawMessage      `json:"result,omitempty"`
	DispatchError *APIError            `json:"dispatch_error,omitempty"`
	NativeStarted bool                 `json:"native_started,omitempty"`
}

func (a *App) assetSoftlinkDeleteItem(ctx context.Context, assetID, path string) (*plugins.ModuleFileItem, error) {
	// Asset ownership outlives a record-only history deletion. Resolve an exact
	// execution (not only the visible current-history row), then check its asset.
	history, err := a.scanTransferHistoryMP(a.Store.reader().QueryRowContext(ctx, `SELECT `+transferHistoryMPColumns+` FROM transferhistory WHERE dest=? AND json_extract(src_fileitem,'$.asset_id')=? ORDER BY id DESC LIMIT 1`, path, assetID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var plan ImportPlan
	if err = a.Store.Get(ctx, "import_plan", history.PlanID, &plan); err != nil {
		return nil, err
	}
	file, _, _, err := a.historyPlannedFile(ctx, plan, history)
	if err != nil || file.AssetID != assetID || file.Target != path {
		return nil, bad("REVIEW_HISTORY_DELETE", "原文件项与资产执行归属不同")
	}
	return a.originalHistoryFrozenItem(ctx, history, false)
}

func historySoftlinkManifest(file assetRemovalFile) ([]browserStamp, error) {
	actual, err := snapshotOwnedTransferPath(file.Path, file.Snapshot, file.Softlink, false)
	if err != nil || actual != file.Snapshot {
		return nil, bad("FILE_SNAPSHOT_CHANGED", "删除许可中的软链接条目已变化")
	}
	paths := []string{file.Path}
	if file.Softlink.Directory != nil {
		for rel := range file.Softlink.Directory.Directories {
			paths = append(paths, filepath.Join(file.Path, rel))
		}
		for rel := range file.Softlink.Directory.Members {
			paths = append(paths, filepath.Join(file.Path, rel))
		}
	}
	sort.Strings(paths)
	manifest := make([]browserStamp, 0, len(paths))
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return nil, bad("REVIEW_HISTORY_DELETE", "受管条目缺少本地物理身份")
		}
		manifest = append(manifest, browserStamp{Path: path, Device: uint64(stat.Dev), Inode: uint64(stat.Ino), Size: info.Size(), ModNS: info.ModTime().UnixNano(), Mode: uint32(info.Mode())})
	}
	if after, err := snapshotOwnedTransferPath(file.Path, file.Snapshot, file.Softlink, false); err != nil || after != actual {
		return nil, bad("FILE_SNAPSHOT_CHANGED", "软链接删除清单冻结期间条目已变化")
	}
	return manifest, nil
}

// All surviving entries must still belong to the original permit, including
// ordinary metadata files explicitly incorporated by a metadata-write journal.
func historySoftlinkDeleteEffect(file assetRemovalFile) (string, error) {
	inspect := func(path string) (bool, bool, error) {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return false, false, nil
		} else if err != nil {
			return false, false, err
		}
		actual, err := snapshotOwnedTransferPath(path, file.Snapshot, file.Softlink, false)
		if err != nil {
			return true, false, err
		}
		return true, actual == file.Snapshot, nil
	}
	source, sourceFull, err := inspect(file.Path)
	if err != nil {
		return "", err
	}
	quarantine, quarantineFull, err := inspect(filepath.Join(filepath.Dir(file.Path), file.Quarantine))
	if err != nil {
		return "", err
	}
	if source && quarantine {
		return "", bad("FILE_SNAPSHOT_CHANGED", "删除后原路径与暂存路径同时存在")
	}
	if quarantine {
		if !quarantineFull && !file.DirectoryRemovalStarted {
			return "", bad("FILE_SNAPSHOT_CHANGED", "暂存目录成员缺失且尚未开始精确删除")
		}
		return "quarantine", nil
	}
	if source {
		if !sourceFull {
			return "partial", nil
		}
		return "source", nil
	}
	return "gone", nil
}

func (a *App) originalHistorySoftlinkDelete(ctx context.Context, file *assetRemovalFile, parent *os.File, persist func() error, frozen *plugins.ModuleFileItem) error {
	var operation historySoftlinkDeleteOperation
	if file.SourceOperation != "" {
		sealed, err := base64.StdEncoding.DecodeString(file.SourceOperation)
		if err != nil || a.Store.Unseal(sealed, &operation) != nil || operation.SoftlinkDelete == nil || len(operation.Items) != 1 || operation.Action != "delete" || operation.Items[0].Source != file.Path || operation.Items[0].Target != filepath.Join(filepath.Dir(file.Path), file.Quarantine) || operation.SoftlinkDelete.Snapshot != file.Snapshot || !sameLifecycleSoftlinkProof(operation.SoftlinkDelete.Proof, file.Softlink) {
			return bad("REVIEW_HISTORY_DELETE", "软链接删除封存操作与归属证明不同")
		}
		if operation.State == "rejected" && !operation.Items[0].Done {
			if actual, err := snapshotOwnedTransferPath(file.Path, file.Snapshot, file.Softlink, false); err != nil || actual != file.Snapshot {
				return bad("FILE_SNAPSHOT_CHANGED", "删除被保留后原条目已变化")
			}
			operation = historySoftlinkDeleteOperation{}
		} else if operation.Items[0].SourceModuleStarted && !operation.SoftlinkDelete.Returned {
			return bad("REVIEW_HISTORY_DELETE", "软链接删除扩展已调用但结果未封存，未重复调用")
		}
	}
	if operation.ID == "" {
		manifest, err := historySoftlinkManifest(*file)
		if err != nil {
			return err
		}
		parentStamp, err := browserStat(filepath.Dir(file.Path))
		if err != nil {
			return err
		}
		var root browserRoot
		for _, candidate := range a.browserRoots() {
			if within(candidate.Path, parentStamp.Path) && len(candidate.Path) > len(root.Path) {
				root = candidate
			}
		}
		if root.ID == "" {
			return bad("FILE_ROOT_REJECTED", "软链接删除路径不在当前允许目录内")
		}
		item := frozen
		if item != nil {
			item = item.Clone()
		} else {
			kind := "file"
			if file.Softlink.Directory != nil {
				kind = "dir"
			}
			item, err = plugins.NewModuleFileItem(originalTransferPreviewItem(file.Path, kind, file.Softlink.SourceSnapshot.Size))
			if err != nil {
				return err
			}
		}
		var fields map[string]any
		raw, _ := json.Marshal(item)
		if err = json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		wirePath := sourceText(fields["path"])
		if sourceText(fields["storage"]) != "local" || filepath.Clean(wirePath) != file.Path {
			return bad("REVIEW_HISTORY_DELETE", "原文件项与已许可删除路径不符")
		}
		if file.Softlink.Directory != nil {
			bluray, err := a.originalTransferIsBluray(ctx, fields)
			if err != nil {
				return err
			}
			if !bluray {
				for _, boundary := range a.browserRoots() {
					if boundary.Path == file.Path {
						file.DirectoryRetained = true
						return persist()
					}
				}
				extensions, err := a.historyDeleteExtensions(ctx)
				if err != nil {
					return err
				}
				prune, err := a.historyCanPruneDirectory(ctx, fields, historyAssociatedDirectory(a.Settings(), file.Path), extensions)
				if err != nil {
					return err
				}
				file.DirectoryRetained = !prune
				if file.DirectoryRetained {
					return persist()
				}
				file.DirectoryParentOnly = true
			}
		}
		operation = historySoftlinkDeleteOperation{browserOperation: browserOperation{ID: id("historyfile_"), Root: root, Action: "delete", Parent: parentStamp, State: "executing", CreatedAt: now(), Items: []browserOperationItem{{Source: file.Path, Target: filepath.Join(filepath.Dir(file.Path), file.Quarantine), Manifest: manifest, OriginalFileItem: item}}}, SoftlinkDelete: &historySoftlinkDeleteState{Proof: file.Softlink, Snapshot: file.Snapshot}}
	}
	save := func() error {
		sealed, err := a.Store.Seal(operation)
		if err != nil {
			return err
		}
		file.SourceOperation = base64.StdEncoding.EncodeToString(sealed)
		return persist()
	}
	item, state := &operation.Items[0], operation.SoftlinkDelete
	parentInfo, parentErr := parent.Stat()
	if parentErr != nil {
		return parentErr
	}
	parentIdentity, parentErr := originalDirectoryIdentity(parentInfo)
	if parentErr != nil || parentIdentity.Device != operation.Parent.Device || parentIdentity.Inode != operation.Parent.Inode {
		return bad("FILE_PARENT_CHANGED", "删除描述符与已冻结父目录不同")
	}
	if !state.Returned {
		groups, err := a.originalPluginModules(ctx, "")
		if err != nil {
			return err
		}
		item.SourceModuleStarted = true
		if err = save(); err != nil {
			return err
		}
		raw, dispatchErr := a.originalMetadataStorageGroups(ctx, "delete_file", map[string]any{"fileitem": item.OriginalFileItem}, func() (any, error) {
			state.NativeStarted = true
			if err := save(); err != nil {
				return nil, err
			}
			effect, err := historySoftlinkDeleteEffect(*file)
			if err != nil {
				return false, nil
			}
			if effect == "gone" || effect == "quarantine" {
				return true, nil
			}
			if effect != "source" {
				return false, nil
			}
			if err = a.quarantineHistorySoftlink(ctx, file, parent); err != nil {
				return false, nil
			}
			return true, nil
		}, groups)
		state.Result, state.Returned = raw, true
		if dispatchErr != nil {
			var api *APIError
			if errors.As(dispatchErr, &api) {
				state.DispatchError = api
			} else {
				state.DispatchError = bad("REVIEW_HISTORY_DELETE", "软链接删除扩展返回错误")
			}
		}
		// Persist the actual returned value before classifying its physical effect.
		if err = save(); err != nil {
			return err
		}
	}
	currentParent, err := browserStat(operation.Parent.Path)
	if err != nil || !sameBrowserIdentity(currentParent, operation.Parent) {
		return bad("FILE_PARENT_CHANGED", "软链接删除期间父目录已变化")
	}
	root, err := a.browserRoot(operation.Root.ID, operation.Parent.Path)
	if err != nil || root != operation.Root {
		return bad("FILE_ROOT_CHANGED", "软链接删除期间允许目录已变化")
	}
	effect, err := historySoftlinkDeleteEffect(*file)
	if err != nil {
		return err
	}
	item.Done = effect == "gone" || effect == "quarantine"
	item.Unrecoverable = effect == "gone"
	if effect == "gone" {
		file.Quarantined, file.Deleted = true, true
	}
	operation.State, operation.ModuleError = "completed", nil
	if state.DispatchError != nil {
		operation.ModuleError = state.DispatchError
		operation.State = "review"
	} else if !originalModuleResultTruthy(state.Result) {
		operation.ModuleError = bad("PLUGIN_FILE_OPERATION_REJECTED", "存储插件未确认删除；实际条目及已产生变化保留")
		operation.State = "rejected"
	} else if !item.Done {
		operation.ModuleError = bad("REVIEW_PLUGIN_FILE_OPERATION", "删除扩展返回成功，但原条目尚未实际移除")
		operation.State = "review"
	}
	if err = parent.Sync(); err != nil {
		return err
	}
	if err = save(); err != nil {
		return err
	}
	if operation.ModuleError != nil {
		return operation.ModuleError
	}
	return nil
}
