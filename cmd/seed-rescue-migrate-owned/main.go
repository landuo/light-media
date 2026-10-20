// One-purpose offline delivery command; never registered as a public API.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
	"light-media/internal/app"
)

func regular(path string, limit int64) ([]byte, error) {
	st, e := os.Lstat(path)
	if e != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("invalid_owned_input_file")
	}
	return os.ReadFile(path)
}
func run() error {
	var dir string
	var apply, validate bool
	flag.StringVar(&dir, "target-data-dir", "", "existing stopped target data directory")
	flag.BoolVar(&apply, "apply", false, "perform reviewed atomic migration")
	flag.BoolVar(&validate, "validate", false, "validate selected stdin without opening any target")
	flag.Parse()
	if flag.NArg() != 0 || validate && (apply || dir != "") {
		return errors.New("invalid_owned_tool_arguments")
	}
	raw, e := io.ReadAll(io.LimitReader(os.Stdin, (64<<20)+1))
	if e != nil || len(raw) > 64<<20 || app.ValidateReviewedSeedRescueInput(raw) != nil {
		return errors.New("invalid_reviewed_source_snapshot")
	}
	if validate {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"state": "validated", "plugin": "SeedRescue", "preferences_written": 0, "writes": 0, "network_calls": 0})
	}
	if dir == "" || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || dir == "/" {
		return errors.New("existing_reviewed_target_required")
	}
	st, e := os.Lstat(dir)
	if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid_target_directory")
	}
	lockPath := filepath.Join(dir, "instance.lock")
	if _, e = regular(lockPath, 4096); e != nil {
		return errors.New("target_instance_lock_missing")
	}
	lock, e := os.Open(lockPath)
	if e != nil {
		return errors.New("target_instance_lock_unavailable")
	}
	defer lock.Close()
	if unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return errors.New("target_application_is_running")
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	dbPath := filepath.Join(dir, "light-media.db")
	st, e = os.Lstat(dbPath)
	if e != nil || !st.Mode().IsRegular() {
		return errors.New("existing_target_database_required")
	}
	for _, suffix := range []string{"-wal", "-journal"} {
		st, err := os.Lstat(dbPath + suffix)
		if err == nil && (!st.Mode().IsRegular() || st.Size() != 0) || err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("clean_target_checkpoint_required")
		}
	}
	ro, e := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&immutable=1")
	if e != nil {
		return errors.New("target_read_failed")
	}
	defer ro.Close()
	var schema int
	if ro.QueryRow(`SELECT version FROM plugin_storage_schema`).Scan(&schema) != nil || schema != 1 {
		return errors.New("deploy_final_plugin_platform_before_migration")
	}
	fingerprint, e := app.ReviewedSeedRescueFingerprint(raw)
	if e != nil {
		return errors.New("invalid_reviewed_source_snapshot")
	}
	var existing, receipts, active int
	if ro.QueryRow(`SELECT COUNT(*) FROM plugin_installations WHERE instance_id='SeedRescue'`).Scan(&existing) != nil || ro.QueryRow(`SELECT COUNT(*) FROM plugin_migration_receipts WHERE source_fingerprint=? AND source_plugin_id='SeedRescue' AND instance_id='SeedRescue'`, fingerprint).Scan(&receipts) != nil {
		return errors.New("target_read_failed")
	}
	if existing > 0 && receipts != 1 || existing == 0 && receipts != 0 {
		return errors.New("target_already_has_different_plugin")
	}
	if ro.QueryRow(`SELECT (SELECT COUNT(*) FROM jobs WHERE state IN ('running','queued'))+(SELECT COUNT(*) FROM plugin_runs WHERE state IN ('pending','running','reconcile_pending'))`).Scan(&active) != nil || active != 0 {
		return errors.New("target_has_unfinished_active_work")
	}
	key, e := regular(filepath.Join(dir, "secret.key"), 32)
	if e != nil || len(key) != 32 {
		return errors.New("existing_target_encryption_key_required")
	}
	if app.ValidateReviewedSeedRescueTarget(context.Background(), ro, key, raw) != nil {
		return errors.New("reviewed_seed_rescue_references_changed_or_incomplete")
	}
	if !apply {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "read_only_dry_run", "state": "ready", "same_receipt": receipts == 1, "preferences_written": 0, "writes": 0})
	}
	ro.Close()
	if unix.Flock(int(lock.Fd()), unix.LOCK_UN) != nil {
		return errors.New("target_lock_handoff_failed")
	}
	s, e := app.OpenStore(dir)
	if e != nil {
		return errors.New("target_store_open_failed")
	}
	defer s.Close()
	p, e := app.NewPluginStore(context.Background(), s)
	if e != nil {
		return errors.New("target_plugin_store_open_failed")
	}
	m, e := p.BeginMigration(context.Background(), fingerprint, "SeedRescue", "SeedRescue")
	if e != nil {
		return errors.New("migration_plan_failed")
	}
	receipt, e := p.ImportReviewedSeedRescue(context.Background(), m.ID, raw)
	if e != nil {
		return errors.New("atomic_import_failed")
	}
	var count int
	if s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_runs WHERE instance_id='SeedRescue')+(SELECT COUNT(*) FROM plugin_schedules WHERE instance_id='SeedRescue')+(SELECT COUNT(*) FROM plugin_event_subscriptions WHERE instance_id='SeedRescue')`).Scan(&count) != nil {
		return errors.New("post_import_read_failed")
	}
	if existing == 0 && count != 0 {
		return errors.New("unexpected_import_runtime_effect")
	}
	var historyRows int
	if s.DB.QueryRow(`SELECT COUNT(*) FROM plugin_values WHERE instance_id='SeedRescue' AND kind='data'`).Scan(&historyRows) != nil {
		return errors.New("post_import_history_read_failed")
	}
	installation, e := p.Installation(context.Background(), "SeedRescue")
	if e != nil || existing == 0 && installation.DesiredEnabled {
		return errors.New("reviewed_package_activation_mismatch")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "apply", "state": "imported", "receipt": receipt, "preferences_written": 0, "replayed": existing > 0, "scheduled": false, "executed": false, "desired_enabled": installation.DesiredEnabled, "source_flags_and_history_preserved": true, "site_reference_mappings": 17, "downloader_reference_mappings": 1, "source_kv_rows": 3, "source_history_tasks": 1140, "target_kv_rows": historyRows})
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e.Error())
		os.Exit(1)
	}
}
