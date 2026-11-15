package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
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
	// Inspect the committed rows directly: using a refresh-capable application
	// helper here would hide a missing write-side trigger.
	tx, err := a.Store.reader().BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
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
	for _, query := range []string{
		transferHistoryCTE + `SELECT COUNT(*) FROM (` + transferHistorySelect + ` EXCEPT SELECT ` + transferHistoryStoredColumns + ` FROM transfer_history_current h)`,
		transferHistoryCTE + `SELECT COUNT(*) FROM (SELECT ` + transferHistoryStoredColumns + ` FROM transfer_history_current h EXCEPT ` + transferHistorySelect + `)`,
	} {
		var differences int
		if err := tx.QueryRow(query).Scan(&differences); err != nil || differences != 0 {
			t.Fatalf("stored display snapshots differ from execution records: %d %v", differences, err)
		}
	}
	var dirty bool
	if err := tx.QueryRow(transferHistoryDirtySQL).Scan(&dirty); err != nil || dirty {
		t.Fatalf("committed execution write left deferred history work: %v %v", dirty, err)
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
	// The write is already reflected without an intervening history GET.
	historyIndexMatchesExecutionRecords(t, a)
	var writes int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM history_index_writes WHERE plan_id=?`, first.ID).Scan(&writes); err != nil || writes != 1 {
		t.Fatalf("one plan write must refresh once, not once for OLD and NEW: %d %v", writes, err)
	}
	if err := a.Store.Put(ctx, "unrelated_fixture", "same", map[string]string{"name": "not history"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='running' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	historyIndexMatchesExecutionRecords(t, a)
	for range 2 {
		historyParityReadPage(t, a, "capabilities=deferred")
	}
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM history_index_writes WHERE plan_id=?`, first.ID).Scan(&writes); err != nil || writes != 2 {
		t.Fatalf("plan and job writes must each refresh once; GET must not refresh: %d %v", writes, err)
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
	var status string
	if err := tx.QueryRow(`SELECT status FROM transfer_history_current WHERE plan_id=?`, first.ID).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("cancellation was not visible inside its transaction: %s %v", status, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.DB.QueryRow(`SELECT status FROM transfer_history_current WHERE plan_id=?`, first.ID).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("rolled-back cancellation changed durable history: %s %v", status, err)
	}
	historyIndexMatchesExecutionRecords(t, a)
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
	a := &App{Store: s}
	plan, job := historyParityPlan(t, a, filepath.Join(dir, "source"), filepath.Join(dir, "target"), "legacy", "Legacy", "running", "ready", "old.mkv")
	// Keep the populated v2 scalar index, but remove v3 snapshots and write
	// triggers. No dirty markers exist: upgrading must still backfill snapshots.
	for _, name := range []string{"records_insert", "records_update", "records_delete", "hidden_insert", "hidden_update", "hidden_delete", "jobs_insert", "jobs_update", "jobs_delete", "write_plan"} {
		if _, err := s.DB.Exec(`DROP TRIGGER transfer_history_` + name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.Exec(`DELETE FROM transfer_history_index_version WHERE version=3;
 ALTER TABLE transfer_history_current DROP COLUMN plan_json;
 ALTER TABLE transfer_history_current DROP COLUMN file_json;
 CREATE TABLE history_upgrade_writes(plan_id TEXT);
 CREATE TRIGGER observe_history_upgrade AFTER INSERT ON transfer_history_current
 BEGIN INSERT INTO history_upgrade_writes VALUES(NEW.plan_id); END;`); err != nil {
		t.Fatal(err)
	}
	var dirty bool
	if err := s.DB.QueryRow(transferHistoryDirtySQL).Scan(&dirty); err != nil || dirty {
		t.Fatalf("v2 fixture should start with a clean scalar index: %v %v", dirty, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.Store = s
	// Inspect storage before any application history read. OpenStore must have
	// committed both migration snapshots and running-job recovery already.
	var ready, version, migrationWrites int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM transfer_history_current WHERE history_id=? AND job_state='queued' AND json_extract(plan_json,'$.media.title')='Legacy' AND json_extract(file_json,'$.id')=?`, plan.Files[0].ID, plan.Files[0].ID).Scan(&ready); err != nil || ready != 1 {
		t.Fatalf("startup did not persist complete history before GET: %d %v", ready, err)
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM transfer_history_index_version WHERE version=3`).Scan(&version); err != nil || version != 1 {
		t.Fatalf("migration version was not committed: %d %v", version, err)
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM history_upgrade_writes`).Scan(&migrationWrites); err != nil || migrationWrites == 0 {
		t.Fatalf("v2 upgrade did not write snapshots: %d %v", migrationWrites, err)
	}
	historyIndexMatchesExecutionRecords(t, a)
	item, err := scanTransferHistory(s.DB.QueryRow(transferHistoryIndexedCTE + transferHistoryStoredSelect))
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
	var restartedWrites int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM history_upgrade_writes`).Scan(&restartedWrites); err != nil || restartedWrites != migrationWrites {
		t.Fatalf("unchanged restart rewrote history: before=%d after=%d %v", migrationWrites, restartedWrites, err)
	}
	historyIndexMatchesExecutionRecords(t, a)
	afterItem, err := scanTransferHistory(s.DB.QueryRow(transferHistoryIndexedCTE + transferHistoryStoredSelect))
	after, _ := json.Marshal(afterItem)
	if err != nil || string(before) != string(after) {
		t.Fatalf("unchanged restart changed visible history: %v", err)
	}
}

func TestHistoryWriteIsSynchronousAndGETIsReadOnly(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	plan, _ := historyParityPlan(t, a, source, target, "write-side", "Before", "failed", "error", "one.mkv")
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	plan.Media.Title = "Rolled back"
	if err := txPut(ctx, tx, "import_plan", plan.ID, plan); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := tx.QueryRow(`SELECT json_extract(plan_json,'$.media.title') FROM transfer_history_current WHERE plan_id=?`, plan.ID).Scan(&title); err != nil || title != "Rolled back" {
		t.Fatalf("history was not saved in the execution transaction: %q %v", title, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.DB.QueryRow(`SELECT json_extract(plan_json,'$.media.title') FROM transfer_history_current WHERE plan_id=?`, plan.ID).Scan(&title); err != nil || title != "Before" {
		t.Fatalf("rolled-back display snapshot leaked: %q %v", title, err)
	}
	plan.Media.Title = "Committed"
	if err := a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
		t.Fatal(err)
	}
	historyIndexMatchesExecutionRecords(t, a)

	// Disable writes on the sole writer as well as the normal read pool. Page
	// and detail GETs must still succeed without any refresh or scratch tables.
	if _, err := a.Store.DB.Exec(`PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := a.Store.DB.Exec(`PRAGMA query_only=OFF`); err != nil {
			t.Error(err)
		}
	}()
	page := historyParityReadPage(t, a, "capabilities=deferred")
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Media.Title != "Committed" {
		t.Fatalf("read-only GET lost committed history: %+v", page)
	}
	item, err := a.transferHistoryItem(ctx, plan.Files[0].ID)
	if err != nil || item.Media.Title != "Committed" {
		t.Fatalf("read-only detail GET failed: %+v %v", item, err)
	}
	if _, err := a.transferHistory(ctx, httptest.NewRequest("GET", "/api/history/transfers?status=failed&capabilities=deferred", nil)); err != nil {
		t.Fatal("filtered GET attempted to write:", err)
	}
}
