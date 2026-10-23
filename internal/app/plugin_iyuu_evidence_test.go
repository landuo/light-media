package app

import (
	"context"
	"errors"
	"testing"

	iyuu "light-media/internal/plugins/iyuuautoseed"
)

func iyuuCompletedConfigFixture(t *testing.T) (*App, PluginInstallation, PluginRun) {
	t.Helper()
	a := newTestApp(t)
	run := uninstallInstall(t, a, "OwnedIYUUEvidence", iyuu.Manifest(), map[string]any{"enabled": false, "token": "synthetic-token", "sites": []any{}}, true)
	run = invokeIYUUActualSDK(t, a, run)
	in, e := a.pluginStore.Installation(context.Background(), run.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	return a, in, run
}
func TestIYUUActualEvidenceLossNeverCreatesGenesisOrRetires(t *testing.T) {
	for _, mode := range []string{"owner-delete", "owner-corrupt", "owner-table-drop", "jr-all-delete", "root-and-jr-delete", "run-delete", "deferred-delete", "run-and-deferred-delete", "deferred-corrupt", "run-revision", "run-action"} {
		t.Run(mode, func(t *testing.T) {
			a, in, run := iyuuCompletedConfigFixture(t)
			ctx := context.Background()
			exec := func(q string, args ...any) {
				t.Helper()
				if _, e := a.Store.DB.Exec(q, args...); e != nil {
					t.Fatal(e)
				}
			}
			switch mode {
			case "owner-delete":
				exec("DELETE FROM plugin_iyuu_roots WHERE instance_id=?", in.InstanceID)
			case "owner-corrupt":
				exec("UPDATE plugin_iyuu_roots SET sealed=X'00' WHERE instance_id=?", in.InstanceID)
			case "owner-table-drop":
				exec("DROP TABLE plugin_iyuu_roots")
			case "jr-all-delete", "root-and-jr-delete":
				for _, table := range []string{"iyuu_steps", "iyuu_runs", "iyuu_values", "iyuu_run_manifests", "iyuu_owner_manifests", "iyuu_registry"} {
					exec("DELETE FROM " + table)
				}
				if mode == "root-and-jr-delete" {
					exec("DELETE FROM plugin_iyuu_roots WHERE instance_id=?", in.InstanceID)
				}
			case "run-delete", "run-and-deferred-delete":
				// Simulate page/restore loss of the parent row while preserving its child.
				// Ordinary FK-enforced SQL already rejects this mutation. Only this owned
				// test connection temporarily disables FK enforcement, then restores it.
				conn, e := a.Store.DB.Conn(ctx)
				if e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() {
					_, _ = conn.ExecContext(context.Background(), "PRAGMA foreign_keys=ON")
					_ = conn.Close()
				})
				if _, e = conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); e != nil {
					t.Fatal(e)
				}
				if mode == "run-and-deferred-delete" {
					if _, e = conn.ExecContext(ctx, "DELETE FROM plugin_deferred_operations WHERE run_id=?", run.ID); e != nil {
						t.Fatal(e)
					}
				}
				if _, e = conn.ExecContext(ctx, "DELETE FROM plugin_runs WHERE id=?", run.ID); e != nil {
					t.Fatal(e)
				}
				if _, e = conn.ExecContext(ctx, "PRAGMA foreign_keys=ON"); e != nil {
					t.Fatal(e)
				}
				conn.Close()
			case "deferred-delete":
				exec("DELETE FROM plugin_deferred_operations WHERE run_id=?", run.ID)
			case "deferred-corrupt":
				exec("UPDATE plugin_deferred_operations SET sealed=X'00' WHERE run_id=?", run.ID)
			case "run-revision":
				exec("UPDATE plugin_runs SET config_revision=config_revision+7 WHERE id=?", run.ID)
			case "run-action":
				exec("UPDATE plugin_runs SET action='run' WHERE id=?", run.ID)
			}
			a.pluginIYUU.mu.Lock()
			delete(a.pluginIYUU.owners, in.InstanceID)
			a.pluginIYUU.mu.Unlock()
			if _, e := a.iyuuOwner(ctx, in); !errors.Is(e, iyuu.ErrJournalIntegrity) {
				t.Fatalf("normal cold reader admitted missing evidence: %v", e)
			}
			for _, op := range []string{"reset", "uninstall", "install"} {
				var e error
				switch op {
				case "reset":
					e = a.pluginStore.Reset(ctx, in.InstanceID)
				case "uninstall":
					e = a.pluginStore.Uninstall(ctx, in.InstanceID)
				case "install":
					e = a.pluginStore.Install(ctx, in)
				}
				if !errors.Is(e, iyuu.ErrJournalIntegrity) {
					t.Fatalf("%s admitted broken evidence: %v", op, e)
				}
				current, e := a.pluginStore.Installation(ctx, in.InstanceID)
				if e != nil || current.Generation != in.Generation || !current.Installed {
					t.Fatal("failed transition changed installation", current, e)
				}
			}
			if mode == "owner-table-drop" {
				var n int
				if e := a.Store.DB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='plugin_iyuu_roots'").Scan(&n); e != nil || n != 0 {
					t.Fatal("ordinary missing schema was re-created", n, e)
				}
			}
		})
	}
}

func TestIYUUActualCompletedResultSurvivesLaterCheckpoint(t *testing.T) {
	a, in, old := iyuuCompletedConfigFixture(t)
	ctx := context.Background()
	oldRecord, _, e := a.pluginStore.readDeferred(ctx, a.Store.DB, old)
	if e != nil {
		t.Fatal(e)
	}
	config, e := a.pluginStore.Config(ctx, in.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = a.pluginStore.SaveConfigAndQueue(ctx, in.InstanceID, config.Revision, map[string]any{"enabled": false, "token": "synthetic-token", "sites": []any{}}); e != nil {
		t.Fatal(e)
	}
	newer, e := a.pluginStore.claimRunForInstance(ctx, in.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	newer = invokeIYUUActualSDK(t, a, newer)
	owner, e := a.iyuuOwner(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	checkpoint, found, e := owner.journal.ReadCheckpoint(ctx, in.InstanceID, uint64(in.Generation))
	if e != nil || !found || checkpoint.Run != newer.ID {
		t.Fatal("new actual SDK did not replace checkpoint", checkpoint, found, e)
	}
	result, found, e := a.pluginStore.iyuuDeferredOutcome(ctx, old, oldRecord)
	if e != nil || !found || string(result) != `{"configured":true}` {
		t.Fatalf("old source result was lost after later checkpoint: %s/%v/%v", result, found, e)
	}
}
