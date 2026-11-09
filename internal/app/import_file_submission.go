package app

import (
	"context"
	"encoding/json"
	"errors"
)

type importSubmissionKey struct{}

func importSelectionJSON(plan ImportPlan, selected []int) string {
	ids := make([]string, 0, len(selected))
	for _, i := range selected {
		ids = append(ids, plan.Files[i].ID)
	}
	raw, _ := json.Marshal(ids)
	return string(raw)
}

// A completed download keeps its frozen plan and ownership, but each file gets
// its own execution. Resume fills only missing submissions, never retries a
// terminal result merely because the downloader is polled again.
func (a *App) enqueueCompletedImportFiles(ctx context.Context, plan ImportPlan) error {
	// Publish the pack's submissions before any of its workers can mutate the
	// shared plan. Interrupted submission resumes from the already saved jobs.
	a.importMu.Lock()
	defer a.importMu.Unlock()
	ctx = context.WithValue(ctx, importSubmissionKey{}, true)
	var failures []error
	for _, file := range plan.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		var submitted bool
		err := a.Store.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE kind='import' AND json_extract(payload,'$.id')=? AND
		 (COALESCE(json_array_length(payload,'$.file_ids'),0)=0 OR EXISTS(SELECT 1 FROM json_each(payload,'$.file_ids') WHERE value=?)))`, plan.ID, file.ID).Scan(&submitted)
		if err != nil {
			return err
		}
		if submitted {
			continue
		}
		if file.Status == "blocked" {
			err = bad("REVIEW_IMPORT_FILE", file.Reason)
		} else {
			_, err = a.executeImport(ctx, plan.ID, []string{file.ID})
		}
		if err == nil {
			continue
		}
		if ctx.Err() != nil || organizeFileInterrupted(err) {
			return err
		}
		var api *APIError
		if !errors.As(err, &api) {
			return err
		} // Storage faults remain recoverable.
		if api.Code == "JOB_EXISTS" {
			continue
		}
		if err := a.recordImportFileSubmissionFailure(ctx, plan.ID, file.ID, err); err != nil {
			return err
		}
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func (a *App) recordImportFileSubmissionFailure(ctx context.Context, planID, fileID string, cause error) error {
	if ctx.Value(importSubmissionKey{}) != true {
		a.importMu.Lock()
		defer a.importMu.Unlock()
	}
	var plan ImportPlan
	if err := a.Store.Get(ctx, "import_plan", planID, &plan); err != nil {
		return err
	}
	for i := range plan.Files {
		if plan.Files[i].ID == fileID {
			plan.Files[i].Status, plan.Files[i].Reason = "blocked", a.clean(cause.Error())
		}
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	job, err := enqueueTx(ctx, tx, "import", map[string]any{"id": plan.ID, "file_ids": []string{fileID}}, plan.WorkflowID)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET state='failed',attempt=1,error_code='REVIEW_IMPORT_FILE',error_message=? WHERE id=?`, a.clean(cause.Error()), job.ID); err != nil {
		return err
	}
	if err = txPut(ctx, tx, "import_plan", plan.ID, plan); err != nil {
		return err
	}
	return tx.Commit()
}
