package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"light-media/internal/download"

	"golang.org/x/sys/unix"
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
	}
	if in.DeleteSource && sourceExists {
		if e := a.removeHistorySource(ctx, item, sourceSnapshot); e != nil {
			return nil, e
		}
	}
	if in.DeleteSource {
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
	_, statErr := snapshotPath(item.Target)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	var plan ImportPlan
	if e := a.Store.Get(ctx, "import_plan", item.PlanID, &plan); e != nil {
		return e
	}
	var historical PlanFile
	for _, file := range plan.Files {
		if file.ID == item.executionFileID() {
			historical = file
			break
		}
	}
	if historical.AssetID == "" {
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		return bad("HISTORY_TARGET_UNMANAGED", "此历史没有可核验的目标资产，不能删除目标文件")
	}
	var owned managedFile
	ownedErr := a.Store.Get(ctx, "managed_file", tokenHash(item.Target), &owned)
	if ownedErr == nil && (owned.Kind != "video" || historical.Target != item.Target || historical.AssetID != owned.AssetID ||
		owned.RootID != plan.RootID || owned.SourceSnapshot != planSnapshot(historical)) {
		return bad("HISTORY_TARGET_CHANGED", "此路径已由其他整理结果接管，不能按旧历史删除")
	}
	if ownedErr != nil && statErr == nil {
		return bad("HISTORY_TARGET_UNMANAGED", "目标文件没有可核验的整理归属，不能从历史中删除")
	}
	var asset Asset
	if e := a.Store.Get(ctx, "asset", historical.AssetID, &asset); e != nil {
		return e
	}
	if asset.Path != item.Target || asset.RootID != plan.RootID {
		return bad("HISTORY_TARGET_CHANGED", "目标资产路径已变化，请重新核对")
	}
	if asset.State == "removed" && errors.Is(statErr, os.ErrNotExist) {
		return nil
	}
	// A stopped asset removal may already have moved the video into its frozen
	// quarantine. Resume its receipt even when the visible target is absent.
	preview, e := a.previewAssetRemoval(ctx, asset)
	if e != nil {
		return e
	}
	return a.removeAsset(ctx, asset.ID, preview.ID, true, false)
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
	var expected importSnapshot
	for _, file := range plan.Files {
		if file.ID == item.executionFileID() {
			expected = planSnapshot(file)
			break
		}
	}
	actual, e := snapshotPath(item.Source)
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
	var busy int
	if e = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM records r,json_each(r.payload,'$.files') f WHERE r.kind='import_plan' AND r.id!=? AND json_extract(r.payload,'$.state') IN ('queued','running') AND json_extract(f.value,'$.source')=?`, item.PlanID, item.Source).Scan(&busy); e != nil {
		return importSnapshot{}, false, e
	}
	if busy > 0 {
		return importSnapshot{}, false, bad("FILE_IN_USE", "其他整理任务正在使用源文件")
	}
	return actual, true, nil
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
		if e = importRenameExclusive(int(parent.Fd()), name, receipt.File.Quarantine); e != nil {
			return e
		}
		actual, e = snapshotPath(filepath.Join(filepath.Dir(item.Source), receipt.File.Quarantine))
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
		receipt.File.Quarantined = true
		if e = a.Store.Put(ctx, "history_source_removal", item.executionFileID(), receipt); e != nil {
			return e
		}
		if e = unix.Unlinkat(int(parent.Fd()), receipt.File.Quarantine, 0); e != nil {
			_ = importRenameExclusive(int(parent.Fd()), receipt.File.Quarantine, name)
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
