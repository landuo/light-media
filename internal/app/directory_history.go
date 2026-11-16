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
	var state int
	err := db.QueryRowContext(ctx, `SELECT status,COALESCE(json_extract(src_fileitem,'$.plan_id'),''),COALESCE(json_extract(src_fileitem,'$.job_id'),''),COALESCE(errmsg,'') FROM transferhistory WHERE src=? ORDER BY id DESC LIMIT 1`, file.Path).Scan(&state, &planID, &jobID, &reason)
	if err == nil && state == 1 {
		status = "success"
	} else if err == nil {
		status = "failed"
	}
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
