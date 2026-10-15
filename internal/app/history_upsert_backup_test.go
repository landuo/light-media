package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHistoryUpsertHiddenCutoffSurvivesVacuumBackupAndRestore(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	input := filepath.Join(source, "unknown.mkv")
	importWrite(t, input, []byte("isolated backup restore resource"))
	first := historyUpsertSubmit(t, a, manualOrganizeRequest{Paths: []string{input}}, true)
	firstPlan, _ := historyUpsertAttempt(t, a, first.JobID)
	// Older implementations persisted this implicit row number as the hide
	// cutoff. VACUUM INTO can compact it, trapping every later attempt below it.
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET rowid=rowid+1000`); err != nil {
		t.Fatal(err)
	}
	item := historyUpsertOnly(t, a, "")
	if _, err := a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true, FileID: item.executionFileID()}); err != nil {
		t.Fatal(err)
	}
	if page := historyParityReadPage(t, a, ""); page.Total != 0 {
		t.Fatal("history was not hidden before backup", page)
	}
	// Persist the synthetic app's mount configuration so New restores it from
	// the encrypted backup, using only the fixture's own encryption key.
	if err := a.saveSettings(ctx, a.Settings()); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(filepath.Dir(a.Dir), "history-backups")
	name, err := a.Store.Backup(ctx, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	restoreDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for destination, original := range map[string]string{
		"light-media.db": filepath.Join(backupDir, name),
		"secret.key":     filepath.Join(a.Dir, "secret.key"),
	} {
		contents, err := os.ReadFile(original)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(restoreDir, destination), contents, 0600); err != nil {
			t.Fatal(err)
		}
	}
	restored, err := New(Options{DataDir: restoreDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restored.Close() })
	var originalRow, restoredRow int64
	if err = a.Store.DB.QueryRow(`SELECT rowid FROM jobs WHERE id=?`, first.JobID).Scan(&originalRow); err != nil {
		t.Fatal(err)
	}
	if err = restored.Store.DB.QueryRow(`SELECT rowid FROM jobs WHERE id=?`, first.JobID).Scan(&restoredRow); err != nil {
		t.Fatal(err)
	}
	// SQLite versions may preserve the implicit row IDs of indexed tables.
	// Exercise the legal compacted layout explicitly when this build does so.
	if restoredRow == originalRow {
		if _, err = restored.Store.DB.Exec(`UPDATE jobs SET rowid=rowid-1000`); err != nil {
			t.Fatal(err)
		}
		if err = restored.Store.DB.QueryRow(`SELECT rowid FROM jobs WHERE id=?`, first.JobID).Scan(&restoredRow); err != nil {
			t.Fatal(err)
		}
	}
	if originalRow <= 1000 || restoredRow >= originalRow {
		t.Fatalf("fixture did not exercise compacted implicit rows after backup restoration: before=%d after=%d", originalRow, restoredRow)
	}
	if page := historyParityReadPage(t, restored, ""); page.Total != 0 {
		t.Fatal("backup restore resurrected hidden history", page)
	}
	second := historyUpsertSubmit(t, restored, manualOrganizeRequest{Paths: []string{input}}, true)
	secondPlan, _ := historyUpsertAttempt(t, restored, second.JobID)
	latest := historyUpsertOnly(t, restored, "")
	if latest.ID != firstPlan.Files[0].ID || latest.PlanID != secondPlan.ID || latest.executionFileID() != secondPlan.Files[0].ID || latest.Status != "failed" {
		t.Fatal("new execution remained hidden or lost stable identity after restore", latest)
	}
	if contents, err := os.ReadFile(input); err != nil || string(contents) != "isolated backup restore resource" {
		t.Fatal("history backup/restore changed source", err)
	}
}

func TestHistoryUpsertChronologyNormalizesRFC3339FractionLengths(t *testing.T) {
	a, source, target := importTestApp(t)
	first, firstJob := historyParityPlan(t, a, source, target, "fraction-first", "较早执行", "succeeded", "imported", "Film.mkv")
	second, secondJob := historyParityPlan(t, a, source, target, "fraction-second", "较新执行", "failed", "ready", "Film.mkv")
	second.Files[0].Source = first.Files[0].Source
	if err := a.Store.Put(context.Background(), "import_plan", second.ID, second); err != nil {
		t.Fatal(err)
	}
	// RFC3339Nano omits trailing zeroes. Raw string ordering wrongly places
	// .1Z after .11Z although .11 seconds is the later instant.
	for jobID, timestamp := range map[string]string{
		firstJob.ID:  "2026-10-15T00:00:00.1Z",
		secondJob.ID: "2026-10-15T00:00:00.11Z",
	} {
		if _, err := a.Store.DB.Exec(`UPDATE jobs SET created_at=? WHERE id=?`, timestamp, jobID); err != nil {
			t.Fatal(err)
		}
	}
	latest := historyUpsertOnly(t, a, "")
	if latest.ID != first.Files[0].ID || latest.PlanID != second.ID || latest.executionFileID() != second.Files[0].ID || latest.Status != "failed" {
		t.Fatalf("variable RFC3339 fractions selected wrong current execution/anchor: %+v", latest)
	}
	if page := historyParityReadPage(t, a, "status=success"); page.Total != 0 {
		t.Fatal("chronologically earlier success leaked into current-state filter", page)
	}
}
