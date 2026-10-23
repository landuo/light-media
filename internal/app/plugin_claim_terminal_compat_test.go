package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// All entry-point checks use real claims and one SQLite connection. A nested
// resolveCompletion transaction would exhaust the bounded operation context.
func claimTerminalCompatClaim(t *testing.T, p *PluginStore, instance string) (PluginRun, context.Context) {
	t.Helper()
	installStoredPlugin(t, p, instance)
	_, queued, err := p.SaveConfigAndQueue(context.Background(), instance, 0, map[string]any{"enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.ClaimRun(context.Background())
	if err != nil || run.ID != queued.ID || run.executionLease == nil {
		t.Fatal("real claim failed", err)
	}
	ctx, err := p.activateSupervisedRun(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	return run, ctx
}

func claimTerminalCompatContext(t *testing.T, parent context.Context) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func claimTerminalCompatFixture(t *testing.T, state string) (*PluginStore, PluginRun, context.Context) {
	t.Helper()
	_, p := pluginStoreFixture(t)
	p.store.DB.SetMaxOpenConns(1)
	p.store.DB.SetMaxIdleConns(1)
	run, ctx := claimTerminalCompatClaim(t, p, "terminal-compat")
	code := ""
	if state != "succeeded" {
		code = "ORIGINAL_OUTCOME"
	}
	if err := p.FinishRun(claimTerminalCompatContext(t, ctx), run.ID, state, code, json.RawMessage(`{"original":true}`)); err != nil {
		t.Fatal("original completion failed", err)
	}
	return p, run, ctx
}

func claimTerminalCompatSnapshot(t *testing.T, p *PluginStore, run PluginRun) (string, int64) {
	t.Helper()
	var snapshot string
	var changes int64
	err := p.store.DB.QueryRow(`SELECT quote(r.instance_id)||'|'||quote(r.version)||'|'||quote(r.generation)||'|'||quote(r.config_revision)||'|'||quote(r.action)||'|'||quote(r.state)||'|'||quote(r.input)||'|'||quote(r.result)||'|'||quote(r.error_code)||'|'||quote(r.updated_at)||'|'||quote(h.nonce)||'|'||quote(h.sealed)||'|'||quote(a.claim)||'|'||quote(a.completion),total_changes() FROM plugin_runs r LEFT JOIN plugin_run_attempt_heads h ON h.run_id=r.id LEFT JOIN plugin_run_attempts a ON a.nonce=h.nonce WHERE r.id=?`, run.ID).Scan(&snapshot, &changes)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot, changes
}

func claimTerminalCompatAssertUnchanged(t *testing.T, p *PluginStore, run PluginRun, before string, changes int64) {
	t.Helper()
	after, afterChanges := claimTerminalCompatSnapshot(t, p, run)
	if before != after || changes != afterChanges {
		t.Fatal("terminal compatibility path performed a SQL mutation")
	}
}

func claimTerminalCompatAssertDenied(t *testing.T, p *PluginStore, run PluginRun, ctx context.Context) {
	t.Helper()
	before, changes := claimTerminalCompatSnapshot(t, p, run)
	err := p.FinishRun(claimTerminalCompatContext(t, ctx), run.ID, "failed", "LATE_RESULT", json.RawMessage(`{"late":true}`))
	if err == nil || pluginErrorCode(err) == "PLUGIN_RUN_TERMINAL" || errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("unproven completion was accepted or used a nested transaction", err)
	}
	err = p.failPluginResult(claimTerminalCompatContext(t, ctx), run.ID, "PLUGIN_RESULT_TOO_LARGE")
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("unproven completion received idempotent success or timed out", err)
	}
	claimTerminalCompatAssertUnchanged(t, p, run, before, changes)
}

func TestPluginClaimTerminalCompatibilitySameAttempt(t *testing.T) {
	for _, state := range []string{"succeeded", "failed", "reconcile_pending"} {
		t.Run(state, func(t *testing.T) {
			p, run, ctx := claimTerminalCompatFixture(t, state)
			lease := run.executionLease
			lease.mu.Lock()
			expected := *lease.expected
			lease.mu.Unlock()
			before, changes := claimTerminalCompatSnapshot(t, p, run)
			// This proposal deliberately differs from the original stored result.
			// It must not become the expected digest of the old completion.
			err := p.FinishRun(claimTerminalCompatContext(t, ctx), run.ID, "failed", "LATE_RESULT", json.RawMessage(`{"late":true}`))
			if pluginErrorCode(err) != "PLUGIN_RUN_TERMINAL" {
				t.Fatal("same-attempt FinishRun lost its terminal contract", err)
			}
			for _, code := range []string{"PLUGIN_RESULT_SAVE_FAILED", "PLUGIN_RESULT_TOO_LARGE"} {
				if err = p.failPluginResult(claimTerminalCompatContext(t, ctx), run.ID, code); err != nil {
					t.Fatal("same-attempt result fallback lost idempotence", err)
				}
			}
			claimTerminalCompatAssertUnchanged(t, p, run, before, changes)
			lease.mu.Lock()
			unchanged := lease.expected != nil && *lease.expected == expected
			lease.mu.Unlock()
			if !unchanged {
				t.Fatal("late result replaced the retained completion intent")
			}
			if _, err = p.runJournal.resolveCompletion(claimTerminalCompatContext(t, ctx), lease.claim, expected); err != nil {
				t.Fatal("original completion proof was weakened", err)
			}
			wrong, err := newPluginRunAttemptResult("failed", "LATE_RESULT", json.RawMessage(`{"late":true}`))
			if err != nil {
				t.Fatal(err)
			}
			if proof, err := p.runJournal.resolveCompletion(claimTerminalCompatContext(t, ctx), lease.claim, wrong); proof != nil || !errors.Is(err, errPluginRunAttemptEvidence) {
				t.Fatal("resolveCompletion accepted a different expected digest", err)
			}
			if err = p.releaseSupervisedRun(run); err != nil {
				t.Fatal(err)
			}
			// A released same-boot capability still carries only read authority.
			detached := context.WithoutCancel(ctx)
			if err = p.failPluginResult(claimTerminalCompatContext(t, detached), run.ID, "PLUGIN_RESULT_SAVE_FAILED"); err != nil {
				t.Fatal("released exact completion lost no-op compatibility", err)
			}
		})
	}
}

func TestPluginClaimTerminalCompatibilityRejectsMissingAndWrongLease(t *testing.T) {
	p, run, ctx := claimTerminalCompatFixture(t, "succeeded")
	claimTerminalCompatAssertDenied(t, p, run, context.Background())
	other, otherCtx := claimTerminalCompatClaim(t, p, "terminal-other")
	claimTerminalCompatAssertDenied(t, p, run, otherCtx)
	if err := p.FinishRun(claimTerminalCompatContext(t, otherCtx), other.ID, "succeeded", "", nil); err != nil {
		t.Fatal("wrong-run rejection damaged the other live lease", err)
	}
	if err := p.releaseSupervisedRun(other); err != nil {
		t.Fatal(err)
	}
	if err := p.failPluginResult(claimTerminalCompatContext(t, ctx), run.ID, "PLUGIN_RESULT_SAVE_FAILED"); err != nil {
		t.Fatal("correct original lease no longer works", err)
	}
	if err := p.releaseSupervisedRun(run); err != nil {
		t.Fatal(err)
	}
}

func TestPluginClaimTerminalCompatibilityRejectsTamperedCompletion(t *testing.T) {
	mutations := []string{"sealed_completion", "sealed_result", "state", "error_code", "final_revision", "missing_completion", "missing_result", "sealed_head", "missing_head"}
	for _, mutation := range mutations {
		t.Run(mutation, func(t *testing.T) {
			p, run, ctx := claimTerminalCompatFixture(t, "succeeded")
			claim := run.executionLease.claim
			var err error
			switch mutation {
			case "sealed_completion":
				_, err = p.store.DB.Exec(`UPDATE plugin_run_attempts SET completion=x'00' WHERE nonce=?`, claim.nonce)
			case "sealed_result":
				var sealed []byte
				sealed, err = p.seal("run_result", run.InstanceID, run.ID, []byte(`{"different":true}`))
				if err == nil {
					_, err = p.store.DB.Exec(`UPDATE plugin_runs SET result=? WHERE id=?`, sealed, run.ID)
				}
			case "state":
				_, err = p.store.DB.Exec(`UPDATE plugin_runs SET state='failed' WHERE id=?`, run.ID)
			case "error_code":
				_, err = p.store.DB.Exec(`UPDATE plugin_runs SET error_code='CHANGED_OUTCOME' WHERE id=?`, run.ID)
			case "final_revision":
				var stored pluginRunAttemptStored
				stored, err = p.runJournal.exactHead(context.Background(), p.store.DB, claim)
				if err != nil {
					t.Fatal(err)
				}
				var completion pluginRunAttemptCompletionRecord
				completion, err = p.runJournal.loadCompletion(stored)
				if err != nil {
					t.Fatal(err)
				}
				completion.FinalRevision++
				var raw, sealed []byte
				raw, err = json.Marshal(completion)
				if err == nil {
					sealed, err = p.seal("run_attempt_completion", run.InstanceID, run.ID+":"+claim.nonce, raw)
				}
				if err == nil {
					_, err = p.store.DB.Exec(`UPDATE plugin_run_attempts SET completion=? WHERE nonce=?`, sealed, claim.nonce)
				}
			case "missing_completion":
				_, err = p.store.DB.Exec(`UPDATE plugin_run_attempts SET completion=NULL WHERE nonce=?`, claim.nonce)
			case "missing_result":
				_, err = p.store.DB.Exec(`UPDATE plugin_runs SET result=NULL WHERE id=?`, run.ID)
			case "sealed_head":
				_, err = p.store.DB.Exec(`UPDATE plugin_run_attempt_heads SET sealed=x'00' WHERE run_id=?`, run.ID)
			case "missing_head":
				_, err = p.store.DB.Exec(`DELETE FROM plugin_run_attempt_heads WHERE run_id=?`, run.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			claimTerminalCompatAssertDenied(t, p, run, ctx)
		})
	}
}

func TestPluginClaimTerminalCompatibilityRejectsReplacedHead(t *testing.T) {
	p, first, oldCtx := claimTerminalCompatFixture(t, "failed")
	if err := p.releaseSupervisedRun(first); err != nil {
		t.Fatal(err)
	}
	// Synthetic authorized retry setup, followed by the actual claim adapter.
	if _, err := p.store.DB.Exec(`UPDATE plugin_runs SET state='pending',result=NULL,error_code='' WHERE id=?`, first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := p.ClaimRun(context.Background())
	if err != nil || second.ID != first.ID || second.executionLease == nil || second.executionLease.claim.nonce == first.executionLease.claim.nonce {
		t.Fatal("retry did not create a distinct real claim", err)
	}
	ctx, err := p.activateSupervisedRun(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	claimTerminalCompatAssertDenied(t, p, second, context.WithoutCancel(oldCtx))
	if err = p.FinishRun(claimTerminalCompatContext(t, ctx), second.ID, "succeeded", "", json.RawMessage(`{"second":true}`)); err != nil {
		t.Fatal("stale completion disturbed retry", err)
	}
	claimTerminalCompatAssertDenied(t, p, second, context.WithoutCancel(oldCtx))
	if err = p.releaseSupervisedRun(second); err != nil {
		t.Fatal(err)
	}
}

func TestPluginClaimTerminalCompatibilityPreservesUnfinishedGuard(t *testing.T) {
	_, p := pluginStoreFixture(t)
	p.store.DB.SetMaxOpenConns(1)
	p.store.DB.SetMaxIdleConns(1)
	run, ctx := claimTerminalCompatClaim(t, p, "terminal-unfinished")
	// A plaintext terminal flag without an attempt completion cannot receive
	// either legacy compatibility response or fresh finalization authority.
	if _, err := p.store.DB.Exec(`UPDATE plugin_runs SET state='failed' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	claimTerminalCompatAssertDenied(t, p, run, ctx)
}
