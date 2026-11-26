package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
)

// Expand only the supplied paths or history IDs. A directory must yield its
// actual transfer tasks before the caller can be told that it is queued.
func (a *App) queueSelectedManualFiles(ctx context.Context, in manualOrganizeRequest) (manualOrganizeOutput, error) {
	out := manualOrganizeOutput{Items: []manualOrganizeItem{}}
	format, err := normalizeManualOrganize(&in)
	if err != nil {
		return out, err
	}
	if !in.Background && a.paused() {
		return out, &APIError{"AUTOMATION_PAUSED", "自动化已暂停，请先恢复", 409}
	}
	if len(in.Paths) == 1 && len(in.HistoryIDs) == 0 {
		if single, handled, e := a.queueInteractiveOrganizeFile(ctx, in); handled {
			return single, e
		}
	}
	ctx, err = a.ensureResourceRecognition(ctx)
	if err != nil {
		return out, err
	}
	if ctx.Value(manualPreviewInputsKey{}) == nil {
		ctx = context.WithValue(ctx, manualPreviewInputsKey{}, &manualPreviewInputs{nfos: make(map[string]manualPreviewNFO)})
	}
	type selection struct {
		path    string
		history *transferHistoryItem
	}
	var selections []selection
	fail := func(source string, cause error) {
		item := manualOrganizeItem{Source: source, Status: "failed", Error: a.clean(cause.Error())}
		var api *APIError
		if errors.As(cause, &api) {
			item.ErrorCode = api.Code
		}
		out.Items = append(out.Items, item)
		out.Failed++
	}
	if len(in.HistoryIDs) > 0 {
		rows, e := a.transferHistorySelections(ctx, in.HistoryIDs)
		if e != nil {
			return out, e
		}
		seen := map[string]bool{}
		for i := range rows {
			h := &rows[i]
			if h.ID == "" {
				fail("", bad("HISTORY_NOT_FOUND", "整理记录不存在，ID："+in.HistoryIDs[i]))
				continue
			}
			if !seen[h.ResourceSource] {
				selections = append(selections, selection{h.organizeSource(), h})
				seen[h.ResourceSource] = true
			}
		}
	} else {
		for _, path := range in.Paths {
			selections = append(selections, selection{path, nil})
		}
	}
	batch := id("scrape_batch_")
	listings := make(map[string][]map[string]any)
	var admissions []organizeAdmission
	seenPaths, seenFiles := map[string]bool{}, map[string]bool{}
	for _, selected := range selections {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		path := selected.path
		if seenPaths[path] {
			continue
		}
		seenPaths[path] = true
		linked, e := a.browserOrganizeLinkedSource(ctx, path)
		if e != nil {
			fail(path, e)
			continue
		}
		if linked || in.Mode == "softlink" {
			path, e = a.resolveSoftlinkReorganizeSource(ctx, path)
			if e != nil {
				fail(selected.path, e)
				continue
			}
		}
		scoped := in
		if selected.history != nil && scoped.FromHistory {
			scoped.inheritHistoryIdentity(*selected.history)
		}
		var limit []int
		if format != nil && scoped.EpisodeDetail != "" {
			limit, e = organizeEpisodeRange(scoped.EpisodeDetail)
			if e != nil {
				if selected.history == nil {
					return out, e
				}
				fail(selected.path, e)
				continue
			}
		}
		scan, e := a.manualOrganizeScan(ctx, path, scoped)
		if e != nil {
			fail(selected.path, e)
			continue
		}
		result := scan.(map[string]any)
		if result["truncated"] == true {
			fail(path, bad("FILE_SELECTION_LIMIT", "目录扫描超过上限，请缩小选择范围"))
			continue
		}
		files := result["files"].([]importScanFile)
		if len(files) == 0 {
			if format != nil {
				out.Items = append(out.Items, manualOrganizeItem{Source: path, Status: "skipped", Error: "不符合指定的集数定位"})
			} else {
				fail(path, bad("NO_MEDIA_FILES", "未找到可整理的视频文件"))
			}
			continue
		}
		for _, file := range files {
			if seenFiles[file.Path] {
				continue
			}
			seenFiles[file.Path] = true
			if len(seenFiles) > 2000 {
				return out, bad("FILE_SELECTION_LIMIT", "一次最多整理2000个视频")
			}
			if reason := manualOrganizeSkip(file, in.MinFilesize, format, limit); reason != "" {
				out.Items = append(out.Items, manualOrganizeItem{Source: file.Path, Status: "skipped", Error: reason})
				continue
			}
			stamp := file.Snapshot
			if stamp == nil {
				observed, err := snapshotTransferPath(file.Path)
				if err != nil {
					fail(file.Path, err)
					continue
				}
				stamp = &observed
			}
			req := in
			req.Background = false
			req.Paths, req.HistoryIDs = []string{file.Path}, nil
			if file.Path == path && selected.path != path {
				req.Paths = []string{selected.path}
			}
			if selected.history != nil {
				if len(files) != 1 {
					fail(file.Path, bad("REVIEW_ORGANIZE_HISTORY", "所选整理历史不再对应同一源文件"))
					continue
				}
				req.Paths, req.HistoryIDs = nil, []string{selected.history.ID}
				file.queueHistory = selected.history
			}
			key, e := interactiveOrganizeFileKey(req, *stamp)
			if e != nil {
				return out, e
			}
			if file.queueHistory != nil {
				key = tokenHash(key + "\x00" + file.queueHistory.ExecutionKey)
			}
			candidate, e := frozenOrganizeFile(file, *stamp, key, batch)
			if e != nil {
				return out, e
			}
			candidate.Manual, candidate.Enumerated = &req, true
			admissions = append(admissions, organizeAdmission{kind: "organize_file", subject: file.Path, key: key, payload: candidate})
			for _, dir := range []string{filepath.Dir(file.Path), filepath.Dir(file.originalMetadataPath)} {
				if items, ok := file.originalDirectoryListings[dir]; ok {
					listings[dir] = items
				}
			}
		}
	}
	if len(in.HistoryIDs) == 0 && len(seenFiles) > 1 && in.EpisodeDetail != "" && format == nil {
		return out, bad("INVALID_EPISODE_RANGE_REQUIRES_FORMAT", "多个文件指定集数时，请填写集数定位")
	}
	if len(admissions) > 0 {
		if err = a.acceptOrganizeBatch(ctx, admissions, batch, listings, in.audit); err != nil {
			return out, err
		}
		for _, r := range admissions {
			if r.key != r.activeKey {
				fail(r.subject, bad("JOB_EXISTS", "该源文件已有其他整理任务正在等待或执行"))
				continue
			}
			out.Items = append(out.Items, manualOrganizeItem{Source: r.subject, Status: "queued", JobID: r.id})
			out.Submitted++
		}
	}
	out.Total = len(out.Items)
	return out, nil
}

func interactiveOrganizeFileKey(in manualOrganizeRequest, stamp importSnapshot) (string, error) {
	raw, err := json.Marshal(struct {
		Request manualOrganizeRequest
		Stamp   importSnapshot
	}{in, stamp})
	if err != nil {
		return "", err
	}
	return tokenHash(string(raw)), nil
}
