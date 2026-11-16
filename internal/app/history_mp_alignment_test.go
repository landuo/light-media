package app

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// MoviePilot history.py converts an explicit glob without adding surrounding
// wildcards; transferhistory.py matches title, source and destination separately.
func TestHistoryMPWildcardMatchesEachField(t *testing.T) {
	a, source, target := importTestApp(t)
	historyParityPlan(t, a, source, target, "exact", "Test Film", "succeeded", "imported", "Feature.mkv")
	historyParityPlan(t, a, source, target, "suffix", "Test Film Extended", "succeeded", "imported", "Feature.mkv.backup")
	for _, tc := range []struct {
		query string
		total int
	}{
		{"Test*Film", 1},
		{"*Feature.mkv", 1},
		{"*Feature.mkv*", 2},
		{"*Film*/exact/*", 0}, // Must not join the title and source into one searchable field.
		{"test film", 2},
	} {
		t.Run(tc.query, func(t *testing.T) {
			page := historyParityReadPage(t, a, url.Values{"q": {tc.query}, "capabilities": {"deferred"}}.Encode())
			if page.Total != tc.total {
				t.Fatalf("query %q returned %d rows, want %d", tc.query, page.Total, tc.total)
			}
		})
	}
}

// A successful move is reorganized from its destination, not its removed source.
func TestHistoryMPMovedFileRecognizeCapability(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	plan, _ := historyParityPlan(t, a, source, target, "moved", "Moved Film", "succeeded", "imported", "Feature.mkv")
	plan.Mode, plan.Files[0].Method = "move", "move"
	importWrite(t, plan.Files[0].Target, []byte("moved media fixture"))
	if err := os.Remove(plan.Files[0].Source); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
		t.Fatal(err)
	}
	rewrite := func() {
		t.Helper()
		if _, err := a.Store.DB.Exec(`DELETE FROM transferhistory WHERE src=?`, plan.Files[0].Source); err != nil {
			t.Fatal(err)
		}
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = writeTransferHistoryMPTx(ctx, tx, transferHistoryMPInput{Plan: plan, File: plan.Files[0], Job: Job{ID: "job-moved"}, Success: true}); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	rewrite()
	var item transferHistoryItem
	for _, candidate := range historyParityReadPage(t, a, "page_size=1000").Items {
		if candidate.FileID == plan.Files[0].ID {
			item = candidate
			break
		}
	}
	if item.ID == "" || !item.CanRecognize || item.CanRetry {
		t.Fatalf("moved destination should be available for recognition: %+v", item)
	}
	// A successful copy must still use its source rather than silently taking the target.
	plan.Mode, plan.Files[0].Method = "copy", "copy"
	if err := a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
		t.Fatal(err)
	}
	rewrite()
	lookup := func() transferHistoryItem {
		for _, candidate := range historyParityReadPage(t, a, "page_size=1000").Items {
			if candidate.FileID == plan.Files[0].ID {
				return candidate
			}
		}
		return transferHistoryItem{}
	}
	item = lookup()
	if item.ID == "" || item.CanRecognize {
		t.Fatalf("copy without source should not switch to target: %+v", item)
	}
	// An existing destination outside configured roots does not grant access.
	plan.Mode, plan.Files[0].Method = "move", "move"
	plan.Files[0].Target = filepath.Join(t.TempDir(), "outside.mkv")
	importWrite(t, plan.Files[0].Target, []byte("outside fixture"))
	if err := a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
		t.Fatal(err)
	}
	rewrite()
	item = lookup()
	if item.ID == "" || item.CanRecognize {
		t.Fatalf("unconfigured destination became available: %+v", item)
	}
}
