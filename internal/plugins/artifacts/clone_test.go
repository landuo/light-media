package artifacts

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins"
)

func TestCloneInstalledCopiesIndependentCodeAndExactReceipt(t *testing.T) {
	req, client, _, _ := fixture(t, []byte("inert-owned-bytes"), "executable")
	source, err := Acquire(context.Background(), req, client)
	if err != nil {
		t.Fatal(err)
	}
	cloned, err := CloneInstalled(req.Root, req.PluginID, "OwnedClone", req.Validate)
	if err != nil {
		t.Fatal(err)
	}
	if cloned.Manifest.PluginID != source.Manifest.PluginID || cloned.SHA256 != source.SHA256 || cloned.Command.Dir == source.Command.Dir {
		t.Fatalf("clone mismatch: %+v", cloned)
	}
	a, err := os.ReadFile(filepath.Join(source.Command.Dir, ".acquired.json"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(cloned.Command.Dir, ".acquired.json"))
	if err != nil || !bytes.Equal(a, b) {
		t.Fatalf("receipt changed: %v", err)
	}
	si, _ := os.Stat(source.ExecutablePath)
	ci, _ := os.Stat(cloned.ExecutablePath)
	if os.SameFile(si, ci) {
		t.Fatal("shared inode instead of independent copy")
	}
	if err = os.WriteFile(source.ExecutablePath, []byte("owned replacement"), 0700); err != nil {
		t.Fatal(err)
	}
	read, err := ReadInstalled(req.Root, "OwnedClone", req.Validate)
	if err != nil || read.SHA256 != cloned.SHA256 {
		t.Fatalf("source update changed clone: %+v %v", read, err)
	}
}
func TestCloneInstalledRejectsExistingAndUnsafeTargets(t *testing.T) {
	for _, kind := range []string{"directory", "file", "link", "same", "traversal"} {
		t.Run(kind, func(t *testing.T) {
			req, client, _, _ := fixture(t, []byte("inert-owned-bytes"), "executable")
			source, err := Acquire(context.Background(), req, client)
			if err != nil {
				t.Fatal(err)
			}
			targetID := "OwnedClone"
			target := filepath.Join(req.Root, targetID)
			switch kind {
			case "directory":
				os.Mkdir(target, 0700)
				os.WriteFile(filepath.Join(target, "keep"), []byte("owned sentinel"), 0600)
			case "file":
				os.WriteFile(target, []byte("owned sentinel"), 0600)
			case "link":
				if err = os.Symlink(source.Command.Dir, target); err != nil {
					t.Skip(err)
				}
			case "same":
				targetID = req.PluginID
			case "traversal":
				targetID = "../escape"
			}
			if _, err = CloneInstalled(req.Root, req.PluginID, targetID, req.Validate); err == nil {
				t.Fatal("unsafe/existing target accepted")
			}
			if _, err = ReadInstalled(req.Root, req.PluginID, req.Validate); err != nil {
				t.Fatalf("source changed: %v", err)
			}
			if kind == "directory" {
				if b, err := os.ReadFile(filepath.Join(target, "keep")); err != nil || string(b) != "owned sentinel" {
					t.Fatal("existing target altered")
				}
			}
			if kind == "file" || kind == "link" {
				if _, err := os.Lstat(target); err != nil {
					t.Fatal("existing target removed")
				}
			}
		})
	}
}
func TestCloneInstalledFailureRemovesOnlyNewTarget(t *testing.T) {
	req, client, _, _ := fixture(t, []byte("inert-owned-bytes"), "executable")
	if _, err := Acquire(context.Background(), req, client); err != nil {
		t.Fatal(err)
	}
	calls := 0
	validate := func(m plugins.Manifest, c string) error {
		calls++
		if calls == 2 {
			return errors.New("owned clone verification rejection")
		}
		return req.Validate(m, c)
	}
	if _, err := CloneInstalled(req.Root, req.PluginID, "OwnedClone", validate); err == nil {
		t.Fatal("expected post-copy rejection")
	}
	if _, err := os.Lstat(filepath.Join(req.Root, "OwnedClone")); !os.IsNotExist(err) {
		t.Fatalf("partial clone remains: %v", err)
	}
	if _, err := ReadInstalled(req.Root, req.PluginID, req.Validate); err != nil {
		t.Fatalf("source altered: %v", err)
	}
}
