package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"light-media/internal/plugins/uninstall"
)

// This historical bridge never authorizes an operation. Self-uninstall predates
// the generic revision chain and already seals its original/final revisions.
func (p *PluginStore) verifyAttemptUninstallRevision(ctx context.Context, q PluginProofReader, a pluginRunAdmission, run PluginRun, original error) error {
	if a.PluginID != "PluginUnInstall" || a.Version != "2.2" || (a.Action != "init" && a.Action != "reload") || run.ConfigRevision <= a.AdmissionRevision || sha256.Sum256(run.Input) != a.InputSHA {
		return original
	}
	var edges int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_run_config_transitions WHERE run_id=? AND from_revision>=? AND from_revision<?`, run.ID, a.AdmissionRevision, run.ConfigRevision).Scan(&edges); err != nil {
		return err
	}
	if edges != 0 {
		return original
	}
	// The source journal also reads its sealed installation list. Keep this
	// additional read-only capability private; a narrower reader cannot bridge.
	reader, ok := q.(pluginRemoveLinkJournalReader)
	if !ok {
		return original
	}
	source, _, err := p.readUninstallSource(ctx, reader, run)
	if err != nil {
		return err
	}
	if (source.Phase != "succeeded" && source.Phase != "failed") || source.InitialRevision != a.AdmissionRevision || source.FinalRevision != run.ConfigRevision {
		return original
	}
	return nil
}

// A recovery attestation extends, rather than overwrites, the immutable attempt
// completion. It is not a claim, execution permit, or permission to retry.
type pluginRunAttemptRecovery struct {
	Schema                                     int
	Nonce                                      string
	ClaimSHA, PreviousCompletionSHA, NativeSHA [sha256.Size]byte
	NativeKind                                 string
	FinalRevision                              int64
	Result                                     pluginRunAttemptResult
}

func (j *pluginRunAttemptJournal) nativeRecoveryEvidenceTx(ctx context.Context, tx *sql.Tx, head pluginRunAttemptStored, run PluginRun) (string, [sha256.Size]byte, bool, error) {
	var zero [sha256.Size]byte
	admission := head.claim.Admission
	if admission.PluginID == "SubscribeGroup" && admission.Version == "2.8.7" && (admission.Action == "event" || admission.Action == "init" || admission.Action == "reload") {
		var instance string
		var generation, revision int64
		var sealed []byte
		err := tx.QueryRowContext(ctx, `SELECT instance_id,generation,config_revision,result FROM plugin_subscribe_group_operations WHERE run_id=?`, run.ID).Scan(&instance, &generation, &revision, &sealed)
		if errors.Is(err, sql.ErrNoRows) {
			return "", zero, false, nil
		}
		if err != nil {
			return "", zero, false, err
		}
		if instance != run.InstanceID || generation != run.Generation || revision != run.ConfigRevision {
			return "", zero, false, errPluginRunAttemptEvidence
		}
		raw, err := j.p.unseal("subscribe_group_result", run.InstanceID, run.ID, sealed)
		if err != nil {
			return "", zero, false, err
		}
		expected, e := newPluginRunAttemptResult("succeeded", "", raw)
		actual, ae := newPluginRunAttemptResult(run.State, run.ErrorCode, run.Result)
		if e != nil || ae != nil || actual != expected {
			return "", zero, false, nil
		}
		return "subscribe-atomic/v1", sha256.Sum256(sealed), true, nil
	}
	r, sealed, err := j.p.readDeferred(ctx, tx, run)
	if err == nil {
		if r.Plugin != admission.PluginID || r.Version != admission.Version || r.Generation != admission.Generation || r.Action != admission.Action || r.Revision < admission.AdmissionRevision || r.Revision > run.ConfigRevision || (r.Phase != "confirmed" && r.Phase != "terminal_failed") {
			return "", zero, false, nil
		}
		// Both the claim and receipt must reach the same exact current invocation.
		_, proofErr := j.p.verifyRunConfigChainTx(ctx, tx, r.Plugin, run, r.Revision, r.InputSHA, r.ConfigSHA)
		if proofErr != nil {
			if pluginErrorCodeInternal(proofErr) != "PLUGIN_RUN_PROOF_INVALID" {
				return "", zero, false, proofErr
			}
			if r.Plugin != "PluginUnInstall" || r.Version != "2.2" || r.Method != uninstall.ApplyCapability || r.Revision != admission.AdmissionRevision || r.InputSHA != hex.EncodeToString(admission.InputSHA[:]) {
				return "", zero, false, nil
			}
			if proofErr = j.p.verifyAttemptUninstallRevision(ctx, tx, admission, run, proofErr); proofErr != nil {
				return "", zero, false, proofErr
			}
		}
		state, code := "succeeded", ""
		if r.Phase == "terminal_failed" {
			state, code = "failed", r.ErrorCode
		}
		expected, e := newPluginRunAttemptResult(state, code, r.Result)
		actual, ae := newPluginRunAttemptResult(run.State, run.ErrorCode, run.Result)
		if e != nil || ae != nil || actual != expected {
			return "", zero, false, nil
		}
		return "deferred/v1", sha256.Sum256(sealed), true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", zero, false, err
	}
	// The older trusted uninstall host has its own authenticated source outcome,
	// including its installation-list proof. A mutable result row is insufficient.
	if admission.PluginID != "PluginUnInstall" || admission.Version != "2.2" || (admission.Action != "init" && admission.Action != "reload") {
		return "", zero, false, nil
	}
	source, sealed, err := j.p.readUninstallSource(ctx, tx, run)
	if errors.Is(err, sql.ErrNoRows) {
		return "", zero, false, nil
	}
	if err != nil {
		return "", zero, false, err
	}
	if source.InitialRevision != admission.AdmissionRevision || source.FinalRevision != run.ConfigRevision || source.InputSHA != hex.EncodeToString(admission.InputSHA[:]) || (source.Phase != "succeeded" && source.Phase != "failed") {
		return "", zero, false, nil
	}
	expected, e := newPluginRunAttemptResult(source.Phase, source.ErrorCode, source.Result)
	actual, ae := newPluginRunAttemptResult(run.State, run.ErrorCode, run.Result)
	if e != nil || ae != nil || actual != expected {
		return "", zero, false, nil
	}
	return "uninstall-source/v1", sha256.Sum256(sealed), true, nil
}

func (j *pluginRunAttemptJournal) reconcileNativeCompletionTx(ctx context.Context, tx *sql.Tx, head pluginRunAttemptStored) (bool, error) {
	return j.nativeCompletionRecoveryTx(ctx, tx, head, true)
}

func (j *pluginRunAttemptJournal) nativeCompletionRecoveryTx(ctx context.Context, tx *sql.Tx, head pluginRunAttemptStored, create bool) (bool, error) {
	// Existing native formats bind run identity, not an attempt nonce. A prior
	// attempt could have produced the same receipt, so never attribute it to a
	// later retry without a future explicitly attempt-bound native format.
	if head.claim.PreviousNonce != "" {
		valid, err := j.nativeRetryHistoryTx(ctx, tx, head)
		if err != nil || !valid {
			return false, err
		}
	}
	if err := j.p.checkPluginFormWrite(ctx, head.claim.Admission.InstanceID, pluginFormWriteLifecycle); err != nil {
		return false, err
	}
	previous, err := j.loadCompletion(head)
	if err != nil && !errors.Is(err, errPluginRunAttemptUnknown) {
		return false, err
	}
	if err == nil {
		// Only a transport/result-persistence fallback may be superseded by the
		// already committed native outcome. A contradictory business success or
		// ordinary source failure cannot be silently corrected here.
		if previous.Result.State != "failed" && previous.Result.State != "reconcile_pending" {
			return false, nil
		}
		switch previous.Result.ErrorCode {
		case "PLUGIN_RESULT_SAVE_FAILED", "PLUGIN_RESULT_TOO_LARGE", "INVALID_PLUGIN_RESULT", "PLUGIN_DEFERRED_UNCERTAIN", "PLUGIN_INTERRUPTED":
		default:
			return false, nil
		}
	}
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM plugin_runs WHERE id=?`, head.claim.Admission.RunID).Scan(&state); err != nil {
		return false, err
	}
	if state != "succeeded" && state != "failed" {
		return false, nil
	}
	run, err := j.runForAdmission(ctx, tx, head.claim.Admission)
	if err != nil {
		// Keep the existing quarantine policy for unproven revision/identity.
		return false, nil
	}
	if run.State != "succeeded" && run.State != "failed" {
		return false, nil
	}
	if len(head.completion) > 0 && previous.FinalRevision > run.ConfigRevision {
		return false, nil
	}
	evidence, err := j.classifyNativeRecoveryTx(ctx, tx, head)
	if err != nil || evidence.Class != pluginNativeCompleted {
		return false, err
	}
	kind, nativeSHA := evidence.Kind, evidence.Digest
	result, err := newPluginRunAttemptResult(run.State, run.ErrorCode, run.Result)
	if err != nil {
		return false, err
	}
	expected := pluginRunAttemptRecovery{1, head.claim.Nonce, head.claimSHA, sha256.Sum256(head.completion), nativeSHA, kind, run.ConfigRevision, result}
	var sealed []byte
	err = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_run_attempt_recoveries WHERE nonce=?`, head.claim.Nonce).Scan(&sealed)
	if err == nil {
		raw, e := j.p.unseal("run_attempt_recovery", run.InstanceID, run.ID+":"+head.claim.Nonce, sealed)
		if e != nil {
			return false, e
		}
		var actual pluginRunAttemptRecovery
		if decodePluginRunAttempt(raw, &actual) != nil || actual != expected {
			return false, errPluginRunAttemptEvidence
		}
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if !create {
		return false, nil
	}
	raw, err := json.Marshal(expected)
	if err != nil {
		return false, err
	}
	sealed, err = j.p.seal("run_attempt_recovery", run.InstanceID, run.ID+":"+head.claim.Nonce, raw)
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_run_attempt_recoveries(nonce,sealed) VALUES(?,?)`, head.claim.Nonce, sealed)
	return err == nil, err
}
