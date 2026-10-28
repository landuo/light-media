package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
)

// The manual endpoint uses delete_media_file and aborts on False; ordinary
// redo uses delete_file and ignores False. Both act only on this history file.
// Native plan/path authorization precedes destructive cleanup; the same frozen
// plan is then submitted, without another recognition or image-provider call.
func (a *App) prepareOriginalHistoryTransfer(ctx context.Context, next ImportPlan, item transferHistoryItem, prior ImportPlan, file PlanFile, key string, auxiliary, retry bool) error {
	if err := a.removeHistoricalPublishedTarget(ctx, item, prior, file, key, auxiliary); err != nil {
		return err
	}
	var receipt historyTargetRemoval
	err := a.Store.Get(ctx, "history_target_removal", item.executionFileID(), &receipt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if !retry && err == nil && receipt.State == "rejected" {
		return bad("PLUGIN_FILE_OPERATION_REJECTED", "旧整理目标删除失败，未开始新的整理")
	}
	if !retry {
		if err := a.completeHistoricalParentCleanup(ctx, item, false); err != nil {
			return err
		}
	}
	if auxiliary {
		return nil
	}
	a.importMu.Lock()
	defer a.importMu.Unlock()
	var meta importMetadata
	if err := a.Store.Get(ctx, "import_meta", next.ID, &meta); err != nil {
		return err
	}
	var current Asset
	if err := a.Store.Get(ctx, "asset", file.AssetID, &current); err != nil {
		return err
	}
	if meta.Reorganize == nil {
		return a.verifyOriginalHistoryCleanup(ctx, item, file, key, current)
	}
	if !meta.Reorganize.SingleHistory || meta.Reorganize.Asset.ID != file.AssetID {
		return bad("REVIEW_REORGANIZE_OWNER", "重新整理缺少原历史文件归属")
	}
	if current.Path != meta.Reorganize.Asset.Path || current.RootID != meta.Reorganize.Asset.RootID || current.SourcePath != meta.Reorganize.Asset.SourcePath {
		return bad("REVIEW_REORGANIZE_CHANGED", "旧整理目标删除期间归属已变化")
	}
	meta.Reorganize.Asset = current
	meta.Reorganize.TargetRemoved = receipt.File.Deleted
	remaining := meta.Reorganize.Owned[:0]
	for _, own := range meta.Reorganize.Owned {
		if own.Path == item.Target && receipt.File.Deleted {
			continue
		}
		remaining = append(remaining, own)
	}
	meta.Reorganize.Owned = remaining
	return a.Store.Put(ctx, "import_meta", next.ID, meta)
}

// A new explicit attempt may follow a completed selected-file cleanup even
// when saving or queuing the replacement failed. Reuse the durable deletion
// fact, while checking the original source and never reviving an absent asset.
func (a *App) verifyOriginalHistoryCleanup(ctx context.Context, item transferHistoryItem, file PlanFile, key string, asset Asset) error {
	var receipt historyTargetRemoval
	if err := a.Store.Get(ctx, "history_target_removal", item.executionFileID(), &receipt); err != nil {
		return err
	}
	if asset.State != "removed" || asset.ID != file.AssetID || asset.Path != item.Target || asset.SourcePath != file.Source || receipt.State != "completed" || !receipt.File.Deleted || receipt.PlanID != item.PlanID || receipt.ReceiptKey != key || receipt.AssetID != asset.ID || receipt.RootID != asset.RootID || receipt.File.Path != asset.Path || receipt.SourceSnapshot != planSnapshot(file) {
		return bad("REVIEW_REORGANIZE_OWNER", "旧目标清理收据与原整理归属不一致")
	}
	if stamp, err := snapshotTransferPath(file.Source); err != nil || stamp != receipt.SourceSnapshot {
		return bad("REVIEW_REORGANIZE_SOURCE", "旧目标清理后原始资源已发生变化")
	}
	if _, err := snapshotTransferPath(asset.Path); !errors.Is(err, os.ErrNotExist) {
		return bad("REVIEW_REORGANIZE_CHANGED", "旧目标清理后路径再次出现，需核对归属")
	}
	return nil
}
