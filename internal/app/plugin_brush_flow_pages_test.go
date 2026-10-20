package app

import (
	"bytes"
	"context"
	"encoding/json"
	bf "light-media/internal/plugins/brushflow"
	"strings"
	"testing"
)

func brushFlowTestRunCount(s brushFlowState) int {
	n := 0
	for range s.allRuns() {
		n++
	}
	return n
}

// This fixture creates terminal rows using the owner's real SQL/seal writers.
// It tests storage capacity, not synthetic downloader success. Only the initial
// configure and final HTTP status run through the actual compiled SDK.
func TestBrushFlowPagedAppHistoryExceedsOldCellLimitAndSurvivesCold(t *testing.T) {
	a, in := brushDefault(t)
	ctx := context.Background()
	p := a.pluginStore
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	s, previous, e := p.brushFlowRead(ctx, tx, in.InstanceID, in.Generation)
	if e != nil {
		t.Fatal(e)
	}
	var runs []PluginRun
	for i := 0; i < 132; i++ {
		r, e := p.enqueueRunTx(ctx, tx, in.InstanceID, "run", nil)
		if e != nil {
			t.Fatal(i, e)
		}
		runs = append(runs, r)
	}
	marker := strings.Repeat("owned capacity fixture ", 6200)
	var originalBytes int
	for _, run := range runs {
		method, raw, e := brushFlowDeferredSpec(run)
		if e != nil {
			t.Fatal(e)
		}
		result := json.RawMessage(`null`)
		d := pluginDeferredRecord{Schema: 1, Plugin: bf.PluginID, Instance: in.InstanceID, Version: bf.Version, Run: run.ID, Action: run.Action, Method: method, Generation: run.Generation, Revision: run.ConfigRevision, InputSHA: deferredInputSHA(run.Input), Raw: raw, Ack: json.RawMessage(`{}`), Result: result, Phase: "confirmed"}
		if e = p.writeDeferred(ctx, tx, d, nil); e != nil {
			t.Fatal(e)
		}
		cell, e := p.seal("run_result", in.InstanceID, run.ID, result)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.ExecContext(ctx, `UPDATE plugin_runs SET state='succeeded',result=? WHERE id=?`, cell, run.ID); e != nil {
			t.Fatal(e)
		}
		args, _ := json.Marshal(map[string]any{"opaque": marker, "exact": json.Number("1.0")})
		f := brushFlowEffect{Kind: "synthetic_capacity", Phase: "confirmed", Args: args, Result: result}
		originalBytes += len(args)
		s.setRun(run.ID, brushFlowAction{RunID: run.ID, Action: run.Action, InputSHA: d.InputSHA, Method: method, Phase: "confirmed", Revision: run.ConfigRevision, EffectiveRevision: run.ConfigRevision, Result: result, Effects: []brushFlowEffect{f}})
	}
	if originalBytes <= pluginDataValueLimit {
		t.Fatal("fixture did not cross previous bound", originalBytes, pluginDataValueLimit)
	}
	if e = p.brushFlowSave(ctx, tx, s, previous); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	cookie := setupTestAdmin(t, a)
	verify := func(app *App) {
		t.Helper()
		state, seal, e := app.pluginStore.brushFlowRead(ctx, app.Store.DB, in.InstanceID, in.Generation)
		if e != nil {
			t.Fatal(e)
		}
		if state.Version != 2 || len(state.Runs) != 0 || len(state.Sources) != 0 || len(seal) > 4096 {
			t.Fatal("history rebuilt into a single state cell", state.Version, len(state.Runs), len(seal))
		}
		count := 0
		for _, r := range state.allRuns() {
			count++
			if r.RunID == runs[0].ID && (!bytes.Contains(r.Effects[0].Args, []byte(`"exact":1.0`)) || !bytes.Contains(r.Effects[0].Args, []byte(marker))) {
				t.Fatal("effect bytes changed")
			}
		}
		if count != 133 || state.check() != nil {
			t.Fatal("historical membership changed", count, state.check())
		}
		resp := callTest(t, app.Handler(), "GET", "/api/plugins/BrushFlowLanduo/api/status", "", cookie, "")
		if resp.Code != 200 || !strings.Contains(resp.Body.String(), `"success":true`) {
			t.Fatal(resp.Code, resp.Body.String())
		}
	}
	verify(a)
	dir := a.Dir
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	cold, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	useBuiltInPluginProcess(t, cold)
	verify(cold)
	// Corrupt an exact reached collection root. Ordinary point access makes
	// no claim about unvisited archives; target lookup and strict cold/lifecycle
	// audit must fail rather than interpret this old identity as absent.
	state, _, e := cold.pluginStore.brushFlowReadCurrent(ctx, cold.Store.DB, in.InstanceID, in.Generation)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = cold.Store.DB.ExecContext(ctx, `DELETE FROM plugin_brush_flow_ledger_pages WHERE collection='effects' AND id=?`, string(state.pages.roots.Effects)); e != nil {
		t.Fatal(e)
	}
	if _, ok := state.lookupEffect(runs[0].ID, 0); ok || state.check() == nil {
		t.Fatal("missing target old effect became absent without integrity error")
	}
	if _, _, e = cold.pluginStore.brushFlowRead(ctx, cold.Store.DB, in.InstanceID, in.Generation); e == nil {
		t.Fatal("cold full audit ignored missing old proof")
	}
	// Even after plaintext rollback, the old authenticated run membership cannot
	// be treated as a new request, without requiring unrelated archived bodies.
	if _, e = cold.Store.DB.Exec(`UPDATE plugin_runs SET state='pending' WHERE id=?`, runs[0].ID); e != nil {
		t.Fatal(e)
	}
	if _, e = cold.pluginStore.ClaimRun(ctx); e == nil {
		t.Fatal("old identity replay admitted after missing effect")
	}
	if e = cold.pluginStore.Reset(ctx, in.InstanceID); e == nil {
		t.Fatal("missing old proof authorized reset")
	}
}
