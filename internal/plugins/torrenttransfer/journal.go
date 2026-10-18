package torrenttransfer

// This journal is an owner-only storage boundary. Cells and the registry of
// admitted runs, steps, bindings and values are authenticated. An intent and its
// manifest are committed before external IO; an unconfirmed mutation is never
// issued again. These guarantees do not detect rollback of the complete database
// together with its authenticated registry. That requires a freshness anchor
// outside this database; a counter or another row in the same file is not one.
import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var ErrUnconfirmed = errors.New("TorrentTransfer external effect unconfirmed; owner reconciliation required")
var ErrReplayDiverged = errors.New("TorrentTransfer durable replay differs from recorded run")
var ErrJournalIntegrity = errors.New("TorrentTransfer authenticated journal is incomplete or changed")

const journalSchema = `CREATE TABLE IF NOT EXISTS torrenttransfer_runs (id TEXT PRIMARY KEY, binding BLOB NOT NULL, initial BLOB NOT NULL, started INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS torrenttransfer_steps (run TEXT NOT NULL, seq INTEGER NOT NULL, request TEXT NOT NULL, kind TEXT NOT NULL, state TEXT NOT NULL, result BLOB, PRIMARY KEY(run,seq));
 CREATE TABLE IF NOT EXISTS torrenttransfer_values (owner TEXT NOT NULL, key TEXT NOT NULL, value BLOB NOT NULL, PRIMARY KEY(owner,key));
 CREATE TABLE IF NOT EXISTS torrenttransfer_registry (namespace TEXT PRIMARY KEY, root BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS torrenttransfer_run_manifests (namespace TEXT NOT NULL, run TEXT NOT NULL, body BLOB NOT NULL, PRIMARY KEY(namespace,run));
 CREATE TABLE IF NOT EXISTS torrenttransfer_owner_manifests (namespace TEXT NOT NULL, owner TEXT NOT NULL, body BLOB NOT NULL, PRIMARY KEY(namespace,owner));`

type Journal struct {
	db        *sql.DB
	aead      cipher.AEAD
	namespace string
	mu        sync.Mutex
	owned     bool
	authority RegistryAuthority
}
type RunIdentity struct {
	Instance    string `json:"instance"`
	Generation  uint64 `json:"generation"`
	Revision    uint64 `json:"revision"`
	Fingerprint string `json:"fingerprint"`
	Run         string `json:"run"`
}
type JournalRun struct {
	journal       *Journal
	identity      RunIdentity
	key           string
	seq           int
	mu            sync.Mutex
	guard         func(context.Context) error
	Started       time.Time
	LocalCommit   func(context.Context, *sql.Tx, RunIdentity, string, string, any) (func(), error)
	LocalRead     func(context.Context, string) (any, error)
	LocalObserved func(string, string, any)
}
type stepResult struct {
	Value   json.RawMessage `json:"value"`
	Fault   string          `json:"fault,omitempty"`
	Outcome string          `json:"outcome"`
}

// The root authenticates membership, not just individual cell contents. Heads
// authenticate exact ordered step records and complete binding/value key sets.
// SQL primary keys and row counts alone cannot prove any of these sets complete.
type journalRegistry struct {
	Version int               `json:"version"`
	Runs    map[string]string `json:"runs"`
	RunIDs  map[string]string `json:"run_ids"`
	Owners  map[string]string `json:"owners"`
}
type journalStepManifest struct {
	Request string `json:"request"`
	Kind    string `json:"kind"`
	State   string `json:"state"`
	Result  string `json:"result"`
}
type journalRunManifest struct {
	Identity RunIdentity           `json:"identity"`
	Owner    string                `json:"owner"`
	Binding  string                `json:"binding"`
	Initial  string                `json:"initial"`
	Started  int64                 `json:"started"`
	Steps    []journalStepManifest `json:"steps"`
	Bindings map[string]string     `json:"bindings"`
}
type journalOwnerManifest struct {
	Instance   string            `json:"instance"`
	Generation uint64            `json:"generation"`
	Values     map[string]string `json:"values"`
}
type journalStoredStep struct {
	journalStepManifest
	Sealed []byte
}

func journalWithKey(db *sql.DB, key []byte, owned bool) (*Journal, error) {
	if db == nil || len(key) != 32 {
		return nil, errors.New("TorrentTransfer storage needs an owner supplied 256-bit key")
	}
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	a, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("light-media/torrenttransfer/journal-namespace/v2"))
	return &Journal{db: db, aead: a, namespace: hex.EncodeToString(mac.Sum(nil)), owned: owned}, nil
}

func OpenJournal(filename string, key []byte) (*Journal, error) {
	if len(key) != 32 {
		return nil, errors.New("TorrentTransfer storage needs an owner supplied 256-bit key")
	}
	db, e := sql.Open("sqlite", filename)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	j, e := journalWithKey(db, key, true)
	if e == nil {
		_, e = db.Exec(`PRAGMA busy_timeout=5000;` + journalSchema)
	}
	if e == nil {
		e = j.initializeRegistry(context.Background())
	}
	if e != nil {
		_ = db.Close()
		return nil, e
	}
	return j, nil
}

// AttachJournal reuses the App connection without changing its pool or closing
// it. Distinct owner-supplied keys have disjoint registries, run IDs and values
// even when they share one database. Old records without a manifest cannot be
// safely adopted and require an explicit owner migration/reconciliation.
func AttachJournal(ctx context.Context, db *sql.DB, key []byte) (*Journal, error) {
	j, e := journalWithKey(db, key, false)
	if e != nil {
		return nil, e
	}
	if _, e = db.ExecContext(ctx, journalSchema); e != nil {
		return nil, e
	}
	if e = j.initializeRegistry(ctx); e != nil {
		return nil, e
	}
	return j, nil
}

func (j *Journal) initializeRegistry(ctx context.Context) error {
	tx, e := j.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var sealed []byte
	e = tx.QueryRowContext(ctx, "SELECT root FROM torrenttransfer_registry WHERE namespace=?", j.namespace).Scan(&sealed)
	if e == nil {
		if _, e = j.readRegistryTx(ctx, tx); e != nil {
			return e
		}
		return tx.Commit()
	}
	if e != sql.ErrNoRows {
		return e
	}
	// Presence is used only to reject orphaned or legacy records, never as proof
	// of integrity. Once attached, a missing root is always an integrity error.
	checks := []struct {
		query string
		args  []any
	}{
		{"SELECT 1 FROM torrenttransfer_run_manifests WHERE namespace=? LIMIT 1", []any{j.namespace}},
		{"SELECT 1 FROM torrenttransfer_owner_manifests WHERE namespace=? LIMIT 1", []any{j.namespace}},
		{"SELECT 1 FROM torrenttransfer_runs WHERE substr(id,1,?)=? OR instr(id,':')=0 LIMIT 1", []any{len(j.namespace) + 1, j.namespace + ":"}},
		{"SELECT 1 FROM torrenttransfer_steps WHERE substr(run,1,?)=? OR instr(run,':')=0 LIMIT 1", []any{len(j.namespace) + 1, j.namespace + ":"}},
		{"SELECT 1 FROM torrenttransfer_values WHERE substr(owner,1,?)=? OR instr(owner,':')=0 LIMIT 1", []any{len(j.namespace) + 1, j.namespace + ":"}},
	}
	for _, check := range checks {
		if e = journalRequireAbsent(ctx, tx, check.query, check.args...); e != nil {
			return e
		}
	}
	reg := &journalRegistry{Version: 2, Runs: map[string]string{}, RunIDs: map[string]string{}, Owners: map[string]string{}}
	if e = j.saveRegistryTx(ctx, tx, reg); e != nil {
		return e
	}
	return tx.Commit()
}

func journalRequireAbsent(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	var present int
	e := tx.QueryRowContext(ctx, query, args...).Scan(&present)
	if e == sql.ErrNoRows {
		return nil
	}
	if e != nil {
		return e
	}
	return ErrJournalIntegrity
}
func (j *Journal) readRegistryTx(ctx context.Context, tx *sql.Tx) (*journalRegistry, error) {
	var sealed []byte
	if e := tx.QueryRowContext(ctx, "SELECT root FROM torrenttransfer_registry WHERE namespace=?", j.namespace).Scan(&sealed); e != nil {
		if e == sql.ErrNoRows {
			return nil, ErrJournalIntegrity
		}
		return nil, e
	}
	raw, e := j.open("registry/"+j.namespace, sealed)
	if e != nil {
		return nil, e
	}
	var reg journalRegistry
	if e = sourceDecode(raw, &reg); e != nil {
		return nil, ErrJournalIntegrity
	}
	if reg.Version != 2 || reg.Runs == nil || reg.RunIDs == nil || reg.Owners == nil || len(reg.RunIDs) != len(reg.Runs) {
		return nil, ErrJournalIntegrity
	}
	for _, run := range reg.RunIDs {
		if reg.Runs[run] == "" {
			return nil, ErrJournalIntegrity
		}
	}
	if j.authority != nil {
		if e = j.authority.ValidateRegistry(ctx, tx, RegistryAnchor{j.namespace, digest(raw)}); e != nil {
			return nil, e
		}
	}
	return &reg, nil
}
func (j *Journal) saveRegistryTx(ctx context.Context, tx *sql.Tx, reg *journalRegistry) error {
	raw, e := json.Marshal(reg)
	if e != nil {
		return e
	}
	sealed, e := j.seal("registry/"+j.namespace, raw)
	if e != nil {
		return e
	}
	if j.authority != nil {
		var previous []byte
		if e = tx.QueryRowContext(ctx, "SELECT root FROM torrenttransfer_registry WHERE namespace=?", j.namespace).Scan(&previous); e != nil {
			return ErrJournalIntegrity
		}
		prior, e := j.open("registry/"+j.namespace, previous)
		if e != nil {
			return e
		}
		if e = j.authority.CommitRegistry(ctx, tx, RegistryAnchor{j.namespace, digest(prior)}, RegistryAnchor{j.namespace, digest(raw)}); e != nil {
			return e
		}
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO torrenttransfer_registry(namespace,root) VALUES(?,?) ON CONFLICT(namespace) DO UPDATE SET root=excluded.root", j.namespace, sealed)
	return e
}
func (j *Journal) ownerKey(instance string, generation uint64) string {
	raw, _ := json.Marshal(struct {
		Instance   string
		Generation uint64
	}{instance, generation})
	return j.namespace + ":" + digest(raw)
}
func (j *Journal) runKey(id RunIdentity) string {
	raw, _ := json.Marshal(id)
	return j.namespace + ":" + digest(raw)
}
func (j *Journal) runSlot(instance string, generation uint64, run string) string {
	return j.ownerKey(instance, generation) + "/" + digest([]byte(run))
}

func (j *Journal) loadOwnerTx(ctx context.Context, tx *sql.Tx, reg *journalRegistry, instance string, generation uint64) (*journalOwnerManifest, error) {
	owner := j.ownerKey(instance, generation)
	expected, exists := reg.Owners[owner]
	if !exists {
		if e := journalRequireAbsent(ctx, tx, "SELECT 1 FROM torrenttransfer_owner_manifests WHERE namespace=? AND owner=?", j.namespace, owner); e != nil {
			return nil, e
		}
		if e := journalRequireAbsent(ctx, tx, "SELECT 1 FROM torrenttransfer_values WHERE owner=? LIMIT 1", owner); e != nil {
			return nil, e
		}
		return nil, nil
	}
	var sealed []byte
	if e := tx.QueryRowContext(ctx, "SELECT body FROM torrenttransfer_owner_manifests WHERE namespace=? AND owner=?", j.namespace, owner).Scan(&sealed); e != nil {
		if e == sql.ErrNoRows {
			return nil, ErrJournalIntegrity
		}
		return nil, e
	}
	if digest(sealed) != expected {
		return nil, ErrJournalIntegrity
	}
	raw, e := j.open("owner-manifest/"+owner, sealed)
	if e != nil {
		return nil, e
	}
	var m journalOwnerManifest
	if e = sourceDecode(raw, &m); e != nil || m.Instance != instance || m.Generation != generation || m.Values == nil {
		return nil, ErrJournalIntegrity
	}
	rows, e := tx.QueryContext(ctx, "SELECT key,value FROM torrenttransfer_values WHERE owner=?", owner)
	if e != nil {
		return nil, e
	}
	seen := 0
	for rows.Next() {
		var key string
		var body []byte
		if e = rows.Scan(&key, &body); e != nil {
			rows.Close()
			return nil, e
		}
		if expected, ok := m.Values[key]; !ok || expected != digest(body) {
			rows.Close()
			return nil, ErrJournalIntegrity
		}
		seen++
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if seen != len(m.Values) {
		return nil, ErrJournalIntegrity
	}
	return &m, nil
}
func (j *Journal) saveOwnerTx(ctx context.Context, tx *sql.Tx, reg *journalRegistry, m *journalOwnerManifest) error {
	owner := j.ownerKey(m.Instance, m.Generation)
	raw, e := json.Marshal(m)
	if e != nil {
		return e
	}
	sealed, e := j.seal("owner-manifest/"+owner, raw)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO torrenttransfer_owner_manifests(namespace,owner,body) VALUES(?,?,?) ON CONFLICT(namespace,owner) DO UPDATE SET body=excluded.body", j.namespace, owner, sealed); e != nil {
		return e
	}
	reg.Owners[owner] = digest(sealed)
	return nil
}
func (j *Journal) putValueTx(ctx context.Context, tx *sql.Tx, owner *journalOwnerManifest, key string, raw []byte) error {
	o := j.ownerKey(owner.Instance, owner.Generation)
	sealed, e := j.seal(o+"/"+key, raw)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO torrenttransfer_values(owner,key,value) VALUES(?,?,?) ON CONFLICT(owner,key) DO UPDATE SET value=excluded.value", o, key, sealed); e != nil {
		return e
	}
	owner.Values[key] = digest(sealed)
	return nil
}
func (j *Journal) valueTx(ctx context.Context, tx *sql.Tx, owner *journalOwnerManifest, key string) ([]byte, bool, error) {
	if _, ok := owner.Values[key]; !ok {
		return nil, false, nil
	}
	o := j.ownerKey(owner.Instance, owner.Generation)
	var sealed []byte
	if e := tx.QueryRowContext(ctx, "SELECT value FROM torrenttransfer_values WHERE owner=? AND key=?", o, key).Scan(&sealed); e != nil {
		if e == sql.ErrNoRows {
			return nil, false, ErrJournalIntegrity
		}
		return nil, false, e
	}
	if digest(sealed) != owner.Values[key] {
		return nil, false, ErrJournalIntegrity
	}
	raw, e := j.open(o+"/"+key, sealed)
	return raw, e == nil, e
}

func (j *Journal) loadRunTx(ctx context.Context, tx *sql.Tx, reg *journalRegistry, owner *journalOwnerManifest, key string) (*journalRunManifest, []byte, error) {
	expected, exists := reg.Runs[key]
	if !exists {
		if e := journalRequireAbsent(ctx, tx, "SELECT 1 FROM torrenttransfer_run_manifests WHERE namespace=? AND run=?", j.namespace, key); e != nil {
			return nil, nil, e
		}
		if e := journalRequireAbsent(ctx, tx, "SELECT 1 FROM torrenttransfer_runs WHERE id=?", key); e != nil {
			return nil, nil, e
		}
		if e := journalRequireAbsent(ctx, tx, "SELECT 1 FROM torrenttransfer_steps WHERE run=? LIMIT 1", key); e != nil {
			return nil, nil, e
		}
		return nil, nil, nil
	}
	var sealed []byte
	if e := tx.QueryRowContext(ctx, "SELECT body FROM torrenttransfer_run_manifests WHERE namespace=? AND run=?", j.namespace, key).Scan(&sealed); e != nil {
		if e == sql.ErrNoRows {
			return nil, nil, ErrJournalIntegrity
		}
		return nil, nil, e
	}
	if digest(sealed) != expected {
		return nil, nil, ErrJournalIntegrity
	}
	raw, e := j.open("run-manifest/"+key, sealed)
	if e != nil {
		return nil, nil, e
	}
	var m journalRunManifest
	if e = sourceDecode(raw, &m); e != nil || m.Steps == nil || m.Bindings == nil || owner == nil {
		return nil, nil, ErrJournalIntegrity
	}
	if j.runKey(m.Identity) != key || m.Owner != j.ownerKey(owner.Instance, owner.Generation) || m.Identity.Instance != owner.Instance || m.Identity.Generation != owner.Generation || reg.RunIDs[j.runSlot(m.Identity.Instance, m.Identity.Generation, m.Identity.Run)] != key {
		return nil, nil, ErrJournalIntegrity
	}
	var binding, initial []byte
	var started int64
	if e = tx.QueryRowContext(ctx, "SELECT binding,initial,started FROM torrenttransfer_runs WHERE id=?", key).Scan(&binding, &initial, &started); e != nil {
		if e == sql.ErrNoRows {
			return nil, nil, ErrJournalIntegrity
		}
		return nil, nil, e
	}
	if digest(binding) != m.Binding || digest(initial) != m.Initial || started != m.Started {
		return nil, nil, ErrJournalIntegrity
	}
	plainBinding, e := j.open(key+"/binding", binding)
	if e != nil {
		return nil, nil, e
	}
	identity, _ := json.Marshal(m.Identity)
	if !bytes.Equal(plainBinding, identity) {
		return nil, nil, ErrJournalIntegrity
	}
	plainInitial, e := j.open(key+"/initial", initial)
	if e != nil {
		return nil, nil, e
	}
	rows, e := tx.QueryContext(ctx, "SELECT seq,request,kind,state,result FROM torrenttransfer_steps WHERE run=? ORDER BY seq", key)
	if e != nil {
		return nil, nil, e
	}
	high := 0
	for rows.Next() {
		var seq int
		var s journalStoredStep
		if e = rows.Scan(&seq, &s.Request, &s.Kind, &s.State, &s.Sealed); e != nil {
			rows.Close()
			return nil, nil, e
		}
		s.Result = digest(s.Sealed)
		if seq != high+1 || seq > len(m.Steps) || s.journalStepManifest != m.Steps[seq-1] || (s.Kind != "read" && s.Kind != "mutation" && s.Kind != "local") || (s.State != "pending" && s.State != "complete") || (s.State == "pending" && (s.Sealed != nil || s.Kind == "local")) || (s.State == "complete" && len(s.Sealed) == 0) {
			rows.Close()
			return nil, nil, ErrJournalIntegrity
		}
		high++
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, nil, e
	}
	if high != len(m.Steps) {
		return nil, nil, ErrJournalIntegrity
	}
	for slot, expected := range m.Bindings {
		if owner.Values["binding/"+key+"/"+slot] != expected {
			return nil, nil, ErrJournalIntegrity
		}
	}
	return &m, plainInitial, nil
}
func (j *Journal) saveRunTx(ctx context.Context, tx *sql.Tx, reg *journalRegistry, key string, m *journalRunManifest) error {
	raw, e := json.Marshal(m)
	if e != nil {
		return e
	}
	sealed, e := j.seal("run-manifest/"+key, raw)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO torrenttransfer_run_manifests(namespace,run,body) VALUES(?,?,?) ON CONFLICT(namespace,run) DO UPDATE SET body=excluded.body", j.namespace, key, sealed); e != nil {
		return e
	}
	reg.Runs[key] = digest(sealed)
	reg.RunIDs[j.runSlot(m.Identity.Instance, m.Identity.Generation, m.Identity.Run)] = key
	return nil
}
func (j *Journal) runStateTx(ctx context.Context, tx *sql.Tx, id RunIdentity) (*journalRegistry, *journalOwnerManifest, *journalRunManifest, []byte, error) {
	reg, e := j.readRegistryTx(ctx, tx)
	if e != nil {
		return nil, nil, nil, nil, e
	}
	owner, e := j.loadOwnerTx(ctx, tx, reg, id.Instance, id.Generation)
	if e != nil {
		return nil, nil, nil, nil, e
	}
	key := j.runKey(id)
	if old, ok := reg.RunIDs[j.runSlot(id.Instance, id.Generation, id.Run)]; ok && old != key {
		return nil, nil, nil, nil, ErrReplayDiverged
	}
	run, initial, e := j.loadRunTx(ctx, tx, reg, owner, key)
	if e != nil {
		return nil, nil, nil, nil, e
	}
	return reg, owner, run, initial, nil
}

// bind prevents a resumed owner from substituting downloader/site credentials,
// roots, or API endpoints while retaining a previously authorized run ID.
func (r *JournalRun) bind(ctx context.Context, slot string, input any) error {
	if e := r.guard(ctx); e != nil {
		return e
	}
	raw, e := json.Marshal(input)
	if e != nil {
		return e
	}
	j := r.journal
	j.mu.Lock()
	defer j.mu.Unlock()
	tx, e := j.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	reg, owner, run, _, e := j.runStateTx(ctx, tx, r.identity)
	if e != nil {
		return e
	}
	if run == nil {
		return ErrJournalIntegrity
	}
	key := "binding/" + r.key + "/" + slot
	if _, exists := run.Bindings[slot]; exists {
		previous, found, e := j.valueTx(ctx, tx, owner, key)
		if e != nil {
			return e
		}
		if !found || !bytes.Equal(previous, raw) {
			return ErrReplayDiverged
		}
		return tx.Commit()
	}
	if _, exists := owner.Values[key]; exists {
		return ErrJournalIntegrity
	}
	if e = j.putValueTx(ctx, tx, owner, key, raw); e != nil {
		return e
	}
	run.Bindings[slot] = owner.Values[key]
	if e = j.saveOwnerTx(ctx, tx, reg, owner); e != nil {
		return e
	}
	if e = j.saveRunTx(ctx, tx, reg, r.key, run); e != nil {
		return e
	}
	if e = j.saveRegistryTx(ctx, tx, reg); e != nil {
		return e
	}
	return tx.Commit()
}
func (j *Journal) Close() error {
	if j.owned {
		return j.db.Close()
	}
	return nil
}
func (j *Journal) seal(aad string, v []byte) ([]byte, error) {
	n := make([]byte, j.aead.NonceSize())
	if _, e := io.ReadFull(rand.Reader, n); e != nil {
		return nil, e
	}
	return j.aead.Seal(n, n, v, []byte(aad)), nil
}
func (j *Journal) open(aad string, v []byte) ([]byte, error) {
	n := j.aead.NonceSize()
	if len(v) < n {
		return nil, ErrJournalIntegrity
	}
	b, e := j.aead.Open(nil, v[:n], v[n:], []byte(aad))
	if e != nil {
		return nil, ErrJournalIntegrity
	}
	return b, nil
}
func digest(v []byte) string { s := sha256.Sum256(v); return hex.EncodeToString(s[:]) }
func sourceDecode(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e := d.Decode(out); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing stored value")
	}
	return nil
}

// Begin freezes the original identity and initial state. The authenticated RunID
// index rejects creating another timeline by changing revision or fingerprint.
func (j *Journal) Begin(ctx context.Context, id RunIdentity, initial any, guard func(context.Context) error) (*JournalRun, error) {
	if guard == nil || id.Instance == "" || id.Generation == 0 || id.Run == "" || id.Fingerprint == "" {
		return nil, errors.New("TorrentTransfer immutable owner binding required")
	}
	if e := guard(ctx); e != nil {
		return nil, e
	}
	binding, e := json.Marshal(id)
	if e != nil {
		return nil, e
	}
	input, e := json.Marshal(initial)
	if e != nil {
		return nil, e
	}
	key := j.runKey(id)
	j.mu.Lock()
	defer j.mu.Unlock()
	tx, e := j.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	reg, owner, run, previous, e := j.runStateTx(ctx, tx, id)
	if e != nil {
		return nil, e
	}
	if run == nil {
		if owner == nil {
			owner = &journalOwnerManifest{Instance: id.Instance, Generation: id.Generation, Values: map[string]string{}}
		}
		b, e := j.seal(key+"/binding", binding)
		if e != nil {
			return nil, e
		}
		i, e := j.seal(key+"/initial", input)
		if e != nil {
			return nil, e
		}
		run = &journalRunManifest{Identity: id, Owner: j.ownerKey(id.Instance, id.Generation), Binding: digest(b), Initial: digest(i), Started: time.Now().UnixNano(), Steps: []journalStepManifest{}, Bindings: map[string]string{}}
		if _, e = tx.ExecContext(ctx, "INSERT INTO torrenttransfer_runs(id,binding,initial,started) VALUES(?,?,?,?)", key, b, i, run.Started); e != nil {
			return nil, e
		}
		if e = j.saveOwnerTx(ctx, tx, reg, owner); e != nil {
			return nil, e
		}
		if e = j.saveRunTx(ctx, tx, reg, key, run); e != nil {
			return nil, e
		}
		if e = j.saveRegistryTx(ctx, tx, reg); e != nil {
			return nil, e
		}
	} else if !bytes.Equal(previous, input) {
		return nil, ErrReplayDiverged
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return &JournalRun{journal: j, identity: id, key: key, guard: guard, Started: time.Unix(0, run.Started)}, nil
}
func (r *JournalRun) ReceiptID() string { return fmt.Sprintf("%s/%d", r.key, r.seq) }
func (r *JournalRun) SourceConfig(ctx context.Context, value map[string]any) error {
	return r.localWrite(ctx, "config", "config", value)
}

// step commits intent and its authenticated highwater in one transaction before
// calling external IO. Result completion and its updated manifest also commit
// atomically. Missing rows at/below the highwater are never treated as new work.
func (r *JournalRun) step(ctx context.Context, op string, input any, mutation bool, out any, fn func() (any, string, error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.guard(ctx); e != nil {
		return e
	}
	raw, e := json.Marshal(struct {
		Op    string
		Input any
	}{op, input})
	if e != nil {
		return e
	}
	r.seq++
	seq := r.seq
	req := digest(raw)
	kind := "read"
	if mutation {
		kind = "mutation"
	}
	aad := fmt.Sprintf("%s/%d/%s/%s", r.key, seq, kind, req)
	j := r.journal
	var replay *stepResult
	e = func() error {
		j.mu.Lock()
		defer j.mu.Unlock()
		tx, e := j.db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		reg, _, run, _, e := j.runStateTx(ctx, tx, r.identity)
		if e != nil {
			return e
		}
		if run == nil {
			return ErrJournalIntegrity
		}
		if seq <= len(run.Steps) {
			s := run.Steps[seq-1]
			if s.Request != req || s.Kind != kind {
				return ErrReplayDiverged
			}
			if s.State == "pending" {
				if mutation {
					return ErrUnconfirmed
				}
				return tx.Commit()
			}
			var sealed []byte
			if e = tx.QueryRowContext(ctx, "SELECT result FROM torrenttransfer_steps WHERE run=? AND seq=?", r.key, seq).Scan(&sealed); e != nil {
				return ErrJournalIntegrity
			}
			plain, e := j.open(aad, sealed)
			if e != nil {
				return e
			}
			var saved stepResult
			if e = sourceDecode(plain, &saved); e != nil {
				return ErrJournalIntegrity
			}
			if saved.Outcome != "read" && saved.Outcome != "confirmed" && saved.Outcome != "rejected" && saved.Outcome != "unknown" {
				return ErrJournalIntegrity
			}
			replay = &saved
			return tx.Commit()
		}
		if seq != len(run.Steps)+1 {
			return ErrReplayDiverged
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO torrenttransfer_steps(run,seq,request,kind,state) VALUES(?,?,?,?, 'pending')", r.key, seq, req, kind); e != nil {
			return e
		}
		run.Steps = append(run.Steps, journalStepManifest{Request: req, Kind: kind, State: "pending", Result: digest(nil)})
		if e = j.saveRunTx(ctx, tx, reg, r.key, run); e != nil {
			return e
		}
		if e = j.saveRegistryTx(ctx, tx, reg); e != nil {
			return e
		}
		return tx.Commit()
	}()
	if e != nil {
		return e
	}
	if replay != nil {
		if replay.Outcome == "unknown" {
			return ErrUnconfirmed
		}
		if replay.Fault != "" {
			if replay.Outcome == "rejected" && (replay.Fault == "owner_denied" || replay.Fault == "cancelled") {
				return nativeStoppedError(replay.Fault)
			}
			return errors.New("TorrentTransfer recorded operation failed")
		}
		return sourceDecode(replay.Value, out)
	}
	var value any
	var outcome string
	var callErr error
	if e = r.guard(ctx); e != nil {
		// This exact stack has not entered the operation callback. Persist that
		// private phase fact, rather than leaving a known-undispatched mutation
		// looking like a crash after an external call.
		if mutation {
			class := "owner_denied"
			if ctx.Err() != nil || errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
				class = "cancelled"
			}
			outcome = "rejected"
			callErr = &nativeStopped{class, nativeStoppedProof}
		} else {
			outcome = "read"
			callErr = e
		}
	} else {
		value, outcome, callErr = fn()
	}
	if !mutation && outcome == "" {
		outcome = "read"
	}
	if mutation && outcome != "confirmed" && outcome != "rejected" {
		outcome = "unknown"
	}
	result := stepResult{Outcome: outcome}
	result.Value, e = json.Marshal(value)
	if e != nil {
		return e
	}
	if callErr != nil {
		result.Fault = "operation failed"
		if class := nativeStoppedClass(callErr); mutation && outcome == "rejected" && class != "" {
			result.Fault = class
		} else if mutation {
			result.Outcome = "unknown"
		}
	}
	plain, e := json.Marshal(result)
	if e != nil {
		return e
	}
	sealed, e := j.seal(aad, plain)
	if e != nil {
		return e
	}
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	e = func() error {
		j.mu.Lock()
		defer j.mu.Unlock()
		tx, e := j.db.BeginTx(persist, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		reg, _, run, _, e := j.runStateTx(persist, tx, r.identity)
		if e != nil {
			return e
		}
		if run == nil || seq > len(run.Steps) {
			return ErrJournalIntegrity
		}
		s := &run.Steps[seq-1]
		if s.Request != req || s.Kind != kind || s.State != "pending" {
			return ErrReplayDiverged
		}
		res, e := tx.ExecContext(persist, "UPDATE torrenttransfer_steps SET state='complete',result=? WHERE run=? AND seq=? AND request=? AND kind=? AND state='pending'", sealed, r.key, seq, req, kind)
		if e != nil {
			return e
		}
		n, e := res.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrJournalIntegrity
		}
		s.State = "complete"
		s.Result = digest(sealed)
		if e = j.saveRunTx(persist, tx, reg, r.key, run); e != nil {
			return e
		}
		if e = j.saveRegistryTx(persist, tx, reg); e != nil {
			return e
		}
		return tx.Commit()
	}()
	if e != nil {
		return e
	}
	if e = r.guard(ctx); e != nil {
		return e
	}
	if result.Outcome == "unknown" {
		return ErrUnconfirmed
	}
	if callErr != nil {
		if class := nativeStoppedClass(callErr); class != "" && result.Outcome == "rejected" {
			return nativeStoppedError(class)
		}
		return errors.New("TorrentTransfer operation failed")
	}
	return sourceDecode(result.Value, out)
}

// LocalCommit must use only its supplied transaction and return an in-memory
// afterCommit hook. It must not call the DB pool (which can have one connection).
// A replay verifies the sealed receipt and manifests, and never repeats the CAS.
func (r *JournalRun) localWrite(ctx context.Context, op, key string, value any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.guard(ctx); e != nil {
		return e
	}
	raw, e := json.Marshal(struct {
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
	r.seq++
	seq := r.seq
	req := digest(raw)
	aad := fmt.Sprintf("%s/%d/local/%s", r.key, seq, req)
	j := r.journal
	j.mu.Lock()
	defer j.mu.Unlock()
	tx, e := j.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	reg, owner, run, _, e := j.runStateTx(ctx, tx, r.identity)
	if e != nil {
		return e
	}
	if run == nil {
		return ErrJournalIntegrity
	}
	if seq <= len(run.Steps) {
		s := run.Steps[seq-1]
		if s.Request != req || s.Kind != "local" || s.State != "complete" {
			return ErrReplayDiverged
		}
		var sealed []byte
		if e = tx.QueryRowContext(ctx, "SELECT result FROM torrenttransfer_steps WHERE run=? AND seq=?", r.key, seq).Scan(&sealed); e != nil {
			return ErrJournalIntegrity
		}
		plain, e := j.open(aad, sealed)
		if e != nil {
			return e
		}
		var result stepResult
		if e = sourceDecode(plain, &result); e != nil || result.Outcome != "confirmed" || result.Fault != "" || string(result.Value) != "null" {
			return ErrJournalIntegrity
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		if r.LocalObserved != nil {
			r.LocalObserved(op, key, value)
		}
		return nil
	}
	if seq != len(run.Steps)+1 {
		return ErrReplayDiverged
	}
	var afterCommit func()
	if r.LocalCommit != nil {
		afterCommit, e = r.LocalCommit(ctx, tx, r.identity, op, key, value)
		if e != nil {
			return e
		}
		// A hook may publish another checkpoint using this same transaction.
		// Reload its authenticated heads before merging our value and receipt.
		reg, owner, run, _, e = j.runStateTx(ctx, tx, r.identity)
		if e != nil {
			return e
		}
		if run == nil || seq != len(run.Steps)+1 {
			return ErrReplayDiverged
		}
	}
	if e = j.putValueTx(ctx, tx, owner, key, body); e != nil {
		return e
	}
	receipt, e := json.Marshal(stepResult{Value: json.RawMessage("null"), Outcome: "confirmed"})
	if e != nil {
		return e
	}
	sealed, e := j.seal(aad, receipt)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO torrenttransfer_steps(run,seq,request,kind,state,result) VALUES(?,?,?,'local','complete',?)", r.key, seq, req, sealed); e != nil {
		return e
	}
	run.Steps = append(run.Steps, journalStepManifest{Request: req, Kind: "local", State: "complete", Result: digest(sealed)})
	if e = j.saveOwnerTx(ctx, tx, reg, owner); e != nil {
		return e
	}
	if e = j.saveRunTx(ctx, tx, reg, r.key, run); e != nil {
		return e
	}
	if e = j.saveRegistryTx(ctx, tx, reg); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	if afterCommit != nil {
		afterCommit()
	}
	if r.LocalObserved != nil {
		r.LocalObserved(op, key, value)
	}
	return nil
}
func (r *JournalRun) readValue(ctx context.Context, key string) (any, error) {
	var result any
	e := r.step(ctx, "local-read", key, false, &result, func() (any, string, error) {
		j := r.journal
		raw, found, e := func() ([]byte, bool, error) {
			j.mu.Lock()
			defer j.mu.Unlock()
			tx, e := j.db.BeginTx(ctx, nil)
			if e != nil {
				return nil, false, e
			}
			defer tx.Rollback()
			_, owner, run, _, e := j.runStateTx(ctx, tx, r.identity)
			if e != nil {
				return nil, false, e
			}
			if run == nil {
				return nil, false, ErrJournalIntegrity
			}
			raw, found, e := j.valueTx(ctx, tx, owner, key)
			if e != nil {
				return nil, false, e
			}
			if e = tx.Commit(); e != nil {
				return nil, false, e
			}
			return raw, found, nil
		}()
		if e != nil {
			return nil, "read", e
		}
		if !found {
			if r.LocalRead != nil {
				v, e := r.LocalRead(ctx, key)
				return v, "read", e
			}
			return nil, "read", nil
		}
		var value any
		e = sourceDecode(raw, &value)
		return value, "read", e
	})
	return result, e
}

// Identity is a read-only copy of the trusted current durable admission.
func (r *JournalRun) Identity() RunIdentity { return r.identity }
