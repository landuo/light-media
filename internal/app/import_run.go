package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

type importReceipt struct {
	ID             string         `json:"id"`
	Target         string         `json:"target"`
	Source         string         `json:"source"`
	SourceSnapshot importSnapshot `json:"source_snapshot"`
	Stage          string         `json:"stage"`
	StageSnapshot  importSnapshot `json:"stage_snapshot"`
	State          string         `json:"state"`
	SHA256         string         `json:"sha256,omitempty"`
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
	selected, err := selectImportFiles(plan, fileIDs)
	if err != nil {
		return Job{}, err
	}
	root, err := a.importRoot(plan.RootID)
	if err != nil {
		return Job{}, err
	}
	var meta importMetadata
	if err = a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil {
		return Job{}, err
	}
	if root.Path != meta.RootPath {
		return Job{}, bad("REVIEW_ROOT_CHANGED", "目标库路径在预览后发生变化，请重新预览")
	}
	if a.paused() {
		return Job{}, &APIError{"AUTOMATION_PAUSED", "自动化已暂停，请先恢复", 409}
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
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
		if _, err = importIntentTx(ctx, tx, plan); err != nil {
			return Job{}, err
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
		if err = importReserve(ctx, tx, plan, file); err != nil {
			return Job{}, err
		}
	}
	plan.State = "queued"
	if err = txPut(ctx, tx, "import_plan", plan.ID, plan); err != nil {
		return Job{}, err
	}
	job, err := enqueueTx(ctx, tx, "import", map[string]any{"id": plan.ID, "file_ids": fileIDs}, plan.WorkflowID)
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

func importReserve(ctx context.Context, tx *sql.Tx, plan ImportPlan, file PlanFile) error {
	owner := plan.ID
	if plan.IntentID != "" {
		owner = plan.IntentID
	}
	for _, episode := range importEpisodes(file.Episodes) {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_slots WHERE media_key=? AND season=? AND episode=?`, mediaKey(plan.Media), file.Season, episode).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return bad("IMPORT_CONFLICT", "影片或季集已存在资产")
		}
		var current string
		err := tx.QueryRowContext(ctx, `SELECT intent_id FROM reservations WHERE media_key=? AND season=? AND episode=?`, mediaKey(plan.Media), file.Season, episode).Scan(&current)
		if err == nil {
			if current != owner {
				return bad("IMPORT_CONFLICT", "影片或季集被其他任务占用")
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
	if err := a.importIntentActive(ctx, plan); err != nil {
		return err
	}
	var meta importMetadata
	if err := a.Store.Get(ctx, "import_meta", planID, &meta); err != nil {
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
	plan.State = "running"
	if err = a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
		return err
	}
	defer func() {
		if returnErr != nil {
			plan.State = "partial"
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = a.Store.Put(cleanupCtx, "import_plan", plan.ID, plan)
		}
	}()
	for _, index := range indices {
		file := &plan.Files[index]
		if file.Status == "imported" || file.Status == "already_imported" {
			continue
		}
		if err = a.jobCancelled(ctx, job); err != nil {
			return err
		}
		if !a.sourceAllowed(file.Source) || !within(root.Path, file.Target) {
			return bad("REVIEW_PATH_CHANGED", "允许路径发生变化，请重新预览")
		}
		if err = a.ensureNotExcluded(ctx, plan.Media, file.Season, file.Episodes); err != nil {
			return err
		}
		release, err := a.beginWrite()
		if err != nil {
			return err
		}
		err = func() error {
			defer release()
			file.Status = "importing"
			file.Reason = ""
			if err := a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
				return err
			}
			check := a.importCheck(ctx, job)
			receipt, err := a.publishImport(ctx, plan.ID+"/"+file.ID, file.Source, file.Target, planSnapshot(*file), meta.Methods[file.ID], check)
			var linkErr *APIError
			if err != nil && plan.Mode == "auto" && errors.As(err, &linkErr) && linkErr.Code == "IMPORT_LINK_CROSS_DEVICE" {
				meta.Methods[file.ID] = "copy"
				file.Method = "copy"
				if saveErr := a.Store.Put(ctx, "import_meta", plan.ID, meta); saveErr != nil {
					return saveErr
				}
				a.event(ctx, job, "WARN", "import", "copy_fallback", "目标文件系统与预览时不同，按自动模式回退复制，源文件保留")
				receipt, err = a.publishImport(ctx, plan.ID+"/"+file.ID, file.Source, file.Target, planSnapshot(*file), "copy", check)
			}
			if err != nil {
				return err
			}
			assetID := file.AssetID
			if assetID == "" {
				assetID = id("asset_")
				file.AssetID = assetID
				if err = a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
					return err
				}
			}
			owned := []managedFile{{Path: file.Target, RootID: root.ID, AssetID: assetID, MediaKey: mediaKey(plan.Media), Kind: "video", Snapshot: receipt.StageSnapshot, SourceSnapshot: planSnapshot(*file), SHA256: receipt.SHA256}}
			for sideIndex, sidecar := range meta.Sidecars[file.ID] {
				if err = check(); err != nil {
					return err
				}
				sideReceipt, err := a.publishImport(ctx, fmt.Sprintf("%s/%s/side-%d", plan.ID, file.ID, sideIndex), sidecar.Source, sidecar.Target, sidecar.Snapshot, "copy", check)
				if err != nil {
					return err
				}
				owned = append(owned, managedFile{Path: sidecar.Target, RootID: root.ID, AssetID: assetID, MediaKey: mediaKey(plan.Media), Kind: "subtitle", Snapshot: sideReceipt.StageSnapshot, SourceSnapshot: sidecar.Snapshot, SHA256: sideReceipt.SHA256})
			}
			asset := Asset{ID: assetID, Media: plan.Media, Season: file.Season, Episodes: append([]int(nil), file.Episodes...), RootID: root.ID, Path: file.Target, SourcePath: file.Source, Size: file.Size, State: "present", ScrapeState: "pending", FNOSState: "pending", IntentID: plan.IntentID, CreatedAt: now()}
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
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
			for _, record := range owned {
				if err = txPut(ctx, tx, "managed_file", tokenHash(record.Path), record); err != nil {
					return err
				}
			}
			file.Status = "imported"
			file.Reason = ""
			if err = txPut(ctx, tx, "import_plan", plan.ID, plan); err != nil {
				return err
			}
			if _, err = enqueueTx(ctx, tx, "scrape", map[string]string{"id": assetID}, job.WorkflowID); err != nil {
				return err
			}
			if err = recordDomainEventTx(ctx, tx, assetDomainEvent("import.completed", job, asset)); err != nil {
				return err
			}
			if err = tx.Commit(); err != nil {
				return err
			}
			a.event(ctx, job, "INFO", "import", "published", "视频和字幕已无覆盖发布；源文件保留，资产已登记")
			return nil
		}()
		if err != nil {
			file.Status = "error"
			file.Reason = a.clean(err.Error())
			return err
		}
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
	if err = importCompleteIntent(ctx, tx, plan); err != nil {
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

func importCompleteIntent(ctx context.Context, tx *sql.Tx, plan ImportPlan) error {
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
	if intent.Media.MediaType == "tv" && len(intent.Episodes) == 0 {
		return nil
	}
	for _, episode := range importEpisodes(intent.Episodes) {
		var count int
		err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_slots s JOIN records r ON r.kind='asset' AND r.id=s.asset_id WHERE s.media_key=? AND s.season=? AND s.episode=? AND json_extract(r.payload,'$.state') IN ('present','external')`, mediaKey(intent.Media), intent.Season, episode).Scan(&count)
		if err != nil {
			return err
		}
		if count == 0 {
			return nil
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
	if method == "hardlink" {
		sourceDir, err := importDir(filepath.Dir(source), false)
		if err != nil {
			return receipt, err
		}
		err = unix.Linkat(int(sourceDir.Fd()), filepath.Base(source), int(parent.Fd()), receipt.Stage, 0)
		sourceDir.Close()
		if err != nil {
			if errors.Is(err, unix.EXDEV) {
				return receipt, bad("IMPORT_LINK_CROSS_DEVICE", "实际目标跨文件系统，硬链接不可用")
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
