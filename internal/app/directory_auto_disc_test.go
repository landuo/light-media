package app

import (
	"database/sql"
	"errors"
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

func TestOpenStoreDropsOnlyLegacyBlurayStreamObservations(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if store != nil {
			store.Close()
		}
	}()
	disc := filepath.Join(t.TempDir(), "Film")
	member := filepath.Join(disc, "BDMV", "STREAM", "00001.m2ts")
	importWrite(t, member, []byte("stream"))
	paths := []string{member, filepath.Join(disc, "bdmv", "Stream", "00002.m2ts"), disc, filepath.Join(disc, "BDMV", "PLAYLIST", "00000.mpls"), filepath.Join(disc, "BDMV", "STREAM-EXTRAS", "bonus.mkv")}
	for _, path := range paths {
		id := directoryFileID("watch", path)
		if err := store.Put(ctx, "directory_file", id, DirectoryObservedFile{ID: id, DirectoryID: "watch", Path: path, State: "baseline"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO transferhistory(src,status,date) VALUES(?,1,?)`, member, now()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i, path := range paths {
		var observation DirectoryObservedFile
		err := store.Get(ctx, "directory_file", directoryFileID("watch", path), &observation)
		if i < 2 {
			if !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("stale observation for %s: %+v, %v", path, observation, err)
			}
		} else if err != nil || observation.Path != path || observation.State != "baseline" {
			t.Fatalf("surviving observation for %s: %+v, %v", path, observation, err)
		}
	}
	var history int
	if err := store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM transferhistory WHERE src=? AND status=1`, member).Scan(&history); err != nil || history != 1 {
		t.Fatalf("history after restart: count=%d err=%v", history, err)
	}
	if contents, err := os.ReadFile(member); err != nil || string(contents) != "stream" {
		t.Fatalf("source after restart: %q %v", contents, err)
	}
	if count, err := dropBlurayStreamObservations(ctx, store.DB); err != nil || count != 0 {
		t.Fatalf("repeat cleanup: count=%d err=%v", count, err)
	}
}
