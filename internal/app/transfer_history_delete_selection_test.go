package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestHistoryDeleteOnlyReadsSelectedRow(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := t.Context()
	// Both imported records refer to the same currently unavailable source.
	// Their row IDs identify independent entries, as in MP's history endpoint.
	missing := filepath.Join(source, "offline.mkv")
	_, err := a.Store.DB.ExecContext(ctx, `INSERT INTO transferhistory(id,src,status,src_fileitem,date) VALUES
	(17,?,0,'{}','2026-01-01'), (18,?,0,'{"file_id":"5400"}','2026-01-02')`, missing, missing)
	if err != nil {
		t.Fatal(err)
	}
	deletion, err := a.transferHistoryDeleteSelection(ctx, "17")
	if err != nil || !deletion.CanRetry {
		t.Fatalf("expected raw failed history without retry evaluation: %+v, %v", deletion, err)
	}
	detail, err := a.transferHistoryItem(ctx, "17")
	if err != nil || detail.CanRetry {
		t.Fatalf("detail should evaluate the unavailable source: %+v, %v", detail, err)
	}
	remove := func(id string) error {
		req := httptest.NewRequest("DELETE", "/", strings.NewReader(`{"confirm":true}`))
		_, err := a.transferHistoryAPI(req, "transfers/"+id)
		return err
	}
	if err := remove("5400"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing numeric history ID selected another row by file alias: %v", err)
	}
	if err := remove("17"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.transferHistoryDeleteSelection(ctx, "17"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("removed row still available: %v", err)
	}
	if _, err := a.transferHistoryDeleteSelection(ctx, "18"); err != nil {
		t.Fatalf("same-source sibling was lost: %v", err)
	}
}

func TestHistoryDeleteLegacyAliasUsesIndex(t *testing.T) {
	a, _, _ := importTestApp(t)
	ctx := t.Context()
	if _, err := a.Store.DB.ExecContext(ctx, `INSERT INTO transferhistory(src,src_fileitem,date) VALUES
('/selected','{"file_id":"old-file"}','2026-01-01'),('/unrelated','{','2026-01-02')`); err != nil {
		t.Fatal(err)
	}
	item, err := a.transferHistoryDeleteSelection(ctx, "old-file")
	if err != nil || item.Source != "/selected" {
		t.Fatalf("alias selected %q: %v", item.Source, err)
	}
	var id, parent, unused int
	var detail string
	err = a.Store.reader().QueryRowContext(ctx, `EXPLAIN QUERY PLAN SELECT `+transferHistoryMPColumns+
		`,dest_fileitem FROM transferhistory WHERE `+transferHistoryFileIDExpr+`=? ORDER BY id DESC LIMIT 1`, "old-file").Scan(&id, &parent, &unused, &detail)
	if err != nil || !strings.Contains(detail, "SEARCH transferhistory USING INDEX ix_transfer_history_file_id") {
		t.Fatalf("alias lookup should use the execution ID index: %s, %v", detail, err)
	}
}

func TestHistoryDeleteRowAndReceiptAreAtomic(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := t.Context()
	_, err := a.Store.DB.ExecContext(ctx, `INSERT INTO transferhistory(id,src,src_fileitem,date) VALUES(19,?,'{"file_id":"old-file"}','2026-01-01')`, filepath.Join(source, "selected.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := a.transferHistoryDeleteSelection(ctx, "old-file")
	if err != nil || item.ID != "19" {
		t.Fatalf("failed to resolve historical execution alias: %+v, %v", item, err)
	}
	if _, err := a.Store.DB.ExecContext(ctx, `CREATE TRIGGER fail_history_delete BEFORE DELETE ON transferhistory BEGIN SELECT RAISE(ABORT,'fixture row deletion failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.finishHistoryRowDeletion(ctx, item, historyDeleteRequest{}); err == nil {
		t.Fatal("expected row failure")
	}
	if _, err := a.transferHistoryDeleteSelection(ctx, "19"); err != nil {
		t.Fatal(err)
	}
	var hidden map[string]any
	if err := a.Store.Get(ctx, "transfer_history_hidden", "19", &hidden); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failure left a removal receipt: %v", err)
	}
	if _, err := a.Store.DB.ExecContext(ctx, `UPDATE transferhistory SET src_fileitem='{"file_id":"new-file"}' WHERE id=19`); err != nil {
		t.Fatal(err)
	}
	if err := a.finishHistoryRowDeletion(ctx, item, historyDeleteRequest{}); fileCode(err) != "HISTORY_CHANGED" {
		t.Fatalf("stale deletion did not preserve a replaced execution: %v", err)
	}
}

func TestHistoryDeleteImportedStoredFileItems(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := t.Context()
	path := filepath.Join(source, "imported.mkv")
	importWrite(t, path, []byte("unowned file"))
	stored, err := json.Marshal(map[string]string{"path": path, "storage": "local", "type": "file"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, srcItem, destItem string
		delSrc, delDest, denied bool
	}{
		{"absent_both", "{ }", "null", true, true, false},
		{"chosen_dest_absent", string(stored), "{}", false, true, false},
		{"chosen_src_absent", "{}", string(stored), true, false, false},
		{"present_src_unbound", string(stored), "null", true, false, true},
		{"present_dest_unbound", "{}", string(stored), true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := a.Store.DB.ExecContext(ctx, `INSERT INTO transferhistory(src,dest,src_fileitem,dest_fileitem,date) VALUES(?,?,?,?,?)`, path, path, tc.srcItem, tc.destItem, now())
			if err != nil {
				t.Fatal(err)
			}
			key, err := res.LastInsertId()
			if err != nil {
				t.Fatal(err)
			}
			id := strconv.FormatInt(key, 10)
			payload, err := json.Marshal(historyDeleteRequest{Confirm: true, FileID: id, DeleteSource: tc.delSrc, DeleteTarget: tc.delDest})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("DELETE", "/", bytes.NewReader(payload))
			_, err = a.transferHistoryAPI(req, "transfers/"+id)
			if tc.denied && fileCode(err) != "HISTORY_FILE_UNMANAGED" || !tc.denied && err != nil {
				t.Fatalf("unexpected file selection result: %v", err)
			}
			_, err = a.transferHistoryDeleteSelection(ctx, id)
			if tc.denied && err != nil || !tc.denied && !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("unexpected visibility after delete: %v", err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != "unowned file" {
				t.Fatalf("stored path changed: %v", err)
			}
		})
	}
}
