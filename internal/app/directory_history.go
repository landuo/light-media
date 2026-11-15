package app

import (
	"context"
	"database/sql"
	"errors"
)

// MoviePilot skips automatic discovery whenever the source has visible history,
// including failures. Hidden history is absent from current, so deleting it
// releases the source without discarding the internal execution receipts.
func directoryHistoryObservation(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, file *DirectoryObservedFile) (bool, error) {
	var status, planID, jobID, reason string
	err := db.QueryRowContext(ctx, `SELECT status,plan_id,COALESCE(job_id,''),COALESCE(NULLIF(json_extract(file_json,'$.reason'),''),job_error,'') FROM transfer_history_current WHERE resource_source=?`, file.Path).Scan(&status, &planID, &jobID, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	file.PlanID, file.JobID = planID, jobID
	switch status {
	case "success":
		file.State = "imported"
	case "failed":
		file.State = "error"
	default:
		file.State = "queued"
	}
	file.Reason = reason
	if file.Reason == "" {
		file.Reason = "已有整理历史，自动整理跳过"
	}
	return true, nil
}
