package app

import (
	"database/sql"
	"encoding/json"
	"testing"
)

// The compatibility view is gone; tests read the owning feature table directly.
// These helpers keep the assertions about "rows for kind X" readable without
// re-introducing a kind-keyed table.

func recordTableForTest(t *testing.T, kind string) recordTable {
	t.Helper()
	table, ok := recordTableFor(kind)
	if !ok {
		t.Fatalf("no feature table for kind %s", kind)
	}
	return table
}

func countRecordKind(t *testing.T, db *sql.DB, kind string) int {
	t.Helper()
	table := recordTableForTest(t, kind)
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM `+table.name+` WHERE record_kind=?`, kind).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", kind, err)
	}
	return n
}

func countRecordKinds(t *testing.T, db *sql.DB, kinds ...string) int {
	t.Helper()
	total := 0
	for _, kind := range kinds {
		total += countRecordKind(t, db, kind)
	}
	return total
}

func readRecordPayload(t *testing.T, db *sql.DB, kind, key string) []byte {
	t.Helper()
	table := recordTableForTest(t, kind)
	var raw []byte
	if err := db.QueryRow(`SELECT payload FROM `+table.name+` WHERE `+recordKeyColumn(table)+`=? AND record_kind=?`, key, kind).Scan(&raw); err != nil {
		t.Fatalf("read %s/%s: %v", kind, key, err)
	}
	return raw
}

func readRecordPayloadOK(t *testing.T, db *sql.DB, kind, key string) ([]byte, bool) {
	t.Helper()
	table := recordTableForTest(t, kind)
	var raw []byte
	err := db.QueryRow(`SELECT payload FROM `+table.name+` WHERE `+recordKeyColumn(table)+`=?`, key).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, false
	}
	if err != nil {
		t.Fatalf("read %s/%s: %v", kind, key, err)
	}
	return raw, true
}

func writeRecordPayload(t *testing.T, db *sql.DB, kind, key string, payload []byte) {
	t.Helper()
	table := recordTableForTest(t, kind)
	if _, err := db.Exec(recordInsertSQL(kind, table), key, payload, now()); err != nil {
		t.Fatalf("write %s/%s: %v", kind, key, err)
	}
}

func recordExists(t *testing.T, db *sql.DB, kind, key string) bool {
	t.Helper()
	_, ok := readRecordPayloadOK(t, db, kind, key)
	return ok
}

func recordRows(t *testing.T, db *sql.DB, kind string) []json.RawMessage {
	t.Helper()
	table := recordTableForTest(t, kind)
	rows, err := db.Query(`SELECT payload FROM ` + table.name + ` WHERE record_kind=? ORDER BY updated_at DESC,` + recordKeyColumn(table))
	if err != nil {
		t.Fatalf("rows %s: %v", kind, err)
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("rows %s: %v", kind, err)
		}
		out = append(out, json.RawMessage(raw))
	}
	return out
}

func countRecordKey(t *testing.T, db *sql.DB, kind, key string) int {
	t.Helper()
	table := recordTableForTest(t, kind)
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM `+table.name+` WHERE `+recordKeyColumn(table)+`=?`, key).Scan(&n); err != nil {
		t.Fatalf("count %s/%s: %v", kind, key, err)
	}
	return n
}

func deleteRecordKind(t *testing.T, q interface {
	Exec(string, ...any) (sql.Result, error)
}, kind string) {
	t.Helper()
	table := recordTableForTest(t, kind)
	if _, err := q.Exec(`DELETE FROM ` + table.name + ` WHERE record_kind='` + kind + `'`); err != nil {
		t.Fatalf("delete %s: %v", kind, err)
	}
}

func deleteRecordKey(t *testing.T, q interface {
	Exec(string, ...any) (sql.Result, error)
}, key string, kinds ...string) {
	t.Helper()
	for _, kind := range kinds {
		table := recordTableForTest(t, kind)
		if _, err := q.Exec(`DELETE FROM `+table.name+` WHERE `+recordKeyColumn(table)+`=? AND record_kind='`+kind+`'`, key); err != nil {
			t.Fatalf("delete %s/%s: %v", kind, key, err)
		}
	}
}
