package app

import (
	"context"
	"database/sql"
	"errors"
)

// The pinned manual loop reports the batch as failed while retaining each
// successful physical task. A committed main file cannot erase an auxiliary
// rejection when the host resumes the same frozen operation after restart.
type originalTransferTaskFailures struct {
	Messages []string                          `json:"messages"`
	Pending  []originalTransferPendingCallback `json:"pending,omitempty"`
}

type originalTransferPendingCallback struct {
	FileID  string `json:"file_id"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func (a *App) originalTransferPriorTaskFailure(ctx context.Context, job Job, plan ImportPlan, file PlanFile, meta importMetadata) error {
	var failure originalTransferTaskFailures
	key := plan.ID + "/" + file.ID
	if err := a.Store.Get(ctx, "original_transfer_task_failures", key, &failure); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	var failures []error
	remaining := []originalTransferPendingCallback{}
	for _, pending := range failure.Pending {
		selected := file
		source, found := meta.OriginalTransfer[file.ID]
		if pending.FileID != file.ID {
			found = false
			for i, side := range meta.Sidecars[file.ID] {
				extra := originalTransferSideFile(file, side, i)
				if extra.ID == pending.FileID && side.Original != nil {
					selected, source, found = extra, *side.Original, true
					break
				}
			}
		}
		if !found {
			return bad("REVIEW_RECEIPT", "待恢复的整理回调不属于原计划")
		}
		if err := a.completeOriginalTransferCallback(ctx, job, plan, file.ID, selected, source, pending.Success, pending.Message); err != nil {
			remaining = append(remaining, pending)
			failures = append(failures, &originalTransferCallbackError{err})
		}
	}
	if len(remaining) != len(failure.Pending) {
		failure.Pending = remaining
		if err := a.Store.Put(ctx, "original_transfer_task_failures", key, failure); err != nil {
			return err
		}
	}
	for _, message := range failure.Messages {
		failures = append(failures, bad("PLUGIN_TRANSFER_FAILED", message))
	}
	return errors.Join(failures...)
}
