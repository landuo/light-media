package app

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// This is a rebuildable index of submitted attempts, not another history
// authority. Only dirty source groups are ranked; page reads hydrate the
// selected current rows from the original execution records.
const transferHistoryIndexColumns = `plan_id,file_json_path,callback_id,updated_at,
 job_id,job_state,job_error,executed_at,execution_key,file_id,execution_file_id,
 plan_created_at,title,source_path,target_path,resource_source,status,first_execution_key,file_rank`

const transferHistoryIndexedBase = `WITH attempts AS (SELECT * FROM transfer_history_attempts) `
const transferHistoryIndexedCTE = transferHistoryIndexedBase + `, history AS (SELECT * FROM transfer_history_current) `
const transferHistoryIndexedBySource = `WITH attempts AS (SELECT * FROM transfer_history_attempts WHERE resource_source=?1), history AS (SELECT * FROM transfer_history_current WHERE resource_source=?1) ` + transferHistorySelect
const transferHistoryIndexedBySourceFile = transferHistoryIndexedBySource + ` WHERE EXISTS (SELECT 1 FROM attempts WHERE execution_file_id=?2)`

func transferHistoryIndexSchema() string {
	var s strings.Builder
	s.WriteString(`
CREATE TABLE IF NOT EXISTS transfer_history_attempts (
 plan_id TEXT NOT NULL,file_json_path TEXT,callback_id TEXT,updated_at TEXT,
 job_id TEXT,job_state TEXT,job_error TEXT,executed_at TEXT,execution_key TEXT,
 file_id TEXT NOT NULL,execution_file_id TEXT,plan_created_at TEXT,title TEXT,
 source_path TEXT,target_path TEXT,resource_source TEXT,status TEXT,
 first_execution_key TEXT,file_rank INTEGER,PRIMARY KEY(plan_id,file_id));
CREATE INDEX IF NOT EXISTS transfer_history_source ON transfer_history_attempts(resource_source);
CREATE INDEX IF NOT EXISTS transfer_history_file ON transfer_history_attempts(execution_file_id);
CREATE INDEX IF NOT EXISTS transfer_history_callback_plan ON records(json_extract(payload,'$.plan_id')) WHERE kind='original_transfer_callback';
CREATE TABLE IF NOT EXISTS transfer_history_dirty(plan_id TEXT PRIMARY KEY NOT NULL);
CREATE TABLE IF NOT EXISTS transfer_history_index_version(version INTEGER PRIMARY KEY);
INSERT OR IGNORE INTO transfer_history_dirty SELECT id FROM records WHERE kind='import_plan'
 AND NOT EXISTS(SELECT 1 FROM transfer_history_index_version WHERE version=1);
INSERT OR IGNORE INTO transfer_history_index_version VALUES(1);
CREATE TABLE IF NOT EXISTS transfer_history_current AS SELECT *,
 CAST('' AS TEXT) AS history_id,CAST('' AS TEXT) AS history_created_at
 FROM transfer_history_attempts WHERE 0;
CREATE UNIQUE INDEX IF NOT EXISTS transfer_history_current_source ON transfer_history_current(resource_source);
CREATE INDEX IF NOT EXISTS transfer_history_current_order ON transfer_history_current(updated_at DESC,execution_key DESC,history_id);
CREATE INDEX IF NOT EXISTS transfer_history_current_status_order ON transfer_history_current(status,updated_at DESC,execution_key DESC,history_id);
CREATE INDEX IF NOT EXISTS transfer_history_current_id ON transfer_history_current(history_id);
CREATE TABLE IF NOT EXISTS transfer_history_dirty_sources(resource_source TEXT PRIMARY KEY NOT NULL);
INSERT OR IGNORE INTO transfer_history_dirty_sources SELECT resource_source FROM transfer_history_attempts
 WHERE NOT EXISTS(SELECT 1 FROM transfer_history_index_version WHERE version=2);
INSERT OR IGNORE INTO transfer_history_index_version VALUES(2);
`)
	// Dirty markers commit and roll back with the original write, including
	// callback completion, cancellation, recovery and maintenance deletions.
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		refs := []string{"NEW"}
		if event == "DELETE" {
			refs = []string{"OLD"}
		} else if event == "UPDATE" {
			refs = []string{"OLD", "NEW"}
		}
		fmt.Fprintf(&s, "CREATE TRIGGER IF NOT EXISTS transfer_history_records_%s AFTER %s ON records BEGIN\n", strings.ToLower(event), event)
		for _, ref := range refs {
			fmt.Fprintf(&s, `INSERT INTO transfer_history_dirty SELECT plan_id FROM (
 SELECT CASE
 WHEN %[1]s.kind IN ('import_plan','import_meta') THEN %[1]s.id
 WHEN %[1]s.kind='original_transfer_callback' THEN json_extract(%[1]s.payload,'$.plan_id')
 WHEN %[1]s.kind='organize_file_cancel' THEN
 (SELECT json_extract(payload,'$.id') FROM jobs WHERE id=substr(%[1]s.id,1,instr(%[1]s.id,'/')-1) AND kind='import')
 END AS plan_id) WHERE plan_id IS NOT NULL AND plan_id!='' ON CONFLICT(plan_id) DO NOTHING;
`, ref)
		}
		s.WriteString("END;\n")
		// Hidden markers can change visibility without changing any plan/job.
		fmt.Fprintf(&s, "CREATE TRIGGER IF NOT EXISTS transfer_history_hidden_%s AFTER %s ON records BEGIN\n", strings.ToLower(event), event)
		for _, ref := range refs {
			fmt.Fprintf(&s, `INSERT INTO transfer_history_dirty_sources
 SELECT resource_source FROM transfer_history_attempts WHERE execution_file_id=%[1]s.id AND %[1]s.kind='transfer_history_hidden' ON CONFLICT(resource_source) DO NOTHING;
`, ref)
		}
		s.WriteString("END;\n")
		updateOf := ""
		if event == "UPDATE" {
			updateOf = " OF kind,payload,state,error_message,created_at"
		}
		fmt.Fprintf(&s, "CREATE TRIGGER IF NOT EXISTS transfer_history_jobs_%s AFTER %s%s ON jobs BEGIN\n", strings.ToLower(event), event, updateOf)
		for _, ref := range refs {
			fmt.Fprintf(&s, `INSERT INTO transfer_history_dirty SELECT json_extract(%[1]s.payload,'$.id')
 WHERE %[1]s.kind='import' AND COALESCE(json_extract(%[1]s.payload,'$.id'),'')!='' ON CONFLICT(plan_id) DO NOTHING;
`, ref)
		}
		s.WriteString("END;\n")
	}
	return s.String()
}

const transferHistoryDirtySQL = `SELECT EXISTS(SELECT 1 FROM transfer_history_dirty) OR EXISTS(SELECT 1 FROM transfer_history_dirty_sources)`

func refreshTransferHistoryIndex(ctx context.Context, tx *sql.Tx) error {
	var dirty bool
	if err := tx.QueryRowContext(ctx, transferHistoryDirtySQL).Scan(&dirty); err != nil || !dirty {
		return err
	}
	// Capture both old and new source identities. A source change or removal
	// must also remove/reselect the previous visible group.
	const markSources = `INSERT OR IGNORE INTO transfer_history_dirty_sources
 SELECT resource_source FROM transfer_history_attempts WHERE plan_id IN (SELECT plan_id FROM transfer_history_dirty)`
	for _, query := range []string{
		markSources,
		`DELETE FROM transfer_history_attempts WHERE plan_id IN (SELECT plan_id FROM transfer_history_dirty)`,
		transferHistoryRefreshSQL(),
		markSources,
		`DELETE FROM transfer_history_current WHERE resource_source IN (SELECT resource_source FROM transfer_history_dirty_sources)`,
		transferHistoryCurrentRefreshSQL(),
		`DELETE FROM transfer_history_dirty`,
		`DELETE FROM transfer_history_dirty_sources`,
	} {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func transferHistoryCurrentRefreshSQL() string {
	// Seek only changed source groups. Hidden markers use the records primary
	// key rather than materializing every marker on each incremental refresh.
	ranked := strings.Replace(transferHistoryRanked, "hidden_history AS MATERIALIZED", "hidden_history AS NOT MATERIALIZED", 1)
	return `WITH attempts AS (SELECT * FROM transfer_history_attempts WHERE resource_source IN
 (SELECT resource_source FROM transfer_history_dirty_sources)) ` + ranked +
		`INSERT INTO transfer_history_current (` + transferHistoryIndexColumns + `,history_id,history_created_at)
 SELECT ` + transferHistoryIndexColumns + `,history_id,history_created_at FROM history`
}

func transferHistoryRefreshSQL() string {
	const changed = ` AND r.id IN (SELECT plan_id FROM transfer_history_dirty)`
	// Bound both sides of the join. Filtering only r.id lets SQLite walk every
	// archived import job before finding the few dirty plans, especially with a
	// cold disk cache. jobs_subject can seek kind + plan ID directly.
	jobs := transferHistoryJobsSelect + ` AND json_extract(payload,'$.id') IN (SELECT plan_id FROM transfer_history_dirty))`
	// SQLite can otherwise start the auxiliary join at every callback, even
	// when only one plan is dirty. Bound that side too and seek its plan index.
	auxiliary := strings.Replace(transferHistoryAuxiliaryFiles, "JOIN records cb ON", "JOIN records cb INDEXED BY transfer_history_callback_plan ON", 1)
	return jobs + transferHistoryExecutedFiles + changed +
		auxiliary + changed + ` AND json_extract(cb.payload,'$.plan_id') IN (SELECT plan_id FROM transfer_history_dirty)` + transferHistoryAttempts +
		`INSERT INTO transfer_history_attempts (` + transferHistoryIndexColumns + `) SELECT ` + transferHistoryIndexColumns + ` FROM attempts`
}

// Validate dirtiness inside the read snapshot used for hydration. After a
// refresh commits, a concurrent writer may invalidate the projection again;
// retry rather than combining old projected state with newer execution JSON.
func (a *App) transferHistoryReadTx(ctx context.Context) (*sql.Tx, error) {
	phase := a.performanceStages(ctx, "history_snapshot", "reader_wait")
	defer phase("")
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if a.Store.readDB != nil {
			phase("reader_wait")
			read, err := a.Store.reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				return nil, err
			}
			phase("dirty_check")
			var dirty bool
			err = read.QueryRowContext(ctx, transferHistoryDirtySQL).Scan(&dirty)
			if err == nil && !dirty {
				return read, nil
			}
			read.Rollback()
			if err != nil {
				return nil, err
			}
		}
		phase("writer_wait")
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		phase("index_refresh")
		if err = refreshTransferHistoryIndex(ctx, tx); err != nil {
			tx.Rollback()
			return nil, err
		}
		if a.Store.readDB == nil {
			return tx, nil
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
	}
}

func (a *App) readIndexedTransferHistoryItem(ctx context.Context, query string, args ...any) (transferHistoryItem, error) {
	tx, err := a.transferHistoryReadTx(ctx)
	if err != nil {
		return transferHistoryItem{}, err
	}
	defer tx.Rollback()
	item, err := scanTransferHistory(tx.QueryRowContext(ctx, query, args...))
	if err != nil && err != sql.ErrNoRows {
		return item, err
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return item, commitErr
	}
	return item, err
}
