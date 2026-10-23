package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins/builtin"
	"reflect"
	"testing"
)

func TestPluginCloneRemovalBlocksActiveAndUnknownAttempt(t *testing.T) {
	for _, state := range []string{"running", "reconcile_pending"} {
		t.Run(state, func(t *testing.T) {
			a := newTestApp(t)
			p := a.pluginStore
			ctx := context.Background()
			m := builtin.SystemProcessManifest()
			installCloneSource(t, p, m)
			clone, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "AttemptOwned"})
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := p.Config(ctx, clone.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = p.SaveConfigAndQueue(ctx, clone.InstanceID, cfg.Revision, map[string]any{"enabled": false})
			if err != nil {
				t.Fatal(err)
			}
			run, err := p.ClaimRun(ctx)
			if err != nil {
				t.Fatal(err)
			}
			execution := executionLeaseActivate(t, p, run)
			defer run.executionLease.runCleanup()
			if state == "reconcile_pending" {
				if _, err = p.store.DB.Exec(`UPDATE plugin_runs SET state='reconcile_pending' WHERE id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
			}
			before := instanceReadTables(t, a)
			if err = p.Uninstall(ctx, clone.InstanceID); err == nil {
				t.Fatal("active/unknown attempt was erased")
			}
			if !reflect.DeepEqual(before, instanceReadTables(t, a)) {
				t.Fatal("rejected uninstall changed state")
			}
			if state == "reconcile_pending" {
				if _, err = p.store.DB.Exec(`UPDATE plugin_runs SET state='running' WHERE id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err = p.FinishRun(execution, run.ID, "succeeded", "", json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			if err = p.releaseSupervisedRun(run); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestPluginCloneRecreationRejectsOldAttemptWrites(t *testing.T) {
	a := newTestApp(t)
	p := a.pluginStore
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	request := PluginCloneRequest{Suffix: "RecreatedOwned"}
	clone, err := p.Clone(ctx, m.PluginID, request)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := p.Config(ctx, clone.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = p.SaveConfigAndQueue(ctx, clone.InstanceID, cfg.Revision, map[string]any{"enabled": false})
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	execution := executionLeaseActivate(t, p, run)
	defer run.executionLease.runCleanup()
	if err = p.FinishRun(execution, run.ID, "succeeded", "", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err = p.releaseSupervisedRun(run); err != nil {
		t.Fatal(err)
	}
	if err = p.Uninstall(ctx, clone.InstanceID); err != nil {
		t.Fatal(err)
	}
	fresh, err := p.Clone(ctx, m.PluginID, request)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Generation <= clone.Generation {
		t.Fatal("recreation reused generation")
	}
	before := instanceReadTables(t, a)
	if err = p.FinishRun(execution, run.ID, "succeeded", "", json.RawMessage(`{"late":true}`)); err == nil {
		t.Fatal("old completion accepted")
	}
	if !reflect.DeepEqual(before, instanceReadTables(t, a)) {
		t.Fatal("old attempt changed fresh clone")
	}
}
