package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"

	"light-media/internal/plugins"
	"light-media/internal/releasemeta"
)

type organizeFileKey struct{}

type organizeFileWork struct {
	Job     Job
	File    importScanFile
	Stamp   importSnapshot
	BatchID string
}

type organizeFilePayload struct {
	ID               string                  `json:"id"`
	File             importScanFile          `json:"file"`
	Snapshot         importSnapshot          `json:"snapshot"`
	MetadataPath     string                  `json:"metadata_path,omitempty"`
	Meta             json.RawMessage         `json:"meta,omitempty"`
	Item             *plugins.ModuleFileItem `json:"item,omitempty"`
	DirectoryPlanned bool                    `json:"directory_planned,omitempty"`
	MaterialKind     string                  `json:"material_kind,omitempty"`
	Title            string                  `json:"title,omitempty"`
	BatchID          string                  `json:"original_scrape_batch"`
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
			job, storeErr := a.recordManualOrganizeFailure(ctx, file.Path, parent.WorkflowID, err)
			if storeErr != nil {
				return out, storeErr
			}
			out.Items = append(out.Items, manualOrganizeItem{Source: file.Path, Status: "failed", Error: a.clean(err.Error()), JobID: job.ID})
			out.Failed++
			continue
		}
		candidate := organizeFilePayload{
			ID: tokenHash(parent.ID + "\x00" + file.Path), File: file, Snapshot: stamp,
			MetadataPath: file.originalMetadataPath, Item: file.originalItem,
			DirectoryPlanned: file.originalDirectoryPlanned, MaterialKind: file.materialKind,
			Title: file.Parsed.Title, BatchID: batchID,
		}
		if file.originalResolvedMeta != nil {
			candidate.Meta, err = file.originalResolvedMeta.EncodeState()
			if err != nil {
				return out, err
			}
			if name := file.originalResolvedMeta.Name(); name != "" {
				candidate.Title = name
			}
		}
		for _, path := range []string{filepath.Dir(file.Path), filepath.Dir(file.originalMetadataPath)} {
			if entries, ok := file.originalDirectoryListings[path]; ok {
				listings[path] = entries
			}
		}
		candidates = append(candidates, candidate)
	}
	if len(candidates) == 0 {
		out.Total = len(out.Items)
		return out, nil
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = guardOrganizePreparation(ctx, tx); err != nil {
		return out, err
	}
	if err = txPut(ctx, tx, "organize_file_listing", parent.ID, struct{}{}); err != nil {
		return out, err
	}
	for path, entries := range listings {
		if err = txPut(ctx, tx, "organize_file_listing", organizeListingKey(parent.ID, path), entries); err != nil {
			return out, err
		}
	}
	if err = txPut(ctx, tx, "original_scrape_batch", batchID, originalScrapeBatch{ID: batchID, Closed: true}); err != nil {
		return out, err
	}
	for _, candidate := range candidates {
		if _, err = enqueueTx(ctx, tx, "organize_file", candidate, parent.WorkflowID); err != nil {
			return out, err
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	out.Submitted += len(candidates)
	out.Total = len(out.Items) + len(candidates)
	select {
	case a.jobWake <- struct{}{}:
	default:
	}
	return out, nil
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
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,lease_until=0,updated_at=?
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
	events, err := flushOriginalScrapeBatchTx(ctx, tx, originalScrapeBatchID(job), false)
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
	if err := json.Unmarshal(job.Payload, &item); err != nil || item.File.Path == "" || job.ParentJobID == "" || item.BatchID == "" {
		return bad("INVALID_JOB", "目录整理文件参数无效")
	}
	var rawRequest []byte
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM jobs WHERE id=? AND kind='organize_prepare'`, job.ParentJobID).Scan(&rawRequest); err != nil {
		return err
	}
	var parent organizePreparationPayload
	if err := json.Unmarshal(rawRequest, &parent); err != nil {
		return bad("INVALID_JOB", "目录整理参数无效")
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
	file.originalDirectoryListings = map[string][]map[string]any{}
	for _, path := range []string{filepath.Dir(file.Path), filepath.Dir(file.originalMetadataPath)} {
		if _, seen := file.originalDirectoryListings[path]; seen {
			continue
		}
		var listing json.RawMessage
		err := a.Store.Get(ctx, "organize_file_listing", organizeListingKey(job.ParentJobID, path), &listing)
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
	ctx = context.WithValue(ctx, organizeFileKey{}, organizeFileWork{Job: job, File: file, Stamp: item.Snapshot, BatchID: item.BatchID})
	ctx = context.WithValue(ctx, originalScrapeBatchKey{}, item.BatchID)
	fail := func(cause error) error {
		_, err := a.recordManualOrganizeFailure(ctx, file.Path, job.WorkflowID, cause)
		return err
	}
	if err := a.jobCancelled(ctx, job); err != nil {
		return err
	}
	if !a.importSourceAllowed(file.Path, true) || browserInternal(file.Path) {
		return fail(bad("REVIEW_SOURCE_CHANGED", "源文件不在允许目录内"))
	}
	stamp, err := snapshotTransferPath(file.Path)
	if err != nil || stamp != item.Snapshot {
		return fail(bad("REVIEW_SOURCE_CHANGED", "源文件已移除、发生变化或不在允许目录内"))
	}
	out, err := a.manualOrganize(ctx, parent.Request)
	if err != nil {
		if ctx.Err() != nil || organizeFileInterrupted(err) {
			return err
		}
		return fail(err)
	}
	if out.Submitted == 0 && out.Failed == 0 {
		return fail(bad("REVIEW_ORGANIZE_SELECTION", "文件不符合本次整理条件"))
	}
	return nil
}

func organizeFileInterrupted(err error) bool {
	var api *APIError
	return errors.As(err, &api) && (api.Code == "JOB_CANCELLED" || api.Code == "AUTOMATION_PAUSED")
}
