package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscMemberSnapshotsMatchRegularFilesAndRejectInvalidNames(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stream := filepath.Join(root, "BDMV", "STREAM", "00001.m2ts")
	importWrite(t, stream, []byte("stream"))
	if err := os.Link(stream, filepath.Join(filepath.Dir(stream), "00002.m2ts")); err != nil {
		t.Fatal(err)
	}
	_, entries, err := snapshotOriginalDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	for _, entry := range entries {
		if entry.Directory {
			continue
		}
		files++
		want, err := snapshotPath(filepath.Join(root, entry.Path))
		if err != nil || entry.Snapshot != want {
			t.Fatalf("%s: snapshot %+v, expected %+v, err %v", entry.Path, entry.Snapshot, want, err)
		}
	}
	if files != 2 {
		t.Fatalf("got %d regular entries, want both hardlinks", files)
	}
	importWrite(t, filepath.Join(filepath.Dir(stream), `invalid\name.m2ts`), []byte("invalid"))
	_, _, err = snapshotOriginalDirectory(root)
	var api *APIError
	if !errors.As(err, &api) || api.Code != "INVALID_PATH" {
		t.Fatalf("invalid member path: %v", err)
	}
}
