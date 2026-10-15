package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/download"
	"light-media/internal/media"
)

type DownloadReviewRequest struct {
	MediaType string              `json:"media_type"`
	TMDBID    int64               `json:"tmdb_id"`
	RootID    string              `json:"root_id"`
	Mode      string              `json:"mode"`
	Files     []ImportFileRequest `json:"files"`
	Mappings  []media.MappingRule `json:"mappings"`
}
type DownloadReviewPreview struct {
	Plan                  ImportPlan    `json:"plan"`
	IdentityChanged       bool          `json:"identity_changed"`
	TargetChanged         bool          `json:"target_changed"`
	PreviousMedia         catalog.Media `json:"previous_media"`
	PreviousSeason        int           `json:"previous_season"`
	PreviousEpisodes      []int         `json:"previous_episodes"`
	SubscriptionWillPause bool          `json:"subscription_will_pause"`
	ExpiresAt             string        `json:"expires_at"`
}
type downloadReviewRecord struct {
	DownloadReviewPreview
	IntentID           string              `json:"intent_id"`
	Fingerprint        string              `json:"fingerprint"`
	Confirmed          bool                `json:"confirmed"`
	Season             int                 `json:"season"`
	Episodes           []int               `json:"episodes"`
	Mappings           []media.MappingRule `json:"mappings"`
	DownloaderSnapshot string              `json:"downloader_snapshot"`
}
type downloadReviewOverrideKey struct{}

// Only this server-side preview path supplies the override. It lets ordinary
// import validation examine the proposed target without changing the intent.
func downloadReviewIntentOverride(ctx context.Context, plan ImportPlan) (Intent, bool) {
	in, ok := ctx.Value(downloadReviewOverrideKey{}).(Intent)
	if !ok || in.ID != plan.IntentID || mediaKey(in.Media) != mediaKey(plan.Media) || in.RootID != plan.RootID {
		return Intent{}, false
	}
	in.ImportPlanID = plan.ID
	return in, true
}
func (a *App) guardDownloadReviewPlan(ctx context.Context, planID string) error {
	var review downloadReviewRecord
	if err := a.Store.Get(ctx, "download_review", planID, &review); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if !review.Confirmed {
		return bad("CONFIRM_REVIEW_REQUIRED", "请从关联下载的纠错预览显式确认，不能直接执行此计划")
	}
	var in Intent
	if err := a.Store.Get(ctx, "intent", review.IntentID, &in); err != nil {
		return err
	}
	if in.ImportPlanID != planID || in.State == "cancelled" {
		return bad("REVIEW_PLAN_REPLACED", "整理计划已被替换或下载已取消")
	}
	return nil
}
func downloadReviewFingerprint(in Intent) string {
	raw, _ := json.Marshal(struct {
		Key                                      string
		Season                                   int
		Episodes                                 []int
		Root, Hash, Plan, Subscription, Snapshot string
		Revision                                 int
	}{mediaKey(in.Media), in.Season, in.Episodes, in.RootID, in.Hash, in.ImportPlanID, in.SubscriptionID, in.DownloaderSnapshot, in.SubscriptionRevision})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

type downloadReviewFile struct {
	download.File
	ApplicationPath string            `json:"application_path"`
	MappingError    string            `json:"mapping_error,omitempty"`
	Video           bool              `json:"is_video"`
	Parsed          media.ParseResult `json:"parsed"`
}
type downloadReviewFiles struct {
	DownloaderSnapshot string               `json:"downloader_snapshot"`
	Intent             Intent               `json:"intent"`
	Task               download.Task        `json:"task"`
	Files              []downloadReviewFile `json:"files"`
}

func (a *App) downloadFiles(ctx context.Context, key string) (downloadReviewFiles, error) {
	var out downloadReviewFiles
	if err := a.Store.Get(ctx, "intent", key, &out.Intent); err != nil {
		return out, err
	}
	d, err := a.downloader(out.Intent.DownloaderID)
	if err != nil {
		return out, err
	}
	out.DownloaderSnapshot = downloaderSnapshot(d)
	client, err := download.New(d.Config)
	if err != nil {
		return out, err
	}
	tasks, err := client.List(ctx)
	if err != nil {
		return out, err
	}
	found := false
	for _, task := range tasks {
		if sameHash(task.Hash, out.Intent.Hash) {
			out.Task = task
			found = true
			break
		}
	}
	if !found {
		return out, bad("DOWNLOAD_TASK_UNAVAILABLE", "原下载器任务不可见；未修改任务或源文件")
	}
	files, err := client.Files(ctx, out.Task.Hash)
	if err != nil {
		return out, err
	}
	out.Files = []downloadReviewFile{}
	for _, file := range files {
		entry := downloadReviewFile{File: file, Video: isVideo(file.Path), Parsed: media.Parse(filepath.Base(file.Path))}
		entry.ApplicationPath, err = mapDownloaderPath(a.Settings(), d, filepath.Join(out.Task.DownloadDir, filepath.FromSlash(file.Path)))
		if err != nil {
			entry.MappingError = a.clean(err.Error())
		}
		out.Files = append(out.Files, entry)
	}
	return out, nil
}
func (a *App) previewDownloadReview(ctx context.Context, key string, request DownloadReviewRequest) (DownloadReviewPreview, error) {
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	a.importMu.Lock()
	defer a.importMu.Unlock()
	out := DownloadReviewPreview{}
	files, err := a.downloadFiles(ctx, key)
	if err != nil {
		return out, err
	}
	in := files.Intent
	if in.State == "cancelled" || in.State == "imported" || files.Task.Progress < 1 {
		return out, bad("REVIEW_DOWNLOAD_NOT_READY", "仅可为未取消且已完成的下载生成纠错预览")
	}
	if request.MediaType == "" {
		request.MediaType = in.Media.MediaType
	}
	if request.TMDBID == 0 {
		request.TMDBID = in.Media.ID
	}
	if request.RootID == "" {
		request.RootID = in.RootID
	}
	if request.Mode == "" {
		request.Mode = in.ImportMode
	}
	if request.Mode == "" {
		request.Mode = "hardlink"
	}
	if len(request.Files) == 0 || len(request.Files) > 2000 {
		return out, bad("INVALID_REVIEW_FILES", "请明确选择1至2000个下载完成的视频文件")
	}
	allowed := map[string]bool{}
	for _, file := range files.Files {
		if file.Video && file.Progress >= 1 && file.ApplicationPath != "" {
			allowed[file.ApplicationPath] = true
		}
	}
	season := 0
	episodes := []int{}
	for _, file := range request.Files {
		if !allowed[file.Path] {
			return out, bad("INVALID_REVIEW_SOURCE", "只能选择原下载器已完成且路径已映射的视频文件")
		}
		if request.MediaType == "tv" {
			if file.Season < 1 || len(file.Episodes) != 1 || file.Episodes[0] < 1 {
				return out, bad("REVIEW_NUMBERING_REQUIRED", "请为每个视频明确填写一个标准季集")
			}
			if season != 0 && season != file.Season {
				return out, bad("REVIEW_MULTIPLE_SEASONS", "本次纠错请只选择同一标准季")
			}
			season = file.Season
			episodes = append(episodes, file.Episodes[0])
		}
	}
	if request.MediaType == "tv" {
		episodes, err = subscriptionNumbers(episodes)
		if err != nil {
			return out, err
		}
	}
	identity, err := a.media(ctx, request.MediaType, request.TMDBID)
	if err != nil {
		return out, err
	}
	identityChanged := mediaKey(identity) != mediaKey(in.Media)
	targetChanged := identityChanged || request.RootID != in.RootID || season != in.Season
	for _, ep := range episodes {
		if !containsInt(in.Episodes, ep) {
			targetChanged = true
		}
	}
	if !targetChanged {
		episodes = append([]int{}, in.Episodes...)
	}
	var active int
	if err = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind='import' AND state IN ('queued','running','retry_wait','waiting_paused') AND json_extract(payload,'$.id')=?`, in.ImportPlanID).Scan(&active); err != nil {
		return out, err
	}
	if active > 0 {
		return out, bad("REVIEW_IMPORT_BUSY", "关联整理仍在队列或执行，请先取消该整理任务后重新预览")
	}
	if targetChanged {
		var assets int
		if err = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='asset' AND json_extract(payload,'$.intent_id')=?`, in.ID).Scan(&assets); err != nil {
			return out, err
		}
		if assets > 0 {
			return out, bad("REVIEW_PARTIAL_IDENTITY", "此下载已有入库资产，不能重定向已发布身份；请先核对原资产")
		}
	}
	shadow := in
	shadow.Media = identity
	shadow.RootID = request.RootID
	shadow.ImportPlanID = ""
	previewCtx := context.WithValue(ctx, downloadReviewOverrideKey{}, shadow)
	plan, err := a.previewImport(previewCtx, ImportRequest{MediaType: identity.MediaType, TMDBID: identity.ID, RootID: request.RootID, Mode: request.Mode, IntentID: in.ID, WorkflowID: in.WorkflowID, Files: request.Files, Mappings: request.Mappings})
	if err != nil {
		return out, err
	}
	out = DownloadReviewPreview{Plan: plan, IdentityChanged: identityChanged, TargetChanged: targetChanged, PreviousMedia: in.Media, PreviousSeason: in.Season, PreviousEpisodes: append([]int{}, in.Episodes...), SubscriptionWillPause: targetChanged && in.SubscriptionID != "", ExpiresAt: time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339Nano)}
	if out.SubscriptionWillPause {
		out.Plan.Warnings = append(out.Plan.Warnings, "确认后原订阅将暂停；原占位将转移到此次明确目标，下载器任务和源文件保留。")
	}
	if in.DownloaderSnapshot != files.DownloaderSnapshot {
		out.Plan.Warnings = append(out.Plan.Warnings, "下载器地址、目录或映射已变化；确认此纠错预览会采用当前明确显示的文件路径。")
	}
	if err = a.Store.Put(ctx, "import_plan", plan.ID, out.Plan); err != nil {
		return out, err
	}
	record := downloadReviewRecord{DownloadReviewPreview: out, IntentID: in.ID, Fingerprint: downloadReviewFingerprint(in), Season: season, Episodes: episodes, Mappings: request.Mappings, DownloaderSnapshot: files.DownloaderSnapshot}
	return out, a.Store.Put(ctx, "download_review", plan.ID, record)
}

func (a *App) confirmDownloadReview(ctx context.Context, key, planID string, confirmTargetChange bool) (Job, error) {
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	a.importMu.Lock()
	defer a.importMu.Unlock()
	a.subMu.Lock()
	defer a.subMu.Unlock()
	finish, err := a.beginWrite()
	if err != nil {
		return Job{}, err
	}
	defer finish()
	var review downloadReviewRecord
	if err = a.Store.Get(ctx, "download_review", planID, &review); err != nil {
		return Job{}, err
	}
	if review.IntentID != key {
		return Job{}, bad("INVALID_REVIEW_INTENT", "纠错预览不属于此下载")
	}
	if review.Confirmed {
		return scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind='import' AND json_extract(payload,'$.id')=? ORDER BY created_at DESC LIMIT 1`, planID))
	}
	expires, err := time.Parse(time.RFC3339Nano, review.ExpiresAt)
	if err != nil || time.Now().After(expires) {
		return Job{}, bad("PREVIEW_EXPIRED", "纠错预览已过期，请重新预览")
	}
	if review.TargetChanged && !confirmTargetChange {
		return Job{}, bad("CONFIRM_TARGET_CHANGE", "更改作品、季集或目标库会转移占位，并暂停关联原订阅，请明确确认")
	}
	current, err := a.downloadFiles(ctx, key)
	if err != nil {
		return Job{}, err
	}
	if current.DownloaderSnapshot != review.DownloaderSnapshot {
		return Job{}, bad("REVIEW_DOWNLOADER_CHANGED", "下载器配置在纠错预览后又有变化，请重新预览")
	}
	in := current.Intent
	if in.State == "cancelled" || in.State == "imported" || current.Task.Progress < 1 || downloadReviewFingerprint(in) != review.Fingerprint {
		return Job{}, bad("REVIEW_INTENT_CHANGED", "下载目标、原计划或完成状态已变化，请重新预览")
	}
	var plan ImportPlan
	if err = a.Store.Get(ctx, "import_plan", planID, &plan); err != nil {
		return Job{}, err
	}
	selected, err := selectImportFiles(plan, nil)
	if err != nil {
		return Job{}, err
	}
	if len(selected) != len(plan.Files) {
		return Job{}, bad("REVIEW_IMPORT_BLOCKED", "此预览有未解决文件，请修正后重新预览")
	}
	root, err := a.importRoot(plan.RootID)
	if err != nil {
		return Job{}, err
	}
	var meta importMetadata
	if err = a.Store.Get(ctx, "import_meta", planID, &meta); err != nil {
		return Job{}, err
	}
	if root.Path != meta.RootPath {
		return Job{}, bad("REVIEW_ROOT_CHANGED", "媒体库路径已改变，请重新预览")
	}
	available := map[string]bool{}
	for _, file := range current.Files {
		if file.Progress >= 1 && file.Video && file.ApplicationPath != "" {
			available[file.ApplicationPath] = true
		}
	}
	for _, file := range plan.Files {
		if !available[file.Source] {
			return Job{}, bad("REVIEW_SOURCE_CHANGED", "原下载器完成文件或路径已变化")
		}
		actual, e := snapshotPath(file.Source)
		if e != nil || actual != planSnapshot(file) {
			return Job{}, bad("REVIEW_SOURCE_CHANGED", "源文件在预览后变化，请重新预览")
		}
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='intent' AND id=?`, key).Scan(&raw); err != nil {
		return Job{}, err
	}
	if json.Unmarshal(raw, &in) != nil || in.State == "cancelled" || in.State == "imported" || downloadReviewFingerprint(in) != review.Fingerprint {
		return Job{}, bad("REVIEW_INTENT_CHANGED", "关联下载已变化，请重新预览")
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind='import' AND state IN ('queued','running','retry_wait','waiting_paused') AND json_extract(payload,'$.id')=?`, in.ImportPlanID).Scan(&active); err != nil {
		return Job{}, err
	}
	if active > 0 {
		return Job{}, bad("REVIEW_IMPORT_BUSY", "原整理仍在执行或队列中，请先取消后重新预览")
	}
	if review.TargetChanged {
		var assets int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='asset' AND json_extract(payload,'$.intent_id')=?`, key).Scan(&assets); err != nil {
			return Job{}, err
		}
		if assets > 0 {
			return Job{}, bad("REVIEW_PARTIAL_IDENTITY", "已有入库资产，不能转移身份或目标")
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM reservations WHERE intent_id=?`, key); err != nil {
			return Job{}, err
		}
		if in.SubscriptionID != "" {
			var sub Subscription
			if err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='subscription' AND id=?`, in.SubscriptionID).Scan(&raw); err != nil {
				return Job{}, err
			}
			if json.Unmarshal(raw, &sub) != nil {
				return Job{}, bad("REVIEW_SUBSCRIPTION", "原订阅记录无效")
			}
			if sub.State != "cancelled" {
				sub.State = "paused"
				sub.Revision++
				sub.NextCheck = ""
				sub.LastReason = "关联下载经人工确认修改目标，订阅已暂停，请核对后再恢复"
				if err = txPut(ctx, tx, "subscription", sub.ID, sub); err != nil {
					return Job{}, err
				}
			}
			if _, err = tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,lease_until=0,updated_at=? WHERE kind='subscription' AND json_extract(payload,'$.id')=? AND state IN ('queued','running','retry_wait','waiting_paused')`, now(), sub.ID); err != nil {
				return Job{}, err
			}
		}
		in.Media = plan.Media
		in.Season = review.Season
		in.Episodes = append([]int{}, review.Episodes...)
		in.RootID = plan.RootID
		in.SubscriptionID = ""
		in.SubscriptionRevision = 0
		in.Request.MediaType = in.Media.MediaType
		in.Request.TMDBID = in.Media.ID
		in.Request.Season = in.Season
		in.Request.Episodes = append([]int{}, in.Episodes...)
		in.Request.AllowEarly = nil
		in.Request.EpisodeDates = nil
	}
	if in.ImportPlanID != "" && in.ImportPlanID != plan.ID {
		var old ImportPlan
		if err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='import_plan' AND id=?`, in.ImportPlanID).Scan(&raw); err == nil {
			if json.Unmarshal(raw, &old) != nil {
				return Job{}, bad("REVIEW_IMPORT_PLAN", "原整理记录无效")
			}
			old.State = "superseded"
			if err = txPut(ctx, tx, "import_plan", old.ID, old); err != nil {
				return Job{}, err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return Job{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,lease_until=0,updated_at=? WHERE kind='import' AND json_extract(payload,'$.id')=? AND state IN ('queued','running','retry_wait','waiting_paused')`, now(), in.ImportPlanID); err != nil {
			return Job{}, err
		}
	}
	for _, index := range selected {
		file := plan.Files[index]
		if err = importExcludedTx(ctx, tx, plan, file); err != nil {
			return Job{}, err
		}
		if err = importReserve(ctx, tx, plan, file); err != nil {
			return Job{}, err
		}
	}
	in.ImportPlanID = plan.ID
	in.State = "downloaded"
	in.Auto = false
	in.Error = ""
	in.UpdatedAt = now()
	in.ImportMode = plan.Mode
	in.DownloaderSnapshot = review.DownloaderSnapshot
	in.Request.Mappings = review.Mappings
	plan.State = "queued"
	review.Confirmed = true
	review.Plan = plan
	if err = txPut(ctx, tx, "intent", key, in); err != nil {
		return Job{}, err
	}
	if err = txPut(ctx, tx, "import_plan", plan.ID, plan); err != nil {
		return Job{}, err
	}
	if err = txPut(ctx, tx, "download_review", plan.ID, review); err != nil {
		return Job{}, err
	}
	job, err := enqueueTx(ctx, tx, "import", map[string]any{"id": plan.ID}, in.WorkflowID)
	if err != nil {
		return Job{}, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, err
	}
	a.log("INFO", "imports", "download_review_confirmed", "已确认关联下载的人工纠错；源文件及下载器任务保留", in.WorkflowID, job.ID)
	select {
	case a.jobWake <- struct{}{}:
	default:
	}
	return job, nil
}
