package app

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func transferHistoryQualifiedIndexColumns(alias string) string {
	columns := strings.Split(transferHistoryIndexColumns, ",")
	for i, column := range columns {
		columns[i] = alias + "." + strings.TrimSpace(column)
	}
	return strings.Join(columns, ",")
}

// Upgrade old installations once, before serving requests or recovering jobs.
// Execution receipts remain unchanged. Current rows gain their own display
// snapshots, and all later writes maintain them in the originating transaction.
func initializeTransferHistory(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, name := range []string{"records", "hidden", "jobs"} {
		for _, event := range []string{"insert", "update", "delete"} {
			if _, err = tx.ExecContext(ctx, "DROP TRIGGER IF EXISTS transfer_history_"+name+"_"+event); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, "DROP TRIGGER IF EXISTS transfer_history_write_plan"); err != nil {
		return err
	}
	for _, column := range []string{"plan_json", "file_json"} {
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('transfer_history_current') WHERE name=?)`, column).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if _, err = tx.ExecContext(ctx, "ALTER TABLE transfer_history_current ADD COLUMN "+column+" TEXT"); err != nil {
				return err
			}
		}
	}
	var migrated bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM transfer_history_index_version WHERE version=3)`).Scan(&migrated); err != nil {
		return err
	}
	if !migrated {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO transfer_history_dirty SELECT id FROM records WHERE kind='import_plan';
   INSERT OR IGNORE INTO transfer_history_dirty_sources SELECT resource_source FROM transfer_history_attempts`); err != nil {
			return err
		}
	}
	if err = refreshTransferHistoryIndex(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO transfer_history_index_version VALUES(3)`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, transferHistoryWriteSchema()); err != nil {
		return err
	}
	return tx.Commit()
}

func transferHistorySyncSourcesSQL() string {
	return `DELETE FROM transfer_history_current WHERE resource_source IN (SELECT resource_source FROM transfer_history_dirty_sources);` +
		transferHistoryCurrentRefreshSQL("transfer_history_dirty_sources") + `; DELETE FROM transfer_history_dirty_sources;`
}

func transferHistoryWriteSchema() string {
	var s strings.Builder
	// A plan's old and new sources are collected before selecting current rows.
	// Consuming old sources earlier would leave a stale row after a path change.
	s.WriteString(`CREATE TRIGGER transfer_history_write_plan AFTER INSERT ON transfer_history_dirty BEGIN
 INSERT INTO transfer_history_dirty_sources SELECT resource_source FROM transfer_history_attempts WHERE plan_id=NEW.plan_id ON CONFLICT(resource_source) DO NOTHING;
 DELETE FROM transfer_history_attempts WHERE plan_id=NEW.plan_id;`)
	s.WriteString(transferHistoryRefreshSQL("(SELECT NEW.plan_id AS plan_id)") + ";")
	s.WriteString(`INSERT INTO transfer_history_dirty_sources SELECT resource_source FROM transfer_history_attempts WHERE plan_id=NEW.plan_id ON CONFLICT(resource_source) DO NOTHING;`)
	s.WriteString(transferHistorySyncSourcesSQL())
	s.WriteString(`DELETE FROM transfer_history_dirty WHERE plan_id=NEW.plan_id; END;`)
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		refs := []string{"NEW"}
		if event == "DELETE" {
			refs = []string{"OLD"}
		} else if event == "UPDATE" {
			refs = []string{"OLD", "NEW"}
		}
		var plans, jobs, recordsWhen, hiddenWhen, jobsWhen []string
		for _, ref := range refs {
			plans = append(plans, fmt.Sprintf(`SELECT CASE
    WHEN %[1]s.kind IN ('import_plan','import_meta') THEN %[1]s.id
    WHEN %[1]s.kind='original_transfer_callback' THEN json_extract(%[1]s.payload,'$.plan_id')
    WHEN %[1]s.kind='organize_file_cancel' THEN
     (SELECT json_extract(payload,'$.id') FROM jobs WHERE id=substr(%[1]s.id,1,instr(%[1]s.id,'/')-1) AND kind='import')
    END AS plan_id`, ref))
			jobs = append(jobs, fmt.Sprintf(`SELECT json_extract(%[1]s.payload,'$.id') AS plan_id WHERE %[1]s.kind='import'`, ref))
			recordsWhen = append(recordsWhen, ref+`.kind IN ('import_plan','import_meta','original_transfer_callback','organize_file_cancel')`)
			hiddenWhen = append(hiddenWhen, ref+`.kind='transfer_history_hidden'`)
			jobsWhen = append(jobsWhen, ref+`.kind='import'`)
		}
		// UNION materializes a unique set: OLD and NEW often identify the same plan.
		mark := func(selects []string) string {
			return `INSERT INTO transfer_history_dirty SELECT plan_id FROM (` + strings.Join(selects, " UNION ") + `) affected
 WHERE plan_id IS NOT NULL AND plan_id!='' AND (
 EXISTS(SELECT 1 FROM transfer_history_attempts a WHERE a.plan_id=affected.plan_id) OR
 EXISTS(SELECT 1 FROM jobs j WHERE j.kind='import' AND json_extract(j.payload,'$.id')=affected.plan_id))
 ON CONFLICT(plan_id) DO NOTHING;`
		}
		fmt.Fprintf(&s, "CREATE TRIGGER transfer_history_records_%s AFTER %s ON records WHEN %s BEGIN %s END;", strings.ToLower(event), event, strings.Join(recordsWhen, " OR "), mark(plans))
		updateOf := ""
		if event == "UPDATE" {
			updateOf = " OF kind,payload,state,error_message,created_at"
		}
		fmt.Fprintf(&s, "CREATE TRIGGER transfer_history_jobs_%s AFTER %s%s ON jobs WHEN %s BEGIN %s END;", strings.ToLower(event), event, updateOf, strings.Join(jobsWhen, " OR "), mark(jobs))
		fmt.Fprintf(&s, "CREATE TRIGGER transfer_history_hidden_%s AFTER %s ON records WHEN %s BEGIN ", strings.ToLower(event), event, strings.Join(hiddenWhen, " OR "))
		for _, ref := range refs {
			fmt.Fprintf(&s, `INSERT INTO transfer_history_dirty_sources SELECT resource_source FROM transfer_history_attempts WHERE execution_file_id=%[1]s.id AND %[1]s.kind='transfer_history_hidden' ON CONFLICT(resource_source) DO NOTHING;`, ref)
		}
		s.WriteString(transferHistorySyncSourcesSQL() + " END;")
	}
	return s.String()
}
