package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/uninstall"
)

func attemptRecoveryFixture(t *testing.T) (*App, PluginRun) {
	t.Helper()
	a, in, run := deferredFixture(t)
	ctx := context.Background()
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
	execution := executionLeaseActivate(t, a.pluginStore, run)
	if err = a.pluginStore.failPluginResult(execution, run.ID, "PLUGIN_RESULT_SAVE_FAILED"); err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.releaseSupervisedRun(run); err != nil {
		t.Fatal(err)
	}
	return a, run
}

func TestPluginAttemptRecoveryPreservesOriginalCompletionAndIsIdempotent(t *testing.T) {
	a, run := attemptRecoveryFixture(t)
	p := a.pluginStore
	ctx := context.Background()
	var original []byte
	if err := a.Store.DB.QueryRow(`SELECT completion FROM plugin_run_attempts WHERE run_id=?`, run.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if err := p.recoverPluginRuns(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := p.Run(ctx, run.ID)
	if err != nil || got.State != "succeeded" {
		t.Fatal(got.State, err)
	}
	var restored, first []byte
	var n int
	if err = a.Store.DB.QueryRow(`SELECT completion FROM plugin_run_attempts WHERE run_id=?`, run.ID).Scan(&restored); err != nil || !bytes.Equal(original, restored) {
		t.Fatal("rewrote original completion", err)
	}
	if err = a.Store.DB.QueryRow(`SELECT r.sealed FROM plugin_run_attempt_recoveries r JOIN plugin_run_attempts a ON a.nonce=r.nonce WHERE a.run_id=?`, run.ID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err = p.recoverPluginRuns(ctx); err != nil {
		t.Fatal(err)
	}
	var again []byte
	if err = a.Store.DB.QueryRow(`SELECT r.sealed FROM plugin_run_attempt_recoveries r JOIN plugin_run_attempts a ON a.nonce=r.nonce WHERE a.run_id=?`, run.ID).Scan(&again); err != nil || !bytes.Equal(first, again) {
		t.Fatal("idempotent recovery changed proof", err)
	}
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_attempts WHERE run_id=?`, run.ID).Scan(&n); err != nil || n != 1 {
		t.Fatal("recovery claimed another attempt", n, err)
	}
	deferredStillInstalled(t, a, false)
}

func TestPluginAttemptRecoveryRejectsUnprovenOrCorruptEvidence(t *testing.T) {
	for _, mutation := range []string{"uncertain", "result", "completion", "native", "recovery"} {
		t.Run(mutation, func(t *testing.T) {
			a, run := attemptRecoveryFixture(t)
			p := a.pluginStore
			ctx := context.Background()
			if mutation == "recovery" {
				if err := p.recoverPluginRuns(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := p.recoverDeferredRuns(ctx); err != nil {
					t.Fatal(err)
				}
			}
			switch mutation {
			case "uncertain":
				receipt, previous, err := p.readDeferred(ctx, a.Store.DB, run)
				if err != nil {
					t.Fatal(err)
				}
				receipt.Phase = "uncertain"
				tx, err := a.Store.DB.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err = p.writeDeferred(ctx, tx, receipt, previous); err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
			case "result":
				sealed, err := p.seal("run_result", run.InstanceID, run.ID, []byte(`{"forged":true}`))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET result=? WHERE id=?`, sealed, run.ID); err != nil {
					t.Fatal(err)
				}
			case "completion":
				if _, err := a.Store.DB.Exec(`UPDATE plugin_run_attempts SET completion=x'00' WHERE run_id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
			case "native":
				if _, err := a.Store.DB.Exec(`UPDATE plugin_deferred_operations SET sealed=x'00' WHERE run_id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
			case "recovery":
				if _, err := a.Store.DB.Exec(`UPDATE plugin_run_attempt_recoveries SET sealed=x'00' WHERE nonce=(SELECT nonce FROM plugin_run_attempt_heads WHERE run_id=?)`, run.ID); err != nil {
					t.Fatal(err)
				}
			}
			err := p.quarantineRecoveredAttempts(ctx)
			got, readErr := p.Run(ctx, run.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if err == nil && got.State != "reconcile_pending" {
				t.Fatal("unproven terminal accepted", mutation, got.State)
			}
			var count int
			if e := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_attempt_recoveries`).Scan(&count); e != nil {
				t.Fatal(e)
			}
			if mutation != "recovery" && count != 0 {
				t.Fatal("bad evidence created recovery proof")
			}
		})
	}
}

func TestPluginAttemptRecoveryProofInsertRollsBack(t *testing.T) {
	a, run := attemptRecoveryFixture(t)
	p := a.pluginStore
	ctx := context.Background()
	if err := p.recoverDeferredRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER reject_attempt_recovery BEFORE INSERT ON plugin_run_attempt_recoveries BEGIN SELECT RAISE(ABORT,'owned recovery proof insert failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := p.quarantineRecoveredAttempts(ctx); err == nil || !strings.Contains(err.Error(), "owned recovery proof insert failed") {
		t.Fatal("did not reach proof insert rollback", err)
	}
	var n int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_attempt_recoveries`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	got, err := p.Run(ctx, run.ID)
	if err != nil || got.State != "succeeded" {
		t.Fatal("rollback changed prior native recovery", got.State, err)
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER reject_attempt_recovery`); err != nil {
		t.Fatal(err)
	}
	if err = p.quarantineRecoveredAttempts(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPluginAttemptRecoveryColdReopenKeepsNativeProof(t *testing.T) {
	a, run := attemptRecoveryFixture(t)
	ctx := context.Background()
	dir := a.Dir
	if err := a.pluginStore.recoverPluginRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if err = cold.pluginStore.recoverPluginRuns(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := cold.pluginStore.Run(ctx, run.ID)
	if err != nil || got.State != "succeeded" {
		t.Fatal(got.State, err)
	}
	var n int
	if err = cold.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_attempts WHERE run_id=?`, run.ID).Scan(&n); err != nil || n != 1 {
		t.Fatal("cold repair created a new attempt", n, err)
	}
	before := lifecycleInstallation(t, cold.pluginStore, run.InstanceID)
	if err = cold.pluginStore.Reset(ctx, run.InstanceID); err != nil {
		t.Fatal("proven recovered completion blocked reset", err)
	}
	after := lifecycleInstallation(t, cold.pluginStore, run.InstanceID)
	if after.Generation != before.Generation+1 {
		t.Fatal("reset did not advance exact instance")
	}

	deferredStillInstalled(t, cold, false)
}

func TestPluginAttemptSelfUninstallRevisionUsesExactSource(t *testing.T) {
	for _, mutation := range []string{"valid", "missing", "initial", "input", "phase", "list"} {
		t.Run(mutation, func(t *testing.T) {
			a, _, run := deferredFixture(t)
			ctx := context.Background()
			p := a.pluginStore
			cfg, err := p.Config(ctx, run.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = p.SaveConfigAndQueue(ctx, run.InstanceID, cfg.Revision, map[string]any{"plugin_ids": []string{"CategoryEditor", "PluginUnInstall"}}); err != nil {
				t.Fatal(err)
			}
			if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET state='canceled' WHERE id=?`, run.ID); err != nil {
				t.Fatal(err)
			}
			run, err = p.ClaimRun(ctx)
			if err != nil {
				t.Fatal(err)
			}
			admission := run.executionLease.claim.admission
			uninstallApply(t, a, &run)
			source, _, err := p.readUninstallSource(ctx, a.Store.DB, run)
			if err != nil {
				t.Fatal(err)
			}
			if source.InitialRevision != admission.AdmissionRevision || source.FinalRevision <= source.InitialRevision {
				t.Fatal("fixture did not exercise self revision")
			}
			switch mutation {
			case "missing":
				if _, err = a.Store.DB.Exec(`DELETE FROM plugin_uninstall_source WHERE run_id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
			case "list":
				if _, err = a.Store.DB.Exec(`DELETE FROM plugin_uninstall_source_lists WHERE run_id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
			case "initial", "input", "phase":
				if mutation == "initial" {
					source.InitialRevision++
				}
				if mutation == "input" {
					source.InputSHA = strings.Repeat("0", 64)
				}
				if mutation == "phase" {
					source.Phase = "started"
				}
				raw, _ := json.Marshal(source)
				sealed, e := p.seal("uninstall_result", run.InstanceID, "source:v1:"+run.ID, raw)
				if e != nil {
					t.Fatal(e)
				}
				if _, err = a.Store.DB.Exec(`UPDATE plugin_uninstall_source SET sealed=? WHERE run_id=?`, sealed, run.ID); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			_, err = p.runJournal.runForAdmission(ctx, tx, admission)
			if (mutation == "valid") != (err == nil) {
				t.Fatal(mutation, err)
			}
		})
	}
}

func TestPluginAttemptResetDoesNotCreateOrIgnoreRecoveryProof(t *testing.T) {
	for _, mode := range []string{"missing", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			a, run := attemptRecoveryFixture(t)
			p := a.pluginStore
			ctx := context.Background()
			if err := p.recoverPluginRuns(ctx); err != nil {
				t.Fatal(err)
			}
			before := lifecycleInstallation(t, p, run.InstanceID)
			query := `DELETE FROM plugin_run_attempt_recoveries`
			if mode == "corrupt" {
				query = `UPDATE plugin_run_attempt_recoveries SET sealed=x'00'`
			}
			if _, err := a.Store.DB.Exec(query); err != nil {
				t.Fatal(err)
			}
			if err := p.Reset(ctx, run.InstanceID); err == nil {
				t.Fatal("reset accepted missing/bad repair proof")
			}
			after := lifecycleInstallation(t, p, run.InstanceID)
			if after.Generation != before.Generation || !bytes.Equal(after.Manifest, before.Manifest) {
				t.Fatal("failed reset changed lifecycle")
			}
			var n int
			if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_attempt_recoveries`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if mode == "missing" && n != 0 {
				t.Fatal("reset minted recovery proof")
			}
		})
	}
}
