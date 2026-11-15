package app

import (
	"context"
	"database/sql"
	"strings"
)

// Execution references preserve exact cancellation and old-ID aliases. Current
// histories save their own display snapshots with the execution transaction;
// only the changed source groups are ranked, never the whole list on a read.
const transferHistoryIndexColumns = `plan_id,file_json_path,callback_id,updated_at,
 job_id,job_state,job_error,executed_at,execution_key,file_id,execution_file_id,
 plan_created_at,title,source_path,target_path,resource_source,status,first_execution_key,file_rank`

const transferHistoryIndexedBase = `WITH attempts AS (SELECT * FROM transfer_history_attempts) `
const transferHistoryIndexedCTE = transferHistoryIndexedBase + `, history AS (SELECT * FROM transfer_history_current) `
const transferHistoryIndexedBySource = `WITH attempts AS (SELECT * FROM transfer_history_attempts WHERE resource_source=?1), history AS (SELECT * FROM transfer_history_current WHERE resource_source=?1) ` + transferHistoryStoredSelect
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
 CAST('' AS TEXT) AS history_id,CAST('' AS TEXT) AS history_created_at,
 CAST(NULL AS TEXT) AS plan_json,CAST(NULL AS TEXT) AS file_json
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
	return s.String()
}

const transferHistoryDirtySQL = `SELECT EXISTS(SELECT 1 FROM transfer_history_dirty) OR EXISTS(SELECT 1 FROM transfer_history_dirty_sources)`

// Used only while upgrading an existing store, before write triggers are installed.
func refreshTransferHistoryIndex(ctx context.Context, tx *sql.Tx) error {
	var dirty bool
	if err := tx.QueryRowContext(ctx, transferHistoryDirtySQL).Scan(&dirty); err != nil || !dirty {
		return err
	}
	markSources := `INSERT OR IGNORE INTO transfer_history_dirty_sources SELECT resource_source FROM transfer_history_attempts WHERE plan_id IN (SELECT plan_id FROM transfer_history_dirty)`
	for _, query := range []string{
		markSources,
		`DELETE FROM transfer_history_attempts WHERE plan_id IN (SELECT plan_id FROM transfer_history_dirty)`,
		transferHistoryRefreshSQL("transfer_history_dirty"),
		markSources,
		transferHistorySyncSourcesSQL(),
		`DELETE FROM transfer_history_dirty`,
	} {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func transferHistoryCurrentRefreshSQL(dirtySources string) string {
	// Seek only changed source groups. Hidden markers use the records primary
	// key rather than materializing every marker on each incremental refresh.
	ranked := strings.Replace(transferHistoryRanked, "hidden_history AS MATERIALIZED", "hidden_history AS NOT MATERIALIZED", 1)
	return `INSERT INTO transfer_history_current (` + transferHistoryIndexColumns + `,history_id,history_created_at,plan_json,file_json)
 SELECT * FROM (WITH attempts AS (SELECT * FROM transfer_history_attempts WHERE resource_source IN
 (SELECT resource_source FROM ` + dirtySources + `)) ` + ranked +
		`SELECT ` + transferHistoryQualifiedIndexColumns("h") + `,h.history_id,h.history_created_at,` + transferHistoryJSON + ` FROM history h` + transferHistoryHydrate + `)`
}

func transferHistoryRefreshSQL(dirtyTable string) string {
	changed := ` AND r.id IN (SELECT plan_id FROM ` + dirtyTable + `)`
	// Bound both sides of the join. Filtering only r.id lets SQLite walk every
	// archived import job before finding the few dirty plans, especially with a
	// cold disk cache. jobs_subject can seek kind + plan ID directly.
	jobs := transferHistoryJobsSelect + ` AND json_extract(payload,'$.id') IN (SELECT plan_id FROM ` + dirtyTable + `))`
	// SQLite can otherwise start the auxiliary join at every callback, even
	// when only one plan is dirty. Bound that side too and seek its plan index.
	auxiliary := strings.Replace(transferHistoryAuxiliaryFiles, "JOIN records cb ON", "JOIN records cb INDEXED BY transfer_history_callback_plan ON", 1)
	return `INSERT INTO transfer_history_attempts (` + transferHistoryIndexColumns + `) SELECT * FROM (` + jobs + transferHistoryExecutedFiles + changed +
		auxiliary + changed + ` AND json_extract(cb.payload,'$.plan_id') IN (SELECT plan_id FROM ` + dirtyTable + `)` + transferHistoryAttempts +
		`SELECT ` + transferHistoryIndexColumns + ` FROM attempts)`
}

// History is saved with the execution write. Page and detail reads never
// acquire the writer, rebuild history, or return a partial warm-up result.
func (a *App) transferHistoryReadTx(ctx context.Context) (*sql.Tx, error) {
	return a.Store.reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
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
