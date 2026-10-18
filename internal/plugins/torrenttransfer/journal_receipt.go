package torrenttransfer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// NewJournalVerifier constructs the owner-key verifier without inspecting or
// initializing storage. It is for trusted App validation against an existing
// transaction, including an App database with a single connection. The caller
// must expose only the readonly validation operation to its notice consumer;
// this owner-key object is not a capability wrapper for untrusted plugin code.
func NewJournalVerifier(db *sql.DB, key []byte) (*Journal, error) {
	return journalWithKey(db, key, false)
}

// ValidateLocalReceipt verifies an existing local receipt using only tx. It
// never takes j.mu, queries the DB pool, admits a run, or updates a manifest.
// The current authenticated value must also match. Use immutable per-receipt
// keys (as the notification outbox does); a later write to the same mutable key
// deliberately prevents validation of an older value as the current one.
func (j *Journal) ValidateLocalReceipt(ctx context.Context, tx *sql.Tx, identity RunIdentity, seq int, op, key string, value any) error {
	if tx == nil || seq <= 0 || identity.Instance == "" || identity.Generation == 0 || identity.Run == "" || identity.Fingerprint == "" {
		return ErrReplayDiverged
	}
	request, e := json.Marshal(struct {
		Op, Key string
		Value   any
	}{op, key, value})
	if e != nil {
		return e
	}
	body, e := json.Marshal(value)
	if e != nil {
		return e
	}
	_, owner, run, _, e := j.runStateTx(ctx, tx, identity)
	if e != nil {
		return e
	}
	if run == nil || owner == nil {
		return ErrJournalIntegrity
	}
	if run.Identity != identity || seq > len(run.Steps) {
		return ErrReplayDiverged
	}
	req := digest(request)
	step := run.Steps[seq-1]
	if step.Kind != "local" || step.State != "complete" || step.Request != req {
		return ErrReplayDiverged
	}
	var sealed []byte
	if e = tx.QueryRowContext(ctx, "SELECT result FROM torrenttransfer_steps WHERE run=? AND seq=?", j.runKey(identity), seq).Scan(&sealed); e != nil {
		if e == sql.ErrNoRows {
			return ErrJournalIntegrity
		}
		return e
	}
	if digest(sealed) != step.Result {
		return ErrJournalIntegrity
	}
	aad := fmt.Sprintf("%s/%d/local/%s", j.runKey(identity), seq, req)
	plain, e := j.open(aad, sealed)
	if e != nil {
		return e
	}
	var receipt stepResult
	if e = sourceDecode(plain, &receipt); e != nil || receipt.Outcome != "confirmed" || receipt.Fault != "" || string(receipt.Value) != "null" {
		return ErrJournalIntegrity
	}
	stored, found, e := j.valueTx(ctx, tx, owner, key)
	if e != nil {
		return e
	}
	if !found || !bytes.Equal(stored, body) {
		return ErrReplayDiverged
	}
	return nil
}
