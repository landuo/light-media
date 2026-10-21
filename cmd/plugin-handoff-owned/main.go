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
	var dir, instance string
	var apply, validate, observe bool
	flag.StringVar(&instance, "instance-id", "", "reviewed migrated plugin instance")
	flag.StringVar(&dir, "target-data-dir", "", "existing stopped target data directory")
	flag.BoolVar(&apply, "apply", false, "perform reviewed atomic migration")
	flag.BoolVar(&validate, "validate", false, "validate selected stdin without opening any target")
	flag.BoolVar(&observe, "observe-source", false, "read the original Docker container state on its host")
	flag.Parse()
	if observe {
		if flag.NArg() != 0 || apply || validate || dir != "" || instance != "" {
			return errors.New("invalid_owned_tool_arguments")
		}
		return observeOriginalSource()
	}
	if flag.NArg() != 0 || validate && (apply || dir != "") {
		return errors.New("invalid_owned_tool_arguments")
	}
	if instance != "IYUUAutoSeed" && instance != "SeedRescue" && instance != "TorrentTransfer" && instance != "TorrentTransfermusic" && instance != "SiteStatistic" {
		return errors.New("reviewed_plugin_instance_required")
	}
	raw, e := io.ReadAll(io.LimitReader(os.Stdin, 4097))
	if e != nil || len(raw) > 4096 || app.ValidateReviewedPluginStoppedInput(raw) != nil {
		return errors.New("invalid_reviewed_source_snapshot")
	}
	if validate {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"state": "validated", "plugin": instance, "preferences_written": 0, "writes": 0, "network_calls": 0})
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
	var active int
	if ro.QueryRow(`SELECT (SELECT COUNT(*) FROM jobs WHERE state IN ('running','queued'))+(SELECT COUNT(*) FROM plugin_runs WHERE state IN ('pending','running','reconcile_pending'))`).Scan(&active) != nil || active != 0 {
		return errors.New("target_has_unfinished_active_work")
	}
	key, e := regular(filepath.Join(dir, "secret.key"), 32)
	if e != nil || len(key) != 32 {
		return errors.New("existing_target_encryption_key_required")
	}
	if app.ValidateReviewedPluginExecutionHandoffTarget(context.Background(), ro, key, instance, raw) != nil {
		return errors.New("reviewed_torrent_transfer_references_changed_or_incomplete")
	}
	if !apply {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "read_only_dry_run", "state": "ready", "preferences_written": 0, "writes": 0})
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
	if e = p.ImportReviewedPluginExecutionHandoff(context.Background(), instance, raw); e != nil {
		return errors.New("handoff_delivery_failed")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"state": "accepted", "plugin": instance, "executed": false, "initialized": false, "source_mutations": 0, "target_handoff_record_written": true})
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e.Error())
		os.Exit(1)
	}
}
