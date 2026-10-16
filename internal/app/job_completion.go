package app

import (
	"context"
	"fmt"
)

func (a *App) finishJob(ctx context.Context, j Job, state, code, message string, runAt int64) (bool, error) {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	result, e := tx.ExecContext(ctx, `UPDATE jobs SET state=?,run_at=?,lease_until=0,error_code=?,error_message=?,updated_at=?,attempt=CASE WHEN (?='waiting_paused' OR (?='retry_wait' AND ?='PLUGIN_REFRESH_PENDING')) AND attempt>0 THEN attempt-1 ELSE attempt END WHERE id=? AND generation=? AND state='running'`, state, runAt, code, message, now(), state, state, code, j.ID, j.Generation)
	if e != nil {
		return false, e
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return false, tx.Commit()
	}
	level, event, description := "INFO", "completed", "任务完成"
	if state != "succeeded" {
		level, event, description = "ERROR", code, message
	}
	if code == "PLUGIN_REFRESH_PENDING" {
		level = "INFO"
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO events(time,workflow_id,job_id,level,module,event,message) VALUES(?,?,?,?,?,?,?)`, now(), j.WorkflowID, j.ID, level, j.Kind, event, description); e != nil {
		return false, e
	}
	if state == "failed" {
		if e = recordDomainEventTx(ctx, tx, DomainEvent{SchemaVersion: 1, EventID: domainEventID("job.failed", fmt.Sprintf("%s:%d", j.ID, j.Attempt)), Type: "job.failed", OccurredAt: now(), WorkflowID: j.WorkflowID, JobID: j.ID, ParentJobID: j.ParentJobID, CausationID: j.RequestID, Purpose: materialPurpose}); e != nil {
			return false, e
		}
	}
	return true, tx.Commit()
}
