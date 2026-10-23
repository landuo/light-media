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

// Only owned empty-inventory fixtures: no pump, services, remote requests or
// cleanup. This uses actual Clone/start/queue/claim/supervisor/finish paths.
func TestOriginalAbsentEngineInitThenConfigureSameObject(t *testing.T) {
	for _, manifest := range []plugins.Manifest{signin.Manifest(), seed.Manifest()} {
		t.Run(manifest.PluginID, func(t *testing.T) {
			t.Setenv("TZ", "UTC")
			ctx := context.Background()
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			settings := a.Settings()
			settings.Sites = nil
			settings.Downloaders = nil
			if err := a.saveSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			installCloneSource(t, a.pluginStore, manifest)
			originalEngineAppSave(t, a, manifest.PluginID, map[string]any{})
			clone, err := a.pluginStore.Clone(ctx, manifest.PluginID, PluginCloneRequest{Suffix: "AbsentEngine"})
			if err != nil {
				t.Fatal(err)
			}
			assertAbsent := func() {
				t.Helper()
				if _, err := a.pluginStore.Config(ctx, clone.InstanceID); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("clone config must remain absent: %v", err)
				}
			}
			assertAbsent()
			p := a.startOriginalPlugin(ctx, clone.InstanceID)
			if p == nil {
				t.Fatal("actual absent clone start failed")
			}
			actual := originalEngineAppPointer(t, p)
			for n := 0; n < 2; n++ {
				if state, err := p.getState(ctx); err != nil || state {
					t.Fatalf("new default state %v %v", state, err)
				}
				if _, err := p.getServices(ctx); err != nil {
					t.Fatal(err)
				}
				if commands, err := p.getCommands(ctx); err != nil || len(commands) == 0 {
					t.Fatalf("default original commands %v %v", commands, err)
				}
			}
			assertAbsent()
			originalEngineAppRuns(t, a, clone.InstanceID, 1)
			var succeeded int
			if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id=? AND state='succeeded' AND config_revision=0 AND action='init'`, clone.InstanceID).Scan(&succeeded); err != nil || succeeded != 1 {
				t.Fatalf("real absent init terminal %d %v", succeeded, err)
			}
			originalEngineAppSave(t, a, clone.InstanceID, map[string]any{"enabled": false, "onlyonce": false, "cron": "", "downloaders": []any{}, "min_seeding_days": 0, "sign_sites": []any{}, "login_sites": []any{}})
			if err := p.init(ctx); err != nil {
				t.Fatal("actual host init after absent object", err)
			}
			if originalEngineAppPointer(t, p) != actual {
				t.Fatal("configuration replaced actual absent object")
			}
			p.configMu.RLock()
			marker := p.originalAbsentEngine
			p.configMu.RUnlock()
			if marker != nil {
				t.Fatal("configured host retained absent identity marker")
			}
			originalEngineAppRuns(t, a, clone.InstanceID, 2)
		})
	}
}
