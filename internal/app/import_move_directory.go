package app

import "context"

// MP moves directory members recursively, leaving source directory containers.
// Publish the entire native disc before clearing members. Their existing move
// receipts own resumable cleanup; an old whole-source digest cannot be checked
// between intentional member removals.
func (a *App) finishDirectoryMove(ctx context.Context, receipt importReceipt, check func() error) (importReceipt, error) {
	if err := check(); err != nil {
		return receipt, err
	}
	actual, err := snapshotOwnedTransferPath(receipt.Target, receipt.StageSnapshot, receipt.Softlink, true, receipt.SharedDirectory)
	if err != nil || actual != receipt.StageSnapshot {
		return receipt, bad("REVIEW_TARGET_CHANGED", "移动目录的已发布目标发生变化")
	}
	if receipt.DirectoryMoveState == "completed" {
		return receipt, nil
	}
	if receipt.State != "published" || receipt.DirectoryMoveState != "cleaning" {
		return receipt, bad("REVIEW_RECEIPT", "目录移动尚未完成全部成员发布")
	}
	for _, relative := range receipt.DirectoryFiles {
		key := receipt.ID + "/directory/" + tokenHash(relative)
		if _, err = a.finishImportMove(ctx, key, check); err != nil {
			return receipt, err
		}
	}
	// Preserve evidence of our own empty-container change for the explicit
	// history “delete source” action. New files are never claimed by this move.
	if stamp, entries, e := snapshotOriginalDirectory(receipt.Source); e == nil && sameInode(stamp, receipt.SourceSnapshot) {
		empty := true
		for _, entry := range entries {
			if !entry.Directory {
				empty = false
				break
			}
		}
		if empty {
			receipt.MoveSourceSnapshot = &stamp
		}
	}
	receipt.DirectoryMoveState = "completed"
	err = a.Store.Put(ctx, "import_receipt", receipt.ID, receipt)
	return receipt, err
}
