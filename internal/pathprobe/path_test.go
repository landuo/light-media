package pathprobe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContainedPathRejectsSymlinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "existing"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "missing"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../outside", "escape/existing", "escape/new", "dangling", "/absolute", "."} {
		if _, err := ContainedPath(root, path); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "inside"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "inside"), filepath.Join(root, "safe")); err != nil {
		t.Fatal(err)
	}
	if _, err := ContainedPath(root, "safe/new"); err != nil {
		t.Fatal(err)
	}
}

func TestProbeOnlyOwnsTemporaryFiles(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	for _, dir := range []string{source, target} {
		if err := os.WriteFile(filepath.Join(dir, "existing-media.mkv"), []byte("untouched"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Run(source, target)
	if err != nil || r.Status != "passed" || !r.LinkSucceeded || !r.SameFile || !r.SourceContentKept || !r.RenameSourceKept || !r.UnlinkSourceKept || !r.CleanupSucceeded {
		t.Fatalf("%+v %v", r, err)
	}
	for _, dir := range []string{source, target} {
		files, err := os.ReadDir(dir)
		if err != nil || len(files) != 1 || files[0].Name() != "existing-media.mkv" {
			t.Fatalf("leaked or modified files %v %v", files, err)
		}
		data, _ := os.ReadFile(filepath.Join(dir, "existing-media.mkv"))
		if string(data) != "untouched" {
			t.Fatal("existing data changed")
		}
	}
}

func TestProbeUnavailableDirectoryIsNotSuccess(t *testing.T) {
	r, err := Run(filepath.Join(t.TempDir(), "missing"), t.TempDir())
	if err == nil || r.Status != "failed" || r.Reason != "source_directory_unavailable" {
		t.Fatalf("%+v %v", r, err)
	}
}
