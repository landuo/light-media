package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"sync"
)

const originalTransferAliasSchema = `
CREATE TABLE IF NOT EXISTS original_transfer_aliases (
    number INTEGER PRIMARY KEY AUTOINCREMENT CHECK(number > 0),
    history_id TEXT NOT NULL CHECK(history_id <> ''),
    plan_id TEXT NOT NULL CHECK(plan_id <> ''),
    file_id TEXT NOT NULL CHECK(file_id <> ''),
    job_id TEXT NOT NULL CHECK(job_id <> ''),
    execution_key TEXT NOT NULL CHECK(execution_key <> ''),
    generation INTEGER NOT NULL CHECK(generation >= 0),
    UNIQUE(history_id, plan_id, file_id, job_id, execution_key, generation)
);`

// MoviePilot replaces the row for an existing source and assigns a fresh
// number for each result. Native history keeps its stable ID, so the numeric
// chat alias additionally binds the actual failed execution and generation.
type originalTransferRetryAttempt struct {
	HistoryID, PlanID, FileID, JobID, ExecutionKey string
	Generation                                     int
}

type originalTransferRetryAttemptKey struct{}

func originalTransferAttempt(history transferHistoryItem, generation int) originalTransferRetryAttempt {
	return originalTransferRetryAttempt{
		HistoryID: history.ID, PlanID: history.PlanID, FileID: history.executionFileID(),
		JobID: history.JobID, ExecutionKey: history.ExecutionKey, Generation: generation,
	}
}

func originalTransferRetryStale() error {
	return bad("TRANSFER_RETRY_STALE", "这条失败整理记录已变化，请查看最新的整理记录")
}

func (a *App) originalChatTransferAlias(ctx context.Context, history transferHistoryItem) (int64, error) {
	if history.ID == "" || history.Status != "failed" {
		return 0, originalTransferRetryStale()
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err = refreshTransferHistoryIndex(ctx, tx); err != nil {
		return 0, err
	}
	current, err := scanTransferHistory(tx.QueryRowContext(ctx, transferHistoryIndexedBySource, history.ResourceSource))
	if errors.Is(err, sql.ErrNoRows) {
		return 0, originalTransferRetryStale()
	}
	if err != nil {
		return 0, err
	}
	if current.Status != "failed" || originalTransferAttempt(current, 0) != originalTransferAttempt(history, 0) {
		return 0, originalTransferRetryStale()
	}
	var generation int
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM jobs WHERE id=? AND kind='import' AND json_extract(payload,'$.id')=?`, history.JobID, history.PlanID).Scan(&generation); err != nil {
		return 0, err
	}
	attempt := originalTransferAttempt(history, generation)
	values := []any{attempt.HistoryID, attempt.PlanID, attempt.FileID, attempt.JobID, attempt.ExecutionKey, attempt.Generation}
	var number int64
	err = tx.QueryRowContext(ctx, `SELECT number FROM original_transfer_aliases
		WHERE history_id=? AND plan_id=? AND file_id=? AND job_id=? AND execution_key=? AND generation=?`, values...).Scan(&number)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `INSERT INTO original_transfer_aliases(history_id,plan_id,file_id,job_id,execution_key,generation)
			VALUES(?,?,?,?,?,?) RETURNING number`, values...).Scan(&number)
	}
	if err != nil {
		return 0, err
	}
	return number, tx.Commit()
}

func (a *App) originalChatTransferAttempt(ctx context.Context, number int64) (originalTransferRetryAttempt, error) {
	var attempt originalTransferRetryAttempt
	if number <= 0 {
		return attempt, bad("TRANSFER_HISTORY_NOT_FOUND", "整理记录不存在")
	}
	err := a.Store.DB.QueryRowContext(ctx, `SELECT history_id,plan_id,file_id,job_id,execution_key,generation FROM original_transfer_aliases WHERE number=?`, number).
		Scan(&attempt.HistoryID, &attempt.PlanID, &attempt.FileID, &attempt.JobID, &attempt.ExecutionKey, &attempt.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return attempt, bad("TRANSFER_HISTORY_NOT_FOUND", "整理记录不存在")
	}
	return attempt, err
}

func (a *App) originalChatTransferHistory(ctx context.Context, number int64) (transferHistoryItem, error) {
	attempt, err := a.originalChatTransferAttempt(ctx, number)
	if err != nil {
		return transferHistoryItem{}, err
	}
	return a.originalCurrentTransferRetry(ctx, attempt, true)
}

func (a *App) originalCurrentTransferRetry(ctx context.Context, attempt originalTransferRetryAttempt, waitRunning bool) (transferHistoryItem, error) {
	var failureCode string
	read := func() (transferHistoryItem, error) {
		current, err := a.transferHistoryItem(ctx, attempt.HistoryID)
		if errors.Is(err, sql.ErrNoRows) {
			return current, originalTransferRetryStale()
		}
		if err != nil {
			return current, err
		}
		var generation int
		err = a.Store.DB.QueryRowContext(ctx, `SELECT generation,error_code FROM jobs WHERE id=? AND kind='import' AND json_extract(payload,'$.id')=?`, current.JobID, current.PlanID).Scan(&generation, &failureCode)
		if errors.Is(err, sql.ErrNoRows) || err == nil && (current.Status != "failed" || originalTransferAttempt(current, generation) != attempt) {
			return current, originalTransferRetryStale()
		}
		return current, err
	}
	current, err := read()
	if err != nil {
		return current, err
	}
	if waitRunning && current.JobState == "running" {
		// The failure callback writes its history before the import worker
		// closes the job. A promptly clicked card waits for that exact job.
		if _, err := a.awaitOriginalChatSubscriptionJob(ctx, current.JobID); err != nil {
			return current, err
		}
		current, err = read()
		if err != nil {
			return current, err
		}
	}
	if _, err := snapshotTransferPath(current.Source); errors.Is(err, os.ErrNotExist) {
		return current, bad("TRANSFER_SOURCE_MISSING", "源目录不存在")
	}
	preparationFailure := failureCode == "MEDIA_NOT_FOUND" && current.JobState == "failed" && current.FileStatus == "blocked" && current.Target == "" && current.CanRecognize
	if !current.CanRetry && !preparationFailure {
		return current, bad("TRANSFER_RETRY_UNAVAILABLE", "该整理记录当前无法重试")
	}
	return current, nil
}

// Use one admission for chat and the native history retry API. The durable
// import job and current-history check take over as soon as enqueue commits.
var originalTransferRetryAdmissions sync.Map

func (a *App) originalChatRetryTransfer(ctx context.Context, number int64) error {
	return a.originalChatRetryTransferWithMedia(ctx, number, nil)
}

func (a *App) originalChatRetryTransferWithMedia(ctx context.Context, number int64, recognize func(context.Context) (json.RawMessage, error)) error {
	attempt, err := a.originalChatTransferAttempt(ctx, number)
	if err != nil {
		return err
	}
	history, err := a.originalCurrentTransferRetry(ctx, attempt, true)
	if err != nil {
		return err
	}
	job, err := a.submitOriginalTransferRetry(ctx, attempt, recognize)
	if err != nil {
		return err
	}
	state, err := a.awaitOriginalChatSubscriptionJob(ctx, job.ID)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return bad("TRANSFER_RETRY_UNCONFIRMED", "重新整理已提交，但尚未确认结果")
	}
	if state != "succeeded" {
		var message string
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT error_message FROM jobs WHERE id=?`, job.ID).Scan(&message); err != nil {
			return err
		}
		if message = a.clean(message); message == "" {
			message = "整理任务未完成"
		}
		return bad("TRANSFER_RETRY_FAILED", message)
	}
	var published bool
	tx, err := a.transferHistoryReadTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, transferHistoryIndexedBase+`SELECT EXISTS(
		SELECT 1 FROM attempts WHERE job_id=? AND resource_source=? AND status='success')`, job.ID, history.ResourceSource).Scan(&published); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if !published {
		return bad("TRANSFER_RETRY_UNCONFIRMED", "未确认这条整理记录的实际结果")
	}
	return nil
}
