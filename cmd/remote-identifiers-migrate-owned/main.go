// One-purpose delivery tool. Not part of the public application or API.
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
	"light-media/internal/plugins/builtin"
)

type input struct {
	SourceSHA         string `json:"source_sha256"`
	SourceContainer   string `json:"source_container"`
	ConfigAbsent      bool   `json:"config_absent"`
	SourceEnabled     bool   `json:"source_enabled"`
	InstalledCount    int    `json:"installed_count"`
	KVCount           int    `json:"kv_count"`
	FileCount         int    `json:"file_count"`
	SymlinkCount      int    `json:"symlink_count"`
	GlobalWordsAbsent bool   `json:"global_words_absent"`
}

func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func flatJSON(payload []byte) bool {
	d := json.NewDecoder(bytes.NewReader(payload))
	d.UseNumber()
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return false
		}
		seen[key] = true
		token, err = d.Token()
		if err != nil {
			return false
		}
		switch token.(type) {
		case string, bool, json.Number:
		default:
			return false
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') {
		return false
	}
	_, err = d.Token()
	return err == io.EOF
}
func regular(path string, limit int64) ([]byte, error) {
	st, e := os.Lstat(path)
	if e != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("invalid_owned_input_file")
	}
	return os.ReadFile(path)
}
func run() error {
	var dir string
	var apply bool
	flag.StringVar(&dir, "target-data-dir", "", "existing target application data directory")
	flag.BoolVar(&apply, "apply", false, "perform the reviewed isolated plugin import; default is read-only")
	flag.Parse()
	if flag.NArg() != 0 || dir == "" || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || dir == "/" {
		return errors.New("explicit_reviewed_target_and_digests_required")
	}
	st, e := os.Lstat(dir)
	if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid_target_directory")
	}
	payload, e := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+1))
	if e != nil || len(payload) > 1<<20 || !flatJSON(payload) {
		return errors.New("invalid_source_payload")
	}
	var shape map[string]json.RawMessage
	if json.Unmarshal(payload, &shape) != nil || len(shape) != 9 {
		return errors.New("invalid_source_payload")
	}
	for _, key := range []string{"source_sha256", "source_container", "config_absent", "source_enabled", "installed_count", "kv_count", "file_count", "symlink_count", "global_words_absent"} {
		if len(shape[key]) == 0 || bytes.Equal(shape[key], []byte("null")) {
			return errors.New("invalid_source_payload")
		}
	}
	var in input
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF || in.KVCount != 0 || in.FileCount != 0 || in.SymlinkCount != 0 || in.SourceSHA != "14ec7f5f2964ebd1674305d5294ffa56fb61e2cd1021ea31e50d9675813891a4" || in.SourceContainer != "cb82e3f550881334ef3814426016d08d909bccd88d3297522756ba31654d129a" || in.SourceEnabled || !in.ConfigAbsent || !in.GlobalWordsAbsent || in.InstalledCount != 1 {
		return errors.New("source_metadata_changed")
	}
	manifest, _, exists := builtin.Lookup("RemoteIdentifiers")
	if !exists || manifest.Version != "2.4.1" {
		return errors.New("native_identifiers_handler_unavailable")
	}
	// Acquire the application's existing instance lock; never create anything in
	// dry-run mode and never edit a running target. No daemon signal is sent.
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
		return errors.New("deploy_plugin_platform_before_migration")
	}
	var supportTables int
	if ro.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('plugin_uninstall_source','plugin_uninstall_source_lists','plugin_install_sequence','plugin_source_installed','recognition_words','recognition_word_updates')`).Scan(&supportTables) != nil || supportTables != 6 {
		return errors.New("deploy_final_plugin_platform_before_migration")
	}
	canonical, _ := json.Marshal(in)
	fingerprint := digest(append([]byte("RemoteIdentifiers/2.4.1/source_config_absent/"), canonical...))
	var existing, receipts int
	if ro.QueryRow(`SELECT COUNT(*) FROM plugin_installations WHERE instance_id='RemoteIdentifiers'`).Scan(&existing) != nil {
		return errors.New("target_read_failed")
	}
	if ro.QueryRow(`SELECT COUNT(*) FROM plugin_migration_receipts WHERE source_fingerprint=? AND source_plugin_id='RemoteIdentifiers' AND instance_id='RemoteIdentifiers'`, fingerprint).Scan(&receipts) != nil {
		return errors.New("target_read_failed")
	}
	if existing > 0 && receipts != 1 {
		return errors.New("target_identifiers_instance_exists_without_same_receipt")
	}
	var active int
	if ro.QueryRow(`SELECT (SELECT COUNT(*) FROM jobs WHERE state IN ('running','queued'))+(SELECT COUNT(*) FROM plugin_runs WHERE state IN ('pending','running','reconcile_pending'))`).Scan(&active) != nil || active != 0 {
		return errors.New("target_has_unfinished_active_work")
	}
	key, e := regular(filepath.Join(dir, "secret.key"), 32)
	if e != nil || len(key) != 32 {
		return errors.New("existing_target_encryption_key_required")
	}
	if !apply {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "read_only_dry_run", "state": "ready", "source_fingerprint": fingerprint, "target_exists": existing > 0, "same_receipt": receipts == 1, "source_config_absent": true, "writes": 0})
	}

	// The authenticated encrypted store owns its transaction and receipt. Import
	// deliberately leaves desired_enabled=false and queues no worker invocation.
	ro.Close()
	// OpenStore takes its own nonblocking instance lock. A process winning this
	// handoff makes OpenStore fail; no migration proceeds under a stale lock.
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
	m, e := p.BeginMigration(context.Background(), fingerprint, "RemoteIdentifiers", "RemoteIdentifiers")
	if e != nil {
		return errors.New("migration_plan_failed")
	}
	raw, _ := json.Marshal(manifest)
	r, e := p.Import(context.Background(), app.PluginImport{RunID: m.ID, Installation: app.PluginInstallation{InstanceID: "RemoteIdentifiers", PluginID: "RemoteIdentifiers", Version: "2.4.1", ManifestSchemaVersion: 1, Manifest: raw}, SourceConfigAbsent: true})
	if e != nil {
		return errors.New("atomic_import_failed")
	}
	if existing == 0 {
		i, e := p.Installation(context.Background(), "RemoteIdentifiers")
		if e != nil || !i.Installed || i.DesiredEnabled {
			return errors.New("post_import_activation_mismatch")
		}
		var configs, effects int
		if s.DB.QueryRow(`SELECT COUNT(*) FROM plugin_values WHERE instance_id='RemoteIdentifiers' AND kind='config'`).Scan(&configs) != nil || configs != 0 {
			return errors.New("post_import_absence_mismatch")
		}
		if s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_runs WHERE instance_id='RemoteIdentifiers')+(SELECT COUNT(*) FROM plugin_schedules WHERE instance_id='RemoteIdentifiers')+(SELECT COUNT(*) FROM plugin_event_subscriptions WHERE instance_id='RemoteIdentifiers')`).Scan(&effects) != nil || effects != 0 {
			return errors.New("unexpected_import_runtime_effect")
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "apply", "state": "imported_disabled", "receipt": r, "source_config_absent": true, "scheduled": false, "executed": false, "replayed": existing > 0})

}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e.Error())
		os.Exit(1)
	}
}
