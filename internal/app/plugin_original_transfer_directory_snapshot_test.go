package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOriginalTransferDirectorySnapshotOwnership(t *testing.T) {
	// The source directory branch recursively transfers every member, not only
	// STREAM videos. Guard that whole unit without changing regular-file APIs.
	root := t.TempDir()
	stream := filepath.Join(root, "BDMV", "STREAM", "00001.m2ts")
	index := filepath.Join(root, "BDMV", "index.bdmv")
	importWrite(t, stream, []byte("owned video"))
	importWrite(t, index, []byte("index"))
	if err := os.Mkdir(filepath.Join(root, "CERTIFICATE"), 0750); err != nil {
		t.Fatal(err)
	}
	before, entries, err := snapshotOriginalDirectory(root)
	if err != nil || before.Directory == "" || before.Size != 16 || len(entries) != 5 {
		t.Fatal(before, entries, err)
	}
	if _, err := snapshotPath(root); err == nil {
		t.Fatal("regular-file snapshot accepted a directory")
	}
	after, err := snapshotTransferPath(root)
	if err != nil || before != after {
		t.Fatal("stable tree", after, err)
	}
	if err := os.WriteFile(index, []byte("changed index"), 0640); err != nil {
		t.Fatal(err)
	}
	after, err = snapshotTransferPath(root)
	if err != nil || before == after {
		t.Fatal("non-video mutation escaped tree identity", err)
	}
	before = after
	if err := os.Mkdir(filepath.Join(root, "BDMV", "EMPTY"), 0750); err != nil {
		t.Fatal(err)
	}
	after, err = snapshotTransferPath(root)
	if err != nil || before == after {
		t.Fatal("empty directory escaped tree identity", err)
	}
	if err := os.Symlink(index, filepath.Join(root, "BDMV", "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotTransferPath(root); err == nil {
		t.Fatal("tree followed symlink")
	}
}
