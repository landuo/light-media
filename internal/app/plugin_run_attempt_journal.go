package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
)

// Execute this additive migration inside NewPluginStore's existing transaction.
// Do not backfill identities for old running rows: no SQL state proves which
// process, claim, or external operation produced an old row. Reset/uninstall
// must preserve these receipts, just as they preserve other execution evidence.
const pluginRunAttemptJournalSchema = `
CREATE TABLE IF NOT EXISTS plugin_run_attempts(
 nonce TEXT PRIMARY KEY,run_id TEXT NOT NULL,instance_id TEXT NOT NULL,
 previous_nonce TEXT NOT NULL,claim BLOB NOT NULL,completion BLOB,
 UNIQUE(run_id,previous_nonce));
CREATE INDEX IF NOT EXISTS plugin_run_attempts_instance ON plugin_run_attempts(instance_id,run_id);
CREATE TABLE IF NOT EXISTS plugin_run_attempt_heads(
 run_id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,nonce TEXT NOT NULL UNIQUE,sealed BLOB NOT NULL,
 FOREIGN KEY(nonce) REFERENCES plugin_run_attempts(nonce));
CREATE TABLE IF NOT EXISTS plugin_run_attempt_retries(
 nonce TEXT PRIMARY KEY,sealed BLOB NOT NULL,
 FOREIGN KEY(nonce) REFERENCES plugin_run_attempts(nonce));
CREATE TABLE IF NOT EXISTS plugin_run_attempt_recoveries(
 nonce TEXT PRIMARY KEY,sealed BLOB NOT NULL,
 FOREIGN KEY(nonce) REFERENCES plugin_run_attempts(nonce));`

var (
	errPluginRunAttemptEvidence = errors.New("plugin run attempt evidence invalid")
	errPluginRunAttemptUnknown  = errors.New("plugin run attempt outcome unproven")
	errPluginRunAttemptStale    = errors.New("plugin run attempt is not the current head")
	errPluginRunAttemptBoot     = errors.New("plugin run attempt belongs to another host boot")
)

// One instance per server-bound PluginStore, constructed before entry points
// become usable. A newly opened journal never adopts a previous boot's lease.
// The pointer and attempts are host-private; neither belongs in an SDK or form
// facade. This is not a second runtime registry and starts no goroutines.
type pluginRunAttemptJournal struct {
	p    *PluginStore
	boot string
}

type pluginRunClaimAttempt struct {
	journal     *pluginRunAttemptJournal
	boot, nonce string
	admission   pluginRunAdmission
}

type pluginRunAttemptClaimRecord struct {
	Schema        int                `json:"schema"`
	Boot          string             `json:"boot"`
	Nonce         string             `json:"nonce"`
	PreviousNonce string             `json:"previous_nonce"`
	Admission     pluginRunAdmission `json:"admission"`
}

type pluginRunAttemptHeadRecord struct {
	Schema               int `json:"schema"`
	Instance, Run, Nonce string
	ClaimSHA             [sha256.Size]byte
}

type pluginRunAttemptResult struct {
	State     string            `json:"state"`
	ErrorCode string            `json:"error_code"`
	ResultSHA [sha256.Size]byte `json:"result_sha"`
}

type pluginRunAttemptCompletionRecord struct {
	Schema        int `json:"schema"`
	Boot, Nonce   string
	ClaimSHA      [sha256.Size]byte
	FinalRevision int64
	Result        pluginRunAttemptResult
}

// These are exact evidence objects, never a running/not-running resolution.
// Only this journal's verified same-boot SQL reads produce them. The adapter
// must bind proof.attempt to its original registry ticket before activation or
// Done; it must not construct a replacement ticket from a run ID or SQL row.
type pluginRunClaimProof struct {
	attempt  *pluginRunClaimAttempt
	claimSHA [sha256.Size]byte
}

type pluginRunCompletionProof struct {
	attempt  *pluginRunClaimAttempt
	claimSHA [sha256.Size]byte
	result   pluginRunAttemptResult
}

type pluginRunAttemptStored struct {
	claim      pluginRunAttemptClaimRecord
	claimSHA   [sha256.Size]byte
	completion []byte
}

func newPluginRunAttemptJournal(p *PluginStore) (*pluginRunAttemptJournal, error) {
	if p == nil || p.store == nil || p.store.DB == nil || len(p.store.key) != 32 {
		return nil, errPluginRunAttemptEvidence
	}
	boot, err := newPluginRunAttemptNonce()
	if err != nil {
		return nil, err
	}
	return &pluginRunAttemptJournal{p: p, boot: boot}, nil
}

func newPluginRunAttemptNonce() (string, error) {
	var nonce [32]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(nonce[:]), nil
}

func validPluginRunAttemptAdmission(a pluginRunAdmission) bool {
	return a.valid() && pluginStorageID.MatchString(a.InstanceID) &&
		pluginStorageID.MatchString(a.PluginID) && pluginStorageID.MatchString(a.RunID) &&
		pluginStorageID.MatchString(a.Action) && pluginStorageVersion.MatchString(a.Version) &&
		a.InputSHA != [sha256.Size]byte{}
}

func (j *pluginRunAttemptJournal) prepare(admission pluginRunAdmission) (*pluginRunClaimAttempt, error) {
	if j == nil || j.p == nil || !pluginStorageDigest.MatchString(j.boot) || !validPluginRunAttemptAdmission(admission) {
		return nil, errPluginRunAttemptEvidence
	}
	nonce, err := newPluginRunAttemptNonce()
	if err != nil {
		return nil, err
	}
	return &pluginRunClaimAttempt{journal: j, boot: j.boot, nonce: nonce, admission: admission}, nil
}

func (j *pluginRunAttemptJournal) requireAttempt(a *pluginRunClaimAttempt) error {
	if j == nil || a == nil || a.journal != j || a.boot != j.boot {
		return errPluginRunAttemptBoot
	}
	if j.p == nil || !pluginStorageDigest.MatchString(a.boot) || !pluginStorageDigest.MatchString(a.nonce) || !validPluginRunAttemptAdmission(a.admission) {
		return errPluginRunAttemptEvidence
	}
	return nil
}

func decodePluginRunAttempt(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return errPluginRunAttemptEvidence
	}
	return nil
}

func (j *pluginRunAttemptJournal) loadAttempt(ctx context.Context, q PluginProofReader, instance, run, nonce string) (pluginRunAttemptStored, error) {
	var out pluginRunAttemptStored
	var actualInstance, actualRun, previous string
	var sealed []byte
	err := q.QueryRowContext(ctx, `SELECT instance_id,run_id,previous_nonce,claim,completion FROM plugin_run_attempts WHERE nonce=?`, nonce).Scan(&actualInstance, &actualRun, &previous, &sealed, &out.completion)
	if errors.Is(err, sql.ErrNoRows) {
		return out, errPluginRunAttemptUnknown
	}
	if err != nil {
		return out, err
	}
	if actualInstance != instance || actualRun != run {
		return out, errPluginRunAttemptEvidence
	}
	raw, err := j.p.unseal("run_attempt_claim", instance, run+":"+nonce, sealed)
	if err != nil {
		return out, err
	}
	if err = decodePluginRunAttempt(raw, &out.claim); err != nil {
		return out, err
	}
	c := out.claim
	if c.Schema != 1 || c.Nonce != nonce || c.PreviousNonce != previous || c.Admission.InstanceID != instance || c.Admission.RunID != run ||
		!pluginStorageDigest.MatchString(c.Boot) || !pluginStorageDigest.MatchString(c.Nonce) || !validPluginRunAttemptAdmission(c.Admission) ||
		(c.PreviousNonce != "" && (!pluginStorageDigest.MatchString(c.PreviousNonce) || c.PreviousNonce == c.Nonce)) {
		return out, errPluginRunAttemptEvidence
	}
	out.claimSHA = sha256.Sum256(raw)
	return out, nil
}

func (j *pluginRunAttemptJournal) loadHead(ctx context.Context, q PluginProofReader, instance, run string) (pluginRunAttemptStored, error) {
	var out pluginRunAttemptStored
	var actualInstance, nonce string
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT instance_id,nonce,sealed FROM plugin_run_attempt_heads WHERE run_id=?`, run).Scan(&actualInstance, &nonce, &sealed); err != nil {
		return out, err
	}
	if actualInstance != instance || !pluginStorageDigest.MatchString(nonce) {
		return out, errPluginRunAttemptEvidence
	}
	raw, err := j.p.unseal("run_attempt_head", instance, run, sealed)
	if err != nil {
		return out, err
	}
	var head pluginRunAttemptHeadRecord
	if err = decodePluginRunAttempt(raw, &head); err != nil {
		return out, err
	}
	if head.Schema != 1 || head.Instance != instance || head.Run != run || head.Nonce != nonce {
		return out, errPluginRunAttemptEvidence
	}
	out, err = j.loadAttempt(ctx, q, instance, run, nonce)
	if err != nil {
		return out, err
	}
	if head.ClaimSHA != out.claimSHA {
		return out, errPluginRunAttemptEvidence
	}
	// An authenticated old head is still stale if a retained successor exists.
	var successor bool
	if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plugin_run_attempts WHERE run_id=? AND previous_nonce=?)`, run, nonce).Scan(&successor); err != nil {
		return out, err
	}
	if successor {
		return out, errPluginRunAttemptStale
	}
	return out, nil
}

func (j *pluginRunAttemptJournal) loadCompletion(stored pluginRunAttemptStored) (pluginRunAttemptCompletionRecord, error) {
	var out pluginRunAttemptCompletionRecord
	c := stored.claim
	if len(stored.completion) == 0 {
		return out, errPluginRunAttemptUnknown
	}
	raw, err := j.p.unseal("run_attempt_completion", c.Admission.InstanceID, c.Admission.RunID+":"+c.Nonce, stored.completion)
	if err != nil {
		return out, err
	}
	if err = decodePluginRunAttempt(raw, &out); err != nil {
		return out, err
	}
	if out.Schema != 1 || out.Boot != c.Boot || out.Nonce != c.Nonce || out.ClaimSHA != stored.claimSHA ||
		out.FinalRevision < c.Admission.AdmissionRevision || !out.Result.valid() {
		return out, errPluginRunAttemptEvidence
	}
	return out, nil
}

func (v pluginRunAttemptResult) valid() bool {
	return (v.State == "succeeded" || v.State == "failed" || v.State == "reconcile_pending") &&
		(v.ErrorCode == "" || pluginStorageID.MatchString(v.ErrorCode)) &&
		(v.State != "succeeded" || v.ErrorCode == "") && v.ResultSHA != [sha256.Size]byte{}
}

// Use the same normalization as finishRun. Retain this value for persistence-
// only repair; a commit error does not authorize re-entering the business code.
func newPluginRunAttemptResult(state, errorCode string, result json.RawMessage) (pluginRunAttemptResult, error) {
	if len(result) == 0 {
		result = json.RawMessage(`null`)
	}
	raw, err := pluginResultJSON(result)
	if err != nil {
		return pluginRunAttemptResult{}, err
	}
	out := pluginRunAttemptResult{state, errorCode, sha256.Sum256(raw)}
	if !out.valid() {
		return pluginRunAttemptResult{}, errPluginRunAttemptEvidence
	}
	return out, nil
}

// This reads the complete authenticated input, not a cached PluginRun. The
// original admission remains immutable when trusted configuration propagation
// advances the SQL revision/input through the existing authenticated chain.
func (j *pluginRunAttemptJournal) runForAdmission(ctx context.Context, q PluginProofReader, admission pluginRunAdmission) (PluginRun, error) {
	run, err := j.p.scanPluginRun(q.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, admission.RunID))
	if err != nil {
		return run, err
	}
	if run.InstanceID != admission.InstanceID || run.ID != admission.RunID || run.Version != admission.Version || run.Generation != admission.Generation || run.Action != admission.Action || run.ConfigRevision < admission.AdmissionRevision {
		return run, errPluginRunAttemptEvidence
	}
	var plugin string
	if err = q.QueryRowContext(ctx, `SELECT plugin_id FROM plugin_installations WHERE instance_id=?`, admission.InstanceID).Scan(&plugin); err != nil {
		return run, err
	}
	if plugin != admission.PluginID {
		return run, errPluginRunAttemptEvidence
	}
	var successor bool
	if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plugin_run_config_transitions WHERE run_id=? AND from_revision>=?)`, run.ID, run.ConfigRevision).Scan(&successor); err != nil {
		return run, err
	}
	if successor {
		return run, errPluginRunAttemptEvidence
	}
	_, err = j.p.verifyRunConfigChainTx(ctx, q, admission.PluginID, run, admission.AdmissionRevision, hex.EncodeToString(admission.InputSHA[:]), "")
	if pluginErrorCodeInternal(err) == "PLUGIN_RUN_PROOF_INVALID" {
		err = j.p.verifyAttemptUninstallRevision(ctx, q, admission, run, err)
	}
	return run, err
}

// Call after the actual pending -> running UPDATE, before the SAME tx commits.
// Authorization, schedule, pool and native-owner checks remain at the original
// four claim sites. A running observer must never call this or prepare a lease.
func (j *pluginRunAttemptJournal) recordClaimTx(ctx context.Context, tx *sql.Tx, a *pluginRunClaimAttempt) error {
	if err := j.requireAttempt(a); err != nil {
		return err
	}
	if tx == nil {
		return errPluginRunAttemptEvidence
	}
	if err := j.p.checkPluginFormWrite(ctx, a.admission.InstanceID, pluginFormWriteLifecycle); err != nil {
		return err
	}
	run, err := j.runForAdmission(ctx, tx, a.admission)
	if err != nil {
		return err
	}
	if run.State != "running" || run.ConfigRevision != a.admission.AdmissionRevision || sha256.Sum256(run.Input) != a.admission.InputSHA {
		return errPluginRunAttemptEvidence
	}
	var installedGeneration int64
	var installedVersion string
	if err = tx.QueryRowContext(ctx, `SELECT version,generation FROM plugin_installations WHERE instance_id=?`, run.InstanceID).Scan(&installedVersion, &installedGeneration); err != nil {
		return err
	}
	if installedVersion != run.Version || installedGeneration != run.Generation {
		return errPluginRunAttemptEvidence
	}
	previous := ""
	old, err := j.loadHead(ctx, tx, run.InstanceID, run.ID)
	if errors.Is(err, sql.ErrNoRows) {
		var retained bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plugin_run_attempts WHERE run_id=?)`, run.ID).Scan(&retained); err != nil {
			return err
		}
		if retained {
			return errPluginRunAttemptUnknown
		}
	} else if err != nil {
		return err
	} else {
		retry, e := j.nativeRetryProofTx(ctx, tx, old, false)
		if e != nil {
			return e
		}
		if !retry {
			completion, e := j.loadCompletion(old)
			if e != nil {
				return e
			}
			if completion.Result.State == "reconcile_pending" {
				return errPluginRunAttemptUnknown
			}
		}
		if _, err = j.runForAdmission(ctx, tx, old.claim.Admission); err != nil {
			return err
		}

		previous = old.claim.Nonce
	}
	claim := pluginRunAttemptClaimRecord{1, a.boot, a.nonce, previous, a.admission}
	raw, err := json.Marshal(claim)
	if err != nil {
		return err
	}
	sealed, err := j.p.seal("run_attempt_claim", run.InstanceID, run.ID+":"+a.nonce, raw)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_run_attempts(nonce,run_id,instance_id,previous_nonce,claim) VALUES(?,?,?,?,?)`, a.nonce, run.ID, run.InstanceID, previous, sealed); err != nil {
		return err
	}
	head := pluginRunAttemptHeadRecord{1, run.InstanceID, run.ID, a.nonce, sha256.Sum256(raw)}
	raw, err = json.Marshal(head)
	if err != nil {
		return err
	}
	sealed, err = j.p.seal("run_attempt_head", run.InstanceID, run.ID, raw)
	if err != nil {
		return err
	}
	var changed sql.Result
	if previous == "" {
		changed, err = tx.ExecContext(ctx, `INSERT INTO plugin_run_attempt_heads(run_id,instance_id,nonce,sealed) VALUES(?,?,?,?)`, run.ID, run.InstanceID, a.nonce, sealed)
	} else {
		changed, err = tx.ExecContext(ctx, `UPDATE plugin_run_attempt_heads SET nonce=?,sealed=? WHERE run_id=? AND instance_id=? AND nonce=?`, a.nonce, sealed, run.ID, run.InstanceID, previous)
	}
	if err != nil {
		return err
	}
	if n, e := changed.RowsAffected(); e != nil {
		return e
	} else if n != 1 {
		return errPluginRunAttemptStale
	}
	return nil
}

func (j *pluginRunAttemptJournal) exactHead(ctx context.Context, q PluginProofReader, a *pluginRunClaimAttempt) (pluginRunAttemptStored, error) {
	var out pluginRunAttemptStored
	if err := j.requireAttempt(a); err != nil {
		return out, err
	}
	out, err := j.loadHead(ctx, q, a.admission.InstanceID, a.admission.RunID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, errPluginRunAttemptUnknown
	}
	if err != nil {
		return out, err
	}
	if out.claim.Nonce != a.nonce || out.claim.Boot != a.boot {
		return out, errPluginRunAttemptStale
	}
	if out.claim.Admission != a.admission {
		return out, errPluginRunAttemptEvidence
	}
	return out, nil
}

// requireCurrentTx is the exact-attempt guard before finishRun's UPDATE and
// all finalization-side mutations (including unentered-task special handling).
// It accepts only the narrow reader; it cannot commit or fall back to the pool.
func (j *pluginRunAttemptJournal) requireCurrentTx(ctx context.Context, q PluginProofReader, a *pluginRunClaimAttempt) (*pluginRunClaimProof, error) {
	stored, err := j.exactHead(ctx, q, a)
	if err != nil {
		return nil, err
	}
	if len(stored.completion) != 0 {
		return nil, errPluginRunAttemptStale
	}
	run, err := j.runForAdmission(ctx, q, a.admission)
	if err != nil {
		return nil, err
	}
	if run.State != "running" {
		return nil, errPluginRunAttemptUnknown
	}
	return &pluginRunClaimProof{a, stored.claimSHA}, nil
}

// Use after Commit returned an error, outside the claim transaction. A missing
// nonce/head, canceled read, running flag, or another attempt is not rollback
// proof: return no activation evidence and keep the ticket quarantined. Caller
// chooses a bounded, non-canceled repair context; never repeat business work.
func (j *pluginRunAttemptJournal) resolveClaim(ctx context.Context, a *pluginRunClaimAttempt) (*pluginRunClaimProof, error) {
	if err := j.requireAttempt(a); err != nil {
		return nil, err
	}
	tx, err := j.p.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	proof, err := j.requireCurrentTx(ctx, tx, a)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return proof, nil
}

// Call after all terminal SQL writes but before the SAME finalization Commit.
// A caller must first requireCurrentTx before touching the run/schedule/owner
// rows. This method additionally verifies the expected terminal digest against
// the actual row and authenticates it under this nonce's distinct AEAD purpose.
func (j *pluginRunAttemptJournal) recordCompletionTx(ctx context.Context, tx *sql.Tx, a *pluginRunClaimAttempt, expected pluginRunAttemptResult) error {
	if tx == nil || !expected.valid() {
		return errPluginRunAttemptEvidence
	}
	if err := j.requireAttempt(a); err != nil {
		return err
	}
	if err := j.p.checkPluginFormWrite(ctx, a.admission.InstanceID, pluginFormWriteLifecycle); err != nil {
		return err
	}
	stored, err := j.exactHead(ctx, tx, a)
	if err != nil {
		return err
	}
	if len(stored.completion) != 0 {
		return errPluginRunAttemptStale
	}
	run, err := j.runForAdmission(ctx, tx, a.admission)
	if err != nil {
		return err
	}
	if len(run.Result) == 0 {
		return errPluginRunAttemptEvidence
	}
	actual, err := newPluginRunAttemptResult(run.State, run.ErrorCode, run.Result)
	if err != nil {
		return err
	}
	if actual != expected {
		return errPluginRunAttemptEvidence
	}
	completion := pluginRunAttemptCompletionRecord{1, a.boot, a.nonce, stored.claimSHA, run.ConfigRevision, expected}
	raw, err := json.Marshal(completion)
	if err != nil {
		return err
	}
	sealed, err := j.p.seal("run_attempt_completion", a.admission.InstanceID, a.admission.RunID+":"+a.nonce, raw)
	if err != nil {
		return err
	}
	changed, err := tx.ExecContext(ctx, `UPDATE plugin_run_attempts SET completion=? WHERE nonce=? AND run_id=? AND instance_id=? AND completion IS NULL AND EXISTS(SELECT 1 FROM plugin_run_attempt_heads h WHERE h.run_id=plugin_run_attempts.run_id AND h.nonce=plugin_run_attempts.nonce)`, sealed, a.nonce, a.admission.RunID, a.admission.InstanceID)
	if err != nil {
		return err
	}
	if n, e := changed.RowsAffected(); e != nil {
		return e
	} else if n != 1 {
		return errPluginRunAttemptStale
	}
	return nil
}

// readCurrentCompletionTx verifies an existing completion using only the caller's
// transaction reader. A nil proof means this exact head has no completion yet;
// it is not permission to mutate, activate, or release the attempt. Callers must
// still requireCurrentTx before any unfinished finalization-side mutation.
func (j *pluginRunAttemptJournal) readCurrentCompletionTx(ctx context.Context, q PluginProofReader, a *pluginRunClaimAttempt) (*pluginRunCompletionProof, error) {
	stored, err := j.exactHead(ctx, q, a)
	if err != nil {
		return nil, err
	}
	if len(stored.completion) == 0 {
		return nil, nil
	}
	completion, err := j.loadCompletion(stored)
	if err != nil {
		return nil, err
	}
	run, err := j.runForAdmission(ctx, q, a.admission)
	if err != nil {
		return nil, err
	}
	if len(run.Result) == 0 {
		return nil, errPluginRunAttemptEvidence
	}
	actual, err := newPluginRunAttemptResult(run.State, run.ErrorCode, run.Result)
	if err != nil {
		return nil, err
	}
	if actual != completion.Result || run.ConfigRevision != completion.FinalRevision {
		return nil, errPluginRunAttemptEvidence
	}
	return &pluginRunCompletionProof{a, stored.claimSHA, actual}, nil
}

// A terminal receipt alone is historical evidence, not authority over the
// current run. Exact head plus current terminal row plus expected full digest
// are required. Thus a late completion cannot finalize or release a retry.
func (j *pluginRunAttemptJournal) resolveCompletion(ctx context.Context, a *pluginRunClaimAttempt, expected pluginRunAttemptResult) (*pluginRunCompletionProof, error) {
	if err := j.requireAttempt(a); err != nil {
		return nil, err
	}
	if !expected.valid() {
		return nil, errPluginRunAttemptEvidence
	}
	tx, err := j.p.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	proof, err := j.readCurrentCompletionTx(ctx, tx, a)
	if err != nil {
		return nil, err
	}
	if proof == nil {
		return nil, errPluginRunAttemptUnknown
	}
	if proof.result != expected {
		return nil, errPluginRunAttemptEvidence
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return proof, nil
}
