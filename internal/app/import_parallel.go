package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"
)

type importSlot struct {
	media           string
	season, episode int
}

type importAdmission struct {
	app          *App
	job, plan    string
	intent       string
	exclusive    bool
	scanVersions bool
	slots        map[importSlot]struct{}
	paths        map[string]struct{}
	sources      map[string]struct{}
	targets      map[string]struct{}
}

type importAdmissionKey struct{}

func parallelImport(ctx context.Context) *importAdmission {
	admission, _ := ctx.Value(importAdmissionKey{}).(*importAdmission)
	return admission
}

func (run *importAdmission) release() {
	run.app.importAdmissionsMu.Lock()
	defer run.app.importAdmissionsMu.Unlock()
	if run.app.importAdmissions[run.job] == run {
		delete(run.app.importAdmissions, run.job)
	}
}

// Claim in queue order, but skip jobs whose scope is busy. A younger job
// may only pass a waiting job when it also has no conflict with that job.
func (a *App) claimImportJob(ctx context.Context) (Job, error) {
	a.importAdmissionsMu.Lock()
	defer a.importAdmissionsMu.Unlock()
	at := time.Now()
	jobs, err := a.queryJobs(ctx, `SELECT `+jobColumns+` FROM jobs
 WHERE kind='import' AND state IN ('queued','retry_wait')
 ORDER BY created_at,id`)
	if err != nil {
		return Job{}, err
	}
	if len(jobs) == 0 {
		return Job{}, sql.ErrNoRows
	}
	var waiting []*importAdmission
	native := a.canFreezeNativeImport()
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return Job{}, err
		}
		run := a.importJobScope(ctx, job, native)
		if job.RunAt > at.Unix() {
			// A deferred retry keeps its place for the same known paths and
			// slots without holding an import worker or blocking unrelated work.
			if !run.exclusive {
				waiting = append(waiting, run)
			}
			continue
		}
		busy := false
		for _, active := range a.importAdmissions {
			if run.conflicts(active) {
				busy = true
				break
			}
		}
		for _, older := range waiting {
			if run.conflicts(older) {
				busy = true
				break
			}
		}
		if busy {
			if run.exclusive {
				break // Let active work drain before an exclusive operation.
			}
			waiting = append(waiting, run)
			continue
		}
		job.UpdatedAt, job.LeaseUntil = now(), at.Add(10*time.Minute).Unix()
		result, err := a.Store.DB.ExecContext(ctx, `UPDATE jobs
 SET state='running',attempt=attempt+1,generation=generation+1,lease_until=?,updated_at=?
 WHERE id=? AND kind='import' AND generation=? AND state=? AND run_at<=?`,
			job.LeaseUntil, job.UpdatedAt, job.ID, job.Generation, job.State, at.Unix())
		if err != nil {
			return Job{}, err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return Job{}, err
		}
		if changed == 0 {
			continue
		}
		job.State, job.Attempt, job.Generation = "running", job.Attempt+1, job.Generation+1
		if a.importAdmissions == nil {
			a.importAdmissions = make(map[string]*importAdmission)
		}
		a.importAdmissions[job.ID] = run
		job.importRun = run
		return job, nil
	}
	return Job{}, sql.ErrNoRows
}

func (a *App) importJobScope(ctx context.Context, job Job, native bool) *importAdmission {
	run := &importAdmission{app: a, job: job.ID, exclusive: true}
	if !native {
		return run
	}
	var input struct {
		ID      string   `json:"id"`
		FileIDs []string `json:"file_ids"`
	}
	var plan ImportPlan
	var meta importMetadata
	if json.Unmarshal(job.Payload, &input) != nil || input.ID == "" ||
		a.Store.Get(ctx, "import_plan", input.ID, &plan) != nil || plan.ID != input.ID ||
		a.Store.Get(ctx, "import_meta", plan.ID, &meta) != nil || meta.Reorganize != nil || plan.ReorganizeAssetID != "" {
		return run
	}
	var special bool
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM records WHERE kind='subscription_quality_import' AND id=?1)
 OR EXISTS(SELECT 1 FROM records WHERE kind='directory_overwrite' AND id>=?2 AND id<?3)
 OR EXISTS(SELECT 1 FROM records WHERE kind='import_receipt' AND id>=?2 AND id<?3 AND json_extract(payload,'$.original_module_started')=1)`,
		plan.ID, plan.ID+"/", plan.ID+"0").Scan(&special); err != nil || special {
		return run
	}
	indices, err := selectImportFiles(plan, input.FileIDs)
	if err != nil {
		return run
	}
	run.plan, run.intent, run.scanVersions = plan.ID, plan.IntentID, importScansVersions(meta)
	run.slots, run.paths = make(map[importSlot]struct{}), make(map[string]struct{})
	run.sources, run.targets = make(map[string]struct{}), make(map[string]struct{})
	for _, index := range indices {
		file := plan.Files[index]
		if file.DirectoryDigest != "" || !run.addFile(file.Source, file.Target, meta) {
			return run
		}
		for _, episode := range importEpisodes(file.Episodes) {
			run.slots[importSlot{mediaKey(plan.Media), file.Season, episode}] = struct{}{}
		}
		if base, ok := meta.OriginalNamingBases[file.ID]; ok {
			target, err := a.originalTransferNativeRename(ctx, plan.Media, file, base, meta.OriginalTransfer[file.ID], nil, false)
			if err != nil || !run.addTarget(target, meta) {
				return run
			}
		}
		for i, side := range meta.Sidecars[file.ID] {
			if !run.addFile(side.Source, side.Target, meta) {
				return run
			}
			if side.Original != nil && side.NamingBase != "" {
				extra := originalTransferSideFile(file, side, i)
				target, err := a.originalTransferNativeRename(ctx, plan.Media, extra, side.NamingBase, *side.Original, nil, false)
				if err != nil || !run.addTarget(target, meta) {
					return run
				}
			}
		}
	}
	if meta.Overwrite != nil {
		// A same-path managed video replacement can transfer ownership of
		// the old asset's metadata and sidecars beyond the new file plan.
		hashes := make([]string, 0, len(run.targets))
		for target := range run.targets {
			hashes = append(hashes, tokenHash(target))
		}
		raw, _ := json.Marshal(hashes)
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM records WHERE kind='managed_file'
 AND id IN (SELECT value FROM json_each(?)))`, raw).Scan(&special); err != nil || special {
			return run
		}
	}
	run.exclusive = false
	return run
}

func (run *importAdmission) addFile(source, target string, meta importMetadata) bool {
	if !run.addPath(source) || !run.addTarget(target, meta) {
		return false
	}
	run.sources[source] = struct{}{}
	return true
}

func (run *importAdmission) addTarget(path string, meta importMetadata) bool {
	if !within(meta.RootPath, path) || !run.addPath(path) {
		return false
	}
	run.targets[path] = struct{}{}
	return !importScansVersions(meta) || run.addPath(filepath.Dir(path))
}

func importScansVersions(meta importMetadata) bool {
	return meta.Overwrite != nil && (!meta.Overwrite.Policy.Known || meta.Overwrite.Policy.Mode != nil && *meta.Overwrite.Policy.Mode == "latest")
}

func (run *importAdmission) covers(plan ImportPlan, meta importMetadata, indices []int) bool {
	if plan.ID != run.plan || plan.IntentID != run.intent || meta.Reorganize != nil || plan.ReorganizeAssetID != "" || importScansVersions(meta) != run.scanVersions {
		return false
	}
	hasFile := func(source, target string) bool {
		_, sourceOK := run.sources[source]
		_, targetOK := run.targets[target]
		return sourceOK && targetOK
	}
	for _, index := range indices {
		file := plan.Files[index]
		if file.DirectoryDigest != "" || !hasFile(file.Source, file.Target) {
			return false
		}
		for _, episode := range importEpisodes(file.Episodes) {
			if _, ok := run.slots[importSlot{mediaKey(plan.Media), file.Season, episode}]; !ok {
				return false
			}
		}
		for _, side := range meta.Sidecars[file.ID] {
			if !hasFile(side.Source, side.Target) {
				return false
			}
		}
	}
	return true
}

func guardParallelOverwrite(ctx context.Context, op directoryOverwriteOperation) error {
	run := parallelImport(ctx)
	if run == nil {
		return nil
	}
	_, sourceOK := run.sources[op.Source]
	_, targetOK := run.targets[op.Target]
	if !sourceOK || !targetOK {
		return bad("REVIEW_IMPORT_SCOPE_CHANGED", "覆盖收据的路径已超出当前整理范围")
	}
	for _, old := range op.Targets {
		if old.Path == op.Target && old.Owner != nil {
			return bad("REVIEW_IMPORT_SCOPE_CHANGED", "同路径受管文件替换需要独占整理")
		}
		if old.Path != op.Target && (!run.scanVersions || !op.Decision.DeleteVersions || filepath.Dir(old.Path) != filepath.Dir(op.Target)) {
			return bad("REVIEW_IMPORT_SCOPE_CHANGED", "覆盖旧文件已超出当前整理范围")
		}
	}
	return nil
}

// Keep lexical paths and identities rooted at each existing ancestor. The
// same file can appear through multiple NAS bind mounts, even when its target
// and target parent do not exist yet.
func (run *importAdmission) addPath(path string) bool {
	if !importPath(path) {
		return false
	}
	run.paths["/path"+path] = struct{}{}
	for ancestor := path; ; ancestor = filepath.Dir(ancestor) {
		info, err := os.Lstat(ancestor)
		if err == nil {
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || info.Mode()&os.ModeSymlink != 0 || ancestor != path && !info.IsDir() {
				return false
			}
			relative, err := filepath.Rel(ancestor, path)
			if err != nil {
				return false
			}
			run.paths[filepath.Join(fmt.Sprintf("/inode/%d/%d", stat.Dev, stat.Ino), relative)] = struct{}{}
		} else if !os.IsNotExist(err) {
			return false
		}
		if ancestor == filepath.Dir(ancestor) {
			return true
		}
	}
}

func (run *importAdmission) conflicts(other *importAdmission) bool {
	if run.exclusive || other.exclusive || run.plan == other.plan || run.intent != "" && run.intent == other.intent {
		return true
	}
	for slot := range run.slots {
		if _, exists := other.slots[slot]; exists {
			return true
		}
	}
	return importPathScopesOverlap(run.paths, other.paths) || importPathScopesOverlap(other.paths, run.paths)
}

func importPathScopesOverlap(paths, other map[string]struct{}) bool {
	for path := range paths {
		for ancestor := path; ancestor != "/"; ancestor = filepath.Dir(ancestor) {
			if _, exists := other[ancestor]; exists {
				return true
			}
		}
	}
	return false
}

// Mutating providers are selected at admission. Changes to the plugin
// registry apply to later jobs; observers can still receive ordinary events.
func (a *App) canFreezeNativeImport() bool {
	for _, object := range a.originalLoadedPluginSnapshot() {
		if slices.Contains(object.manifest.Methods, "get_module") {
			return false
		}
	}
	a.pluginOriginalEventsMu.Lock()
	defer a.pluginOriginalEventsMu.Unlock()
	for name, handlers := range a.pluginOriginalChainHandlers {
		if len(handlers) != 0 && importMutationEvent(name) {
			return false
		}
	}
	return true
}

func importMutationEvent(name string) bool {
	switch name {
	case "transfer.rename.build", "transfer.rename", "transfer.intercept", "transfer.overwrite.check", "storage.operation":
		return true
	}
	return false
}
