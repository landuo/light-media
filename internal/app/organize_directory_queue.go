package app

import "context"

func (a *App) enqueueDirectoryOrganizeFile(ctx context.Context, guard directoryImportGuard, file importScanFile, observed *DirectoryObservedFile) error {
	payload := organizeFilePayload{ID: tokenHash(observed.ID + observed.LastSeen), File: file, Snapshot: observed.Snapshot,
		Title: file.Parsed.Title, Directory: &guard, BatchID: id("scrape_batch_"),
		Item: file.originalItem, DirectoryPlanned: file.originalDirectoryPlanned, MetadataPath: file.originalMetadataPath}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if found, err := directoryHistoryObservation(ctx, tx, observed); err != nil {
		return err
	} else if found {
		if err = txPut(ctx, tx, "directory_file", observed.ID, observed); err != nil {
			return err
		}
		return tx.Commit()
	}
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
	job, err := a.enqueueTx(ctx, tx, "organize_file", payload, id("wf_"))
	if err != nil {
		return err
	}
	observed.JobID, observed.State, observed.Reason = job.ID, "queued", "文件已加入整理队列，等待识别"
	if err = txPut(ctx, tx, "directory_file", observed.ID, observed); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) runDirectoryOrganizeFile(ctx context.Context, payload organizeFilePayload, file importScanFile, known *ImportPlan, fail func(error) error) error {
	guard := payload.Directory
	rule, err := a.enabledDirectory(guard.Rule.ID)
	if err != nil || directoryFingerprint(rule) != directoryFingerprint(guard.Rule) {
		return fail(bad("DIRECTORY_RULE_CHANGED", "目录规则已修改或停用，停止旧整理任务"))
	}
	request, err := a.directoryFileIdentity(ctx, rule, file)
	known.Media.ID, known.Media.MediaType = request.TMDBID, request.MediaType
	known.RootID, known.Mode = request.RootID, request.Mode
	for _, planned := range request.Files {
		if planned.Path == file.Path && (planned.SeasonExplicit || len(planned.Episodes) > 0) {
			known.Files[0].Season, known.Files[0].Episodes = planned.Season, append([]int{}, planned.Episodes...)
		}
	}
	if err != nil {
		return fail(err)
	}
	plan, err := a.previewImport(ctx, request)
	if err != nil {
		if plan.Media.Title != "" {
			known.Media = plan.Media
		}
		if plan.Category != "" {
			known.Category = plan.Category
		}
		return fail(err)
	}
	*known = plan
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
