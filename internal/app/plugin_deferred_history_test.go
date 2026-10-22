package app

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/uninstall"
)

func TestPluginDeferredHistoryRetainsTerminalAcrossLifecycle(t *testing.T) {
	for _, lifecycle := range []string{"none", "reset", "uninstall"} {
		t.Run(lifecycle, func(t *testing.T) {
			a, d, run, ctx := deferredRevisionFixture(t)
			if err := a.pluginStore.patchRunConfig(ctx, &run, map[string]json.RawMessage{"test_once": json.RawMessage(`false`)}); err != nil {
				t.Fatal(err)
			}
			result := json.RawMessage(`{"synthetic":"already-completed-no-replay"}`)
			if err := d.changePhase(ctx, "started", "confirmed", result); err != nil {
				t.Fatal(err)
			}
			var err error
			switch lifecycle {
			case "reset":
				err = a.pluginStore.Reset(ctx, run.InstanceID)
			case "uninstall":
				err = a.pluginStore.Uninstall(ctx, run.InstanceID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = a.pluginStore.recoverDeferredRuns(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := a.pluginStore.Run(ctx, run.ID)
			if err != nil || got.State != "succeeded" || !bytes.Equal(got.Result, result) {
				t.Fatal(got.State, string(got.Result), err)
			}
			if err = a.pluginStore.recoverDeferredRuns(ctx); err != nil {
				t.Fatal("idempotent recovery", err)
			}
			deferredStillInstalled(t, a, true)
		})
	}
}

func TestPluginDeferredHistoryNeverReplaysEvolvedOperations(t *testing.T) {
	for _, phase := range []string{"prepared", "started", "uncertain"} {
		t.Run(phase, func(t *testing.T) {
			a, in, run := deferredFixture(t)
			ctx := context.Background()
			d, err := a.preparePluginDeferred(ctx, in, run)
			if err != nil {
				t.Fatal(err)
			}
			if phase != "prepared" {
				if err = d.changePhase(ctx, "prepared", "started", nil); err != nil {
					t.Fatal(err)
				}
			}
			if err = a.pluginStore.patchRunConfig(ctx, &run, map[string]json.RawMessage{"test_once": json.RawMessage(`false`)}); err != nil {
				t.Fatal(err)
			}
			if phase == "uncertain" {
				if err = d.changePhase(ctx, "started", "uncertain", nil); err != nil {
					t.Fatal(err)
				}
			}
			if err = a.pluginStore.recoverDeferredRuns(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := a.pluginStore.Run(ctx, run.ID)
			want := "reconcile_pending"
			if phase == "prepared" {
				want = "canceled"
			}
			if err != nil || got.State != want {
				t.Fatal(phase, got.State, err)
			}
			if _, err = a.retryPluginDeferred(ctx, run.InstanceID, run.ID); err == nil {
				t.Fatal("evolved operation replay accepted")
			}
			deferredStillInstalled(t, a, true)
		})
	}
}

func TestPluginDeferredHistoryRejectsMissingCorruptAndRollbackProof(t *testing.T) {
	for _, mode := range []string{"missing", "corrupt", "rollback", "input"} {
		t.Run(mode, func(t *testing.T) {
			a, d, run, ctx := deferredRevisionFixture(t)
			old := run
			if err := a.pluginStore.patchRunConfig(ctx, &run, map[string]json.RawMessage{"test_once": json.RawMessage(`false`)}); err != nil {
				t.Fatal(err)
			}
			if err := d.changePhase(ctx, "started", "confirmed", json.RawMessage(`{"synthetic":true}`)); err != nil {
				t.Fatal(err)
			}
			var err error
			switch mode {
			case "missing":
				_, err = a.Store.DB.Exec(`DELETE FROM plugin_run_config_transitions WHERE run_id=?`, run.ID)
			case "corrupt":
				_, err = a.Store.DB.Exec(`UPDATE plugin_run_config_transitions SET sealed=x'00' WHERE run_id=?`, run.ID)
			case "rollback":
				_, err = a.Store.DB.Exec(`UPDATE plugin_runs SET config_revision=? WHERE id=?`, old.ConfigRevision, run.ID)
			case "input":
				sealed, e := a.pluginStore.seal("run", run.InstanceID, run.ID, []byte(`{}`))
				if e != nil {
					t.Fatal(e)
				}
				_, err = a.Store.DB.Exec(`UPDATE plugin_runs SET input=? WHERE id=?`, sealed, run.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = a.pluginStore.recoverDeferredRuns(ctx); err == nil {
				t.Fatal("invalid historical proof accepted")
			}
			got, _ := a.pluginStore.Run(ctx, run.ID)
			if got.State == "succeeded" {
				t.Fatal("false repaired success")
			}
			deferredStillInstalled(t, a, true)
		})
	}
}

func TestPluginDeferredHistoryPendingSnapshotChainCancelsPrepared(t *testing.T) {
	a, in, run := deferredFixture(t)
	ctx := context.Background()
	p := a.pluginStore
	d, err := a.preparePluginDeferred(ctx, in, run)
	if err != nil {
		t.Fatal(err)
	}
	_, before, err := p.readDeferred(ctx, a.Store.DB, run)
	if err != nil {
		t.Fatal(err)
	}
	old, err := p.Config(ctx, run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET state='pending' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"plugin_ids":["CategoryEditor"],"test_once":false}`)
	value, err := p.saveValueTx(ctx, tx, run.InstanceID, "config", "main", old.Revision, raw)
	if err == nil {
		err = p.propagateInstanceConfigTx(ctx, tx, run.InstanceID, run.Generation, old.Revision, value.Revision, old.Value, raw)
	}
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	evolved, err := p.Run(ctx, run.ID)
	if err != nil || bytes.Equal(evolved.Input, run.Input) {
		t.Fatal("pending input not propagated", err)
	}
	// Recovery normally selects unfinished running/reconcile-pending invocations.
	if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET state='reconcile_pending' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if err = p.recoverDeferredRuns(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := p.Run(ctx, run.ID)
	if err != nil || got.State != "canceled" {
		t.Fatal(got.State, err)
	}
	receipt, after, err := p.readDeferred(ctx, a.Store.DB, d.run)
	if err != nil || !bytes.Equal(before, after) || receipt.Revision != run.ConfigRevision {
		t.Fatal("old authorization was rewritten", err)
	}
	deferredStillInstalled(t, a, true)
}

func TestPluginDeferredHistoryNativeSelfResetBridgeAndColdQuarantine(t *testing.T) {
	for _, mode := range []string{"confirmed", "started", "missing-list", "corrupt-source", "wrong-initial"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			a, err := New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			open := true
			t.Cleanup(func() {
				if open {
					_ = a.Close()
				}
			})
			ctx := context.Background()
			uninstallInstall(t, a, "CategoryEditor", builtin.CategoryEditorManifest(), map[string]any{"enabled": false}, false)
			run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"PluginUnInstall", "CategoryEditor"}}, true)
			in, err := a.pluginStore.Installation(ctx, run.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			d, err := a.preparePluginDeferred(ctx, in, run)
			if err != nil {
				t.Fatal(err)
			}
			ack, err := d.handshake(ctx, deferredIdentity(in), uninstall.ApplyCapability, json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			if err = d.ready(plugins.RunResult{HostCalls: 1, Result: ack}); err != nil {
				t.Fatal(err)
			}
			if _, err = d.execute(ctx, &run); err != nil {
				t.Fatal(err)
			}
			receipt, previous, err := a.pluginStore.readDeferred(ctx, a.Store.DB, run)
			if err != nil {
				t.Fatal(err)
			}
			if receipt.ConfigSHA == "" || run.ConfigRevision <= receipt.Revision {
				t.Fatal("native bridge fixture did not evolve anchored receipt")
			}
			var edges int
			if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_config_transitions WHERE run_id=?`, run.ID).Scan(&edges); err != nil || edges != 0 {
				t.Fatal("fixture unexpectedly used generic chain", edges, err)
			}
			if err = a.pluginStore.validateDeferredHistory(ctx, receipt, run); err != nil {
				t.Fatal("native bridge rejected", err)
			}
			if mode != "confirmed" {
				source, oldSource, e := a.pluginStore.readUninstallSource(ctx, a.Store.DB, run)
				if e != nil {
					t.Fatal(e)
				}
				source.Phase = "started"
				source.Result = nil
				if mode == "wrong-initial" {
					source.InitialRevision++
				}
				receipt.Phase = "started"
				receipt.Result = nil
				tx, e := a.Store.DB.BeginTx(ctx, nil)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = a.pluginStore.writeUninstallSourceTx(ctx, tx, source, oldSource); e == nil {
					e = a.pluginStore.writeDeferred(ctx, tx, receipt, previous)
				}
				if e != nil {
					tx.Rollback()
					t.Fatal(e)
				}
				if e = tx.Commit(); e != nil {
					t.Fatal(e)
				}
				if mode == "missing-list" {
					_, err = a.Store.DB.Exec(`DELETE FROM plugin_uninstall_source_lists WHERE run_id=?`, run.ID)
				}
				if mode == "corrupt-source" {
					_, err = a.Store.DB.Exec(`UPDATE plugin_uninstall_source SET sealed=x'00' WHERE run_id=?`, run.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			// Reinstall only this synthetic target, proving recovery never replays the
			// already-started native removal against a new installed generation.
			manifest, _ := json.Marshal(builtin.CategoryEditorManifest())
			if err = a.pluginStore.Install(ctx, PluginInstallation{InstanceID: "CategoryEditor", PluginID: "CategoryEditor", Version: builtin.CategoryEditorManifest().Version, ManifestSchemaVersion: 1, Manifest: manifest, DesiredEnabled: true}); err != nil {
				t.Fatal(err)
			}
			if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET state='reconcile_pending' WHERE id=?`, run.ID); err != nil {
				t.Fatal(err)
			}
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			open = false
			cold, err := New(Options{DataDir: dir})
			if err != nil {
				t.Fatal("one synthetic receipt blocked cold App", err)
			}
			t.Cleanup(func() { _ = cold.Close() })
			got, err := cold.pluginStore.Run(ctx, run.ID)
			want := "reconcile_pending"
			if mode == "confirmed" {
				want = "succeeded"
			}
			if err != nil || got.State != want {
				t.Fatal(mode, got.State, got.ErrorCode, err)
			}
			if mode != "confirmed" && mode != "started" && got.ErrorCode != "PLUGIN_UNINSTALL_EVIDENCE_INVALID" {
				t.Fatal("missing native evidence hidden", got.ErrorCode)
			}
			if response := callTest(t, cold.Handler(), "GET", "/healthz", "", nil, ""); response.Code != 200 {
				t.Fatal(response.Code)
			}
			deferredStillInstalled(t, cold, true)
		})
	}
}
