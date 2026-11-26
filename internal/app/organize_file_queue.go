package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"light-media/internal/plugins"
	"light-media/internal/releasemeta"
)

type organizeFileKey struct{}

type organizeFileWork struct {
	InteractiveKey       string
	DirectoryObservation bool
	Job                  Job
	File                 importScanFile
	Stamp                importSnapshot
	BatchID              string
	HistorySource        string
}

type organizeFilePayload struct {
	Enumerated          bool                    `json:"enumerated,omitempty"`
	Manual              *manualOrganizeRequest  `json:"manual_request,omitempty"`
	Directory           *directoryImportGuard   `json:"directory,omitempty"`
	HistoryID           string                  `json:"history_id,omitempty"`
	HistoryExecutionKey string                  `json:"history_execution_key,omitempty"`
	HistorySource       string                  `json:"history_source,omitempty"`
	ID                  string                  `json:"id"`
	File                importScanFile          `json:"file"`
	Snapshot            importSnapshot          `json:"snapshot"`
	MetadataPath        string                  `json:"metadata_path,omitempty"`
	Meta                json.RawMessage         `json:"meta,omitempty"`
	Item                *plugins.ModuleFileItem `json:"item,omitempty"`
	DirectoryPlanned    bool                    `json:"directory_planned,omitempty"`
	MaterialKind        string                  `json:"material_kind,omitempty"`
	Title               string                  `json:"title,omitempty"`
	BatchID             string                  `json:"original_scrape_batch"`
}

// Freeze the entire candidate list before any worker can start provider
// recognition. The shared directory listing also marks a committed batch.
func (a *App) enqueueOrganizeFiles(ctx context.Context, parent Job, files []importScanFile, prepared map[string]bool, out manualOrganizeOutput) (manualOrganizeOutput, error) {
	batchID := "scrape_batch_" + parent.ID
	listings := map[string][]map[string]any{}
	candidates := make([]organizeFilePayload, 0, len(files))
	for _, file := range files {
		if prepared[file.Path] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
		stamp, err := snapshotTransferPath(file.Path)
		if err != nil {
			job, storeErr := a.recordManualOrganizeFailure(ctx, file.Path, parent.WorkflowID, err, organizeFileHistorySource(file))
			if storeErr != nil {
				return out, storeErr
			}
			out.Items = append(out.Items, manualOrganizeItem{Source: file.Path, Status: "failed", Error: a.clean(err.Error()), JobID: job.ID})
			out.Failed++
			continue
		}
		candidate, err := frozenOrganizeFile(file, stamp, tokenHash(parent.ID+"\x00"+file.Path), batchID)
		if err != nil {
			return out, err
		}
		for _, path := range []string{filepath.Dir(file.Path), filepath.Dir(file.originalMetadataPath)} {
			if entries, ok := file.originalDirectoryListings[path]; ok {
				listings[path] = entries
			}
		}
		candidates = append(candidates, candidate)
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = guardOrganizePreparation(ctx, tx); err != nil {
		return out, err
	}
	for path, entries := range listings {
		if err = txPut(ctx, tx, "organize_file_listing", organizeListingKey(parent.ID, path), entries); err != nil {
			return out, err
		}
	}
	if len(candidates) > 0 {
		if err = txPut(ctx, tx, "original_scrape_batch", batchID, originalScrapeBatch{ID: batchID, Closed: true}); err != nil {
			return out, err
		}
	}
	for _, candidate := range candidates {
		existing, err := activeOrganizeSourceTx(ctx, tx, candidate.File.Path)
		if err != nil {
			return out, err
		}
		if existing == "" {
			job, err := a.enqueueTx(ctx, tx, "organize_file", candidate, parent.WorkflowID)
			if err != nil {
				return out, err
			}
			existing = job.ID
		}
		out.Items = append(out.Items, manualOrganizeItem{Source: candidate.File.Path, Status: "queued", JobID: existing})
		out.Submitted++
	}
	out.Total = len(out.Items)
	if err = txPut(ctx, tx, "organize_file_listing", parent.ID, out); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	select {
	case a.jobWake <- struct{}{}:
	default:
	}
	return out, nil
}

func frozenOrganizeFile(file importScanFile, stamp importSnapshot, key, batchID string) (organizeFilePayload, error) {
	candidate := organizeFilePayload{
		ID: key, File: file, Snapshot: stamp,
		MetadataPath: file.originalMetadataPath, Item: file.originalItem,
		DirectoryPlanned: file.originalDirectoryPlanned, MaterialKind: file.materialKind,
		Title: file.Parsed.Title, BatchID: batchID,
	}
	if file.queueHistory != nil {
		candidate.HistoryID, candidate.HistoryExecutionKey, candidate.HistorySource = file.queueHistory.ID, file.queueHistory.ExecutionKey, file.queueHistory.ResourceSource
	}
	if file.originalResolvedMeta != nil {
		var err error
		candidate.Meta, err = file.originalResolvedMeta.EncodeState()
		if err != nil {
			return candidate, err
		}
		if name := file.originalResolvedMeta.Name(); name != "" {
			candidate.Title = name
		}
	}
	return candidate, nil
}

func organizeListingKey(batchID, path string) string {
	return tokenHash(batchID + "\x00" + path)
}

func (a *App) cancelOrganizeFile(ctx context.Context, job Job) (any, error) {
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,updated_at=?
		WHERE id=? AND kind='organize_file' AND generation=? AND state IN `+activeOrganizeStates, now(), job.ID, job.Generation)
	if err != nil {
		return nil, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		if err != nil {
			return nil, err
		}
		return nil, bad("QUEUE_FILE_CHANGED", "文件任务已变化，请刷新整理队列后操作")
	}
	events, err := a.flushOriginalScrapeBatchTx(ctx, tx, originalScrapeBatchID(job), false)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	a.broadcastOriginalScrapeBatch(ctx, events)
	return map[string]bool{"cancelled": true}, nil
}

func (a *App) runOrganizeFile(ctx context.Context, job Job) error {
	var item organizeFilePayload
	if err := json.Unmarshal(job.Payload, &item); err != nil || item.File.Path == "" || (job.ParentJobID == "" && item.Directory == nil && item.Manual == nil) || item.BatchID == "" {
		return bad("INVALID_JOB", "目录整理文件参数无效")
	}
	if item.Directory != nil {
		// The row may have been deleted after this observation was queued.
		if consumed, err := directorySourceHistoryDeleted(ctx, a.Store.reader(), item.File.Path, item.Snapshot); err != nil || consumed {
			return err
		}
	}
	var parent organizePreparationPayload
	if item.Manual != nil {
		validSelection := len(item.Manual.Paths) == 1 && item.Manual.Paths[0] == item.File.Path && len(item.Manual.HistoryIDs) == 0
		if item.Enumerated && item.HistoryID != "" {
			validSelection = len(item.Manual.Paths) == 0 && len(item.Manual.HistoryIDs) == 1 && item.Manual.HistoryIDs[0] == item.HistoryID
		} else if item.Enumerated && !validSelection && len(item.Manual.Paths) == 1 && len(item.Manual.HistoryIDs) == 0 {
			actual, err := a.resolveSoftlinkReorganizeSource(ctx, item.Manual.Paths[0])
			validSelection = err == nil && actual == item.File.Path
		}
		if job.ParentJobID != "" || item.Directory != nil || !validSelection {
			return bad("INVALID_JOB", "手动整理任务与提交的源文件不匹配")
		}
		parent.Request = *item.Manual
	} else if item.Directory == nil {
		var rawRequest []byte
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM jobs WHERE id=? AND kind='organize_prepare'`, job.ParentJobID).Scan(&rawRequest); err != nil {
			return err
		}
		if err := json.Unmarshal(rawRequest, &parent); err != nil {
			return bad("INVALID_JOB", "目录整理参数无效")
		}
	}
	file := item.File
	file.originalMetadataPath, file.originalItem = item.MetadataPath, item.Item
	file.originalDirectoryPlanned, file.materialKind = item.DirectoryPlanned, item.MaterialKind
	if len(item.Meta) > 0 {
		var err error
		file.originalResolvedMeta, err = releasemeta.DecodeMetaObjectState(item.Meta)
		if err != nil {
			return bad("INVALID_JOB", "目录整理文件识别数据无效")
		}
	}
	listingID := job.ParentJobID
	if item.Enumerated {
		listingID = item.BatchID
	}
	file.originalDirectoryListings = map[string][]map[string]any{}
	for _, path := range []string{filepath.Dir(file.Path), filepath.Dir(file.originalMetadataPath)} {
		if listingID == "" {
			break // Standalone files have no stored directory listing.
		}
		if _, seen := file.originalDirectoryListings[path]; seen {
			continue
		}
		var listing json.RawMessage
		err := a.Store.Get(ctx, "organize_file_listing", organizeListingKey(listingID, path), &listing)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		decoder := json.NewDecoder(bytes.NewReader(listing))
		decoder.UseNumber()
		var entries []map[string]any
		if err := decoder.Decode(&entries); err != nil {
			return err
		}
		file.originalDirectoryListings[path] = entries
	}
	work := organizeFileWork{DirectoryObservation: item.Directory != nil, Job: job, File: file, Stamp: item.Snapshot, BatchID: item.BatchID, HistorySource: item.HistorySource}
	if item.Manual != nil {
		work.InteractiveKey = item.ID
	}
	ctx = context.WithValue(ctx, organizeFileKey{}, work)
	ctx = context.WithValue(ctx, originalScrapeBatchKey{}, item.BatchID)
	known := ImportPlan{Files: []PlanFile{{Source: file.Path}}}
	known.Media.Title, known.Media.Year = file.Parsed.Title, file.Parsed.Year
	if numbering := file.Parsed.Original; numbering != nil {
		if numbering.Season != nil {
			known.Files[0].Season = *numbering.Season
		}
		known.Files[0].Episodes = append([]int{}, numbering.Episodes...)
	}
	if item.Directory != nil {
		known.Mode, known.RootID = item.Directory.Rule.Mode, item.Directory.Rule.RootID
	}
	fail := func(cause error) error {
		if item.Directory != nil && a.sourceAllowed(file.Path) {
			// Recognition can outlast the local file it was started for.
			if _, statErr := os.Lstat(file.Path); errors.Is(statErr, os.ErrNotExist) {
				return bad("DIRECTORY_SOURCE_GONE", "自动整理的源文件已移除，已跳过")
			}
		}
		_, err := a.recordManualOrganizeFailurePlan(ctx, file.Path, job.WorkflowID, cause, item.HistorySource, known)
		return err
	}
	if err := a.jobCancelled(ctx, job); err != nil {
		return err
	}
	if item.HistoryID != "" {
		selected, err := a.transferHistoryItemsForOrganize(ctx, []string{item.HistoryID})
		if err != nil || selected[0].ExecutionKey != item.HistoryExecutionKey {
			return bad("REVIEW_ORGANIZE_HISTORY", "所选整理历史已变化，请刷新后重新选择")
		}
	}
	if !a.importSourceAllowed(file.Path, true) || browserInternal(file.Path) {
		return fail(bad("INVALID_SOURCE", "源路径不在当前允许整理的目录内，请检查目录设置"))
	}
	disc := item.Snapshot.Directory != "" || file.isOriginalDisc()
	var stamp importSnapshot
	var entries []originalDirectoryEntry
	var err error
	if disc && item.Directory == nil {
		stamp, entries, err = snapshotOriginalDirectory(file.Path)
	} else {
		stamp, err = snapshotTransferPath(file.Path)
	}
	if item.Directory != nil && (errors.Is(err, os.ErrNotExist) || err == nil && stamp != item.Snapshot) {
		// This automatic event no longer describes a file to organize. The
		// monitor will observe the replacement separately, if one exists.
		return bad("DIRECTORY_SOURCE_GONE", "自动整理的源文件已移除或更换，已跳过")
	}
	if err != nil {
		return fail(queuedSourceReadError(file.Path, disc, err))
	}
	if stamp != item.Snapshot {
		if disc {
			if stamp.Device != item.Snapshot.Device || stamp.Inode != item.Snapshot.Inode {
				return fail(bad("REVIEW_SOURCE_CHANGED", "入队时选择的蓝光原盘目录已被替换，请重新选择目录"))
			}
			return fail(a.changedDiscSource("蓝光原盘内容在入队后发生变化", entries))
		}
		return fail(bad("REVIEW_SOURCE_CHANGED", "源文件在入队后发生变化，请重新选择文件整理"))
	}
	if item.Directory != nil {
		return a.runDirectoryOrganizeFile(ctx, item, file, &known, fail)
	}
	if !item.Enumerated && (item.HistoryID != "" || item.Manual != nil) {
		if item.HistoryID != "" {
			parent.Request.HistoryIDs = []string{item.HistoryID}
			parent.Request.Paths = nil
		}
		// Auxiliary material and plugin metadata are resolved by this file's
		// worker; a corrupt history or failed provider cannot stop other files.
		scan, err := a.manualOrganizeScan(ctx, file.Path, parent.Request)
		if err != nil {
			return fail(err)
		}
		files := scan.(map[string]any)["files"].([]importScanFile)
		if len(files) != 1 || files[0].Path != file.Path {
			if item.HistoryID != "" {
				return fail(bad("REVIEW_ORGANIZE_HISTORY", "所选整理历史不再对应同一源文件"))
			}
			return fail(bad("REVIEW_ORGANIZE_SELECTION", "所选源路径未对应一个可整理的文件"))
		}
		file = files[0]
		work.File = file
		ctx = context.WithValue(ctx, organizeFileKey{}, work)
	}
	out, err := a.manualOrganize(ctx, parent.Request)
	if err != nil {
		if ctx.Err() != nil || organizeFileInterrupted(err) {
			return err
		}
		return fail(err)
	}
	for _, item := range out.Items {
		if item.Status == "failed" && item.JobID == "" {
			// A rejected history cleanup deliberately leaves its previous success
			// intact. The selected queue item must still report that rejection.
			code := item.ErrorCode
			if code == "" {
				code = "REVIEW_ORGANIZE_SELECTION"
			}
			return bad(code, item.Error)
		}
	}
	if out.Submitted == 0 && out.Failed == 0 {
		if parent.Request.Interactive && len(out.Items) == 1 && out.Items[0].Status == "skipped" {
			return bad("ORGANIZE_FILE_SKIPPED", out.Items[0].Error)
		}
		return fail(bad("REVIEW_ORGANIZE_SELECTION", "文件不符合本次整理条件"))
	}
	return nil
}

func queuedSourceReadError(path string, disc bool, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		if disc {
			// A disappearing child during a recursive walk is not evidence
			// that the selected disc root was removed.
			if _, statErr := os.Lstat(path); statErr == nil {
				return bad("REVIEW_SOURCE_CHANGED", "蓝光原盘成员在检查时发生变化，请确认下载完成，再重新整理")
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return bad("INVALID_FILE", "无法读取所选蓝光原盘，请检查源目录和读取权限")
			}
		}
		return bad("TRANSFER_SOURCE_MISSING", "入队时选择的源路径已不存在，请检查下载目录")
	}
	if errors.Is(err, os.ErrPermission) {
		return bad("INVALID_FILE", "无法读取所选源文件或目录，请检查读取权限")
	}
	var api *APIError
	if errors.As(err, &api) {
		return api
	}
	return bad("INVALID_FILE", "无法读取所选源文件或目录，请检查文件类型和读取权限")
}

func organizeFileInterrupted(err error) bool {
	var api *APIError
	return errors.As(err, &api) && (api.Code == "JOB_CANCELLED" || api.Code == "AUTOMATION_PAUSED")
}
