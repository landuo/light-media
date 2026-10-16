package seedhost

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
)

// SQLJournal exists only in test fixtures. Production must supply the App
// journal with this same-transaction Core contract; this creates no App store.
type SQLJournal struct {
	DB    *sql.DB
	scope string
	aead  cipher.AEAD
}

func NewSQLJournal(db *sql.DB, key []byte, scope string) (*SQLJournal, error) {
	if db == nil || scope == "" {
		return nil, ErrUnsafe
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS seed_host_state(scope TEXT PRIMARY KEY,sealed BLOB NOT NULL)`); err != nil {
		return nil, err
	}
	return &SQLJournal{db, scope, aead}, nil
}
func (j *SQLJournal) read(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (State, error) {
	var s State
	var sealed []byte
	err := q.QueryRowContext(ctx, `SELECT sealed FROM seed_host_state WHERE scope=?`, j.scope).Scan(&sealed)
	if err == sql.ErrNoRows {
		initialize(&s)
		return s, nil
	}
	if err != nil {
		return s, err
	}
	n := j.aead.NonceSize()
	if len(sealed) < n || len(sealed) > (32<<20)+n+j.aead.Overhead() {
		return s, ErrUnsafe
	}
	raw, err := j.aead.Open(nil, sealed[:n], sealed[n:], []byte("seed-host:v1:"+j.scope))
	if err != nil || len(raw) > 32<<20 {
		return s, ErrUnsafe
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&s) != nil || d.Decode(new(any)) != io.EOF {
		return s, ErrUnsafe
	}
	if bytes.Equal(bytes.TrimSpace(s.Config), []byte("null")) {
		s.Config = nil
	}
	initialize(&s)
	return s, nil
}
func (j *SQLJournal) Read(ctx context.Context) (State, error) { return j.read(ctx, j.DB) }
func (j *SQLJournal) Update(ctx context.Context, fn func(*sql.Tx, *State) error) error {
	tx, err := j.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	s, err := j.read(ctx, tx)
	if err != nil {
		return err
	}
	if err = fn(tx, &s); err != nil {
		return err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if len(raw) > 32<<20 {
		return ErrUnsafe
	}
	nonce := make([]byte, j.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	sealed := j.aead.Seal(nonce, nonce, raw, []byte("seed-host:v1:"+j.scope))
	if _, err = tx.ExecContext(ctx, `INSERT INTO seed_host_state VALUES(?,?) ON CONFLICT(scope) DO UPDATE SET sealed=excluded.sealed`, j.scope, sealed); err != nil {
		return err
	}
	return tx.Commit()
}
