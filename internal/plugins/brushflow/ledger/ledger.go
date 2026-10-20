// Package brushledger stores BrushFlow's native proof collections in immutable,
// authenticated 16-way radix pages. It has no driver, pool or transaction owner.
// The App must retain Root in its independently authenticated lifecycle root;
// a worker-supplied Root (including Empty) is never an authorization grant.
package brushledger

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
	"strings"
	"unicode/utf8"
)

var ErrIntegrity = errors.New("BrushFlow ledger proof is missing or changed")
var ErrInput = errors.New("invalid BrushFlow ledger scope or input")

// Collections are the actual BrushFlow host proof namespaces, not extensible
// worker-selected database tables. Ordinals belong in exact logical keys.
type Collection string

const (
	Runs       Collection = "runs"
	Effects    Collection = "effects"
	Sources    Collection = "sources"
	Unresolved Collection = "unresolved"
	Values     Collection = "values"
	chunkBytes            = 64 << 10
)

type Root string

// Roots is fixed-size even when each collection contains many historical keys.
type Roots struct{ Runs, Effects, Sources, Unresolved, Values Root }

type Store struct {
	key       [32]byte
	namespace string
	aead      cipher.AEAD
}

type page struct {
	Version  int      `json:"v"`
	Kind     string   `json:"kind"`
	Path     string   `json:"path"`
	Children []string `json:"children,omitempty"`
	Key      string   `json:"key,omitempty"`
	ValueSHA string   `json:"value_sha,omitempty"`
	Size     int64    `json:"size,omitempty"`
	Head     string   `json:"head,omitempty"`
	Index    int64    `json:"index,omitempty"`
	Next     string   `json:"next,omitempty"`
	Data     []byte   `json:"data,omitempty"`
}

// New binds all roots, page IDs and AEAD associated data to a fixed native owner
// key, exact instance and generation. It never reads or initializes SQL rows.
func New(key []byte, instance string, generation int64) (*Store, error) {
	if len(key) != 32 || instance == "" || !utf8.ValidString(instance) || generation < 1 {
		return nil, ErrInput
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	s := &Store{aead: aead}
	copy(s.key[:], key)
	raw, _ := json.Marshal([]any{"light-media/brush-flow-ledger/v1", instance, generation})
	s.namespace = s.digest("scope", raw)
	return s, nil
}
func CreateSchema(ctx context.Context, tx *sql.Tx) error {
	if tx == nil {
		return ErrInput
	}
	_, e := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS plugin_brush_flow_ledger_pages(namespace TEXT NOT NULL,collection TEXT NOT NULL,id TEXT NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(namespace,collection,id))`)
	return e
}
func validCollection(c Collection) bool {
	return c == Runs || c == Effects || c == Sources || c == Unresolved || c == Values
}
func validDigest(v string) bool {
	b, e := hex.DecodeString(v)
	return e == nil && len(b) == 32 && v == strings.ToLower(v)
}
func (s *Store) digest(domain string, b []byte) string {
	m := hmac.New(sha256.New, s.key[:])
	m.Write([]byte(domain))
	m.Write([]byte{0})
	m.Write(b)
	return hex.EncodeToString(m.Sum(nil))
}
func (s *Store) id(c Collection, b []byte) string {
	return s.digest(s.namespace+"/"+string(c)+"/page", b)
}
func (s *Store) Empty(c Collection) Root {
	if !validCollection(c) {
		return ""
	}
	return Root(s.digest(s.namespace+"/"+string(c)+"/empty", nil))
}
func (s *Store) EmptyRoots() Roots {
	return Roots{s.Empty(Runs), s.Empty(Effects), s.Empty(Sources), s.Empty(Unresolved), s.Empty(Values)}
}
func (s *Store) keyPath(c Collection, key string) string {
	return s.digest(s.namespace+"/"+string(c)+"/key", []byte(key))
}
func (s *Store) aad(c Collection, id string) []byte {
	return []byte(s.namespace + "/" + string(c) + "/" + id)
}
func (s *Store) check(tx *sql.Tx, c Collection, r Root, key string) error {
	if s == nil || tx == nil || !validCollection(c) || !validDigest(string(r)) || !utf8.ValidString(key) {
		return ErrInput
	}
	return nil
}
func strict(raw []byte, p *page) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(p) != nil || d.Decode(new(any)) != io.EOF {
		return ErrIntegrity
	}
	return nil
}
func (s *Store) read(ctx context.Context, tx *sql.Tx, c Collection, id string) (page, error) {
	var p page
	if !validDigest(id) {
		return p, ErrIntegrity
	}
	var sealed []byte
	e := tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_brush_flow_ledger_pages WHERE namespace=? AND collection=? AND id=?`, s.namespace, string(c), id).Scan(&sealed)
	if errors.Is(e, sql.ErrNoRows) {
		return p, ErrIntegrity
	}
	if e != nil {
		return p, e
	}
	n := s.aead.NonceSize()
	if len(sealed) < n+s.aead.Overhead() {
		return p, ErrIntegrity
	}
	raw, e := s.aead.Open(nil, sealed[:n], sealed[n:], s.aad(c, id))
	if e != nil || s.id(c, raw) != id || strict(raw, &p) != nil || p.Version != 1 {
		return p, ErrIntegrity
	}
	// The serialized form is canonical. This also rejects ignored duplicate JSON
	// field spellings or noncanonical encodings before a page acquires authority.
	canonical, e := json.Marshal(p)
	if e != nil || !bytes.Equal(canonical, raw) {
		return p, ErrIntegrity
	}
	return p, nil
}
func (s *Store) putPage(ctx context.Context, tx *sql.Tx, c Collection, p page) (string, error) {
	raw, e := json.Marshal(p)
	if e != nil {
		return "", e
	}
	id := s.id(c, raw)
	nonce := make([]byte, s.aead.NonceSize())
	if _, e = io.ReadFull(rand.Reader, nonce); e != nil {
		return "", e
	}
	sealed := s.aead.Seal(nonce, nonce, raw, s.aad(c, id))
	result, e := tx.ExecContext(ctx, `INSERT INTO plugin_brush_flow_ledger_pages VALUES(?,?,?,?) ON CONFLICT(namespace,collection,id) DO NOTHING`, s.namespace, string(c), id, sealed)
	if e != nil {
		return "", e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return "", e
	}
	if n == 0 {
		old, e := s.read(ctx, tx, c, id)
		if e != nil {
			return "", e
		}
		b, _ := json.Marshal(old)
		if !bytes.Equal(raw, b) {
			return "", ErrIntegrity
		}
	}
	return id, nil
}
func validPath(path string) bool {
	if len(path) > 64 {
		return false
	}
	for _, v := range path {
		if v < '0' || v > '9' && v < 'a' || v > 'f' {
			return false
		}
	}
	return true
}
func branch(p page) bool {
	if p.Kind != "branch" || len(p.Path) >= 64 || !validPath(p.Path) || len(p.Children) != 16 || p.Key != "" || p.ValueSHA != "" || p.Size != 0 || p.Head != "" || p.Index != 0 || p.Next != "" || len(p.Data) != 0 {
		return false
	}
	populated := 0
	for _, child := range p.Children {
		if child != "" {
			if !validDigest(child) {
				return false
			}
			populated++
		}
	}
	return populated >= 2
}
func location(p page, prefix string) bool {
	if !strings.HasPrefix(p.Path, prefix) {
		return false
	}
	if p.Kind == "branch" {
		return branch(p)
	}
	return p.Kind == "leaf" && len(p.Path) == 64 && validPath(p.Path)
}
func leaf(p page, path, key string) bool {
	return p.Kind == "leaf" && p.Path == path && p.Key == key && len(p.Children) == 0 && validDigest(p.ValueSHA) && p.Size >= 0 && ((p.Size == 0 && p.Head == "") || (p.Size > 0 && validDigest(p.Head))) && p.Index == 0 && p.Next == "" && len(p.Data) == 0
}
func nibble(v byte) int {
	if v >= '0' && v <= '9' {
		return int(v - '0')
	}
	return int(v-'a') + 10
}
func (s *Store) value(ctx context.Context, tx *sql.Tx, c Collection, p page) ([]byte, error) {
	var out bytes.Buffer
	id := p.Head
	index := int64(0)
	remaining := p.Size
	h := sha256.New()
	for remaining > 0 {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		q, e := s.read(ctx, tx, c, id)
		if e != nil {
			return nil, e
		}
		want := int64(chunkBytes)
		if remaining < want {
			want = remaining
		}
		if q.Kind != "chunk" || q.Path != p.Path || q.Key != "" || len(q.Children) != 0 || q.ValueSHA != "" || q.Size != 0 || q.Head != "" || q.Index != index || int64(len(q.Data)) != want || ((remaining == want && q.Next != "") || (remaining > want && !validDigest(q.Next))) {
			return nil, ErrIntegrity
		}
		h.Write(q.Data)
		out.Write(q.Data)
		remaining -= want
		index++
		id = q.Next
	}
	if id != "" || hex.EncodeToString(h.Sum(nil)) != p.ValueSHA {
		return nil, ErrIntegrity
	}
	return out.Bytes(), nil
}

// Lookup proves membership or absence from the supplied authenticated root.
// Missing SQL rows are integrity errors, never a proof of absence.
func (s *Store) Lookup(ctx context.Context, tx *sql.Tx, c Collection, root Root, key string) ([]byte, bool, error) {
	if e := s.check(tx, c, root, key); e != nil {
		return nil, false, e
	}
	if e := ctx.Err(); e != nil {
		return nil, false, e
	}
	if root == s.Empty(c) {
		return nil, false, nil
	}
	path, id, prefix := s.keyPath(c, key), string(root), ""
	for {
		if e := ctx.Err(); e != nil {
			return nil, false, e
		}
		p, e := s.read(ctx, tx, c, id)
		if e != nil {
			return nil, false, e
		}
		if !location(p, prefix) {
			return nil, false, ErrIntegrity
		}
		if p.Kind == "leaf" {
			if !leaf(p, p.Path, p.Key) || s.keyPath(c, p.Key) != p.Path {
				return nil, false, ErrIntegrity
			}
			if p.Path != path {
				return nil, false, nil
			}
			if p.Key != key {
				return nil, false, ErrIntegrity
			}
			v, e := s.value(ctx, tx, c, p)
			return v, e == nil, e
		}
		if !strings.HasPrefix(path, p.Path) {
			return nil, false, nil
		}
		index := nibble(path[len(p.Path)])
		id = p.Children[index]
		if id == "" {
			return nil, false, nil
		}
		prefix = p.Path + string(path[len(p.Path)])
	}
}

// Put returns a new root; old roots and pages remain immutable and readable.
// The caller publishes it with its independent authority CAS in this same tx.
func (s *Store) Put(ctx context.Context, tx *sql.Tx, c Collection, root Root, key string, value []byte) (Root, error) {
	if e := s.check(tx, c, root, key); e != nil {
		return "", e
	}
	if e := ctx.Err(); e != nil {
		return "", e
	}
	// Validate the existing value before inserting any page. A same-value Put
	// must not recreate a deleted old chunk and thereby repair missing evidence.
	if _, _, e := s.Lookup(ctx, tx, c, root, key); e != nil {
		return "", e
	}
	path := s.keyPath(c, key)
	head := ""
	for offset := (len(value) - 1) / chunkBytes * chunkBytes; len(value) > 0 && offset >= 0; offset -= chunkBytes {
		end := offset + chunkBytes
		if end > len(value) {
			end = len(value)
		}
		p := page{Version: 1, Kind: "chunk", Path: path, Index: int64(offset / chunkBytes), Next: head, Data: value[offset:end]}
		id, e := s.putPage(ctx, tx, c, p)
		if e != nil {
			return "", e
		}
		head = id
	}
	sum := sha256.Sum256(value)
	l := page{Version: 1, Kind: "leaf", Path: path, Key: key, ValueSHA: hex.EncodeToString(sum[:]), Size: int64(len(value)), Head: head}
	leafID, e := s.putPage(ctx, tx, c, l)
	if e != nil {
		return "", e
	}
	id := string(root)
	if root == s.Empty(c) {
		id = ""
	}
	next, e := s.change(ctx, tx, c, id, path, key, leafID, "")
	return Root(next), e
}
func commonPath(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return a[:i]
}
func (s *Store) join(ctx context.Context, tx *sql.Tx, c Collection, aID, aPath, bID, bPath string) (string, error) {
	prefix := commonPath(aPath, bPath)
	if len(prefix) >= 64 || prefix == aPath || prefix == bPath {
		return "", ErrIntegrity
	}
	p := page{Version: 1, Kind: "branch", Path: prefix, Children: make([]string, 16)}
	p.Children[nibble(aPath[len(prefix)])] = aID
	p.Children[nibble(bPath[len(prefix)])] = bID
	return s.putPage(ctx, tx, c, p)
}
func (s *Store) change(ctx context.Context, tx *sql.Tx, c Collection, id, path, key, replacement, prefix string) (string, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	if id == "" {
		return replacement, nil
	}
	p, e := s.read(ctx, tx, c, id)
	if e != nil {
		return "", e
	}
	if !location(p, prefix) {
		return "", ErrIntegrity
	}
	if p.Kind == "leaf" {
		if !leaf(p, p.Path, p.Key) || s.keyPath(c, p.Key) != p.Path {
			return "", ErrIntegrity
		}
		if p.Path == path {
			if p.Key != key {
				return "", ErrIntegrity
			}
			return replacement, nil
		}
		if replacement == "" {
			return id, nil
		}
		return s.join(ctx, tx, c, id, p.Path, replacement, path)
	}
	if !strings.HasPrefix(path, p.Path) {
		if replacement == "" {
			return id, nil
		}
		return s.join(ctx, tx, c, id, p.Path, replacement, path)
	}
	slot := nibble(path[len(p.Path)])
	next, e := s.change(ctx, tx, c, p.Children[slot], path, key, replacement, p.Path+string(path[len(p.Path)]))
	if e != nil {
		return "", e
	}
	if next == p.Children[slot] {
		return id, nil
	}
	p.Children[slot] = next
	count, remaining := 0, ""
	for _, child := range p.Children {
		if child != "" {
			count++
			remaining = child
		}
	}
	if count == 0 {
		return "", nil
	}
	if count == 1 {
		return remaining, nil
	}
	return s.putPage(ctx, tx, c, p)
}
func (s *Store) Delete(ctx context.Context, tx *sql.Tx, c Collection, root Root, key string) (Root, error) {
	if e := s.check(tx, c, root, key); e != nil {
		return "", e
	}
	if e := ctx.Err(); e != nil {
		return "", e
	}
	if _, exists, e := s.Lookup(ctx, tx, c, root, key); e != nil {
		return "", e
	} else if !exists {
		return root, nil
	}
	next, e := s.change(ctx, tx, c, string(root), s.keyPath(c, key), key, "", "")
	if e != nil {
		return "", e
	}
	if next == "" {
		return s.Empty(c), nil
	}
	return Root(next), nil
}

// Iterate walks authenticated root reachability, never an unauthenticated SQL
// key listing. It retains at most a 64-level branch stack and one logical value.
// Stop/cancel propagates to the caller. Node IDs are keyed hashes, so ordering
// is deterministic trie ordering, not lexical ordering of source logical keys.
func (s *Store) Iterate(ctx context.Context, tx *sql.Tx, c Collection, root Root, fn func(string, []byte) error) error {
	if e := s.check(tx, c, root, ""); e != nil {
		return e
	}
	if fn == nil {
		return ErrInput
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	if root == s.Empty(c) {
		return nil
	}
	var walk func(string, string) error
	walk = func(id, prefix string) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		p, e := s.read(ctx, tx, c, id)
		if e != nil {
			return e
		}
		if !location(p, prefix) {
			return ErrIntegrity
		}
		if p.Kind == "leaf" {
			if !leaf(p, p.Path, p.Key) || s.keyPath(c, p.Key) != p.Path {
				return ErrIntegrity
			}
			v, e := s.value(ctx, tx, c, p)
			if e != nil {
				return e
			}
			return fn(p.Key, v)
		}
		for i, child := range p.Children {
			if child != "" {
				if e = walk(child, p.Path+fmt.Sprintf("%x", i)); e != nil {
					return e
				}
			}
		}
		return nil
	}
	return walk(string(root), "")
}

// Audit checks the complete reachable collection, including all value chunks.
// It detects a removed/replaced historical page when that page is reached. A
// point lookup cannot assert physical existence of unrelated unread pages.
func (s *Store) Audit(ctx context.Context, tx *sql.Tx, c Collection, root Root) error {
	return s.Iterate(ctx, tx, c, root, func(string, []byte) error { return nil })
}
