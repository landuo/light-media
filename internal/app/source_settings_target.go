package app

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Both selected-settings importers use this same stopped-target boundary.
// Closing it rolls back an uncommitted transaction before releasing the lock.
type sourceSettingsTarget struct {
	p     *PluginStore
	tx    *sql.Tx
	close func()
}

func openSourceSettingsTarget(ctx context.Context, dir string, apply bool) (*sourceSettingsTarget, error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || dir == "/" {
		return nil, errors.New("explicit_existing_source_settings_target_required")
	}
	regular := func(path string, directory bool) bool {
		st, e := os.Lstat(path)
		return e == nil && st.Mode()&os.ModeSymlink == 0 && ((directory && st.IsDir()) || (!directory && st.Mode().IsRegular()))
	}
	if !regular(dir, true) || !regular(filepath.Join(dir, "instance.lock"), false) || !regular(filepath.Join(dir, "secret.key"), false) || !regular(filepath.Join(dir, "light-media.db"), false) {
		return nil, errors.New("existing_source_settings_target_required")
	}
	lock, e := os.Open(filepath.Join(dir, "instance.lock"))
	if e != nil {
		return nil, errors.New("source_settings_target_lock_unavailable")
	}

	var db *sql.DB
	var tx *sql.Tx
	success := false
	cleanup := func() {
		if tx != nil {
			_ = tx.Rollback()
		}
		if db != nil {
			_ = db.Close()
		}
		_ = unix.Flock(int(lock.Fd()), unix.LOCK_UN)
		_ = lock.Close()
	}
	defer func() {
		if !success {
			cleanup()
		}
	}()
	if unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return nil, errors.New("source_settings_target_is_running")
	}

	key, e := os.ReadFile(filepath.Join(dir, "secret.key"))
	if e != nil || len(key) != 32 {
		return nil, errors.New("existing_source_settings_target_key_required")
	}
	mode := "ro"
	if apply {
		mode = "rw"
	}
	dsn := (&url.URL{Scheme: "file", Path: filepath.Join(dir, "light-media.db")}).String() + "?mode=" + mode
	if !apply {
		// SQLite mode=ro can still create WAL/SHM siblings. An immutable read
		// opens none, but it must never ignore uncheckpointed committed WAL.
		// The instance lock is held, and a nonempty WAL is an explicit failure.
		for _, suffix := range []string{"-wal", "-journal"} {
			st, err := os.Lstat(filepath.Join(dir, "light-media.db") + suffix)
			if err == nil && (!st.Mode().IsRegular() || st.Size() != 0) || err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, errors.New("source_settings_target_requires_clean_checkpoint")
			}
		}
		dsn += "&immutable=1"
	}
	db, e = sql.Open("sqlite", dsn)
	if e != nil {
		return nil, errors.New("source_settings_target_read_failed")
	}

	db.SetMaxOpenConns(1)
	if _, e = db.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000`); e != nil {
		return nil, errors.New("source_settings_target_read_failed")
	}
	var schema, pluginSchema int
	if db.QueryRowContext(ctx, `SELECT MAX(version) FROM migrations`).Scan(&schema) != nil || schema != 1 || db.QueryRowContext(ctx, `SELECT MAX(version) FROM plugin_storage_schema`).Scan(&pluginSchema) != nil || pluginSchema != 1 {
		return nil, errors.New("source_settings_target_schema_unsupported")
	}
	p := &PluginStore{store: &Store{DB: db, key: key}}
	tx, e = db.BeginTx(ctx, &sql.TxOptions{ReadOnly: !apply})
	if e != nil {
		return nil, errors.New("source_settings_target_transaction_failed")
	}

	var active int
	if tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM jobs WHERE state IN ('running','queued'))+(SELECT COUNT(*) FROM plugin_runs WHERE state IN ('pending','running','reconcile_pending'))`).Scan(&active) != nil || active != 0 {
		return nil, errors.New("source_settings_target_has_active_work")
	}
	success = true
	return &sourceSettingsTarget{p: p, tx: tx, close: cleanup}, nil
}
