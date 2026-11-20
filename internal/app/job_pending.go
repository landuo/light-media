package app

import (
	"database/sql"
	"encoding/json"
	"sync"
	"time"
)

// A task that must not become visible before its business write commits is
// registered against the transaction that produced it. The caller releases the
// tasks right after a successful commit, so the work appears exactly when the
// state it depends on is durable — without a jobs row to store it in.
//
// If a caller forgets to release (or the process dies between commit and
// release), the work is not lost: every migrated kind is also re-derivable from
// its business state by the reconcile sweep.
type pendingJobs struct {
	mu   sync.Mutex
	byTx map[*sql.Tx][]Job
}

func (a *App) notePendingJob(tx *sql.Tx, j Job) {
	if tx == nil || j.ID == "" {
		return
	}
	a.pendingJobs.mu.Lock()
	defer a.pendingJobs.mu.Unlock()
	if a.pendingJobs.byTx == nil {
		a.pendingJobs.byTx = map[*sql.Tx][]Job{}
	}
	// Rolled-back transactions leave their entry behind (no hook fires on
	// rollback), so bound the map instead of letting it grow without limit.
	if len(a.pendingJobs.byTx) > 256 {
		a.pendingJobs.byTx = map[*sql.Tx][]Job{}
	}
	a.pendingJobs.byTx[tx] = append(a.pendingJobs.byTx[tx], j)
}

// commitWithJobs commits and then publishes whatever the transaction queued.
func (a *App) commitWithJobs(tx *sql.Tx) error {
	if tx == nil {
		return nil
	}
	a.pendingJobs.mu.Lock()
	pending := a.pendingJobs.byTx[tx]
	delete(a.pendingJobs.byTx, tx)
	a.pendingJobs.mu.Unlock()
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, j := range pending {
		a.indexEnqueue(j)
	}
	if len(pending) > 0 {
		select {
		case a.jobWake <- struct{}{}:
		default:
		}
	}
	return nil
}

// dropPendingJobs forgets work registered for a rolled-back transaction.
func (a *App) dropPendingJobs(tx *sql.Tx) {
	if tx == nil {
		return
	}
	a.pendingJobs.mu.Lock()
	delete(a.pendingJobs.byTx, tx)
	a.pendingJobs.mu.Unlock()
}

// ephemeralPayloadTask builds a rowless job for a kind that is re-derivable from
// business state. Used by the enqueue helper and by the reconcile sweep.
func newRowlessJob(kind string, payload any, wf string) (Job, bool) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Job{}, false
	}
	j := Job{ID: id("job_"), WorkflowID: wf, Kind: kind, State: "queued", Payload: b,
		RunAt: time.Now().Unix(), CreatedAt: now(), UpdatedAt: now()}
	j.Ephemeral = true
	return j, true
}
