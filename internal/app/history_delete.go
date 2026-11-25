package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
)

type historyDeleteRequest struct {
	Confirm      bool   `json:"confirm"`
	DeleteSource bool   `json:"delete_source"`
	DeleteTarget bool   `json:"delete_target"`
	FileID       string `json:"file_id,omitempty"`
}
type historySourceRemoval struct {
	HistoryID string           `json:"history_id"`
	File      assetRemovalFile `json:"file"`
	State     string           `json:"state"`
}

func (r historySourceRemoval) succeeded() bool {
	return r.State == "completed" && r.File.Deleted && !r.File.DirectoryRetained ||
		r.State == "absent" && !r.File.Deleted && !r.File.DirectoryRetained ||
		r.State == "retained" && r.File.DirectoryRetained && !r.File.Deleted && r.File.Snapshot.Directory != ""
}

// History is a projection of durable import plans. Removing a list entry must
// never erase the plan or its recovery/audit records.
func (a *App) deleteTransferHistory(ctx context.Context, item transferHistoryItem, in historyDeleteRequest) (any, error) {
	if !in.Confirm {
		return nil, bad("CONFIRM_REQUIRED", "请确认删除整理记录及所选文件")
	}
	if files := item.deleteFiles; files != nil {
		in.DeleteSource = in.DeleteSource && files.source
		in.DeleteTarget = in.DeleteTarget && files.target
		if (in.DeleteSource || in.DeleteTarget) && (item.PlanID == "" || item.FileID == "") {
			return nil, bad("HISTORY_FILE_UNMANAGED", "这条整理历史有文件项，但缺少光影的文件归属记录。可选择“仅删除整理记录”。")
		}
	}
	if (in.FileID != "" && in.FileID != item.executionFileID()) ||
		(in.FileID == "" && (in.DeleteSource || in.DeleteTarget)) {
		return nil, bad("HISTORY_CHANGED", "整理记录已更新，请刷新后重新确认删除")
	}
	if in.DeleteSource {
		// A running transfer may hold importMu while waiting for an external
		// storage module. Reject its frozen sources before waiting for that
		// lock; the physical-removal path checks again under the lock.
		if err := a.guardHistorySourceInUse(ctx, item); err != nil {
			return nil, err
		}
	}
	if in.DeleteSource || in.DeleteTarget {
		var busy bool
		if err := a.Store.reader().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jobs
 WHERE kind='import' AND json_extract(payload,'$.id')=? AND state IN ('queued','running'))`, item.PlanID).Scan(&busy); err != nil {
			return nil, err
		}
		if busy {
			return nil, bad("TRANSFER_BUSY", "整理任务仍在进行，请完成或停止后再删除记录")
		}
	}
	if in.DeleteTarget && item.Target != "" {
		if e := a.removeHistoricalTarget(ctx, item); e != nil {
			return nil, e
		}
		if e := a.completeHistoricalParentCleanup(ctx, item, false); e != nil && !originalHistoryDeleteRejected(e) {
			return nil, e
		}
	}
	if in.DeleteSource {
		if e := a.removeHistorySource(ctx, item); e != nil {
			return nil, e
		}
		if e := a.completeHistoricalParentCleanup(ctx, item, true); e != nil {
			return nil, e
		}
		if e := a.publishHistoryDownloadFileDeleted(ctx, item); e != nil {
			return nil, e
		}
	}
	if e := a.finishHistoryRowDeletion(ctx, item, in); e != nil {
		return nil, e
	}

	a.log("INFO", "history", "deleted", "已删除用户选定的整理历史及明确勾选的文件", item.JobID, "")
	return map[string]any{"deleted": true, "delete_source": in.DeleteSource, "delete_target": in.DeleteTarget}, nil
}

func (a *App) removeHistoricalTarget(ctx context.Context, item transferHistoryItem) error {
	var plan ImportPlan
	if e := a.Store.Get(ctx, "import_plan", item.PlanID, &plan); e != nil {
		return e
	}
	historical, auxiliary, receiptKey, e := a.historyPlannedFile(ctx, plan, item)
	if e != nil {
		return e
	}
	return a.removeHistoricalPublishedTarget(ctx, item, plan, historical, receiptKey, auxiliary)
}

func (a *App) historySourceForRemoval(ctx context.Context, item transferHistoryItem) (importSnapshot, bool, error) {
	if !importPath(item.Source) || browserInternal(item.Source) {
		return importSnapshot{}, false, bad("INVALID_SOURCE", "源文件不在当前资源目录中")
	}
	var plan ImportPlan
	if e := a.Store.Get(ctx, "import_plan", item.PlanID, &plan); e != nil {
		return importSnapshot{}, false, e
	}
	file, _, receiptKey, e := a.historyPlannedFile(ctx, plan, item)
	if e != nil {
		return importSnapshot{}, false, e
	}
	expected := planSnapshot(file)
	if file.Method == "move" {
		var published importReceipt
		if a.Store.Get(ctx, "import_receipt", receiptKey, &published) == nil && published.ID == receiptKey &&
			published.State == "published" && (published.Move != nil && published.Move.State == "completed" || expected.Directory != "" && published.DirectoryMoveState == "completed") &&
			published.Source == file.Source && published.Target == file.Target && published.SourceSnapshot == expected {
			// This is absence reconciliation, not permission to delete anything
			// below a library root. In particular, never skip a recreated source.
			if _, err := os.Lstat(item.Source); errors.Is(err, os.ErrNotExist) {
				return expected, false, nil
			}
		}
	}
	if !a.sourceAllowed(item.Source) && !a.historyReorganizedMoveSourceAllowed(ctx, plan, file) {
		return importSnapshot{}, false, bad("INVALID_SOURCE", "源文件不在当前资源目录中")
	}
	if file.Method == "move" && expected.Directory != "" {
		// Native directory move deliberately leaves empty containers. Only
		// this exact completed publication can own that changed source tree;
		// ordinary/plugin receipts and any newly added files keep the old guard.
		var published importReceipt
		if a.Store.Get(ctx, "import_receipt", receiptKey, &published) == nil &&
			published.ID == receiptKey && published.State == "published" && published.DirectoryMoveState == "completed" &&
			published.Source == file.Source && published.Target == file.Target && published.SourceSnapshot == expected &&
			published.MoveSourceSnapshot != nil && published.MoveSourceSnapshot.Directory != "" && sameInode(*published.MoveSourceSnapshot, expected) {
			expected = *published.MoveSourceSnapshot
		}
	}
	actual, e := snapshotTransferPath(item.Source)
	if errors.Is(e, os.ErrNotExist) {
		var receipt historySourceRemoval
		if a.Store.Get(ctx, "history_source_removal", item.executionFileID(), &receipt) == nil {
			if receipt.HistoryID != item.executionFileID() || receipt.File.Path != item.Source || receipt.File.Snapshot != expected {
				return importSnapshot{}, false, bad("HISTORY_SOURCE_RECEIPT", "源文件删除收据不一致")
			}
			if receipt.State == "absent" {
				return expected, false, nil
			}
			location, checkErr := removalLocation(receipt.File)
			if checkErr != nil {
				return importSnapshot{}, false, checkErr
			}
			if location != "gone" {
				return expected, true, nil
			}
			if receipt.State != "completed" {
				return expected, true, nil
			}
		}
		return expected, false, nil
	}
	if e != nil {
		return importSnapshot{}, false, e
	}
	if expected != actual {
		return importSnapshot{}, false, bad("FILE_SNAPSHOT_CHANGED", "源文件在整理后发生变化，不能按旧历史删除")
	}
	if e = a.guardHistorySourceInUse(ctx, item); e != nil {
		return importSnapshot{}, false, e
	}
	return actual, true, nil
}

// An already moved historical source may be an old managed library path rather
// than a download root. The frozen reorganize proof grants this one pathname,
// not arbitrary deletion below a current library root. Current asset ownership
// may legitimately have moved on; the immutable pre-transfer ownership is used.
func (a *App) historyReorganizedMoveSourceAllowed(ctx context.Context, plan ImportPlan, file PlanFile) bool {
	if !plan.Manual || file.Method != "move" || plan.ReorganizeAssetID == "" {
		return false
	}
	var meta importMetadata
	if a.Store.Get(ctx, "import_meta", plan.ID, &meta) != nil || meta.Reorganize == nil {
		return false
	}
	frozen := meta.Reorganize
	if frozen.Asset.ID != plan.ReorganizeAssetID || frozen.Asset.Path != file.Source ||
		frozen.Video.Path != file.Source || frozen.Video.AssetID != frozen.Asset.ID ||
		frozen.Video.RootID != frozen.Asset.RootID || frozen.Video.Kind != "video" ||
		frozen.Video.MediaKey != mediaKey(frozen.Asset.Media) || frozen.Video.Snapshot != planSnapshot(file) ||
		file.Source == frozen.RootPath || !within(frozen.RootPath, file.Source) {
		return false
	}
	root, err := a.importRoot(frozen.Asset.RootID)
	return err == nil && root.Path == frozen.RootPath
}

func (a *App) guardHistorySourceInUse(ctx context.Context, item transferHistoryItem) error {
	// Start with the state index. Completed plans never need to be opened to
	// determine whether a worker currently owns this exact source or sidecar.
	var busy bool
	if e := a.Store.reader().QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM jobs j INDEXED BY jobs_due
 CROSS JOIN import_plan r ON r.id=json_extract(j.payload,'$.id')
 LEFT JOIN import_meta m ON m.id=r.id
 WHERE j.state IN ('queued','running') AND j.kind='import' AND r.id!=?
 AND (EXISTS(SELECT 1 FROM json_each(r.payload,'$.files') f WHERE json_extract(f.value,'$.source')=?)
 OR EXISTS(SELECT 1 FROM json_each(m.payload,'$.sidecars') g,json_each(g.value) s WHERE json_extract(s.value,'$.source')=?)))`, item.PlanID, item.Source, item.Source).Scan(&busy); e != nil {
		return e
	}
	if busy {
		return bad("FILE_IN_USE", "其他整理任务正在使用源文件")
	}
	return nil
}

func (a *App) removeHistorySource(ctx context.Context, item transferHistoryItem) error {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	expected, exists, e := a.historySourceForRemoval(ctx, item)
	if e != nil {
		return e
	}
	var receipt historySourceRemoval
	key := item.executionFileID()
	readErr := a.Store.Get(ctx, "history_source_removal", key, &receipt)
	if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
		return readErr
	}
	if readErr == nil && (receipt.HistoryID != key || receipt.File.Path != item.Source || receipt.File.Snapshot != expected) {
		return bad("HISTORY_SOURCE_RECEIPT", "源文件删除收据不一致")
	}
	if !exists {
		if readErr == nil {
			return nil
		}
		// LocalStorage treats an absent path as success. Persist that observation
		// so event publication can resume without claiming a physical deletion.
		receipt = historySourceRemoval{HistoryID: key, File: assetRemovalFile{Path: item.Source, Kind: "source", Snapshot: expected}, State: "absent"}
		return a.Store.Put(ctx, "history_source_removal", key, receipt)
	}
	if receipt.State == "absent" {
		return bad("FILE_SNAPSHOT_CHANGED", "原先缺失的源文件已重新出现，请重新核对")
	}
	release, e := a.beginWrite()
	if e != nil {
		return e
	}
	defer release()
	parent, e := importDir(filepath.Dir(item.Source), false)
	if e != nil {
		return e
	}
	defer parent.Close()
	name := filepath.Base(item.Source)
	if errors.Is(readErr, sql.ErrNoRows) {
		receipt = historySourceRemoval{HistoryID: item.executionFileID(), File: assetRemovalFile{Path: item.Source, Snapshot: expected,
			Kind: "source", Quarantine: ".light-media-remove-" + tokenHash(item.executionFileID() + now())[:32] + ".part"}, State: "deleting"}
		if e = a.Store.Put(ctx, "history_source_removal", item.executionFileID(), receipt); e != nil {
			return e
		}
	}
	location, e := removalLocation(receipt.File)
	if e != nil {
		return e
	}
	if location == "source" {
		frozen, err := a.originalHistoryFrozenItem(ctx, item, true)
		if err != nil {
			return err
		}
		if e = a.originalHistoryStorageDelete(ctx, &receipt.File, parent, func() error { return a.Store.Put(ctx, "history_source_removal", item.executionFileID(), receipt) }, frozen); e != nil {
			return e
		}
		if receipt.File.DirectoryRetained {
			receipt.State = "retained"
			return a.Store.Put(ctx, "history_source_removal", item.executionFileID(), receipt)
		}
		location, e = removalLocation(receipt.File)
		if e != nil {
			return e
		}
	}
	if location == "source" {
		if e = importRenameExclusive(int(parent.Fd()), name, receipt.File.Quarantine); e != nil {
			return e
		}
		actual, e := snapshotTransferPath(filepath.Join(filepath.Dir(item.Source), receipt.File.Quarantine))
		if e != nil || actual != expected {
			_ = importRenameExclusive(int(parent.Fd()), receipt.File.Quarantine, name)
			_ = parent.Sync()
			return bad("FILE_SNAPSHOT_CHANGED", "删除时源文件发生变化，已停止并保留数据")
		}
		if e = parent.Sync(); e != nil {
			return e
		}
	}
	if location != "gone" {
		if e = parent.Sync(); e != nil {
			return e
		}
		receipt.File.Quarantined = true
		if e = a.Store.Put(ctx, "history_source_removal", item.executionFileID(), receipt); e != nil {
			return e
		}
		if e = a.removeHistoryQuarantine(ctx, &receipt.File, parent, func() error { return a.Store.Put(ctx, "history_source_removal", item.executionFileID(), receipt) }); e != nil {
			if !receipt.File.DirectoryRemovalStarted {
				_ = importRenameExclusive(int(parent.Fd()), receipt.File.Quarantine, name)
			}
			return e
		}
	}
	if e = parent.Sync(); e != nil {
		return e
	}
	receipt.State = "completed"
	receipt.File.Deleted = true
	return a.Store.Put(ctx, "history_source_removal", item.executionFileID(), receipt)
}

// fileInTaskDirectory reports whether the file sits directly in a task's own
// directory. A task rooted at a parent directory (the download root itself, or a
// whole-category task) does not own every file below it, and treating it as if it
// did blocked every history delete on the machine.
func fileInTaskDirectory(directory, path string) bool {
	if directory == "" || path == "" {
		return false
	}
	return filepath.Dir(path) == filepath.Clean(directory)
}
