package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImportSharedDirectorySnapshotOwnsOnlyPublishedSubset(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "owned.mkv")
	importWrite(t, path, []byte("owned bytes"))
	unrelated := filepath.Join(root, "external.nfo")
	importWrite(t, unrelated, []byte("unrelated"))
	entry, err := snapshotPath(path)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := importDir(root, false)
	if err != nil {
		t.Fatal(err)
	}
	info, err := dir.Stat()
	dir.Close()
	if err != nil {
		t.Fatal(err)
	}
	identity, err := originalDirectoryIdentity(info)
	if err != nil {
		t.Fatal(err)
	}
	proof := &importSharedDirectoryProof{Root: sharedDirectoryIdentity(identity), Directories: map[string]importSnapshot{}, Members: map[string]importSharedDirectoryMember{"owned.mkv": {Snapshot: entry}}}
	expected, err := snapshotSharedDirectory(root, proof.Root, proof, true)
	if err != nil || expected.Size != entry.Size {
		t.Fatal(expected, err)
	}
	// An unrelated writer retains authority over its own file after publication.
	if err = os.WriteFile(unrelated, []byte("new unrelated content"), 0600); err != nil {
		t.Fatal(err)
	}
	if actual, e := snapshotSharedDirectory(root, expected, proof, true); e != nil || actual != expected {
		t.Fatal("unrelated member was included in ownership", actual, e)
	}
	if err = os.WriteFile(path, []byte("changed owned bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = snapshotSharedDirectory(root, expected, proof, true); err == nil {
		t.Fatal("changed owned file accepted")
	}
}
