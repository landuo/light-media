package app

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"light-media/internal/plugins"
	signin "light-media/internal/plugins/autosignin"
	seed "light-media/internal/plugins/cleaninvalidseed"
)

// These tests enter actual App start/direct init and the normal SDK/deferred
// host pipeline. No fake owner map, manufactured run, receipt or source plan.
// No App.Start, pump, sign-in, cleanup, downloader, site or filesystem work.
func originalEngineDiagnosticSave(t *testing.T, a *App, id string, config map[string]any) {
	t.Helper()
	stored, err := a.pluginStore.Config(context.Background(), id)
	revision := int64(0)
	if err == nil {
		revision = stored.Revision
	} else if !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.SaveConfig(context.Background(), id, revision, config); err != nil {
		t.Fatal(err)
	}
}
func originalEngineDiagnosticRuns(t *testing.T, a *App, id string, want int) {
	t.Helper()
	var total, nonInit int
	if err := a.Store.DB.QueryRow(`SELECT count(*),COALESCE(sum(CASE WHEN action<>'init' THEN 1 ELSE 0 END),0) FROM plugin_runs WHERE instance_id=?`, id).Scan(&total, &nonInit); err != nil {
		t.Fatal(err)
	}
	if total != want || nonInit != 0 {
		t.Fatalf("actual admitted runs=%d non-init=%d want=%d", total, nonInit, want)
	}
}
func originalEngineDiagnosticPointer(t *testing.T, p *originalPluginInstance) any {
	t.Helper()
	p.configMu.RLock()
	actual := p.nativeState
	p.configMu.RUnlock()
	switch p.installation.PluginID {
	case "AutoSignIn":
		engine, release, err := originalLoadedSignInEngine(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		if actual != engine || engine == nil || engine.Effects != nil {
			t.Fatal("actual admitted Engine missing or retained run callback")
		}
	case "CleanInvalidSeed":
		machine, release, err := originalLoadedCleanSeedMachine(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		if actual != machine || machine == nil || machine.Host != nil {
			t.Fatal("actual admitted Machine missing or retained run Host")
		}
	default:
		t.Fatal("unsupported fixture")
	}
	return actual
}
func TestOriginalEngineAppDiagnosticV2(t *testing.T) {
	for _, manifest := range []plugins.Manifest{signin.Manifest(), seed.Manifest()} {
		t.Run(manifest.PluginID, func(t *testing.T) {
			t.Setenv("TZ", "UTC")
			ctx := context.Background()
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			// No synthetic remote endpoint is needed: empty inventories and no onlyonce
			// mean actual initialization has no business I/O to perform.
			settings := a.Settings()
			settings.Sites = nil
			settings.Downloaders = nil
			if err := a.saveSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			installCloneSource(t, a.pluginStore, manifest)
			cfg := map[string]any{"enabled": true, "onlyonce": false, "notify": false, "cron": "0 1 * * *", "sign_sites": []any{}, "login_sites": []any{}, "downloaders": []any{}, "min_seeding_days": 0, "download_dirs": ""}
			originalEngineDiagnosticSave(t, a, manifest.PluginID, cfg)
			p := a.startOriginalPlugin(ctx, manifest.PluginID)
			if p == nil {
				originalEngineDiagnosticDump(t, a, manifest.PluginID)
				t.Fatal("actual original start failed")
			}
			first := originalEngineDiagnosticPointer(t, p)
			originalEngineDiagnosticRuns(t, a, manifest.PluginID, 1)
			for n := 0; n < 2; n++ {
				state, err := p.getState(ctx)
				if err != nil || !state {
					t.Fatalf("actual state %v %v", state, err)
				}
				services, err := p.getServices(ctx)
				if err != nil || len(services) != 1 || services[0].ID != manifest.PluginID {
					t.Fatalf("actual services %+v %v", services, err)
				}
				commands, err := p.getCommands(ctx)
				if err != nil || len(commands) == 0 {
					t.Fatalf("actual commands %+v %v", commands, err)
				}
				if originalEngineDiagnosticPointer(t, p) != first {
					t.Fatal("getters replaced actual object")
				}
			}
			originalEngineDiagnosticRuns(t, a, manifest.PluginID, 1)
			// Empty direct init is an original same-object operation, not restart or
			// reconstruction from the newly saved empty config.
			originalEngineDiagnosticSave(t, a, manifest.PluginID, map[string]any{})
			if err := p.init(ctx); err != nil {
				originalEngineDiagnosticDump(t, a, manifest.PluginID)
				t.Fatal("actual direct empty init", err)
			}
			if originalEngineDiagnosticPointer(t, p) != first {
				t.Fatal("direct init replaced actual pointer")
			}
			if state, err := p.getState(ctx); err != nil || !state {
				t.Fatalf("direct empty init discarded members %v %v", state, err)
			}
			originalEngineDiagnosticRuns(t, a, manifest.PluginID, 2)
			sibling, err := a.pluginStore.Clone(ctx, manifest.PluginID, PluginCloneRequest{Suffix: "EngineSibling"})
			if err != nil {
				t.Fatal(err)
			}
			q := a.startOriginalPlugin(ctx, sibling.InstanceID)
			if q == nil {
				t.Fatal("actual clone start failed")
			}
			clonePointer := originalEngineDiagnosticPointer(t, q)
			if clonePointer == first {
				t.Fatal("clone aliases source members")
			}
			if state, err := q.getState(ctx); err != nil || state {
				t.Fatalf("clone must start disabled %v %v", state, err)
			}
			originalEngineDiagnosticRuns(t, a, sibling.InstanceID, 1)
			// New logical object in the same generation must not adopt old owner's
			// retained machine. This is distinct from direct p.init above.
			originalEngineDiagnosticSave(t, a, manifest.PluginID, map[string]any{})
			fresh := a.startOriginalPlugin(ctx, manifest.PluginID)
			if fresh == nil {
				t.Fatal("actual fresh start failed")
			}
			if got := originalEngineDiagnosticPointer(t, fresh); got == first || got == clonePointer {
				t.Fatal("new start reused another logical object")
			}
			if state, err := fresh.getState(ctx); err != nil || state {
				t.Fatalf("fresh empty object inherited prior state %v %v", state, err)
			}
			if originalEngineDiagnosticPointer(t, q) != clonePointer {
				t.Fatal("source restart disturbed clone")
			}
			originalEngineDiagnosticRuns(t, a, manifest.PluginID, 3)
			originalEngineDiagnosticRuns(t, a, sibling.InstanceID, 1)
		})
	}
}

func originalEngineDiagnosticDump(t *testing.T, a *App, id string) {
	t.Helper()
	ctx := context.Background()
	rows, err := a.Store.DB.Query(`SELECT id,state,error_code,config_revision FROM plugin_runs WHERE instance_id=? ORDER BY created_at,id`, id)
	if err != nil {
		t.Logf("diagnostic run query: %v", err)
		return
	}
	for rows.Next() {
		var run, state, code string
		var revision int64
		if err := rows.Scan(&run, &state, &code, &revision); err != nil {
			t.Log(err)
			break
		}
		t.Logf("diagnostic run=%s state=%s code=%s revision=%d", run, state, code, revision)
	}
	rows.Close()
	if id == "AutoSignIn" {
		a.pluginAutoSignIn.mu.Lock()
		slot := a.pluginAutoSignIn.owners[id]
		a.pluginAutoSignIn.mu.Unlock()
		if slot == nil {
			t.Log("diagnostic no signin slot")
			return
		}
		<-slot.ready
		slot.gate <- struct{}{}
		defer func() { <-slot.gate }()
		if slot.owner == nil {
			t.Logf("diagnostic no owner err=%v", slot.err)
			return
		}
		t.Logf("diagnostic actual Engine retained=%v", slot.owner.originalEngine != nil)
		tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Log(err)
			return
		}
		defer tx.Rollback()
		loaded, err := slot.owner.readTx(ctx, tx)
		if err != nil {
			t.Logf("diagnostic sealed signin read: %v", err)
			return
		}
		for _, run := range loaded.State.Runs {
			t.Logf("diagnostic signin source run=%s phase=%s action=%s public_defs=%v runtime_epoch_matches=%v", run.ID, run.Phase, run.Action, signinAppliedKind(run, "service.definitions"), loaded.State.RuntimeEpoch == loaded.State.Epoch)
		}
	} else {
		a.pluginCleanInvalidSeed.mu.Lock()
		o := a.pluginCleanInvalidSeed.owners[id]
		a.pluginCleanInvalidSeed.mu.Unlock()
		if o == nil {
			t.Log("diagnostic no clean owner")
			return
		}
		if err := o.lock(ctx); err != nil {
			t.Log(err)
			return
		}
		defer o.unlock()
		tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Log(err)
			return
		}
		defer tx.Rollback()
		state, meta, err := o.j.read(ctx, tx)
		if err != nil {
			t.Logf("diagnostic clean sealed read: %v", err)
			return
		}
		t.Logf("diagnostic clean actual_machine=%v binding_revision=%d closed=%v applied_revision=%d transition_present=%v source_runs=%d", o.originalMachine != nil, state.Binding.Revision, meta.Closed, meta.AppliedBinding.Revision, meta.Transition != nil, len(state.Runs))
		if meta.Transition != nil {
			t.Logf("diagnostic clean transition kind=%s phase=%s run=%s to_revision=%d", meta.Transition.Kind, meta.Transition.Phase, meta.Transition.RunID, meta.Transition.To.Revision)
		}
		for _, run := range state.Runs {
			t.Logf("diagnostic clean source run=%s phase=%s action=%s revision=%d", run.Invocation.RunID, run.Phase, run.Invocation.Action, run.Invocation.Binding.Revision)
		}
	}
}
