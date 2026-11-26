package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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
	OwnerHash           string         `json:"owner_hash,omitempty"`
	OwnerSize           int64          `json:"owner_size,omitempty"`
}
type directoryBaseline struct {
	Fingerprint string            `json:"fingerprint"`
	LastScan    string            `json:"last_scan"`
	Tasks       map[string]string `json:"tasks,omitempty"`
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
	OwnerHash        string
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
	rows, e := a.Store.DB.QueryContext(ctx, `SELECT payload FROM directory_file WHERE json_extract(payload,'$.directory_id')=? ORDER BY json_extract(payload,'$.last_seen') DESC LIMIT 500`, ruleID)
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
func (a *App) pollDirectoryFileRule(ctx context.Context, rule DirectoryRule) error {
	return a.inspectDirectoryFiles(ctx, rule, nil)
}

// A nil event batch is the periodic reconciliation, including startup baselines
// and missing observations. Native events only inspect the changed paths.
func (a *App) inspectDirectoryFiles(ctx context.Context, rule DirectoryRule, events []directoryWatchEvent) (result error) {
	// A slow downloader or large sweep on another rule must not hold up a
	// close-write on this root. Overlapping rules still use the same ordered
	// ownership check and the atomic source deduplication at enqueue.
	digest := sha256.Sum256([]byte(rule.ID))
	lock := &a.directoryFileMu[int(digest[0])%len(a.directoryFileMu)]
	lock.Lock()
	defer lock.Unlock()
	defer func() {
		if events != nil {
			return
		}
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
	if events != nil && (current.MonitorType != "monitor" || directoryFingerprint(rule) != directoryFingerprint(current)) {
		return nil
	}
	rule = current
	source, e := a.directorySource(rule)
	if e != nil {
		return e
	}
	fingerprint := directoryFingerprint(rule)
	var inspectionError error
	inspectFailed := func(path string, cause error) error {
		// A file can move between enumeration and stat. MP drops that event;
		// other files in the same directory still belong to this scan.
		if errors.Is(cause, fs.ErrNotExist) {
			return nil
		}
		if inspectionError == nil {
			inspectionError = fmt.Errorf("未能核对源文件 %s: %w", filepath.Base(path), cause)
		}
		return a.recordDirectoryInspectionFailure(ctx, rule, fingerprint, path, cause)
	}
	var baseline directoryBaseline
	baselineErr := a.Store.Get(ctx, "directory_file_state", rule.ID, &baseline)
	if baselineErr != nil && !errors.Is(baselineErr, sql.ErrNoRows) {
		return baselineErr
	}
	initial := baseline.Fingerprint != fingerprint
	var files []importScanFile
	closed := map[string]bool{}
	var allObserved map[string]DirectoryObservedFile
	var stored map[string]map[string]directoryDownloadFile
	fingerprints := baseline.Tasks
	if events == nil {
		allObserved, stored, e = a.directoryObservations(ctx, rule.ID)
		if e != nil {
			return e
		}
		unchanged := func(path string, stamp importSnapshot) bool {
			existing, ok := allObserved[path]
			return ok && existing.RuleFingerprint == fingerprint && existing.State != directoryInspectionPending && existing.State != "needs_review" && existing.State != "error" && sameDirectorySnapshot(existing.Snapshot, stamp)
		}
		raw, err := a.scanImportFiles(ctx, source, true, inspectFailed, unchanged, true)
		if err != nil {
			return err
		}
		scan := raw.(map[string]any)
		files = scan["files"].([]importScanFile)
		if truncated, _ := scan["truncated"].(bool); truncated {
			return bad("DIRECTORY_SCAN_LIMIT", "监听目录超过2000个视频或扫描上限，请拆分目录；本轮未执行整理")
		}
	} else {
		for _, event := range events {
			// A later rename at the same path replaces the inode that closed.
			closed[event.path] = event.closed
		}
		files, e = a.directoryEventCandidates(ctx, source, events)
		if e != nil || len(files) == 0 {
			return e
		}
		// Reuse the verified downloader mapping on its next live inventory
		// check. Do not enumerate the monitored filesystem for an event.
		if s := a.settingsSnapshot(); len(s.Downloaders) > 0 {
			allObserved, stored, e = a.directoryObservations(ctx, rule.ID)
			if e != nil {
				return e
			}
		}
	}
	if initial {
		fingerprints, stored = nil, nil
	}
	downloads, taskFingerprints, e := a.directoryDownloadFiles(ctx, rule, source, fingerprints, stored)
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
	// An observation keyed by a BDMV/STREAM member is stale by construction:
	// MoviePilot promotes the event to the disc root before dispatching, and this
	// scanner does the same below. Drop the old rows so the disc is scanned as a
	// unit instead of replaying an m2ts fragment that can never be identified.
	for path, observed := range allObserved {
		if events != nil {
			break
		}
		if !strings.Contains(strings.ToLower(filepath.ToSlash(path)), "/bdmv/stream/") {
			continue
		}
		if err := a.Store.Delete(ctx, "directory_file", observed.ID); err == nil {
			a.log("INFO", "directories", "bluray_stream_observation_dropped", "光盘成员观测已清理，改为按光盘根扫描："+path, "", "")
		}
		delete(allObserved, path)
	}
	settings := a.settingsSnapshot()
	identity := cachedMountedDirectoryIdentity()
	observedAt := time.Now().UTC()
	observedStamp := observedAt.Format(time.RFC3339Nano)
	var heartbeatIDs []string
	for _, file := range files {
		// A Blu-ray is organized as one unit: promote a STREAM member to the disc
		// root so it is recognized by the disc's name instead of an m2ts fragment.
		if root, ok := blurayDiscRoot(file.Path); ok {
			if stamp, err := snapshotTransferPath(root); err == nil {
				file.Path = root
				file.Size = stamp.Size
				file.Parsed = a.parseMediaName(filepath.Base(root))
				file.Snapshot = &stamp
			}
		}
		if file.Snapshot != nil && file.Snapshot.Directory != "" {
			owner, known, e := automaticDiscDownloadOwner(file.Path, *file.Snapshot, downloads)
			if e != nil {
				if err := inspectFailed(file.Path, e); err != nil {
					return err
				}
				continue
			}
			if known {
				downloads[file.Path] = owner
			}
		}
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
		if !directoryRuleOwnsFile(settings, rule, file, identity) {
			key := directoryFileID(rule.ID, file.Path)
			existing, seen := allObserved[file.Path]
			if !seen || existing.State == directoryInspectionPending {
				stamp, snapshotErr := snapshotTransferPath(file.Path)
				if snapshotErr != nil {
					if err := inspectFailed(file.Path, snapshotErr); err != nil {
						return err
					}
					continue
				}
				baseline := DirectoryObservedFile{ID: key, DirectoryID: rule.ID, Path: file.Path, State: "baseline", Reason: "由列表中更靠前的目录规则处理", Snapshot: stamp, StableSince: time.Now().UnixNano(), RuleFingerprint: fingerprint, LastSeen: now()}
				if err := a.Store.Put(ctx, "directory_file", key, baseline); err != nil {
					return err
				}
			}
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if a.paused() {
			return bad("AUTOMATION_PAUSED", "自动化已暂停")
		}
		stamp, e := snapshotTransferPath(file.Path)
		if e != nil {
			// A path may move between enumeration and inspection.
			if e = inspectFailed(file.Path, e); e != nil {
				return e
			}
			continue
		}
		key := directoryFileID(rule.ID, file.Path)
		var f DirectoryObservedFile
		if cached, ok := allObserved[file.Path]; ok && cached.ID == key {
			f = cached
		} else {
			e = a.Store.Get(ctx, "directory_file", key, &f)
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		before := f
		fresh := e != nil || f.RuleFingerprint != fingerprint || f.State == directoryInspectionPending
		changed := !fresh && !sameDirectorySnapshot(f.Snapshot, stamp)

		evidenceJSON, _ := json.Marshal(struct {
			Media     *importInferredMedia
			Numbering *media.Numbering
			Reason    string
		}{file.InferredMedia, file.Numbering, file.IdentityReason})
		evidenceSum := sha256.Sum256(evidenceJSON)
		evidence := hex.EncodeToString(evidenceSum[:])
		// An NFO or download identity can change while the media file stays still.
		// A quiet directory does not generate another recognition event.
		retryIdentity := !fresh && !changed && (f.State == "needs_review" || f.State == "error") &&
			f.EvidenceFingerprint != evidence
		if fresh || changed || retryIdentity {
			f = DirectoryObservedFile{ID: key, DirectoryID: rule.ID, Path: file.Path, State: "observing", Reason: "等待文件至少60秒保持稳定", Snapshot: stamp, StableSince: time.Now().UnixNano(), RuleFingerprint: fingerprint, EvidenceFingerprint: evidence}
			if fresh && initial && events == nil && !rule.IncludeExisting {
				f.State = "baseline"
				f.Reason = "启用监听时已存在，已跳过；需要时从文件管理手动整理"
			}
		}
		if last, err := time.Parse(time.RFC3339, f.LastSeen); err != nil || observedAt.Sub(last) >= 5*time.Minute {
			f.LastSeen = observedStamp
		}
		if f.JobID != "" && f.PlanID == "" {
			var kind, state, planID string
			err := a.Store.DB.QueryRowContext(ctx, `SELECT kind,state,COALESCE(json_extract(payload,'$.id'),'') FROM jobs WHERE id=?`, f.JobID).Scan(&kind, &state, &planID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if kind == "import" {
				f.PlanID = planID
			} else if kind == "organize_file" && state == "cancelled" {
				f.State, f.Reason = "cancelled", "整理任务已取消"
			}
		}
		hasHistory, err := directoryHistoryObservation(ctx, a.Store.DB, &f)
		if err != nil {
			return err
		}
		if !hasHistory && !fresh && !changed && before.PlanID != "" && (before.State == "error" || before.State == "imported") {
			// Old observations can predate deletion receipts. Conversely, a
			// replacement may already have been observed while the old row
			// was visible. Only that new file should go through stability.
			deleted, err := lastDeletedDirectorySnapshot(ctx, a.Store.DB, file.Path)
			if err != nil {
				return err
			}
			if deleted == nil || *deleted == stamp {
				f.retainDeletedHistory()
			} else {
				f.PlanID, f.JobID = "", ""
				f.State, f.Reason = "observing", "等待更换后的源文件稳定"
				f.StableSince = time.Now().UnixNano()
			}
		}
		if !hasHistory && f.State == "queued" {
			// A pending directory job from a prior pass can outlive the row.
			consumed, err := directorySourceHistoryDeleted(ctx, a.Store.DB, file.Path, stamp)
			if err != nil {
				return err
			}
			if consumed {
				f.retainDeletedHistory()
			}
		}
		terminal := f.State == "complete" || f.State == "baseline" || f.State == "imported" || f.State == "queued" || f.State == "cancelled" || f.State == "needs_review" || f.State == "error"
		owner, hasOwner := downloads[file.Path]
		downloadReady := (rule.MonitorType == "downloader" || events != nil && stamp.Directory != "") && hasOwner && owner.Complete && owner.Size == stamp.Size && stamp.Size > 0
		stable := events == nil && !fresh && !changed && time.Since(time.Unix(0, f.StableSince)) >= directoryStableTime
		if stable && !terminal && !downloadReady && stamp.Size > 0 && stamp.Directory == "" {
			stable = directoryFileReady(file.Path, true)
		}
		eventReady := events != nil && file.Snapshot != nil && *file.Snapshot == stamp && stamp.Size > 0 && stamp.Directory == "" && !terminal && directoryFileReady(file.Path, closed[file.Path])
		if !terminal && (downloadReady || stable || eventReady) {
			// A finished managed download that has no plan yet must be organized
			// here: nothing in this build advances download history to
			// "imported", so parking it in "tracking" would wait forever.
			// Organization is driven by the file system, not by provenance: a
			// file that sits complete in a monitored directory is organized no
			// matter which client produced it. The download record only adds the
			// media identity used for recognition, and the ownership hash.
			if hasOwner && (!owner.Complete || owner.Size != stamp.Size) {
				f.State, f.Reason = "downloading", "等待下载器确认文件已完整下载"
			} else if !hasOwner && rule.MonitorType == "downloader" {
				// A downloader-mode rule watches a client's queue rather than the
				// directory, so it still needs the client to confirm the task.
				f.State = "observing"
				f.Reason = "等待下载器确认此目录中的文件已完成"
			} else if stamp.Size <= 0 {
				f.State = "observing"
				f.Reason = "空文件不参与整理"
			} else {
				if file.Unchanged {
					file.Parsed = a.parseMediaName(filepath.Base(file.Path))
					nfoAnchor := importNFOAnchor(file.Path, stamp.Directory != "")
					mediaHint, numbering, reason := a.importNFOEvidence(nfoAnchor)
					if mediaHint != nil {
						file.InferredMedia = mediaHint
						file.IdentitySource = "nfo"
					}
					if numbering != nil {
						file.Numbering = numbering
					}
					if reason != "" {
						file.IdentityReason = reason
					}
				}
				// The download owner is applied unconditionally: it must not depend
				// on the "unchanged" fast path, because a fluctuating identity would
				// change the evidence fingerprint and reset the stability clock on
				// every pass, so the file would never reach the organize step.
				if owner, ok := downloads[file.Path]; ok {
					if owner.IdentityConflict {
						file.IdentityReason = "下载任务的影视身份存在冲突"
					} else if owner.Hint != nil {
						if file.InferredMedia != nil && (file.InferredMedia.ID != owner.Hint.ID || file.InferredMedia.Type != owner.Hint.Type) {
							file.IdentityReason = "下载任务与NFO影视身份存在冲突"
						} else {
							// The acquisition requested this media, so its identity
							// outranks a guess from the file name. A failed name parse
							// must not veto a download we placed ourselves.
							file.InferredMedia = owner.Hint
							file.IdentityReason = ""
							file.IdentitySource = "download"
							if file.Numbering == nil && file.Parsed.Status == media.Parsed {
								file.Numbering = file.Parsed.Original
							}
						}
					}
				}
				f.OwnerHash, f.OwnerSize = owner.OwnerHash, owner.Size
				if e = a.enqueueDirectoryOrganizeFile(ctx, guard, file, &f); e != nil {
					return e
				}
				// Enqueue committed the complete observation with the job.
				continue
			}
		}
		if owner, ok := downloads[file.Path]; ok {
			f.OwnerHash, f.OwnerSize = owner.OwnerHash, owner.Size
		} else {
			f.OwnerHash, f.OwnerSize = "", 0
		}
		if before.LastSeen != f.LastSeen {
			prior := before
			prior.LastSeen = f.LastSeen
			if prior == f {
				heartbeatIDs = append(heartbeatIDs, f.ID)
				continue
			}
		}
		if before != f {
			// The pass reads every observation up front, so a row can change
			// while this loop runs (the startup reconcile releases stranded
			// rows). Writing this copy back whole would silently revert that
			// change, so the stored verdict wins and only harmless fields from
			// this pass are kept.
			var live DirectoryObservedFile
			if err := a.Store.Get(ctx, "directory_file", f.ID, &live); err == nil && live.State != before.State {
				f.State, f.Reason = live.State, live.Reason
				f.PlanID, f.JobID = live.PlanID, live.JobID
				f.StableSince = live.StableSince
			}
			if e = a.Store.Put(ctx, "directory_file", f.ID, f); e != nil {
				return e
			}
		}
	}
	if err := a.touchDirectoryObservations(ctx, heartbeatIDs, observedStamp); err != nil {
		return err
	}
	if inspectionError != nil || events != nil {
		return inspectionError
	}
	// An observation whose source is gone has nothing left to describe. The
	// startup pass already drops these, but a long-running process would keep
	// showing them as pending forever — and they block deleting the parent folder
	// because the row still claims the file is in use.
	for path, observed := range allObserved {
		if _, statErr := os.Stat(path); !errors.Is(statErr, fs.ErrNotExist) {
			continue
		}
		if err := a.Store.Delete(ctx, "directory_file", observed.ID); err == nil {
			a.log("INFO", "directories", "orphan_observation_removed", "源文件已删除，清理观测记录："+path, "", "")
		}
	}
	return a.Store.Put(ctx, "directory_file_state", rule.ID, directoryBaseline{Fingerprint: fingerprint, LastScan: now(), Tasks: taskFingerprints})
}

// Refresh only the heartbeat on the current row. A concurrent source deletion
// or job completion must not be overwritten by the earlier scan snapshot.
func (a *App) touchDirectoryObservations(ctx context.Context, ids []string, at string) error {
	for group := range slices.Chunk(ids, 64) {
		keys, err := json.Marshal(group)
		if err != nil {
			return err
		}
		_, err = a.Store.DB.ExecContext(ctx, `UPDATE directory_file SET payload=CAST(json_set(payload,'$.last_seen',?1) AS BLOB),
 updated_at=CASE WHEN julianday(updated_at)>julianday(?1) THEN updated_at ELSE ?1 END
WHERE id IN (SELECT value FROM json_each(?2)) AND (julianday(last_seen) IS NULL OR julianday(last_seen)<julianday(?1))`, at, string(keys))
		if err != nil {
			return err
		}
	}
	return nil
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
		// The failed plan and its job remain the evidence for this verdict.
		// The ordinary history and retry gates decide when to release them.
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
		if !directoryRuleOwnsFile(a.settingsSnapshot(), rule, importScanFile{Path: file.Source, InferredMedia: &importInferredMedia{Type: plan.Media.MediaType}}, mountedDirectoryIdentity) {
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

func (a *App) directoryDownloadFiles(ctx context.Context, rule DirectoryRule, source string, fingerprints map[string]string, stored map[string]map[string]directoryDownloadFile) (map[string]directoryDownloadFile, map[string]string, error) {
	settings := a.Settings()
	out := map[string]directoryDownloadFile{}
	configs := []DownloaderConfig{}
	if rule.DownloaderID != "" {
		d, e := a.downloader(rule.DownloaderID)
		if e != nil {
			return nil, nil, e
		}
		configs = append(configs, d)
	} else {
		for _, d := range settings.Downloaders {
			if !downloaderEnabled(d) {
				rows, err := a.Store.DB.QueryContext(ctx, `SELECT json_extract(payload,'$.download_path') FROM downloadhistory WHERE json_extract(payload,'$.downloader_id')=? AND json_extract(payload,'$.state') NOT IN ('cancelled','imported')`, d.ID)
				if err != nil {
					return nil, nil, err
				}
				blocked := false
				for rows.Next() {
					var path sql.NullString
					if err = rows.Scan(&path); err != nil {
						rows.Close()
						return nil, nil, err
					}
					if path.String == "" {
						path.String = d.DownloadPath
					}
					mapped, mapErr := mapDownloaderPath(settings, d, path.String)
					if mapErr == nil && (within(source, mapped) || within(mapped, source)) {
						blocked = true
					}
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return nil, nil, err
				}
				if blocked {
					return nil, nil, bad("DOWNLOADER_DISABLED", "此目录有停用下载器的在途任务，启用后继续核对与整理")
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
	next := map[string]string{}
	for _, d := range configs {
		files, updated, err := a.directoryFilesForDownloader(ctx, d, source, rule.MonitorType == "downloader", fingerprints, stored)
		if err != nil {
			return nil, nil, err
		}
		for hash, fingerprint := range updated {
			next[hash] = fingerprint
		}
		for path, entry := range files {
			if old, ok := out[path]; ok {
				entry = mergeDirectoryDownloadFile(entry, old)
			}
			out[path] = entry
		}
	}
	return out, next, nil
}

// directoryObservations loads the persisted per-file observations once; both
// the downloader mapping and the filesystem incremental scan share it.
func (a *App) directoryObservations(ctx context.Context, ruleID string) (map[string]DirectoryObservedFile, map[string]map[string]directoryDownloadFile, error) {
	all := map[string]DirectoryObservedFile{}
	stored := map[string]map[string]directoryDownloadFile{}
	rows, e := a.Store.reader().QueryContext(ctx, `SELECT payload FROM directory_file WHERE directory_id=? ORDER BY rowid`, ruleID)
	if e != nil {
		return nil, nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var b sql.RawBytes
		var f DirectoryObservedFile
		if e = rows.Scan(&b); e != nil {
			return nil, nil, e
		}
		if json.Unmarshal(b, &f) != nil {
			continue
		}
		all[f.Path] = f
		// A disc observation is an aggregate, not a downloader file row. Its
		// readiness must be rebuilt from the actual per-leaf torrent inventory.
		if f.OwnerHash != "" && f.OwnerSize > 0 && f.Snapshot.Directory == "" {
			if stored[f.OwnerHash] == nil {
				stored[f.OwnerHash] = map[string]directoryDownloadFile{}
			}
			stored[f.OwnerHash][f.Path] = directoryDownloadFile{Size: f.OwnerSize, OwnerHash: f.OwnerHash}
		}
	}
	return all, stored, rows.Err()
}

type directoryTaskState struct {
	Files    int
	Imported int
	// A core download records the media it was acquired for. Reusing that
	// identity is what lets a finished download be organized without a second
	// recognition pass, which for an anime name is often ambiguous.
	TMDBID    int64
	MediaType string
}

// directoryDownloadTaskState loads intent/native state for one downloader in
// two queries instead of two queries per task.
func (a *App) directoryDownloadTaskState(ctx context.Context, downloaderID string) (map[string]directoryTaskState, map[string]NativeDownload, error) {
	states := map[string]directoryTaskState{}
	rows, e := a.Store.reader().QueryContext(ctx, `SELECT lower(json_extract(payload,'$.hash')), count(*), coalesce(max(json_extract(payload,'$.state')='imported'),0), coalesce(max(json_extract(payload,'$.request.tmdb_id')),0), coalesce(max(json_extract(payload,'$.request.media_type')),'') FROM downloadhistory WHERE json_extract(payload,'$.downloader_id')=? AND json_extract(payload,'$.state')!='cancelled' GROUP BY 1`, downloaderID)
	if e != nil {
		return nil, nil, e
	}
	for rows.Next() {
		var hash string
		var count, imported int
		var tmdbID int64
		var mediaType string
		if e = rows.Scan(&hash, &count, &imported, &tmdbID, &mediaType); e != nil {
			rows.Close()
			return nil, nil, e
		}
		states[hash] = directoryTaskState{Files: count, Imported: imported, TMDBID: tmdbID, MediaType: mediaType}
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return nil, nil, e
	}
	rows.Close()
	natives := map[string]NativeDownload{}
	nrows, e := a.Store.reader().QueryContext(ctx, `SELECT payload FROM downloadhistory WHERE json_extract(payload,'$.downloader_id')=? AND json_extract(payload,'$.state')!='cancelled' ORDER BY updated_at DESC`, downloaderID)
	if e != nil {
		return nil, nil, e
	}
	defer nrows.Close()
	for nrows.Next() {
		var payload []byte
		if e = nrows.Scan(&payload); e != nil {
			return nil, nil, e
		}
		var native NativeDownload
		if json.Unmarshal(payload, &native) != nil {
			continue
		}
		key := strings.ToLower(native.Hash)
		if _, ok := natives[key]; !ok && key != "" {
			natives[key] = native
		}
	}
	return states, natives, nrows.Err()
}

func directoryTaskFingerprint(t download.Task) string {
	return fmt.Sprintf("%s|%.6f|%d|%d|%t|%s", t.Status, t.Progress, t.Completed, t.TotalSize, t.TransferReady, t.Name)
}

// directoryDownloadHint prefers the recorded native media, then the identity the
// acquisition requested. A core download carries tmdb_id, so a finished file is
// organized against the media it was bought for instead of being re-recognized.
func directoryDownloadHint(native NativeDownload, state directoryTaskState) *importInferredMedia {
	if hint := directoryNativeHint(native); hint != nil {
		return hint
	}
	if validMedia(state.MediaType, state.TMDBID) {
		return &importInferredMedia{Type: state.MediaType, MediaType: state.MediaType, ID: state.TMDBID}
	}
	return nil
}

func directoryNativeHint(native NativeDownload) *importInferredMedia {
	if !validMedia(native.MediaType, native.TMDBID) {
		return nil
	}
	return &importInferredMedia{Type: native.MediaType, MediaType: native.MediaType, ID: native.TMDBID}
}

// directoryFilesForDownloader maps downloader tasks to their files. One task
// fingerprint is persisted per hash; when it is unchanged the per-file
// observations already carry the ownership mapping, so no per-task file
// listing is needed. Only tasks seen and verified on this pass contribute a
// fingerprint. A removed or relocated task must not grow the baseline forever.
func (a *App) directoryFilesForDownloader(ctx context.Context, d DownloaderConfig, source string, transferPoll bool, fingerprints map[string]string, stored map[string]map[string]directoryDownloadFile) (map[string]directoryDownloadFile, map[string]string, error) {
	settings := a.Settings()
	out := map[string]directoryDownloadFile{}
	next := map[string]string{}
	client, e := download.New(d.Config)
	if e != nil {
		return nil, nil, e
	}
	tasks, e := client.List(ctx)
	if e != nil {
		return nil, nil, bad("DIRECTORY_DOWNLOADER_UNAVAILABLE", "无法核对下载器文件是否完成，本轮不执行目录整理")
	}
	var states map[string]directoryTaskState
	var natives map[string]NativeDownload
	for _, t := range tasks {
		if transferPoll && (!t.TransferReady || slices.Contains(t.Tags, "已整理")) {
			continue
		}
		sharedPath := ""
		if d.SharedPaths {
			path := cleanDirectoryInput(t.DownloadDir)
			if err := safeRoot(path); err != nil {
				if within(source, path) || within(path, source) {
					return nil, nil, bad("DIRECTORY_PATH_UNVERIFIED", "相关下载任务返回不规范保存目录，本轮未整理")
				}
				continue
			}
			var relevant bool
			sharedPath, relevant = sharedDirectoryPath(source, path)
			if !relevant && len(d.Mappings) > 0 {
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
		mapped, e := mapDownloaderPath(settings, d, pathToCheck)
		if e != nil {
			if within(source, t.DownloadDir) || within(t.DownloadDir, source) {
				return nil, nil, bad("DIRECTORY_PATH_UNVERIFIED", "下载器路径不在应用容器可见目录内，本轮未整理")
			}
			continue
		}
		if !within(source, mapped) && !within(mapped, source) {
			continue
		}
		// Load history once, only if a task passes the directory checks.
		if states == nil {
			states, natives, e = a.directoryDownloadTaskState(ctx, d.ID)
			if e != nil {
				return nil, nil, e
			}
		}
		hash := strings.ToLower(t.Hash)
		state := states[hash]
		native := natives[hash]
		entries := stored[hash]
		count := 0
		if entries != nil {
			count = len(entries)
		}
		fingerprint := directoryTaskFingerprint(t) + "|" + strconv.Itoa(count)
		hint := directoryDownloadHint(native, state)
		cacheable := true
		ready := t.TransferReady || (!transferPoll && t.Status == "paused" && directoryTaskComplete(t))
		if fingerprints[hash] != fingerprint || entries == nil {
			files, err := client.Files(ctx, t.Hash)
			if err != nil {
				if ctx.Err() != nil {
					return nil, nil, ctx.Err()
				}
				a.log("WARN", "directories", "directory_files_unavailable", "无法读取部分下载任务文件，已跳过："+t.Hash, "", "")
				continue
			}
			entries = map[string]directoryDownloadFile{}
			for _, f := range files {
				if !filepath.IsLocal(filepath.FromSlash(f.Path)) {
					return nil, nil, bad("DIRECTORY_UNSAFE_DOWNLOADER_PATH", "下载器返回异常文件路径")
				}
				fileRoot := t.DownloadDir
				if sharedPath != "" {
					fileRoot = sharedPath
				}
				candidate := filepath.Join(fileRoot, filepath.FromSlash(f.Path))
				if d.SharedPaths && !within(source, candidate) {
					continue
				}
				path, err := mapDownloaderPath(settings, d, candidate)
				if err != nil {
					return nil, nil, err
				}
				if !within(source, path) {
					continue
				}
				entry := directoryDownloadFile{Complete: ready && f.Size > 0 && f.Completed >= f.Size && f.Progress >= 1, Managed: state.Files > 0, Imported: state.Imported > 0, Size: f.Size, OwnerHash: hash}
				entry.Hint = hint
				// A torrent may be complete while a skipped file still lacks
				// bytes. A cached task total cannot certify that file later.
				if ready && !entry.Complete {
					cacheable = false
				}
				if old, ok := entries[path]; ok {
					entry = mergeDirectoryDownloadFile(entry, old)
				}
				entries[path] = entry
			}
			fingerprint = directoryTaskFingerprint(t) + "|" + strconv.Itoa(len(entries))
		} else {
			for path, entry := range entries {
				entry.Managed = state.Files > 0
				entry.Imported = state.Imported > 0
				entry.Complete = ready && entry.Size > 0
				entry.Hint = hint
				entries[path] = entry
			}
		}
		if cacheable {
			next[hash] = fingerprint
		}
		for path, entry := range entries {
			if old, ok := out[path]; ok {
				entry = mergeDirectoryDownloadFile(entry, old)
			}
			out[path] = entry
		}
	}
	return out, next, nil
}

func directoryTitle(s string) string {
	return media.NormalizeTitle(s)
}

var directoryTVReleaseYear = regexp.MustCompile(`^(.+) ((?:19|20)[0-9]{2})$`)

// TmdbApi.__search_tv_by_name sorts by descending first_air_date and takes the
// first exact title/original_name match. Equal dates retain TMDB response order.
func directoryFirstTVTitle(results []catalog.Media, query string, year int) *catalog.Media {
	var best *catalog.Media
	for i := range results {
		m := &results[i]
		if m.MediaType != "tv" || m.ID <= 0 || year > 0 && m.Year != year {
			continue
		}
		if !originalHostMediaCompareNames(query, m.Title) && !originalHostMediaCompareNames(query, m.OriginalTitle) {
			continue
		}
		// Older compact rows can carry only the year.
		if best == nil || m.Year > best.Year || m.Year == best.Year && m.ReleaseDate > best.ReleaseDate {
			best = m
		}
	}
	return best
}

func (a *App) directoryTitleMatches(ctx context.Context, kind string, year int, names []string, preferTVPremiere bool) (map[int64]catalog.Media, error) {
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
			if preferTVPremiere && number == 1 && page.Page == 1 && page.TotalPages >= 1 && time.Since(at) < 24*time.Hour {
				if m := directoryFirstTVTitle(page.Results, query, year); m != nil {
					return map[int64]catalog.Media{m.ID: *m}, nil
				}
			}
			// Stale search fallback is useful for browsing, but cannot prove
			// uniqueness when recognition requires the complete result set.
			if time.Since(at) >= 24*time.Hour || page.Page != number || page.TotalPages < 1 || page.TotalPages > 5 || number > 1 && (page.TotalPages != totalPages || page.TotalResults != totalResults) || page.TotalPages > 1 && len(page.Results) == 0 {
				return nil, bad("DIRECTORY_IDENTITY_REVIEW", "影视搜索分页不完整或结果过多，请在“手动整理”中选择电影或电视剧类型，并查询或填写TheMovieDb编号")
			}
			if number == 1 {
				totalPages, totalResults = page.TotalPages, page.TotalResults
			}
			// A site names a release after the show's English title while TMDB
			// answers with the localized one, so an exact comparison can never match.
			// A single unambiguous result for the parsed title and year is accepted;
			// ambiguity still requires the operator to choose.
			// MoviePilot narrows the search by media type and year, then accepts the
			// single survivor (__choose_media_infos). Requiring the API itself to
			// return one row rejected names that a year filter already disambiguates.
			var survivors []catalog.Media
			for _, m := range page.Results {
				if m.MediaType != kind || year > 0 && year != m.Year {
					continue
				}
				survivors = append(survivors, m)
			}
			if len(survivors) == 1 {
				matches[survivors[0].ID] = survivors[0]
				continue
			}
			for _, m := range survivors {
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
	year := strconv.Itoa(p.Year)
	names := append([]string{p.Title}, p.AlternateTitles...)
	if p.Year == 0 {
		release := directoryTVReleaseYear.FindStringSubmatch(p.Title)
		if release == nil {
			return nil, nil
		}
		names, year = []string{release[1]}, release[2]
	}
	// A release may use the season's year instead of the show's premiere.
	matches, err := a.directoryTitleMatches(ctx, "tv", 0, names, false)
	if err != nil || p.Year == 0 && len(matches) <= 1 {
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
			if season.SeasonNumber == *p.Original.Season && strings.HasPrefix(season.AirDate, year+"-") {
				seasonMatches[id] = candidate
				break
			}
		}
	}
	return seasonMatches, nil
}

func (a *App) directoryFileIdentity(ctx context.Context, rule DirectoryRule, file importScanFile) (ImportRequest, error) {
	request := ImportRequest{RootID: rule.RootID, Mode: rule.Mode, Renaming: rule.Renaming, Scraping: rule.Scraping, LibraryTypeFolder: rule.LibraryTypeFolder, WorkflowID: id("wf_"), Files: []ImportFileRequest{{Path: file.Path}}}
	if file.originalDirectoryPlanned {
		request.originalDirectoryPlanned = true
		request.Files[0].originalItem = file.originalItem
		request.Files[0].originalMetadataPath = file.Path
	}
	if rule.ID != "" {
		copyRule := rule
		request.directoryRule = &copyRule
	}
	var numbering *media.Numbering
	if file.IdentityReason != "" {
		return request, bad("DIRECTORY_IDENTITY_REVIEW", "NFO身份存在疑问，请在“手动整理”中选择电影或电视剧类型，并查询或填写TheMovieDb编号")
	}
	hint := file.InferredMedia
	if file.isOriginalDisc() && (hint == nil || hint.Type == "tv" && file.Numbering == nil) {
		parsed, err := a.discReleaseIdentity(ctx, file.Path, file.originalResolvedMeta)
		if err != nil {
			return request, err
		}
		file.Parsed = parsed
		if file.Numbering == nil {
			file.Numbering = parsed.Original
		}
	}
	if hint != nil {
		request.MediaType = hint.Type
		request.TMDBID = hint.ID
		numbering = file.Numbering
	} else {
		p := file.Parsed
		if file.isOriginalDisc() && (p.Title == "" || p.Type == "movie" && p.Year == 0) {
			return request, bad("DIRECTORY_IDENTITY_REVIEW", "无法从蓝光原盘目录名确定影视身份，请在“手动整理”中选择影视类型并填写对应的 TMDB 编号。电视剧还需指定季号，集号可留空")
		}
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
		if source := a.effectiveRecognitionSource(); source != "themoviedb" {
			return a.directoryProviderIdentity(ctx, request, file, source)
		}
		matches, err := a.directoryTitleMatches(ctx, kind, p.Year, append([]string{p.Title}, p.AlternateTitles...), kind == "tv")
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
		if err := file.validateImportNumbering(numbering); err != nil {
			return request, err
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
