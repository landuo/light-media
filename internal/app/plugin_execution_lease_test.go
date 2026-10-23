package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"light-media/internal/plugins/builtin"
)

// These tests use the production constructor, claim/finish adapters and real
// SQLite. Only the business owner is synthetic except for the final SDK test.
func executionLeaseQueue(t *testing.T, p *PluginStore, instance string) PluginRun {
	t.Helper()
	installStoredPlugin(t, p, instance)
	_, queued, err := p.SaveConfigAndQueue(context.Background(), instance, 0, map[string]any{"enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	return queued
}

func executionLeaseClaim(t *testing.T, p *PluginStore, queued PluginRun) PluginRun {
	t.Helper()
	run, err := p.ClaimRun(context.Background())
	if err != nil || run.ID != queued.ID || run.State != "running" || run.executionLease == nil {
		t.Fatalf("real claim did not return the committed lease: id=%s state=%s err=%v", run.ID, run.State, err)
	}
	return run
}

func executionLeaseActivate(t *testing.T, p *PluginStore, run PluginRun) context.Context {
	t.Helper()
	ctx, err := p.activateSupervisedRun(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func executionLeaseFinish(t *testing.T, p *PluginStore, ctx context.Context, run PluginRun) {
	t.Helper()
	if err := p.FinishRun(ctx, run.ID, "succeeded", "", json.RawMessage(`{"confirmed":true}`)); err != nil {
		t.Fatal(err)
	}
}

func executionLeaseAssertCompletion(t *testing.T, p *PluginStore, run PluginRun) {
	t.Helper()
	l := run.executionLease
	l.mu.Lock()
	expected := l.expected
	l.mu.Unlock()
	if expected == nil {
		t.Fatal("terminal SQL did not retain expected outcome for exact proof")
	}
	proof, err := p.runJournal.resolveCompletion(context.Background(), l.claim, *expected)
	if err != nil || proof == nil || proof.attempt != l.claim {
		t.Fatal("exact SQLite completion receipt not proven", err)
	}
}

func executionLeaseAssertCount(t *testing.T, p *PluginStore, want int) {
	t.Helper()
	attempts, err := p.runSupervisor.snapshot()
	if err != nil || len(attempts) != want {
		t.Fatalf("live attempt count=%d want=%d err=%v", len(attempts), want, err)
	}
}

func executionLeaseWait(t *testing.T, transition *pluginRunTransition, blocked bool) {
	t.Helper()
	budget := 2 * time.Second
	if blocked {
		budget = 25 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	err := transition.Wait(ctx)
	if blocked && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("join returned before owner cleanup: %v", err)
	}
	if !blocked && err != nil {
		t.Fatal("join did not observe completed cleanup", err)
	}
}

func TestPluginExecutionLeaseRealClaimFinishAndRelease(t *testing.T) {
	_, p := pluginStoreFixture(t)
	run := executionLeaseClaim(t, p, executionLeaseQueue(t, p, "lease-lifecycle"))
	ctx := executionLeaseActivate(t, p, run)
	executionLeaseAssertCount(t, p, 1)
	if _, err := p.runJournal.resolveClaim(context.Background(), run.executionLease.claim); err != nil {
		t.Fatal("actual claim transaction omitted its nonce receipt", err)
	}
	executionLeaseFinish(t, p, ctx, run)
	executionLeaseAssertCompletion(t, p, run)
	executionLeaseAssertCount(t, p, 1) // A durable result alone does not release ownership.
	if err := p.releaseSupervisedRun(run); err != nil {
		t.Fatal(err)
	}
	executionLeaseAssertCount(t, p, 0)
	if err := p.releaseSupervisedRun(run); err != nil {
		t.Fatal("idempotent lease release failed", err)
	}
	public, err := json.Marshal(run)
	if err != nil || bytes.Contains(public, []byte(run.executionLease.claim.nonce)) || bytes.Contains(public, []byte("executionLease")) {
		t.Fatal("private execution capability leaked into public run", err)
	}
}

func TestPluginExecutionLeaseFinishRejectsMissingContext(t *testing.T) {
	_, p := pluginStoreFixture(t)
	run := executionLeaseClaim(t, p, executionLeaseQueue(t, p, "lease-no-context"))
	ctx := executionLeaseActivate(t, p, run)
	if err := p.FinishRun(context.Background(), run.ID, "succeeded", "", nil); !errors.Is(err, errPluginRunAttemptEvidence) {
		t.Fatal("run-ID-only finish accepted a supervised attempt", err)
	}
	actual, err := p.Run(context.Background(), run.ID)
	if err != nil || actual.State != "running" || len(actual.Result) != 0 {
		t.Fatal("unauthorized finish changed running row", actual.State, err)
	}
	executionLeaseFinish(t, p, ctx, run)
	if err := p.releaseSupervisedRun(run); err != nil {
		t.Fatal(err)
	}
}

func TestPluginExecutionLeaseOldContextCannotFinishLaterRun(t *testing.T) {
	_, p := pluginStoreFixture(t)
	first := executionLeaseClaim(t, p, executionLeaseQueue(t, p, "lease-old-context"))
	oldCtx := executionLeaseActivate(t, p, first)
	executionLeaseFinish(t, p, oldCtx, first)
	if err := p.releaseSupervisedRun(first); err != nil {
		t.Fatal(err)
	}
	_, queued, err := p.SaveConfigAndQueue(context.Background(), first.InstanceID, first.ConfigRevision, map[string]any{"enabled": true, "revision_value": 2})
	if err != nil {
		t.Fatal(err)
	}
	second := executionLeaseClaim(t, p, queued)
	currentCtx := executionLeaseActivate(t, p, second)
	// Detach cancellation to test capability identity, not ctx.Err rejection.
	if err = p.FinishRun(context.WithoutCancel(oldCtx), second.ID, "succeeded", "", nil); !errors.Is(err, errPluginRunAttemptEvidence) {
		t.Fatal("previous run capability finished a new attempt", err)
	}
	actual, err := p.Run(context.Background(), second.ID)
	if err != nil || actual.State != "running" {
		t.Fatal("stale context terminalized current run", actual.State, err)
	}
	executionLeaseFinish(t, p, currentCtx, second)
	if err = p.releaseSupervisedRun(second); err != nil {
		t.Fatal(err)
	}
}

func TestPluginExecutionLeaseJoinWaitsForOwnerCleanupAfterFinish(t *testing.T) {
	_, p := pluginStoreFixture(t)
	run := executionLeaseClaim(t, p, executionLeaseQueue(t, p, "lease-owner-cleanup"))
	ctx := executionLeaseActivate(t, p, run)
	executionLeaseFinish(t, p, ctx, run)
	transition, err := p.runSupervisor.beginTransition([]string{run.InstanceID})
	if err != nil {
		t.Fatal(err)
	}
	executionLeaseWait(t, transition, true)
	entered, allowCleanup := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	l := run.executionLease
	l.mu.Lock()
	oldCleanup := l.cleanup
	l.cleanup = func() {
		close(entered)
		<-allowCleanup
		if oldCleanup != nil {
			oldCleanup()
		}
	}
	l.mu.Unlock()
	released := make(chan error, 1)
	releaseObserved := false
	go func() { released <- p.releaseSupervisedRun(run) }()
	defer func() {
		unblock.Do(func() { close(allowCleanup) })
		if !releaseObserved {
			select {
			case <-released:
			case <-time.After(2 * time.Second):
				t.Error("lease cleanup goroutine did not terminate")
			}
		}
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("release did not enter actual owner cleanup")
	}
	executionLeaseWait(t, transition, true)
	unblock.Do(func() { close(allowCleanup) })
	select {
	case err = <-released:
		releaseObserved = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("release did not finish after owner cleanup")
	}
	executionLeaseWait(t, transition, false)
	if err = transition.releaseAfterResolution(); err != nil {
		t.Fatal(err)
	}
}

func TestPluginExecutionLeaseFenceCancelsOnlyExactInstance(t *testing.T) {
	_, p := pluginStoreFixture(t)
	a := executionLeaseClaim(t, p, executionLeaseQueue(t, p, "lease-instance-a"))
	ctxA := executionLeaseActivate(t, p, a)
	b := executionLeaseClaim(t, p, executionLeaseQueue(t, p, "lease-instance-b"))
	ctxB := executionLeaseActivate(t, p, b)
	transition, err := p.runSupervisor.beginTransition([]string{a.InstanceID})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(ctxA.Err(), context.Canceled) || ctxB.Err() != nil {
		t.Fatalf("instance cancellation crossed scope: A=%v B=%v", ctxA.Err(), ctxB.Err())
	}
	executionLeaseFinish(t, p, ctxB, b)
	if err = p.releaseSupervisedRun(b); err != nil {
		t.Fatal(err)
	}
	executionLeaseWait(t, transition, true)
	if err = p.FinishRun(context.WithoutCancel(ctxA), a.ID, "failed", "PLUGIN_RUN_CANCELED", nil); err != nil {
		t.Fatal(err)
	}
	if err = p.releaseSupervisedRun(a); err != nil {
		t.Fatal(err)
	}
	executionLeaseWait(t, transition, false)
	if err = transition.releaseAfterResolution(); err != nil {
		t.Fatal(err)
	}
	executionLeaseAssertCount(t, p, 0)
}

func TestPluginExecutionLeaseCommittedFenceBeforeActivation(t *testing.T) {
	_, p := pluginStoreFixture(t)
	run := executionLeaseClaim(t, p, executionLeaseQueue(t, p, "lease-unentered"))
	transition, err := p.runSupervisor.beginTransition([]string{run.InstanceID})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := p.activateSupervisedRun(context.Background(), run)
	if !errors.Is(err, errPluginRunCanceled) {
		t.Fatal("fenced committed lease activated", err)
	}
	executionLeaseWait(t, transition, true)
	if err = p.finalizeSupervisedUnentered(context.WithoutCancel(ctx), run); err != nil {
		t.Fatal("unentered claim did not persist safe cancellation", err)
	}
	actual, err := p.Run(context.Background(), run.ID)
	if err != nil || actual.State != "failed" || actual.ErrorCode != "PLUGIN_RUN_CANCELED" {
		t.Fatal("unentered cancellation lost its terminal result", actual.State, actual.ErrorCode, err)
	}
	executionLeaseAssertCompletion(t, p, run)
	executionLeaseWait(t, transition, true)
	if err = p.releaseSupervisedRun(run); err != nil {
		t.Fatal(err)
	}
	executionLeaseWait(t, transition, false)
	if err = transition.releaseAfterResolution(); err != nil {
		t.Fatal(err)
	}
}

func TestPluginExecutionLeaseColdUnfinishedAttemptCannotReplayOrReset(t *testing.T) {
	store, p := pluginStoreFixture(t)
	ctx := context.Background()
	manifest := builtin.CategoryEditorManifest()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Install(ctx, PluginInstallation{InstanceID: manifest.PluginID, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: 1, Manifest: raw}); err != nil {
		t.Fatal(err)
	}
	_, queued, err := p.SaveConfigAndQueue(ctx, manifest.PluginID, 0, map[string]any{"enabled": false, "content": ""})
	if err != nil {
		t.Fatal(err)
	}
	run := executionLeaseClaim(t, p, queued)
	before, err := p.Installation(ctx, run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	cold, err := NewPluginStore(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if cold.runJournal.boot == p.runJournal.boot {
		t.Fatal("cold constructor adopted old host boot")
	}
	if err = cold.recoverPluginRuns(ctx); err != nil {
		t.Fatal(err)
	}
	actual, err := cold.Run(ctx, run.ID)
	if err != nil || actual.State != "reconcile_pending" || actual.ErrorCode != "PLUGIN_ATTEMPT_UNRESOLVED" {
		t.Fatal("old idempotent recovery replayed an uncompleted attempt", actual.State, actual.ErrorCode, err)
	}
	if _, err = cold.ClaimRun(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("cold unproven attempt was claimable", err)
	}
	if err = cold.Reset(ctx, run.InstanceID); err == nil {
		t.Fatal("empty cold registry was accepted as durable reset quiescence")
	}
	after, err := cold.Installation(ctx, run.InstanceID)
	if err != nil || after.Generation != before.Generation || after.DesiredEnabled != before.DesiredEnabled {
		t.Fatal("rejected cold reset still changed installation", after.Generation, err)
	}
	if _, err = cold.Config(ctx, run.InstanceID); err != nil {
		t.Fatal("rejected cold reset removed configuration", err)
	}
	var attempts int
	if err = store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_attempts WHERE run_id=?`, run.ID).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatal("cold recovery/reset discarded execution evidence", attempts, err)
	}
}

func TestPluginExecutionLeaseClaimJournalFailureRollsBackBothOwners(t *testing.T) {
	_, p := pluginStoreFixture(t)
	queued := executionLeaseQueue(t, p, "lease-claim-rollback")
	if _, err := p.store.DB.Exec(`CREATE TRIGGER lease_claim_failure BEFORE INSERT ON plugin_run_attempt_heads BEGIN SELECT RAISE(ABORT,'synthetic lease claim failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ClaimRun(context.Background()); err == nil {
		t.Fatal("claim journal failure reported success")
	}
	actual, err := p.Run(context.Background(), queued.ID)
	if err != nil || actual.State != "pending" {
		t.Fatal("claim failure committed running state", actual.State, err)
	}
	var attempts int
	if err = p.store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_attempts WHERE run_id=?`, queued.ID).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatal("claim rollback retained partial SQL attempt", attempts, err)
	}
	executionLeaseAssertCount(t, p, 0)
}

func TestPluginExecutionLeaseReleaseWithoutTerminalCannotAcknowledgeDone(t *testing.T) {
	_, p := pluginStoreFixture(t)
	run := executionLeaseClaim(t, p, executionLeaseQueue(t, p, "lease-no-terminal"))
	ctx := executionLeaseActivate(t, p, run)
	transition, err := p.runSupervisor.beginTransition([]string{run.InstanceID})
	if err != nil {
		t.Fatal(err)
	}
	if err = p.releaseSupervisedRun(run); !errors.Is(err, errPluginRunUnresolved) {
		t.Fatal("owner exit without durable completion reported Done", err)
	}
	executionLeaseAssertCount(t, p, 1)
	executionLeaseWait(t, transition, true)
	if err = p.FinishRun(context.WithoutCancel(ctx), run.ID, "reconcile_pending", "PLUGIN_EXECUTION_FAILED", nil); err != nil {
		t.Fatal(err)
	}
	if err = p.releaseSupervisedRun(run); err != nil {
		t.Fatal(err)
	}
	executionLeaseWait(t, transition, false)
	if err = transition.releaseAfterResolution(); err != nil {
		t.Fatal(err)
	}
}

func TestPluginExecutionLeaseActualAppSDKCompletesAndReleases(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	ctx := context.Background()
	manifest := builtin.CategoryEditorManifest()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.Install(ctx, PluginInstallation{InstanceID: manifest.PluginID, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: 1, Manifest: raw}); err != nil {
		t.Fatal(err)
	}
	_, queued, err := a.pluginStore.SaveConfigAndQueue(ctx, manifest.PluginID, 0, map[string]any{"enabled": true, "content": customJapaneseMovieCategory})
	if err != nil {
		t.Fatal(err)
	}
	run := executionLeaseClaim(t, a.pluginStore, queued)
	if err = a.executePluginRun(ctx, run); err != nil {
		t.Fatal("real SDK lifecycle did not finish", err)
	}
	actual, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || actual.State != "succeeded" || string(actual.Result) != `{"enabled":false,"applied":true}` {
		t.Fatal("real compiled SDK result missing", actual.State, actual.ErrorCode, string(actual.Result), err)
	}
	content, err := os.ReadFile(filepath.Join(a.Dir, "category.yaml"))
	if err != nil || string(content) != customJapaneseMovieCategory {
		t.Fatal("real host did not apply requested category file", err)
	}
	executionLeaseAssertCompletion(t, a.pluginStore, run)
	executionLeaseAssertCount(t, a.pluginStore, 0)
	if len(a.pluginProcess) != 0 {
		t.Fatal("lease completed while SDK process slot was still held")
	}
}
