package torrenttransfer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type RuntimeCheckpoint struct {
	SourceError     string           `json:"source_error,omitempty"`
	Counts          Counts           `json:"counts"`
	Identity        RunIdentity      `json:"identity"`
	Core            State            `json:"core"`
	AuthorityProofs []AuthorityGrant `json:"authority_proofs"`
	Revision        uint64           `json:"revision"`
	Run             string           `json:"run"`
}

func (r *JournalRun) Checkpoint(ctx context.Context, state RuntimeCheckpoint) error {
	state.Run = r.identity.Run
	state.Identity = r.identity
	return r.localWrite(ctx, "runtime-checkpoint", "runtime/"+strconv.FormatUint(r.identity.Generation, 10), state)
}
func (j *Journal) ReadCheckpoint(ctx context.Context, instance string, generation uint64) (RuntimeCheckpoint, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var state RuntimeCheckpoint
	tx, e := j.db.BeginTx(ctx, nil)
	if e != nil {
		return state, false, e
	}
	defer tx.Rollback()
	reg, e := j.readRegistryTx(ctx, tx)
	if e != nil {
		return state, false, e
	}
	owner, e := j.loadOwnerTx(ctx, tx, reg, instance, generation)
	if e != nil {
		return state, false, e
	}
	if owner == nil {
		return state, false, tx.Commit()
	}
	raw, found, e := j.valueTx(ctx, tx, owner, "runtime/"+strconv.FormatUint(generation, 10))
	if e != nil {
		return state, false, e
	}
	if found {
		if e = sourceDecode(raw, &state); e != nil {
			return state, false, e
		}
	}
	if e = tx.Commit(); e != nil {
		return state, false, e
	}
	return state, found, nil
}
func (j *Journal) ReadRunInitial(ctx context.Context, id RunIdentity) (json.RawMessage, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	tx, e := j.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, false, e
	}
	defer tx.Rollback()
	_, _, run, initial, e := j.runStateTx(ctx, tx, id)
	if e != nil {
		return nil, false, e
	}
	if e = tx.Commit(); e != nil {
		return nil, false, e
	}
	return initial, run != nil, nil
}

// ReadRunIdentity resolves an original identity through the authenticated RunID
// index. An App run may have advanced its live config revision after a local CAS;
// that must never create a second journal timeline for the same original run.
func (j *Journal) ReadRunIdentity(ctx context.Context, instance string, generation uint64, runID string) (RunIdentity, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var id RunIdentity
	tx, e := j.db.BeginTx(ctx, nil)
	if e != nil {
		return id, false, e
	}
	defer tx.Rollback()
	reg, e := j.readRegistryTx(ctx, tx)
	if e != nil {
		return id, false, e
	}
	owner, e := j.loadOwnerTx(ctx, tx, reg, instance, generation)
	if e != nil {
		return id, false, e
	}
	key, found := reg.RunIDs[j.runSlot(instance, generation, runID)]
	if !found {
		return id, false, tx.Commit()
	}
	run, _, e := j.loadRunTx(ctx, tx, reg, owner, key)
	if e != nil {
		return id, false, e
	}
	if run == nil {
		return id, false, ErrJournalIntegrity
	}
	if e = tx.Commit(); e != nil {
		return id, false, e
	}
	return run.Identity, true, nil
}

// WriteCheckpointTx publishes the value, owner manifest and registry using only
// the supplied App transaction. Do not obtain j.mu or query j.db here: another
// journal caller can hold that mutex while waiting for the App's sole connection.
// The caller controls commit/rollback; nothing is published in memory beforehand.
func (j *Journal) WriteCheckpointTx(ctx context.Context, tx *sql.Tx, instance string, generation uint64, state RuntimeCheckpoint) error {
	if tx == nil || instance == "" || generation == 0 {
		return ErrReplayDiverged
	}
	if state.Identity.Instance != "" && (state.Identity.Instance != instance || state.Identity.Generation != generation) {
		return ErrReplayDiverged
	}
	raw, e := json.Marshal(state)
	if e != nil {
		return e
	}
	reg, e := j.readRegistryTx(ctx, tx)
	if e != nil {
		return e
	}
	owner, e := j.loadOwnerTx(ctx, tx, reg, instance, generation)
	if e != nil {
		return e
	}
	if owner == nil {
		owner = &journalOwnerManifest{Instance: instance, Generation: generation, Values: map[string]string{}}
	}
	if e = j.putValueTx(ctx, tx, owner, "runtime/"+strconv.FormatUint(generation, 10), raw); e != nil {
		return e
	}
	if e = j.saveOwnerTx(ctx, tx, reg, owner); e != nil {
		return e
	}
	return j.saveRegistryTx(ctx, tx, reg)
}

// HasUnconfirmed is an admission/drain barrier over the authenticated manifests
// for this generation. It does not infer safety from a count of plaintext rows.
func (j *Journal) HasUnconfirmed(ctx context.Context, instance string, generation uint64) (bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	tx, e := j.db.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	unconfirmed, e := j.hasUnconfirmedTx(ctx, tx, instance, generation)
	if e != nil {
		return false, e
	}
	if e = tx.Commit(); e != nil {
		return false, e
	}
	return unconfirmed, nil
}
func (j *Journal) hasUnconfirmedTx(ctx context.Context, tx *sql.Tx, instance string, generation uint64) (bool, error) {
	reg, e := j.readRegistryTx(ctx, tx)
	if e != nil {
		return false, e
	}
	owner, e := j.loadOwnerTx(ctx, tx, reg, instance, generation)
	if e != nil {
		return false, e
	}
	prefix := j.ownerKey(instance, generation) + "/"
	unconfirmed := false
	for slot, key := range reg.RunIDs {
		if !strings.HasPrefix(slot, prefix) {
			continue
		}
		run, _, e := j.loadRunTx(ctx, tx, reg, owner, key)
		if e != nil {
			return false, e
		}
		if run == nil {
			return false, ErrJournalIntegrity
		}
		for index, step := range run.Steps {
			if step.Kind != "mutation" {
				continue
			}
			if step.State == "pending" {
				unconfirmed = true
				continue
			}
			var sealed []byte
			if e = tx.QueryRowContext(ctx, "SELECT result FROM torrenttransfer_steps WHERE run=? AND seq=?", key, index+1).Scan(&sealed); e != nil {
				return false, ErrJournalIntegrity
			}
			aad := fmt.Sprintf("%s/%d/mutation/%s", key, index+1, step.Request)
			raw, e := j.open(aad, sealed)
			if e != nil {
				return false, e
			}
			var result stepResult
			if e = sourceDecode(raw, &result); e != nil {
				return false, ErrJournalIntegrity
			}
			switch result.Outcome {
			case "unknown":
				unconfirmed = true
			case "confirmed", "rejected":
			default:
				return false, ErrJournalIntegrity
			}
		}
	}
	return unconfirmed, nil
}
