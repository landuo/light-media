package app

import (
	"context"
	"database/sql"
	"fmt"
)

// PluginBase namespaces are independent of the installed list. Preserve the
// same encrypted table, rowids, constraints, and caller-defined indexes/triggers;
// only its obsolete installation FK is removed, atomically with startup schema.
func migrateOriginalBaseNamespaces(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_list(plugin_values)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var id, seq int
		var table, from, to, onUpdate, onDelete, match string
		if err = rows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			rows.Close()
			return err
		}
		if table != "plugin_installations" || from != "instance_id" || to != "instance_id" {
			rows.Close()
			return fmt.Errorf("unexpected plugin_values foreign key")
		}
		found = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	rows, err = tx.QueryContext(ctx, `SELECT sql FROM sqlite_master WHERE tbl_name='plugin_values' AND type IN ('index','trigger') AND sql IS NOT NULL ORDER BY type,name`)
	if err != nil {
		return err
	}
	var declarations []string
	for rows.Next() {
		var text string
		if err = rows.Scan(&text); err != nil {
			rows.Close()
			return err
		}
		declarations = append(declarations, text)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `CREATE TABLE plugin_values_original_namespace(
 instance_id TEXT NOT NULL,kind TEXT NOT NULL CHECK(kind IN ('config','data')),item_key TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),sealed BLOB NOT NULL,updated_at TEXT NOT NULL,
 PRIMARY KEY(instance_id,kind,item_key));
 INSERT INTO plugin_values_original_namespace(rowid,instance_id,kind,item_key,revision,sealed,updated_at)
 SELECT rowid,instance_id,kind,item_key,revision,sealed,updated_at FROM plugin_values;
 DROP TABLE plugin_values;
 ALTER TABLE plugin_values_original_namespace RENAME TO plugin_values;`)
	if err != nil {
		return err
	}
	for _, declaration := range declarations {
		if _, err = tx.ExecContext(ctx, declaration); err != nil {
			return err
		}
	}
	return nil
}
