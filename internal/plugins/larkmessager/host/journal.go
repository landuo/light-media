// Package larkhost provides the native owner's durable Lark boundaries. It is
// not an SDK capability and does not install or enable a plugin by itself.
package larkhost

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
	"regexp"
	"strconv"
	"strings"

	lark "light-media/internal/plugins/larkmessager"
)

var ErrIntegrity = errors.New("Lark native journal evidence is missing or changed")
var ErrReplay = errors.New("Lark native journal invocation differs from its admission")

const schema = `CREATE TABLE IF NOT EXISTS lark_native_roots(namespace TEXT PRIMARY KEY,sealed BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS lark_native_effects(namespace TEXT NOT NULL,effect_id TEXT NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(namespace,effect_id));`

// Group is fixed by the actual installation generation, not worker input.
type Group struct {
	Owner      string `json:"owner"`
	Generation int64  `json:"generation"`
}

// Scope binds an invocation to its original input and configuration snapshots.
// The native App's guard must validate this against its actual admitted run.
type Scope struct {
	Group
	Revision     int64  `json:"revision"`
	InvocationID string `json:"invocation_id"`
	// SourceID is the actual producer's stable queue/request/module-call ID.
	// A new App run must never turn an old source fact into a new send attempt.
	SourceID  string `json:"source_id"`
	InputSHA  string `json:"input_sha"`
	ConfigSHA string `json:"config_sha"`
	Action    string `json:"action"`
}

type Anchor struct{ Namespace, Digest string }

// Authority validates an independent installation root and advances it in the
// same transaction as journal mutations. A second unauthenticated SQL counter
// is not an authority. A complete database rollback needs an external freshness
// anchor; this journal does not claim to detect one by itself.
type Authority interface {
	Validate(context.Context, *sql.Tx, Anchor) error
	Advance(context.Context, *sql.Tx, Anchor, Anchor) error
}

type Guard func(context.Context, *sql.Tx, Scope) error

type effectIndex struct {
	Digest string `json:"digest"`
	State  string `json:"state"`
}
type runIndex struct {
	Scope   Scope                  `json:"scope"`
	Effects map[string]effectIndex `json:"effects"`
}
type root struct {
	Version int                  `json:"version"`
	Group   Group                `json:"group"`
	Runs    map[string]*runIndex `json:"runs"`
	Sources map[string]string    `json:"sources"`
}
type cell struct {
	Version int          `json:"version"`
	Scope   Scope        `json:"scope"`
	Receipt lark.Receipt `json:"receipt"`
}

type Store struct {
	db        *sql.DB
	group     Group
	namespace string
	aead      cipher.AEAD
	authority Authority
	guard     Guard
	// Native tests can inject a lost local acknowledgement only after Commit.
	// No request, SDK payload or configuration can set this hook.
	afterCommit func(string) error
}
type Run struct {
	store *Store
	scope Scope
}

var _ lark.Journal = (*Run)(nil)
var storageID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func validGroup(g Group) bool { return storageID.MatchString(g.Owner) && g.Generation > 0 }
func sha(raw []byte) string   { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func validSHA(value string) bool {
	decoded, e := hex.DecodeString(value)
	return e == nil && len(decoded) == 32 && value == strings.ToLower(value)
}
func validScope(s Scope) bool {
	return validGroup(s.Group) && s.Revision > 0 && storageID.MatchString(s.InvocationID) && s.SourceID != "" && len(s.SourceID) <= 512 && !strings.ContainsAny(s.SourceID, "\x00\r\n") && s.Action != "" && len(s.Action) <= 128 && validSHA(s.InputSHA) && validSHA(s.ConfigSHA)
}
func strict(raw []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(target); e != nil || d.Decode(new(any)) != io.EOF {
		return ErrIntegrity
	}
	return nil
}
func newStore(db *sql.DB, key []byte, group Group) (*Store, error) {
	if db == nil || len(key) != 32 || !validGroup(group) {
		return nil, lark.ErrDenied
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	mac := hmac.New(sha256.New, key)
	_, _ = fmt.Fprintf(mac, "light-media/lark-native-journal/v1/%s/%d", group.Owner, group.Generation)
	return &Store{db: db, group: group, namespace: hex.EncodeToString(mac.Sum(nil)), aead: aead}, nil
}
func (s *Store) seal(key string, value any) ([]byte, error) {
	plain, e := json.Marshal(value)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, e = io.ReadFull(rand.Reader, nonce); e != nil {
		return nil, e
	}
	return s.aead.Seal(nonce, nonce, plain, []byte(s.namespace+"/"+key)), nil
}
func (s *Store) open(key string, sealed []byte, target any) error {
	n := s.aead.NonceSize()
	if len(sealed) < n+s.aead.Overhead() {
		return ErrIntegrity
	}
	plain, e := s.aead.Open(nil, sealed[:n], sealed[n:], []byte(s.namespace+"/"+key))
	if e != nil {
		return ErrIntegrity
	}
	return strict(plain, target)
}
func rootAnchor(namespace string, r *root) Anchor {
	raw, _ := json.Marshal(r)
	return Anchor{namespace, sha(raw)}
}

// GenesisTx is only for the trusted installation/lifecycle producer. The caller
// must validate retirement of the previous generation before calling it and
// persist the returned anchor with the actual installation in this same tx.
// Attachment and ordinary run calls never initialize a missing namespace.
func GenesisTx(ctx context.Context, db *sql.DB, tx *sql.Tx, key []byte, group Group) (Anchor, error) {
	if tx == nil {
		return Anchor{}, lark.ErrDenied
	}
	s, e := newStore(db, key, group)
	if e != nil {
		return Anchor{}, e
	}
	if _, e = tx.ExecContext(ctx, schema); e != nil {
		return Anchor{}, e
	}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM lark_native_roots WHERE namespace=?)+(SELECT COUNT(*) FROM lark_native_effects WHERE namespace=?)`, s.namespace, s.namespace).Scan(&count); e != nil {
		return Anchor{}, e
	}
	if count != 0 {
		return Anchor{}, ErrIntegrity
	}
	r := &root{Version: 1, Group: group, Runs: map[string]*runIndex{}, Sources: map[string]string{}}
	sealed, e := s.seal("root", r)
	if e != nil {
		return Anchor{}, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO lark_native_roots VALUES(?,?)`, s.namespace, sealed); e != nil {
		return Anchor{}, e
	}
	return rootAnchor(s.namespace, r), nil
}

func Attach(ctx context.Context, db *sql.DB, key []byte, group Group, authority Authority, guard Guard) (*Store, error) {
	if authority == nil || guard == nil {
		return nil, lark.ErrDenied
	}
	s, e := newStore(db, key, group)
	if e != nil {
		return nil, e
	}
	s.authority, s.guard = authority, guard
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if _, _, _, e = s.load(ctx, tx); e != nil {
		return nil, e
	}
	return s, tx.Commit()
}

func (s *Store) load(ctx context.Context, tx *sql.Tx) (*root, []byte, map[string]cell, error) {
	var sealed []byte
	if e := tx.QueryRowContext(ctx, `SELECT sealed FROM lark_native_roots WHERE namespace=?`, s.namespace).Scan(&sealed); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return nil, nil, nil, ErrIntegrity
		}
		return nil, nil, nil, e
	}
	var r root
	if s.open("root", sealed, &r) != nil || r.Version != 1 || r.Group != s.group || r.Runs == nil || r.Sources == nil || len(r.Sources) != len(r.Runs) {
		return nil, nil, nil, ErrIntegrity
	}
	if e := s.authority.Validate(ctx, tx, rootAnchor(s.namespace, &r)); e != nil {
		return nil, nil, nil, e
	}
	wanted := map[string]effectIndex{}
	scopes := map[string]Scope{}
	for invocation, run := range r.Runs {
		if run == nil || !validScope(run.Scope) || run.Scope.Group != s.group || run.Scope.InvocationID != invocation || r.Sources[run.Scope.SourceID] != invocation || run.Effects == nil {
			return nil, nil, nil, ErrIntegrity
		}
		for id, index := range run.Effects {
			if !validIntentID(run.Scope, id) || !validSHA(index.Digest) || !validState(index.State) {
				return nil, nil, nil, ErrIntegrity
			}
			wanted[id], scopes[id] = index, run.Scope
		}
	}
	rows, e := tx.QueryContext(ctx, `SELECT effect_id,sealed FROM lark_native_effects WHERE namespace=? ORDER BY effect_id`, s.namespace)
	if e != nil {
		return nil, nil, nil, e
	}
	defer rows.Close()
	cells := map[string]cell{}
	for rows.Next() {
		var id string
		var value []byte
		if e = rows.Scan(&id, &value); e != nil {
			return nil, nil, nil, e
		}
		index, found := wanted[id]
		if !found || index.Digest != sha(value) {
			return nil, nil, nil, ErrIntegrity
		}
		// Authenticate every stored ciphertext, without retaining every prior
		// response body in memory. The selected receipt is decrypted below only
		// when it is read or completed.
		cells[id] = cell{Version: 1, Scope: scopes[id], Receipt: lark.Receipt{State: index.State}}
	}
	if e = rows.Err(); e != nil {
		return nil, nil, nil, e
	}
	if len(cells) != len(wanted) {
		return nil, nil, nil, ErrIntegrity
	}
	return &r, sealed, cells, nil
}

func (s *Store) readCell(ctx context.Context, tx *sql.Tx, r *root, scope Scope, id string) (cell, error) {
	var c cell
	run := r.Runs[scope.InvocationID]
	if run == nil || run.Scope != scope {
		return c, ErrReplay
	}
	index, found := run.Effects[id]
	if !found {
		return c, ErrReplay
	}
	var value []byte
	if e := tx.QueryRowContext(ctx, `SELECT sealed FROM lark_native_effects WHERE namespace=? AND effect_id=?`, s.namespace, id).Scan(&value); e != nil {
		return c, ErrIntegrity
	}
	if sha(value) != index.Digest || s.open("effect/"+id, value, &c) != nil || c.Version != 1 || c.Scope != scope || c.Receipt.Intent.ID != id || !validIntent(scope, c.Receipt.Intent) || c.Receipt.State != index.State {
		return c, ErrIntegrity
	}
	return c, nil
}

func validState(state string) bool {
	return state == "intent" || state == "confirmed" || state == "rejected" || state == "unknown"
}
func validIntentID(scope Scope, id string) bool {
	prefix := fmt.Sprintf("%s/%d/%d/%s/", scope.Owner, scope.Generation, scope.Revision, scope.InvocationID)
	if !strings.HasPrefix(id, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(id, prefix)
	n, e := strconv.ParseUint(suffix, 10, 64)
	return e == nil && n != 0 && strconv.FormatUint(n, 10) == suffix
}
func validIntent(scope Scope, intent lark.Intent) bool {
	if intent.Owner != scope.Owner || intent.Generation != scope.Generation || intent.Revision != scope.Revision || !validIntentID(scope, intent.ID) || !validSHA(intent.Digest) {
		return false
	}
	switch intent.Op {
	case "send", "reply", "edit", "reaction_add", "reaction_delete", "card_create", "card_content", "card_close", "upload_image", "upload_file":
		return true
	}
	return false
}
func uncertain(cells map[string]cell, scope Scope) bool {
	for _, c := range cells {
		if c.Scope == scope && (c.Receipt.State == "intent" || c.Receipt.State == "unknown") {
			return true
		}
	}
	return false
}

func (s *Store) save(ctx context.Context, tx *sql.Tx, previous []byte, before Anchor, r *root) error {
	sealed, e := s.seal("root", r)
	if e != nil {
		return e
	}
	result, e := tx.ExecContext(ctx, `UPDATE lark_native_roots SET sealed=? WHERE namespace=? AND sealed=?`, sealed, s.namespace, previous)
	if e != nil {
		return e
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		return ErrIntegrity
	}
	return s.authority.Advance(ctx, tx, before, rootAnchor(s.namespace, r))
}
func (s *Store) commit(tx *sql.Tx, operation string) error {
	if e := tx.Commit(); e != nil {
		return lark.ErrUnknown
	}
	if s.afterCommit != nil && s.afterCommit(operation) != nil {
		return lark.ErrUnknown
	}
	return nil
}

func (s *Store) ForRun(ctx context.Context, scope Scope) (*Run, error) {
	if !validScope(scope) || scope.Group != s.group {
		return nil, lark.ErrDenied
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	r, previous, _, e := s.load(ctx, tx)
	if e != nil {
		return nil, e
	}
	if e = s.guard(ctx, tx, scope); e != nil {
		return nil, e
	}
	if current, found := r.Runs[scope.InvocationID]; found {
		if current.Scope != scope {
			return nil, ErrReplay
		}
		return &Run{s, scope}, tx.Commit()
	}
	if _, exists := r.Sources[scope.SourceID]; exists {
		return nil, ErrReplay
	}
	before := rootAnchor(s.namespace, r)
	r.Runs[scope.InvocationID] = &runIndex{Scope: scope, Effects: map[string]effectIndex{}}
	r.Sources[scope.SourceID] = scope.InvocationID
	if e = s.save(ctx, tx, previous, before, r); e != nil {
		return nil, e
	}
	if e = s.commit(tx, "admit"); e != nil {
		return nil, e
	}
	return &Run{s, scope}, nil
}

// ValidateRun is for the native Adapter's before/after-auth guard. It performs
// no IO to Lark and never creates a run or a missing root.
func (s *Store) ValidateRun(ctx context.Context, scope Scope) error {
	tx, e := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	r, _, _, e := s.load(ctx, tx)
	if e != nil {
		return e
	}
	current := r.Runs[scope.InvocationID]
	if current == nil || current.Scope != scope {
		return ErrReplay
	}
	if e = s.guard(ctx, tx, scope); e != nil {
		return e
	}
	return tx.Commit()
}

func (run *Run) Begin(ctx context.Context, intent lark.Intent) (*lark.Receipt, error) {
	s := run.store
	if !validIntent(run.scope, intent) {
		return nil, lark.ErrDenied
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	r, previous, cells, e := s.load(ctx, tx)
	if e != nil {
		return nil, e
	}
	current := r.Runs[run.scope.InvocationID]
	if current == nil || current.Scope != run.scope {
		return nil, ErrReplay
	}
	if e = s.guard(ctx, tx, run.scope); e != nil {
		return nil, e
	}
	if _, found := cells[intent.ID]; found {
		existing, e := s.readCell(ctx, tx, r, run.scope, intent.ID)
		if e != nil {
			return nil, e
		}
		if existing.Scope != run.scope || existing.Receipt.Intent != intent {
			return nil, ErrReplay
		}
		if existing.Receipt.State == "intent" || existing.Receipt.State == "unknown" {
			return nil, lark.ErrUnknown
		}
		if e = tx.Commit(); e != nil {
			return nil, e
		}
		result := existing.Receipt
		return &result, nil
	}
	if uncertain(cells, run.scope) {
		return nil, lark.ErrUnknown
	}
	before := rootAnchor(s.namespace, r)
	value, e := s.seal("effect/"+intent.ID, cell{1, run.scope, lark.Receipt{Intent: intent, State: "intent"}})
	if e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO lark_native_effects VALUES(?,?,?)`, s.namespace, intent.ID, value); e != nil {
		return nil, e
	}
	current.Effects[intent.ID] = effectIndex{sha(value), "intent"}
	if e = s.save(ctx, tx, previous, before, r); e != nil {
		return nil, e
	}
	return nil, s.commit(tx, "begin")
}

func (run *Run) Finish(ctx context.Context, receipt lark.Receipt) error {
	s := run.store
	if !validIntent(run.scope, receipt.Intent) || receipt.State == "intent" || !validState(receipt.State) {
		return lark.ErrDenied
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	r, previous, _, e := s.load(ctx, tx)
	if e != nil {
		return e
	}
	current := r.Runs[run.scope.InvocationID]
	old, e := s.readCell(ctx, tx, r, run.scope, receipt.Intent.ID)
	if e != nil {
		return e
	}
	if current == nil || current.Scope != run.scope || old.Scope != run.scope || old.Receipt.Intent != receipt.Intent {
		return ErrReplay
	}
	// Finishing records the result of an already admitted effect; it grants no
	// permission to issue IO. A configuration change may revoke dispatch after
	// authentication, yet the old owner must still persist that rejection (or
	// the observed response) instead of losing its exact intent's evidence.
	if old.Receipt.State != "intent" {
		left, _ := json.Marshal(old.Receipt)
		right, _ := json.Marshal(receipt)
		if !bytes.Equal(left, right) {
			return ErrReplay
		}
		return tx.Commit()
	}
	before := rootAnchor(s.namespace, r)
	value, e := s.seal("effect/"+receipt.Intent.ID, cell{1, run.scope, receipt})
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE lark_native_effects SET sealed=? WHERE namespace=? AND effect_id=?`, value, s.namespace, receipt.Intent.ID); e != nil {
		return e
	}
	current.Effects[receipt.Intent.ID] = effectIndex{sha(value), receipt.State}
	if e = s.save(ctx, tx, previous, before, r); e != nil {
		return e
	}
	return s.commit(tx, "finish")
}
