package artifacts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins"
)

func TestCodeBackupRestoreMissingAndPreserveExisting(t *testing.T) {
	req, client, idx, _ := fixture(t, []byte("inert-owned-bytes"), "executable")
	req.RefreshBackup, req.BackupRoot = true, t.TempDir()
	if _, err := Acquire(context.Background(), req, client); err != nil {
		t.Fatal(err)
	}
	read := func(root string) Acquired {
		t.Helper()
		a, err := ReadInstalled(root, req.PluginID, req.Validate)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	if read(req.BackupRoot).Manifest.Version != "1.2.3" {
		t.Fatal("initial backup missing")
	}
	idx.CurrentVersion, idx.Releases[0].Version, idx.Releases[0].Manifest.Version = "2.0", "2.0", "2.0"
	req.RefreshBackup = false
	if _, err := Acquire(context.Background(), req, client); err != nil {
		t.Fatal(err)
	}
	if err := BackupInstalled(req.Root, req.BackupRoot, req.PluginID, false, req.Validate); err != nil {
		t.Fatal(err)
	}
	if read(req.BackupRoot).Manifest.Version != "1.2.3" {
		t.Fatal("shutdown backup overwrote existing snapshot")
	}
	if changed, err := RestoreMissingInstalled(req.Root, req.BackupRoot, req.PluginID, req.Validate); err != nil || changed {
		t.Fatalf("existing code overwritten: %t %v", changed, err)
	}
	if read(req.Root).Manifest.Version != "2.0" {
		t.Fatal("restoration downgraded existing code")
	}
	if err := BackupInstalled(req.Root, req.BackupRoot, req.PluginID, true, req.Validate); err != nil {
		t.Fatal(err)
	}
	if read(req.BackupRoot).Manifest.Version != "2.0" {
		t.Fatal("explicit refresh did not update backup")
	}
	if err := os.RemoveAll(filepath.Join(req.Root, req.PluginID)); err != nil {
		t.Fatal(err)
	}
	if changed, err := RestoreMissingInstalled(req.Root, req.BackupRoot, req.PluginID, req.Validate); err != nil || !changed {
		t.Fatalf("missing code not restored: %t %v", changed, err)
	}
	runtime, backup := read(req.Root), read(req.BackupRoot)
	a, _ := os.Stat(runtime.ExecutablePath)
	b, _ := os.Stat(backup.ExecutablePath)
	if os.SameFile(a, b) {
		t.Fatal("restore shares the backup inode")
	}
}

func TestCodeBackupRejectsInvalidBytesIdentityAndPaths(t *testing.T) {
	for _, reason := range []string{"corrupt", "identity", "symlink", "overlap"} {
		t.Run(reason, func(t *testing.T) {
			req, client, _, _ := fixture(t, []byte("inert-owned-bytes"), "executable")
			if _, err := Acquire(context.Background(), req, client); err != nil {
				t.Fatal(err)
			}
			backup := t.TempDir()
			if reason == "overlap" {
				if err := BackupInstalled(req.Root, filepath.Join(req.Root, req.PluginID, "nested"), req.PluginID, true, req.Validate); err == nil {
					t.Fatal("recursive backup accepted")
				}
				return
			}
			if err := BackupInstalled(req.Root, backup, req.PluginID, true, req.Validate); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(filepath.Join(req.Root, req.PluginID)); err != nil {
				t.Fatal(err)
			}
			validate := req.Validate
			switch reason {
			case "corrupt":
				if err := os.WriteFile(filepath.Join(backup, req.PluginID, "bin/worker"), []byte("tampered"), 0700); err != nil {
					t.Fatal(err)
				}
			case "identity":
				validate = func(plugins.Manifest, string) error { return errors.New("persisted version differs") }
			case "symlink":
				source := filepath.Join(backup, req.PluginID, "bin/worker")
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), source); err != nil {
					t.Fatal(err)
				}
			}
			if changed, err := RestoreMissingInstalled(req.Root, backup, req.PluginID, validate); err == nil || changed {
				t.Fatal("unsafe restore succeeded")
			}
			if _, err := os.Lstat(filepath.Join(req.Root, req.PluginID)); !os.IsNotExist(err) {
				t.Fatal("failure created runtime code")
			}
			if _, err := os.Lstat(filepath.Join(backup, req.PluginID)); err != nil {
				t.Fatal("failure deleted recoverable backup")
			}
		})
	}
}
