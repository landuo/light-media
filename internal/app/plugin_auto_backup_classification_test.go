package app

import (
	"context"
	"errors"
	"testing"
)

func TestPluginAutoBackupUnknownClassificationRejectsForgedDiagnostics(t *testing.T) {
	for _, mode := range []string{"valid-root-forged-label", "invalid-deferred"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a := newTestApp(t)
			autoTestInstall(t, a)
			a.pluginAutoBackup.afterEffect = func(kind string) error {
				if kind == "archive_write" {
					return errors.New("owned classification lost acknowledgment")
				}
				return nil
			}
			if _, err := a.pluginStore.EnqueueRun(ctx, "AutoBackup", "run", nil); err != nil {
				t.Fatal(err)
			}
			run := autoTestExecute(t, a, autoTestClaim(t, a))
			a.pluginAutoBackup.afterEffect = nil
			if run.State != "reconcile_pending" {
				t.Fatal("fixture did not retain unknown", run.State)
			}
			if _, _, err := a.pluginStore.autoBackupRead(ctx, a.Store.DB, run.InstanceID, run.Generation); err != nil {
				t.Fatal("fixture root invalid", err)
			}
			files := len(autoTestFiles(t, a))
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			head, err := a.pluginStore.runJournal.loadHead(ctx, tx, run.InstanceID, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = tx.ExecContext(ctx, `UPDATE plugin_runs SET error_code='AUTO_BACKUP_EVIDENCE_MISSING' WHERE id=?`, run.ID); err != nil {
				t.Fatal(err)
			}
			if mode == "invalid-deferred" {
				if _, err = tx.ExecContext(ctx, `UPDATE plugin_deferred_operations SET sealed=X'00' WHERE run_id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
			}
			evidence, classifyErr := a.pluginStore.runJournal.classifyNativeRecoveryTx(ctx, tx, head)
			if evidence.Class != pluginNativeUnknownBlocked || evidence.Diagnostic != "" {
				t.Fatal("untrusted label granted native evidence", evidence, classifyErr)
			}
			if mode == "valid-root-forged-label" && classifyErr != nil {
				t.Fatal("valid root failed classification", classifyErr)
			}
			if mode == "invalid-deferred" && classifyErr == nil {
				t.Fatal("bad deferred receipt accepted")
			}
			var retries int
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_run_attempt_retries r JOIN plugin_run_attempts a ON a.nonce=r.nonce WHERE a.run_id=?`, run.ID).Scan(&retries); err != nil || retries != 0 {
				t.Fatal("unknown granted replay", retries, err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if len(autoTestFiles(t, a)) != files {
				t.Fatal("readonly classification caused file effects")
			}
		})
	}
}
