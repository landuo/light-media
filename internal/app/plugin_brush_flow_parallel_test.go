package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBrushFlowActualCanceledSDKBeforeOwnerLeaseIsKnownNoEffect(t *testing.T) {
	a, in := brushDefault(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	a.pluginBrushFlow.beforeLease = func(ctx context.Context, run PluginRun) { close(entered); <-ctx.Done() }
	_, err := a.pluginStore.EnqueueRun(ctx, in.InstanceID, "run", nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { a.executePluginRun(ctx, run); close(done) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("real SDK never became ready")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("canceled pre-Host run did not finish")
	}
	actual, err := a.pluginStore.Run(context.Background(), run.ID)
	if err != nil || actual.State != "failed" || actual.ErrorCode != "BRUSH_FLOW_NOT_STARTED" {
		t.Fatal("pre-Host cancellation became unknown", err, actual.State, actual.ErrorCode)
	}
	s, _, err := a.pluginStore.brushFlowRead(context.Background(), a.Store.DB, in.InstanceID, in.Generation)
	if err != nil || brushFlowNoUnknown(s) != nil {
		t.Fatal("unentered SDK polluted owner", err)
	}
	if r, exists := s.lookupRun(run.ID); !exists || !brushFlowKnownNotStarted(r, actual) {
		t.Fatal("pre-Host cancellation lacks exact authenticated zero-effect terminal")
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET state='pending' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.ClaimRun(context.Background()); err == nil {
		t.Fatal("prepared terminal rollback reauthorized canceled old identity")
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET state='failed' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}

}

func TestBrushFlowActualLifecycleWriterWaitsWithoutGlobalOrSQLLocksAndCancels(t *testing.T) {
	a, in := brushDefault(t)
	ctx := context.Background()
	o, err := a.brushFlowOwner(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = o.gate.acquire(ctx, false); err != nil {
		t.Fatal(err)
	}
	defer o.gate.release(false)
	waitCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		prepared, e := a.prepareBrushFlowTransition(waitCtx, in)
		if prepared != nil {
			prepared.o.unlock()
		}
		done <- e
	}()
	// Wait for the actual writer reservation, rather than infer it from time.
	deadline := time.Now().Add(time.Second)
	for {
		o.gate.mu.Lock()
		waiting := o.gate.writers > 0
		o.gate.mu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("lifecycle never reserved a drain")
		}
		time.Sleep(time.Millisecond)
	}
	if !a.pluginMu.TryLock() {
		cancel()
		t.Fatal("lifecycle held global plugin lock while waiting")
	}
	a.pluginMu.Unlock()
	var one int
	if err := a.Store.DB.QueryRow(`SELECT 1`).Scan(&one); err != nil || one != 1 {
		cancel()
		t.Fatal("lifecycle held SQL while waiting", err)
	}
	cancel()
	select {
	case err = <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("lifecycle cancellation did not release reservation")
	}
	if err := o.gate.acquire(ctx, false); err != nil {
		t.Fatal("cancel left phantom writer", err)
	}
	o.gate.release(false)
}

func TestBrushFlowLiveLeaseCannotHideUnknownForAnotherIdentity(t *testing.T) {
	a, in := brushDefault(t)
	ctx := context.Background()
	_, err := a.pluginStore.EnqueueRun(ctx, in.InstanceID, "run", nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d, err := a.preparePluginDeferred(ctx, in, run)
	if err != nil {
		t.Fatal(err)
	}
	held, release, err := d.lockBrushFlow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err = d.changePhase(held, "prepared", "started", nil); err != nil {
		t.Fatal(err)
	}
	s, _, err := a.pluginStore.brushFlowRead(ctx, a.Store.DB, in.InstanceID, in.Generation)
	if err != nil {
		t.Fatal(err)
	}
	o := a.pluginStore.brushFlowRuntimeOwner(in.InstanceID, in.Generation)
	if o == nil || brushFlowNoUnknown(a.pluginStore.brushFlowWithLiveState(s)) != nil {
		t.Fatal("actual live lease was not recognized")
	}
	for _, tc := range []struct {
		name string
		edit func(*pluginDeferredCall)
	}{
		{"instance", func(v *pluginDeferredCall) { v.run.InstanceID = "other" }},
		{"generation", func(v *pluginDeferredCall) { v.run.Generation++ }},
		{"run", func(v *pluginDeferredCall) { v.run.ID = "pr_other" }},
		{"input", func(v *pluginDeferredCall) { v.run.Input = []byte(`{}`) }},
		{"method", func(v *pluginDeferredCall) { v.record.Method = "other" }},
		{"revision", func(v *pluginDeferredCall) { v.run.ConfigRevision++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := pluginDeferredCall{a: d.a, installation: d.installation, run: d.run, record: d.record}
			tc.edit(&copy)
			o.mu.Lock()
			original := o.live[run.ID]
			o.live[run.ID] = &brushFlowLiveLease{d: &copy}
			o.mu.Unlock()
			defer func() { o.mu.Lock(); o.live[run.ID] = original; o.mu.Unlock() }()
			if brushFlowNoUnknown(a.pluginStore.brushFlowWithLiveState(s)) == nil {
				t.Fatal("foreign lease suppressed authenticated pending operation")
			}
			o.mu.Lock()
			o.live[run.ID] = original
			o.mu.Unlock()
		})
	}
	c := &brushFlowCall{o: o, run: run, method: d.record.Method}
	o.mu.Lock()
	o.live[run.ID].c = c
	o.mu.Unlock()
	c.uncertain.Store(true)
	if _, err := a.pluginStore.EnqueueRun(ctx, in.InstanceID, "run", nil); err == nil {
		t.Fatal("still-live unknown admitted another write")
	}
	if brushFlowNoUnknown(a.pluginStore.brushFlowWithLiveState(s)) == nil {
		t.Fatal("unknown was hidden merely because its lease remains live")
	}
}
