package app

import (
	"os"
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

// A running qB download may create the disc folders, then finish streams one at
// a time. The monitor must not fall through to submitting individual streams.
func TestAutomaticDiscScanWaitsForCompletedMembers(t *testing.T) {
	a, source, _ := importTestApp(t)
	disc := filepath.Join(source, "Fallout.S01.BluRay", "Disc 2")
	stream := filepath.Join(disc, "BDMV", "STREAM")
	pending := filepath.Join(stream, "00054.m2ts.!qB")
	importWrite(t, pending, []byte("part one"))
	check := func(want int) {
		t.Helper()
		out, err := a.scanImportFiles(t.Context(), source, true, nil, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		files := out.(map[string]any)["files"].([]importScanFile)
		if len(files) != want || (want > 0 && files[0].Path != disc) {
			t.Fatalf("expected %d eligible discs, got %+v", want, files)
		}
	}
	check(0)
	importWrite(t, filepath.Join(stream, "00056.m2ts"), []byte("complete stream"))
	check(0)
	if err := os.Rename(pending, filepath.Join(stream, "00054.m2ts")); err != nil {
		t.Fatal(err)
	}
	check(1)
}
