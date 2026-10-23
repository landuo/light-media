package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestPluginAutoBackupCanceledRecoveryRequiresNativeEvidence(t *testing.T) {
	for _, mode := range []string{"authenticated", "forged-row", "broken-root", "broken-deferred", "missing-attestation", "broken-attestation", "changed-result"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a := newTestApp(t)
			cookie := autoTestInstall(t, a)
			a.pluginAutoBackup.afterEffect = func(kind string) error {
				if kind == "archive_write" {
					return errors.New("owned lost acknowledgment")
				}
				return nil
			}
			if _, err := a.pluginStore.EnqueueRun(ctx, "AutoBackup", "run", nil); err != nil {
				t.Fatal(err)
			}
			run := autoTestExecute(t, a, autoTestClaim(t, a))
			a.pluginAutoBackup.afterEffect = nil
			if run.State != "reconcile_pending" {
				t.Fatal("unknown fixture", run.State)
			}
			if mode != "forged-row" {
				body, _ := json.Marshal(map[string]any{"run_id": run.ID, "confirm_abandon": true})
				w := callTest(t, a.Handler(), "POST", "/api/plugins/AutoBackup/abandon-unknown", string(body), cookie, "")
				if w.Code != 200 {
					t.Fatal("authenticated abandon", w.Code, w.Body.String())
				}
			}
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			head, err := a.pluginStore.runJournal.loadHead(ctx, tx, run.InstanceID, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			original := append([]byte(nil), head.completion...)
			switch mode {
			case "forged-row":
				_, err = tx.ExecContext(ctx, `UPDATE plugin_runs SET state='canceled',error_code='AUTO_BACKUP_ABANDONED_UNKNOWN' WHERE id=?`, run.ID)
			case "changed-result":
				var sealed []byte
				sealed, err = a.pluginStore.seal("run_result", run.InstanceID, run.ID, []byte(`{"foreign":true}`))
				if err == nil {
					_, err = tx.ExecContext(ctx, `UPDATE plugin_runs SET result=? WHERE id=?`, sealed, run.ID)
				}
			case "broken-root":
				_, err = tx.ExecContext(ctx, `UPDATE plugin_auto_backup_state SET sealed=X'00' WHERE instance_id=? AND generation=?`, run.InstanceID, run.Generation)
			case "broken-deferred":
				_, err = tx.ExecContext(ctx, `UPDATE plugin_deferred_operations SET sealed=X'00' WHERE run_id=?`, run.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "missing-attestation" {
				if _, err = tx.ExecContext(ctx, `DELETE FROM plugin_run_attempt_recoveries WHERE nonce=?`, head.claim.Nonce); err != nil {
					t.Fatal(err)
				}
				ok, e := a.pluginStore.runJournal.nativeCompletionRecoveryTx(ctx, tx, head, false)
				if ok || e != nil {
					t.Fatal("read-only recovery manufactured proof", ok, e)
				}
			} else {
				ok, e := a.pluginStore.runJournal.reconcileNativeCompletionTx(ctx, tx, head)
				want := mode == "authenticated" || mode == "broken-attestation"
				if ok != want || (want && e != nil) {
					t.Fatal("native cancellation classification", mode, ok, e)
				}
				if want {
					if mode == "broken-attestation" {
						if _, err = tx.ExecContext(ctx, `UPDATE plugin_run_attempt_recoveries SET sealed=X'00' WHERE nonce=?`, head.claim.Nonce); err != nil {
							t.Fatal(err)
						}
					}
					ok, e = a.pluginStore.runJournal.nativeCompletionRecoveryTx(ctx, tx, head, false)
					if mode == "authenticated" {
						if !ok || e != nil {
							t.Fatal("valid durable proof rejected", e)
						}
					} else if ok || e == nil {
						t.Fatal("bad durable proof accepted", ok, e)
					}
				}
			}
			current, err := a.pluginStore.runJournal.loadHead(ctx, tx, run.InstanceID, run.ID)
			if err != nil || !bytes.Equal(current.completion, original) {
				t.Fatal("original completion changed", err)
			}
			var retries int
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_run_attempt_retries WHERE nonce=?`, head.claim.Nonce).Scan(&retries); err != nil || retries != 0 {
				t.Fatal("cancellation granted retry", retries, err)
			}
		})
	}
}

func TestPluginAutoBackupAuthenticatedAbandonImmediateResetAndProofRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "immediate-reset", true: "proof-insert-rollback"}[fail], func(t *testing.T) {
			ctx := context.Background()
			a := newTestApp(t)
			cookie := autoTestInstall(t, a)
			a.pluginAutoBackup.afterEffect = func(kind string) error {
				if kind == "archive_write" {
					return errors.New("owned unknown")
				}
				return nil
			}
			if _, e := a.pluginStore.EnqueueRun(ctx, "AutoBackup", "run", nil); e != nil {
				t.Fatal(e)
			}
			run := autoTestExecute(t, a, autoTestClaim(t, a))
			a.pluginAutoBackup.afterEffect = nil
			if run.State != "reconcile_pending" {
				t.Fatal("unknown fixture", run.State)
			}
			before, _, e := a.pluginStore.autoBackupRead(ctx, a.Store.DB, run.InstanceID, run.Generation)
			if e != nil {
				t.Fatal(e)
			}
			if fail {
				if _, e = a.Store.DB.Exec(`CREATE TRIGGER owned_abandon_proof_failure BEFORE INSERT ON plugin_run_attempt_recoveries BEGIN SELECT RAISE(ABORT,'owned abandonment proof failure'); END`); e != nil {
					t.Fatal(e)
				}
			}
			body, _ := json.Marshal(map[string]any{"run_id": run.ID, "confirm_abandon": true})
			w := callTest(t, a.Handler(), "POST", "/api/plugins/AutoBackup/abandon-unknown", string(body), cookie, "")
			if fail {
				if w.Code == 200 {
					t.Fatal("proof failure committed")
				}
				after, _, e := a.pluginStore.autoBackupRead(ctx, a.Store.DB, run.InstanceID, run.Generation)
				if e != nil || !bytes.Equal(mustCleanSeedJSON(before), mustCleanSeedJSON(after)) {
					t.Fatal("native state did not rollback", e)
				}
				got, e := a.pluginStore.Run(ctx, run.ID)
				if e != nil || got.State != run.State || got.ErrorCode != run.ErrorCode {
					t.Fatal("run did not rollback", e)
				}
				return
			}
			if w.Code != 200 {
				t.Fatal("abandon", w.Code, w.Body.String())
			}
			if w = callTest(t, a.Handler(), "POST", "/api/plugins/AutoBackup/reset", "", cookie, ""); w.Code != 200 {
				t.Fatal("immediate reset", w.Code, w.Body.String())
			}
		})
	}
}
