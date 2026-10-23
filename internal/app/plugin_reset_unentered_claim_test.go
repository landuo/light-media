package app

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestPluginResetCommittedUnenteredClaimFinishesWithoutExecutor(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	run := executionLeaseClaim(t, p, executionLeaseQueue(t, p, "unentered-reset"))
	before, err := p.Installation(ctx, run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	request, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err = p.Reset(request, run.InstanceID); err != nil {
		t.Fatal("reset waited for an executor that never entered", err)
	}
	current, err := p.Run(ctx, run.ID)
	if err != nil || current.State != "failed" || current.ErrorCode != "PLUGIN_RUN_CANCELED" {
		t.Fatal(current.State, current.ErrorCode, err)
	}
	executionLeaseAssertCompletion(t, p, run)
	executionLeaseAssertCount(t, p, 0)
	after, err := p.Installation(ctx, run.InstanceID)
	if err != nil || after.Generation != before.Generation+1 {
		t.Fatal(after.Generation, err)
	}
	if _, err = p.Config(ctx, run.InstanceID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("reset did not remove owned synthetic config", err)
	}
	if _, err = p.activateSupervisedRun(ctx, run); err == nil {
		t.Fatal("late executor entered a finalized old claim")
	}
}

func TestPluginResetFenceBeforeClaimCommitFinalizesExactUnenteredAttempt(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	run := executionLeaseQueue(t, p, "unentered-commit")
	window, err := p.beginSupervisedClaim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_runs SET state='running' WHERE id=? AND state='pending'`, run.ID); err != nil {
		t.Fatal(err)
	}
	if err = p.markPluginExecutionStartedTx(ctx, tx, run); err != nil {
		t.Fatal(err)
	}
	lease, err := p.reserveSupervisedClaimTx(ctx, tx, window, run)
	if err != nil {
		t.Fatal(err)
	}
	transition, err := p.runSupervisor.beginTransition([]string{run.InstanceID})
	if err != nil {
		t.Fatal(err)
	}
	if phase := lease.ticket.attempt.phase; phase != pluginRunReserved {
		t.Fatal("fixture did not capture reserved claim", phase)
	}
	if err = p.commitSupervisedClaim(ctx, lease, tx.Commit()); !errors.Is(err, errPluginRunCanceled) {
		t.Fatal("post-fence commit was allowed to execute", err)
	}
	run.executionLease = lease
	executionLeaseAssertCompletion(t, p, run)
	executionLeaseAssertCount(t, p, 0)
	executionLeaseWait(t, transition, false)
	if err = transition.releaseAfterResolution(); err != nil {
		t.Fatal(err)
	}
	actual, err := p.Run(ctx, run.ID)
	if err != nil || actual.State != "failed" || actual.ErrorCode != "PLUGIN_RUN_CANCELED" {
		t.Fatal(actual.State, actual.ErrorCode, err)
	}
}
