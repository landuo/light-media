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
	"regexp"
	"strings"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/download"
	"light-media/internal/media"
)

// Directory monitoring uses persistent observations, not a file-create event as
// proof that a download finished. The first pass is a baseline unless opted in.
const directoryStableTime = 60 * time.Second
const directoryInspectionPending = "inspect_pending"

type DirectoryObservedFile struct {
	JobID               string         `json:"job_id,omitempty"`
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
	Complete         bool
	Managed          bool
	Imported         bool
	Size             int64
	Hint             *importInferredMedia
	IdentityConflict bool
}

func mergeDirectoryDownloadFile(entry, old directoryDownloadFile) directoryDownloadFile {
	entry.Complete = entry.Complete && old.Complete && entry.Size == old.Size
	entry.Managed = entry.Managed || old.Managed
	entry.Imported = entry.Imported || old.Imported
	entry.IdentityConflict = entry.IdentityConflict || old.IdentityConflict
	if entry.Hint != nil && old.Hint != nil && (entry.Hint.Type != old.Hint.Type || entry.Hint.ID != old.Hint.ID) {
		entry.IdentityConflict = true
	}
	if entry.Hint == nil {
		entry.Hint = old.Hint
	}
	if entry.IdentityConflict {
		entry.Hint = nil
	}
	return entry
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
		if r.Enabled && (r.MonitorType == "monitor" || r.MonitorType == "downloader") {
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
	if current.MonitorType != "monitor" && current.MonitorType != "downloader" {
		return bad("DIRECTORY_MONITOR_TYPE", "该规则不是目录监听")
	}
	rule = current
	source, e := a.directorySource(rule)
	if e != nil {
		return e
	}
	fingerprint := directoryFingerprint(rule)
	var inspectionError error
	inspectFailed := func(path string, cause error) error {
		if inspectionError == nil {
			inspectionError = fmt.Errorf("未能核对源文件 %s: %w", filepath.Base(path), cause)
		}
		return a.recordDirectoryInspectionFailure(ctx, rule, fingerprint, path, cause)
	}
	raw, e := a.scanImportFiles(ctx, source, true, inspectFailed)
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
		if owner, ok := downloads[file.Path]; ok {
			if owner.IdentityConflict {
				file.IdentityReason = "下载任务的影视身份存在冲突"
			} else if owner.Hint != nil {
				if file.InferredMedia != nil && (file.InferredMedia.ID != owner.Hint.ID || file.InferredMedia.Type != owner.Hint.Type) {
					file.IdentityReason = "下载任务与NFO影视身份存在冲突"
				} else if file.IdentityReason == "" {
					file.InferredMedia = owner.Hint
					if file.Numbering == nil {
						file.Numbering = file.Parsed.Original
					}
				}
			}
		}
		if !a.directoryRuleOwnsFile(rule, file) {
			key := directoryFileID(rule.ID, file.Path)
			var existing DirectoryObservedFile
			if err := a.Store.Get(ctx, "directory_file", key, &existing); errors.Is(err, sql.ErrNoRows) || err == nil && existing.State == directoryInspectionPending {
				stamp, snapshotErr := snapshotPath(file.Path)
				if snapshotErr != nil {
					if err = inspectFailed(file.Path, snapshotErr); err != nil {
						return err
					}
					continue
				}
				baseline := DirectoryObservedFile{ID: key, DirectoryID: rule.ID, Path: file.Path, State: "baseline", Reason: "由列表中更靠前的目录规则处理", Snapshot: stamp, StableSince: time.Now().UnixNano(), RuleFingerprint: fingerprint, LastSeen: now()}
				if err = a.Store.Put(ctx, "directory_file", key, baseline); err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if a.paused() {
			return bad("AUTOMATION_PAUSED", "自动化已暂停")
		}
		stamp, e := snapshotPath(file.Path)
		if e != nil {
			if e = inspectFailed(file.Path, e); e != nil {
				return e
			}
			continue
		}
		key := directoryFileID(rule.ID, file.Path)
		var f DirectoryObservedFile
		e = a.Store.Get(ctx, "directory_file", key, &f)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		fresh := e != nil || f.RuleFingerprint != fingerprint || f.State == directoryInspectionPending
		changed := !fresh && !sameDirectorySnapshot(f.Snapshot, stamp)

		evidenceJSON, _ := json.Marshal(struct {
			Media     *importInferredMedia
			Numbering *media.Numbering
			Reason    string
		}{file.InferredMedia, file.Numbering, file.IdentityReason})
		evidenceSum := sha256.Sum256(evidenceJSON)
		evidence := hex.EncodeToString(evidenceSum[:])
		retryIdentity := !fresh && !changed && (f.State == "needs_review" || f.State == "error") && f.EvidenceFingerprint != evidence
		if fresh || changed || retryIdentity {
			f = DirectoryObservedFile{ID: key, DirectoryID: rule.ID, Path: file.Path, State: "observing", Reason: "等待文件至少60秒保持稳定", Snapshot: stamp, StableSince: time.Now().UnixNano(), RuleFingerprint: fingerprint, EvidenceFingerprint: evidence}
			if fresh && initial && !rule.IncludeExisting {
				f.State = "baseline"
				f.Reason = "启用监听时已存在，已跳过；需要时从文件管理手动整理"
			}
		}
		f.LastSeen = now()
		if f.JobID != "" && f.PlanID == "" {
			var planID string
			if a.Store.DB.QueryRowContext(ctx, `SELECT json_extract(payload,'$.id') FROM jobs WHERE id=? AND kind='import'`, f.JobID).Scan(&planID) == nil {
				f.PlanID = planID
			}
		}
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
			} else if !ok && rule.MonitorType == "downloader" {
				f.State = "observing"
				f.Reason = "等待下载器确认此目录中的文件已完成"
			} else if stamp.Size <= 0 {
				f.State = "observing"
				f.Reason = "空文件不参与整理"
			} else {
				if e = a.enqueueDirectoryOrganizeFile(ctx, guard, file, &f); e != nil {
					return e
				}
			}
		}
		if e = a.Store.Put(ctx, "directory_file", f.ID, f); e != nil {
			return e
		}
	}
	if inspectionError != nil {
		return inspectionError
	}
	return a.Store.Put(ctx, "directory_file_state", rule.ID, directoryBaseline{Fingerprint: fingerprint, LastScan: now()})
}

func (a *App) recordDirectoryInspectionFailure(ctx context.Context, rule DirectoryRule, fingerprint, path string, cause error) error {
	key := directoryFileID(rule.ID, path)
	var file DirectoryObservedFile
	if err := a.Store.Get(ctx, "directory_file", key, &file); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if file.RuleFingerprint == fingerprint && (file.PlanID != "" || file.State == "baseline") {
		return nil
	}
	file = DirectoryObservedFile{
		ID: key, DirectoryID: rule.ID, Path: path, State: directoryInspectionPending,
		Reason:          "暂时无法核对源文件，下轮重新检查：" + a.clean(cause.Error()),
		RuleFingerprint: fingerprint, LastSeen: now(),
	}
	return a.Store.Put(ctx, "directory_file", key, file)
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
	var plan ImportPlan
	if err := a.Store.Get(ctx, "import_plan", planID, &plan); err != nil {
		return err
	}
	for _, file := range plan.Files {
		if !a.directoryRuleOwnsFile(rule, importScanFile{Path: file.Source, InferredMedia: &importInferredMedia{Type: plan.Media.MediaType}}) {
			return bad("DIRECTORY_RULE_CHANGED", "目录规则优先级已改变，停止旧整理计划")
		}
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
			if !downloaderEnabled(d) {
				rows, err := a.Store.DB.QueryContext(ctx, `SELECT json_extract(payload,'$.download_path') FROM records WHERE kind IN ('intent','native_download') AND json_extract(payload,'$.downloader_id')=? AND json_extract(payload,'$.state') NOT IN ('cancelled','imported')`, d.ID)
				if err != nil {
					return nil, err
				}
				blocked := false
				for rows.Next() {
					var path sql.NullString
					if err = rows.Scan(&path); err != nil {
						rows.Close()
						return nil, err
					}
					if path.String == "" {
						path.String = d.DownloadPath
					}
					mapped, mapErr := mapDownloaderPath(a.Settings(), d, path.String)
					if mapErr == nil && (within(source, mapped) || within(mapped, source)) {
						blocked = true
					}
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return nil, err
				}
				if blocked {
					return nil, bad("DOWNLOADER_DISABLED", "此目录有停用下载器的在途任务，启用后继续核对与整理")
				}
				continue
			}
			relevant := d.SharedPaths || d.DownloadPath == "" || len(d.Mappings) == 0 && (within(source, d.DownloadPath) || within(d.DownloadPath, source))
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
				entry = mergeDirectoryDownloadFile(entry, old)
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
		sharedPath := ""
		if d.SharedPaths {
			// Shared container paths are already the file identity. Unrelated
			// daemon tasks do not require authorization for this source scan.
			path := cleanDirectoryInput(t.DownloadDir)
			if err := safeRoot(path); err != nil {
				if within(source, path) || within(path, source) {
					return nil, bad("DIRECTORY_PATH_UNVERIFIED", "相关下载任务返回不规范保存目录，本轮未整理")
				}
				continue
			}
			var relevant bool
			sharedPath, relevant = sharedDirectoryPath(source, path)
			if !relevant && len(d.Mappings) > 0 {
				// Frozen legacy instances can still report a historical daemon
				// path before their container mounts have been aligned.
				if legacy, err := mapPath(path, d.Mappings); err == nil && (within(source, legacy) || within(legacy, source)) {
					sharedPath, relevant = legacy, true
				}
			}
			if !relevant {
				continue
			}
			t.DownloadDir = path
		}
		pathToCheck := t.DownloadDir
		if sharedPath != "" {
			pathToCheck = sharedPath
		}
		mapped, e := mapDownloaderPath(a.Settings(), d, pathToCheck)
		if e != nil {
			if within(source, t.DownloadDir) || within(t.DownloadDir, source) {
				return nil, bad("DIRECTORY_PATH_UNVERIFIED", "下载器路径不在应用容器可见目录内，本轮未整理")
			}
			continue
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
		var native NativeDownload
		var payload []byte
		nativeErr := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='native_download' AND json_extract(payload,'$.downloader_id')=? AND lower(json_extract(payload,'$.hash'))=lower(?) AND json_extract(payload,'$.state')!='cancelled' ORDER BY updated_at DESC LIMIT 1`, d.ID, t.Hash).Scan(&payload)
		if nativeErr != nil && !errors.Is(nativeErr, sql.ErrNoRows) {
			return nil, nativeErr
		}
		if nativeErr == nil && json.Unmarshal(payload, &native) != nil {
			return nil, bad("DOWNLOAD_RECORD_INVALID", "下载记录无法读取")
		}
		for _, f := range files {
			if !filepath.IsLocal(filepath.FromSlash(f.Path)) {
				return nil, bad("DIRECTORY_UNSAFE_DOWNLOADER_PATH", "下载器返回异常文件路径")
			}
			fileRoot := t.DownloadDir
			if sharedPath != "" {
				fileRoot = sharedPath
			}
			candidate := filepath.Join(fileRoot, filepath.FromSlash(f.Path))
			if d.SharedPaths && !within(source, candidate) {
				continue
			}
			path, e := mapDownloaderPath(a.Settings(), d, candidate)
			if e != nil {
				return nil, e
			}
			if !within(source, path) {
				continue
			}
			taskReady := directoryTaskComplete(t)
			entry := directoryDownloadFile{Complete: taskReady && f.Size > 0 && f.Completed >= f.Size && f.Progress >= 1, Managed: managed > 0, Imported: imported > 0, Size: f.Size}
			if validMedia(native.MediaType, native.TMDBID) {
				entry.Hint = &importInferredMedia{Type: native.MediaType, MediaType: native.MediaType, ID: native.TMDBID}
			}
			if old, ok := out[path]; ok {
				entry = mergeDirectoryDownloadFile(entry, old)
			}
			out[path] = entry
		}
	}
	return out, nil
}
func directoryTitle(s string) string {
	return media.NormalizeTitle(s)
}

var directoryTVReleaseYear = regexp.MustCompile(`^(.+) ((?:19|20)[0-9]{2})$`)

func (a *App) directoryTitleMatches(ctx context.Context, kind string, year int, names []string) (map[int64]catalog.Media, error) {
	matches := map[int64]catalog.Media{}
	titles := map[string]bool{}
	queries := []string{}
	for _, title := range names {
		if normalized := directoryTitle(title); normalized != "" && !titles[normalized] {
			titles[normalized] = true
			queries = append(queries, title)
		}
	}
	for _, query := range queries {
		totalPages, totalResults := 1, 0
		for number := 1; number <= totalPages; number++ {
			key := "directory-identify:" + kind + ":" + query
			if number > 1 {
				key += fmt.Sprintf(":page:%d", number)
			}
			var page catalog.Page
			at, err := a.cached(ctx, key, 24*time.Hour, &page, func() (any, error) { return a.catalog().Search(ctx, query, kind, number) })
			if err != nil {
				return nil, err
			}
			// Stale search fallback is useful for browsing, but cannot prove
			// uniqueness when recognition requires the complete result set.
			if time.Since(at) >= 24*time.Hour || page.Page != number || page.TotalPages < 1 || page.TotalPages > 5 || number > 1 && (page.TotalPages != totalPages || page.TotalResults != totalResults) || page.TotalPages > 1 && len(page.Results) == 0 {
				return nil, bad("DIRECTORY_IDENTITY_REVIEW", "影视搜索分页不完整或结果过多，请在“手动整理”中选择电影或电视剧类型，并查询或填写TheMovieDb编号")
			}
			if number == 1 {
				totalPages, totalResults = page.TotalPages, page.TotalResults
			}
			for _, m := range page.Results {
				if m.MediaType != kind || year > 0 && year != m.Year {
					continue
				}
				if titles[directoryTitle(m.Title)] || titles[directoryTitle(m.OriginalTitle)] {
					matches[m.ID] = m
				}
			}
		}
	}
	return matches, nil
}

func (a *App) directoryTVReleaseMatches(ctx context.Context, p media.ParseResult) (map[int64]catalog.Media, error) {
	if p.Original == nil || p.Original.Form != "season_episode" || p.Original.Season == nil {
		return nil, nil
	}
	release := directoryTVReleaseYear.FindStringSubmatch(p.Title)
	if release == nil {
		return nil, nil
	}
	// A year preceding SxxExx can describe that season. Keep literal-title
	// recognition first and search the stripped title without a premiere year.
	matches, err := a.directoryTitleMatches(ctx, "tv", 0, []string{release[1]})
	if err != nil || len(matches) <= 1 {
		return matches, err
	}
	// As in MoviePilot, check the requested season's year when the title alone
	// is inconclusive. Require a single candidate before using the identity.
	seasonMatches := map[int64]catalog.Media{}
	for id, candidate := range matches {
		detail, err := a.media(ctx, "tv", id)
		if err != nil {
			return nil, err
		}
		for _, season := range detail.Seasons {
			if season.SeasonNumber == *p.Original.Season && strings.HasPrefix(season.AirDate, release[2]+"-") {
				seasonMatches[id] = candidate
				break
			}
		}
	}
	return seasonMatches, nil
}

func (a *App) directoryFileIdentity(ctx context.Context, rule DirectoryRule, file importScanFile) (ImportRequest, error) {
	request := ImportRequest{RootID: rule.RootID, Mode: rule.Mode, Renaming: rule.Renaming, Scraping: rule.Scraping, LibraryTypeFolder: rule.LibraryTypeFolder, WorkflowID: id("wf_"), Files: []ImportFileRequest{{Path: file.Path}}}
	if rule.ID != "" {
		copyRule := rule
		request.directoryRule = &copyRule
	}
	var numbering *media.Numbering
	if file.IdentityReason != "" {
		return request, bad("DIRECTORY_IDENTITY_REVIEW", "NFO身份存在疑问，请在“手动整理”中选择电影或电视剧类型，并查询或填写TheMovieDb编号")
	}
	if hint := file.InferredMedia; hint != nil {
		request.MediaType = hint.Type
		request.TMDBID = hint.ID
		numbering = file.Numbering
	} else {
		p := file.Parsed
		if p.Status != media.Parsed || p.Title == "" {
			return request, bad("DIRECTORY_IDENTITY_REVIEW", "文件名缺少明确影视身份，请在“手动整理”中选择电影或电视剧类型，并查询或填写TheMovieDb编号")
		}
		kind := "movie"
		if p.Type == "episode" {
			kind = "tv"
		}
		if kind == "movie" && p.Year == 0 {
			return request, bad("DIRECTORY_IDENTITY_REVIEW", "电影文件名缺少年份，请在“手动整理”中选择电影类型，并查询或填写TheMovieDb编号")
		}
		matches, err := a.directoryTitleMatches(ctx, kind, p.Year, append([]string{p.Title}, p.AlternateTitles...))
		if err != nil {
			return request, err
		}
		if len(matches) == 0 && kind == "tv" {
			matches, err = a.directoryTVReleaseMatches(ctx, p)
			if err != nil {
				return request, err
			}
		}
		if len(matches) != 1 {
			return request, bad("DIRECTORY_IDENTITY_REVIEW", "影视身份不唯一或不完整，请在“手动整理”中选择电影或电视剧类型，并查询或填写TheMovieDb编号")
		}
		request.MediaType = kind
		for matchedID := range matches {
			request.TMDBID = matchedID
		}
		numbering = p.Original
	}
	if rule.MediaType != "" && rule.MediaType != "mixed" && rule.MediaType != request.MediaType {
		return request, bad("DIRECTORY_MEDIA_TYPE", "影视类型与目录规则不符")
	}
	if request.MediaType == "tv" {
		if numbering == nil || numbering.Season == nil || *numbering.Season < 0 || len(numbering.Episodes) == 0 {
			return request, bad("DIRECTORY_NUMBERING_REVIEW", "剧集需要明确季号和单集编号，请在“手动整理”中选择电视剧类型并设置季、集，必要时查询或填写TheMovieDb编号")
		}
		request.Files[0].Season = *numbering.Season
		request.Files[0].SeasonExplicit = true
		if file.InferredMedia != nil && numbering.Form == "nfo_episode" {
			request.Files[0].SourceEpisodeID = file.InferredMedia.EpisodeID
		}
		request.Files[0].Episodes = numbering.Episodes
	}
	if !validMedia(request.MediaType, request.TMDBID) {
		return request, fmt.Errorf("目录文件缺少有效影视身份")
	}
	return request, nil
}
