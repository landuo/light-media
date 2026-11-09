package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
)

func (a *App) startImportRun(ctx context.Context, job Job, plan *ImportPlan, selected []int) error {
	if err := a.jobCancelled(ctx, job); err != nil {
		return err
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE id=? AND state='running' AND generation=?)`,
		job.ID, job.Generation).Scan(&active); err != nil {
		return err
	}
	if !active {
		return bad("JOB_CANCELLED", "任务已取消")
	}
	// The generic job retry can bypass submission. Check or reacquire its
	// reservation before it can publish after a same-source handoff.
	for _, index := range selected {
		file := plan.Files[index]
		if file.Status == "imported" || file.Status == "already_imported" {
			continue
		}
		if err := a.importReserve(ctx, tx, *plan, file); err != nil {
			return err
		}
	}
	plan.State = "running"
	if err := txPut(ctx, tx, "import_plan", plan.ID, *plan); err != nil {
		return err
	}
	return tx.Commit()
}

// A new local execution may take over an abandoned slot for the exact same
// source. The old execution and its receipts remain available for history.
func canReplaceFailedImportReservation(ctx context.Context, q qualityReader, owner, key string, file PlanFile, episode int) (bool, error) {
	var old ImportPlan
	err := qualityRecord(ctx, q, "import_plan", owner, &old)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // Download intents own their reservations independently.
	}
	if err != nil {
		return false, err
	}
	if old.ID != owner || old.IntentID != "" || mediaKey(old.Media) != key || (old.State != "partial" && old.State != "failed") {
		return false, nil
	}
	return failedImportSourceUnpublished(ctx, q, old, file, episode)
}

// Shared by local retries and a completed download's own manual retry. Neither
// may discard a receipt that could already have published or replaced a file.
func failedImportSourceUnpublished(ctx context.Context, q qualityReader, old ImportPlan, file PlanFile, episode int) (bool, error) {
	var retryable PlanFile
	for _, previous := range old.Files {
		if previous.Season != file.Season || previous.MaterialKind != "" || !slices.Contains(importEpisodes(previous.Episodes), episode) {
			continue
		}
		if retryable.ID != "" || previous.Status != "error" || previous.Source != file.Source || planSnapshot(previous) != planSnapshot(file) || previous.DirectoryDigest != "" || !importPath(previous.Target) || previous.Target == previous.Source {
			return false, nil
		}
		retryable = previous
	}
	if retryable.ID == "" {
		return false, nil
	}
	var failed, active bool
	if err := q.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM jobs WHERE kind='import' AND json_extract(payload,'$.id')=? AND state='failed'
			AND (COALESCE(json_array_length(payload,'$.file_ids'),0)=0 OR EXISTS(SELECT 1 FROM json_each(payload,'$.file_ids') WHERE value=?))),
		EXISTS(SELECT 1 FROM jobs WHERE kind='import' AND json_extract(payload,'$.id')=? AND state IN ('queued','running','retry_wait','waiting_paused'))`,
		old.ID, retryable.ID, old.ID).Scan(&failed, &active); err != nil || !failed || active {
		return false, err
	}
	want := originalTransferPlannedReceipt(old, retryable)
	var receipt importReceipt
	err := qualityRecord(ctx, q, "import_receipt", want.ID, &receipt)
	if err == nil {
		if receipt.ID != want.ID || receipt.Source != want.Source || receipt.Target != want.Target || receipt.SourceSnapshot != want.SourceSnapshot ||
			receipt.Stage != want.Stage || receipt.State != "planned" || receipt.StageSnapshot != (importSnapshot{}) || receipt.SHA256 != "" ||
			receipt.ReusedHardlink || receipt.OriginalModuleStarted || len(receipt.OriginalModuleResult) != 0 || receipt.OriginalNativeInterrupted || receipt.DirectoryListed || receipt.DirectoryException != "" {
			return false, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var pending bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM records WHERE
		(kind='directory_overwrite' AND id=?) OR
		(kind IN ('directory_overwrite','import_receipt') AND instr(id,?)=1) OR
		(kind='managed_file' AND id=?))`, want.ID, want.ID+"/", tokenHash(want.Target)).Scan(&pending); err != nil || pending {
		return false, err
	}
	if stamp, err := snapshotPath(want.Source); err != nil || stamp != want.SourceSnapshot {
		return false, nil
	}
	_, targetErr := snapshotPath(want.Target)
	_, stageErr := snapshotPath(filepath.Join(filepath.Dir(want.Target), want.Stage))
	return errors.Is(targetErr, os.ErrNotExist) && errors.Is(stageErr, os.ErrNotExist), nil
}
