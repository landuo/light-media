package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

type Store struct {
	RecoveredJobs int64
	DB            *sql.DB
	key           []byte
	lock          *os.File
	mu            sync.Mutex
}

func OpenStore(dir string) (*Store, error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || dir == "/" {
		return nil, errors.New("use a specific absolute data directory")
	}
	if st, err := os.Lstat(dir); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("data directory cannot be a symlink")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "instance.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("data directory already used by another process")
	}
	fail := func(e error) (*Store, error) { lock.Close(); return nil, e }
	keyPath := filepath.Join(dir, "secret.key")
	if st, e := os.Lstat(keyPath); e == nil && !st.Mode().IsRegular() {
		return fail(errors.New("invalid application encryption key file"))
	}
	key, err := os.ReadFile(keyPath)
	if os.IsNotExist(err) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return fail(err)
		}
		f, e := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return fail(e)
		}
		_, err = f.Write(key)
		if err == nil {
			err = f.Sync()
		}
		f.Close()
		if err != nil {
			return fail(err)
		}
	} else if err != nil {
		return fail(err)
	}
	if len(key) != 32 {
		return fail(errors.New("invalid application encryption key"))
	}
	if err = os.Chmod(keyPath, 0600); err != nil {
		return fail(err)
	}
	dbPath := filepath.Join(dir, "light-media.db")
	if st, e := os.Lstat(dbPath); e == nil && !st.Mode().IsRegular() {
		return fail(errors.New("database must be a regular file"))
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fail(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	var versionTable int
	if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='migrations'`).Scan(&versionTable); err != nil {
		db.Close()
		return fail(err)
	}
	if versionTable != 0 {
		var version int
		if err = db.QueryRow(`SELECT MAX(version) FROM migrations`).Scan(&version); err != nil || version != 1 {
			db.Close()
			return fail(errors.New("database schema is not supported by this application version"))
		}
	}
	schema := `PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA synchronous=FULL;
 CREATE TABLE IF NOT EXISTS migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS records(kind TEXT NOT NULL,id TEXT NOT NULL,payload BLOB NOT NULL,updated_at TEXT NOT NULL,PRIMARY KEY(kind,id));
 CREATE TABLE IF NOT EXISTS priority_rule_revisions(section TEXT PRIMARY KEY,revision INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS admin(id INTEGER PRIMARY KEY CHECK(id=1),username TEXT NOT NULL,password_hash BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS sessions(token_hash TEXT PRIMARY KEY,expires_at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS jobs(id TEXT PRIMARY KEY,workflow_id TEXT NOT NULL,kind TEXT NOT NULL,state TEXT NOT NULL,payload BLOB NOT NULL,attempt INTEGER NOT NULL DEFAULT 0,generation INTEGER NOT NULL DEFAULT 0,run_at INTEGER NOT NULL,lease_until INTEGER NOT NULL DEFAULT 0,error_code TEXT NOT NULL DEFAULT '',error_message TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS jobs_due ON jobs(state,run_at);
 CREATE INDEX IF NOT EXISTS jobs_subject ON jobs(kind,json_extract(payload,'$.id'),created_at);
 CREATE TABLE IF NOT EXISTS events(seq INTEGER PRIMARY KEY AUTOINCREMENT,time TEXT NOT NULL,workflow_id TEXT NOT NULL,job_id TEXT NOT NULL,level TEXT NOT NULL,module TEXT NOT NULL,event TEXT NOT NULL,message TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS events_job ON events(job_id,seq);
 CREATE TABLE IF NOT EXISTS reservations(media_key TEXT NOT NULL,season INTEGER NOT NULL,episode INTEGER NOT NULL,intent_id TEXT NOT NULL,PRIMARY KEY(media_key,season,episode));
 CREATE INDEX IF NOT EXISTS reservations_intent ON reservations(intent_id);
 CREATE TABLE IF NOT EXISTS asset_slots(media_key TEXT NOT NULL,season INTEGER NOT NULL,episode INTEGER NOT NULL,asset_id TEXT NOT NULL,PRIMARY KEY(media_key,season,episode,asset_id));
 CREATE TABLE IF NOT EXISTS site_usage(site_id TEXT NOT NULL,ts INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS site_usage_window ON site_usage(site_id,ts);
 CREATE TABLE IF NOT EXISTS quota(site_id TEXT NOT NULL,day TEXT NOT NULL,used INTEGER NOT NULL,PRIMARY KEY(site_id,day));
 INSERT OR IGNORE INTO migrations VALUES(1,strftime('%Y-%m-%dT%H:%M:%fZ','now'));
 `
	if _, err = db.Exec(schema + nativeSiteIdentitySchema + sourceAccountSchema + originalSubscriptionAliasSchema + originalTransferAliasSchema); err != nil {
		db.Close()
		return fail(err)
	}
	recovery, err := db.Exec(`UPDATE jobs SET state='queued',lease_until=0,run_at=unixepoch(),generation=generation+1 WHERE state='running'`)
	if err != nil {
		db.Close()
		return fail(err)
	}
	recovered, _ := recovery.RowsAffected()
	_ = os.Chmod(filepath.Join(dir, "light-media.db"), 0600)
	return &Store{DB: db, key: key, lock: lock, RecoveredJobs: recovered}, nil
}
func (s *Store) Close() error { err := s.DB.Close(); s.lock.Close(); return err }
func (s *Store) Seal(value any) ([]byte, error) {
	b, e := json.Marshal(value)
	if e != nil {
		return nil, e
	}
	block, e := aes.NewCipher(s.key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, g.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	return g.Seal(nonce, nonce, b, []byte("light-media-config-v1")), nil
}
func (s *Store) Unseal(b []byte, dst any) error {
	block, e := aes.NewCipher(s.key)
	if e != nil {
		return e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return e
	}
	if len(b) < g.NonceSize() {
		return errors.New("configuration cannot be decrypted")
	}
	v, e := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], []byte("light-media-config-v1"))
	if e != nil {
		return errors.New("configuration cannot be decrypted")
	}
	return json.Unmarshal(v, dst)
}
func id(prefix string) string {
	b := make([]byte, 18)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}
func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func (s *Store) Put(ctx context.Context, kind, key string, value any) error {
	b, e := json.Marshal(value)
	if e != nil {
		return e
	}
	if siteAccountSnapshotKind(kind) {
		b, e = s.sealSiteAccountSnapshot(kind, key, b)
		if e != nil {
			return e
		}
	}
	_, e = s.DB.ExecContext(ctx, `INSERT INTO records VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, kind, key, b, now())
	return e
}
func (s *Store) Get(ctx context.Context, kind, key string, dst any) error {
	var b []byte
	e := s.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, kind, key).Scan(&b)
	if e != nil {
		return e
	}
	if siteAccountSnapshotKind(kind) {
		return s.openSiteAccountSnapshot(kind, key, b, dst)
	}
	return json.Unmarshal(b, dst)
}
func (s *Store) List(ctx context.Context, kind string, limit, offset int) ([]json.RawMessage, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT payload FROM records WHERE kind=? ORDER BY updated_at DESC,id LIMIT ? OFFSET ?`, kind, limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		out = append(out, json.RawMessage(b))
	}
	return out, rows.Err()
}
func (s *Store) Delete(ctx context.Context, kind, key string) error {
	_, e := s.DB.ExecContext(ctx, `DELETE FROM records WHERE kind=? AND id=?`, kind, key)
	return e
}
func txPut(ctx context.Context, tx *sql.Tx, kind, key string, value any) error {
	b, e := json.Marshal(value)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO records VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, kind, key, b, now())
	return e
}
func (s *Store) Backup(ctx context.Context, dir string) (string, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	name := filepath.Join(dir, "light-media-"+time.Now().UTC().Format("20060102T150405")+"-"+id("")+".db")
	if strings.Contains(name, "'") {
		return "", errors.New("invalid backup directory")
	}
	if _, e := s.DB.ExecContext(ctx, `VACUUM INTO '`+strings.ReplaceAll(name, "'", "''")+`'`); e != nil {
		return "", e
	}
	_ = os.Chmod(name, 0600)
	return filepath.Base(name), nil
}
func (s *Store) Reserve(ctx context.Context, tx *sql.Tx, key string, season int, episodes []int, intent string) error {
	if len(episodes) == 0 {
		episodes = []int{0}
	}
	for _, ep := range episodes {
		var n int
		if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_slots WHERE media_key=? AND season=? AND episode=?`, key, season, ep).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return errors.New("target already exists in library")
		}
		if _, e := tx.ExecContext(ctx, `INSERT INTO reservations VALUES(?,?,?,?)`, key, season, ep, intent); e != nil {
			return fmt.Errorf("target already reserved by another download")
		}
	}
	return nil
}
