package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Keep the visible file job through recognition and import. The generation
// check lets cancellation win before reservations or a failure record commit.
func promoteOrganizeFileTx(ctx context.Context, tx *sql.Tx, work organizeFileWork, planID string, fileIDs []string, state, code, reason string) (Job, error) {
	payload, err := json.Marshal(struct {
		ID                      string   `json:"id"`
		FileIDs                 []string `json:"file_ids"`
		ParentJobID             string   `json:"_parent_job_id,omitempty"`
		RequestID               string   `json:"_request_id,omitempty"`
		OriginalScrapeBatch     string   `json:"original_scrape_batch,omitempty"`
		ManualPreparationFailed bool     `json:"manual_preparation_failed,omitempty"`
	}{
		ID:                      planID,
		FileIDs:                 fileIDs,
		ParentJobID:             work.Job.ParentJobID,
		RequestID:               work.Job.RequestID,
		OriginalScrapeBatch:     work.BatchID,
		ManualPreparationFailed: state == "failed",
	})
	if err != nil {
		return Job{}, err
	}
	attempt := 0
	if state == "failed" {
		attempt = 1
	}
	job, err := scanJob(tx.QueryRowContext(ctx, `UPDATE jobs
		SET kind='import', state=?, payload=?, attempt=?, generation=generation+1,
			lease_until=0, error_code=?, error_message=?, updated_at=?
		WHERE id=? AND kind='organize_file' AND state='running' AND generation=?
		RETURNING `+jobColumns, state, payload, attempt, code, reason, now(), work.Job.ID, work.Job.Generation))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, bad("JOB_CANCELLED", "整理文件任务已取消或已由其他执行接管")
	}
	if err == nil {
		err = registerOrganizeQueueTx(ctx, tx, job)
	}
	if err != nil || state != "queued" {
		return job, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO events(time,workflow_id,job_id,level,module,event,message) VALUES(?,?,?,?,?,?,?)`, now(), job.WorkflowID, job.ID, "INFO", "import", "queued", "文件识别完成，等待整理")
	return job, err
}
