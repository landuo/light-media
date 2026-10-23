package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
)

// These are real SQLite transaction tests, not an in-memory registry model.
// The fixture executes the same additive migration the production constructor
// must execute. Four-entry-point/App integration is intentionally a separate
// gate; these tests do not claim that unmodified callers use the journal.
func attemptJournalFixture(t *testing.T) (*PluginStore, *pluginRunAttemptJournal, PluginRun, *pluginRunClaimAttempt) {
	t.Helper()
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	if _, err := p.store.DB.ExecContext(ctx, pluginRunAttemptJournalSchema); err != nil {
		t.Fatal(err)
	}
	installStoredPlugin(t, p, "attempt-one")
	_, run, err := p.SaveConfigAndQueue(ctx, "attempt-one", 0, map[string]any{"enabled": true, "private": "synthetic-attempt-secret"})
	if err != nil {
		t.Fatal(err)
	}
	j, err := newPluginRunAttemptJournal(p)
	if err != nil {
		t.Fatal(err)
	}
	admission := pluginRunAdmission{InstanceID: run.InstanceID, PluginID: "SyntheticPlugin", Version: run.Version, RunID: run.ID, Action: run.Action, Generation: run.Generation, AdmissionRevision: run.ConfigRevision, InputSHA: sha256.Sum256(run.Input)}
	a, err := j.prepare(admission)
	if err != nil {
		t.Fatal(err)
	}
	return p, j, run, a
}

func attemptJournalClaimTx(t *testing.T, p *PluginStore, j *pluginRunAttemptJournal, run PluginRun, a *pluginRunClaimAttempt) (*sql.Tx, error) {
	t.Helper()
	ctx := context.Background()
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := tx.ExecContext(ctx, `UPDATE plugin_runs SET state='running',error_code='',updated_at=? WHERE id=? AND state='pending'`, now(), run.ID)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if n, err := changed.RowsAffected(); err != nil || n != 1 {
		tx.Rollback()
		t.Fatalf("claim row count %d: %v", n, err)
	}
	return tx, j.recordClaimTx(ctx, tx, a)
}

func attemptJournalClaim(t *testing.T, p *PluginStore, j *pluginRunAttemptJournal, run PluginRun, a *pluginRunClaimAttempt) {
	t.Helper()
	tx, err := attemptJournalClaimTx(t, p, j, run, a)
	defer tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func attemptJournalResult(t *testing.T, state, code string, result json.RawMessage) pluginRunAttemptResult {
	t.Helper()
	out, err := newPluginRunAttemptResult(state, code, result)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func attemptJournalFinishTx(t *testing.T, p *PluginStore, j *pluginRunAttemptJournal, a *pluginRunClaimAttempt, expected pluginRunAttemptResult, result json.RawMessage) (*sql.Tx, error) {
	t.Helper()
	ctx := context.Background()
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = j.requireCurrentTx(ctx, tx, a); err != nil {
		return tx, err
	}
	if len(result) == 0 {
		result = json.RawMessage(`null`)
	}
	raw, err := pluginResultJSON(result)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	sealed, err := p.seal("run_result", a.admission.InstanceID, a.admission.RunID, raw)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	changed, err := tx.ExecContext(ctx, `UPDATE plugin_runs SET state=?,error_code=?,result=?,updated_at=? WHERE id=? AND state='running'`, expected.State, expected.ErrorCode, sealed, now(), a.admission.RunID)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if n, e := changed.RowsAffected(); e != nil || n != 1 {
		tx.Rollback()
		t.Fatalf("finish row count %d: %v", n, e)
	}
	return tx, j.recordCompletionTx(ctx, tx, a, expected)
}

func attemptJournalFinish(t *testing.T, p *PluginStore, j *pluginRunAttemptJournal, a *pluginRunClaimAttempt, expected pluginRunAttemptResult, result json.RawMessage) {
	t.Helper()
	tx, err := attemptJournalFinishTx(t, p, j, a, expected, result)
	defer tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestPluginRunAttemptJournalRandomBootNonceAndEncryptedAdmission(t *testing.T) {
	p, j, run, a := attemptJournalFixture(t)
	other, err := j.prepare(a.admission)
	if err != nil {
		t.Fatal(err)
	}
	secondBoot, err := newPluginRunAttemptJournal(p)
	if err != nil {
		t.Fatal(err)
	}
	if a.nonce == other.nonce || a.boot != j.boot || j.boot == secondBoot.boot || len(a.nonce) != 64 || len(j.boot) != 64 {
		t.Fatal("claim or boot identity reused")
	}
	attemptJournalClaim(t, p, j, run, a)
	var claim, head []byte
	if err = p.store.DB.QueryRow(`SELECT a.claim,h.sealed FROM plugin_run_attempts a JOIN plugin_run_attempt_heads h ON h.nonce=a.nonce WHERE a.nonce=?`, a.nonce).Scan(&claim, &head); err != nil {
		t.Fatal(err)
	}
	for _, value := range [][]byte{claim, head} {
		if bytes.Contains(value, []byte("synthetic-attempt-secret")) || bytes.Contains(value, []byte(a.boot)) || bytes.Contains(value, []byte("admission")) {
			t.Fatal("journal disclosed plaintext")
		}
	}
	proof, err := j.resolveClaim(context.Background(), a)
	if err != nil || proof == nil || proof.attempt != a || proof.claimSHA == [sha256.Size]byte{} {
		t.Fatal("exact committed claim not proven", err)
	}
}

func TestPluginRunAttemptJournalAtomicClaimRollback(t *testing.T) {
	p, j, run, a := attemptJournalFixture(t)
	if _, err := p.store.DB.Exec(`CREATE TRIGGER attempt_head_failure BEFORE INSERT ON plugin_run_attempt_heads BEGIN SELECT RAISE(ABORT,'synthetic head failure'); END`); err != nil {
		t.Fatal(err)
	}
	tx, err := attemptJournalClaimTx(t, p, j, run, a)
	if err == nil {
		tx.Rollback()
		t.Fatal("head failure accepted")
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	actual, err := p.Run(context.Background(), run.ID)
	if err != nil || actual.State != "pending" {
		t.Fatal("running escaped rolled-back journal", actual.State, err)
	}
	var count int
	if err = p.store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_attempts`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial attempt retained", count, err)
	}
	if proof, err := j.resolveClaim(context.Background(), a); proof != nil || !errors.Is(err, errPluginRunAttemptUnknown) {
		t.Fatal("absence treated as committed", proof, err)
	}
}

func TestPluginRunAttemptJournalCommitLostAckRequiresExactNonce(t *testing.T) {
	p, j, run, a := attemptJournalFixture(t)
	tx, err := attemptJournalClaimTx(t, p, j, run, a)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	// Real SQLite commit succeeds; only the acknowledgement is discarded. No
	// fake DB and no boolean stand in for durable exact-nonce readback.
	commitThenLoseAck := func() error {
		if err := tx.Commit(); err != nil {
			return err
		}
		return errors.New("synthetic lost commit acknowledgement")
	}
	if err = commitThenLoseAck(); err == nil {
		t.Fatal("test did not lose acknowledgement")
	}
	if proof, err := j.resolveClaim(context.Background(), a); err != nil || proof == nil || proof.attempt != a {
		t.Fatal("own committed nonce not recovered", err)
	}
	other, err := j.prepare(a.admission)
	if err != nil {
		t.Fatal(err)
	}
	if proof, err := j.resolveClaim(context.Background(), other); proof != nil || !errors.Is(err, errPluginRunAttemptStale) {
		t.Fatal("running row proved wrong nonce", proof, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if proof, err := j.resolveClaim(ctx, a); proof != nil || err == nil {
		t.Fatal("canceled evidence read granted activation")
	}
}

func TestPluginRunAttemptJournalMissingProofNeverAdoptsRunning(t *testing.T) {
	for _, missing := range []string{"legacy-running", "head", "claim", "completion"} {
		t.Run(missing, func(t *testing.T) {
			p, j, run, a := attemptJournalFixture(t)
			if missing == "legacy-running" {
				if _, err := p.store.DB.Exec(`UPDATE plugin_runs SET state='running' WHERE id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				attemptJournalClaim(t, p, j, run, a)
				switch missing {
				case "head":
					if _, err := p.store.DB.Exec(`DELETE FROM plugin_run_attempt_heads WHERE run_id=?`, run.ID); err != nil {
						t.Fatal(err)
					}
				case "claim":
					if _, err := p.store.DB.Exec(`UPDATE plugin_run_attempts SET claim=x'00' WHERE nonce=?`, a.nonce); err != nil {
						t.Fatal(err)
					}
				case "completion":
					// Old runID-only terminalization has no attempt receipt.
					sealed, err := p.seal("run_result", run.InstanceID, run.ID, []byte(`null`))
					if err != nil {
						t.Fatal(err)
					}
					if _, err = p.store.DB.Exec(`UPDATE plugin_runs SET state='succeeded',result=? WHERE id=?`, sealed, run.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if proof, err := j.resolveClaim(context.Background(), a); proof != nil || err == nil {
				t.Fatal("missing proof accepted for activation", missing)
			}
			expected := attemptJournalResult(t, "succeeded", "", nil)
			if proof, err := j.resolveCompletion(context.Background(), a, expected); proof != nil || err == nil {
				t.Fatal("missing proof accepted for completion", missing)
			}
		})
	}
}

func TestPluginRunAttemptJournalRejectsTamperingAndCrossScope(t *testing.T) {
	for _, mutation := range []string{"claim-cipher", "head-cipher", "claim-instance", "head-instance", "run-instance", "run-generation", "run-plugin", "run-action", "run-input", "run-revision", "previous-nonce"} {
		t.Run(mutation, func(t *testing.T) {
			p, j, run, a := attemptJournalFixture(t)
			attemptJournalClaim(t, p, j, run, a)
			var err error
			switch mutation {
			case "claim-cipher":
				_, err = p.store.DB.Exec(`UPDATE plugin_run_attempts SET claim=x'00' WHERE nonce=?`, a.nonce)
			case "head-cipher":
				_, err = p.store.DB.Exec(`UPDATE plugin_run_attempt_heads SET sealed=x'00' WHERE run_id=?`, run.ID)
			case "claim-instance":
				_, err = p.store.DB.Exec(`UPDATE plugin_run_attempts SET instance_id='other' WHERE nonce=?`, a.nonce)
			case "head-instance":
				_, err = p.store.DB.Exec(`UPDATE plugin_run_attempt_heads SET instance_id='other' WHERE run_id=?`, run.ID)
			case "run-instance":
				installStoredPlugin(t, p, "attempt-other")
				_, err = p.store.DB.Exec(`UPDATE plugin_runs SET instance_id='attempt-other' WHERE id=?`, run.ID)
			case "run-generation":
				_, err = p.store.DB.Exec(`UPDATE plugin_runs SET generation=generation+1 WHERE id=?`, run.ID)
			case "run-plugin":
				_, err = p.store.DB.Exec(`UPDATE plugin_installations SET plugin_id='OtherPlugin' WHERE instance_id=?`, run.InstanceID)
			case "run-action":
				_, err = p.store.DB.Exec(`UPDATE plugin_runs SET action='event' WHERE id=?`, run.ID)
			case "run-input":
				sealed, e := p.seal("run", run.InstanceID, run.ID, []byte(`{"changed":true}`))
				if e != nil {
					t.Fatal(e)
				}
				_, err = p.store.DB.Exec(`UPDATE plugin_runs SET input=? WHERE id=?`, sealed, run.ID)
			case "run-revision":
				_, err = p.store.DB.Exec(`UPDATE plugin_runs SET config_revision=config_revision+1 WHERE id=?`, run.ID)
			case "previous-nonce":
				_, err = p.store.DB.Exec(`UPDATE plugin_run_attempts SET previous_nonce=? WHERE nonce=?`, a.nonce, a.nonce)
			}
			if err != nil {
				t.Fatal(err)
			}
			if proof, err := j.resolveClaim(context.Background(), a); proof != nil || err == nil {
				t.Fatal("tampered identity granted activation", mutation)
			}
		})
	}
}

func TestPluginRunAttemptJournalRevisionChainKeepsOriginalAdmission(t *testing.T) {
	p, j, run, a := attemptJournalFixture(t)
	attemptJournalClaim(t, p, j, run, a)
	original := a.admission
	var originalClaim []byte
	if err := p.store.DB.QueryRow(`SELECT claim FROM plugin_run_attempts WHERE nonce=?`, a.nonce).Scan(&originalClaim); err != nil {
		t.Fatal(err)
	}
	run.State = "running"
	if err := p.patchRunConfig(context.Background(), &run, map[string]json.RawMessage{"private": json.RawMessage(`"new-synthetic-value"`)}); err != nil {
		t.Fatal(err)
	}
	if run.ConfigRevision <= original.AdmissionRevision {
		t.Fatal("test did not advance revision")
	}
	if proof, err := j.resolveClaim(context.Background(), a); proof == nil || err != nil {
		t.Fatal("authenticated revision progression rejected", err)
	}
	var after []byte
	if err := p.store.DB.QueryRow(`SELECT claim FROM plugin_run_attempts WHERE nonce=?`, a.nonce).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if a.admission != original || !bytes.Equal(originalClaim, after) {
		t.Fatal("original claim was rewritten")
	}
	expected := attemptJournalResult(t, "succeeded", "", nil)
	attemptJournalFinish(t, p, j, a, expected, nil)
	if proof, err := j.resolveCompletion(context.Background(), a, expected); proof == nil || err != nil {
		t.Fatal("exact revised terminal proof failed", err)
	}
}

func TestPluginRunAttemptJournalTerminalLostAckAndExactDigest(t *testing.T) {
	p, j, run, a := attemptJournalFixture(t)
	attemptJournalClaim(t, p, j, run, a)
	result := json.RawMessage(`{"effects":"known","count":2}`)
	expected := attemptJournalResult(t, "succeeded", "", result)
	tx, err := attemptJournalFinishTx(t, p, j, a, expected, result)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// Simulate an absent ACK by resolving from SQL, never running the effect.
	proof, err := j.resolveCompletion(context.Background(), a, expected)
	if err != nil || proof == nil || proof.attempt != a || proof.result != expected {
		t.Fatal("durable terminal receipt not recovered", err)
	}
	wrong := attemptJournalResult(t, "succeeded", "", json.RawMessage(`{"effects":"known","count":3}`))
	if proof, err = j.resolveCompletion(context.Background(), a, wrong); proof != nil || err == nil {
		t.Fatal("different terminal result accepted")
	}
	if proof, err := j.resolveClaim(context.Background(), a); proof != nil || err == nil {
		t.Fatal("terminal attempt reactivated")
	}
	sealed, err := p.seal("run_result", run.InstanceID, run.ID, []byte(`null`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.store.DB.Exec(`UPDATE plugin_runs SET result=? WHERE id=?`, sealed, run.ID); err != nil {
		t.Fatal(err)
	}
	if proof, err = j.resolveCompletion(context.Background(), a, expected); proof != nil || err == nil {
		t.Fatal("receipt masked changed current result")
	}
}

func TestPluginRunAttemptJournalCompletionWriteFailureRollsBackTerminal(t *testing.T) {
	p, j, run, a := attemptJournalFixture(t)
	attemptJournalClaim(t, p, j, run, a)
	if _, err := p.store.DB.Exec(`CREATE TRIGGER attempt_completion_failure BEFORE UPDATE OF completion ON plugin_run_attempts BEGIN SELECT RAISE(ABORT,'synthetic completion failure'); END`); err != nil {
		t.Fatal(err)
	}
	expected := attemptJournalResult(t, "failed", "SYNTHETIC_FAILED", nil)
	tx, err := attemptJournalFinishTx(t, p, j, a, expected, nil)
	if err == nil {
		tx.Rollback()
		t.Fatal("failed receipt accepted")
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	actual, err := p.Run(context.Background(), run.ID)
	if err != nil || actual.State != "running" || len(actual.Result) != 0 {
		t.Fatal("partial terminal row escaped", actual.State, err)
	}
	if proof, err := j.resolveCompletion(context.Background(), a, expected); proof != nil || !errors.Is(err, errPluginRunAttemptUnknown) {
		t.Fatal("failed finalization released lease", proof, err)
	}
}

func TestPluginRunAttemptJournalOldCompletionCannotFinishRetry(t *testing.T) {
	p, j, run, first := attemptJournalFixture(t)
	attemptJournalClaim(t, p, j, run, first)
	expected := attemptJournalResult(t, "failed", "SYNTHETIC_FAILED", nil)
	attemptJournalFinish(t, p, j, first, expected, nil)
	var originalHead []byte
	if err := p.store.DB.QueryRow(`SELECT sealed FROM plugin_run_attempt_heads WHERE run_id=?`, run.ID).Scan(&originalHead); err != nil {
		t.Fatal(err)
	}
	// This represents a separately authorized retry after a known terminal
	// result, not automatic recovery of uncertain external work.
	if _, err := p.store.DB.Exec(`UPDATE plugin_runs SET state='pending',result=NULL,error_code='' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	second, err := j.prepare(first.admission)
	if err != nil {
		t.Fatal(err)
	}
	attemptJournalClaim(t, p, j, run, second)
	var count int
	if err = p.store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_attempts WHERE run_id=?`, run.ID).Scan(&count); err != nil || count != 2 {
		t.Fatal("retry overwrote original attempt", count, err)
	}
	if proof, err := j.resolveClaim(context.Background(), second); err != nil || proof == nil {
		t.Fatal("new attempt missing", err)
	}
	if proof, err := j.resolveCompletion(context.Background(), first, expected); proof != nil || !errors.Is(err, errPluginRunAttemptStale) {
		t.Fatal("old completion released retry", proof, err)
	}
	tx, err := attemptJournalFinishTx(t, p, j, first, expected, nil)
	if err == nil {
		tx.Rollback()
		t.Fatal("late old completion mutated retry")
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	actual, err := p.Run(context.Background(), run.ID)
	if err != nil || actual.State != "running" {
		t.Fatal("retry was terminalized by old attempt", actual.State, err)
	}
	// Even an authentic historical head is rejected when its successor remains.
	if _, err = p.store.DB.Exec(`UPDATE plugin_run_attempt_heads SET nonce=?,sealed=? WHERE run_id=?`, first.nonce, originalHead, run.ID); err != nil {
		t.Fatal(err)
	}
	if proof, err := j.resolveClaim(context.Background(), first); proof != nil || !errors.Is(err, errPluginRunAttemptStale) {
		t.Fatal("historical head replay accepted", proof, err)
	}
}

func TestPluginRunAttemptJournalColdBootUnknownNeverReissues(t *testing.T) {
	p, j, run, old := attemptJournalFixture(t)
	attemptJournalClaim(t, p, j, run, old)
	fresh, err := newPluginRunAttemptJournal(p)
	if err != nil {
		t.Fatal(err)
	}
	if proof, err := fresh.resolveClaim(context.Background(), old); proof != nil || !errors.Is(err, errPluginRunAttemptBoot) {
		t.Fatal("new boot adopted live token", proof, err)
	}
	forged := *old
	forged.journal, forged.boot = fresh, fresh.boot
	if proof, err := fresh.resolveClaim(context.Background(), &forged); proof != nil || err == nil {
		t.Fatal("old nonce renamed to new boot")
	}
	if _, err = p.store.DB.Exec(`UPDATE plugin_runs SET state='pending' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	retry, err := fresh.prepare(old.admission)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := attemptJournalClaimTx(t, p, fresh, run, retry)
	if !errors.Is(err, errPluginRunAttemptUnknown) {
		tx.Rollback()
		t.Fatal("unknown old claim granted business retry", err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = p.store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_attempts WHERE run_id=?`, run.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("unknown attempt evidence changed", count, err)
	}
}

func TestPluginRunAttemptJournalCompletionRejectsCorruptionAndMissingResult(t *testing.T) {
	for _, mutation := range []string{"completion-cipher", "missing-result", "state", "code", "generation"} {
		t.Run(mutation, func(t *testing.T) {
			p, j, run, a := attemptJournalFixture(t)
			attemptJournalClaim(t, p, j, run, a)
			expected := attemptJournalResult(t, "failed", "SYNTHETIC_FAILED", nil)
			attemptJournalFinish(t, p, j, a, expected, nil)
			var err error
			switch mutation {
			case "completion-cipher":
				_, err = p.store.DB.Exec(`UPDATE plugin_run_attempts SET completion=x'00' WHERE nonce=?`, a.nonce)
			case "missing-result":
				_, err = p.store.DB.Exec(`UPDATE plugin_runs SET result=NULL WHERE id=?`, run.ID)
			case "state":
				_, err = p.store.DB.Exec(`UPDATE plugin_runs SET state='reconcile_pending' WHERE id=?`, run.ID)
			case "code":
				_, err = p.store.DB.Exec(`UPDATE plugin_runs SET error_code='OTHER' WHERE id=?`, run.ID)
			case "generation":
				_, err = p.store.DB.Exec(`UPDATE plugin_runs SET generation=generation+1 WHERE id=?`, run.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if proof, err := j.resolveCompletion(context.Background(), a, expected); proof != nil || err == nil {
				t.Fatal("corrupt terminal evidence accepted", mutation)
			}
		})
	}
}

func TestPluginRunAttemptJournalUnknownTerminalDoesNotAuthorizeRetry(t *testing.T) {
	p, j, run, old := attemptJournalFixture(t)
	attemptJournalClaim(t, p, j, run, old)
	expected := attemptJournalResult(t, "reconcile_pending", "PLUGIN_REMOTE_UNKNOWN", json.RawMessage(`{"outcome":"unknown"}`))
	attemptJournalFinish(t, p, j, old, expected, json.RawMessage(`{"outcome":"unknown"}`))
	if proof, err := j.resolveCompletion(context.Background(), old, expected); proof == nil || err != nil {
		t.Fatal("quiescent unknown result was not persisted", err)
	}
	if _, err := p.store.DB.Exec(`UPDATE plugin_runs SET state='pending',result=NULL,error_code='' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	fresh, err := newPluginRunAttemptJournal(p)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := fresh.prepare(old.admission)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := attemptJournalClaimTx(t, p, fresh, run, retry)
	if !errors.Is(err, errPluginRunAttemptUnknown) {
		tx.Rollback()
		t.Fatal("unknown terminal receipt authorized business replay", err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}
