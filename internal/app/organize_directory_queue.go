package app

import "context"

func (a *App) enqueueDirectoryOrganizeFile(ctx context.Context, guard directoryImportGuard, file importScanFile, observed *DirectoryObservedFile) error {
	payload := organizeFilePayload{ID: tokenHash(observed.ID + observed.LastSeen), File: file, Snapshot: observed.Snapshot,
		Title: file.Parsed.Title, Directory: &guard, BatchID: id("scrape_batch_")}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	existing, err := activeOrganizeSourceTx(ctx, tx, file.Path)
	if err != nil {
		return err
	}
	if existing != "" {
		observed.JobID, observed.State, observed.Reason = existing, "queued", "此源文件已在整理队列中"
		if err = txPut(ctx, tx, "directory_file", observed.ID, observed); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err = txPut(ctx, tx, "original_scrape_batch", payload.BatchID, originalScrapeBatch{ID: payload.BatchID, Closed: true}); err != nil {
		return err
	}
	job, err := enqueueTx(ctx, tx, "organize_file", payload, id("wf_"))
	if err != nil {
		return err
	}
	observed.JobID, observed.State, observed.Reason = job.ID, "queued", "文件已加入整理队列，等待识别"
	if err = txPut(ctx, tx, "directory_file", observed.ID, observed); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) runDirectoryOrganizeFile(ctx context.Context, payload organizeFilePayload, file importScanFile, fail func(error) error) error {
	guard := payload.Directory
	rule, err := a.enabledDirectory(guard.Rule.ID)
	if err != nil || directoryFingerprint(rule) != directoryFingerprint(guard.Rule) {
		return fail(bad("DIRECTORY_RULE_CHANGED", "目录规则已修改或停用，停止旧整理任务"))
	}
	request, err := a.directoryFileIdentity(ctx, rule, file)
	if err != nil {
		return fail(err)
	}
	plan, err := a.previewImport(ctx, request)
	if err != nil {
		return fail(err)
	}
	if err = a.Store.Put(ctx, "directory_import", plan.ID, guard); err != nil {
		return err
	}
	if _, err = a.executeImport(ctx, plan.ID, nil); err != nil {
		if ctx.Err() != nil || organizeFileInterrupted(err) {
			return err
		}
		return fail(err)
	}
	return nil
}
