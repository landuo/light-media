package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"light-media/internal/catalog"
	"light-media/internal/download"
	"light-media/internal/media"
)

// Directory monitoring uses persistent observations, not a file-create event as
// proof that a download finished. The first pass is a baseline unless opted in.
const directoryStableTime = 60 * time.Second

type DirectoryObservedFile struct {
	ID                  string         `json:"id"`
	DirectoryID         string         `json:"directory_id"`
	Path                string         `json:"path"`
	State               string         `json:"state"`
	Reason              string         `json:"reason"`
	PlanID              string         `json:"plan_id,omitempty"`
	LastSeen            string         `json:"last_seen"`
	StableSince         int64          `json:"stable_since"`
	Snapshot            importSnapshot `json:"snapshot"`
	RuleFingerprint     string         `json:"rule_fingerprint"`
	EvidenceFingerprint string         `json:"evidence_fingerprint"`
}
type directoryBaseline struct {
	Fingerprint string `json:"fingerprint"`
	LastScan    string `json:"last_scan"`
}
type directoryImportGuard struct {
	Rule               DirectoryRule `json:"rule"`
	SourcePath         string        `json:"source_path"`
	DownloaderSnapshot string        `json:"downloader_snapshot,omitempty"`
}
type directoryDownloadFile struct {
	Complete bool
	Managed  bool
	Imported bool
	Size     int64
}

func directoryFileID(ruleID, path string) string {
	s := sha256.Sum256([]byte(ruleID + "\x00" + path))
	return hex.EncodeToString(s[:])
}
func sameDirectorySnapshot(a, b importSnapshot) bool { return a == b }

func (a *App) listDirectoryFiles(ctx context.Context, ruleID string) ([]DirectoryObservedFile, error) {
	rows, e := a.Store.DB.QueryContext(ctx, `SELECT payload FROM records WHERE kind='directory_file' AND json_extract(payload,'$.directory_id')=? ORDER BY json_extract(payload,'$.last_seen') DESC LIMIT 500`, ruleID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []DirectoryObservedFile{}
	for rows.Next() {
		var b []byte
		var f DirectoryObservedFile
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &f); e != nil {
			return nil, e
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
func (a *App) pollDirectoryFiles(ctx context.Context) error {
	var first error
	for _, r := range a.Settings().Directories {
		if r.Enabled && r.MonitorType == "monitor" {
			if e := a.pollDirectoryFileRule(ctx, r); e != nil {
				a.log("WARN", "directories", "monitor_failed", a.clean(e.Error()), "", "")
				if first == nil {
					first = e
				}
			}
		}
	}
	return first
}
func (a *App) pollDirectoryFileRule(ctx context.Context, rule DirectoryRule) (result error) {
	a.directoryFileMu.Lock()
	defer a.directoryFileMu.Unlock()
	defer func() {
		message := ""
		if result != nil {
			message = a.clean(result.Error())
		}
		statusCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		status := "ok"
		if result != nil {
			status = "error"
		}
		_ = a.Store.Put(statusCtx, "directory_status", rule.ID, managedConnection{Status: status, CheckedAt: now(), Error: message})
	}()
	if a.paused() {
		return bad("AUTOMATION_PAUSED", "自动化已暂停")
	}
	current, e := a.enabledDirectory(rule.ID)
	if e != nil {
		return e
	}
	if current.MonitorType != "monitor" {
		return bad("DIRECTORY_MONITOR_TYPE", "该规则不是目录监听")
	}
	rule = current
	source, e := a.directorySource(rule)
	if e != nil {
		return e
	}
	raw, e := a.scanFiles(ctx, source, true)
	if e != nil {
		return e
	}
	scan := raw.(map[string]any)
	files := scan["files"].([]importScanFile)
	if truncated, _ := scan["truncated"].(bool); truncated {
		return bad("DIRECTORY_SCAN_LIMIT", "监听目录超过2000个视频或扫描上限，请拆分目录；本轮未执行整理")
	}
	downloads, e := a.directoryDownloadFiles(ctx, rule, source)
	if e != nil {
		return e
	}
	fingerprint := directoryFingerprint(rule)
	guard := directoryImportGuard{Rule: rule, SourcePath: source}
	if rule.DownloaderID != "" {
		d, err := a.downloader(rule.DownloaderID)
		if err != nil {
			return err
		}
		guard.DownloaderSnapshot = downloaderSnapshot(d)
	}
	var baseline directoryBaseline
	baselineErr := a.Store.Get(ctx, "directory_file_state", rule.ID, &baseline)
	if baselineErr != nil && !errors.Is(baselineErr, sql.ErrNoRows) {
		return baselineErr
	}
	initial := baseline.Fingerprint != fingerprint
	for _, file := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if a.paused() {
			return bad("AUTOMATION_PAUSED", "自动化已暂停")
		}
		stamp, e := snapshotPath(file.Path)
		if e != nil {
			return e
		}
		key := directoryFileID(rule.ID, file.Path)
		var f DirectoryObservedFile
		e = a.Store.Get(ctx, "directory_file", key, &f)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		fresh := e != nil || f.RuleFingerprint != fingerprint
		changed := !fresh && !sameDirectorySnapshot(f.Snapshot, stamp)
		evidenceJSON, _ := json.Marshal(struct {
			Media     *importInferredMedia
			Numbering *media.Numbering
			Reason    string
		}{file.InferredMedia, file.Numbering, file.IdentityReason})
		evidenceSum := sha256.Sum256(evidenceJSON)
		evidence := hex.EncodeToString(evidenceSum[:])
		retryIdentity := !fresh && !changed && f.State == "needs_review" && f.PlanID == "" && f.EvidenceFingerprint != evidence
		if fresh || changed || retryIdentity {
			f = DirectoryObservedFile{ID: key, DirectoryID: rule.ID, Path: file.Path, State: "observing", Reason: "等待文件至少60秒保持稳定", Snapshot: stamp, StableSince: time.Now().UnixNano(), RuleFingerprint: fingerprint, EvidenceFingerprint: evidence}
			if fresh && initial && !rule.IncludeExisting {
				f.State = "baseline"
				f.Reason = "启用监听时已存在，已跳过；需要时从文件管理手动整理"
			}
		}
		f.LastSeen = now()
		if f.PlanID != "" {
			a.refreshDirectoryFilePlan(ctx, &f)
		}
		terminal := f.State == "baseline" || f.State == "imported" || f.State == "queued" || f.State == "needs_review" || f.State == "error"
		if !terminal && !fresh && !changed && time.Since(time.Unix(0, f.StableSince)) >= directoryStableTime {
			if owner, ok := downloads[file.Path]; ok && owner.Managed {
				f.State = "tracking"
				f.Reason = "本应用下载任务，由下载完成核验流程整理"
				if owner.Imported {
					f.State = "imported"
					f.Reason = "下载任务已整理完成，源文件保留"
				}
			} else if ok && (!owner.Complete || owner.Size != stamp.Size) {
				f.State = "downloading"
				f.Reason = "下载器仍在写入或尚未确认文件完成"
				f.StableSince = time.Now().UnixNano()
			} else if stamp.Size <= 0 {
				f.State = "observing"
				f.Reason = "空文件不参与整理"
			} else {
				request, identityErr := a.directoryFileIdentity(ctx, rule, file)
				if identityErr != nil {
					f.State = "needs_review"
					f.Reason = a.clean(identityErr.Error())
				} else {
					// Persist the preview before enqueueing so a crash cannot duplicate work.
					plan, previewErr := a.previewImport(ctx, request)
					if previewErr != nil {
						f.State = "needs_review"
						f.Reason = a.clean(previewErr.Error())
					} else {
						f.PlanID = plan.ID
						f.State = "queued"
						f.Reason = "目录规则已生成整理预览，正在排队"
						if e = a.Store.Put(ctx, "directory_import", plan.ID, guard); e != nil {
							return e
						}
						if e = a.Store.Put(ctx, "directory_file", f.ID, f); e != nil {
							return e
						}
						if _, e = a.executeImport(ctx, plan.ID, nil); e != nil {
							f.State = "error"
							f.Reason = a.clean(e.Error())
						}
					}
				}
			}
		}
		if e = a.Store.Put(ctx, "directory_file", f.ID, f); e != nil {
			return e
		}
	}
	return a.Store.Put(ctx, "directory_file_state", rule.ID, directoryBaseline{Fingerprint: fingerprint, LastScan: now()})
}
func (a *App) refreshDirectoryFilePlan(ctx context.Context, f *DirectoryObservedFile) {
	var p ImportPlan
	if e := a.Store.Get(ctx, "import_plan", f.PlanID, &p); e != nil {
		f.State = "error"
		f.Reason = "整理计划无法读取，请查看任务日志"
		return
	}
	done := len(p.Files) > 0
	for _, v := range p.Files {
		if v.Status != "imported" && v.Status != "already_imported" {
			done = false
		}
	}
	if done {
		f.State = "imported"
		f.Reason = "整理完成，源文件保留"
		return
	}
	var payload []byte
	e := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM jobs WHERE kind='import' AND json_extract(payload,'$.id')=? ORDER BY created_at DESC LIMIT 1`, p.ID).Scan(&payload)
	if errors.Is(e, sql.ErrNoRows) && f.State == "queued" {
		if _, e = a.executeImport(ctx, p.ID, nil); e != nil {
			f.State = "error"
			f.Reason = a.clean(e.Error())
		}
		return
	}
	var state, reason string
	if a.Store.DB.QueryRowContext(ctx, `SELECT state,error_message FROM jobs WHERE kind='import' AND json_extract(payload,'$.id')=? ORDER BY created_at DESC LIMIT 1`, p.ID).Scan(&state, &reason) == nil && (state == "failed" || state == "cancelled") {
		f.State = "error"
		f.Reason = a.clean(reason)
	}
}
func (a *App) guardDirectoryFilePlan(ctx context.Context, planID string) error {
	var g directoryImportGuard
	e := a.Store.Get(ctx, "directory_import", planID, &g)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	rule, e := a.enabledDirectory(g.Rule.ID)
	if e != nil {
		return bad("DIRECTORY_RULE_CHANGED", "目录规则已停用或删除，停止自动整理")
	}
	if directoryFingerprint(rule) != directoryFingerprint(g.Rule) {
		return bad("DIRECTORY_RULE_CHANGED", "目录规则已修改，停止旧整理计划")
	}
	source, err := a.directorySource(rule)
	if err != nil || source != g.SourcePath {
		return bad("DIRECTORY_MAPPING_CHANGED", "监听目录路径映射已改变，停止旧整理计划")
	}
	if rule.DownloaderID != "" {
		d, err := a.downloader(rule.DownloaderID)
		if err != nil || downloaderSnapshot(d) != g.DownloaderSnapshot {
			return bad("DIRECTORY_MAPPING_CHANGED", "下载器配置已改变，停止旧整理计划")
		}
	}
	return nil
}

func (a *App) directoryDownloadFiles(ctx context.Context, rule DirectoryRule, source string) (map[string]directoryDownloadFile, error) {
	out := map[string]directoryDownloadFile{}
	configs := []DownloaderConfig{}
	if rule.DownloaderID != "" {
		d, e := a.downloader(rule.DownloaderID)
		if e != nil {
			return nil, e
		}
		configs = append(configs, d)
	} else {
		for _, d := range a.Settings().Downloaders {
			relevant := len(d.Mappings) == 0 && (within(source, d.DownloadPath) || within(d.DownloadPath, source))
			for _, m := range d.Mappings {
				if within(source, m.To) || within(m.To, source) {
					relevant = true
				}
			}
			if relevant {
				configs = append(configs, d)
			}
		}
	}
	for _, d := range configs {
		files, err := a.directoryFilesForDownloader(ctx, d, source)
		if err != nil {
			return nil, err
		}
		for path, entry := range files {
			if old, ok := out[path]; ok {
				entry.Complete = entry.Complete && old.Complete && entry.Size == old.Size
				entry.Managed = entry.Managed || old.Managed
				entry.Imported = entry.Imported || old.Imported
			}
			out[path] = entry
		}
	}
	return out, nil
}
func (a *App) directoryFilesForDownloader(ctx context.Context, d DownloaderConfig, source string) (map[string]directoryDownloadFile, error) {
	out := map[string]directoryDownloadFile{}
	client, e := download.New(d.Config)
	if e != nil {
		return nil, e
	}
	tasks, e := client.List(ctx)
	if e != nil {
		return nil, bad("DIRECTORY_DOWNLOADER_UNAVAILABLE", "无法核对下载器文件是否完成，本轮不执行目录整理")
	}
	checked := 0
	for _, t := range tasks {
		mapped, e := mapDownloaderPath(a.Settings(), d, t.DownloadDir)
		if e != nil {
			return nil, bad("DIRECTORY_MAPPING_UNVERIFIED", "下载器路径映射未确认，本轮不执行目录整理")
		}
		if !within(source, mapped) && !within(mapped, source) {
			continue
		}
		checked++
		if checked > 200 {
			return nil, bad("DIRECTORY_TASK_LIMIT", "该目录关联下载任务超过200个，请缩小监听目录")
		}
		files, e := client.Files(ctx, t.Hash)
		if e != nil {
			return nil, e
		}
		var managed, imported int
		e = a.Store.DB.QueryRowContext(ctx, `SELECT count(*),coalesce(max(json_extract(payload,'$.state')='imported'),0) FROM records WHERE kind='intent' AND json_extract(payload,'$.downloader_id')=? AND lower(json_extract(payload,'$.hash'))=lower(?) AND json_extract(payload,'$.state')!='cancelled'`, d.ID, t.Hash).Scan(&managed, &imported)
		if e != nil {
			return nil, e
		}
		for _, f := range files {
			if !filepath.IsLocal(filepath.FromSlash(f.Path)) {
				return nil, bad("DIRECTORY_UNSAFE_DOWNLOADER_PATH", "下载器返回异常文件路径")
			}
			path, e := mapDownloaderPath(a.Settings(), d, filepath.Join(t.DownloadDir, filepath.FromSlash(f.Path)))
			if e != nil {
				return nil, e
			}
			if !within(source, path) {
				continue
			}
			taskReady := directoryTaskComplete(t)
			entry := directoryDownloadFile{Complete: taskReady && f.Size > 0 && f.Completed >= f.Size && f.Progress >= 1, Managed: managed > 0, Imported: imported > 0, Size: f.Size}
			if old, ok := out[path]; ok {
				entry.Complete = entry.Complete && old.Complete && entry.Size == old.Size
				entry.Managed = entry.Managed || old.Managed
				entry.Imported = entry.Imported || old.Imported
			}
			out[path] = entry
		}
	}
	return out, nil
}
func directoryTitle(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}
func (a *App) directoryFileIdentity(ctx context.Context, rule DirectoryRule, file importScanFile) (ImportRequest, error) {
	request := ImportRequest{RootID: rule.RootID, Mode: rule.Mode, WorkflowID: id("wf_"), Files: []ImportFileRequest{{Path: file.Path}}}
	var numbering *media.Numbering
	if file.IdentityReason != "" {
		return request, bad("DIRECTORY_IDENTITY_REVIEW", "NFO身份存在疑问，请在文件管理中手动识别")
	}
	if hint := file.InferredMedia; hint != nil {
		request.MediaType = hint.Type
		request.TMDBID = hint.ID
		numbering = file.Numbering
	} else {
		p := file.Parsed
		if p.Status != media.Parsed || p.Title == "" {
			return request, bad("DIRECTORY_IDENTITY_REVIEW", "文件名缺少明确影视身份，请在文件管理中手动识别")
		}
		kind := "movie"
		if p.Type == "episode" {
			kind = "tv"
		}
		if kind == "movie" && p.Year == 0 {
			return request, bad("DIRECTORY_IDENTITY_REVIEW", "电影文件名缺少年份，请手动识别")
		}
		var page catalog.Page
		_, e := a.cached(ctx, "directory-identify:"+kind+":"+p.Title, 24*time.Hour, &page, func() (any, error) { return a.catalog().Search(ctx, p.Title, kind, 1) })
		if e != nil {
			return request, e
		}
		matches := []catalog.Media{}
		for _, m := range page.Results {
			if m.MediaType != kind || p.Year > 0 && p.Year != m.Year {
				continue
			}
			if directoryTitle(m.Title) == directoryTitle(p.Title) || directoryTitle(m.OriginalTitle) == directoryTitle(p.Title) {
				matches = append(matches, m)
			}
		}
		if len(matches) != 1 || page.TotalPages > 1 {
			return request, bad("DIRECTORY_IDENTITY_REVIEW", "影视身份不唯一或不完整，请在文件管理中手动识别")
		}
		request.MediaType = kind
		request.TMDBID = matches[0].ID
		numbering = p.Original
	}
	if rule.MediaType != "" && rule.MediaType != "mixed" && rule.MediaType != request.MediaType {
		return request, bad("DIRECTORY_MEDIA_TYPE", "影视类型与目录规则不符")
	}
	if request.MediaType == "tv" {
		if numbering == nil || numbering.Season == nil || *numbering.Season < 1 || len(numbering.Episodes) != 1 {
			return request, bad("DIRECTORY_NUMBERING_REVIEW", "剧集需要明确季号和单集编号，请手动识别")
		}
		request.Files[0].Season = *numbering.Season
		request.Files[0].Episodes = numbering.Episodes
	}
	if !validMedia(request.MediaType, request.TMDBID) {
		return request, fmt.Errorf("目录文件缺少有效影视身份")
	}
	return request, nil
}
