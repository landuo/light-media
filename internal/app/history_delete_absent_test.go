package app

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestHistoryDeletionReconcilesAbsentSelectedFiles(t *testing.T) {
	a, item := historyRemovalFixture(t)
	ctx := t.Context()
	historyUnrelatedDownloader(t, a, item)
	for _, path := range []string{item.Source, item.Target} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	unrelated := filepath.Join(filepath.Dir(item.Source), "other.mkv")
	for key, path := range map[string]string{"selected": item.Source, "other": unrelated} {
		if _, err := a.Store.DB.ExecContext(ctx, `INSERT INTO downloadfiles(uid,payload,updated_at)
 VALUES(?,json_object('fullpath',?,'state',1),?)`, key, path, now()); err != nil {
			t.Fatal(err)
		}
	}
	selected, err := a.transferHistoryDeleteSelection(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.deleteTransferHistory(ctx, selected, historyDeleteRequest{
		Confirm: true, DeleteSource: true, DeleteTarget: true, FileID: item.FileID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.transferHistoryResolveID(ctx, item.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("visible history after deletion: %v", err)
	}
	var source historySourceRemoval
	var target historyTargetRemoval
	if err := a.Store.Get(ctx, "history_source_removal", item.FileID, &source); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Get(ctx, "history_target_removal", item.FileID, &target); err != nil {
		t.Fatal(err)
	}
	if source.State != "absent" || target.State != "absent" || source.File.Deleted || target.File.Deleted {
		t.Fatal("absence recorded as a physical removal", source.State, target.State)
	}
	if _, err := os.Stat(filepath.Dir(item.Target)); !os.IsNotExist(err) {
		t.Fatalf("expected empty target parent to be removed, got %v", err)
	}
	for key, want := range map[string]int{"selected": 0, "other": 1} {
		var state, stored int
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT state,json_extract(payload,'$.state')
 FROM downloadfiles WHERE uid=?`, key).Scan(&state, &stored); err != nil || stored != want || state != want {
			t.Fatalf("%s: want state %d, got %d/%d, err=%v", key, want, state, stored, err)
		}
	}
	var events int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='downloadfile.deleted'`).Scan(&events); err != nil || events != 1 {
		t.Fatalf("expected one source event, got %d: %v", events, err)
	}
}
