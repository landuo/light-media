package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// The capability is host-private and never serialized into worker input.
type pluginExecutionLease struct {
	releaseMu     sync.Mutex
	unenteredOnce sync.Once
	unenteredErr  error
	store         *PluginStore
	ticket        *pluginRunTicket
	claim         *pluginRunClaimAttempt
	mu            sync.Mutex
	cleanup       func()
	expected      *pluginRunAttemptResult
	released      bool
}
type pluginExecutionLeaseContextKey struct{}

func (p *PluginStore) beginSupervisedClaim(ctx context.Context) (pluginRunClaimWindow, error) {
	if err := p.checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return pluginRunClaimWindow{}, err
	}
	if err := ctx.Err(); err != nil {
		return pluginRunClaimWindow{}, err
	}
	return p.runSupervisor.beginClaim()
}
func (p *PluginStore) reserveSupervisedClaimTx(ctx context.Context, tx *sql.Tx, window pluginRunClaimWindow, run PluginRun) (*pluginExecutionLease, error) {
	var pluginID string
	if err := tx.QueryRowContext(ctx, `SELECT plugin_id FROM plugin_installations WHERE instance_id=?`, run.InstanceID).Scan(&pluginID); err != nil {
		return nil, err
	}
	admission := pluginRunAdmission{InstanceID: run.InstanceID, PluginID: pluginID, Version: run.Version, RunID: run.ID, Action: run.Action, Generation: run.Generation, AdmissionRevision: run.ConfigRevision, InputSHA: sha256.Sum256(run.Input)}
	claim, err := p.runJournal.prepare(admission)
	if err != nil {
		return nil, err
	}
	ticket, err := p.runSupervisor.reserve(ctx, window, admission)
	if err != nil {
		return nil, err
	}
	lease := &pluginExecutionLease{store: p, ticket: ticket, claim: claim}
	p.runSupervisor.mu.Lock()
	ticket.attempt.lease = lease
	p.runSupervisor.mu.Unlock()
	if budget, _ := ctx.Value(brushFlowClaimBudgetKey{}).(*brushFlowClaimBudget); budget != nil {
		lease.cleanup = budget.release
		budget.release = nil
	}
	if err = p.runJournal.recordClaimTx(ctx, tx, claim); err != nil {
		return lease, err
	}
	return lease, nil
}
func (l *pluginExecutionLease) rollbackClaim() error {
	if l == nil {
		return nil
	}
	if err := l.ticket.recordClaimAborted(l.claim.admission); err != nil {
		return err
	}
	l.runCleanup()
	return l.ticket.Done(l.claim.admission, pluginRunRollbackComplete)
}
func (l *pluginExecutionLease) runCleanup() {
	l.mu.Lock()
	f := l.cleanup
	l.cleanup = nil
	l.mu.Unlock()
	if f != nil {
		f()
	}
}
func (p *PluginStore) commitSupervisedClaim(ctx context.Context, l *pluginExecutionLease, commitErr error) error {
	if l == nil || l.store != p {
		return errPluginRunAttemptEvidence
	}
	if commitErr != nil {
		repair, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if _, err := p.runJournal.resolveClaim(repair, l.claim); err != nil {
			_ = l.ticket.quarantineClaim(l.claim.admission)
			l.runCleanup()
			return errors.Join(commitErr, err)
		}
	}
	if err := l.ticket.recordClaimCommitted(l.claim.admission); err != nil {
		return err
	}
	finished, err := p.finishCanceledUnenteredAttempt(ctx, l.ticket.attempt)
	if err != nil {
		return err
	}
	if finished {
		return errPluginRunCanceled
	}
	return nil
}
func (p *PluginStore) activateSupervisedRun(ctx context.Context, run PluginRun) (context.Context, error) {
	l := run.executionLease
	if l == nil || l.store != p || l.claim.admission.RunID != run.ID || l.claim.admission.InstanceID != run.InstanceID {
		return ctx, errPluginRunAttemptEvidence
	}
	active, err := l.ticket.activate(l.claim.admission)
	if err != nil {
		return context.WithValue(ctx, pluginExecutionLeaseContextKey{}, l), err
	}
	// Preserve cancellation from both the captured claim and this executor caller.
	joined, cancel := context.WithCancel(active)
	stop := context.AfterFunc(ctx, cancel)
	l.mu.Lock()
	old := l.cleanup
	l.cleanup = func() {
		stop()
		cancel()
		if old != nil {
			old()
		}
	}
	l.mu.Unlock()
	return context.WithValue(joined, pluginExecutionLeaseContextKey{}, l), nil
}
func (p *PluginStore) supervisedLease(ctx context.Context, runID string) (*pluginExecutionLease, error) {
	l, _ := ctx.Value(pluginExecutionLeaseContextKey{}).(*pluginExecutionLease)
	if l != nil && (l.store != p || l.claim == nil || l.claim.admission.RunID != runID) {
		return nil, errPluginRunAttemptEvidence
	}
	return l, nil
}
func (p *PluginStore) requireSupervisedFinishTx(ctx context.Context, tx *sql.Tx, runID string) (*pluginExecutionLease, error) {
	l, err := p.supervisedLease(ctx, runID)
	if err != nil {
		return nil, err
	}
	if l == nil {
		var n int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_run_attempt_heads WHERE run_id=?`, runID).Scan(&n); err != nil {
			return nil, err
		}
		if n != 0 {
			return nil, errPluginRunAttemptEvidence
		}
		return nil, nil
	}
	_, err = p.runJournal.requireCurrentTx(ctx, tx, l.claim)
	return l, err
}
func (p *PluginStore) recordSupervisedCompletionTx(ctx context.Context, tx *sql.Tx, l *pluginExecutionLease, runID string) error {
	if l == nil {
		return nil
	}
	run, err := p.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, runID))
	if err != nil {
		return err
	}
	expected, err := newPluginRunAttemptResult(run.State, run.ErrorCode, run.Result)
	if err != nil {
		return err
	}
	if err = p.runJournal.recordCompletionTx(ctx, tx, l.claim, expected); err != nil {
		return err
	}
	// A retained expected digest is intent, not proof. release always reads back.
	l.mu.Lock()
	l.expected = &expected
	l.mu.Unlock()
	return nil
}
func (p *PluginStore) releaseSupervisedRun(run PluginRun) error {
	l := run.executionLease
	if l == nil || l.store != p {
		return errPluginRunAttemptEvidence
	}
	l.releaseMu.Lock()
	defer l.releaseMu.Unlock()
	l.mu.Lock()
	if l.released {
		l.mu.Unlock()
		return nil
	}
	expected := l.expected
	l.mu.Unlock()
	l.runCleanup()
	if expected == nil {
		return errPluginRunUnresolved
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := p.runJournal.resolveCompletion(ctx, l.claim, *expected); err != nil {
		return err
	}
	if err := l.ticket.Done(l.claim.admission, pluginRunDurableCompletion); err != nil {
		return err
	}
	l.mu.Lock()
	l.released = true
	l.mu.Unlock()
	return nil
}

// Only called before an SDK or deferred business owner is entered. The private
// attempt capability is mandatory; cancellation alone is never completion.
func (p *PluginStore) finalizeSupervisedUnentered(ctx context.Context, run PluginRun) error {
	l := run.executionLease
	if l == nil || l.store != p {
		return errPluginRunAttemptEvidence
	}
	l.unenteredOnce.Do(func() {
		ctx = context.WithValue(ctx, pluginExecutionLeaseContextKey{}, l)
		if l.claim.admission.PluginID == "BrushFlowLanduo" {
			l.unenteredErr = p.brushFlowCancelBeforeSDK(ctx, run)
		} else {
			l.unenteredErr = p.finishRun(ctx, run.ID, "failed", "PLUGIN_RUN_CANCELED", json.RawMessage(`null`), false)
		}
	})
	return l.unenteredErr
}

// Empty memory after restart is not evidence of a prior attempt's outcome.
// The lifecycle caller owns this transaction and must retain all journal rows.
func (p *PluginStore) requireResetJournalQuiescenceTx(ctx context.Context, tx *sql.Tx, instanceID string) error {
	var legacyUnknown bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plugin_runs r WHERE instance_id=? AND (state IN ('running','reconcile_pending') OR (state='pending' AND error_code='PLUGIN_INTERRUPTED')) AND NOT EXISTS(SELECT 1 FROM plugin_run_attempts a WHERE a.run_id=r.id))`, instanceID).Scan(&legacyUnknown); err != nil {
		return err
	}
	if legacyUnknown {
		return errPluginRunAttemptUnknown
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT run_id FROM plugin_run_attempts WHERE instance_id=?`, instanceID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		head, e := p.runJournal.loadHead(ctx, tx, instanceID, id)
		if e != nil {
			return e
		}
		completion, e := p.runJournal.loadCompletion(head)
		if e != nil {
			return e
		}
		if completion.Result.State == "reconcile_pending" {
			return errPluginRunAttemptUnknown
		}
		run, e := p.runJournal.runForAdmission(ctx, tx, head.claim.Admission)
		if e != nil {
			return e
		}
		actual, e := newPluginRunAttemptResult(run.State, run.ErrorCode, run.Result)
		if e != nil {
			return e
		}
		if len(run.Result) == 0 || actual != completion.Result || run.ConfigRevision != completion.FinalRevision {
			return errPluginRunAttemptEvidence
		}
	}
	return nil
}

// Startup reconciliation must never turn a sealed but uncompleted attempt into
// an executable retry. Existing native recoveries run first; this final gate
// prevents them from replaying an attempt whose effect outcome is not proven.
func (p *PluginStore) quarantineRecoveredAttempts(ctx context.Context) error {
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT instance_id,run_id FROM plugin_run_attempts`)
	if err != nil {
		return err
	}
	type pair struct{ instance, id string }
	var ids []pair
	for rows.Next() {
		var v pair
		if err = rows.Scan(&v.instance, &v.id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, v := range ids {
		head, e := p.runJournal.loadHead(ctx, tx, v.instance, v.id)
		proven := false
		if e == nil {
			completion, ce := p.runJournal.loadCompletion(head)
			if ce == nil {
				run, re := p.runJournal.runForAdmission(ctx, tx, head.claim.Admission)
				if re == nil && len(run.Result) > 0 {
					actual, ae := newPluginRunAttemptResult(run.State, run.ErrorCode, run.Result)
					proven = ae == nil && actual == completion.Result && run.ConfigRevision == completion.FinalRevision
				}
			}
		}
		if !proven {
			if _, err = tx.ExecContext(ctx, `UPDATE plugin_runs SET state='reconcile_pending',error_code='PLUGIN_ATTEMPT_UNRESOLVED',updated_at=? WHERE id=?`, now(), v.id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Sticky fence cancellation prevents activation. Only this exact committed,
// never-active claim may be finalized by the framework without an executor.
// Reserved attempts are handled by commitSupervisedClaim after durable proof;
// active/quarantined attempts must join their real owner, never infer an exit.
func (p *PluginStore) finishCanceledUnenteredAttempt(ctx context.Context, a *pluginRunAttempt) (bool, error) {
	s := p.runSupervisor
	s.mu.Lock()
	if a != nil && a.phase == pluginRunDone && a.cancelRequested {
		s.mu.Unlock()
		return true, nil
	}
	if a == nil || a.phase != pluginRunCommitted || !a.cancelRequested {
		s.mu.Unlock()
		return false, nil
	}
	l := a.lease
	if l == nil || l.store != p || l.ticket.attempt != a {
		s.mu.Unlock()
		return false, errPluginRunAttemptEvidence
	}
	s.mu.Unlock()
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	run, err := p.Run(finish, a.admission.RunID)
	if err != nil {
		return true, err
	}
	run.executionLease = l
	if err = p.finalizeSupervisedUnentered(finish, run); err != nil {
		return true, err
	}
	return true, p.releaseSupervisedRun(run)
}
