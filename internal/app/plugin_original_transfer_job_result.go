package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// The Go queue owns one import job, whereas the pinned transfer loop reports
// each video/subtitle/audio task. Project the actual frozen tasks and receipts
// through the existing job detail, so a failed extra cannot mark its successful
// main or other extras as failed in the manual dialog.
func (a *App) originalTransferJobResult(ctx context.Context, job Job) (*manualOrganizeOutput, error) {
	if job.Kind != "import" {
		return nil, nil
	}
	var payload struct {
		ID      string   `json:"id"`
		FileIDs []string `json:"file_ids"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, err
	}
	var plan ImportPlan
	if err := a.Store.Get(ctx, "import_plan", payload.ID, &plan); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var meta importMetadata
	if err := a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	out := &manualOrganizeOutput{Items: []manualOrganizeItem{}}
	terminal := job.State == "failed" || job.State == "cancelled" || job.State == "needs_review" || job.State == "succeeded"
	for _, primary := range plan.Files {
		selected := len(payload.FileIDs) == 0
		for _, id := range payload.FileIDs {
			selected = selected || id == primary.ID
		}
		if !selected {
			continue
		}
		tasks := []PlanFile{primary}
		for i, side := range meta.Sidecars[primary.ID] {
			tasks = append(tasks, originalTransferSideFile(primary, side, i))
		}
		var failures originalTransferTaskFailures
		if err := a.Store.Get(ctx, "original_transfer_task_failures", plan.ID+"/"+primary.ID, &failures); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		for _, task := range tasks {
			item := manualOrganizeItem{Source: task.Source, Target: task.Target, Media: plan.Media, Mode: task.Method, PlanID: plan.ID, JobID: job.ID, Status: "queued"}
			if job.State == "running" {
				item.Status = "running"
			}
			key := plan.ID + "/" + task.ID
			var callback originalTransferCallback
			if err := a.Store.Get(ctx, "original_transfer_callback", key, &callback); err == nil {
				item.Target, item.Mode = callback.File.Target, callback.File.Method
				if callback.File.Status == "imported" {
					item.Status = "success"
				} else {
					item.Status, item.Error = "failed", callback.File.Reason
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			} else {
				var receipt importReceipt
				err := a.Store.Get(ctx, "import_receipt", key, &receipt)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return nil, err
				}
				if err == nil && receipt.State == "published" {
					item.Status, item.Target = "success", receipt.Target
				} else if job.State == "succeeded" && task.ID == primary.ID && primary.Status == "already_imported" {
					// A verified repeat uses the existing asset and has no new
					// publication receipt. Its completed job is still successful.
					item.Status = "success"
				} else if terminal || err == nil && len(receipt.OriginalModuleResult) > 0 {
					item.Status, item.Error = "failed", job.ErrorMessage
					if task.ID == primary.ID && primary.Reason != "" {
						item.Error = primary.Reason
					}
					if item.Error == "" {
						item.Error = "文件整理未完成"
					}
				}
			}
			for _, pending := range failures.Pending {
				if pending.FileID == task.ID {
					item.Status, item.Error = "failed", job.ErrorMessage
					if item.Error == "" {
						item.Error = "整理回调尚未完成"
					}
				}
			}
			if item.Status == "failed" {
				out.Failed++
			}
			out.Items = append(out.Items, item)
		}
	}
	out.Total = len(out.Items)
	out.Submitted = out.Total
	return out, nil
}
