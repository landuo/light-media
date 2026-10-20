package app

import (
	"bytes"
	"context"
	"database/sql"
	bf "light-media/internal/plugins/brushflow"
	"testing"
)

// Synthetic queue publication uses actual native owner, source seal/effect and
// SQLite writers. The later execution uses the compiled SDK and no downloader.
func brushFlowOwnedQueuedSource(t *testing.T) (*App, PluginInstallation, PluginRun) {
	t.Helper()
	a, in := brushDefault(t)
	ctx := context.Background()
	p := a.pluginStore
	s, _, e := p.brushFlowRead(ctx, a.Store.DB, in.InstanceID, in.Generation)
	if e != nil {
		t.Fatal(e)
	}
	origin := ""
	for id, r := range s.allRuns() {
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
	c := &brushFlowCall{o: o, run: run, method: bf.ConfigureCapability, queued: []brushFlowQueued{{Operation: "cleanup_tags"}}}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	s, prior, e := p.brushFlowRead(ctx, tx, in.InstanceID, in.Generation)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.publishQueuedTx(ctx, tx, &s); e != nil {
		t.Fatal(e)
	}
	if e = p.brushFlowSave(ctx, tx, s, prior); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	var id string
	if e = a.Store.DB.QueryRow(`SELECT id FROM plugin_runs WHERE instance_id=? AND action='task' AND state='pending'`, in.InstanceID).Scan(&id); e != nil {
		t.Fatal(e)
	}
	queued, e := p.Run(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	return a, in, queued
}
func TestBrushFlowAuthenticatedLiveSourceRejectsDeletedOrTerminalSQLCandidate(t *testing.T) {
	for _, damage := range []string{"run", "phase", "source_root", "live_root"} {
		t.Run(damage, func(t *testing.T) {
			a, in, queued := brushFlowOwnedQueuedSource(t)
			ctx := context.Background()
			p := a.pluginStore
			s, _, e := p.brushFlowReadCurrent(ctx, a.Store.DB, in.InstanceID, in.Generation)
			if e != nil {
				t.Fatal(e)
			}
			q := `DELETE FROM plugin_runs WHERE id=?`
			arg := any(queued.ID)
			switch damage {
			case "phase":
				q = `UPDATE plugin_runs SET state='failed' WHERE id=?`
			case "source_root":
				q = `DELETE FROM plugin_brush_flow_ledger_pages WHERE collection='sources' AND id=?`
				arg = string(s.pages.roots.Sources)
			case "live_root":
				q = `DELETE FROM plugin_brush_flow_ledger_pages WHERE collection='unresolved' AND id=?`
				arg = string(s.pages.roots.Unresolved)
			}
			if _, e = a.Store.DB.Exec(q, arg); e != nil {
				t.Fatal(e)
			}
			if _, _, e = p.brushFlowReadCurrent(ctx, a.Store.DB, in.InstanceID, in.Generation); e == nil {
				t.Fatal("candidate SQL absence hid authenticated live source", damage)
			}
			if _, e = p.EnqueueRun(ctx, in.InstanceID, "run", nil); e == nil {
				t.Fatal("missing live proof admitted work")
			}
			if e = p.Reset(ctx, in.InstanceID); e == nil {
				t.Fatal("missing live proof allowed lifecycle")
			}
		})
	}
}
func TestBrushFlowActualSourceTerminalRemovesOnlyLiveMembershipAndRejectsReplay(t *testing.T) {
	a, in, queued := brushFlowOwnedQueuedSource(t)
	ctx := context.Background()
	p := a.pluginStore
	run, e := p.ClaimRun(ctx)
	if e != nil || run.ID != queued.ID {
		t.Fatal(e)
	}
	brushFinish(t, a, run)
	s, _, e := p.brushFlowReadCurrent(ctx, a.Store.DB, in.InstanceID, in.Generation)
	if e != nil {
		t.Fatal(e)
	}
	n := 0
	for range s.pendingSources(ctx, a.Store.DB) {
		n++
	}
	if n != 0 || s.check() != nil {
		t.Fatal(n, s.check())
	}
	token, _ := brushFlowSourceToken(queued)
	source, ok := s.lookupSource(token)
	if !ok || source.Run != queued.ID {
		t.Fatal("permanent source identity removed")
	}
	if _, e = a.Store.DB.Exec(`UPDATE plugin_runs SET state='pending' WHERE id=?`, run.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = p.ClaimRun(ctx); e == nil {
		t.Fatal("terminal source replay accepted")
	}
}
func TestBrushFlowPrivateClaimCancelClosesLiveSourceAtomically(t *testing.T) {
	a, in, queued := brushFlowOwnedQueuedSource(t)
	ctx := context.Background()
	p := a.pluginStore
	cfg, e := p.Config(ctx, in.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.SaveConfig(ctx, in.InstanceID, cfg.Revision, bf.DefaultConfig()); e != nil {
		t.Fatal(e)
	}
	// Claim observes the stale snapshot and cancels it through the private branch.
	if _, e = p.ClaimRun(ctx); e == nil {
		t.Fatal("stale source claimed")
	}
	actual, e := p.Run(ctx, queued.ID)
	if e != nil || actual.State != "canceled" {
		t.Fatal(actual.State, e)
	}
	s, _, e := p.brushFlowReadCurrent(ctx, a.Store.DB, in.InstanceID, in.Generation)
	if e != nil {
		t.Fatal(e)
	}
	for range s.pendingSources(ctx, a.Store.DB) {
		t.Fatal("closed source remained live")
	}
	if e = s.check(); e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`UPDATE plugin_runs SET state='pending' WHERE id=?`, queued.ID); e != nil {
		t.Fatal(e)
	}
	// Even if the plaintext config snapshot is rewound, a closed live membership
	// cannot be silently reconstructed from the permanent source registry.
	if e = p.brushFlowClaimIdentityTx(ctx, mustBrushFlowTx(t, a), bf.PluginID, queued); e == nil {
		t.Fatal("private cancellation replay accepted")
	}
}
func mustBrushFlowTx(t *testing.T, a *App) *sql.Tx {
	t.Helper()
	tx, e := a.Store.DB.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { tx.Rollback() })
	return tx
}
func TestBrushFlowIndependentHeadBindsEveryMutableRootCommit(t *testing.T) {
	a, in := brushDefault(t)
	ctx := context.Background()
	p := a.pluginStore
	var oldRoot, oldState []byte
	if e := a.Store.DB.QueryRow(`SELECT sealed FROM plugin_brush_flow_roots WHERE instance_id=? AND generation=?`, in.InstanceID, in.Generation).Scan(&oldRoot); e != nil {
		t.Fatal(e)
	}
	if e := a.Store.DB.QueryRow(`SELECT sealed FROM plugin_brush_flow_state WHERE instance_id=? AND generation=?`, in.InstanceID, in.Generation).Scan(&oldState); e != nil {
		t.Fatal(e)
	}
	old, _, e := p.brushFlowHeadRead(ctx, a.Store.DB, in.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	queued, e := p.EnqueueRun(ctx, in.InstanceID, "run", nil)
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := p.ClaimRun(ctx)
	if e != nil || claimed.ID != queued.ID {
		t.Fatal(e)
	}
	brushFinish(t, a, claimed)
	current, _, e := p.brushFlowHeadRead(ctx, a.Store.DB, in.InstanceID)
	if e != nil || current.RootSHA == old.RootSHA {
		t.Fatal("head did not advance", e)
	}
	if _, e = a.Store.DB.Exec(`UPDATE plugin_brush_flow_roots SET sealed=? WHERE instance_id=? AND generation=?`, oldRoot, in.InstanceID, in.Generation); e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`UPDATE plugin_brush_flow_state SET sealed=? WHERE instance_id=? AND generation=?`, oldState, in.InstanceID, in.Generation); e != nil {
		t.Fatal(e)
	}
	if _, _, e = p.brushFlowReadCurrent(ctx, a.Store.DB, in.InstanceID, in.Generation); e == nil {
		t.Fatal("state/root rollback bypassed independent tip")
	}
	if bytes.Equal(oldRoot, oldState) {
		t.Fatal("bad fixture")
	}
}
