package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
	"light-media/internal/app"
)

// Run the actual command entry point in a separate process. Do not compile a
// second binary or point any test at a user, production, or NAS directory.
func TestCommandProcess(t *testing.T) {
	if os.Getenv("REMOVE_LINK_OWNED_CLI_CHILD") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{os.Args[0]}, os.Args[i+1:]...)
			main()
			return
		}
	}
	os.Exit(99)
}

func command(t *testing.T, raw []byte, args ...string) (map[string]any, bool) {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestCommandProcess$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "REMOVE_LINK_OWNED_CLI_CHILD=1")
	cmd.Stdin = bytes.NewReader(raw)
	out, err := cmd.CombinedOutput()
	if strings.Contains(string(out), "private-marker") {
		t.Fatal("command disclosed private input")
	}
	var result map[string]any
	if json.Unmarshal(out, &result) != nil {
		t.Fatalf("non-JSON command result: %s (%v)", out, err)
	}
	return result, err == nil
}

func input(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"version": 1, "source_container": strings.Repeat("a", 64), "plugin_id": "RemoveLink", "instance_id": "RemoveLink", "source_sha256": "1f9e719e16f5f72340aa50e942d6f7062390a4bddd82f5f3f47d58ef757dbda4", "installed_count": 1, "config": `{"enabled":false,"monitor_dirs":""}`, "data": map[string]any{}, "file_count": 0, "symlink_count": 0, "path_mappings": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func target(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private-marker-target")
	s, err := app.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.NewPluginStore(context.Background(), s); err != nil {
		s.Close()
		t.Fatal(err)
	}
	// A real encrypted configuration is required even for the empty mapping.
	sealed, err := s.Seal(app.Settings{})
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`INSERT INTO records(kind,id,payload,updated_at) VALUES('config','main',?,'owned-fixture')`, sealed); err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Hash file bytes, directory entries, permission modes, symlink destinations,
// and mtimes. Reads may change atime, which is deliberately excluded.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		value := fmt.Sprintf("%s:%d", info.Mode(), info.ModTime().UnixNano())
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			h := sha256.Sum256(b)
			value += ":" + hex.EncodeToString(h[:])
		}
		if info.Mode()&os.ModeSymlink != 0 {
			dest, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value += ":" + dest
		}
		result[rel] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func noWrites(t *testing.T, dir string, raw []byte, args ...string) (map[string]any, bool) {
	t.Helper()
	before := snapshot(t, dir)
	result, ok := command(t, raw, args...)
	if !reflect.DeepEqual(before, snapshot(t, dir)) {
		t.Fatal("command changed target files, directories, modes, or mtimes")
	}
	return result, ok
}

func TestValidationSubprocessNeverOpensTarget(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		raw  []byte
		args []string
		ok   bool
	}{
		{"valid", input(t), []string{"--validate"}, true},
		{"malformed", []byte(`{"private-marker":"broken"`), []string{"--validate"}, false},
		{"oversized", bytes.Repeat([]byte("x"), maxInput+1), []string{"--validate"}, false},
		{"conflict", input(t), []string{"--validate", "--apply"}, false},
		{"target-in-validation", input(t), []string{"--validate", "--target-data-dir", filepath.Join(dir, "private-marker-missing")}, false},
		{"unknown", input(t), []string{"--private-marker"}, false},
		{"positional", input(t), []string{"private-marker"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := noWrites(t, dir, tc.raw, tc.args...)
			if ok != tc.ok {
				t.Fatal("unexpected command status")
			}
		})
	}
}

func TestDryRunSubprocessZeroWrites(t *testing.T) {
	dir := target(t)
	result, ok := noWrites(t, dir, input(t), "--target-data-dir", dir)
	if !ok || result["mode"] != "read_only_dry_run" || result["writes"] != float64(0) {
		t.Fatal(result)
	}
}

func TestBoundaryFailuresSubprocessZeroWrites(t *testing.T) {
	for _, kind := range []string{"lock", "wal", "journal", "shm", "key-missing", "key-invalid", "lock-missing", "db-missing", "schema-missing", "schema-version", "db-symlink", "key-symlink", "lock-symlink", "directory-symlink", "ancestor-symlink", "unfinished"} {
		t.Run(kind, func(t *testing.T) {
			dir := target(t)
			root := filepath.Dir(dir)
			requested := dir
			write := func(p string, b []byte) {
				t.Helper()
				if err := os.WriteFile(p, b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			rename := func(p string) {
				t.Helper()
				if err := os.Rename(p, p+".saved"); err != nil {
					t.Fatal(err)
				}
			}
			symlink := func(from, to string) {
				t.Helper()
				if err := os.Symlink(from, to); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "lock":
				f, err := os.Open(filepath.Join(dir, "instance.lock"))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
					t.Fatal("fixture lock")
				}
				defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
			case "wal", "journal", "shm":
				write(filepath.Join(dir, "light-media.db-"+kind), []byte("private-marker-dirty"))
			case "key-missing":
				rename(filepath.Join(dir, "secret.key"))
			case "lock-missing":
				rename(filepath.Join(dir, "instance.lock"))
			case "db-missing":
				rename(filepath.Join(dir, "light-media.db"))
			case "key-invalid":
				write(filepath.Join(dir, "secret.key"), []byte("short"))
			case "db-symlink", "key-symlink", "lock-symlink":
				name := map[string]string{"db-symlink": "light-media.db", "key-symlink": "secret.key", "lock-symlink": "instance.lock"}[kind]
				p := filepath.Join(dir, name)
				rename(p)
				symlink(p+".saved", p)
			case "directory-symlink":
				requested = filepath.Join(root, "private-marker-link")
				symlink(dir, requested)
			case "ancestor-symlink":
				alias := filepath.Join(root, "private-marker-parent")
				symlink(root, alias)
				requested = filepath.Join(alias, filepath.Base(dir))
			case "schema-missing", "schema-version", "unfinished":
				db, err := openDatabase(filepath.Join(dir, "light-media.db"), "rw")
				if err != nil {
					t.Fatal(err)
				}
				query := map[string]string{"schema-missing": `ALTER TABLE plugin_storage_schema RENAME TO saved_plugin_storage_schema`, "schema-version": `UPDATE plugin_storage_schema SET version=99`, "unfinished": `INSERT INTO jobs(id,workflow_id,kind,state,payload,run_at,created_at,updated_at) VALUES('owned','owned','test','retry_wait','{}',0,'','')`}[kind]
				if _, err = db.Exec(query); err != nil {
					db.Close()
					t.Fatal(err)
				}
				db.Close()
			}
			for _, apply := range []bool{false, true} {
				args := []string{"--target-data-dir", requested}
				if apply {
					args = append(args, "--apply")
				}
				result, ok := noWrites(t, root, input(t), args...)
				if ok || result["state"] != "failed" {
					t.Fatal(result)
				}
			}
		})
	}
}

func TestApplySubprocessDisabledAndIdempotent(t *testing.T) {
	dir := target(t)
	raw := input(t)
	for i := 0; i < 2; i++ {
		result, ok := command(t, raw, "--target-data-dir", dir, "--apply")
		if !ok || result["state"] != "imported" || result["desired_enabled"] != false || result["replayed"] != (i > 0) {
			t.Fatal(result)
		}
		ro, err := openDatabase(filepath.Join(dir, "light-media.db"), "ro")
		if err != nil {
			t.Fatal(err)
		}
		var installations, receipts, runtime int
		if err = ro.QueryRow(`SELECT COUNT(*) FROM plugin_installations WHERE instance_id='RemoveLink' AND desired_enabled=0`).Scan(&installations); err != nil {
			t.Fatal(err)
		}
		if err = ro.QueryRow(`SELECT COUNT(*) FROM plugin_migration_receipts WHERE instance_id='RemoveLink'`).Scan(&receipts); err != nil {
			t.Fatal(err)
		}
		if err = ro.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_runs)+(SELECT COUNT(*) FROM plugin_schedules)+(SELECT COUNT(*) FROM plugin_event_subscriptions)+(SELECT COUNT(*) FROM plugin_values WHERE kind='data')+(SELECT COUNT(*) FROM jobs)`).Scan(&runtime); err != nil {
			t.Fatal(err)
		}
		ro.Close()
		if installations != 1 || receipts != 1 || runtime != 0 {
			t.Fatal("unexpected import effect", installations, receipts, runtime)
		}
	}
	result, ok := noWrites(t, dir, raw, "--target-data-dir", dir)
	if !ok || result["same_receipt"] != true {
		t.Fatal(result)
	}
}

func TestReplayPreservesLaterUserState(t *testing.T) {
	dir := target(t)
	raw := input(t)
	if result, ok := command(t, raw, "--target-data-dir", dir, "--apply"); !ok {
		t.Fatal(result)
	}
	db, err := openDatabase(filepath.Join(dir, "light-media.db"), "rw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE plugin_installations SET desired_enabled=1 WHERE instance_id='RemoveLink'; INSERT INTO plugin_runs(id,instance_id,version,generation,config_revision,action,state,input,created_at,updated_at) VALUES('owned-completed','RemoveLink','2.17',1,1,'service','canceled',X'00','','')`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	result, ok := noWrites(t, dir, raw, "--target-data-dir", dir)
	if !ok || result["desired_enabled"] != true || result["same_receipt"] != true {
		t.Fatal(result)
	}
	result, ok = command(t, raw, "--target-data-dir", dir, "--apply")
	if !ok || result["desired_enabled"] != true || result["replayed"] != true {
		t.Fatal(result)
	}
	ro, err := openDatabase(filepath.Join(dir, "light-media.db"), "ro")
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	var runs int
	if err = ro.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE id='owned-completed' AND state='canceled'`).Scan(&runs); err != nil || runs != 1 {
		t.Fatal("user runtime state changed", err)
	}
}
