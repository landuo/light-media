package app

import (
	"context"
	"encoding/json"
	"time"
)

func (a *App) enqueueDirectoryOrganizeFile(ctx context.Context, guard directoryImportGuard, file importScanFile, observed *DirectoryObservedFile) error {
	payload := organizeFilePayload{ID: tokenHash(observed.ID + observed.LastSeen), File: file, Snapshot: observed.Snapshot,
		Title: file.Parsed.Title, Directory: &guard, BatchID: id("scrape_batch_"),
		Item: file.originalItem, DirectoryPlanned: file.originalDirectoryPlanned, MetadataPath: file.originalMetadataPath}
	_ = observed
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
	existing, err := a.activeOrganizeSourceTx(ctx, tx, file.Path)
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
	// The task id is minted before commit so the observation can point at it;
	// the task itself is registered in memory afterwards. Nothing about the work
	// needs a row: if the process dies, the file is still in the directory and
	// the next scan re-derives it.
	taskID := id("job_")
	observed.JobID, observed.State, observed.Reason = taskID, "queued", "文件已加入整理队列，等待识别"
	if err = txPut(ctx, tx, "directory_file", observed.ID, observed); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	a.submitMemoryJob("organize_file", payload, id("wf_"), taskID)
	return nil
}

// recordDirectoryOrganizeVerdict writes the outcome back to the observation.
func (a *App) recordDirectoryOrganizeVerdict(ctx context.Context, item organizeFilePayload, path, state, reason string) {
	if item.Directory == nil || item.Directory.Rule.ID == "" || path == "" {
		return
	}
	key := directoryFileID(item.Directory.Rule.ID, path)
	var f DirectoryObservedFile
	if err := a.Store.Get(ctx, "directory_file", key, &f); err != nil {
		return
	}
	f.State, f.Reason = state, reason
	f.LastSeen = now()
	_ = a.Store.Put(ctx, "directory_file", key, f)
}

// submitMemoryJob registers a rowless task after its business state committed.
func (a *App) submitMemoryJob(kind string, payload any, wf, taskID string) Job {
	b, e := json.Marshal(payload)
	if e != nil {
		return Job{}
	}
	j := Job{ID: taskID, WorkflowID: wf, Kind: kind, State: "queued", Payload: b, RunAt: time.Now().Unix(), CreatedAt: now(), UpdatedAt: now()}
	j.Ephemeral = true
	a.indexEnqueue(j)
	select {
	case a.jobWake <- struct{}{}:
	default:
	}
	return j
}

// indexSourceActive answers "is this source file already being organized" from
// the dispatch index, which now holds the organizing tasks that have no row.
func (a *App) indexSourceActive(source string) (string, bool) {
	if source == "" {
		return "", false
	}
	ix := a.index()
	ix.mu.Lock()
	defer ix.mu.Unlock()
	for _, j := range ix.entries {
		if j.Kind != "organize_file" || j.State != "queued" {
			continue
		}
		var payload struct {
			File struct {
				Path string `json:"path"`
			} `json:"file"`
		}
		if json.Unmarshal(j.Payload, &payload) == nil && payload.File.Path == source {
			return j.ID, true
		}
	}
	return "", false
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
	// Success used to be observed by a later scan reading the plan's state.
	// Record it here so a rowless run leaves the same trail.
	a.recordDirectoryOrganizeVerdict(ctx, payload, file.Path, "imported", "整理完成，源文件保留")
	return nil
}
