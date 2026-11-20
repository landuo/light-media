package app

import (
	"context"
	"testing"
)

// Organizing a single source file must not need a jobs row: the file is the
// business state, and a restart re-derives the work from it.
func TestOrganizeFileRunsWithoutARow(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	taskID := "job_organize_probe"
	payload := organizeFilePayload{ID: "probe", File: importScanFile{Path: "/tmp/probe/S01E01.mkv"}}
	j := a.submitMemoryJob("organize_file", payload, "wf_probe", taskID)
	if j.ID != taskID || !j.Ephemeral {
		t.Fatalf("submitted %+v", j)
	}
	var rows int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE id=?`, taskID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("no row may be written, got %d", rows)
	}
	// The duplicate gate must still see it, from the index.
	if id, ok := a.indexSourceActive("/tmp/probe/S01E01.mkv"); !ok || id != taskID {
		t.Fatalf("the source must be reported busy, got %q %v", id, ok)
	}
	if _, ok := a.indexSourceActive("/tmp/probe/other.mkv"); ok {
		t.Fatal("an unrelated source must not be busy")
	}
	claimed, err := a.claimFromIndex(ctx, generalJobs)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != taskID {
		t.Fatalf("claimed %+v", claimed)
	}
}
