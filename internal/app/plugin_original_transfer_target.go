package app

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
)

type originalTransferTargetUnknown struct{ cause error }

func (e *originalTransferTargetUnknown) Error() string {
	return "插件整理目标事务无法确认，已保留真实文件和持久收据供核对：" + e.cause.Error()
}
func (e *originalTransferTargetUnknown) Unwrap() error { return e.cause }

// A returned physical destination changes the existing sealed plan and its
// publication receipt in one transaction. It never grants a different root,
// source, media identity, transfer method or authority over another asset.
func (a *App) adoptOriginalTransferTarget(ctx context.Context, job Job, plan *ImportPlan, index int, meta importMetadata, receipt importReceipt) error {
	return a.transitionOriginalTransferTarget(ctx, job, plan, index, meta, receipt, true)
}

func (a *App) transitionOriginalTransferTarget(ctx context.Context, job Job, plan *ImportPlan, index int, meta importMetadata, receipt importReceipt, published bool) error {
	return a.transitionOriginalTransferTargetAt(ctx, job, plan, index, &meta, receipt, published, -1)
}

func (a *App) transitionOriginalTransferTargetAt(ctx context.Context, job Job, plan *ImportPlan, index int, meta *importMetadata, receipt importReceipt, published bool, sideIndex int) error {
	deny := func() error {
		return bad("REVIEW_PLUGIN_TRANSFER_TARGET", "插件整理目标与当前文件、任务或已授权目录无法核对")
	}
	if index < 0 || index >= len(plan.Files) {
		return deny()
	}
	file := plan.Files[index]
	if sideIndex >= 0 {
		if sideIndex >= len(meta.Sidecars[file.ID]) {
			return deny()
		}
		file = originalTransferSideFile(file, meta.Sidecars[file.ID][sideIndex], sideIndex)
	}
	if receipt.ID != plan.ID+"/"+file.ID || receipt.Source != file.Source || receipt.SourceSnapshot != planSnapshot(file) || !importPath(receipt.Target) || !within(meta.RootPath, receipt.Target) || receipt.Target == file.Source {
		return deny()
	}
	if published && (receipt.State != "published" || !receipt.OriginalModuleStarted || len(receipt.OriginalModuleResult) == 0) || !published && (receipt.State != "planned" || !receipt.OriginalNamingResolved || receipt.StageSnapshot != (importSnapshot{}) || receipt.SHA256 != "" || len(receipt.OriginalModuleResult) > 0 && !receipt.OriginalNativeInterrupted) {
		return deny()
	}
	for i, other := range plan.Files {
		if (i != index || sideIndex >= 0) && other.Target == receipt.Target {
			return deny()
		}
		for j, side := range meta.Sidecars[other.ID] {
			if !(i == index && j == sideIndex) && side.Target == receipt.Target {
				return deny()
			}
		}
	}
	if err := a.jobCancelled(ctx, job); err != nil {
		return err
	}
	if err := a.guardImportDirectory(ctx, *plan); err != nil {
		return err
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var before ImportPlan
	var actualMeta importMetadata
	var beforeReceipt importReceipt
	if err = qualityRecord(ctx, tx, "import_plan", plan.ID, &before); err != nil {
		return err
	}
	if err = qualityRecord(ctx, tx, "import_meta", plan.ID, &actualMeta); err != nil {
		return err
	}
	if err = qualityRecord(ctx, tx, "import_receipt", receipt.ID, &beforeReceipt); err != nil {
		if published || !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		beforeReceipt = originalTransferPlannedReceipt(*plan, file)
	}
	if !reflect.DeepEqual(before, *plan) || !reflect.DeepEqual(actualMeta, *meta) || beforeReceipt.Target != file.Target || beforeReceipt.Source != file.Source || beforeReceipt.SourceSnapshot != receipt.SourceSnapshot || beforeReceipt.State != "planned" || beforeReceipt.OriginalNativeInterrupted != receipt.OriginalNativeInterrupted || !reflect.DeepEqual(beforeReceipt.OriginalModuleResult, receipt.OriginalModuleResult) {
		return deny()
	}
	currentJob, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
	if err != nil {
		return err
	}
	if currentJob.State != "running" || currentJob.Generation != job.Generation || currentJob.Kind != "import" || !reflect.DeepEqual(currentJob.Payload, job.Payload) {
		return bad("JOB_CANCELLED", "任务已取消")
	}
	if published {
		var owned managedFile
		err = qualityRecord(ctx, tx, "managed_file", tokenHash(receipt.Target), &owned)
		if err == nil {
			return bad("REVIEW_PLUGIN_TRANSFER_TARGET", "插件返回的目标已属于受管文件，不能直接替换其归属")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='asset' AND json_extract(payload,'$.path')=? AND json_extract(payload,'$.state') IN ('present','external')`, receipt.Target).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return bad("REVIEW_PLUGIN_TRANSFER_TARGET", "插件返回的目标已属于其他资产")
		}
	}
	quality, err := a.qualityPlanProof(ctx, tx, before)
	if err != nil {
		return err
	}
	// Existing quality replacement receipts bind old asset/overwrite targets.
	// Do not silently discard those facts when a provider changes destination.
	if quality != nil && receipt.Target != file.Target {
		return bad("REVIEW_PLUGIN_TRANSFER_TARGET", "插件改变了洗版目标，请先核对原资产与实际覆盖收据")
	}
	proposed := before
	proposed.Files = append([]PlanFile(nil), before.Files...)
	proposedMeta := actualMeta
	if sideIndex < 0 {
		proposed.Files[index].Target = receipt.Target
	} else {
		proposedMeta.Sidecars = make(map[string][]importSidecar, len(actualMeta.Sidecars))
		for key, sides := range actualMeta.Sidecars {
			proposedMeta.Sidecars[key] = append([]importSidecar(nil), sides...)
		}
		proposedMeta.Sidecars[before.Files[index].ID][sideIndex].Target = receipt.Target
	}
	if err = a.transitionNativeDirectoryAuthorizationTx(ctx, tx, before, actualMeta, proposed, proposedMeta); err != nil {
		return err
	}
	if err = txPut(ctx, tx, "import_plan", plan.ID, proposed); err != nil {
		return err
	}
	if sideIndex >= 0 {
		if err = txPut(ctx, tx, "import_meta", plan.ID, proposedMeta); err != nil {
			return err
		}
	}
	if err = txPut(ctx, tx, "import_receipt", receipt.ID, receipt); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		// A lost acknowledgement must not let the import failure path overwrite
		// a transaction whose new destination was actually committed.
		var actualPlan ImportPlan
		var actualReceipt importReceipt
		var committedMeta importMetadata
		pErr := a.Store.Get(ctx, "import_plan", plan.ID, &actualPlan)
		rErr := a.Store.Get(ctx, "import_receipt", receipt.ID, &actualReceipt)
		mErr := a.Store.Get(ctx, "import_meta", plan.ID, &committedMeta)
		if pErr == nil && rErr == nil && mErr == nil && reflect.DeepEqual(actualPlan, proposed) && reflect.DeepEqual(actualReceipt, receipt) && reflect.DeepEqual(committedMeta, proposedMeta) && a.requireNativeDirectoryAuthorizationTx(ctx, a.Store.DB, actualPlan, proposedMeta) == nil {
			*plan = proposed
			*meta = proposedMeta
			return nil
		}
		if pErr == nil && rErr == nil && mErr == nil && reflect.DeepEqual(actualPlan, before) && reflect.DeepEqual(actualReceipt, beforeReceipt) && reflect.DeepEqual(committedMeta, actualMeta) {
			return err
		}
		return &originalTransferTargetUnknown{err}
	}
	*plan = proposed
	*meta = proposedMeta
	return nil
}
