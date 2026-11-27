package app

import (
	"context"
	"encoding/json"
	"fmt"
)

func jobCompletionLog(j Job, state, code, reason string) (level, event, message string) {
	action := jobActionLabel(j.Kind)
	switch state {
	case "succeeded":
		return "INFO", "completed", action + "：已完成"
	case "cancelled":
		return "INFO", code, action + "：已取消，" + reason
	case "waiting_paused":
		return "INFO", code, action + "：等待自动化恢复，" + reason
	case "queued", "retry_wait":
		level := "WARN"
		if code == "PLUGIN_REFRESH_PENDING" {
			level = "DEBUG"
		}
		return level, code, action + "：已重新排队，" + reason
	default:
		return "ERROR", code, action + "失败：" + reason
	}
}

func (a *App) finishJob(ctx context.Context, j Job, state, code, message string, runAt int64) (bool, error) {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	result, e := tx.ExecContext(ctx, `UPDATE jobs SET state=?,run_at=?,error_code=?,error_message=?,updated_at=?,attempt=CASE WHEN (?='waiting_paused' OR (?='retry_wait' AND ?='PLUGIN_REFRESH_PENDING')) AND attempt>0 THEN attempt-1 ELSE attempt END WHERE id=? AND generation=? AND state='running'`, state, runAt, code, message, now(), state, state, code, j.ID, j.Generation)
	if e != nil {
		return false, e
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return false, tx.Commit()
	}
	if state == "succeeded" && j.Kind == "subscription" {
		if e = a.finishOriginalChatRSSQualityRootTx(ctx, tx, j); e != nil {
			return false, e
		}
	}
	if state == "failed" && code == originalChatAddRejectedCode {
		if e := a.failOriginalChatDownloadTx(ctx, tx, j, message); e != nil {
			return false, e
		}
	}
	level, event, description := jobCompletionLog(j, state, code, message)
	if _, e = tx.ExecContext(ctx, `INSERT INTO events(time,workflow_id,job_id,level,module,event,message) VALUES(?,?,?,?,?,?,?)`, now(), j.WorkflowID, j.ID, level, j.Kind, event, description); e != nil {
		return false, e
	}
	if state == "failed" {
		if e = recordDomainEventTx(ctx, tx, DomainEvent{SchemaVersion: 1, EventID: domainEventID("job.failed", fmt.Sprintf("%s:%d", j.ID, j.Attempt)), Type: "job.failed", OccurredAt: now(), WorkflowID: j.WorkflowID, JobID: j.ID, ParentJobID: j.ParentJobID, CausationID: j.RequestID, Purpose: materialPurpose}); e != nil {
			return false, e
		}
	}
	var events []json.RawMessage
	if j.Kind == "import" || j.Kind == "organize_file" || j.Kind == "reorganize_cleanup" || j.Kind == "quality_cleanup" {
		events, e = a.flushOriginalScrapeBatchTx(ctx, tx, originalScrapeBatchID(j), false)
		if e != nil {
			return false, e
		}
	}
	if e = tx.Commit(); e != nil {
		return false, e
	}
	// The index follows the row: a retry re-arms the job, a terminal state drops
	// it. Without this the worker would keep seeing work that no longer exists.
	if j2 := j; true {
		j2.State = state
		j2.RunAt = runAt
		j2.UpdatedAt = now()
		a.indexRefresh(j2)
	}
	if err := a.finishOriginalTransferTorrent(ctx, j, state); err != nil {
		a.log("WARN", "imports", "torrent_completion_check_failed", a.clean(err.Error()), j.WorkflowID, j.ID)
	}
	a.broadcastOriginalScrapeBatch(ctx, events)
	return true, nil
}
