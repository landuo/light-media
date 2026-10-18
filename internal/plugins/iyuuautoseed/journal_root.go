package iyuuautoseed

import (
	"context"
	"database/sql"
	"encoding/json"
)

// RegistryAnchor is an authenticated journal namespace and its plaintext root
// digest. The actual App keeps its independently sealed installation root in a
// different table, and advances it in the same SQL transaction as this journal.
type RegistryAnchor struct{ Namespace, Digest string }
type RegistryAuthority interface {
	ValidateRegistry(context.Context, *sql.Tx, RegistryAnchor) error
	CommitRegistry(context.Context, *sql.Tx, RegistryAnchor, RegistryAnchor) error
}

// CreateJournalGenesisTx is only for the trusted installation/lifecycle producer.
// Ordinary attachment never calls it. A partial or existing namespace is denied.
func CreateJournalGenesisTx(ctx context.Context, db *sql.DB, tx *sql.Tx, key []byte) (RegistryAnchor, error) {
	j, e := journalWithKey(db, key, false)
	if e != nil {
		return RegistryAnchor{}, e
	}
	if _, e = tx.ExecContext(ctx, journalSchema); e != nil {
		return RegistryAnchor{}, e
	}
	checks := []struct {
		query string
		args  []any
	}{
		{"SELECT 1 FROM iyuu_registry WHERE namespace=?", []any{j.namespace}},
		{"SELECT 1 FROM iyuu_run_manifests WHERE namespace=? LIMIT 1", []any{j.namespace}},
		{"SELECT 1 FROM iyuu_owner_manifests WHERE namespace=? LIMIT 1", []any{j.namespace}},
		{"SELECT 1 FROM iyuu_runs WHERE substr(id,1,?)=? LIMIT 1", []any{len(j.namespace) + 1, j.namespace + ":"}},
		{"SELECT 1 FROM iyuu_steps WHERE substr(run,1,?)=? LIMIT 1", []any{len(j.namespace) + 1, j.namespace + ":"}},
		{"SELECT 1 FROM iyuu_values WHERE substr(owner,1,?)=? LIMIT 1", []any{len(j.namespace) + 1, j.namespace + ":"}},
	}
	for _, c := range checks {
		if e = journalRequireAbsent(ctx, tx, c.query, c.args...); e != nil {
			return RegistryAnchor{}, e
		}
	}
	reg := &journalRegistry{Version: 2, Runs: map[string]string{}, RunIDs: map[string]string{}, Owners: map[string]string{}}
	if e = j.saveRegistryTx(ctx, tx, reg); e != nil {
		return RegistryAnchor{}, e
	}
	raw, _ := json.Marshal(reg)
	return RegistryAnchor{j.namespace, digest(raw)}, nil
}

// AttachAnchoredJournal is read-only admission. Missing schema, namespace or
// independent App root is corruption and can never become a fresh genesis.
func AttachAnchoredJournal(ctx context.Context, db *sql.DB, key []byte, authority RegistryAuthority) (*Journal, error) {
	if authority == nil {
		return nil, ErrJournalIntegrity
	}
	j, e := journalWithKey(db, key, false)
	if e != nil {
		return nil, e
	}
	j.authority = authority
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if _, e = j.readRegistryTx(ctx, tx); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return j, nil
}

// InspectJournalTx authenticates and validates all records for the generation
// using an existing caller transaction. No schema creation, nested transaction,
// mutex acquisition, IO or new root initialization occurs.
func InspectJournalTx(ctx context.Context, db *sql.DB, tx *sql.Tx, key []byte, instance string, generation uint64) (RegistryAnchor, []RunIdentity, bool, error) {
	j, e := journalWithKey(db, key, false)
	if e != nil {
		return RegistryAnchor{}, nil, false, e
	}
	reg, e := j.readRegistryTx(ctx, tx)
	if e != nil {
		return RegistryAnchor{}, nil, false, e
	}
	raw, _ := json.Marshal(reg)
	ids := []RunIdentity{}
	owner, e := j.loadOwnerTx(ctx, tx, reg, instance, generation)
	if e != nil {
		return RegistryAnchor{}, nil, false, e
	}
	for _, k := range reg.RunIDs {
		run, _, e := j.loadRunTx(ctx, tx, reg, owner, k)
		if e != nil {
			return RegistryAnchor{}, nil, false, e
		}
		if run == nil || run.Identity.Instance != instance || run.Identity.Generation != generation {
			return RegistryAnchor{}, nil, false, ErrJournalIntegrity
		}
		ids = append(ids, run.Identity)
	}
	unknown, e := j.hasUnconfirmedTx(ctx, tx, instance, generation)
	return RegistryAnchor{j.namespace, digest(raw)}, ids, unknown, e
}

// NewAnchoredJournalVerifier is a read-only constructor for callers which will
// validate a private receipt through their existing SQL transaction.
func NewAnchoredJournalVerifier(db *sql.DB, key []byte, authority RegistryAuthority) (*Journal, error) {
	if authority == nil {
		return nil, ErrJournalIntegrity
	}
	j, e := journalWithKey(db, key, false)
	if e != nil {
		return nil, e
	}
	j.authority = authority
	return j, nil
}
