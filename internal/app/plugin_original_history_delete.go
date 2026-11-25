package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"light-media/internal/plugins"
)

func originalHistoryDeleteRejected(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Code == "PLUGIN_FILE_OPERATION_REJECTED"
}

// History and asset removal already freeze a single authorized physical file.
// Reuse the existing browser mutation's ordinary delete_file dispatcher and
// effect verification at that same quarantine boundary. Its private journal
// is sealed, including FileItem values and a provider's completion/error fact.
func (a *App) originalHistoryStorageDelete(ctx context.Context, file *assetRemovalFile, parent *os.File, persist func() error, frozen *plugins.ModuleFileItem) error {
	if file.SharedDirectory != nil {
		return a.removeSharedDirectoryMembers(ctx, file, persist)
	}
	if file.Softlink != nil {
		return a.originalHistorySoftlinkDelete(ctx, file, parent, persist, frozen)
	}
	var operation browserOperation
	if len(file.SourceOperation) > 0 {
		sealed, err := base64.StdEncoding.DecodeString(file.SourceOperation)
		if err != nil {
			return bad("REVIEW_HISTORY_DELETE", "文件删除扩展收据不可读取")
		}
		if err := a.Store.Unseal(sealed, &operation); err != nil {
			return bad("REVIEW_HISTORY_DELETE", "文件删除扩展收据不可读取")
		}
		if len(operation.Items) != 1 || operation.Action != "delete" || operation.Items[0].Source != file.Path || operation.Items[0].Target != filepath.Join(filepath.Dir(file.Path), file.Quarantine) {
			return bad("REVIEW_HISTORY_DELETE", "文件删除扩展与已确认文件不一致")
		}
		stamp := operation.Items[0].Manifest
		if len(stamp) == 0 || file.Snapshot.Directory == "" && len(stamp) != 1 || stamp[0].Device != file.Snapshot.Device || stamp[0].Inode != file.Snapshot.Inode || file.Snapshot.Directory == "" && stamp[0].Size != file.Snapshot.Size || stamp[0].ModNS != file.Snapshot.ModNS {
			return bad("REVIEW_HISTORY_DELETE", "文件删除扩展快照不一致")
		}
		if operation.Items[0].Done {
			if operation.ModuleError != nil {
				return operation.ModuleError
			}
			return nil
		}
		if operation.State == "rejected" {
			// A fresh explicit request can retry a known false/no-effect result.
			// An interrupted or uncertain invocation is never sent again.
			if current, err := snapshotTransferPath(file.Path); err != nil || current != file.Snapshot {
				return bad("FILE_SNAPSHOT_CHANGED", "上次删除失败后文件发生变化")
			}
			operation = browserOperation{}
		} else if operation.Items[0].SourceModuleStarted {
			return bad("REVIEW_HISTORY_DELETE", "文件删除扩展已调用，结果尚未确认，未重复调用")
		} else if file.DirectoryParentOnly {
			// Ordinary-directory cleanup prepared by an earlier version must
			// re-evaluate its current directory policy before a first dispatch.
			if _, err := os.Lstat(operation.Items[0].Target); errors.Is(err, os.ErrNotExist) {
				if current, err := snapshotTransferPath(file.Path); err != nil || current != file.Snapshot {
					return bad("FILE_SNAPSHOT_CHANGED", "待删除目录在恢复前发生变化")
				}
				operation = browserOperation{}
			}
		}
	}
	if operation.ID == "" {
		actual, err := snapshotTransferPath(file.Path)
		if err != nil {
			return err
		}
		if actual != file.Snapshot {
			return bad("FILE_SNAPSHOT_CHANGED", "删除扩展调用前文件发生变化")
		}
		manifest, err := originalHistoryManifest(file.Path)
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
			return bad("FILE_ROOT_REJECTED", "删除扩展路径不在当前允许目录内")
		}
		item, err := originalLocalFileItem(file.Path)
		if err != nil {
			return err
		}
		if frozen != nil {
			item = frozen.Clone()
		}
		if file.Snapshot.Directory != "" {
			raw, _ := json.Marshal(item)
			var fields map[string]any
			if err := json.Unmarshal(raw, &fields); err != nil {
				return err
			}
			bluray, err := a.originalTransferIsBluray(ctx, fields)
			if err != nil {
				return err
			}
			if !bluray {
				// StorageChain skips the initial delete for ordinary directories.
				// Its cleanup loop starts at the directory and stops successfully
				// unless any_files returns the exact boolean False.
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
		operation = browserOperation{ID: id("historyfile_"), Root: root, Action: "delete", Parent: parentStamp, State: "executing", CreatedAt: now(), Items: []browserOperationItem{{Source: file.Path, Target: filepath.Join(filepath.Dir(file.Path), file.Quarantine), Manifest: manifest, OriginalFileItem: item}}}
	}
	save := func() error {
		sealed, err := a.Store.Seal(operation)
		if err != nil {
			return err
		}
		file.SourceOperation = base64.StdEncoding.EncodeToString(sealed)
		return persist()
	}
	if err := save(); err != nil {
		return err
	}
	item := &operation.Items[0]
	done, err := a.originalBrowserMutation(ctx, &operation, item, parent, func() error { item.SourceModuleStarted = true; return save() }, true)
	item.Done = done
	if done && item.Unrecoverable {
		file.Quarantined, file.Deleted = true, true
	}
	operation.State = "completed"
	if err != nil {
		var api *APIError
		if errors.As(err, &api) {
			operation.ModuleError = api
		} else {
			operation.ModuleError = bad("REVIEW_HISTORY_DELETE", "文件删除扩展未完成，请核对实际文件")
		}
		operation.State = "review"
		if originalHistoryDeleteRejected(err) {
			operation.State = "rejected"
		}
	}
	if saveErr := save(); saveErr != nil {
		return saveErr
	}
	return err
}

// Only the proof-bound delete adapter's selected native fallback reaches this
// quarantine operation. Generic browser manifests still reject arbitrary links.
func (a *App) quarantineHistorySoftlink(ctx context.Context, file *assetRemovalFile, parent *os.File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	location, err := removalLocation(*file)
	if err != nil || location != "source" {
		return err
	}
	name := filepath.Base(file.Path)
	if err := importRenameExclusive(int(parent.Fd()), name, file.Quarantine); err != nil {
		return err
	}
	quarantine := filepath.Join(filepath.Dir(file.Path), file.Quarantine)
	actual, err := snapshotOwnedTransferPath(quarantine, file.Snapshot, file.Softlink, false)
	if err != nil || actual != file.Snapshot {
		_ = importRenameExclusive(int(parent.Fd()), file.Quarantine, name)
		_ = parent.Sync()
		return bad("FILE_SNAPSHOT_CHANGED", "软链接删除隔离时实际条目发生变化")
	}
	return parent.Sync()
}

// TransferHistory persists the original FileItem values. Keep those values
// when a later history action calls storage, while authorizing physical paths
// independently through the plan, ownership and published receipt.
func (a *App) originalHistoryFrozenItem(ctx context.Context, item transferHistoryItem, source bool) (*plugins.ModuleFileItem, error) {
	var plan ImportPlan
	if err := a.Store.Get(ctx, "import_plan", item.PlanID, &plan); err != nil {
		return nil, err
	}
	file, auxiliary, key, err := a.historyPlannedFile(ctx, plan, item)
	if err != nil {
		return nil, err
	}
	file.ID = strings.TrimPrefix(key, plan.ID+"/")
	info, _, err := a.originalRecordedTransferModule(ctx, plan, file)
	if err != nil {
		return nil, err
	}
	field := "target_item"
	if source {
		field = "fileitem"
	}
	if value := sourceMap(info[field]); value != nil {
		return originalTransferFrozenItem(value)
	}
	if !source {
		return nil, nil
	}
	var meta importMetadata
	if err := a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil {
		return nil, err
	}
	if !auxiliary {
		return meta.OriginalTransfer[file.ID].Item, nil
	}
	for _, sides := range meta.Sidecars {
		for _, side := range sides {
			if side.Source == item.Source {
				return side.Item, nil
			}
		}
	}
	return nil, nil
}
