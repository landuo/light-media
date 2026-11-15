package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"light-media/internal/download"
)

// Only semantic preparation failures are terminal. In particular an arbitrary
// APIError may be a paused worker, refreshed plugin, or unavailable service.
func completedDownloadPreparationFailure(err error) bool {
	var api *APIError
	if !errors.As(err, &api) {
		return false
	}
	switch api.Code {
	case "REVIEW_DOWNLOAD_COVERAGE", "REVIEW_DOWNLOAD_FILES", "REVIEW_EPISODES",
		"REVIEW_FILE_IDENTITY", "REVIEW_SEASON_SCOPE", "REVIEW_ACTUAL_SIZE", "QUALITY_FILE_IDENTITY_CHANGED",
		"INVALID_DOWNLOAD_CONTEXT", "INVALID_IMPORT", "INVALID_ROOT_KIND",
		"INVALID_METADATA", "MEDIA_NOT_FOUND", "INVALID_TARGET", "INVALID_MAPPING",
		"MATERIAL_CORRUPT", "INVALID_MATERIAL_REF":
		return true
	}
	return false
}

func (a *App) completedDownloadSourcePlan(ctx context.Context, d DownloaderConfig, in Intent, task download.Task, files []download.File) (ImportPlan, error) {
	plan := ImportPlan{IntentID: in.ID, WorkflowID: in.WorkflowID, Media: in.Media, RootID: in.RootID, Mode: in.ImportMode}
	if plan.Mode == "" {
		plan.Mode = "hardlink"
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return ImportPlan{}, err
		}
		if !isVideo(file.Path) {
			continue
		}
		if file.Progress < 1 {
			return ImportPlan{}, bad("DOWNLOAD_NOT_COMPLETE", "视频文件尚未下载完成，等待下一轮")
		}
		if filepath.IsAbs(file.Path) || strings.ContainsAny(file.Path, "\x00\r\n") || filepath.Clean(file.Path) == ".." || strings.HasPrefix(filepath.Clean(file.Path), ".."+string(filepath.Separator)) {
			return ImportPlan{}, bad("INVALID_DOWNLOAD_PATH", "下载器返回的视频文件路径越界")
		}
		full, err := mapDownloaderPath(a.Settings(), d, filepath.Join(task.DownloadDir, filepath.FromSlash(file.Path)))
		if err != nil {
			return ImportPlan{}, err
		}
		stamp, err := snapshotPath(full)
		if err != nil {
			return ImportPlan{}, err
		}
		if file.Size <= 0 || stamp.Size != file.Size {
			return ImportPlan{}, bad("DOWNLOAD_FILE_NOT_READY", "下载文件大小与下载器记录不一致，等待重新检查")
		}
		plan.Files = append(plan.Files, PlanFile{Source: full, Size: stamp.Size, Device: stamp.Device, Inode: stamp.Inode, ModNS: stamp.ModNS, Season: in.Season})
	}
	return plan, nil
}

// Freeze the failed source set on the intent, then reuse the existing per-file
// blocked submission path. Its job gate also resumes interrupted persistence
// without creating a fresh failure attempt on every downloader poll.
func (a *App) recordCompletedDownloadPreparationFailure(ctx context.Context, in *Intent, plan ImportPlan, cause error) error {
	if len(plan.Files) == 0 {
		return nil
	}
	plan.ID, plan.CreatedAt, plan.State = id("plan_"), now(), "failed"
	for i := range plan.Files {
		plan.Files[i].ID = id("file_")
		plan.Files[i].Status, plan.Files[i].Reason = "blocked", a.clean(cause.Error())
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := importIntentTx(ctx, tx, plan)
	if err != nil {
		return err
	}
	if current.ImportPlanID != "" {
		*in = current
		return nil
	}
	if current.State != "downloaded" {
		return nil
	}
	current.ImportPlanID, current.Error = plan.ID, a.clean(cause.Error())
	if err = txPut(ctx, tx, "import_plan", plan.ID, plan); err != nil {
		return err
	}
	if err = txPut(ctx, tx, "intent", current.ID, current); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	*in = current
	return a.enqueueCompletedImportFiles(ctx, plan)
}
