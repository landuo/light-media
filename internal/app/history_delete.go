package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"light-media/internal/download"
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
		r.State == "retained" && r.File.DirectoryRetained && !r.File.Deleted && r.File.Snapshot.Directory != ""
}

// History is a projection of durable import plans. Removing a list entry must
// never erase the plan or its recovery/audit records.
func (a *App) deleteTransferHistory(ctx context.Context, item transferHistoryItem, in historyDeleteRequest) (any, error) {
	if !in.Confirm {
		return nil, bad("CONFIRM_REQUIRED", "请确认删除整理记录及所选文件")
	}
	if (in.FileID != "" && in.FileID != item.executionFileID()) ||
		(in.FileID == "" && (in.DeleteSource || in.DeleteTarget) && item.executionFileID() != item.ID) {
		return nil, bad("HISTORY_CHANGED", "整理记录已更新，请刷新后重新确认删除")
	}
	if in.DeleteSource {
		// A running transfer may hold importMu while waiting for an external
		// storage module. Reject its frozen sources before waiting for that
		// lock; the physical-removal path checks again under the lock.
		if err := a.guardHistorySourceInUse(ctx, item); err != nil {
			return nil, err
		}
		a.downloadMu.Lock()
		defer a.downloadMu.Unlock()
	}
	var active int
	if e := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind='import' AND json_extract(payload,'$.id')=? AND state IN ('queued','running','retry_wait','waiting_paused')`, item.PlanID).Scan(&active); e != nil {
		return nil, e
	}
	if active > 0 && (in.DeleteSource || in.DeleteTarget) {
		return nil, bad("TRANSFER_BUSY", "整理任务仍在进行，请完成或停止后再删除记录")
	}
	var sourceSnapshot importSnapshot
	var sourceExists bool
	if in.DeleteSource {
		a.importMu.Lock()
		var e error
		sourceSnapshot, sourceExists, e = a.historySourceForRemoval(ctx, item)
		a.importMu.Unlock()
		if e != nil {
			return nil, e
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
	if in.DeleteSource && sourceExists {
		if e := a.removeHistorySource(ctx, item, sourceSnapshot); e != nil {
			return nil, e
		}
	}
	if in.DeleteSource {
		if e := a.completeHistoricalParentCleanup(ctx, item, true); e != nil {
			return nil, e
		}
		if e := a.publishHistoryDownloadFileDeleted(ctx, item); e != nil {
			return nil, e
		}
	}
	if e := a.Store.Put(ctx, "transfer_history_hidden", item.ID, map[string]any{
		"at": now(), "plan_id": item.PlanID, "delete_source": in.DeleteSource, "delete_target": in.DeleteTarget,
		"file_id": item.executionFileID(), "execution_key": item.ExecutionKey,
	}); e != nil {
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
	if !a.sourceAllowed(item.Source) || browserInternal(item.Source) {
		return importSnapshot{}, false, bad("INVALID_SOURCE", "源文件不在当前资源目录中")
	}
	var plan ImportPlan
	if e := a.Store.Get(ctx, "import_plan", item.PlanID, &plan); e != nil {
		return importSnapshot{}, false, e
	}
	if _, e := a.historyDownloadIdentity(ctx, item); e != nil {
		return importSnapshot{}, false, e
	}
	file, _, _, e := a.historyPlannedFile(ctx, plan, item)
	if e != nil {
		return importSnapshot{}, false, e
	}
	expected := planSnapshot(file)
	actual, e := snapshotTransferPath(item.Source)
	if errors.Is(e, os.ErrNotExist) {
		var receipt historySourceRemoval
		if a.Store.Get(ctx, "history_source_removal", item.executionFileID(), &receipt) == nil {
			if receipt.HistoryID != item.executionFileID() || receipt.File.Path != item.Source || receipt.File.Snapshot != expected {
				return importSnapshot{}, false, bad("HISTORY_SOURCE_RECEIPT", "源文件删除收据不一致")
			}
			location, checkErr := removalLocation(receipt.File)
			if checkErr != nil {
				return importSnapshot{}, false, checkErr
			}
			if location != "gone" {
				if guardErr := a.guardHistoryDownloaderSource(ctx, item); guardErr != nil {
					return importSnapshot{}, false, guardErr
				}
				return expected, true, nil
			}
			if receipt.State != "completed" {
				return expected, true, nil
			}
		}
		return importSnapshot{}, false, nil
	}
	if e != nil {
		return importSnapshot{}, false, e
	}
	if expected != actual {
		return importSnapshot{}, false, bad("FILE_SNAPSHOT_CHANGED", "源文件在整理后发生变化，不能按旧历史删除")
	}
	if e = a.guardHistoryDownloaderSource(ctx, item); e != nil {
		return importSnapshot{}, false, e
	}
	if e = a.guardHistorySourceInUse(ctx, item); e != nil {
		return importSnapshot{}, false, e
	}
	return actual, true, nil
}

func (a *App) guardHistorySourceInUse(ctx context.Context, item transferHistoryItem) error {
	var busy int
	if e := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM records r
 LEFT JOIN records m ON m.kind='import_meta' AND m.id=r.id
 WHERE r.kind='import_plan' AND r.id!=?
 AND EXISTS(SELECT 1 FROM jobs j WHERE j.kind='import' AND json_extract(j.payload,'$.id')=r.id AND j.state IN ('queued','running','retry_wait','waiting_paused'))
 AND (EXISTS(SELECT 1 FROM json_each(r.payload,'$.files') f WHERE json_extract(f.value,'$.source')=?)
 OR EXISTS(SELECT 1 FROM json_each(m.payload,'$.sidecars') g,json_each(g.value) s WHERE json_extract(s.value,'$.source')=?))`, item.PlanID, item.Source, item.Source).Scan(&busy); e != nil {
		return e
	}
	if busy > 0 {
		return bad("FILE_IN_USE", "其他整理任务正在使用源文件")
	}
	return nil
}

// Downloaders no longer need configured save paths: their actual task paths
// are the only reliable evidence that a selected source is still in use.
func (a *App) guardHistoryDownloaderSource(ctx context.Context, item transferHistoryItem) error {
	path := item.Source
	for _, configured := range a.Settings().Downloaders {
		client, err := download.New(configured.Config)
		if err != nil {
			return bad("FILE_DOWNLOADER_UNVERIFIED", "无法连接下载器核对做种源，已停止删除源文件")
		}
		tasks, err := client.List(ctx)
		if err != nil {
			return bad("FILE_DOWNLOADER_UNVERIFIED", "无法读取下载器任务核对做种源，已停止删除源文件")
		}
		for _, task := range tasks {
			directory, err := mapDownloaderPath(a.Settings(), configured, task.DownloadDir)
			if err != nil || !importPath(directory) {
				return bad("FILE_DOWNLOADER_UNVERIFIED", "下载器返回了无法核对的任务目录，已停止删除源文件")
			}
			if _, overlap := sharedDirectoryPath(directory, path); overlap {
				authorized, checkErr := a.historyDownloadTaskAuthorized(ctx, item, configured, task)
				if checkErr != nil {
					return checkErr
				}
				if authorized {
					continue
				}
				return bad("FILE_DOWNLOADER_SOURCE_PROTECTED", "源文件位于现有下载任务目录中，请先在下载器中处理该任务")
			}
		}
	}
	return nil
}

func (a *App) removeHistorySource(ctx context.Context, item transferHistoryItem, expected importSnapshot) error {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	actual, exists, e := a.historySourceForRemoval(ctx, item)
	if e != nil || !exists {
		return e
	}
	if actual != expected {
		return bad("FILE_SNAPSHOT_CHANGED", "源文件在确认后发生变化，请重新核对")
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
	var receipt historySourceRemoval
	if a.Store.Get(ctx, "history_source_removal", item.executionFileID(), &receipt) != nil {
		receipt = historySourceRemoval{HistoryID: item.executionFileID(), File: assetRemovalFile{Path: item.Source, Snapshot: expected,
			Kind: "source", Quarantine: ".light-media-remove-" + tokenHash(item.executionFileID() + now())[:32] + ".part"}, State: "deleting"}
		if e = a.Store.Put(ctx, "history_source_removal", item.executionFileID(), receipt); e != nil {
			return e
		}
	}
	if receipt.HistoryID != item.executionFileID() || receipt.File.Path != item.Source || receipt.File.Snapshot != expected {
		return bad("HISTORY_SOURCE_RECEIPT", "源文件删除收据不一致")
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
		actual, e = snapshotTransferPath(filepath.Join(filepath.Dir(item.Source), receipt.File.Quarantine))
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
