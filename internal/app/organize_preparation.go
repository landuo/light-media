package app

import (
	"context"
	"database/sql"
	"encoding/json"
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
	if len(in.HistoryIDs) != 0 {
		return a.enqueueOrganizePreparation(ctx, in, out)
	}
	for _, path := range in.Paths {
		if !a.importSourceAllowed(path, true) || browserInternal(path) {
			continue
		}
		info, err := pluginStorageEntryInfo(path)
		if err != nil || !info.IsDir() {
			continue
		}
		return a.enqueueOrganizePreparation(ctx, in, out)
	}
	return a.manualOrganize(ctx, in)
}

// Acknowledge submission once the request is durable, before scanning,
// resolving history or looking up media metadata.
func (a *App) enqueueOrganizePreparation(ctx context.Context, in manualOrganizeRequest, out manualOrganizeOutput) (manualOrganizeOutput, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return out, err
	}
	job, err := a.enqueue(ctx, "organize_prepare", organizePreparationPayload{ID: tokenHash(string(raw)), Request: in}, id("wf_"))
	if err != nil {
		return out, err
	}
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
	var committed bool
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM records WHERE kind='organize_file_listing' AND id=?)`, job.ID).Scan(&committed); err != nil || committed {
		return err
	}
	ctx = context.WithValue(ctx, organizePreparationKey{}, job)
	out, err := a.manualOrganize(ctx, payload.Request)
	if err != nil {
		return err
	}
	if out.Submitted == 0 && out.Failed > 0 {
		for _, item := range out.Items {
			if item.Status == "failed" {
				return bad("REVIEW_ORGANIZE_PREPARATION", item.Error)
			}
		}
	}
	if out.Submitted == 0 && out.Total > 0 {
		return bad("REVIEW_ORGANIZE_PREPARATION", "未找到符合整理条件的文件")
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
		FROM jobs j JOIN records p ON p.kind='import_plan' AND p.id=json_extract(j.payload,'$.id')
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
