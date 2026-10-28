package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Resolve the actual file represented by a source callback. Auxiliary IDs are
// independent history IDs, while their frozen I/O and intent belong to the
// parent's concrete plan. Never infer this binding from a client pathname.
func (a *App) historyPlannedFile(ctx context.Context, plan ImportPlan, item transferHistoryItem) (PlanFile, bool, string, error) {
	for _, file := range plan.Files {
		if file.ID == item.executionFileID() {
			if file.Source != item.Source {
				return PlanFile{}, false, "", coreDeleteInvalid()
			}
			return file, auxiliaryMaterialKind(file.MaterialKind), plan.ID + "/" + file.ID, nil
		}
	}
	var key string
	var raw []byte
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT id,payload FROM records WHERE kind='original_transfer_callback' AND json_extract(payload,'$.plan_id')=? AND json_extract(payload,'$.file.id')=?`, plan.ID, item.executionFileID()).Scan(&key, &raw); err != nil {
		return PlanFile{}, false, "", err
	}
	var callback originalTransferCallback
	if json.Unmarshal(raw, &callback) != nil || callback.PlanID != plan.ID || callback.File.ID != item.executionFileID() || callback.File.Source != item.Source || callback.File.Target != item.Target || callback.File.ParentFileID != callback.ParentFileID {
		return PlanFile{}, false, "", coreDeleteInvalid()
	}
	var meta importMetadata
	if err := a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil {
		return PlanFile{}, false, "", err
	}
	if err := a.requireNativeDirectoryAuthorizationTx(ctx, a.Store.DB, plan, meta); err != nil {
		return PlanFile{}, false, "", err
	}
	for _, parent := range plan.Files {
		if parent.ID != callback.ParentFileID {
			continue
		}
		for index, side := range meta.Sidecars[parent.ID] {
			if key != fmt.Sprintf("%s/%s/side-%d", plan.ID, parent.ID, index) || side.Source != item.Source {
				continue
			}
			file := originalTransferSideFile(parent, side, index)
			file.ID, file.ParentFileID, file.AssetID = callback.File.ID, parent.ID, parent.AssetID
			return file, true, key, nil
		}
	}
	return PlanFile{}, false, "", coreDeleteInvalid()
}

type historyTargetRemoval struct {
	HistoryID, PlanID, ReceiptKey, AssetID, RootID string
	SourceSnapshot                                 importSnapshot
	File                                           assetRemovalFile
	State                                          string
}

// StorageChain.delete_media_file removes the selected published file. Other
// subtitles/audio remain media files and prevent its parent cleanup. Each
// history therefore uses its own receipt, including the main video history.
func (a *App) removeHistoricalPublishedTarget(ctx context.Context, item transferHistoryItem, plan ImportPlan, file PlanFile, key string, auxiliary bool) error {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	root, err := a.importRoot(plan.RootID)
	if err != nil {
		return err
	}
	var meta importMetadata
	if err := a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil {
		return err
	}
	if err := a.requireNativeDirectoryAuthorizationTx(ctx, a.Store.DB, plan, meta); err != nil {
		return err
	}
	if root.Path != meta.RootPath || file.Target != item.Target || !within(root.Path, file.Target) || file.Target == file.Source {
		return bad("HISTORY_TARGET_CHANGED", "关联文件目标与原整理归属不一致")
	}
	var published importReceipt
	if err := a.Store.Get(ctx, "import_receipt", key, &published); err != nil {
		return err
	}
	if published.State != "published" || published.Source != file.Source || published.Target != file.Target || published.SourceSnapshot != planSnapshot(file) {
		return bad("HISTORY_TARGET_UNMANAGED", "关联文件缺少已确认的实际发布收据")
	}
	var owned managedFile
	ownedErr := a.Store.Get(ctx, "managed_file", tokenHash(item.Target), &owned)
	if ownedErr != nil && !errors.Is(ownedErr, sql.ErrNoRows) {
		return ownedErr
	}
	validKind := owned.Kind == "video"
	if auxiliary {
		validKind = auxiliaryMaterialKind(owned.Kind)
	}
	if ownedErr == nil && (!validKind || owned.AssetID != file.AssetID || owned.RootID != plan.RootID || owned.Path != file.Target || owned.Snapshot != published.StageSnapshot || owned.SourceSnapshot != published.SourceSnapshot) {
		return bad("HISTORY_TARGET_CHANGED", "关联文件已被其他整理结果接管")
	}
	var asset Asset
	assetErr := a.Store.Get(ctx, "asset", file.AssetID, &asset)
	if !auxiliary {
		if assetErr != nil {
			return assetErr
		}
		if asset.Path != file.Target || asset.RootID != plan.RootID || asset.SourcePath == file.Target {
			return bad("HISTORY_TARGET_CHANGED", "目标资产路径或原始资源归属已变化")
		}
	}
	var receipt historyTargetRemoval
	err = a.Store.Get(ctx, "history_target_removal", item.executionFileID(), &receipt)
	if errors.Is(err, sql.ErrNoRows) {
		actual, err := snapshotTransferPath(file.Target)
		missing := errors.Is(err, os.ErrNotExist)
		if !missing && (err != nil || actual != published.StageSnapshot) {
			return bad("FILE_SNAPSHOT_CHANGED", "关联目标文件发生变化，未删除")
		}
		receipt = historyTargetRemoval{HistoryID: item.executionFileID(), PlanID: plan.ID, ReceiptKey: key, AssetID: file.AssetID, RootID: plan.RootID, SourceSnapshot: published.SourceSnapshot,
			File: assetRemovalFile{Path: file.Target, Kind: "auxiliary", Snapshot: published.StageSnapshot, Quarantine: ".light-media-remove-" + tokenHash(item.executionFileID() + now())[:32] + ".part"}, State: "deleting"}
		if missing {
			// An older host version may have frozen this exact file in an
			// asset-removal preview. Adopt only that selected quarantine entry;
			// never resume its other subtitle/audio files as a group.
			var raw []byte
			err := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='removal_preview' AND json_extract(payload,'$.asset_id')=? AND json_extract(payload,'$.state')='deleting' ORDER BY updated_at DESC LIMIT 1`, file.AssetID).Scan(&raw)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			var previous assetRemovalPreview
			found := false
			if err == nil {
				if json.Unmarshal(raw, &previous) != nil || previous.RootID != plan.RootID || previous.RootPath != root.Path {
					return bad("HISTORY_TARGET_CHANGED", "旧目标删除收据归属不一致")
				}
				for _, selected := range previous.Files {
					if selected.Path != file.Target || selected.Deleted || selected.Snapshot != published.StageSnapshot || selected.Quarantine == "" {
						continue
					}
					location, e := removalLocation(selected)
					if e != nil {
						return e
					}
					if location == "quarantine" {
						receipt.File, found = selected, true
					}
				}
			}
			if !found {
				return nil
			}
		}
		if err := a.Store.Put(ctx, "history_target_removal", item.executionFileID(), receipt); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if receipt.HistoryID != item.executionFileID() || receipt.PlanID != plan.ID || receipt.ReceiptKey != key || receipt.AssetID != file.AssetID || receipt.RootID != plan.RootID || receipt.SourceSnapshot != published.SourceSnapshot || receipt.File.Path != file.Target || receipt.File.Snapshot != published.StageSnapshot {
		return bad("HISTORY_TARGET_CHANGED", "关联目标删除收据不一致")
	}
	if receipt.State == "completed" && receipt.File.Deleted {
		// Parent cleanup may already have removed this directory. Resume its
		// own receipt without reopening or repeating the completed file write.
		return nil
	}
	release, err := a.beginWrite()
	if err != nil {
		return err
	}
	defer release()
	parent, err := importDir(filepath.Dir(file.Target), false)
	if err != nil {
		return err
	}
	defer parent.Close()
	location, err := removalLocation(receipt.File)
	if err != nil {
		return err
	}
	if location == "source" {
		frozen, err := a.originalHistoryFrozenItem(ctx, item, false)
		if err != nil {
			return err
		}
		err = a.originalHistoryStorageDelete(ctx, &receipt.File, parent, func() error { return a.Store.Put(ctx, "history_target_removal", item.executionFileID(), receipt) }, frozen)
		if err != nil && !originalHistoryDeleteRejected(err) {
			return err
		}
		location, err = removalLocation(receipt.File)
		if err != nil {
			return err
		}
		if location == "source" {
			// The pinned endpoint ignores a false target deletion result. Keep
			// its real file and ownership, while allowing record-only removal.
			receipt.State = "rejected"
			if receipt.File.DirectoryRetained {
				receipt.State = "retained"
			}
			return a.Store.Put(ctx, "history_target_removal", item.executionFileID(), receipt)
		}
	}
	name := filepath.Base(file.Target)
	if location == "source" {
		if err := importRenameExclusive(int(parent.Fd()), name, receipt.File.Quarantine); err != nil {
			return err
		}
		actual, err := snapshotTransferPath(filepath.Join(filepath.Dir(file.Target), receipt.File.Quarantine))
		if err != nil || actual != receipt.File.Snapshot {
			_ = importRenameExclusive(int(parent.Fd()), receipt.File.Quarantine, name)
			_ = parent.Sync()
			return bad("FILE_SNAPSHOT_CHANGED", "删除时关联目标发生变化，已保留数据")
		}
		if err := parent.Sync(); err != nil {
			return err
		}
	}
	if location != "gone" {
		if err := parent.Sync(); err != nil {
			return err
		}
		receipt.File.Quarantined = true
		if err := a.Store.Put(ctx, "history_target_removal", item.executionFileID(), receipt); err != nil {
			return err
		}
		if err := a.removeHistoryQuarantine(ctx, &receipt.File, parent, func() error { return a.Store.Put(ctx, "history_target_removal", item.executionFileID(), receipt) }); err != nil {
			if !receipt.File.DirectoryRemovalStarted {
				_ = importRenameExclusive(int(parent.Fd()), receipt.File.Quarantine, name)
			}
			return err
		}
	}
	if err := parent.Sync(); err != nil {
		return err
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if ownedErr == nil {
		var currentRaw []byte
		var current managedFile
		if err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='managed_file' AND id=?`, tokenHash(item.Target)).Scan(&currentRaw); err != nil || json.Unmarshal(currentRaw, &current) != nil || current != owned {
			return bad("HISTORY_TARGET_CHANGED", "关联目标归属已变化，删除收据需要核对")
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM records WHERE kind='managed_file' AND id=? AND payload=?`, tokenHash(item.Target), currentRaw)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return bad("HISTORY_TARGET_CHANGED", "关联目标归属已变化，删除收据需要核对")
		}
	}
	if !auxiliary {
		var current Asset
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='asset' AND id=?`, asset.ID).Scan(&raw); err != nil {
			return err
		}
		if json.Unmarshal(raw, &current) != nil || current.Path != asset.Path || current.RootID != asset.RootID || current.SourcePath != asset.SourcePath {
			return bad("HISTORY_TARGET_CHANGED", "删除期间目标资产归属已变化")
		}
		current.State, current.FNOSState = "removed", "needs_attention"
		current.Error = "视频文件已由用户从整理历史移除；其他关联文件保留"
		if err := txPut(ctx, tx, "asset", current.ID, current); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM asset_slots WHERE asset_id=?`, current.ID); err != nil {
			return err
		}
	}
	receipt.State, receipt.File.Deleted = "completed", true
	if err := txPut(ctx, tx, "history_target_removal", item.executionFileID(), receipt); err != nil {
		return err
	}
	return tx.Commit()
}
