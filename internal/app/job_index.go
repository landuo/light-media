package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

// The dispatch index holds every non-terminal job in memory. Workers claim from
// it instead of scanning the table, so a busy queue costs one primary-key update
// per task and an idle queue costs nothing at all. The row remains the durable
// record used for restart recovery and audit; it is never the thing that decides
// what runs next.
//
// Two invariants keep the index honest:
//   - loading happens before any worker starts, so a row inserted by an older
//     build is claimable;
//   - a claim is confirmed with a conditional update, so a row that was
//     cancelled, retried or removed elsewhere is dropped instead of run twice.
type jobIndex struct {
	mu      sync.Mutex
	entries map[string]Job
	order   []string
}

func (a *App) index() *jobIndex {
	if a.jobIndex == nil {
		a.jobIndex = &jobIndex{entries: map[string]Job{}}
	}
	return a.jobIndex
}

// indexEnqueue registers a job as claimable. It is safe to call for a job whose
// transaction later rolls back: the claim confirms against the row first.
func (a *App) indexEnqueue(j Job) {
	if j.ID == "" || jobTerminal(j.State) {
		return
	}
	ix := a.index()
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, exists := ix.entries[j.ID]; !exists {
		ix.order = append(ix.order, j.ID)
	}
	ix.entries[j.ID] = j
}

// indexRemove forgets a job once it reaches a terminal state.
func (a *App) indexRemove(id string) {
	ix := a.index()
	ix.mu.Lock()
	defer ix.mu.Unlock()
	delete(ix.entries, id)
	for i, candidate := range ix.order {
		if candidate == id {
			ix.order = append(ix.order[:i], ix.order[i+1:]...)
			break
		}
	}
}

// indexRefresh replaces a tracked job's copy without changing its position.
func (a *App) indexRefresh(j Job) {
	if j.ID == "" {
		return
	}
	if jobTerminal(j.State) {
		a.indexRemove(j.ID)
		return
	}
	ix := a.index()
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, exists := ix.entries[j.ID]; !exists {
		ix.order = append(ix.order, j.ID)
	}
	ix.entries[j.ID] = j
}

// indexDepth reports how many jobs are tracked, for the health probe.
func (a *App) indexDepth() int {
	ix := a.index()
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return len(ix.entries)
}

// indexDue returns claimable jobs for a lane in the order the SQL claim used:
// run_at then creation, with the two subscription-download priorities preserved.
func (a *App) indexDue(lane jobLane, at time.Time, limit int) []Job {
	ix := a.index()
	ix.mu.Lock()
	candidates := make([]Job, 0, len(ix.entries))
	for _, j := range ix.entries {
		if j.State != "queued" || j.RunAt > at.Unix() {
			continue
		}
		if laneAllows(lane, j.Kind) {
			candidates = append(candidates, j)
		}
	}
	ix.mu.Unlock()
	sort.SliceStable(candidates, func(i, k int) bool {
		if candidates[i].RunAt != candidates[k].RunAt {
			return candidates[i].RunAt < candidates[k].RunAt
		}
		return candidates[i].CreatedAt < candidates[k].CreatedAt
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

// laneAllows mirrors the kind filter the SQL claim applied per lane.
func laneAllows(lane jobLane, kind string) bool {
	switch kind {
	case "lark_download", "subscription_download":
		return false
	}
	switch lane {
	case subscriptionJobs:
		return kind == "subscription"
	case generalJobs:
		return kind != "subscription" && kind != "import"
	case importJobs:
		return true
	}
	return true
}

func jobTerminal(state string) bool {
	switch state {
	case "succeeded", "failed", "cancelled", "needs_review":
		return true
	}
	return false
}

// loadJobIndex populates the index from rows left by a previous run. Rows a
// crash left running are requeued first so they become claimable again.
func (a *App) loadJobIndex(ctx context.Context) error {
	rows, err := a.Store.DB.QueryContext(ctx,
		`SELECT `+jobColumns+` FROM jobs WHERE state IN ('queued','running') ORDER BY run_at,created_at`)
	if err != nil {
		return err
	}
	defer rows.Close()
	loaded := 0
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return err
		}
		if j.State == "running" {
			j.State = "queued"
		}
		a.indexEnqueue(j)
		loaded++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if loaded > 0 {
		a.log("INFO", "jobs", "index_loaded", "已把待执行任务装入内存调度索引", "", "")
	}
	return nil
}

// subjectBusy replaces the per-enqueue table lookup that refused a second task
// for the same object. The index already knows every pending job, so the check
// is a memory scan instead of a query inside the caller's transaction.
func (a *App) subjectBusy(kind, subjectID string) bool {
	if subjectID == "" {
		return false
	}
	ix := a.index()
	ix.mu.Lock()
	defer ix.mu.Unlock()
	for _, j := range ix.entries {
		if j.Kind != kind || jobTerminal(j.State) {
			continue
		}
		var payload struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(j.Payload, &payload) == nil && payload.ID == subjectID {
			return true
		}
	}
	return false
}

// claimFromIndex picks a candidate from memory and confirms it with a single
// conditional update. A job that is no longer queued in the row (cancelled,
// retried, removed) is dropped from the index rather than executed.
func (a *App) claimFromIndex(ctx context.Context, lane jobLane) (Job, error) {
	at := time.Now()
	candidates := a.indexDue(lane, at, 32)
	if len(candidates) == 0 {
		return Job{}, sql.ErrNoRows
	}
	// When the general lane is about to run organizing work, a subscription
	// download is allowed to go first: it carries the file-safety checks that
	// the rest of the pipeline waits on.
	if lane == generalJobs {
		for _, j := range candidates {
			switch j.Kind {
			case "organize_prepare", "organize_file", "reorganize_cleanup", "quality_cleanup", "scrape", "fnos_sync":
				if download, ok := a.preferSubscriptionDownload(ctx); ok {
					candidates = append([]Job{download}, candidates...)
				}
			}
		}
	}
	for _, j := range candidates {
		if ctx.Err() != nil {
			return Job{}, ctx.Err()
		}
		j.State = "running"
		j.Attempt++
		j.Generation++
		j.UpdatedAt = now()
		result, err := a.Store.DB.ExecContext(ctx,
			`UPDATE jobs SET state=?,attempt=?,generation=?,updated_at=? WHERE id=? AND state='queued'`,
			j.State, j.Attempt, j.Generation, j.UpdatedAt, j.ID)
		if err != nil {
			return Job{}, err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			// Someone else moved this row: forget the stale copy and try the next.
			a.indexRemove(j.ID)
			continue
		}
		a.indexRefresh(j)
		return j, nil
	}
	return Job{}, sql.ErrNoRows
}

// preferSubscriptionDownload finds a queued download that belongs to a
// subscription, if one is waiting.
func (a *App) preferSubscriptionDownload(ctx context.Context) (Job, bool) {
	ix := a.index()
	ix.mu.Lock()
	candidates := make([]Job, 0, 8)
	for _, j := range ix.entries {
		if j.Kind == "download" && j.State == "queued" {
			candidates = append(candidates, j)
		}
	}
	ix.mu.Unlock()
	for _, j := range candidates {
		var payload struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(j.Payload, &payload) != nil || payload.ID == "" {
			continue
		}
		var found string
		err := a.Store.DB.QueryRowContext(ctx,
			`SELECT payload FROM downloadhistory WHERE record_kind='intent' AND id=? LIMIT 1`, payload.ID).Scan(&found)
		if err != nil {
			continue
		}
		var intent struct {
			SubscriptionID string `json:"subscription_id"`
		}
		if json.Unmarshal([]byte(found), &intent) == nil && strings.TrimSpace(intent.SubscriptionID) != "" {
			return j, true
		}
	}
	return Job{}, false
}
