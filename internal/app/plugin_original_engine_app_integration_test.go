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
func originalEngineAppSave(t *testing.T, a *App, id string, config map[string]any) {
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
func originalEngineAppRuns(t *testing.T, a *App, id string, want int) {
	t.Helper()
	var total, nonInit int
	if err := a.Store.DB.QueryRow(`SELECT count(*),COALESCE(sum(CASE WHEN action<>'init' THEN 1 ELSE 0 END),0) FROM plugin_runs WHERE instance_id=?`, id).Scan(&total, &nonInit); err != nil {
		t.Fatal(err)
	}
	if total != want || nonInit != 0 {
		t.Fatalf("actual admitted runs=%d non-init=%d want=%d", total, nonInit, want)
	}
}
func originalEngineAppPointer(t *testing.T, p *originalPluginInstance) any {
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
func TestOriginalEngineAppStartRepeatAndClone(t *testing.T) {
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
			originalEngineAppSave(t, a, manifest.PluginID, cfg)
			p := a.startOriginalPlugin(ctx, manifest.PluginID)
			if p == nil {
				t.Fatal("actual original start failed")
			}
			first := originalEngineAppPointer(t, p)
			originalEngineAppRuns(t, a, manifest.PluginID, 1)
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
				if originalEngineAppPointer(t, p) != first {
					t.Fatal("getters replaced actual object")
				}
			}
			originalEngineAppRuns(t, a, manifest.PluginID, 1)
			// Empty direct init is an original same-object operation, not restart or
			// reconstruction from the newly saved empty config.
			originalEngineAppSave(t, a, manifest.PluginID, map[string]any{})
			if err := p.init(ctx); err != nil {
				t.Fatal("actual direct empty init", err)
			}
			if originalEngineAppPointer(t, p) != first {
				t.Fatal("direct init replaced actual pointer")
			}
			if state, err := p.getState(ctx); err != nil || !state {
				t.Fatalf("direct empty init discarded members %v %v", state, err)
			}
			originalEngineAppRuns(t, a, manifest.PluginID, 2)
			sibling, err := a.pluginStore.Clone(ctx, manifest.PluginID, PluginCloneRequest{Suffix: "EngineSibling"})
			if err != nil {
				t.Fatal(err)
			}
			q := a.startOriginalPlugin(ctx, sibling.InstanceID)
			if q == nil {
				t.Fatal("actual clone start failed")
			}
			clonePointer := originalEngineAppPointer(t, q)
			if clonePointer == first {
				t.Fatal("clone aliases source members")
			}
			if state, err := q.getState(ctx); err != nil || state {
				t.Fatalf("clone must start disabled %v %v", state, err)
			}
			originalEngineAppRuns(t, a, sibling.InstanceID, 1)
			// New logical object in the same generation must not adopt old owner's
			// retained machine. This is distinct from direct p.init above.
			originalEngineAppSave(t, a, manifest.PluginID, map[string]any{})
			fresh := a.startOriginalPlugin(ctx, manifest.PluginID)
			if fresh == nil {
				t.Fatal("actual fresh start failed")
			}
			if got := originalEngineAppPointer(t, fresh); got == first || got == clonePointer {
				t.Fatal("new start reused another logical object")
			}
			if state, err := fresh.getState(ctx); err != nil || state {
				t.Fatalf("fresh empty object inherited prior state %v %v", state, err)
			}
			if originalEngineAppPointer(t, q) != clonePointer {
				t.Fatal("source restart disturbed clone")
			}
			originalEngineAppRuns(t, a, manifest.PluginID, 3)
			originalEngineAppRuns(t, a, sibling.InstanceID, 1)
		})
	}
}
