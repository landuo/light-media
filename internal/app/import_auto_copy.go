package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"time"
)

// Only an actual failed source-to-stage Linkat produces this one-use local
// capability. A public APIError/string or a JSON receipt cannot authorize a new
// transfer method. No successful/uncertain publication can enter this branch.
type importEXDEVFailure struct {
	app     *App
	receipt importReceipt
	cause   *APIError
	used    atomic.Bool
}

func (e *importEXDEVFailure) Error() string { return e.cause.Error() }
func (e *importEXDEVFailure) Unwrap() error { return e.cause }

type importAutoCopyReceipt struct {
	Version                               int
	Plan, File, Job                       string
	Generation                            int
	SourceReceipt                         importReceipt
	BeforeInputSHA, AfterInputSHA         string
	BeforeOperationSHA, AfterOperationSHA string
}

type importAutoCopyUnknown struct{ cause error }

func (e *importAutoCopyUnknown) Error() string {
	return "自动复制方式事务无法确认，已保留持久状态供核对：" + e.cause.Error()
}
func (e *importAutoCopyUnknown) Unwrap() error { return e.cause }

// Commit acknowledgements do not erase a real local transaction. Read only its
// complete exact closure; no current-policy check or new filesystem action is
// authorized by this acknowledgement recovery.
func (a *App) knownImportAutoCopy(ctx context.Context, plan ImportPlan, meta importMetadata, op *directoryOverwriteOperation, quality *qualityImportProof, want importAutoCopyReceipt) error {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var proof importAutoCopyReceipt
	if e = a.readQualitySealed(ctx, tx, "import_auto_copy", want.Plan+"/"+want.File, &proof); e != nil {
		return e
	}
	if !reflect.DeepEqual(proof, want) {
		return errors.New("auto transition receipt differs")
	}
	var current ImportPlan
	var currentMeta importMetadata
	var receipt importReceipt
	if e = qualityRecord(ctx, tx, "import_plan", plan.ID, &current); e != nil {
		return e
	}
	if e = qualityRecord(ctx, tx, "import_meta", plan.ID, &currentMeta); e != nil {
		return e
	}
	if e = qualityRecord(ctx, tx, "import_receipt", want.SourceReceipt.ID, &receipt); e != nil {
		return e
	}
	if !reflect.DeepEqual(current, plan) || !reflect.DeepEqual(currentMeta, meta) || !reflect.DeepEqual(receipt, want.SourceReceipt) {
		return errors.New("auto transition local closure differs")
	}
	if op != nil {
		var cipher directoryOverwriteCipher
		if e = qualityRecord(ctx, tx, "directory_overwrite", op.ID, &cipher); e != nil {
			return e
		}
		raw, ue := a.pluginStore.unseal("directory_overwrite", "system", op.ID, cipher.Sealed)
		if ue != nil {
			return ue
		}
		var actual directoryOverwriteOperation
		if cleanupJournalStrict(raw, &actual) != nil || !reflect.DeepEqual(actual, *op) {
			return errors.New("auto transition operation differs")
		}
	}
	if e = a.requireNativeDirectoryAuthorizationTx(ctx, tx, plan, meta); e != nil {
		return e
	}
	var actualQuality qualityImportProof
	e = a.readQualitySealed(ctx, tx, "subscription_quality_import", plan.ID, &actualQuality)
	if quality == nil {
		if !errors.Is(e, sql.ErrNoRows) {
			return errors.New("unexpected auto transition quality proof")
		}
	} else if e != nil || !reflect.DeepEqual(actualQuality, *quality) {
		return errors.New("auto transition quality proof differs")
	}
	return nil
}

func (a *App) adoptImportAutoCopy(ctx context.Context, job Job, plan *ImportPlan, index int, meta *importMetadata, quality *qualityImportProof, failed *importEXDEVFailure, check func() error) error {
	deny := func() error {
		return bad("REVIEW_AUTO_COPY", "自动复制缺少实际跨盘失败或原计划身份，请重新核对")
	}
	if failed == nil || failed.app != a || failed.cause == nil || failed.cause.Code != "IMPORT_LINK_CROSS_DEVICE" || failed.used.Swap(true) || plan.Mode != "auto" || index < 0 || index >= len(plan.Files) {
		return deny()
	}
	file := plan.Files[index]
	key := plan.ID + "/" + file.ID
	r := failed.receipt
	if file.Method != "hardlink" || meta.Methods[file.ID] != "hardlink" || r.ID != key || r.Source != file.Source || r.Target != file.Target || r.SourceSnapshot != planSnapshot(file) || r.State != "planned" || r.StageSnapshot != (importSnapshot{}) || r.SHA256 != "" || r.Stage != ".light-media-"+tokenHash(key)[:32]+".part" {
		return deny()
	}
	if e := check(); e != nil {
		return e
	}
	if e := a.jobCancelled(ctx, job); e != nil {
		return e
	}
	if stamp, e := snapshotPath(file.Source); e != nil || stamp != r.SourceSnapshot {
		return deny()
	}
	if _, e := os.Lstat(filepath.Join(filepath.Dir(file.Target), r.Stage)); !errors.Is(e, os.ErrNotExist) {
		return deny()
	}
	var actual importReceipt
	if e := a.Store.Get(ctx, "import_receipt", key, &actual); e != nil || !reflect.DeepEqual(actual, r) {
		return deny()
	}
	proof := importAutoCopyReceipt{Version: 1, Plan: plan.ID, File: file.ID, Job: job.ID, Generation: job.Generation, SourceReceipt: r, BeforeInputSHA: qualityImportSHA(*plan)}
	var beforeOp, afterOp *directoryOverwriteOperation
	if meta.Overwrite != nil {
		op, e := a.readDirectoryOverwriteOperation(ctx, key)
		if e != nil {
			return e
		}
		if op.Source != file.Source || op.Target != file.Target || op.RootID != plan.RootID || op.SourceSnapshot != r.SourceSnapshot || op.Method != "hardlink" || !sameOverwritePolicy(op.Policy, *meta.Overwrite) || (op.State != "planned" && op.State != "quarantined") {
			return deny()
		}
		if known, e := a.knownDirectoryOverwritePublication(ctx, &op); e != nil {
			return e
		} else if known {
			return deny()
		}
		for _, old := range op.Targets {
			if stamp, e := snapshotPath(old.Path); e != nil || stamp != old.Snapshot {
				return deny()
			}
			if _, e := os.Lstat(filepath.Join(filepath.Dir(old.Path), old.Backup)); !errors.Is(e, os.ErrNotExist) {
				return deny()
			}
		}
		// Copy is a new actual action: dispatch the original overwrite event
		// with transfer_type=copy and re-read every original target identity.
		next, e := a.readDirectoryOverwrite(ctx, key, file.Source, file.Target, plan.RootID, "copy", r.SourceSnapshot, *meta.Overwrite)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(op.Targets, next.Targets) {
			return deny()
		}
		beforeOp, afterOp = &op, &next
		proof.BeforeOperationSHA = tokenHash(string(mustCleanSeedJSON(op)))
		proof.AfterOperationSHA = tokenHash(string(mustCleanSeedJSON(next)))
	} else if _, e := os.Lstat(file.Target); !errors.Is(e, os.ErrNotExist) {
		return deny()
	}
	proposed := *plan
	proposed.Files = append([]PlanFile(nil), plan.Files...)
	proposed.Files[index].Method = "copy"
	newMeta := *meta
	newMeta.Methods = make(map[string]string, len(meta.Methods))
	for k, v := range meta.Methods {
		newMeta.Methods[k] = v
	}
	newMeta.Methods[file.ID] = "copy"
	proof.AfterInputSHA = qualityImportSHA(proposed)
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	actualJob, e := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
	if e != nil {
		return e
	}
	if actualJob.State != "running" || actualJob.Generation != job.Generation {
		return bad("JOB_CANCELLED", "任务已取消")
	}
	var input struct {
		ID                  string   `json:"id"`
		FileIDs             []string `json:"file_ids"`
		OriginalScrapeBatch string   `json:"original_scrape_batch,omitempty"`
		ParentJobID         string   `json:"_parent_job_id,omitempty"`
		RequestID           string   `json:"_request_id,omitempty"`
	}
	if actualJob.Kind != "import" || actualJob.WorkflowID != plan.WorkflowID || !reflect.DeepEqual(actualJob.Payload, job.Payload) || cleanupJournalStrict(actualJob.Payload, &input) != nil || input.ID != plan.ID {
		return deny()
	}
	selected, se := selectImportFiles(*plan, input.FileIDs)
	if se != nil {
		return se
	}
	found := false
	for _, n := range selected {
		if n == index {
			found = true
		}
	}
	if !found {
		return deny()
	}
	var oldPlan ImportPlan
	var oldMeta importMetadata
	var oldReceipt importReceipt
	if e = qualityRecord(ctx, tx, "import_plan", plan.ID, &oldPlan); e != nil {
		return e
	}
	if e = qualityRecord(ctx, tx, "import_meta", plan.ID, &oldMeta); e != nil {
		return e
	}
	if e = qualityRecord(ctx, tx, "import_receipt", key, &oldReceipt); e != nil {
		return e
	}
	if !reflect.DeepEqual(oldPlan, *plan) || !reflect.DeepEqual(oldMeta, *meta) || !reflect.DeepEqual(oldReceipt, r) {
		return deny()
	}
	current, e := a.qualityPlanProof(ctx, tx, *plan)
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(current, quality) {
		return deny()
	}
	var nextQuality *qualityImportProof
	if current != nil {
		if current.Version != 3 || current.Overwrite == nil || beforeOp == nil {
			return deny()
		}
		copyProof := *current
		copyProof.InputSHA = proof.AfterInputSHA
		nextQuality = &copyProof
		if e = a.writeQualitySealedTx(ctx, tx, "subscription_quality_import", plan.ID, copyProof); e != nil {
			return e
		}
	}
	if beforeOp != nil {
		var cipher directoryOverwriteCipher
		if e = qualityRecord(ctx, tx, "directory_overwrite", key, &cipher); e != nil {
			return e
		}
		raw, ue := a.pluginStore.unseal("directory_overwrite", "system", key, cipher.Sealed)
		if ue != nil {
			return ue
		}
		var currentOp directoryOverwriteOperation
		if cleanupJournalStrict(raw, &currentOp) != nil || !reflect.DeepEqual(currentOp, *beforeOp) {
			return deny()
		}
		sealed, se := a.pluginStore.seal("directory_overwrite", "system", key, mustCleanSeedJSON(afterOp))
		if se != nil {
			return se
		}
		if e = txPut(ctx, tx, "directory_overwrite", key, directoryOverwriteCipher{sealed}); e != nil {
			return e
		}
	}
	if e = a.transitionNativeDirectoryAuthorizationTx(ctx, tx, *plan, *meta, proposed, newMeta); e != nil {
		return e
	}
	if e = txPut(ctx, tx, "import_meta", plan.ID, newMeta); e != nil {
		return e
	}
	if e = txPut(ctx, tx, "import_plan", plan.ID, proposed); e != nil {
		return e
	}
	if e = a.writeQualitySealedTx(ctx, tx, "import_auto_copy", key, proof); e != nil {
		return e
	}
	commit := a.importAutoCommit
	if commit == nil {
		commit = func(tx *sql.Tx) error { return tx.Commit() }
	}
	if e = commit(tx); e != nil {
		// Release a rolled-back/unfinished transaction before the fresh exact
		// read (the store deliberately uses one connection).
		_ = tx.Rollback()
		finish, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		known := a.knownImportAutoCopy(finish, proposed, newMeta, afterOp, nextQuality, proof)
		cancel()
		if known != nil {
			return &importAutoCopyUnknown{e}
		}
	}
	*plan = proposed
	*meta = newMeta
	if quality != nil {
		*quality = *nextQuality
	}
	return nil
}
