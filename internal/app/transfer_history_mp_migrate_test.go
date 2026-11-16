package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"light-media/internal/catalog"
)

func TestMigrateLegacyProjectionIntoMPTableAndDropOldTables(t *testing.T) {
	dir := t.TempDir()
	first, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	plan := ImportPlan{ID: "legacy-plan", WorkflowID: "legacy-wf", Mode: "copy", Media: catalog.Media{ID: 123, MediaType: "movie", Title: "Legacy Film", Year: 2024}, Files: []PlanFile{{ID: "legacy-file", Source: "/source/Legacy.mkv", Target: "/library/Legacy.mkv", Method: "copy", Status: "imported", Size: 42}}}
	planRaw, _ := json.Marshal(plan)
	fileRaw, _ := json.Marshal(plan.Files[0])
	db, err := sql.Open("sqlite", filepath.Join(dir, "light-media.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE transfer_history_current(plan_id TEXT,file_id TEXT,plan_json TEXT,file_json TEXT,status TEXT,updated_at TEXT,source_path TEXT,target_path TEXT,resource_source TEXT)`,
		`CREATE TABLE transfer_history_attempts(id TEXT)`,
		`CREATE TABLE transfer_history_dirty(id TEXT)`,
		`CREATE TABLE transfer_history_dirty_sources(id TEXT)`,
		`CREATE TABLE transfer_history_index_version(version INTEGER)`,
	} {
		if _, err = db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO transfer_history_current(plan_id,file_id,plan_json,file_json,status,updated_at,source_path,target_path,resource_source) VALUES(?,?,?,?,?,?,?,?,?)`, plan.ID, plan.Files[0].ID, string(planRaw), string(fileRaw), "success", "2026-11-15T10:00:00Z", plan.Files[0].Source, plan.Files[0].Target, plan.Files[0].Source); err != nil {
		t.Fatal(err)
	}
	db.Close()
	first.Close()

	app, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	var count int
	if err = app.Store.DB.QueryRow(`SELECT COUNT(*) FROM transferhistory WHERE src=?`, plan.Files[0].Source).Scan(&count); err != nil || count != 1 {
		t.Fatal("migrated row", count, err)
	}
	var title, dest string
	if err = app.Store.DB.QueryRow(`SELECT title,dest FROM transferhistory WHERE src=?`, plan.Files[0].Source).Scan(&title, &dest); err != nil || title != "Legacy Film" || dest != plan.Files[0].Target {
		t.Fatal("migrated fields", title, dest, err)
	}
	for _, table := range []string{"transfer_history_current", "transfer_history_attempts", "transfer_history_dirty", "transfer_history_dirty_sources", "transfer_history_index_version"} {
		var exists int
		if err = app.Store.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); err != nil || exists != 0 {
			t.Fatal("legacy table retained", table, exists, err)
		}
	}
	var tables int
	if err = app.Store.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name LIKE 'transferhistory%'`).Scan(&tables); err != nil || tables != 1 {
		t.Fatalf("expected exactly one history table, got %d (%v)", tables, err)
	}
	var one int
	if err = app.Store.DB.QueryRowContext(context.Background(), `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatal(err)
	}
}
