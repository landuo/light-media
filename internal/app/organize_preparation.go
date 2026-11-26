package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type organizePreparationKey struct{}

type organizePreparationPayload struct {
	ID      string                `json:"id"`
	Request manualOrganizeRequest `json:"request"`
}

func (a *App) submitManualOrganize(ctx context.Context, in manualOrganizeRequest) (manualOrganizeOutput, error) {
	out := manualOrganizeOutput{Items: []manualOrganizeItem{}}
	if _, err := normalizeManualOrganize(&in); err != nil {
		return out, err
	}
	return a.enqueueOrganizePreparation(ctx, in, out)
}

// Acknowledge submission once the request is durable, before scanning,
// resolving history or looking up media metadata.
func (a *App) enqueueOrganizePreparation(ctx context.Context, in manualOrganizeRequest, out manualOrganizeOutput) (manualOrganizeOutput, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return out, err
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	job, err := a.enqueueTx(ctx, tx, "organize_prepare", organizePreparationPayload{ID: tokenHash(string(raw)), Request: in}, id("wf_"))
	if err != nil {
		return out, err
	}
	// A normal HTTP submission is already durable when the dialog starts polling.
	if in.Interactive {
		err = in.audit.commit(ctx, tx)
	} else {
		err = tx.Commit()
	}
	if err != nil {
		return out, err
	}
	a.wakeQueuedJob(job.Kind)
	out.PreparationJobID = job.ID
	return out, nil
}

func (a *App) runOrganizePreparation(ctx context.Context, job Job) error {
	var payload organizePreparationPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || !payload.Request.Background || payload.Request.Preview {
		return bad("INVALID_JOB", "整理准备参数无效")
	}
	if err := a.jobCancelled(ctx, job); err != nil {
		return err
	}
	var out manualOrganizeOutput
	err := a.Store.Get(ctx, "organize_file_listing", job.ID, &out)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		ctx = context.WithValue(ctx, organizePreparationKey{}, job)
		if len(payload.Request.HistoryIDs) > 0 {
			out, err = a.prepareOrganizeHistory(ctx, job, payload.Request)
		} else {
			out, err = a.manualOrganize(ctx, payload.Request)
		}
		if err != nil {
			return err
		}
	case err != nil:
		return err
	case out.Items == nil:
		// Older committed preparations only stored an empty completion marker.
		return nil
	}
	if out.Submitted == 0 && out.Failed > 0 {
		for _, item := range out.Items {
			if item.Status == "failed" {
				return bad("REVIEW_ORGANIZE_PREPARATION", item.Error)
			}
		}
	}
	if !payload.Request.Interactive && out.Submitted == 0 && out.Total > 0 {
		return bad("REVIEW_ORGANIZE_PREPARATION", "未找到符合整理条件的文件")
	}
	for _, item := range out.Items {
		if item.Status == "failed" {
			a.event(ctx, job, "WARN", job.Kind, "file_preparation_failed", item.Error)
		}
	}
	a.event(ctx, job, "INFO", job.Kind, "prepared", fmt.Sprintf("已提交 %d 项整理任务，%d 项准备失败", out.Submitted, out.Failed))
	return nil
}

// Each child is saved with its preparation ID in the same transaction as its
// plan. Resuming the parent must skip both paths and original history sources,
// since a completed reorganization can change the latest attempt's path.
func (a *App) preparedOrganizeSources(ctx context.Context, jobID string, out *manualOrganizeOutput) (paths, historySources map[string]bool, err error) {
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT json_extract(p.payload,'$.files[0].source'),
			COALESCE(json_extract(p.payload,'$.files[0].history_source'),''), j.id,
			COALESCE(json_extract(j.payload,'$.manual_preparation_failed'),0), j.error_message
		FROM jobs j JOIN import_plan p ON p.id=json_extract(j.payload,'$.id')
		WHERE j.kind='import' AND json_extract(j.payload,'$._parent_job_id')=?`, jobID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	paths, historySources = map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var item manualOrganizeItem
		var historySource string
		var failed bool
		if err := rows.Scan(&item.Source, &historySource, &item.JobID, &failed, &item.Error); err != nil {
			return nil, nil, err
		}
		paths[item.Source] = true
		if historySource != "" {
			historySources[historySource] = true
		}
		if failed {
			item.Status = "failed"
			out.Failed++
		} else {
			item.Status, item.Error = "queued", ""
			out.Submitted++
		}
		out.Items = append(out.Items, item)
	}
	return paths, historySources, rows.Err()
}

func guardOrganizePreparation(ctx context.Context, tx *sql.Tx) error {
	job, ok := ctx.Value(organizePreparationKey{}).(Job)
	if !ok {
		return nil
	}
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE id=? AND state='running' AND generation=?)`, job.ID, job.Generation).Scan(&active)
	if err != nil {
		return err
	}
	if !active {
		return bad("JOB_CANCELLED", "整理准备已取消")
	}
	return nil
}

// The dialog polls just the live state, the prepared file IDs and transfer
// receipts. General job details expose the same data beside the workflow log.
func (a *App) organizeJobResult(ctx context.Context, j Job, result map[string]any) (map[string]any, error) {
	if j.Kind == "organize_prepare" {
		var preparation manualOrganizeOutput
		err := a.Store.Get(ctx, "organize_file_listing", j.ID, &preparation)
		if err == nil && preparation.Items != nil {
			result["preparation"] = preparation
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	if j.Kind == "import" {
		transfer, err := a.originalTransferJobResult(ctx, j)
		if err != nil {
			return nil, err
		}
		result["transfer"] = transfer
	}
	return result, nil
}
