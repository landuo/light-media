package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
)

type pluginNativeRetryProof struct {
	Schema                             int
	Nonce                              string
	ClaimSHA, CompletionSHA, NativeSHA [sha256.Size]byte
	Kind                               string
}

func (j *pluginRunAttemptJournal) nativeRetryProofTx(ctx context.Context, tx *sql.Tx, head pluginRunAttemptStored, create bool) (bool, error) {
	if create && head.claim.Boot == j.boot {
		return false, nil
	}
	// Unknown or damaged completion never becomes a synthetic completion.
	if len(head.completion) > 0 {
		completion, err := j.loadCompletion(head)
		if err != nil {
			return false, err
		}
		if completion.Result.State == "succeeded" {
			return false, nil
		}
	}
	evidence, err := j.classifyNativeRecoveryTx(ctx, tx, head)
	if err != nil {
		return false, err
	}
	if evidence.Class != pluginNativeNoEffectsRetry {
		return false, nil
	}
	expected := pluginNativeRetryProof{1, head.claim.Nonce, head.claimSHA, sha256.Sum256(head.completion), evidence.Digest, evidence.Kind}
	var sealed []byte
	err = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_run_attempt_retries WHERE nonce=?`, head.claim.Nonce).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		if !create {
			return false, nil
		}
		raw, e := json.Marshal(expected)
		if e != nil {
			return false, e
		}
		sealed, e = j.p.seal("run_attempt_retry", head.claim.Admission.InstanceID, head.claim.Admission.RunID+":"+head.claim.Nonce, raw)
		if e != nil {
			return false, e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO plugin_run_attempt_retries(nonce,sealed) VALUES(?,?)`, head.claim.Nonce, sealed)
		return e == nil, e
	}
	if err != nil {
		return false, err
	}
	raw, err := j.p.unseal("run_attempt_retry", head.claim.Admission.InstanceID, head.claim.Admission.RunID+":"+head.claim.Nonce, sealed)
	if err != nil {
		return false, err
	}
	var proof pluginNativeRetryProof
	if err = decodePluginRunAttempt(raw, &proof); err != nil {
		return false, err
	}
	if proof != expected {
		return false, errPluginRunAttemptEvidence
	}
	return true, nil
}

// A later native result may complete the same invocation only when EVERY prior
// attempt ended in an authenticated no-business-effects handoff. Old generic
// failed rows or an unbound retry never acquire this compatibility path.
func (j *pluginRunAttemptJournal) nativeRetryHistoryTx(ctx context.Context, tx *sql.Tx, head pluginRunAttemptStored) (bool, error) {
	seen := map[string]bool{}
	for head.claim.PreviousNonce != "" {
		if head.claim.Admission.PluginID != "SubscribeGroup" || head.claim.Admission.Version != "2.8.7" || (head.claim.Admission.Action != "event" && head.claim.Admission.Action != "init" && head.claim.Admission.Action != "reload") {
			return false, nil
		}
		prev := head.claim.PreviousNonce
		if seen[prev] {
			return false, errPluginRunAttemptEvidence
		}
		seen[prev] = true
		old, err := j.loadAttempt(ctx, tx, head.claim.Admission.InstanceID, head.claim.Admission.RunID, prev)
		if err != nil {
			return false, err
		}
		if old.claim.Boot == head.claim.Boot || old.claim.Admission != head.claim.Admission {
			return false, errPluginRunAttemptEvidence
		}
		var sealed []byte
		if err = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_run_attempt_retries WHERE nonce=?`, prev).Scan(&sealed); err != nil {
			return false, err
		}
		raw, err := j.p.unseal("run_attempt_retry", old.claim.Admission.InstanceID, old.claim.Admission.RunID+":"+prev, sealed)
		if err != nil {
			return false, err
		}
		var proof pluginNativeRetryProof
		if err = decodePluginRunAttempt(raw, &proof); err != nil {
			return false, err
		}
		if proof.Schema != 1 || proof.Nonce != prev || proof.ClaimSHA != old.claimSHA || proof.CompletionSHA != sha256.Sum256(old.completion) || (proof.Kind != "subscribe-event-atomic/v1" && proof.Kind != "subscribe-config-atomic/v1") || proof.NativeSHA == [sha256.Size]byte{} {
			return false, errPluginRunAttemptEvidence
		}
		head = old
	}
	return true, nil
}
