package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The storage split replaces the single records(kind,id,payload,updated_at)
// table with one table per feature. MoviePilot owns the shape of its tables;
// light-media keeps a payload column so the canonical record survives a partial
// migration. This file holds the one-shot version 1 -> 2 migration and the
// child-row materialisation shared by writes and the migration.

const recordsFrozenTable = "records_frozen_v1"

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func tableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	return n != 0, err
}

// recordChildren materialises the rows that used to live inside a payload array
// or object. Child tables mirror the parent so history, plan and receipt reads
// no longer decode a JSON array per request.
func recordChildren(ctx context.Context, q execer, kind, key string, payload []byte, updatedAt string) error {
	switch kind {
	case "import_plan":
		var plan struct {
			Files []struct {
				ID      string `json:"id"`
				Status  string `json:"status"`
				Source  string `json:"source"`
				Target  string `json:"target"`
				Size    int64  `json:"size"`
				AssetID string `json:"asset_id"`
			} `json:"files"`
		}
		if err := json.Unmarshal(payload, &plan); err != nil {
			return nil
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM import_plan_file WHERE plan_id=?`, key); err != nil {
			return err
		}
		for _, f := range plan.Files {
			if f.ID == "" {
				continue
			}
			row := map[string]any{"id": key + "/" + f.ID, "plan_id": key, "file_id": f.ID, "status": f.Status, "source": f.Source, "target": f.Target, "size": f.Size, "asset_id": f.AssetID}
			raw, err := json.Marshal(row)
			if err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, recordInsertSQL("import_plan_file", recordTableForKind("import_plan_file")), "import_plan_file:"+key+"/"+f.ID, raw, updatedAt); err != nil {
				return err
			}
		}
	case "import_receipt":
		var receipt struct {
			DirectoryMembers map[string]json.RawMessage `json:"directory_members"`
		}
		if err := json.Unmarshal(payload, &receipt); err != nil {
			return nil
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM import_receipt_member WHERE receipt_id=?`, key); err != nil {
			return err
		}
		for path, member := range receipt.DirectoryMembers {
			var m struct {
				AssetID string `json:"asset_id"`
			}
			_ = json.Unmarshal(member, &m)
			row := map[string]any{"id": key + "\x00" + path, "receipt_id": key, "path": path, "asset_id": m.AssetID}
			raw, err := json.Marshal(row)
			if err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, recordInsertSQL("import_receipt_member", recordTableForKind("import_receipt_member")), "import_receipt_member:"+key+"\x00"+path, raw, updatedAt); err != nil {
				return err
			}
		}
	}
	return nil
}

// recordTableForKind resolves a table name that is also a storage kind. Child
// tables are addressed directly, so the helper accepts their own names.
func recordTableForKind(name string) recordTable {
	if t, ok := recordTableFor(name); ok {
		return t
	}
	if t, ok := mpTables[name]; ok {
		t.cols = mpCols[name]
		return t
	}
	for _, t := range nativeTables {
		if t.name == name {
			return t
		}
	}
	return recordTable{}
}

// migrateRecordsV2 copies every v1 row into its feature table and freezes the
// source table. It is idempotent: rows are upserted by their historical key.
func migrateRecordsV2(ctx context.Context, db *sql.DB) error {
	present, err := tableExists(ctx, db, "records")
	if err != nil {
		return err
	}
	var version int
	if err = db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 2 || !present {
		if version < 2 {
			_, err = db.ExecContext(ctx, `INSERT OR IGNORE INTO migrations VALUES(2,?)`, now())
		}
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT kind,id,payload,updated_at FROM records`)
	if err != nil {
		return err
	}
	type row struct {
		kind, id, updatedAt string
		payload             []byte
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.kind, &r.id, &r.payload, &r.updatedAt); err != nil {
			rows.Close()
			return err
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	var archived int
	for _, r := range all {
		t, ok := recordTableFor(r.kind)
		if !ok {
			if _, err := tx.ExecContext(ctx, `INSERT INTO plugin_record(kind,id,payload,updated_at) VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, r.kind, r.id, r.payload, r.updatedAt); err != nil {
				return fmt.Errorf("archive %s/%s: %w", r.kind, r.id, err)
			}
			archived++
			continue
		}
		if _, err := tx.ExecContext(ctx, recordInsertSQL(r.kind, t), r.id, r.payload, r.updatedAt); err != nil {
			return fmt.Errorf("migrate %s/%s: %w", r.kind, r.id, err)
		}
		if err := recordChildren(ctx, tx, r.kind, r.id, r.payload, r.updatedAt); err != nil {
			return fmt.Errorf("children %s/%s: %w", r.kind, r.id, err)
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM records`).Scan(&count); err != nil {
		return err
	}
	if count != len(all) {
		return errors.New("records changed during migration")
	}
	if count == 0 {
		if _, err := tx.ExecContext(ctx, `DROP TABLE records`); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE records RENAME TO `+recordsFrozenTable); err != nil {
			return err
		}
		for _, idx := range []string{
			"organize_queue_source", "organize_queue_group", "organize_queue_job",
			"organize_queue_meta", "organize_queue_media", "managed_file_asset",
			"asset_path", "import_plan_state", "import_plan_intent", "asset_media_key",
			"subscription_media_key", "intent_media_key", "notification_display_order",
			"notification_unread", "subscription_display_order",
		} {
			_, _ = tx.ExecContext(ctx, `DROP INDEX IF EXISTS `+idx)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO migrations VALUES(2,?)`, now()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_ = archived
	return nil
}

// RecordsMigrationReport is the dry-run output of `migrate records --check`.
type RecordsMigrationReport struct {
	Version       int            `json:"version"`
	SourceRows    int            `json:"source_rows"`
	ArchivedRows  int            `json:"archived_rows"`
	Tables        map[string]int `json:"tables"`
	UnknownKinds  map[string]int `json:"unknown_kinds,omitempty"`
	SealedKinds   []string       `json:"sealed_kinds,omitempty"`
	CheckedAt     string         `json:"checked_at"`
	TargetMissing []string       `json:"target_missing,omitempty"`
	details       map[string]map[string]int
}

// CheckRecordsMigration reports how many rows每 kind would move and whether the
// target table exists. It never writes.
func CheckRecordsMigration(ctx context.Context, db *sql.DB) (RecordsMigrationReport, error) {
	report := RecordsMigrationReport{CheckedAt: time.Now().UTC().Format(time.RFC3339), Tables: map[string]int{}, UnknownKinds: map[string]int{}}
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM migrations`).Scan(&report.Version); err != nil {
		return report, err
	}
	present, err := tableExists(ctx, db, "records")
	if err != nil || !present {
		return report, err
	}
	rows, err := db.QueryContext(ctx, `SELECT kind,COUNT(*) FROM records GROUP BY kind ORDER BY kind`)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return report, err
		}
		report.SourceRows += n
		table, ok := recordKinds[kind]
		if !ok {
			report.UnknownKinds[kind] = n
			report.ArchivedRows += n
			continue
		}
		report.Tables[table] += n
	}
	if err := rows.Err(); err != nil {
		return report, err
	}
	if report.Version < 2 {
		// A v1 database has no target tables yet; their absence is expected and
		// the migration creates them.
		return report, nil
	}
	var names []string
	for name := range report.Tables {
		names = append(names, name)
	}
	for _, name := range names {
		exists, err := tableExists(ctx, db, name)
		if err != nil {
			return report, err
		}
		if !exists {
			report.TargetMissing = append(report.TargetMissing, name)
		}
	}
	for _, sealed := range []string{"site_message_collection", "subscription_acquisition", "download_season_scope", "download_episode_selection", "core_settings_lifecycle", "original_transfer_callback"} {
		if _, ok := recordKinds[sealed]; ok {
			report.SealedKinds = append(report.SealedKinds, sealed)
		}
	}
	return report, nil
}

func (r RecordsMigrationReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "schema version=%d source_rows=%d archived=%d targets=%d\n", r.Version, r.SourceRows, r.ArchivedRows, len(r.Tables))
	names := make([]string, 0, len(r.Tables))
	for name := range r.Tables {
		names = append(names, name)
	}
	sortStrings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "  %-32s %d\n", name, r.Tables[name])
	}
	if len(r.UnknownKinds) > 0 {
		b.WriteString("unknown kinds (archived):\n")
		unknown := make([]string, 0, len(r.UnknownKinds))
		for kind := range r.UnknownKinds {
			unknown = append(unknown, kind)
		}
		sortStrings(unknown)
		for _, kind := range unknown {
			fmt.Fprintf(&b, "  %-32s %d\n", kind, r.UnknownKinds[kind])
		}
	}
	if len(r.TargetMissing) > 0 {
		b.WriteString("missing target tables:\n")
		for _, name := range r.TargetMissing {
			fmt.Fprintf(&b, "  %s\n", name)
		}
	}
	return b.String()
}

func sortStrings(v []string) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

// recordChildrenDelete removes the materialised child rows that belong to a
// deleted parent record.
func recordChildrenDelete(ctx context.Context, q execer, kind, key string) error {
	switch kind {
	case "import_plan":
		_, err := q.ExecContext(ctx, `DELETE FROM import_plan_file WHERE plan_id=?`, key)
		return err
	case "import_receipt":
		_, err := q.ExecContext(ctx, `DELETE FROM import_receipt_member WHERE receipt_id=?`, key)
		return err
	}
	return nil
}
