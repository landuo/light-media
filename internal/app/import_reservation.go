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
	// A directory observation may already have been promoted to a queued
	// single-file import when a user deletes history. Stop before publishing or
	// taking reservations. Running transfers retain their own recovery path.
	if plan.State == "queued" && !plan.Manual && plan.IntentID == "" && len(plan.Files) == 1 {
		var auto bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM directory_import WHERE id=?)`, plan.ID).Scan(&auto); err != nil {
			return err
		}
		if auto {
			file := plan.Files[0]
			consumed, err := directorySourceHistoryDeleted(ctx, tx, file.Source, planSnapshot(file))
			if err != nil {
				return err
			}
			var skip error
			if consumed {
				skip = bad("DIRECTORY_HISTORY_DELETED", "整理记录已删除，未变化的源文件已跳过")
			} else if info, statErr := os.Lstat(file.Source); errors.Is(statErr, os.ErrNotExist) || (statErr == nil && file.DirectoryDigest == "" && !planSnapshot(file).matches(info)) {
				// A published move can legitimately have no source. Preserve
				// recovery for any operation which already has a receipt.
				var started bool
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM import_receipt WHERE id=?)`, plan.ID+"/"+file.ID).Scan(&started); err != nil {
					return err
				}
				if !started {
					skip = bad("DIRECTORY_SOURCE_GONE", "自动整理的源文件已移除或更换，已跳过")
				}
			}
			if skip != nil {
				if _, err = tx.ExecContext(ctx, `DELETE FROM reservations WHERE intent_id=?`, plan.ID); err != nil {
					return err
				}
				if err = updateImportPlanState(ctx, tx, plan, "cancelled"); err != nil {
					return err
				}
				if err = tx.Commit(); err != nil {
					return err
				}
				return skip
			}
		}
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
	if err := updateImportPlanState(ctx, tx, plan, "running"); err != nil {
		return err
	}
	return tx.Commit()
}

// A queue transition changes only the parent. File projections were recorded
// with the preview and remain valid until a transfer changes a file.
func updateImportPlanState(ctx context.Context, tx *sql.Tx, plan *ImportPlan, state string) error {
	result, err := tx.ExecContext(ctx, `UPDATE import_plan SET payload=CAST(json_set(payload,'$.state',?) AS BLOB),updated_at=? WHERE id=?`, state, now(), plan.ID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	plan.State = state
	return nil
}

// A new local execution may take over an abandoned slot for the exact same
// source. The old execution and its receipts remain available for history.
func canReplaceFailedImportReservation(ctx context.Context, q qualityReader, owner, key string, file PlanFile, season, episode int) (bool, error) {
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
	return failedImportSourceUnpublished(ctx, q, old, file, season, episode, false)
}

// Shared by local retries and a completed download's own manual retry. Neither
// may discard a receipt that could already have published or replaced a file.
func failedImportSourceUnpublished(ctx context.Context, q qualityReader, old ImportPlan, file PlanFile, season, episode int, allowSourceHardlink bool) (bool, error) {
	var retryable PlanFile
	for _, previous := range old.Files {
		if previous.MaterialKind != "" || !slices.Contains(previous.inventorySlots(old.Media.MediaType), canonicalEpisodeSlot{season, episode}) {
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
		EXISTS(SELECT 1 FROM jobs WHERE kind='import' AND json_extract(payload,'$.id')=? AND state IN ('queued','running'))`,
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
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM directory_overwrite WHERE id=?)
	 OR EXISTS(SELECT 1 FROM directory_overwrite WHERE instr(id,?)=1)
	 OR EXISTS(SELECT 1 FROM import_receipt WHERE instr(id,?)=1)
	 OR EXISTS(SELECT 1 FROM managed_file WHERE id=?)`, want.ID, want.ID+"/", tokenHash(want.Target)).Scan(&pending); err != nil || pending {
		return false, err
	}
	if stamp, err := snapshotPath(want.Source); err != nil || stamp != want.SourceSnapshot {
		return false, nil
	}
	target, targetErr := snapshotPath(want.Target)
	_, stageErr := snapshotPath(filepath.Join(filepath.Dir(want.Target), want.Stage))
	// A downloaded file can already have its exact hardlink at the destination
	// without a committed asset. Reuse is safe only for that unchanged inode,
	// the identical destination, no staging residue and no existing owner.
	targetSafe := errors.Is(targetErr, os.ErrNotExist) || allowSourceHardlink && targetErr == nil && file.Target == want.Target && target == want.SourceSnapshot
	return targetSafe && errors.Is(stageErr, os.ErrNotExist), nil
}
