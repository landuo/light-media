package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	iyuu "light-media/internal/plugins/iyuuautoseed"
)

func TestIYUUStartedAdmissionSurvivesColdBeforeHostEntry(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	init := uninstallInstall(t, a, "OwnedIYUUAtomicStart", iyuu.Manifest(), map[string]any{"enabled": false, "token": "owned-synthetic-token", "downloaders": []any{}, "sites": []any{}}, true)
	invokeIYUUActualSDK(t, a, init)
	if _, e := a.pluginStore.EnqueueRun(ctx, init.InstanceID, "run", nil); e != nil {
		t.Fatal(e)
	}
	run, e := a.pluginStore.claimRunForInstance(ctx, init.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	d := prepareIYUUActualSDK(t, a, run)
	held, release, e := d.lockIYUUDeferred(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = d.changePhase(held, "prepared", "started", nil); e != nil {
		release()
		t.Fatal(e)
	}
	release() // Simulate a process stopping at the exact committed boundary.
	dir := a.Dir
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	cold, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	actual, e := cold.pluginStore.Run(ctx, run.ID)
	if e != nil || actual.State != "reconcile_pending" || actual.ErrorCode != "IYUU_UNCONFIRMED" {
		t.Fatal("legal started lost its authenticated admission before Host", actual.State, actual.ErrorCode, e)
	}
	if _, e = cold.pluginStore.EnqueueRun(ctx, run.InstanceID, "run", nil); !errors.Is(e, iyuu.ErrUnconfirmed) || pluginErrorCode(e) != "IYUU_UNCONFIRMED" {
		t.Fatal("cold legal start was replayed or mislabeled", e)
	}
	record, _, e := cold.pluginStore.readDeferred(ctx, cold.Store.DB, run)
	if e != nil || record.Phase != "started" {
		t.Fatal("cold changed original exact receipt", record.Phase, e)
	}
	tx, e := cold.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	root, _, e := cold.pluginStore.iyuuRootReadTx(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		t.Fatal(e)
	}
	fact, ok := root.Runs[run.ID]
	if !ok || fact.Phase != "accepted" || fact.InputSHA != deferredInputSHA(run.Input) || fact.Revision != run.ConfigRevision {
		t.Fatal("started did not atomically bind original snapshot", fact)
	}
	var effects int
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM iyuu_runs`).Scan(&effects); e != nil || effects != 0 {
		t.Fatal("Host unexpectedly ran during cold boundary", effects, e)
	}
}

func TestIYUUStartedAndAcceptedRollbackTogether(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	init := uninstallInstall(t, a, "OwnedIYUUAtomicRollback", iyuu.Manifest(), map[string]any{"enabled": false, "token": "owned-synthetic-token", "downloaders": []any{}, "sites": []any{}}, true)
	invokeIYUUActualSDK(t, a, init)
	if _, e := a.pluginStore.EnqueueRun(ctx, init.InstanceID, "run", nil); e != nil {
		t.Fatal(e)
	}
	run, e := a.pluginStore.claimRunForInstance(ctx, init.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	d := prepareIYUUActualSDK(t, a, run)
	if _, e = a.Store.DB.Exec(`CREATE TRIGGER owned_reject_iyuu_start BEFORE UPDATE ON plugin_iyuu_roots BEGIN SELECT RAISE(ABORT,'owned-start-root-fault'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e = d.execute(ctx, &run); e == nil {
		t.Fatal("owned root update fault ignored")
	}
	record, _, e := a.pluginStore.readDeferred(ctx, a.Store.DB, run)
	if e != nil || record.Phase != "prepared" {
		t.Fatal("failed atomic admission committed started", record.Phase, e)
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	root, _, e := a.pluginStore.iyuuRootReadTx(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	_, exists := root.Runs[run.ID]
	tx.Rollback()
	if exists {
		t.Fatal("rolled back start committed root fact")
	}
	if _, e = a.Store.DB.Exec(`DROP TRIGGER owned_reject_iyuu_start`); e != nil {
		t.Fatal(e)
	}
	// Exact same prepared identity can proceed after a transaction proven to have
	// rolled back; no source/Host operation ran during the failed admission.
	result, e := d.execute(ctx, &run)
	var outcome struct {
		Completed bool `json:"completed"`
	}
	if e != nil || json.Unmarshal(result, &outcome) != nil || !outcome.Completed {
		t.Fatal("rolled back exact preparation could not complete actual source", string(result), e)
	}
	if e = finishClaimedFixtureRun(ctx, a.pluginStore, run, "succeeded", "", result); e != nil {
		t.Fatal(e)
	}
}
