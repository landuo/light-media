package app

import (
	"context"
	"light-media/internal/download"
	"path/filepath"
)

func effectiveIntentDownloadPath(in Intent, d DownloaderConfig) string {
	if in.DownloadPath != "" {
		return in.DownloadPath
	}
	return d.DownloadPath
}
func subscriptionLegacyRoot(s Subscription) string {
	if s.DownloadPath == "" && s.DirectoryID == "" {
		return s.RootID
	}
	return ""
}

// A downloader's save location is frozen independently from the library rule.
// Missing/disabled rules never turn a completed download into a library write.
func (a *App) guardIntentDirectory(ctx context.Context, in Intent, forImport bool) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if in.DirectoryID == "" {
		if forImport && in.RootID == "" {
			return bad("DIRECTORY_MONITOR_NOT_CONFIGURED", "下载已完成；此下载目录未配置启用的整理规则")
		}
		return nil
	}
	rule, e := a.getDirectory(in.DirectoryID)
	if e != nil {
		return e
	}
	if in.DirectorySnapshot == "" || directoryFingerprint(rule) != in.DirectorySnapshot {
		return bad("REVIEW_DIRECTORY_CHANGED", "目录整理规则已改变，请核对已下载文件后手动整理")
	}
	if rule.DownloaderID != "" && (rule.DownloaderID != in.DownloaderID || !within(rule.DownloadPath, in.DownloadPath)) {
		return bad("REVIEW_DIRECTORY_CHANGED", "下载器或保存目录与已确认规则不符")
	}
	d, e := a.downloader(in.DownloaderID)
	if e != nil {
		return e
	}
	source, e := mapDownloaderPath(a.Settings(), d, in.DownloadPath)
	if e != nil {
		return e
	}
	ruleSource, e := a.directorySource(rule)
	if e != nil || !sharedDirectoryContains(ruleSource, source) {
		return bad("REVIEW_DIRECTORY_CHANGED", "下载目录已离开已确认监听范围")
	}
	if forImport && (!rule.Enabled || rule.MonitorType == "none") {
		return bad("DIRECTORY_MONITOR_DISABLED", "下载已完成；目录监听未启用，源文件保留")
	}
	return nil
}
func (a *App) bindCompletedDirectory(ctx context.Context, in *Intent, task download.Task, d DownloaderConfig) error {
	if in.DirectoryID == "" && in.RootID == "" {
		destination, e := a.resolveDownloadDestinationContext(ctx, d, "", effectiveIntentDownloadPath(*in, d), "", in.Media.MediaType)
		if e != nil {
			return e
		}
		if destination.Rule.ID != "" {
			in.DirectoryID = destination.Rule.ID
			in.DirectorySnapshot = destination.Snapshot
			in.RootID = destination.Root
			in.ImportMode = destination.Mode
			if e = a.Store.Put(ctx, "intent", in.ID, *in); e != nil {
				return e
			}
		}
	}
	if e := a.guardIntentDirectory(ctx, *in, true); e != nil {
		return e
	}
	if in.DownloadPath != "" && filepath.Clean(task.DownloadDir) != filepath.Clean(in.DownloadPath) {
		return bad("REVIEW_DOWNLOAD_PATH_CHANGED", "下载器实际保存目录与确认路径不同，请人工核对")
	}
	if in.DirectoryID != "" && !directoryTaskComplete(task) {
		return bad("DOWNLOAD_NOT_COMPLETE", "下载器尚未确认文件完成或正在校验，等待下一轮")
	}
	return nil
}
func (a *App) guardImportDirectory(ctx context.Context, plan ImportPlan) error {
	if e := a.guardDirectoryFilePlan(ctx, plan.ID); e != nil {
		return e
	}
	if plan.IntentID == "" {
		return nil
	}
	var in Intent
	if e := a.Store.Get(ctx, "intent", plan.IntentID, &in); e != nil {
		return e
	}
	// An explicit human review is separately authorized and may override a rule.
	var review downloadReviewRecord
	if a.Store.Get(ctx, "download_review", plan.ID, &review) == nil && review.Confirmed {
		return nil
	}
	return a.guardIntentDirectory(ctx, in, true)
}

func (a *App) recordDirectoryDownloaderStatus(ctx context.Context, downloaderID string, err error) {
	state := managedConnection{Status: "ok", CheckedAt: now()}
	if err != nil {
		state.Status = "error"
		state.Error = a.clean(err.Error())
	}
	for _, rule := range a.Settings().Directories {
		if rule.Enabled && rule.MonitorType == "downloader" && rule.DownloaderID == downloaderID {
			_ = a.Store.Put(ctx, "directory_status", rule.ID, state)
		}
	}
}
func (a *App) recordDirectoryIntentError(ctx context.Context, in Intent, err error) {
	if in.DirectoryID == "" {
		return
	}
	rule, e := a.getDirectory(in.DirectoryID)
	if e != nil || rule.MonitorType != "downloader" {
		return
	}
	_ = a.Store.Put(ctx, "directory_status", rule.ID, managedConnection{Status: "error", CheckedAt: now(), Error: a.clean(err.Error())})
}

func directoryTaskComplete(task download.Task) bool {
	return task.Progress >= 1 && task.TotalSize > 0 && task.Completed >= task.TotalSize && (task.Status == "seeding" || task.Status == "paused" || task.Status == "queued")
}
