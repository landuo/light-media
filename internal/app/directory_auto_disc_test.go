package app

import (
	"path/filepath"
	"testing"
)

// Filesystem mechanism assertions, not an additional publisher/SDK matrix.
func TestAutomaticDiscRequiresAllReportedLeavesAndOneTask(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, second := filepath.Join(root, "BDMV/STREAM/00001.m2ts"), filepath.Join(root, "BDMV/index.bdmv")
	importWrite(t, first, []byte("stream"))
	stamp, _, err := snapshotOriginalDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]directoryDownloadFile{first: {Complete: true, Size: 6, OwnerHash: "one"}, second: {Complete: true, Size: 5, OwnerHash: "one"}}
	owner, known, err := automaticDiscDownloadOwner(root, stamp, files)
	if err != nil || !known || owner.Complete {
		t.Fatal("absent reported member admitted", owner, known, err)
	}
	importWrite(t, second, []byte("index"))
	stamp, _, err = snapshotOriginalDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	owner, known, err = automaticDiscDownloadOwner(root, stamp, files)
	if err != nil || !known || !owner.Complete || owner.Size != 11 {
		t.Fatal("complete same-task aggregate", owner, known, err)
	}
	leaf := files[second]
	leaf.OwnerHash = "another"
	files[second] = leaf
	owner, known, err = automaticDiscDownloadOwner(root, stamp, files)
	if err != nil || !known || owner.Complete || !owner.IdentityConflict {
		t.Fatal("mixed torrent disc admitted", owner, known, err)
	}
}
