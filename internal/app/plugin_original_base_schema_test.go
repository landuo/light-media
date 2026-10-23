package app

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOriginalBaseNamespaceMigrationPreservesRowsAndDeclarations(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "owned.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA foreign_keys=ON;
 CREATE TABLE plugin_installations(instance_id TEXT PRIMARY KEY);
 INSERT INTO plugin_installations VALUES('OwnedExisting');
 CREATE TABLE plugin_values(instance_id TEXT NOT NULL,kind TEXT NOT NULL CHECK(kind IN ('config','data')),item_key TEXT NOT NULL,revision INTEGER NOT NULL CHECK(revision>0),sealed BLOB NOT NULL,updated_at TEXT NOT NULL,PRIMARY KEY(instance_id,kind,item_key),FOREIGN KEY(instance_id) REFERENCES plugin_installations(instance_id));
 CREATE INDEX owned_value_revision ON plugin_values(revision);
 CREATE TRIGGER owned_value_protect BEFORE DELETE ON plugin_values WHEN OLD.item_key='protected' BEGIN SELECT RAISE(ABORT,'owned protected row'); END;
 INSERT INTO plugin_values(rowid,instance_id,kind,item_key,revision,sealed,updated_at) VALUES(71,'OwnedExisting','data','protected',4,x'000102ff','owned-time');`)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateOriginalBaseNamespaces(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var rowid, revision int64
	var sealed []byte
	var updated string
	if err = db.QueryRow(`SELECT rowid,revision,sealed,updated_at FROM plugin_values WHERE instance_id='OwnedExisting'`).Scan(&rowid, &revision, &sealed, &updated); err != nil {
		t.Fatal(err)
	}
	if rowid != 71 || revision != 4 || !bytes.Equal(sealed, []byte{0, 1, 2, 255}) || updated != "owned-time" {
		t.Fatal("migration changed original sealed row")
	}
	if _, err = db.Exec(`DELETE FROM plugin_values WHERE item_key='protected'`); err == nil {
		t.Fatal("migration lost delete trigger")
	}
	var n int
	if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='owned_value_revision'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("index lost: %d %v", n, err)
	}
	if _, err = db.Exec(`INSERT INTO plugin_values(instance_id,kind,item_key,revision,sealed,updated_at) VALUES('OwnedNeverInstalled','data','key',1,x'01','owned-time')`); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM plugin_installations`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("fabricated installation: %d %v", n, err)
	}
	if _, err = db.Exec(`INSERT INTO plugin_values(instance_id,kind,item_key,revision,sealed,updated_at) VALUES('OwnedNeverInstalled','invalid','other',0,x'01','owned-time')`); err == nil {
		t.Fatal("migration lost constraints")
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateOriginalBaseNamespaces(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
