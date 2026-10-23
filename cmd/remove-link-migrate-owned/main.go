// Offline, reviewed RemoveLink import. This command never starts the application.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
	"light-media/internal/app"
)

const maxInput = 1 << 20

// Reject symlinks in every component, not just the leaf. No path is created.
func existingDirectory(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return false
	}
	for p := path; p != "/"; p = filepath.Dir(p) {
		st, err := os.Lstat(p)
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}

func openRegular(path string, limit int64) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > limit {
		f.Close()
		return nil, errors.New("invalid_owned_file")
	}
	return f, nil
}

func cleanCheckpoint(dbPath string) bool {
	for _, suffix := range []string{"-wal", "-journal", "-shm"} {
		st, err := os.Lstat(dbPath + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !st.Mode().IsRegular() || st.Size() != 0 {
			return false
		}
	}
	return true
}

func openDatabase(path, mode string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", mode)
	if mode == "ro" {
		q.Set("immutable", "1")
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err == nil {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	}
	return db, err
}

func run(ctx context.Context, args []string, in io.Reader, out io.Writer) int {
	emit := func(v map[string]any) int {
		if json.NewEncoder(out).Encode(v) != nil {
			return 1
		}
		return 0
	}
	fail := func(code string) int {
		emit(map[string]any{"state": "failed", "code": code, "plugin": "RemoveLink"})
		return 1
	}
	flags := flag.NewFlagSet("remove-link-migrate-owned", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var dir string
	var apply, validate bool
	flags.StringVar(&dir, "target-data-dir", "", "existing stopped target data directory")
	flags.BoolVar(&apply, "apply", false, "apply reviewed disabled import")
	flags.BoolVar(&validate, "validate", false, "validate stdin without opening a target")
	if flags.Parse(args) != nil || flags.NArg() != 0 || validate && (apply || dir != "") {
		return fail("invalid_owned_tool_arguments")
	}
	raw, err := io.ReadAll(io.LimitReader(in, maxInput+1))
	if err != nil || len(raw) > maxInput || app.ValidateReviewedRemoveLinkInput(raw) != nil {
		return fail("invalid_reviewed_source_snapshot")
	}
	if validate {
		return emit(map[string]any{"state": "validated", "plugin": "RemoveLink", "writes": 0, "network_calls": 0})
	}
	if !existingDirectory(dir) {
		return fail("existing_reviewed_target_required")
	}
	lock, err := openRegular(filepath.Join(dir, "instance.lock"), 4096)
	if err != nil {
		return fail("target_instance_lock_missing")
	}
	defer lock.Close()
	if unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return fail("target_application_is_running")
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	dbPath := filepath.Join(dir, "light-media.db")
	dbFile, err := openRegular(dbPath, 1<<62)
	if err != nil {
		return fail("existing_target_database_required")
	}
	defer dbFile.Close()
	if !cleanCheckpoint(dbPath) {
		return fail("clean_target_checkpoint_required")
	}
	keyFile, err := openRegular(filepath.Join(dir, "secret.key"), 32)
	if err != nil {
		return fail("existing_target_encryption_key_required")
	}
	key, err := io.ReadAll(io.LimitReader(keyFile, 33))
	keyFile.Close()
	if err != nil || len(key) != 32 {
		return fail("existing_target_encryption_key_required")
	}
	ro, err := openDatabase(dbPath, "ro")
	if err != nil {
		return fail("target_read_failed")
	}
	defer ro.Close()
	var schema, baseSchema int
	if ro.QueryRowContext(ctx, `SELECT MAX(version) FROM migrations`).Scan(&baseSchema) != nil || baseSchema != 1 || ro.QueryRowContext(ctx, `SELECT MAX(version) FROM plugin_storage_schema`).Scan(&schema) != nil || schema != 1 {
		return fail("deploy_final_plugin_platform_before_migration")
	}
	var active int
	if ro.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM jobs WHERE state NOT IN ('succeeded','failed','cancelled'))+(SELECT COUNT(*) FROM plugin_runs WHERE state NOT IN ('succeeded','failed','canceled'))`).Scan(&active) != nil || active != 0 {
		return fail("target_has_unfinished_active_work")
	}
	var source struct {
		InstanceID string `json:"instance_id"`
	}
	if json.Unmarshal(raw, &source) != nil {
		return fail("invalid_reviewed_source_snapshot")
	}
	fingerprint, err := app.ReviewedRemoveLinkFingerprint(raw)
	if err != nil {
		return fail("invalid_reviewed_source_snapshot")
	}
	var existing, receipts int
	if ro.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installations WHERE instance_id=?`, source.InstanceID).Scan(&existing) != nil || ro.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_migration_receipts WHERE source_fingerprint=? AND source_plugin_id='RemoveLink' AND instance_id=?`, fingerprint, source.InstanceID).Scan(&receipts) != nil {
		return fail("target_read_failed")
	}
	if existing > 0 && receipts != 1 || existing == 0 && receipts != 0 {
		return fail("target_already_has_different_plugin")
	}
	var priorEnabled bool
	var priorRuntime, priorValues int
	if existing > 0 && ro.QueryRowContext(ctx, `SELECT desired_enabled FROM plugin_installations WHERE instance_id=?`, source.InstanceID).Scan(&priorEnabled) != nil {
		return fail("target_read_failed")
	}
	if ro.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_schedules WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_event_subscriptions WHERE instance_id=?)`, source.InstanceID, source.InstanceID, source.InstanceID).Scan(&priorRuntime) != nil || ro.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_values WHERE instance_id=? AND kind='data'`, source.InstanceID).Scan(&priorValues) != nil {
		return fail("target_read_failed")
	}
	if app.ValidateReviewedRemoveLinkTarget(ctx, ro, key, raw) != nil {
		return fail("reviewed_remove_link_target_changed_or_incomplete")
	}
	if !apply {
		return emit(map[string]any{"mode": "read_only_dry_run", "state": "ready", "plugin": "RemoveLink", "same_receipt": receipts == 1, "writes": 0, "network_calls": 0, "desired_enabled": priorEnabled})
	}
	ro.Close()
	// Keep the same flock throughout: no OpenStore, lock handoff, initialization,
	// job recovery, chmod, key generation, or application startup is permitted.
	db, err := openDatabase(dbPath, "rw")
	if err != nil {
		return fail("target_store_open_failed")
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA synchronous=FULL;`); err != nil {
		return fail("target_store_open_failed")
	}
	receipt, err := app.ImportReviewedRemoveLinkOwned(ctx, db, key, raw)
	if err != nil {
		return fail("atomic_import_failed")
	}
	var enabled bool
	var runtime, values int
	if db.QueryRowContext(ctx, `SELECT desired_enabled FROM plugin_installations WHERE instance_id=?`, source.InstanceID).Scan(&enabled) != nil || enabled != priorEnabled {
		return fail("reviewed_package_activation_mismatch")
	}
	if db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_schedules WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_event_subscriptions WHERE instance_id=?)`, source.InstanceID, source.InstanceID, source.InstanceID).Scan(&runtime) != nil || runtime != priorRuntime {
		return fail("unexpected_import_runtime_effect")
	}
	if db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_values WHERE instance_id=? AND kind='data'`, source.InstanceID).Scan(&values) != nil || values != priorValues {
		return fail("unexpected_import_data")
	}
	if strings.TrimSpace(receipt.State) == "" {
		return fail("missing_import_receipt")
	}
	return emit(map[string]any{"mode": "apply", "state": "imported", "plugin": "RemoveLink", "receipt_state": receipt.State, "replayed": existing > 0, "desired_enabled": enabled, "scheduled": false, "executed": false, "network_calls": 0, "source_data_rows": 0, "target_data_rows": values})
}

func main() { os.Exit(run(context.Background(), os.Args[1:], os.Stdin, os.Stdout)) }
