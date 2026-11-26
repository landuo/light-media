package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"

	"light-media/internal/transferextensions"
)

// Persist the selection and source identity before acknowledging a single file.
// The same job ID is promoted from recognition to import, including on restart.
func (a *App) queueInteractiveOrganizeFile(ctx context.Context, in manualOrganizeRequest) (manualOrganizeOutput, bool, error) {
	phase := a.performanceStages(ctx, "organize_submit", "validate_source")
	defer phase("")
	var out manualOrganizeOutput
	format, err := normalizeManualOrganize(&in)
	if err != nil {
		return out, true, err
	}
	path := in.Paths[0]
	if !a.importSourceAllowed(path, true) || browserInternal(path) || originalTransferBlockedExtra(path) || originalTransferBlurayStream.MatchString(path) {
		return out, false, nil
	}
	stamp, err := snapshotPath(path)
	if err != nil || stamp.Size == 0 {
		return out, false, nil // Directories, links and empty files keep the existing flow.
	}
	classifier, err := a.transferMediaClassifier(ctx)
	if err != nil {
		return out, true, err
	}
	if video, _ := classifier.Media(transferextensions.PathFile(path)); !video {
		return out, false, nil
	}
	if format != nil || in.MinFilesize > 0 && stamp.Size <= in.MinFilesize*1024*1024 {
		// The storage module can supply a display name and size. Use its actual
		// scan item rather than assuming the basename matches the episode filter.
		scan, err := a.manualOrganizeScan(ctx, path, in)
		if err != nil {
			return out, false, nil
		}
		files := scan.(map[string]any)["files"].([]importScanFile)
		if len(files) != 1 || files[0].Path != path {
			return out, false, nil
		}
		var limit []int
		if format != nil && in.EpisodeDetail != "" {
			limit, err = organizeEpisodeRange(in.EpisodeDetail)
			if err != nil {
				return out, true, err
			}
		}
		if reason := manualOrganizeSkip(files[0], in.MinFilesize, format, limit); reason != "" {
			out.Total = 1
			out.Items = []manualOrganizeItem{{Source: path, Status: "skipped", Error: reason}}
			return out, true, nil
		}
	}
	if !in.Background && a.paused() {
		return out, true, &APIError{"AUTOMATION_PAUSED", "自动化已暂停，请先恢复", 409}
	}
	// Background and foreground only differ in how the caller waits. A retry
	// with changed options must never bind to the earlier operation.
	in.Background = false
	raw, err := json.Marshal(struct {
		Request manualOrganizeRequest
		Stamp   importSnapshot
	}{in, stamp})
	if err != nil {
		return out, true, err
	}
	key := tokenHash(string(raw))
	parsed := a.parseMediaNameContext(ctx, filepath.Base(path))
	phase("writer_wait")
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, true, err
	}
	defer tx.Rollback()
	phase("active_job")
	var jobID, activeKey string
	err = tx.QueryRowContext(ctx, `SELECT j.id,COALESCE(CASE j.kind WHEN 'organize_file' THEN json_extract(j.payload,'$.id') ELSE json_extract(j.payload,'$.interactive_key') END,'')
		FROM organize_queue_member m JOIN jobs j ON j.id=m.job_id WHERE m.source=? AND j.state IN `+activeOrganizeStates+` LIMIT 1`, path).Scan(&jobID, &activeKey)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, true, err
	}
	if jobID != "" && activeKey != key {
		return out, true, bad("JOB_EXISTS", "该源文件已有其他整理任务正在等待或执行")
	}
	if jobID == "" {
		phase("enqueue")
		batchID := id("scrape_batch_")
		if err = txPut(ctx, tx, "original_scrape_batch", batchID, originalScrapeBatch{ID: batchID, Closed: true}); err != nil {
			return out, true, err
		}
		payload := organizeFilePayload{ID: key, File: importScanFile{Path: path, Size: stamp.Size, Parsed: parsed}, Snapshot: stamp, Title: parsed.Title, BatchID: batchID, Manual: &in}
		job, err := a.enqueueTx(ctx, tx, "organize_file", payload, id("wf_"))
		if err != nil {
			return out, true, err
		}
		jobID = job.ID
	}
	phase("commit")
	if err = in.audit.commit(ctx, tx); err != nil {
		return out, true, err
	}
	a.wakeQueuedJob("organize_file")
	out = manualOrganizeOutput{Items: []manualOrganizeItem{{Source: path, Status: "queued", JobID: jobID}}, Total: 1, Submitted: 1}
	return out, true, nil
}
