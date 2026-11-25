package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Visible history gates both successful and failed transfers. Dropping a history
// row is not a new filesystem event for an unchanged, already observed source.
func directoryHistoryObservation(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, file *DirectoryObservedFile) (bool, error) {
	var status, planID, jobID, reason string
	var state int
	err := db.QueryRowContext(ctx, `SELECT status,COALESCE(json_extract(src_fileitem,'$.plan_id'),''),COALESCE(json_extract(src_fileitem,'$.job_id'),''),COALESCE(errmsg,'') FROM transferhistory WHERE src=? ORDER BY id DESC LIMIT 1`, file.Path).Scan(&state, &planID, &jobID, &reason)
	if err == nil && state == 1 {
		status = "success"
	} else if err == nil {
		status = "failed"
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	file.PlanID, file.JobID = planID, jobID
	switch status {
	case "success":
		file.State = "imported"
	case "failed":
		file.State = "error"
	default:
		file.State = "queued"
	}
	file.Reason = reason
	if file.Reason == "" {
		file.Reason = "已有整理历史，自动整理跳过"
	}
	return true, nil
}

// The identity comes from the selected execution or the monitor's observation.
// Deleting a history row never needs to stat its source (which may be offline).
func directoryDeletedSourceStamp(ctx context.Context, tx *sql.Tx, item transferHistoryItem) (*importSnapshot, error) {
	if item.PlanID != "" {
		var raw []byte
		err := tx.QueryRowContext(ctx, `SELECT f.value FROM import_plan p,json_each(p.payload,'$.files') f
WHERE p.id=? AND json_extract(f.value,'$.id')=? AND json_extract(f.value,'$.source')=?`, item.PlanID, item.FileID, item.Source).Scan(&raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		var file PlanFile
		if err == nil && json.Unmarshal(raw, &file) == nil && file.Inode != 0 {
			s := planSnapshot(file)
			return &s, nil
		}
	}
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT json_extract(payload,'$.snapshot') FROM directory_file
WHERE path=? AND json_extract(payload,'$.snapshot.inode')>0 ORDER BY updated_at DESC LIMIT 1`, item.Source).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var observed importSnapshot
	if err = json.Unmarshal(raw, &observed); err != nil {
		return nil, err
	}
	return &observed, nil
}

func lastDeletedDirectorySnapshot(ctx context.Context, db qualityReader, path string) (*importSnapshot, error) {
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT json_extract(payload,'$.source_snapshot') FROM history_hidden
WHERE json_extract(payload,'$.source')=? AND json_type(payload,'$.source_snapshot')='object'
ORDER BY updated_at DESC, id DESC LIMIT 1`, path).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var consumed importSnapshot
	if err = json.Unmarshal(raw, &consumed); err != nil {
		return nil, err
	}
	return &consumed, nil
}

func directorySourceHistoryDeleted(ctx context.Context, db qualityReader, path string, stamp importSnapshot) (bool, error) {
	if stamp.Inode == 0 {
		return false, nil
	}
	prior, err := lastDeletedDirectorySnapshot(ctx, db, path)
	return prior != nil && *prior == stamp, err
}

func (f *DirectoryObservedFile) retainDeletedHistory() {
	f.PlanID, f.JobID = "", ""
	f.State, f.Reason = "complete", "整理记录已删除，源文件未变化"
}

// Older deletion receipts carry the execution and file IDs but no path. Restore
// the original source identity once, using only those exact historical plans.
func migrateDeletedDirectorySources(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var pending bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM history_hidden WHERE json_type(payload,'$.source') IS NULL)`).Scan(&pending); err != nil {
		return err
	}
	if !pending {
		return tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `WITH selected AS MATERIALIZED (
 SELECT h.id, f.value AS file
 FROM history_hidden h JOIN import_plan p ON p.id=json_extract(h.payload,'$.plan_id')
 JOIN json_each(p.payload,'$.files') f ON json_extract(f.value,'$.id')=json_extract(h.payload,'$.file_id')
 WHERE json_type(h.payload,'$.source') IS NULL AND json_extract(f.value,'$.source')!='' AND json_extract(f.value,'$.inode')>0
)
UPDATE history_hidden AS h SET payload=json_set(h.payload,'$.source',json_extract(s.file,'$.source'),'$.source_snapshot',
json_object('device',json_extract(s.file,'$.device'),'inode',json_extract(s.file,'$.inode'),'size',json_extract(s.file,'$.size'),
'mod_ns',json_extract(s.file,'$.mod_ns'),'directory',coalesce(json_extract(s.file,'$.directory_digest'),'')))
FROM selected s WHERE h.id=s.id`)
	if err != nil {
		return err
	}
	// Rows whose original plan was removed have no attributable file.
	// A JSON null distinguishes those checked rows from fresh legacy writes.
	if _, err = tx.ExecContext(ctx, `UPDATE history_hidden SET payload=json_set(payload,'$.source',NULL)
WHERE json_type(payload,'$.source') IS NULL`); err != nil {
		return err
	}
	return tx.Commit()
}
