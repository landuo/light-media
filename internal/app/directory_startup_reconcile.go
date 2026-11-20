package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A restart can hide a finished download from the monitor pipeline. Two states
// wait forever without it: "baseline" (skipped because the file existed when
// monitoring was enabled) and "tracking" (waiting for a download-completion
// verification that never arrives). A third case is an identity error caused by
// name-based recognition before the download's own media identity existed.
//
// The pass works from the observations the scanner already stored, so a record's
// key always matches the one the scanner will read next. Selection is bounded by
// the source file's modification time; only stat data is read, never content.
const (
	// directoryStartupReconcileWindow is the look-back that covers an overnight
	// restart. Older files stay as the operator left them.
	directoryStartupReconcileWindow = 48 * time.Hour
	// directoryStartupReconcileDelay lets schedulers settle before the pass.
	directoryStartupReconcileDelay = 90 * time.Second
	// directoryStartupRecordLimit bounds how many stored observations per rule
	// are examined.
	directoryStartupRecordLimit = 500
	// directoryStartupSettle keeps the pass away from a file still downloading.
	directoryStartupSettle = 5 * time.Minute
)

type startupReconcileStats struct {
	RanAt      string         `json:"ran_at"`
	Examined   int            `json:"examined"`
	Candidates int            `json:"candidates"`
	Released   int            `json:"released"`
	Skipped    int            `json:"skipped"`
	States     map[string]int `json:"states,omitempty"`
	LastSkip   string         `json:"last_skip,omitempty"`
	Attempts   []string       `json:"attempts,omitempty"`
}

func (a *App) startupReconcileSnapshot() any {
	if stats := a.startupReconcile.Load(); stats != nil {
		return stats
	}
	return nil
}

func directoryStartupReconcileDisabled(lookup func(string) (string, bool)) bool {
	raw, ok := lookup("LIGHT_MEDIA_STARTUP_RECONCILE")
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "off", "0", "false", "disabled":
		return true
	}
	return false
}

// planLive reports whether an import plan can still make progress on its own.
func planLive(state string) bool {
	switch state {
	case "preview", "queued", "running", "executing", "pending":
		return true
	}
	return false
}

// importPlanJobLive reports whether the job driving an import plan can still
// make progress. A plan keeps its live state after its job fails, so the job is
// the only reliable signal.
func (a *App) importPlanJobLive(ctx context.Context, planID string) bool {
	var state string
	err := a.Store.DB.QueryRowContext(ctx,
		`SELECT state FROM jobs WHERE kind='import' AND json_extract(payload,'$.id')=? ORDER BY created_at DESC LIMIT 1`,
		planID).Scan(&state)
	if err != nil {
		return false
	}
	return jobLive(state)
}

// jobLive reports whether a job is still pending or running.
func jobLive(state string) bool {
	switch state {
	case "queued", "running":
		return true
	}
	return false
}

// runDirectoryStartupReconcile releases source files a restart could have
// stranded, then leaves them to the ordinary pipeline: this pass never imports
// anything itself and keeps every history, completeness and stability gate.
func (a *App) runDirectoryStartupReconcile(ctx context.Context) {
	if directoryStartupReconcileDisabled(os.LookupEnv) {
		return
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(directoryStartupReconcileDelay):
	}
	if a.paused() {
		return
	}
	stats := &startupReconcileStats{RanAt: now(), States: map[string]int{}}
	defer func() {
		stats.RanAt = now()
		a.startupReconcile.Store(stats)
	}()
	window := time.Now().Add(-directoryStartupReconcileWindow)
	for _, rule := range a.Settings().Directories {
		if !rule.Enabled || rule.IncludeExisting || !strings.EqualFold(rule.MonitorType, "monitor") {
			continue
		}
		files, err := a.listDirectoryFiles(ctx, rule.ID)
		if err != nil || len(files) == 0 {
			continue
		}
		if len(files) > directoryStartupRecordLimit {
			files = files[:directoryStartupRecordLimit]
		}
		for _, f := range files {
			if ctx.Err() != nil {
				return
			}
			stats.Examined++
			stats.States[f.State]++
			// An observation whose source is gone describes nothing: the file
			// was deleted (or a test file was removed) and the row would keep
			// reporting a pending state forever.
			if _, statErr := os.Stat(f.Path); errors.Is(statErr, fs.ErrNotExist) {
				if err := a.Store.Delete(ctx, "directory_file", f.ID); err == nil {
					stats.Released++
				}
				continue
			}
			if !startupReconcileCandidate(f) {
				continue
			}
			stats.Candidates++
			ok, why := a.releaseStrandedObservation(ctx, rule, f, window)
			if ok {
				stats.Released++
			} else {
				stats.Skipped++
				stats.LastSkip = why
			}
			if len(stats.Attempts) < 12 {
				stats.Attempts = append(stats.Attempts, fmt.Sprintf("%s/%s state=%s => %v %s",
					rule.Name, filepath.Base(f.Path), f.State, ok, why))
			}
		}
	}
	if stats.Released > 0 {
		a.log("INFO", "directory", "startup_reconcile_released",
			fmt.Sprintf("启动补扫释放 %d 个滞留源文件，交由普通整理流程复检", stats.Released), "", "")
	}
}

// startupReconcileCandidate names the states this pass is allowed to repair.
func startupReconcileCandidate(f DirectoryObservedFile) bool {
	switch f.State {
	case "baseline", "tracking":
		return true
	case "error":
		// Identity and reservation failures are now recoverable: the download
		// hint supplies the media identity, and a completed intent can adopt its
		// own reservation. Both verdicts deserve one more attempt.
		return strings.Contains(f.Reason, "影视身份") || strings.Contains(f.Reason, "占用")
	}
	return false
}

// releaseStrandedObservation puts a stranded record back into observation. It
// clears plan and job references only when they can no longer progress, and
// never touches a file that is still being written.
func (a *App) releaseStrandedObservation(ctx context.Context, rule DirectoryRule, f DirectoryObservedFile, window time.Time) (bool, string) {
	// Defence in depth: this is the write path, so it re-checks the state gate
	// instead of trusting the caller's filter.
	if !startupReconcileCandidate(f) {
		return false, "state " + f.State
	}
	info, err := os.Stat(f.Path)
	if err != nil {
		return false, "stat: " + err.Error()
	}
	if info.ModTime().Before(window) {
		return false, "older than window"
	}
	if time.Since(info.ModTime()) < directoryStartupSettle {
		return false, "still settling"
	}
	if f.PlanID != "" {
		var plan ImportPlan
		if err := a.Store.Get(ctx, "import_plan", f.PlanID, &plan); err != nil {
			f.PlanID = ""
		} else if planLive(plan.State) && a.importPlanJobLive(ctx, plan.ID) {
			return false, "live plan " + plan.State
		} else {
			// A live-looking plan whose job already failed can never progress.
			f.PlanID = ""
		}
	}
	if f.JobID != "" {
		var state string
		if err := a.jobStateByID(ctx, f.JobID, &state, nil); err != nil || !jobLive(state) {
			f.JobID = ""
		} else {
			return false, "live job " + state
		}
	}
	stamp, err := snapshotTransferPath(f.Path)
	if err != nil {
		return false, "snapshot: " + err.Error()
	}
	f.State = "observing"
	f.Reason = "启动补扫：源文件已完成但从未进入整理，重新进入识别流程"
	f.StableSince = time.Now().UnixNano()
	f.Snapshot = stamp
	f.LastSeen = now()
	// The scanner addresses observations by rule+path. Writing through f.ID
	// would land on a different row whenever that field is empty or stale
	// (records created by older builds), leaving the real row untouched.
	key := directoryFileID(rule.ID, f.Path)
	if key == "" {
		return false, "empty key"
	}
	f.ID = key
	// Rebuild the row rather than update it: a stale row for the same path can
	// otherwise survive the write and keep answering with the old verdict.
	if err := a.Store.Delete(ctx, "directory_file", key); err != nil {
		return false, "delete failed: " + err.Error()
	}
	if err := a.Store.Put(ctx, "directory_file", key, f); err != nil {
		return false, "write failed: " + err.Error()
	}
	// Read back immediately: a successful Put that does not read back means the
	// scanner overwrote this row between the write and this check.
	var check DirectoryObservedFile
	if err := a.Store.Get(ctx, "directory_file", key, &check); err != nil {
		return false, "readback failed: " + err.Error()
	}
	if check.State != "observing" || check.PlanID != "" || check.JobID != "" {
		return false, "overwritten by scanner state=" + check.State + " plan=" + check.PlanID
	}
	return true, ""
}
