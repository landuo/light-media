package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"light-media/internal/plugins"

	"golang.org/x/sys/unix"
)

type importReceipt struct {
	ReusedHardlink            bool                               `json:"reused_hardlink,omitempty"`
	DirectoryException        string                             `json:"directory_exception,omitempty"`
	DirectoryMembers          map[string]originalDirectoryMember `json:"directory_members,omitempty"`
	DirectoryFiles            []string                           `json:"directory_files,omitempty"`
	DirectoryCompleted        []string                           `json:"directory_completed,omitempty"`
	DirectoryListed           bool                               `json:"directory_listed,omitempty"`
	DirectoryStreamSize       *int64                             `json:"directory_stream_size,omitempty"`
	OriginalNamingResolved    bool                               `json:"original_naming_resolved,omitempty"`
	OriginalModuleStarted     bool                               `json:"original_module_started,omitempty"`
	OriginalModuleResult      []byte                             `json:"original_module_result,omitempty"`
	OriginalNativeInterrupted bool                               `json:"original_native_interrupted,omitempty"`
	ID                        string                             `json:"id"`
	Target                    string                             `json:"target"`
	Source                    string                             `json:"source"`
	SourceSnapshot            importSnapshot                     `json:"source_snapshot"`
	Stage                     string                             `json:"stage"`
	StageSnapshot             importSnapshot                     `json:"stage_snapshot"`
	State                     string                             `json:"state"`
	SHA256                    string                             `json:"sha256,omitempty"`
}

type managedFile struct {
	Path           string         `json:"path"`
	RootID         string         `json:"root_id"`
	AssetID        string         `json:"asset_id"`
	MediaKey       string         `json:"media_key"`
	Kind           string         `json:"kind"`
	Snapshot       importSnapshot `json:"snapshot"`
	SourceSnapshot importSnapshot `json:"source_snapshot,omitempty"`
	SHA256         string         `json:"sha256,omitempty"`
}

func (a *App) executeImport(ctx context.Context, planID string, fileIDs []string) (Job, error) {
	if err := a.guardDownloadReviewPlan(ctx, planID); err != nil {
		return Job{}, err
	}
	a.importMu.Lock()
	defer a.importMu.Unlock()
	var plan ImportPlan
	if err := a.Store.Get(ctx, "import_plan", planID, &plan); err != nil {
		return Job{}, err
	}
	if err := a.guardImportDirectory(ctx, plan); err != nil {
		return Job{}, err
	}
	selected, err := selectImportFiles(plan, fileIDs)
	if err != nil {
		return Job{}, err
	}
	work, organizing := ctx.Value(organizeFileKey{}).(organizeFileWork)
	if organizing {
		for _, index := range selected {
			file := plan.Files[index]
			if file.Source != work.File.Path || planSnapshot(file) != work.Stamp {
				return Job{}, bad("REVIEW_SOURCE_CHANGED", "源文件在识别过程中发生变化")
			}
		}
	}
	root, err := a.importRoot(plan.RootID)
	if err != nil {
		return Job{}, err
	}
	var meta importMetadata
	if err = a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil {
		return Job{}, err
	}
	if meta.OriginalPreview {
		return Job{}, bad("PLUGIN_PREVIEW_ONLY", "此预览仅计算结果，请从当前整理表单重新提交执行")
	}
	if root.Path != meta.RootPath {
		return Job{}, bad("REVIEW_ROOT_CHANGED", "目标库路径在预览后发生变化，请重新预览")
	}
	if a.paused() {
		return Job{}, &APIError{"AUTOMATION_PAUSED", "自动化已暂停，请先恢复", 409}
	}
	if attempt, ok := ctx.Value(originalTransferRetryAttemptKey{}).(originalTransferRetryAttempt); ok {
		if _, err := a.originalCurrentTransferRetry(ctx, attempt, false); err != nil {
			return Job{}, err
		}
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	if err = a.requireNativeDirectoryAuthorizationTx(ctx, tx, plan, meta); err != nil {
		return Job{}, err
	}
	// A lost HTTP response must not turn the same confirmation into another
	// execution or a misleading error. Different selections remain separate.
	existing, findErr := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind='import' AND json_extract(payload,'$.id')=? AND state IN ('queued','running','retry_wait','waiting_paused','succeeded') ORDER BY created_at DESC LIMIT 1`, plan.ID))
	if findErr == nil {
		var payload struct {
			FileIDs []string `json:"file_ids"`
		}
		if err = json.Unmarshal(existing.Payload, &payload); err != nil {
			return Job{}, err
		}
		prior, selectionErr := selectImportFiles(plan, payload.FileIDs)
		if selectionErr == nil && sameImportSelection(prior, selected) {
			return existing, tx.Commit()
		}
	} else if !errors.Is(findErr, sql.ErrNoRows) {
		return Job{}, findErr
	}
	if plan.IntentID != "" {
		intent, err := importIntentTx(ctx, tx, plan)
		if err != nil {
			return Job{}, err
		}
		if intent.ChatWholeUnscoped {
			if _, err = a.readCompletedUnscopedPlan(ctx, tx, intent, plan); err != nil {
				return Job{}, err
			}
		}
	}
	for _, index := range selected {
		file := plan.Files[index]
		if file.Status == "imported" || file.Status == "already_imported" {
			continue
		}
		if err = importExcludedTx(ctx, tx, plan, file); err != nil {
			return Job{}, err
		}
		if err = a.importReserve(ctx, tx, plan, file); err != nil {
			return Job{}, err
		}
	}
	plan.State = "queued"
	if err = txPut(ctx, tx, "import_plan", plan.ID, plan); err != nil {
		return Job{}, err
	}
	batchID, _ := ctx.Value(originalScrapeBatchKey{}).(string)
	if organizing {
		batchID = work.BatchID
	}
	if batchID == "" {
		batchID = id("scrape_batch_")
		if err = txPut(ctx, tx, "original_scrape_batch", batchID, originalScrapeBatch{ID: batchID, Closed: true}); err != nil {
			return Job{}, err
		}
	}
	var job Job
	if organizing {
		if len(fileIDs) == 0 && len(selected) == 1 {
			fileIDs = []string{plan.Files[selected[0]].ID}
		}
		job, err = promoteOrganizeFileTx(ctx, tx, work, plan.ID, fileIDs, "queued", "", "")
	} else {
		job, err = enqueueTx(ctx, tx, "import", map[string]any{"id": plan.ID, "file_ids": fileIDs, "original_scrape_batch": batchID}, plan.WorkflowID)
	}
	if err != nil {
		return Job{}, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, err
	}
	select {
	case a.jobWake <- struct{}{}:
	default:
	}
	return job, nil
}

func sameImportSelection(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func selectImportFiles(plan ImportPlan, requested []string) ([]int, error) {
	wanted := map[string]bool{}
	for _, key := range requested {
		if wanted[key] {
			return nil, bad("INVALID_SELECTION", "重复选择文件")
		}
		wanted[key] = true
	}
	result := []int{}
	for index, file := range plan.Files {
		if len(requested) > 0 && !wanted[file.ID] {
			continue
		}
		if len(requested) == 0 && file.Status == "blocked" {
			continue
		}
		if file.Status == "blocked" {
			return nil, bad("REVIEW_IMPORT", "选中文件尚需核对")
		}
		if file.Status != "ready" && file.Status != "importing" && file.Status != "error" && file.Status != "imported" && file.Status != "already_imported" {
			return nil, bad("INVALID_IMPORT_STATE", "文件状态不能执行")
		}
		result = append(result, index)
		delete(wanted, file.ID)
	}
	if len(wanted) > 0 || len(result) == 0 {
		return nil, bad("INVALID_SELECTION", "请选择计划中可执行的文件")
	}
	return result, nil
}

func (a *App) importReserve(ctx context.Context, tx *sql.Tx, plan ImportPlan, file PlanFile) error {
	if auxiliaryMaterialKind(file.MaterialKind) {
		return nil
	}
	quality, e := a.qualityPlanProof(ctx, tx, plan)
	if e != nil {
		return e
	}
	if quality != nil {
		if e = a.guardQualityImportFile(ctx, tx, plan, file, quality, false); e != nil {
			return e
		}
	}
	if err := reorganizeReserveTx(ctx, tx, plan); err != nil {
		return err
	}
	var overwriteMeta importMetadata
	var overwriteRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='import_meta' AND id=?`, plan.ID).Scan(&overwriteRaw); err != nil {
		return err
	}
	if json.Unmarshal(overwriteRaw, &overwriteMeta) != nil {
		return bad("REVIEW_OVERWRITE_RECEIPT", "整理目录策略不可读")
	}
	owner := plan.ID
	if plan.IntentID != "" {
		owner = plan.IntentID
	}
	for _, episode := range importEpisodes(file.Episodes) {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_slots WHERE media_key=? AND season=? AND episode=? AND asset_id!=?`, mediaKey(plan.Media), file.Season, episode, plan.ReorganizeAssetID).Scan(&count); err != nil {
			return err
		}
		if count > 0 && quality == nil && overwriteMeta.Overwrite == nil {
			return bad("IMPORT_CONFLICT", "影片或季集已存在资产")
		}
		var current string
		err := tx.QueryRowContext(ctx, `SELECT intent_id FROM reservations WHERE media_key=? AND season=? AND episode=?`, mediaKey(plan.Media), file.Season, episode).Scan(&current)
		if err == nil {
			if current != owner {
				reusable := false
				if plan.IntentID == "" {
					reusable, err = canReplaceFailedImportReservation(ctx, tx, current, mediaKey(plan.Media), file, episode)
					if err != nil {
						return err
					}
				}
				if !reusable {
					return bad("IMPORT_CONFLICT", "影片或季集被其他任务占用")
				}
				if _, err := tx.ExecContext(ctx, `UPDATE reservations SET intent_id=? WHERE media_key=? AND season=? AND episode=? AND intent_id=?`,
					owner, mediaKey(plan.Media), file.Season, episode, current); err != nil {
					return err
				}
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO reservations VALUES(?,?,?,?)`, mediaKey(plan.Media), file.Season, episode, owner); err != nil {
			return bad("IMPORT_CONFLICT", "影片或季集同时被其他任务占用")
		}
	}
	return nil
}

func importExcludedTx(ctx context.Context, tx *sql.Tx, plan ImportPlan, file PlanFile) error {
	for _, episode := range importEpisodes(file.Episodes) {
		var raw []byte
		err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='exclusion' AND id=?`, fmt.Sprintf("%s:%d:%d", mediaKey(plan.Media), file.Season, episode)).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		var rule struct {
			Blocked bool `json:"blocked"`
		}
		if json.Unmarshal(raw, &rule) != nil {
			return bad("REVIEW_EXCLUSION", "排除记录损坏，需人工核对")
		}
		if rule.Blocked {
			return bad("EXCLUDED_CONTENT", "该内容已被排除获取和整理，请先显式解除")
		}
	}
	return nil
}

func (a *App) runImport(ctx context.Context, job Job, planID string, fileIDs []string) (returnErr error) {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	var plan ImportPlan
	if err := a.Store.Get(ctx, "import_plan", planID, &plan); err != nil {
		return err
	}
	if err := a.guardImportDirectory(ctx, plan); err != nil {
		return err
	}
	if err := a.importIntentActive(ctx, plan); err != nil {
		return err
	}
	var meta importMetadata
	if err := a.Store.Get(ctx, "import_meta", planID, &meta); err != nil {
		return err
	}
	if meta.OriginalPreview {
		return bad("PLUGIN_PREVIEW_ONLY", "此预览仅计算结果，请从当前整理表单重新提交执行")
	}
	quality, err := a.qualityPlanProof(ctx, a.Store.DB, plan)
	if err != nil {
		return err
	}
	if err := a.requireNativeDirectoryAuthorizationTx(ctx, a.Store.DB, plan, meta); err != nil {
		return err
	}
	root, err := a.importRoot(plan.RootID)
	if err != nil {
		return err
	}
	if root.Path != meta.RootPath {
		return bad("REVIEW_ROOT_CHANGED", "目标库路径已变化，请重新预览")
	}
	indices, err := selectImportFiles(plan, fileIDs)
	if err != nil {
		return err
	}
	if err = a.startImportRun(ctx, job, &plan, indices); err != nil {
		return err
	}
	var batchFailures []error
	var originalFailedFile *PlanFile
	var originalFailedAuxiliary *importSidecar
	originalFailedAuxiliaryIndex := -1
	defer func() {
		if returnErr != nil {
			if originalTransferInterrupted(returnErr) {
				return // An invalidated execution is not a failed physical callback.
			}
			var uncertain *importAutoCopyUnknown
			if errors.As(returnErr, &uncertain) {
				return
			}
			var targetUncertain *originalTransferTargetUnknown
			if errors.As(returnErr, &targetUncertain) {
				return
			}
			plan.State = "partial"
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := a.persistOriginalTransferFailure(cleanupCtx, plan, meta, indices, returnErr); err == nil && originalFailedFile != nil {
				var callbackFailure *originalTransferCallbackError
				if errors.As(returnErr, &callbackFailure) {
					return
				}
				if originalFailedAuxiliary == nil {
					if source, ok := meta.OriginalTransfer[originalFailedFile.ID]; ok {
						if err := a.completeOriginalTransferCallback(cleanupCtx, job, plan, originalFailedFile.ID, *originalFailedFile, source, false, originalFailedFile.Reason); err != nil {
							a.originalTransferEventError(cleanupCtx, err)
						}
					}
				}
				if originalFailedAuxiliary != nil {
					extra := originalTransferSideFile(*originalFailedFile, *originalFailedAuxiliary, originalFailedAuxiliaryIndex)
					if projection := originalFailedAuxiliary.Original; projection != nil {
						if err := a.completeOriginalTransferCallback(cleanupCtx, job, plan, originalFailedFile.ID, extra, *projection, false, originalFailedFile.Reason); err != nil {
							a.originalTransferEventError(cleanupCtx, err)
						}
					}
				}
			}
		}
	}()
	for _, index := range indices {
		file := &plan.Files[index]
		originalFailedFile, originalFailedAuxiliary = nil, nil
		if file.Status == "imported" || file.Status == "already_imported" {
			if file.Status == "imported" {
				if err = a.finishImportOverwrites(ctx, plan, *file, meta); err != nil {
					return err
				}
				file.Reason = ""
			}
			if failure := a.originalTransferPriorTaskFailure(ctx, job, plan, *file, meta); failure != nil {
				batchFailures = append(batchFailures, failure)
			}
			continue
		}
		if err = a.jobCancelled(ctx, job); err != nil {
			return err
		}
		if !a.importSourceAllowed(file.Source, plan.Manual) || !within(root.Path, file.Target) {
			return bad("REVIEW_PATH_CHANGED", "允许路径发生变化，请重新预览")
		}
		if err = a.ensureNotExcluded(ctx, plan.Media, file.Season, file.Episodes); err != nil {
			return err
		}
		if err = a.guardImportReorganize(ctx, meta.Reorganize); err != nil {
			return err
		}
		release, err := a.beginWrite()
		if err != nil {
			return err
		}
		publicationCommitted := false
		var taskFailures []error
		failures := originalTransferTaskFailures{}
		err = func() error {
			ctx := ctx
			var originalMetadataEvent json.RawMessage
			defer release()
			if a.originalTransferHasHandlers() || file.MaterialKind != "" {
				original, exists := meta.OriginalTransfer[file.ID]
				if !exists {
					var err error
					original, err = a.originalTransferContext(ctx, plan.Media, *file)
					if err != nil {
						return err
					}
					if meta.OriginalTransfer == nil {
						meta.OriginalTransfer = map[string]originalTransferFileContext{}
					}
					meta.OriginalTransfer[file.ID] = original
				}
				ctx = context.WithValue(ctx, originalTransferWriteContextKey{}, &originalTransferWriteContext{File: *file, Source: original, Media: plan.Media})
				sourceStorage, err := a.originalSelectTransferStorage(ctx)
				if err != nil {
					return err
				}
				targetStorage, err := a.originalSelectTransferStorage(ctx)
				if err != nil {
					return err
				}
				ctx = context.WithValue(ctx, originalTransferStorageKey{}, &originalTransferStoragePair{Source: sourceStorage, Target: targetStorage})
			}
			file.Status = "importing"
			file.Reason = ""
			if err := a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
				return err
			}
			check := func() error {
				// Each new publication step needs current admission. A throttled
				// stream-progress check could otherwise miss cancellation between
				// the failed link and a separately authorized copy action.
				if e := a.jobCancelled(ctx, job); e != nil {
					return e
				}
				return a.guardImportDirectory(ctx, plan)
			}
			if err := a.preflightQualityDirectoryOverwrite(ctx, plan, *file, quality, meta); err != nil {
				return err
			}
			inPlace := meta.Reorganize != nil && !meta.Reorganize.TargetRemoved && file.Target == meta.Reorganize.Asset.Path && (!meta.Reorganize.SingleHistory || file.Source == file.Target)
			var receipt importReceipt
			var err error
			publish := func() (importReceipt, error) {
				if err := a.resolveOriginalNativeTransferName(ctx, job, &plan, index, meta); err != nil {
					return importReceipt{}, err
				}
				reused, useExisting, err := a.reuseExistingImportHardlink(ctx, job, &plan, index, meta, check)
				if useExisting || err != nil {
					file = &plan.Files[index]
					return reused, err
				}
				file = &plan.Files[index]
				if file.DirectoryDigest != "" {
					return a.publishOriginalDirectory(ctx, plan.ID+"/"+file.ID, *file, check)
				}
				inPlace = meta.Reorganize != nil && !meta.Reorganize.TargetRemoved && file.Target == meta.Reorganize.Asset.Path && (!meta.Reorganize.SingleHistory || file.Source == file.Target)
				if err := a.originalPrepareTransferTarget(ctx, file.Target); err != nil {
					return importReceipt{}, err
				}
				if inPlace {
					actualSource, inspectErr := snapshotPath(file.Source)
					if inspectErr != nil || actualSource != planSnapshot(*file) {
						return receipt, bad("REVIEW_SOURCE_CHANGED", "重新整理的源文件在预览后发生变化")
					}
					receipt = importReceipt{Target: file.Target, Source: file.Source, SourceSnapshot: planSnapshot(*file), StageSnapshot: meta.Reorganize.Video.Snapshot, SHA256: meta.Reorganize.Video.SHA256, State: "published"}
				} else {
					if meta.Overwrite != nil {
						receipt, err = a.publishDirectoryOverwrite(ctx, plan.ID+"/"+file.ID, file.Source, file.Target, plan.RootID, meta.Methods[file.ID], planSnapshot(*file), *meta.Overwrite, check)
					} else {
						receipt, err = a.publishQualityImport(ctx, plan, *file, quality, meta.Methods[file.ID], check)
					}
				}
				var linkErr *importEXDEVFailure
				if err != nil && plan.Mode == "auto" && errors.As(err, &linkErr) {
					if saveErr := a.adoptImportAutoCopy(ctx, job, &plan, index, &meta, quality, linkErr, check); saveErr != nil {
						return receipt, saveErr
					}
					file = &plan.Files[index]
					a.event(ctx, job, "WARN", "import", "copy_fallback", "目标文件系统与预览时不同，按自动模式回退复制，源文件保留")
					if meta.Overwrite != nil {
						receipt, err = a.publishDirectoryOverwrite(ctx, plan.ID+"/"+file.ID, file.Source, file.Target, plan.RootID, "copy", planSnapshot(*file), *meta.Overwrite, check)
					} else {
						receipt, err = a.publishQualityImport(ctx, plan, *file, quality, "copy", check)
					}
				}
				if err != nil {
					return receipt, err
				}
				return receipt, nil
			}
			receipt, err = a.originalTransferModulePublish(ctx, plan, *file, meta, check, publish, func(receipt importReceipt) error {
				return a.adoptOriginalTransferTarget(ctx, job, &plan, index, meta, receipt)
			})
			if originalTransferInterrupted(err) {
				return err
			}
			var copyUnknown *importAutoCopyUnknown
			var targetUnknown *originalTransferTargetUnknown
			if errors.As(err, &copyUnknown) || errors.As(err, &targetUnknown) {
				return err
			}
			mainSucceeded := err == nil
			mainFailure := err
			if err != nil {
				taskFailures = append(taskFailures, err)
				failures.Messages = append(failures.Messages, a.clean(err.Error()))
			}
			file = &plan.Files[index]
			inPlace = meta.Reorganize != nil && !meta.Reorganize.TargetRemoved && file.Target == meta.Reorganize.Asset.Path && (!meta.Reorganize.SingleHistory || file.Source == file.Target)
			completeTask := func(task PlanFile, source originalTransferFileContext, success bool, message string) {
				if callbackErr := a.completeOriginalTransferCallback(ctx, job, plan, file.ID, task, source, success, message); callbackErr != nil {
					taskFailures = append(taskFailures, &originalTransferCallbackError{callbackErr})
					failures.Pending = append(failures.Pending, originalTransferPendingCallback{FileID: task.ID, Success: success, Message: a.clean(message)})
				}
			}
			if source, ok := meta.OriginalTransfer[file.ID]; ok {
				message := ""
				if mainFailure != nil {
					message = mainFailure.Error()
				}
				completeTask(*file, source, mainSucceeded, message)
			}
			moduleInfo, moduleCalled, err := a.originalRecordedTransferModule(ctx, plan, *file)
			if err != nil {
				return err
			}
			wantsScrape := mainSucceeded && importBool(plan.Scraping, true)
			if mainSucceeded && moduleCalled {
				wantsScrape = moduleInfo != nil && moduleInfo["need_scrape"] == true && originalViewJSONTruth(mustCleanSeedJSON(moduleInfo["target_diritem"]))
			} else if _, ok := meta.OriginalTransfer[file.ID]; mainSucceeded && ok {
				kwargs, e := a.originalTransferModuleArguments(ctx, plan, *file, meta)
				if e != nil {
					return e
				}
				flags := map[string]any{}
				if e := originalTransferApplyNativeFlags(flags, kwargs, true); e != nil {
					return e
				}
				wantsScrape = flags["need_scrape"] == true
			}
			wantsScrape = wantsScrape && file.MaterialKind == ""
			assetID := file.AssetID
			if assetID == "" {
				assetID = id("asset_")
				file.AssetID = assetID
				if err = a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
					return err
				}
			}
			owned := []managedFile{}
			if mainSucceeded && !receipt.ReusedHardlink {
				kind := file.MaterialKind
				if kind == "" {
					kind = "video"
				}
				owned = append(owned, managedFile{Path: file.Target, RootID: root.ID, AssetID: assetID, MediaKey: mediaKey(plan.Media), Kind: kind, Snapshot: receipt.StageSnapshot, SourceSnapshot: planSnapshot(*file), SHA256: receipt.SHA256})
			}
			if mainSucceeded && inPlace {
				for _, prior := range meta.Reorganize.Owned {
					if auxiliaryMaterialKind(prior.Kind) {
						if actual, e := snapshotPath(prior.Path); e == nil && actual == prior.Snapshot {
							prior.AssetID, prior.RootID, prior.MediaKey = assetID, root.ID, mediaKey(plan.Media)
							owned = append(owned, prior)
						}
					}
				}
			}
			for sideIndex, sidecar := range meta.Sidecars[file.ID] {
				if err = check(); err != nil {
					return err
				}
				originalFailedAuxiliary = &sidecar
				originalFailedAuxiliaryIndex = sideIndex
				sideReceipt, err := a.publishOriginalTransferSide(ctx, job, &plan, index, sideIndex, &meta, quality, check)
				file = &plan.Files[index]
				sidecar = meta.Sidecars[file.ID][sideIndex]
				originalFailedAuxiliary = &sidecar
				if err != nil {
					taskFailures = append(taskFailures, err)
					failures.Messages = append(failures.Messages, a.clean(err.Error()))
					if sidecar.Original != nil {
						extra := originalTransferSideFile(*file, sidecar, sideIndex)
						completeTask(extra, *sidecar.Original, false, err.Error())
					}
					originalFailedAuxiliary = nil
					continue
				}
				if sidecar.Original != nil {
					extra := originalTransferSideFile(*file, sidecar, sideIndex)
					completeTask(extra, *sidecar.Original, true, "")
				}
				originalFailedAuxiliary = nil
				owned = append(owned, managedFile{Path: sidecar.Target, RootID: root.ID, AssetID: assetID, MediaKey: mediaKey(plan.Media), Kind: sidecarMaterialKind(sidecar), Snapshot: sideReceipt.StageSnapshot, SourceSnapshot: sidecar.Snapshot, SHA256: sideReceipt.SHA256})
			}
			asset := Asset{ID: assetID, Media: plan.Media, Season: file.Season, Episodes: append([]int(nil), file.Episodes...), RootID: root.ID, Path: file.Target, SourcePath: file.Source, Size: file.Size, State: "present", ScrapeState: "pending", FNOSState: "pending", IntentID: plan.IntentID, CreatedAt: now()}
			if _, sourced := meta.OriginalTransfer[file.ID]; sourced && mainSucceeded && file.MaterialKind == "" {
				asset.FNOSRefreshTransfer = plan.ID + "/" + file.ID
			}
			asset.Part = file.Part
			if meta.Reorganize != nil {
				asset.IntentID = meta.Reorganize.Asset.IntentID
				if file.Source == meta.Reorganize.Asset.Path {
					asset.SourcePath = meta.Reorganize.Asset.SourcePath
				}
			}
			asset.Renaming = plan.Renaming
			if !wantsScrape {
				asset.ScrapeState = "skipped"
				asset.FNOSState = "unverified"
				if !a.Settings().FNOS.Enabled {
					asset.FNOSState = "disabled"
				}
			}
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if err = a.requireNativeDirectoryAuthorizationTx(ctx, tx, plan, meta); err != nil {
				return err
			}
			if mainSucceeded && file.MaterialKind == "" {
				if err = a.commitQualityReplacementTx(ctx, tx, plan, *file, asset, owned); err != nil {
					return err
				}
			}
			for i, sidecar := range meta.Sidecars[file.ID] {
				var material *managedFile
				for j := range owned {
					if owned[j].Path == sidecar.Target && owned[j].Kind == sidecarMaterialKind(sidecar) {
						material = &owned[j]
						break
					}
				}
				if material == nil {
					continue // A rejected task has no publication or ownership to transfer.
				}
				if err = a.commitDirectoryOverwriteExtraTx(ctx, tx, fmt.Sprintf("%s/%s/side-%d", plan.ID, file.ID, i), asset, *material); err != nil {
					return err
				}
			}
			if mainSucceeded && file.MaterialKind != "" {
				if err = a.commitDirectoryOverwriteExtraTx(ctx, tx, plan.ID+"/"+file.ID, asset, owned[0]); err != nil {
					return err
				}
			}
			if mainSucceeded && file.MaterialKind == "" {
				if receipt.ReusedHardlink {
					if err := checkExistingImportHardlink(ctx, tx, receipt); err != nil {
						return err
					}
					asset.State, asset.IdentitySource, asset.IdentityConfirmedAt = "external", "manual", now()
					proof := libraryIdentity{AssetID: asset.ID, RootID: root.ID, RootPath: root.Path, Path: asset.Path,
						Snapshot: receipt.StageSnapshot, MediaType: asset.Media.MediaType, TMDBID: asset.Media.ID,
						Season: asset.Season, Episodes: asset.Episodes, Source: "manual", ConfirmedAt: asset.IdentityConfirmedAt, Actor: "admin"}
					if err := txPut(ctx, tx, "asset_identity", asset.ID, proof); err != nil {
						return err
					}
				}
				if err = commitImportReorganizeTx(ctx, tx, plan, meta, asset, owned); err != nil {
					return err
				}
				if err = a.commitDirectoryOverwriteTx(ctx, tx, plan.ID+"/"+file.ID, asset); err != nil {
					return err
				}
				for _, episode := range importEpisodes(file.Episodes) {
					var other string
					err = tx.QueryRowContext(ctx, `SELECT asset_id FROM asset_slots WHERE media_key=? AND season=? AND episode=? AND asset_id!=? LIMIT 1`, mediaKey(plan.Media), file.Season, episode, assetID).Scan(&other)
					if err == nil {
						return bad("IMPORT_CONFLICT", "发布后发现同季集资产冲突，文件已保留供人工核对")
					}
					if !errors.Is(err, sql.ErrNoRows) {
						return err
					}
					if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO asset_slots VALUES(?,?,?,?)`, mediaKey(plan.Media), file.Season, episode, assetID); err != nil {
						return err
					}
					owner := plan.ID
					if plan.IntentID != "" {
						owner = plan.IntentID
					}
					if _, err = tx.ExecContext(ctx, `DELETE FROM reservations WHERE media_key=? AND season=? AND episode=? AND intent_id=?`, mediaKey(plan.Media), file.Season, episode, owner); err != nil {
						return err
					}
				}
				if err = txPut(ctx, tx, "asset", assetID, asset); err != nil {
					return err
				}
			}
			for _, record := range owned {
				if err = txPut(ctx, tx, "managed_file", tokenHash(record.Path), record); err != nil {
					return err
				}
			}
			file.Status, file.Reason = "imported", ""
			if !mainSucceeded {
				file.Status, file.Reason = "error", a.clean(mainFailure.Error())
			}
			if err = txPut(ctx, tx, "original_transfer_task_failures", plan.ID+"/"+file.ID, failures); err != nil {
				return err
			}
			if err = txPut(ctx, tx, "import_plan", plan.ID, plan); err != nil {
				return err
			}
			var qualityCleanupCount int
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='subscription_quality_cleanup' AND id=?`, plan.ID+"/"+file.ID).Scan(&qualityCleanupCount); err != nil {
				return err
			}
			if wantsScrape {
				originalMetadataEvent, err = originalTransferMetadataEvent(plan, *file, meta, moduleInfo)
				if err != nil {
					return err
				}
			}
			deferredScrape := false
			if mainSucceeded && meta.Reorganize != nil && !meta.Reorganize.SingleHistory {
				if err = deferOriginalScrapeTargetTx(ctx, tx, job, "reorganize_cleanup", plan.ID, originalMetadataEvent, wantsScrape); err != nil {
					return err
				}
				deferredScrape = true
			}
			if qualityCleanupCount > 0 {
				if err = deferOriginalScrapeTargetTx(ctx, tx, job, "quality_cleanup", plan.ID+"/"+file.ID, originalMetadataEvent, wantsScrape); err != nil {
					return err
				}
				deferredScrape = true
			}
			if deferredScrape {
				originalMetadataEvent = nil
			} else if wantsScrape {
				payload := map[string]any{"id": assetID}
				if len(originalMetadataEvent) > 0 {
					payload["original_metadata_event"] = originalMetadataEvent
				}
				batched, e := recordOriginalScrapeTargetTx(ctx, tx, job, originalMetadataEvent, assetID)
				if e != nil {
					return e
				}
				if batched {
					originalMetadataEvent = nil
				} else if _, err = enqueueTx(ctx, tx, "scrape", payload, job.WorkflowID); err != nil {
					return err
				}
			}
			if mainSucceeded && file.MaterialKind == "" {
				if err = recordDomainEventTx(ctx, tx, assetDomainEvent("import.completed", job, asset)); err != nil {
					return err
				}
			}
			if err = tx.Commit(); err != nil {
				return err
			}
			publicationCommitted = true
			if len(owned) != 0 {
				a.event(ctx, job, "INFO", "import", "published", "已登记本次实际发布成功的文件；源文件保留")
			}
			if len(originalMetadataEvent) > 0 {
				// The same core event consumer is scheduled by the existing scrape
				// job above. Plugin observers still get the ordinary broadcast.
				a.broadcastOriginalPluginEventCoreState(ctx, "metadata.scrape", originalMetadataEvent, true)
			}
			if err = a.finishImportOverwrites(ctx, plan, *file, meta); err != nil {
				return err
			}
			return nil
		}()
		if err != nil {
			if !publicationCommitted {
				file.Status = "error"
				originalFailedFile = file
			}
			file.Reason = a.clean(err.Error())
			return err
		}
		batchFailures = append(batchFailures, taskFailures...)
	}
	if len(batchFailures) > 0 {
		return errors.Join(batchFailures...)
	}
	plan.State = "complete"
	for _, file := range plan.Files {
		if file.Status != "imported" && file.Status != "already_imported" {
			plan.State = "partial"
		}
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = txPut(ctx, tx, "import_plan", plan.ID, plan); err != nil {
		return err
	}
	if err = a.importCompleteIntent(ctx, tx, plan); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) importIntentActive(ctx context.Context, plan ImportPlan) error {
	if intent, ok := downloadReviewIntentOverride(ctx, plan); ok {
		return validImportIntent(plan, intent)
	}
	if plan.IntentID == "" {
		return nil
	}
	var intent Intent
	if err := a.Store.Get(ctx, "intent", plan.IntentID, &intent); err != nil {
		return bad("REVIEW_IMPORT_INTENT", "关联下载意图不存在")
	}
	if intent.DownloaderSnapshot != "" {
		if err := a.guardDownloaderSnapshot(ctx, intent); err != nil {
			return err
		}
	}
	if e := a.guardSeasonImportPlan(ctx, intent, plan); e != nil {
		return e
	}
	if intent.ChatWholeUnscoped && len(plan.Files) > 0 {
		scope, e := a.readCompletedUnscopedPlan(ctx, a.Store.DB, intent, plan)
		if e != nil {
			return e
		}
		if scope != nil {
			if e := a.guardSourcePostAddReceipt(ctx, intent); e != nil {
				return e
			}
		}
	}
	return validImportIntent(plan, intent)
}

func validImportIntent(plan ImportPlan, intent Intent) error {
	if intent.ID != plan.IntentID || mediaKey(intent.Media) != mediaKey(plan.Media) || intent.RootID != plan.RootID {
		return bad("REVIEW_IMPORT_INTENT", "关联下载的影视身份或目标库与整理计划不符")
	}
	if intent.State == "cancelled" {
		return bad("REVIEW_IMPORT_CANCELLED", "下载意图已取消；如需手动整理请创建独立新预览")
	}
	if intent.ImportPlanID != "" && intent.ImportPlanID != plan.ID {
		return bad("REVIEW_IMPORT_REPLACED", "此整理计划已被新的人工核对计划替换，请使用最新预览")
	}
	return nil
}

func importIntentTx(ctx context.Context, tx *sql.Tx, plan ImportPlan) (Intent, error) {
	if plan.IntentID == "" {
		return Intent{}, nil
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='intent' AND id=?`, plan.IntentID).Scan(&raw); err != nil {
		return Intent{}, bad("REVIEW_IMPORT_INTENT", "关联下载意图不存在")
	}
	var intent Intent
	if json.Unmarshal(raw, &intent) != nil {
		return intent, bad("REVIEW_IMPORT_INTENT", "下载意图记录无效")
	}
	return intent, validImportIntent(plan, intent)
}

func (a *App) importCompleteIntent(ctx context.Context, tx *sql.Tx, plan ImportPlan) error {
	if plan.IntentID == "" {
		return nil
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='intent' AND id=?`, plan.IntentID).Scan(&raw); err != nil {
		return err
	}
	var intent Intent
	if json.Unmarshal(raw, &intent) != nil {
		return bad("REVIEW_IMPORT_INTENT", "下载意图记录无效")
	}
	if intent.State == "cancelled" || intent.State == "imported" {
		return nil
	}
	if err := validImportIntent(plan, intent); err != nil {
		return err
	}
	if intent.Media.MediaType == "tv" && len(intent.Episodes) == 0 && !intent.ChatWholeUnscoped {
		return nil
	}
	seasonProof, e := a.intentSeasonProof(ctx, tx, intent)
	if e != nil {
		return e
	}
	scopes := []downloadSeasonScope{{intent.Season, importEpisodes(intent.Episodes)}}
	if seasonProof != nil {
		scopes = seasonProof.Scopes
	}
	if intent.ChatWholeUnscoped {
		completed, err := a.readCompletedUnscopedPlan(ctx, tx, intent, plan)
		if err != nil {
			return err
		}
		if completed != nil {
			scopes = nil
			for _, file := range completed.Files {
				scopes = append(scopes, downloadSeasonScope{Season: file.Season, Episodes: file.Episodes})
			}
		}
	}
	for _, scope := range scopes {
		for _, episode := range scope.Episodes {
			var count int
			err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_slots s JOIN records r ON r.kind='asset' AND r.id=s.asset_id WHERE s.media_key=? AND s.season=? AND s.episode=? AND json_extract(r.payload,'$.state') IN ('present','external')`, mediaKey(intent.Media), scope.Season, episode).Scan(&count)
			if err != nil {
				return err
			}
			if count == 0 {
				return nil
			}
		}
	}
	intent.State = "imported"
	intent.Error = ""
	intent.UpdatedAt = now()
	if err := txPut(ctx, tx, "intent", intent.ID, intent); err != nil {
		return err
	}
	if intent.SubscriptionID != "" {
		var raw []byte
		err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='subscription' AND id=?`, intent.SubscriptionID).Scan(&raw)
		if err == nil {
			var subscription Subscription
			if json.Unmarshal(raw, &subscription) != nil {
				return bad("REVIEW_SUBSCRIPTION", "关联订阅记录无效")
			}
			if subscription.State == "active" && subscription.Revision == intent.SubscriptionRevision {
				_, err = enqueueTx(ctx, tx, "subscription", map[string]any{"id": subscription.ID, "revision": subscription.Revision}, plan.WorkflowID)
				if err != nil {
					var api *APIError
					if !errors.As(err, &api) || api.Code != "JOB_EXISTS" {
						return err
					}
				}
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

func (a *App) importCheck(ctx context.Context, job Job) func() error {
	last := time.Time{}
	return func() error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Since(last) < 250*time.Millisecond {
			return nil
		}
		last = time.Now()
		return a.jobCancelled(ctx, job)
	}
}

func sameInode(a, b importSnapshot) bool {
	return a.Device == b.Device && a.Inode == b.Inode && a.Inode != 0
}

func (a *App) publishImport(ctx context.Context, key, source, target string, locked importSnapshot, method string, check func() error) (importReceipt, error) {
	if method != "copy" && method != "hardlink" {
		return importReceipt{}, bad("INVALID_IMPORT_MODE", "预览中的整理方式无效")
	}
	if !importPath(target) || source == target {
		return importReceipt{}, bad("INVALID_TARGET", "目标路径无效")
	}
	parent, err := importDir(filepath.Dir(target), true)
	if err != nil {
		return importReceipt{}, err
	}
	defer parent.Close()
	receipt := importReceipt{ID: key, Target: target, Source: source, SourceSnapshot: locked, Stage: ".light-media-" + tokenHash(key)[:32] + ".part", State: "planned"}
	var prior importReceipt
	err = a.Store.Get(ctx, "import_receipt", key, &prior)
	if err == nil {
		if prior.Target != target || prior.Source != source || prior.SourceSnapshot != locked || prior.Stage != receipt.Stage {
			return receipt, bad("REVIEW_RECEIPT", "发布收据与计划不一致")
		}
		receipt = prior
	} else if !errors.Is(err, sql.ErrNoRows) {
		return receipt, err
	} else if err = a.Store.Put(ctx, "import_receipt", key, receipt); err != nil {
		return receipt, err
	}
	current, openErr := snapshotPath(target)
	if openErr == nil {
		if (receipt.State == "staged" || receipt.State == "published") && current == receipt.StageSnapshot {
			receipt.State = "published"
			if err = a.Store.Put(ctx, "import_receipt", key, receipt); err != nil {
				return receipt, err
			}
			_ = unlinkImportStage(parent, receipt)
			return receipt, nil
		}
		return receipt, bad("IMPORT_CONFLICT", "目标已存在且不属于本次持久发布收据，禁止覆盖")
	}
	if !os.IsNotExist(openErr) {
		return receipt, bad("IMPORT_CONFLICT", "无法确认目标不存在")
	}
	if receipt.State == "published" {
		return receipt, bad("REVIEW_PUBLISHED_MISSING", "已发布文件被外部移除，不能自动重新发布")
	}
	stagePath := filepath.Join(filepath.Dir(target), receipt.Stage)
	if stage, err := snapshotPath(stagePath); err == nil {
		if receipt.State == "staged" && stage == receipt.StageSnapshot {
			if err = check(); err != nil {
				return receipt, err
			}
			if err = unix.Linkat(int(parent.Fd()), receipt.Stage, int(parent.Fd()), filepath.Base(target), 0); err != nil {
				return receipt, bad("IMPORT_CONFLICT", "目标在发布时发生冲突")
			}
			if err = parent.Sync(); err != nil {
				return receipt, err
			}
			if actual, verifyErr := snapshotPath(target); verifyErr != nil || actual != receipt.StageSnapshot {
				return receipt, bad("REVIEW_TARGET_CHANGED", "发布目录或目标在提交时发生变化，已保留收据供核对")
			}
			receipt.State = "published"
			if err = a.Store.Put(ctx, "import_receipt", key, receipt); err != nil {
				return receipt, err
			}
			_ = unlinkImportStage(parent, receipt)
			return receipt, nil
		}
		if receipt.State != "created" || !sameInode(stage, receipt.StageSnapshot) {
			return receipt, bad("REVIEW_STAGE", "临时文件没有有效所有权收据，请人工核对")
		}
		if err = unix.Unlinkat(int(parent.Fd()), receipt.Stage, 0); err != nil {
			return receipt, err
		}
	} else if !os.IsNotExist(err) {
		return receipt, bad("REVIEW_STAGE", "临时文件路径不可安全访问")
	} else if receipt.State == "staged" {
		return receipt, bad("REVIEW_STAGE_MISSING", "已持久化的临时发布文件消失，不能盲目重新发布")
	}
	input, err := importOpen(source)
	if err != nil {
		return receipt, bad("REVIEW_SOURCE", "源文件不可访问或已变化")
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !locked.matches(info) {
		return receipt, bad("REVIEW_SOURCE_CHANGED", "源文件设备、inode、大小或修改时间已变化，请重新预览")
	}
	if err = check(); err != nil {
		return receipt, err
	}
	storage := originalTransferStorageFromContext(ctx)
	if storage != nil && storage.Source != nil {
		item, err := originalLocalFileItem(source)
		if err != nil {
			return receipt, err
		}
		if member, ok := ctx.Value(originalDirectorySourceItemKey{}).(*plugins.ModuleFileItem); ok {
			var path string
			if json.Unmarshal(member.Fields["path"], &path) != nil || path != source {
				return receipt, bad("REVIEW_RECEIPT", "目录成员文件项与源路径不同")
			}
			item = member
		}
		operation, path := originalTransferType(method), target
		var name *string
		if method == "copy" {
			path = filepath.Dir(target)
			filename := filepath.Base(target)
			name = &filename
		}
		result, err := storage.Source.call(ctx, operation, item, path, name, stagePath)
		if err != nil {
			return receipt, err
		}
		if !originalModuleResultTruthy(result) {
			return receipt, bad("PLUGIN_STORAGE_FAILED", "插件存储文件操作失败")
		}
		if method == "copy" {
			copy, err := importOpen(stagePath)
			if err != nil {
				return receipt, err
			}
			digest, err := importCopy(ctx, copy, io.Discard, check)
			if err == nil {
				err = copy.Sync()
			}
			copy.Close()
			if err != nil {
				return receipt, err
			}
			sourceDigest, err := importCopy(ctx, input, io.Discard, check)
			if err != nil {
				return receipt, err
			}
			if digest != sourceDigest {
				return receipt, bad("REVIEW_COPY_CONTENT", "插件复制内容与已核对的源文件不同")
			}
			receipt.SHA256 = digest
		}
	} else if method == "hardlink" {
		sourceDir, err := importDir(filepath.Dir(source), false)
		if err != nil {
			return receipt, err
		}
		linkSource := a.importSourceLink
		if linkSource == nil {
			linkSource = unix.Linkat
		}
		err = linkSource(int(sourceDir.Fd()), filepath.Base(source), int(parent.Fd()), receipt.Stage, 0)
		sourceDir.Close()
		if err != nil {
			if errors.Is(err, unix.EXDEV) {
				return receipt, &importEXDEVFailure{app: a, receipt: receipt, cause: bad("IMPORT_LINK_CROSS_DEVICE", "实际目标跨文件系统，硬链接不可用")}
			}
			return receipt, bad("IMPORT_LINK_FAILED", "硬链接失败，未回退复制，请核对文件系统和权限")
		}
	} else {
		available, err := importAvailableSpace(parent)
		if err != nil {
			return receipt, err
		}
		if available < 64<<10 || locked.Size > available-(64<<10) {
			return receipt, bad("IMPORT_SPACE_INSUFFICIENT", "实际复制所需空间不足，源文件与正式目标未改变")
		}
		fd, err := unix.Openat(int(parent.Fd()), receipt.Stage, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0640)
		if err != nil {
			return receipt, bad("IMPORT_CONFLICT", "无法独占创建整理临时文件")
		}
		output := os.NewFile(uintptr(fd), stagePath)
		createdInfo, err := output.Stat()
		if err == nil {
			receipt.StageSnapshot, err = snapshot(createdInfo)
		}
		if err == nil {
			receipt.State = "created"
			err = a.Store.Put(ctx, "import_receipt", key, receipt)
		}
		if err == nil {
			receipt.SHA256, err = importCopy(ctx, input, output, check)
		}
		if err == nil {
			err = output.Sync()
		}
		output.Close()
		if err != nil {
			_ = unlinkImportStage(parent, receipt)
			return receipt, err
		}
	}
	stageInfo, err := snapshotPath(stagePath)
	if err != nil {
		return receipt, err
	}
	if method == "hardlink" && !sameInode(stageInfo, locked) {
		_ = unix.Unlinkat(int(parent.Fd()), receipt.Stage, 0)
		return receipt, bad("REVIEW_SOURCE_CHANGED", "硬链接源在创建时发生替换")
	}
	if method == "hardlink" {
		if err = input.Sync(); err != nil {
			return receipt, err
		}
	}
	if info, err = input.Stat(); err != nil || !locked.matches(info) {
		_ = unix.Unlinkat(int(parent.Fd()), receipt.Stage, 0)
		return receipt, bad("REVIEW_SOURCE_CHANGED", "整理期间源文件发生变化")
	}
	if stageInfo.Size != locked.Size {
		_ = unix.Unlinkat(int(parent.Fd()), receipt.Stage, 0)
		return receipt, bad("REVIEW_COPY_SIZE", "复制文件大小不一致")
	}
	receipt.StageSnapshot = stageInfo
	receipt.State = "staged"
	if err = a.Store.Put(ctx, "import_receipt", key, receipt); err != nil {
		return receipt, err
	}
	if err = check(); err != nil {
		return receipt, err
	}
	if err = unix.Linkat(int(parent.Fd()), receipt.Stage, int(parent.Fd()), filepath.Base(target), 0); err != nil {
		return receipt, bad("IMPORT_CONFLICT", "目标在发布时发生冲突，原文件未覆盖")
	}
	if err = parent.Sync(); err != nil {
		return receipt, err
	}
	if actual, verifyErr := snapshotPath(target); verifyErr != nil || actual != receipt.StageSnapshot {
		return receipt, bad("REVIEW_TARGET_CHANGED", "发布目录或目标在提交时发生变化，已保留收据供核对")
	}
	receipt.State = "published"
	if err = a.Store.Put(ctx, "import_receipt", key, receipt); err != nil {
		return receipt, err
	}
	if err = unlinkImportStage(parent, receipt); err != nil {
		return receipt, err
	}
	return receipt, nil
}

func unlinkImportStage(parent *os.File, receipt importReceipt) error {
	current, err := snapshotPath(filepath.Join(parent.Name(), receipt.Stage))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !sameInode(current, receipt.StageSnapshot) {
		return bad("REVIEW_STAGE", "临时文件所有权已变化")
	}
	if err = unix.Unlinkat(int(parent.Fd()), receipt.Stage, 0); err != nil {
		return err
	}
	return parent.Sync()
}
