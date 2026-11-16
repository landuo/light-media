package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// migrateTransferHistoryMP copies the latest legacy projection into the
// MoviePilot-shaped single table, then removes the obsolete projection tables.
// The operation is idempotent and only runs when the legacy table exists.
func migrateTransferHistoryMP(ctx context.Context, db *sql.DB) error {
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='transfer_history_current'`).Scan(&exists); err != nil {
		return err
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transferhistory`).Scan(&count); err != nil {
		return err
	}
	if exists > 0 && count == 0 {
		rows, err := db.QueryContext(ctx, `SELECT plan_id,file_id,plan_json,file_json,status,updated_at,source_path,target_path FROM transfer_history_current WHERE status!='pending' ORDER BY updated_at`)
		if err != nil {
			return err
		}
		type legacy struct {
			planID, fileID, planJSON, fileJSON, status, updated, source, target string
		}
		var items []legacy
		for rows.Next() {
			var item legacy
			if err := rows.Scan(&item.planID, &item.fileID, &item.planJSON, &item.fileJSON, &item.status, &item.updated, &item.source, &item.target); err != nil {
				rows.Close()
				return err
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, item := range items {
			var plan ImportPlan
			var file PlanFile
			if json.Unmarshal([]byte(item.planJSON), &plan) != nil || json.Unmarshal([]byte(item.fileJSON), &file) != nil {
				continue
			}
			file.ID, file.Source, file.Target = item.fileID, item.source, item.target
			plan.ID = item.planID
			if err := writeTransferHistoryMPTx(ctx, tx, transferHistoryMPInput{Plan: plan, File: file, Job: Job{ID: "migrated"}, Success: item.status == "success"}); err != nil {
				tx.Rollback()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	triggerRows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='trigger' AND name LIKE 'transfer_history_%'`)
	if err != nil {
		return err
	}
	var triggers []string
	for triggerRows.Next() {
		var name string
		if err = triggerRows.Scan(&name); err != nil {
			triggerRows.Close()
			return err
		}
		triggers = append(triggers, name)
	}
	if err = triggerRows.Err(); err != nil {
		triggerRows.Close()
		return err
	}
	triggerRows.Close()
	for _, trigger := range triggers {
		if _, err := db.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+trigger); err != nil {
			return err
		}
	}
	tableRows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND (name='transfer_history' OR name LIKE 'transfer_history_%')`)
	if err != nil {
		return err
	}
	var tables []string
	for tableRows.Next() {
		var name string
		if err = tableRows.Scan(&name); err != nil {
			tableRows.Close()
			return err
		}
		tables = append(tables, name)
	}
	if err = tableRows.Err(); err != nil {
		tableRows.Close()
		return err
	}
	tableRows.Close()
	for _, table := range tables {
		if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			return err
		}
	}
	return nil
}

var _ = errors.Is
