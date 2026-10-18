package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"light-media/internal/plugins"
	bf "light-media/internal/plugins/brushflow"
	"light-media/internal/plugins/builtin"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBrushFlowSourcePoolsActualDefaultsAndIndependentReservations(t *testing.T) {
	for _, large := range []bool{false, true} {
		p := newBrushFlowSourcePools(large)
		n := 50
		if large {
			n = 100
		}
		if cap(p.tasks) != n || cap(p.services) != n {
			t.Fatal("source CONF bound changed")
		}
		var releases []func()
		for i := 0; i < n; i++ {
			b := &brushFlowClaimBudget{pools: p}
			ctx := context.WithValue(context.Background(), brushFlowClaimBudgetKey{}, b)
			if !brushFlowReserveClaim(ctx, bf.PluginID, PluginRun{Version: bf.Version, Action: "task"}) {
				t.Fatal(i)
			}
			releases = append(releases, b.release)
		}
		b := &brushFlowClaimBudget{pools: p}
		ctx := context.WithValue(context.Background(), brushFlowClaimBudgetKey{}, b)
		if brushFlowReserveClaim(ctx, bf.PluginID, PluginRun{Version: bf.Version, Action: "task"}) {
			t.Fatal("source threadpool overclaimed")
		}
		if !brushFlowReserveClaim(ctx, bf.PluginID, PluginRun{Version: bf.Version, Action: "service"}) {
			t.Fatal("scheduler incorrectly shares threadpool slots")
		}
		b.release()
		for _, release := range releases {
			release()
			release()
		}
		if len(p.tasks) != 0 || len(p.services) != 0 {
			t.Fatal("reservation leaked or released twice")
		}
	}
}

func TestBrushFlowActualWorkerKeepsOverCapacityInSQLAndRunsOtherPlugin(t *testing.T) {
	a, in := brushDefault(t)
	ctx := context.Background()
	p := a.pluginStore
	state, _, e := p.brushFlowRead(ctx, a.Store.DB, in.InstanceID, in.Generation)
	if e != nil {
		t.Fatal(e)
	}
	var origin string
	for id, r := range state.Runs {
		if r.Action == "init" {
			origin = id
		}
	}
	run, e := p.Run(ctx, origin)
	if e != nil {
		t.Fatal(e)
	}
	o, e := a.brushFlowOwner(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	c := &brushFlowCall{o: o, run: run, method: bf.ConfigureCapability}
	for i := 0; i < 65; i++ {
		c.queued = append(c.queued, brushFlowQueued{Operation: "cleanup_tags"})
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	state, prior, e := p.brushFlowRead(ctx, tx, in.InstanceID, in.Generation)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.publishQueuedTx(ctx, tx, &state); e != nil {
		t.Fatal(e)
	}
	if e = p.brushFlowSave(ctx, tx, state, prior); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	// Explicit synthetic reservations stand for 47 occupied ThreadHelper slots and
	// 50 scheduler slots. The remaining three execute actual compiled SDKs below;
	// this is a queue/memory boundary fixture, not 100 invented native successes.
	pools := a.brushFlowPools()
	for i := 0; i < 47; i++ {
		pools.tasks <- struct{}{}
	}
	for i := 0; i < 50; i++ {
		pools.services <- struct{}{}
	}
	// A failed actual SQL claim must release its source reservation atomically.
	if _, e = a.Store.DB.Exec(`CREATE TRIGGER owned_pool_rollback BEFORE UPDATE OF state ON plugin_runs WHEN NEW.instance_id='BrushFlowLanduo' AND NEW.state='running' BEGIN SELECT RAISE(ABORT,'owned pool rollback'); END`); e != nil {
		t.Fatal(e)
	}
	budget := &brushFlowClaimBudget{pools: pools}
	_, e = p.ClaimRun(context.WithValue(ctx, brushFlowClaimBudgetKey{}, budget))
	if e == nil || len(pools.tasks) != 47 {
		t.Fatal("failed SQL claim consumed source slot", e, len(pools.tasks))
	}
	if _, e = a.Store.DB.Exec(`DROP TRIGGER owned_pool_rollback`); e != nil {
		t.Fatal(e)
	}
	entered := make(chan string, 65)
	a.pluginBrushFlow.beforeLease = func(ctx context.Context, r PluginRun) {
		if r.Action == "task" {
			entered <- r.ID
			<-ctx.Done()
		}
	}
	uninstallInstall(t, a, "SystemProcess", builtin.SystemProcessManifest(), map[string]any{"enable": false}, false)
	other, e := p.EnqueueRun(ctx, "SystemProcess", "init", nil)
	if e != nil {
		t.Fatal(e)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Error("source pool shutdown did not join claimed tasks")
			}
		})
	}
	defer stop()
	go func() { a.pluginWorker(workerCtx); close(done) }()
	a.wakePlugins()
	for i := 0; i < 3; i++ {
		select {
		case <-entered:
		case <-time.After(60 * time.Second):
			t.Fatal("source task did not reach actual SDK ready", i)
		}
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		r, e := p.Run(ctx, other.ID)
		if e != nil {
			t.Fatal(e)
		}
		if r.State == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("full Brush pools blocked other compiled plugin", r.State, r.ErrorCode)
		}
		time.Sleep(time.Millisecond)
	}
	var pending, running, prepared int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND action='task' AND state='pending'`, in.InstanceID).Scan(&pending); e != nil {
		t.Fatal(e)
	}
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND action='task' AND state='running'`, in.InstanceID).Scan(&running)
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_deferred_operations d JOIN plugin_runs r ON r.id=d.run_id WHERE r.instance_id=? AND r.action='task'`, in.InstanceID).Scan(&prepared)
	if pending != 62 || running != 3 || prepared != 3 || len(pools.tasks) != 50 || len(pools.services) != 50 {
		t.Fatal("eager claim/goroutine escaped source bounds", pending, running, prepared, len(pools.tasks), len(pools.services))
	}
	select {
	case <-entered:
		t.Fatal("over-capacity task started SDK")
	default:
	}
	stop()
	if len(pools.tasks) != 47 || len(pools.services) != 50 {
		t.Fatal("actual claimed task reservations did not release")
	}
	for i := 0; i < 47; i++ {
		<-pools.tasks
	}
	for i := 0; i < 50; i++ {
		<-pools.services
	}
}

func TestBrushFlowActualCancelWhileWaitingSDKSlotIsKnownZeroEffect(t *testing.T) {
	a, in := brushDefault(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, e := a.pluginStore.EnqueueRun(ctx, in.InstanceID, "run", nil)
	if e != nil {
		t.Fatal(e)
	}
	run, e := a.pluginStore.ClaimRun(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var launched atomic.Int32
	original := a.pluginCommand
	a.pluginCommand = func(id string) (plugins.Command, error) { launched.Add(1); return original(id) }
	a.pluginProcess <- struct{}{}
	defer func() { <-a.pluginProcess }()
	done := make(chan struct{})
	go func() { a.executePluginRun(ctx, run); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("SDK slot cancellation did not close")
	}
	actual, e := a.pluginStore.Run(context.Background(), run.ID)
	if e != nil || actual.State != "failed" || actual.ErrorCode != "BRUSH_FLOW_NOT_STARTED" || launched.Load() != 0 {
		t.Fatal(actual.State, actual.ErrorCode, e, launched.Load())
	}
	s, _, e := a.pluginStore.brushFlowRead(context.Background(), a.Store.DB, in.InstanceID, in.Generation)
	if e != nil || brushFlowNoUnknown(s) != nil {
		t.Fatal(e)
	}
	if r, ok := s.Runs[run.ID]; !ok || !brushFlowKnownNotStarted(r, actual) {
		t.Fatal("pre-SDK wait lacks authenticated zero-effect terminal")
	}
	if _, e = a.Store.DB.Exec(`UPDATE plugin_runs SET state='pending' WHERE id=?`, run.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = a.pluginStore.ClaimRun(context.Background()); e == nil {
		t.Fatal("plaintext terminal rollback reauthorized canceled old identity")
	}
	if _, e = a.Store.DB.Exec(`UPDATE plugin_runs SET state='failed' WHERE id=?`, run.ID); e != nil {
		t.Fatal(e)
	}
	if _, _, e = a.pluginStore.readDeferred(context.Background(), a.Store.DB, run); e != sql.ErrNoRows {
		t.Fatal("pre-SDK wait invented a handshake", e)
	}
	var result map[string]any
	if json.Unmarshal(actual.Result, &result) != nil || result["decision"] != "not_started" || result["effects_started"] != false {
		t.Fatal("missing terminal zero-effect receipt")
	}
}

func TestBrushFlowActualPreSDKCancelAfterRetirementNeverChangesAncestor(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-handshake", true: "prepared"}[prepared], func(t *testing.T) {
			a, in := brushDefault(t)
			ctx := context.Background()
			_, e := a.pluginStore.EnqueueRun(ctx, in.InstanceID, "run", nil)
			if e != nil {
				t.Fatal(e)
			}
			run, e := a.pluginStore.ClaimRun(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if prepared {
				if _, e = a.preparePluginDeferred(ctx, in, run); e != nil {
					t.Fatal(e)
				}
			}
			if e = a.pluginStore.Reset(ctx, in.InstanceID); e != nil {
				t.Fatal(e)
			}
			var before, after []byte
			if e = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_brush_flow_roots WHERE instance_id=? AND generation=?`, in.InstanceID, in.Generation).Scan(&before); e != nil {
				t.Fatal(e)
			}
			if e = a.pluginStore.brushFlowCancelBeforeSDK(ctx, run); e != nil {
				t.Fatal(e)
			}
			if e = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_brush_flow_roots WHERE instance_id=? AND generation=?`, in.InstanceID, in.Generation).Scan(&after); e != nil || !bytes.Equal(before, after) {
				t.Fatal("cancellation changed sealed retired parent", e)
			}
			current, e := a.pluginStore.Installation(ctx, in.InstanceID)
			if e != nil {
				t.Fatal(e)
			}
			if _, _, e = a.pluginStore.brushFlowRead(ctx, a.Store.DB, in.InstanceID, current.Generation); e != nil {
				t.Fatal("new generation parent chain was broken", e)
			}
			if _, e = a.Store.DB.Exec(`UPDATE plugin_runs SET state='pending' WHERE id=?`, run.ID); e != nil {
				t.Fatal(e)
			}
			claimed, e := a.pluginStore.ClaimRun(ctx)
			if e == nil && claimed.ID == run.ID {
				t.Fatal("retired identity reauthorized")
			}
			actual, e := a.pluginStore.Run(ctx, run.ID)
			if e != nil || actual.State != "canceled" || actual.ErrorCode != "PLUGIN_SNAPSHOT_STALE" {
				t.Fatal("retired scope was not rejected", actual.State, actual.ErrorCode, e)
			}
		})
	}
}
