package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryRefreshSeeksCallbacksForDirtyPlans(t *testing.T) {
	a, _, _ := importTestApp(t)
	rows, err := a.Store.DB.Query(`EXPLAIN QUERY PLAN ` + transferHistoryRefreshSQL("transfer_history_dirty"))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var callbackSeek bool
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "SCAN cb") || strings.Contains(detail, "SEARCH cb USING INDEX sqlite_autoindex_records_1 (kind=?)") {
			t.Fatalf("one dirty plan still scans every callback: %s", detail)
		}
		callbackSeek = callbackSeek || strings.Contains(detail, "SEARCH cb USING INDEX transfer_history_callback_plan (<expr>=?)")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !callbackSeek {
		t.Fatal("auxiliary callbacks must be sought by dirty plan ID")
	}
}

func historyIndexMatchesExecutionRecords(t *testing.T, a *App) {
	t.Helper()
	tx, err := a.transferHistoryReadTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, query := range []string{
		transferHistoryBaseCTE + `SELECT COUNT(*) FROM (SELECT ` + transferHistoryIndexColumns + ` FROM attempts EXCEPT SELECT ` + transferHistoryIndexColumns + ` FROM transfer_history_attempts)`,
		transferHistoryBaseCTE + `SELECT COUNT(*) FROM (SELECT ` + transferHistoryIndexColumns + ` FROM transfer_history_attempts EXCEPT SELECT ` + transferHistoryIndexColumns + ` FROM attempts)`,
	} {
		var differences int
		if err := tx.QueryRow(query).Scan(&differences); err != nil || differences != 0 {
			t.Fatalf("index differs from original execution projection: %d %v", differences, err)
		}
	}
	columns := transferHistoryIndexColumns + `,history_id,history_created_at`
	for _, query := range []string{
		transferHistoryCTE + `SELECT COUNT(*) FROM (SELECT ` + columns + ` FROM history EXCEPT SELECT ` + columns + ` FROM transfer_history_current)`,
		transferHistoryCTE + `SELECT COUNT(*) FROM (SELECT ` + columns + ` FROM transfer_history_current EXCEPT SELECT ` + columns + ` FROM history)`,
	} {
		var differences int
		if err := tx.QueryRow(query).Scan(&differences); err != nil || differences != 0 {
			t.Fatalf("current projection differs from original ranking: %d %v", differences, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryIndexIncrementalUpdatesAndTransactionalInvalidation(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	first, job := historyParityPlan(t, a, source, target, "first", "Original", "failed", "error", "one.mkv")
	historyParityPlan(t, a, source, target, "untouched", "Other", "succeeded", "imported", "two.mkv")
	historyIndexMatchesExecutionRecords(t, a)
	// Observe actual writes to prove a single changed attempt does not rebuild
	// the unrelated histories, and repeated reads perform no index writes.
	if _, err := a.Store.DB.Exec(`CREATE TABLE history_index_writes(plan_id TEXT);
 CREATE TRIGGER observe_history_index AFTER INSERT ON transfer_history_attempts BEGIN INSERT INTO history_index_writes VALUES(NEW.plan_id); END;`); err != nil {
		t.Fatal(err)
	}
	first.Media.Title = "Updated title"
	first.Files[0].Target = filepath.Join(target, "changed.mkv")
	if err := a.Store.Put(ctx, "import_plan", first.ID, first); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "unrelated_fixture", "same", map[string]string{"name": "not history"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='running' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	historyIndexMatchesExecutionRecords(t, a)
	historyIndexMatchesExecutionRecords(t, a)
	var writes int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM history_index_writes WHERE plan_id=?`, first.ID).Scan(&writes); err != nil || writes != 1 {
		t.Fatalf("changed plan rebuilt %d times: %v", writes, err)
	}
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM history_index_writes WHERE plan_id!=?`, first.ID).Scan(&writes); err != nil || writes != 0 {
		t.Fatalf("unrelated plans were rebuilt: %d %v", writes, err)
	}
	// A rolled-back cancellation must not leak into the index or queue.
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := txPut(ctx, tx, "organize_file_cancel", job.ID+"/"+first.Files[0].ID, struct{}{}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM transfer_history_dirty`).Scan(&writes); err != nil || writes != 0 {
		t.Fatalf("rolled-back cancellation left dirty rows: %d %v", writes, err)
	}
	if err := a.Store.Put(ctx, "organize_file_cancel", job.ID+"/"+first.Files[0].ID, struct{}{}); err != nil {
		t.Fatal(err)
	}
	historyIndexMatchesExecutionRecords(t, a)
	callback := originalTransferCallback{PlanID: first.ID, ParentFileID: first.Files[0].ID, JobID: job.ID, File: first.Files[0]}
	callback.File.Status = "imported"
	if err := a.Store.Put(ctx, "original_transfer_callback", first.ID+"/"+first.Files[0].ID, callback); err != nil {
		t.Fatal(err)
	}
	historyIndexMatchesExecutionRecords(t, a)
	// An auxiliary callback belongs to the primary plan but has its own file
	// identity/history. Bounding the callback lookup must retain that row.
	callback.File.ID = "subtitle-file"
	callback.File.Source = filepath.Join(source, "one.srt")
	callback.File.Target = filepath.Join(target, "one.srt")
	if err := a.Store.Put(ctx, "original_transfer_callback", first.ID+"/subtitle-file", callback); err != nil {
		t.Fatal(err)
	}
	historyIndexMatchesExecutionRecords(t, a)
	var auxiliary int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM transfer_history_attempts WHERE plan_id=? AND execution_file_id='subtitle-file' AND source_path=? AND status='success'`, first.ID, callback.File.Source).Scan(&auxiliary); err != nil || auxiliary != 1 {
		t.Fatalf("auxiliary history lost by bounded refresh: %d %v", auxiliary, err)
	}
	for _, query := range []string{
		`DELETE FROM records WHERE kind='original_transfer_callback'`,
		`DELETE FROM jobs WHERE id='` + job.ID + `'`,
		`DELETE FROM records WHERE kind='import_plan'`,
	} {
		if _, err := a.Store.DB.Exec(query); err != nil {
			t.Fatal(err)
		}
		historyIndexMatchesExecutionRecords(t, a)
	}
}

func TestHistoryIndexUpgradeAndRestartPreserveStableHistory(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the previous schema: the upgrade must backfill old executions,
	// including writes made before the dirty-marker triggers existed.
	for _, name := range []string{"records_insert", "records_update", "records_delete", "jobs_insert", "jobs_update", "jobs_delete"} {
		if _, err := s.DB.Exec(`DROP TRIGGER transfer_history_` + name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.Exec(`DELETE FROM transfer_history_index_version`); err != nil {
		t.Fatal(err)
	}
	a := &App{Store: s}
	plan, job := historyParityPlan(t, a, filepath.Join(dir, "source"), filepath.Join(dir, "target"), "legacy", "Legacy", "running", "ready", "old.mkv")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.Store = s
	historyIndexMatchesExecutionRecords(t, a)
	item, err := a.readIndexedTransferHistoryItem(context.Background(), transferHistoryIndexedCTE+transferHistorySelect)
	if err != nil || item.ID != plan.Files[0].ID || item.JobID != job.ID || item.JobState != "queued" {
		t.Fatalf("upgrade/recovery changed history identity or lost new job state: %+v %v", item, err)
	}
	before, _ := json.Marshal(item)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a.Store = s
	var dirty int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM transfer_history_dirty`).Scan(&dirty); err != nil || dirty != 0 {
		t.Fatalf("unchanged restart rebuilt all history: %d %v", dirty, err)
	}
	afterItem, err := a.readIndexedTransferHistoryItem(context.Background(), transferHistoryIndexedCTE+transferHistorySelect)
	after, _ := json.Marshal(afterItem)
	if err != nil || string(before) != string(after) {
		t.Fatalf("unchanged restart changed visible history: %v", err)
	}
}

func TestHistoryRefreshBatchDrainsDirtyPlansInBoundedSteps(t *testing.T) {
	a, _, _ := importTestApp(t)
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		if _, err := a.Store.DB.Exec(`INSERT INTO transfer_history_dirty(plan_id) VALUES(?)`, "batch-plan-"+strings.Repeat("0", 2)+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	steps := 0
	for {
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		remaining, err := refreshTransferHistoryIndexBatch(ctx, tx, 5)
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		steps++
		if !remaining {
			break
		}
		if steps > 20 {
			t.Fatal("batch refresh did not converge")
		}
	}
	if steps < 3 {
		t.Fatalf("expected at least 3 batches for 12 plans at limit 5, got %d", steps)
	}
	var left int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM transfer_history_dirty`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("dirty plans left: %d", left)
	}
}
