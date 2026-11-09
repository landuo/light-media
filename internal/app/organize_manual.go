package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"light-media/internal/catalog"
)

// Manual organization is one operation shared by files and transfer history.
// Preview uses the same preparation path without submitting a transfer job.
type manualOrganizeRequest struct {
	originalRetry         bool
	originalRetryAttempt  *originalTransferRetryAttempt
	originalResolvedMedia json.RawMessage
	providerIdentity      catalog.Media
	Paths                 []string `json:"paths"`
	HistoryIDs            []string `json:"history_ids"`
	FromHistory           bool     `json:"from_history"`
	TargetStorage         string   `json:"target_storage"`
	TargetPath            string   `json:"target_path"`
	Mode                  string   `json:"mode"`
	MediaType             string   `json:"media_type"`
	TMDBID                int64    `json:"tmdb_id"`
	Season                *int     `json:"season"`
	EpisodeDetail         string   `json:"episode_detail"`
	EpisodeFormat         string   `json:"episode_format"`
	EpisodeOffset         string   `json:"episode_offset"`
	EpisodePart           string   `json:"episode_part"`
	MinFilesize           int64    `json:"min_filesize"`
	Renaming              *bool    `json:"renaming"`
	Scraping              *bool    `json:"scraping"`
	LibraryTypeFolder     *bool    `json:"library_type_folder"`
	Preview               bool     `json:"preview"`
	Background            bool     `json:"background"`
}
type manualOrganizeItem struct {
	Source    string        `json:"source"`
	Target    string        `json:"target,omitempty"`
	Status    string        `json:"status"`
	Error     string        `json:"error,omitempty"`
	ErrorCode string        `json:"error_code,omitempty"`
	Media     catalog.Media `json:"media"`
	Mode      string        `json:"mode,omitempty"`
	PlanID    string        `json:"plan_id,omitempty"`
	JobID     string        `json:"job_id,omitempty"`
}
type manualOrganizeOutput struct {
	Items            []manualOrganizeItem `json:"items"`
	Total            int                  `json:"total"`
	Submitted        int                  `json:"submitted"`
	Failed           int                  `json:"failed"`
	PreparationJobID string               `json:"preparation_job_id,omitempty"`
}

func normalizeManualOrganize(in *manualOrganizeRequest) (*regexp.Regexp, error) {
	if in.TargetStorage != "" && in.TargetStorage != "local" {
		return nil, bad("INVALID_STORAGE", "请选择本地存储")
	}
	if len(in.Paths)+len(in.HistoryIDs) == 0 || len(in.Paths) > 2000 || len(in.HistoryIDs) > 1000 || in.MinFilesize < 0 || in.MinFilesize > 1<<30 {
		return nil, bad("INVALID_ORGANIZE", "请选择文件或整理历史，并检查最小文件大小")
	}
	if in.MediaType == "auto" {
		in.MediaType = ""
	}
	if in.Mode == "自动" {
		in.Mode = ""
	}
	if in.Season != nil && (*in.Season < 0 || *in.Season > 1000) {
		return nil, bad("INVALID_SEASON", "季号须在0至1000之间")
	}
	if in.TMDBID != 0 && !validMedia(in.MediaType, in.TMDBID) {
		return nil, bad("INVALID_MEDIA", "请先选择媒体类型，再填写TMDB ID")
	}
	return organizeEpisodeTemplate(in.EpisodeFormat)
}

func (a *App) manualOrganize(ctx context.Context, in manualOrganizeRequest) (out manualOrganizeOutput, returnErr error) {
	out = manualOrganizeOutput{Items: []manualOrganizeItem{}}
	format, err := normalizeManualOrganize(&in)
	if err != nil {
		return out, err
	}
	wf := id("wf_")
	var prepared, preparedHistorySources map[string]bool
	preparation, _ := ctx.Value(organizePreparationKey{}).(Job)
	work, isFileJob := ctx.Value(organizeFileKey{}).(organizeFileWork)
	if isFileJob {
		wf = work.Job.WorkflowID
	}
	if preparation.ID != "" {
		wf = preparation.WorkflowID
		prepared, preparedHistorySources, err = a.preparedOrganizeSources(ctx, preparation.ID, &out)
		if err != nil {
			return out, err
		}
	}
	histories := map[string]transferHistoryItem{}
	historyOwners := map[string]string{}
	historySources := map[string]string{}
	type priorHistory struct {
		item      transferHistoryItem
		plan      ImportPlan
		file      PlanFile
		key       string
		auxiliary bool
	}
	priorHistories := map[string]priorHistory{}
	paths := append([]string{}, in.Paths...)
	if len(in.HistoryIDs) > 0 {
		paths = nil
		var selected []transferHistoryItem
		if in.originalRetryAttempt != nil {
			for _, key := range in.HistoryIDs {
				if key != in.originalRetryAttempt.HistoryID {
					return out, originalTransferRetryStale()
				}
			}
			var item transferHistoryItem
			item, err = a.originalCurrentTransferRetry(ctx, *in.originalRetryAttempt, false)
			selected = []transferHistoryItem{item}
		} else {
			selected, err = a.transferHistoryItemsForOrganize(ctx, in.HistoryIDs)
			if preparation.ID != "" && errors.Is(err, sql.ErrNoRows) {
				return out, bad("REVIEW_ORGANIZE_HISTORY", "所选整理历史已变化，请刷新后重新选择")
			}
		}
		if err != nil {
			return out, err
		}
		for _, h := range selected {
			if preparedHistorySources[h.ResourceSource] {
				continue
			}
			path := h.Source
			if h.Target != "" && h.Status == "success" && h.Mode == "move" {
				path = h.Target
			}
			paths = append(paths, path)
			histories[path] = h
			historySources[path] = h.ResourceSource
			var original ImportPlan
			if e := a.Store.Get(ctx, "import_plan", h.PlanID, &original); e != nil {
				return out, e
			}
			file, auxiliary, key, e := a.historyPlannedFile(ctx, original, h)
			if e != nil {
				return out, e
			}
			if auxiliary {
				current, e := snapshotTransferPath(path)
				expected := planSnapshot(file)
				if path == h.Target {
					var receipt importReceipt
					if e := a.Store.Get(ctx, "import_receipt", key, &receipt); e != nil {
						return out, e
					}
					expected = receipt.StageSnapshot
				}
				if e != nil || current != expected {
					return out, bad("REVIEW_REORGANIZE_SOURCE", "附加文件在原整理后已发生变化")
				}
				priorHistories[path] = priorHistory{h, original, file, key, true}
			} else {
				// A failed primary may have a reserved owner ID for successful
				// sibling tasks, but no video asset to reorganize.
				var asset Asset
				e := a.Store.Get(ctx, "asset", file.AssetID, &asset)
				if e == nil {
					historyOwners[path] = asset.ID
					var meta importMetadata
					if e := a.Store.Get(ctx, "import_meta", original.ID, &meta); e != nil {
						return out, e
					}
					if _, ordinary := meta.OriginalTransfer[file.ID]; ordinary {
						priorHistories[path] = priorHistory{h, original, file, key, false}
						if asset.State == "removed" {
							if e := a.verifyOriginalHistoryCleanup(ctx, h, file, key, asset); e != nil {
								return out, e
							}
							// Cleanup committed before preparation was interrupted. The
							// new plan has no remaining video owner to replace.
							delete(historyOwners, path)
						}
					}
				} else if !errors.Is(e, sql.ErrNoRows) || h.Status == "success" {
					return out, e
				}
			}

		}
	}
	if !in.Preview && !isFileJob && (preparation.ID == "" || len(in.HistoryIDs) > 0) {
		batchID := id("scrape_batch_")
		if err := a.Store.Put(ctx, "original_scrape_batch", batchID, originalScrapeBatch{ID: batchID}); err != nil {
			return out, err
		}
		ctx = context.WithValue(ctx, originalScrapeBatchKey{}, batchID)
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			returnErr = errors.Join(returnErr, a.closeOriginalScrapeBatch(closeCtx, batchID, true))
		}()
	}
	fail := func(source string, e error) error {
		if isFileJob && (ctx.Err() != nil || organizeFileInterrupted(e)) {
			return e
		}
		item := manualOrganizeItem{Source: source, Status: "failed", Error: a.clean(e.Error())}
		var api *APIError
		if errors.As(e, &api) {
			item.ErrorCode = api.Code
		}
		if !in.Preview {
			job, storeErr := a.recordManualOrganizeFailure(ctx, source, wf, e, historySources[source])
			if storeErr != nil {
				return storeErr
			}
			item.JobID = job.ID
		}
		out.Items = append(out.Items, item)
		out.Failed++
		return nil
	}
	files := []importScanFile{}
	if isFileJob {
		files = append(files, work.File)
		paths = nil // The directory already enumerated and froze this candidate.
	}
	seen := map[string]bool{}
	for _, path := range paths {
		if seen[path] || prepared[path] {
			continue
		}
		if preparation.ID != "" {
			if e := a.jobCancelled(ctx, preparation); e != nil {
				return out, e
			}
		}
		seen[path] = true
		scan, e := a.manualOrganizeScan(ctx, path, in)
		if e != nil {
			if e = fail(path, e); e != nil {
				return out, e
			}
			continue
		}
		result := scan.(map[string]any)
		if result["truncated"] == true {
			if e = fail(path, bad("FILE_SELECTION_LIMIT", "目录扫描超过上限，请缩小选择范围")); e != nil {
				return out, e
			}
			continue
		}
		found := result["files"].([]importScanFile)
		if len(found) == 0 {
			if e = fail(path, bad("NO_MEDIA_FILES", "未找到可整理的视频文件")); e != nil {
				return out, e
			}
		}
		for _, file := range found {
			if !seen["file:"+file.Path] {
				seen["file:"+file.Path] = true
				files = append(files, file)
			}
		}
	}
	if len(files) > 2000 {
		return out, bad("FILE_SELECTION_LIMIT", "一次最多整理2000个视频")
	}
	if preparation.ID != "" && len(in.HistoryIDs) == 0 {
		if in.EpisodeDetail != "" && format == nil && len(files) > 1 {
			return out, bad("INVALID_EPISODE_RANGE_REQUIRES_FORMAT", "多个文件指定集数时，请填写集数定位")
		}
		var limit []int
		if format != nil && in.EpisodeDetail != "" {
			limit, err = organizeEpisodeRange(in.EpisodeDetail)
			if err != nil {
				return out, err
			}
		}
		files = slices.DeleteFunc(files, func(file importScanFile) bool {
			if !file.originalDirectoryPlanned && file.materialKind == "" && in.MinFilesize > 0 && file.Size <= in.MinFilesize*1024*1024 {
				return true
			}
			if format == nil {
				return false
			}
			match := format.FindStringSubmatch(manualOrganizeFileName(file))
			if match == nil {
				return true
			}
			if len(limit) > 0 {
				episodes, e := organizeEpisodeRange(match[format.SubexpIndex("ep")])
				return e == nil && (episodes[0] < limit[0] || episodes[0] > limit[len(limit)-1])
			}
			return false
		})
		if len(files) == 0 && out.Submitted == 0 && out.Failed == 0 {
			return out, bad("REVIEW_ORGANIZE_PREPARATION", "未找到符合整理条件的文件")
		}
		return a.enqueueOrganizeFiles(ctx, preparation, files, prepared, out)
	}
	for _, file := range files {
		err = nil
		if prepared[file.Path] {
			continue
		}
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		// The original transfer filter exempts subtitle and audio tasks from the video size threshold.
		if !file.originalDirectoryPlanned && file.materialKind == "" && in.MinFilesize > 0 && file.Size <= in.MinFilesize*1024*1024 {
			out.Items = append(out.Items, manualOrganizeItem{Source: file.Path, Status: "skipped", Error: "小于最小文件大小"})
			continue
		}
		request := ImportRequest{MediaType: in.MediaType, TMDBID: in.TMDBID, TargetPath: in.TargetPath, Mode: in.Mode, Renaming: in.Renaming, Scraping: in.Scraping, LibraryTypeFolder: in.LibraryTypeFolder, WorkflowID: wf, Files: []ImportFileRequest{{Path: file.Path}}}
		request.Manual = true
		request.originalResolvedMedia = in.originalResolvedMedia
		request.originalRetry = in.originalRetryAttempt != nil
		request.providerIdentity = in.providerIdentity
		request.Files[0].originalMetadataPath = file.originalMetadataPath
		request.Files[0].originalItem = file.originalItem
		request.Files[0].originalResolvedMeta = file.originalResolvedMeta.DeepCopy()
		request.originalDirectoryListings = file.originalDirectoryListings
		request.originalDirectoryPlanned = file.originalDirectoryPlanned
		request.ReorganizeAssetID = historyOwners[file.Path]
		if prior, ok := priorHistories[file.Path]; ok {
			request.originalSingleHistory = !prior.auxiliary
		}
		request.HistorySource = historySources[file.Path]
		if len(in.HistoryIDs) == 0 {
			var owned managedFile
			if e := a.Store.Get(ctx, "managed_file", tokenHash(file.Path), &owned); e == nil && owned.Kind == "video" && owned.AssetID != "" {
				var asset Asset
				if e = a.Store.Get(ctx, "asset", owned.AssetID, &asset); e != nil {
					return out, e
				}
				if asset.Path == file.Path && (asset.State == "present" || asset.State == "external") {
					request.ReorganizeAssetID = asset.ID
					request.HistorySource = asset.SourcePath
				}
			} else if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return out, e
			}
		}
		historySources[file.Path] = request.HistorySource
		if h, ok := histories[file.Path]; ok && in.FromHistory {
			if !validMedia(request.MediaType, request.TMDBID) {
				request.MediaType, request.TMDBID = h.Media.MediaType, h.Media.ID
			}
			request.Files[0].Season = h.Season
			request.Files[0].SeasonExplicit = true
			request.Files[0].Part = h.Part
			request.Files[0].Episodes = append([]int{}, h.Episodes...)
		}
		if in.Season != nil {
			request.Files[0].Season = *in.Season
			request.Files[0].SeasonExplicit = true
		}
		if in.EpisodePart != "" {
			request.Files[0].Part = in.EpisodePart
		}
		episodes := []int{}
		if format != nil {
			matches := format.FindStringSubmatch(manualOrganizeFileName(file))
			if matches == nil {
				out.Items = append(out.Items, manualOrganizeItem{Source: file.Path, Status: "skipped", Error: "不符合指定的集数定位"})
				continue
			}
			for i, name := range format.SubexpNames() {
				if name == "ep" {
					episodes, err = organizeEpisodeRange(matches[i])
					break
				}
			}
			if err != nil {
				if e := fail(file.Path, err); e != nil {
					return out, e
				}
				continue
			}
		} else if in.EpisodeDetail != "" {
			if len(files) > 1 && len(in.HistoryIDs) == 0 {
				return out, bad("EPISODE_RANGE_REQUIRES_FORMAT", "多个文件指定集数时，请填写集数定位")
			}
			episodes, err = organizeEpisodeRange(in.EpisodeDetail)
			if err != nil {
				if e := fail(file.Path, err); e != nil {
					return out, e
				}
				continue
			}
		}
		if format != nil && in.EpisodeDetail != "" {
			limit, e := organizeEpisodeRange(in.EpisodeDetail)
			if e != nil {
				return out, e
			}
			if len(episodes) == 0 || episodes[0] < limit[0] || episodes[0] > limit[len(limit)-1] {
				out.Items = append(out.Items, manualOrganizeItem{Source: file.Path, Status: "skipped", Error: "不在指定集数范围内"})
				continue
			}
		}
		if len(episodes) == 0 && in.EpisodeOffset != "" {
			if len(request.Files[0].Episodes) > 0 {
				episodes = append(episodes, request.Files[0].Episodes...)
			} else if file.Numbering != nil {
				episodes = append(episodes, file.Numbering.Episodes...)
			} else if file.Parsed.Original != nil {
				episodes = append(episodes, file.Parsed.Original.Episodes...)
			}
		}
		for i, ep := range episodes {
			episodes[i], err = organizeEpisodeOffset(in.EpisodeOffset, ep)
			if err != nil {
				break
			}
		}
		if err != nil {
			if e := fail(file.Path, err); e != nil {
				return out, e
			}
			err = nil
			continue
		}
		if len(episodes) > 0 {
			request.Files[0].Episodes = episodes
		}
		if meta := file.originalResolvedMeta; meta != nil {
			if n := meta.Number("begin_season"); n != nil && n.IsInt64() {
				request.Files[0].Season = int(n.Int64())
				request.Files[0].SeasonExplicit = true
			}
			numbers, e := meta.EpisodeList()
			if e != nil {
				return out, e
			}
			request.Files[0].Episodes = nil
			for _, n := range numbers {
				if !n.IsInt64() || n.Sign() <= 0 || n.Int64() > 9999 {
					return out, bad("DIRECTORY_NUMBERING_REVIEW", "剧集编号超出受支持范围")
				}
				request.Files[0].Episodes = append(request.Files[0].Episodes, int(n.Int64()))
			}
		}
		// Explicit numbering can supply a season after automatic media lookup.
		request, err = a.prepareManualImport(ctx, request)
		if err == nil && len(request.Files[0].Episodes) > 0 && request.Files[0].Season == 0 && !request.Files[0].SeasonExplicit && in.Season == nil && file.Parsed.Original != nil && file.Parsed.Original.Season != nil {
			request.Files[0].Season = *file.Parsed.Original.Season
		}
		var plan ImportPlan
		if err == nil {
			request.originalModulePreview = in.Preview
			plan, err = a.previewImport(ctx, request)
		}
		if err != nil {
			if e := fail(file.Path, err); e != nil {
				return out, e
			}
			err = nil
			continue
		}
		if in.Preview {
			for _, p := range plan.Files {
				item := manualOrganizeItem{Source: p.Source, Target: p.Target, Status: "ready", Media: plan.Media, Mode: plan.Mode, PlanID: plan.ID}
				if p.Status == "blocked" {
					item.Status = "failed"
					item.Error = p.Reason
					if !p.OriginalPreviewCallback {
						item.Media = catalog.Media{}
					}
					out.Failed++
				}
				out.Items = append(out.Items, item)
			}
			continue
		}
		item := manualOrganizeItem{Source: file.Path, Status: "ready", Media: plan.Media, Mode: plan.Mode, PlanID: plan.ID}
		for _, p := range plan.Files {
			item.Target = p.Target
			if p.Status == "blocked" {
				item.Status = "failed"
				item.Error = p.Reason
			}
		}
		if item.Status == "failed" {
			if e := fail(file.Path, bad("REVIEW_IMPORT", item.Error)); e != nil {
				return out, e
			}
			continue
		}
		if !in.Preview {
			executeCtx := ctx
			if in.originalRetryAttempt != nil {
				if _, err := a.originalCurrentTransferRetry(ctx, *in.originalRetryAttempt, false); err != nil {
					return out, err
				}
				executeCtx = context.WithValue(ctx, originalTransferRetryAttemptKey{}, *in.originalRetryAttempt)
			}
			// The pinned manual history endpoint cleans only this history's
			// destination before starting the explicitly requested transfer.
			// Preview does not delete anything; no parent video is selected.
			if prior, ok := priorHistories[file.Path]; ok && prior.item.Target != "" && prior.item.Target != file.Path {
				if preparation.ID != "" {
					if e := a.jobCancelled(ctx, preparation); e != nil {
						return out, e
					}
				}
				if e := a.prepareOriginalHistoryTransfer(ctx, plan, prior.item, prior.plan, prior.file, prior.key, prior.auxiliary, in.originalRetry); e != nil {
					// Source cleanup rejection returns failure without replacing the
					// existing successful history with a failed preparation record.
					failed := manualOrganizeItem{Source: file.Path, Status: "failed", Error: a.clean(e.Error())}
					var api *APIError
					if errors.As(e, &api) {
						failed.ErrorCode = api.Code
					}
					out.Items = append(out.Items, failed)
					out.Failed++
					continue
				}
			}
			job, e := a.executeImport(executeCtx, plan.ID, nil)
			if e != nil {
				if e = fail(file.Path, e); e != nil {
					return out, e
				}
				continue
			}
			item.JobID = job.ID
			item.Status = "queued"
			out.Submitted++
		}
		out.Items = append(out.Items, item)
	}
	out.Total = len(out.Items)
	return out, nil
}

func manualOrganizeFileName(file importScanFile) string {
	name := filepath.Base(file.Path)
	if file.originalItem != nil {
		_ = json.Unmarshal(file.originalItem.Fields["name"], &name)
	}
	return name
}

func (a *App) recordManualOrganizeFailure(ctx context.Context, path, wf string, cause error, historySource ...string) (Job, error) {
	plan := ImportPlan{ID: id("plan_"), WorkflowID: wf, State: "failed", CreatedAt: now(), Files: []PlanFile{{ID: id("file_"), Source: path, Status: "blocked", Reason: a.clean(cause.Error())}}}
	if len(historySource) > 0 {
		plan.Files[0].HistorySource = historySource[0]
	}
	if stamp, e := snapshotTransferPath(path); e == nil {
		f := &plan.Files[0]
		f.Size, f.Device, f.Inode, f.ModNS = stamp.Size, stamp.Device, stamp.Inode, stamp.ModNS
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return Job{}, e
	}
	defer tx.Rollback()
	if e = txPut(ctx, tx, "import_plan", plan.ID, plan); e != nil {
		return Job{}, e
	}
	payload := map[string]any{"id": plan.ID, "file_ids": []string{plan.Files[0].ID}}
	if _, ok := ctx.Value(organizePreparationKey{}).(Job); ok {
		payload["manual_preparation_failed"] = true
	}
	code := "ORGANIZE_PREPARATION_FAILED"
	var api *APIError
	if errors.As(cause, &api) {
		code = api.Code
	}
	reason := a.clean(cause.Error())
	var job Job
	work, isFileJob := ctx.Value(organizeFileKey{}).(organizeFileWork)
	var events []json.RawMessage
	if isFileJob {
		job, e = promoteOrganizeFileTx(ctx, tx, work, plan.ID, []string{plan.Files[0].ID}, "failed", code, reason)
		if e == nil {
			events, e = flushOriginalScrapeBatchTx(ctx, tx, work.BatchID, false)
		}
	} else {
		job, e = enqueueTx(ctx, tx, "import", payload, wf)
		if e == nil {
			_, e = tx.ExecContext(ctx, `UPDATE jobs SET state='failed',attempt=1,error_code=?,error_message=?,updated_at=? WHERE id=?`, code, reason, now(), job.ID)
		}
	}
	if e != nil {
		return Job{}, e
	}
	if e = tx.Commit(); e != nil {
		return Job{}, e
	}
	job.State, job.ErrorCode, job.ErrorMessage = "failed", code, a.clean(cause.Error())
	a.broadcastOriginalScrapeBatch(ctx, events)
	a.log("WARN", "import", "preparation_failed", job.ErrorMessage, wf, job.ID)
	if code == "MEDIA_NOT_FOUND" {
		history, err := a.transferHistoryPlanFile(ctx, plan.ID, plan.Files[0].ID)
		if err != nil {
			a.originalTransferEventError(ctx, err)
		} else if history.JobID == job.ID {
			a.originalTransferFailureNotice(ctx, history, nil)
		}
	}
	return job, nil
}

func organizeEpisodeRange(value string) ([]int, error) {
	match := regexp.MustCompile(`(?i)^(?:EP|E)?([0-9]{1,4})(?:\s*[,-]\s*(?:EP|E)?([0-9]{1,4}))?$`).FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return nil, bad("INVALID_EPISODES", "集数应为单集或起止范围，例如1或1,12")
	}
	parts := []string{match[1]}
	if match[2] != "" {
		parts = append(parts, match[2])
	}
	first, e := strconv.Atoi(strings.TrimSpace(parts[0]))
	if e != nil || first < 1 || first > 9999 {
		return nil, bad("INVALID_EPISODES", "集号须在1至9999之间")
	}
	last := first
	if len(parts) == 2 {
		last, e = strconv.Atoi(strings.TrimSpace(parts[1]))
		if e != nil || last < first || last > 9999 || last-first > 2000 {
			return nil, bad("INVALID_EPISODES", "集数范围无效")
		}
	}
	result := []int{}
	for n := first; n <= last; n++ {
		result = append(result, n)
	}
	return result, nil
}
func organizeEpisodeTemplate(value string) (*regexp.Regexp, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > 512 {
		return nil, bad("INVALID_EPISODE_FORMAT", "集数定位过长")
	}
	var b strings.Builder
	b.WriteString("^")
	seen := map[string]bool{}
	for i := 0; i < len(value); {
		if strings.HasPrefix(value[i:], "{{") {
			b.WriteString(regexp.QuoteMeta("{"))
			i += 2
			continue
		}
		if strings.HasPrefix(value[i:], "}}") {
			b.WriteString(regexp.QuoteMeta("}"))
			i += 2
			continue
		}
		if value[i] == '{' {
			end := strings.IndexByte(value[i:], '}')
			if end < 0 {
				return nil, bad("INVALID_EPISODE_FORMAT", "集数定位存在未闭合占位符")
			}
			name := value[i+1 : i+end]
			if !regexp.MustCompile(`^[A-Za-z_]\w*$`).MatchString(name) || seen[name] {
				return nil, bad("INVALID_EPISODE_FORMAT", "占位符名称无效或重复")
			}
			seen[name] = true
			if name == "ep" {
				b.WriteString("(?P<ep>.+?)")
			} else {
				b.WriteString(".*?")
			}
			i += end + 1
			continue
		}
		if value[i] == '}' {
			return nil, bad("INVALID_EPISODE_FORMAT", "请将右花括号转义为双括号")
		}
		end := i + 1
		for end < len(value) && value[end] != '{' && value[end] != '}' {
			end++
		}
		b.WriteString(regexp.QuoteMeta(value[i:end]))
		i = end
	}
	if !seen["ep"] {
		return nil, bad("INVALID_EPISODE_FORMAT", "集数定位应包含{ep}")
	}
	b.WriteString("$")
	compiled, e := regexp.Compile(b.String())
	if e != nil {
		return nil, e
	}
	return compiled, nil
}
func organizeEpisodeOffset(expression string, episode int) (int, error) {
	if expression == "" {
		return episode, nil
	}
	if len(expression) > 64 {
		return 0, bad("INVALID_EPISODE_OFFSET", "集数偏移表达式过长")
	}
	expression = strings.ToUpper(strings.TrimSpace(expression))
	if !strings.Contains(expression, "EP") {
		if !strings.HasPrefix(expression, "-") && !strings.HasPrefix(expression, "+") {
			expression = "+" + expression
		}
		expression = "EP" + expression
	}
	expr, e := parser.ParseExpr(expression)
	if e != nil {
		return 0, bad("INVALID_EPISODE_OFFSET", "集数偏移表达式无效")
	}
	var eval func(ast.Expr, int) (int64, error)
	eval = func(node ast.Expr, depth int) (int64, error) {
		if depth > 12 {
			return 0, fmt.Errorf("expression too deep")
		}
		switch n := node.(type) {
		case *ast.Ident:
			if n.Name == "EP" {
				return int64(episode), nil
			}
		case *ast.BasicLit:
			if n.Kind == token.INT {
				return strconv.ParseInt(n.Value, 10, 32)
			}
		case *ast.ParenExpr:
			return eval(n.X, depth+1)
		case *ast.UnaryExpr:
			v, e := eval(n.X, depth+1)
			if e != nil {
				return 0, e
			}
			if n.Op == token.SUB {
				return -v, nil
			}
			if n.Op == token.ADD {
				return v, nil
			}
		case *ast.BinaryExpr:
			l, e := eval(n.X, depth+1)
			if e != nil {
				return 0, e
			}
			r, e := eval(n.Y, depth+1)
			if e != nil {
				return 0, e
			}
			if l > 1e6 || l < -1e6 || r > 1e6 || r < -1e6 {
				return 0, fmt.Errorf("overflow")
			}
			switch n.Op {
			case token.ADD:
				return l + r, nil
			case token.SUB:
				return l - r, nil
			case token.MUL:
				return l * r, nil
			case token.QUO:
				if r != 0 && l%r == 0 {
					return l / r, nil
				}
			}
		}
		return 0, fmt.Errorf("unsupported episode expression")
	}
	result, e := eval(expr, 0)
	if e != nil || result < 1 || result > 9999 {
		return 0, bad("INVALID_EPISODE_OFFSET", "偏移后须为1至9999的整数集号，仅支持EP与加减乘除")
	}
	return int(result), nil
}

func (a *App) recognizeBrowserFile(ctx context.Context, path string) (any, error) {
	scan, e := a.browserScan(ctx, []string{path}, true)
	if e != nil {
		return nil, e
	}
	files := scan.(map[string]any)["files"].([]importScanFile)
	if len(files) == 0 {
		return nil, bad("NO_MEDIA_FILES", "未找到视频文件")
	}
	request, e := a.directoryFileIdentity(ctx, DirectoryRule{MediaType: "mixed"}, files[0])
	if e != nil {
		return nil, e
	}
	identity, e := a.media(ctx, request.MediaType, request.TMDBID)
	if e != nil {
		return nil, e
	}
	return map[string]any{"media": identity, "parsed": files[0].Parsed, "season": request.Files[0].Season, "episodes": request.Files[0].Episodes}, nil
}

const activeOrganizeStates = `('queued','running','retry_wait','waiting_paused')`

type organizeQueuePreparation struct {
	ID           string   `json:"id"`
	Paths        []string `json:"paths"`
	State        string   `json:"state"`
	HistoryCount int      `json:"history_count,omitempty"`
}

func (a *App) organizeQueue(ctx context.Context) (any, error) {
	if err := a.seedOrganizeQueue(ctx); err != nil {
		return nil, err
	}
	jobs, e := a.queryJobs(ctx, `SELECT `+jobColumns+` FROM jobs
		WHERE kind IN ('import','organize_prepare','organize_file') AND state!='cancelled' AND (state IN `+activeOrganizeStates+` OR id IN (
		 SELECT json_extract(payload,'$.job') FROM records WHERE kind='organize_queue_member' AND json_extract(payload,'$.group') IN (`+liveOrganizeGroups+`)))
		AND (kind!='organize_prepare' OR NOT EXISTS(SELECT 1 FROM records WHERE kind='organize_file_listing' AND records.id=jobs.id))
		ORDER BY created_at DESC, id`)
	if e != nil {
		return nil, e
	}
	groups := []map[string]any{}
	preparations := []organizeQueuePreparation{}
	seen := map[string]bool{}
	for _, job := range jobs {
		if job.Kind == "organize_prepare" {
			var payload organizePreparationPayload
			if json.Unmarshal(job.Payload, &payload) == nil && len(payload.Request.Paths)+len(payload.Request.HistoryIDs) > 0 {
				paths := append([]string{}, payload.Request.Paths...)
				preparations = append(preparations, organizeQueuePreparation{ID: job.ID, Paths: paths, State: job.State, HistoryCount: len(payload.Request.HistoryIDs)})
			}
			continue
		}
		if job.Kind == "organize_file" {
			var payload organizeFilePayload
			if json.Unmarshal(job.Payload, &payload) != nil || payload.File.Path == "" {
				continue
			}
			file := map[string]any{"id": job.ID, "source": payload.File.Path, "size": payload.File.Size, "status": "pending", "job_id": job.ID, "job_state": job.State, "can_cancel": true, "recognizing": true}
			groups = append(groups, map[string]any{"id": job.ID, "title": payload.Title, "media": map[string]string{"title": payload.Title}, "status": job.State, "files": []map[string]any{file}})
			continue
		}
		var payload struct {
			ID      string   `json:"id"`
			FileIDs []string `json:"file_ids"`
		}
		if json.Unmarshal(job.Payload, &payload) != nil {
			continue
		}
		var plan ImportPlan
		if a.Store.Get(ctx, "import_plan", payload.ID, &plan) != nil {
			continue
		}
		files := []map[string]any{}
		for _, f := range plan.Files {
			if len(payload.FileIDs) > 0 && !slices.Contains(payload.FileIDs, f.ID) {
				continue
			}
			fileKey := plan.ID + "/" + f.ID
			if seen[fileKey] {
				continue
			}
			seen[fileKey] = true
			var cancelled bool
			if err := a.Store.reader().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM records WHERE kind='organize_file_cancel' AND id=?)`, job.ID+"/"+f.ID).Scan(&cancelled); err != nil {
				return nil, err
			}
			if cancelled {
				continue
			}
			canCancel := job.State == "queued" || job.State == "running" || job.State == "retry_wait" || job.State == "waiting_paused"
			entry := map[string]any{"id": f.ID, "source": f.Source, "target": f.Target, "size": f.Size, "status": f.Status, "error": f.Reason, "job_id": job.ID, "job_state": job.State, "can_cancel": canCancel}
			if progress, ok := a.organizeFileProgress(f); ok {
				entry["progress"] = progress
			}
			files = append(files, entry)
		}
		if len(files) == 0 {
			continue
		}
		title := plan.Media.Title
		if title == "" && len(plan.Files) > 0 {
			title = filepath.Base(plan.Files[0].Source)
		}
		groups = append(groups, map[string]any{"id": job.ID, "title": title, "media": plan.Media, "status": job.State, "error": job.ErrorMessage, "files": files})
	}
	return map[string]any{"groups": groups, "preparations": preparations}, nil
}

func historySearchPattern(value string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`, "*", "%", "?", "_").Replace(value) + "%"
}

func (a *App) cancelOrganizeQueueFile(ctx context.Context, key string) (any, error) {
	if strings.HasPrefix(key, "job_") {
		job, err := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=? AND kind IN ('organize_file','import')`, key))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, bad("QUEUE_FILE_FINISHED", "此文件整理任务已经结束")
		}
		if err != nil {
			return nil, err
		}
		if job.Kind == "organize_file" {
			return a.cancelOrganizeFile(ctx, job)
		}
		var payload struct {
			FileIDs []string `json:"file_ids"`
		}
		if json.Unmarshal(job.Payload, &payload) != nil || len(payload.FileIDs) != 1 {
			return nil, bad("QUEUE_FILE_CHANGED", "文件已完成识别，请刷新整理队列后操作")
		}
		key = payload.FileIDs[0]
	}
	item, e := a.transferHistoryExecution(ctx, key)
	if e != nil {
		return nil, e
	}
	var plan ImportPlan
	if e = a.Store.Get(ctx, "import_plan", item.PlanID, &plan); e != nil {
		return nil, e
	}
	if len(plan.Files) != 1 {
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		var active bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE id=? AND state IN `+activeOrganizeStates+`)`, item.JobID).Scan(&active); err != nil {
			return nil, err
		}
		if !active {
			return nil, bad("QUEUE_FILE_FINISHED", "此文件整理任务已经结束")
		}
		if err = txPut(ctx, tx, "organize_file_cancel", item.JobID+"/"+item.FileID, struct{}{}); err != nil {
			return nil, err
		}
		return map[string]bool{"cancelled": true}, tx.Commit()
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	result, e := tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,lease_until=0,updated_at=? WHERE id=? AND state IN `+activeOrganizeStates, now(), item.JobID)
	if e != nil {
		return nil, e
	}
	count, e := result.RowsAffected()
	if e != nil {
		return nil, e
	}
	if count != 1 {
		return nil, bad("QUEUE_FILE_FINISHED", "此文件整理任务已经结束")
	}
	reservationOwner := plan.IntentID
	if reservationOwner == "" {
		reservationOwner = plan.ID
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM reservations WHERE intent_id=?`, reservationOwner); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	job, e := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, item.JobID))
	if e != nil {
		return nil, e
	}
	if e = a.closeOriginalScrapeBatch(ctx, originalScrapeBatchID(job), false); e != nil {
		return nil, e
	}
	return map[string]bool{"cancelled": true}, nil
}
