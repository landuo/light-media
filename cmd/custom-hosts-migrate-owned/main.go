// One-purpose offline delivery command; never registered as a public API.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	var dir, hostsPath string
	var apply, validate bool
	flag.StringVar(&hostsPath, "owned-hosts-file", "", "existing writable file mounted only into this application as /etc/hosts")
	flag.StringVar(&dir, "target-data-dir", "", "existing stopped target data directory")
	flag.BoolVar(&apply, "apply", false, "perform reviewed atomic migration")
	flag.BoolVar(&validate, "validate", false, "validate selected stdin without opening any target")
	flag.Parse()
	if flag.NArg() != 0 || validate && (apply || dir != "" || hostsPath != "") {
		return errors.New("invalid_owned_tool_arguments")
	}
	raw, e := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 || app.ValidateReviewedCustomHostsInput(raw) != nil {
		return errors.New("invalid_reviewed_source_snapshot")
	}
	if validate {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"state": "validated", "plugin": "CustomHosts", "preferences_written": 0, "writes": 0, "network_calls": 0})
	}
	if dir == "" || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || dir == "/" {
		return errors.New("existing_reviewed_target_required")
	}
	st, e := os.Lstat(dir)
	if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid_target_directory")
	}
	if !filepath.IsAbs(hostsPath) || filepath.Clean(hostsPath) != hostsPath || filepath.Base(hostsPath) != "container-hosts" || hostsPath == "/etc/hosts" {
		return errors.New("explicit_owned_container_hosts_required")
	}
	hostInfo, e := os.Lstat(hostsPath)
	if e != nil || !hostInfo.Mode().IsRegular() || hostInfo.Mode().Perm() != 0600 || hostInfo.Size() > 128<<10 {
		return errors.New("invalid_owned_container_hosts")
	}
	hostFile, e := os.OpenFile(hostsPath, os.O_RDWR, 0)
	if e != nil {
		return errors.New("owned_container_hosts_not_writable")
	}
	hostFile.Close()
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
	var input any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&input) != nil {
		return errors.New("invalid_reviewed_source_snapshot")
	}
	canonical, _ := json.Marshal(input)
	sum := sha256.Sum256(append([]byte("CustomHosts/1.2.1/reviewed-configuration/"), canonical...))
	fingerprint := hex.EncodeToString(sum[:])
	var existing, receipts, active int
	if ro.QueryRow(`SELECT COUNT(*) FROM plugin_installations WHERE instance_id='CustomHosts'`).Scan(&existing) != nil || ro.QueryRow(`SELECT COUNT(*) FROM plugin_migration_receipts WHERE source_fingerprint=? AND source_plugin_id='CustomHosts' AND instance_id='CustomHosts'`, fingerprint).Scan(&receipts) != nil {
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
	m, e := p.BeginMigration(context.Background(), fingerprint, "CustomHosts", "CustomHosts")
	if e != nil {
		return errors.New("migration_plan_failed")
	}
	receipt, e := p.ImportReviewedCustomHosts(context.Background(), m.ID, raw)
	if e != nil {
		return errors.New("atomic_import_failed")
	}
	var count int
	if s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_runs WHERE instance_id='CustomHosts')+(SELECT COUNT(*) FROM plugin_schedules WHERE instance_id='CustomHosts')+(SELECT COUNT(*) FROM plugin_event_subscriptions WHERE instance_id='CustomHosts')`).Scan(&count) != nil {
		return errors.New("post_import_read_failed")
	}
	if existing == 0 && count != 0 {
		return errors.New("unexpected_import_runtime_effect")
	}
	installation, e := p.Installation(context.Background(), "CustomHosts")
	if e != nil || existing == 0 && !installation.DesiredEnabled {
		return errors.New("reviewed_package_activation_mismatch")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "apply", "state": "imported", "receipt": receipt, "preferences_written": 0, "replayed": existing > 0, "scheduled": false, "executed": false, "desired_enabled": installation.DesiredEnabled, "source_configuration_preserved": true})
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e.Error())
		os.Exit(1)
	}
}
