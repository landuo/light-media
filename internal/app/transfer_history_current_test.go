package app

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestHistoryCurrentRefreshReleasesWriterAndPreservesSnapshot(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	plan, job := historyParityPlan(t, a, source, target, "snapshot", "Before", "running", "ready", "one.mkv")
	// This starts dirty, so the refresh-to-reader handoff is exercised.
	read, err := a.transferHistoryReadTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Rollback()
	before, err := scanTransferHistory(read.QueryRowContext(ctx, transferHistoryIndexedCTE+transferHistorySelect))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			done <- err
			return
		}
		defer tx.Rollback()
		plan.Media.Title = "After"
		plan.Files[0].Status = "imported"
		if err = txPut(ctx, tx, "import_plan", plan.ID, plan); err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID)
		}
		if err == nil {
			err = tx.Commit()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("history reader held writer connection after refresh")
	}
	during, err := scanTransferHistory(read.QueryRowContext(ctx, transferHistoryIndexedCTE+transferHistorySelect))
	if err != nil || during.Media.Title != "Before" || during.Status != before.Status {
		t.Fatalf("mixed snapshot: %+v %v", during, err)
	}
	if err = read.Commit(); err != nil {
		t.Fatal(err)
	}
	after, err := a.readIndexedTransferHistoryItem(ctx, transferHistoryIndexedCTE+transferHistorySelect)
	if err != nil || after.Media.Title != "After" || after.Status != "success" || after.ID != before.ID {
		t.Fatalf("completion not projected: %+v %v", after, err)
	}
	historyIndexMatchesExecutionRecords(t, a)
}

func TestHistoryCurrentHiddenInvalidationAndChangedSource(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	first, _ := historyParityPlan(t, a, source, target, "first-current", "First", "failed", "error", "one.mkv")
	second, _ := historyParityPlan(t, a, source, target, "second-current", "Second", "running", "ready", "one.mkv")
	second.Files[0].HistorySource = first.Files[0].Source
	if err := a.Store.Put(ctx, "import_plan", second.ID, second); err != nil {
		t.Fatal(err)
	}
	historyIndexMatchesExecutionRecords(t, a)
	item, err := a.readIndexedTransferHistoryItem(ctx, transferHistoryIndexedCTE+transferHistorySelect)
	if err != nil || item.ID != first.Files[0].ID || item.PlanID != second.ID {
		t.Fatalf("repeat source lost stable ID: %+v %v", item, err)
	}
	// Marker-only writes must invalidate the projected source, even though no
	// import plan is dirty. Removing/updating a marker must invalidate it too.
	for _, key := range []string{item.ExecutionKey, "0000", item.ExecutionKey} {
		if err := a.Store.Put(ctx, "transfer_history_hidden", first.Files[0].ID, map[string]string{"execution_key": key}); err != nil {
			t.Fatal(err)
		}
		historyIndexMatchesExecutionRecords(t, a)
	}
	if _, err := a.readIndexedTransferHistoryItem(ctx, transferHistoryIndexedCTE+transferHistorySelect); err != sql.ErrNoRows {
		t.Fatalf("hidden source visible: %v", err)
	}
	if _, err := a.Store.DB.Exec(`DELETE FROM records WHERE kind='transfer_history_hidden'`); err != nil {
		t.Fatal(err)
	}
	historyIndexMatchesExecutionRecords(t, a)
	// Moving a plan to a different source must reselect the old source and
	// insert the new one, not merge by media ID or leave the old row stale.
	second.Files[0].HistorySource = "different-source"
	if err := a.Store.Put(ctx, "import_plan", second.ID, second); err != nil {
		t.Fatal(err)
	}
	historyIndexMatchesExecutionRecords(t, a)
	var count int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM transfer_history_current`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("source change count=%d: %v", count, err)
	}
}

func TestHistoryCurrentPageUsesOrderedIndexes(t *testing.T) {
	a, _, _ := importTestApp(t)
	for _, tc := range []struct {
		where, index string
		args         []any
	}{
		{` WHERE 1=1`, "transfer_history_current_order", []any{50, 0}},
		{` WHERE status=?`, "transfer_history_current_status_order", []any{"failed", 50, 0}},
	} {
		rows, err := a.Store.DB.Query(`EXPLAIN QUERY PLAN `+transferHistoryPageSQL(tc.where), tc.args...)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(detail, "SCAN hp") || strings.Contains(detail, "SEARCH hp USING INDEX sqlite_autoindex_records_1 (kind=?)") {
				t.Fatalf("bounded page scans all plans: %s", detail)
			}
			if strings.Contains(detail, "TEMP B-TREE") {
				t.Fatalf("page sorts complete history: %s", detail)
			}
			found = found || strings.Contains(detail, tc.index)
		}
		err = rows.Err()
		rows.Close()
		if err != nil || !found {
			t.Fatalf("missing ordered index %s: %v", tc.index, err)
		}
	}
}

// Synthetic SQL benchmark: 100k distinct sources with minimal plan JSON.
// Measures count + first 50 hydrated rows, without filesystem capabilities.
func BenchmarkHistoryCurrentPage100K(b *testing.B) {
	s, err := OpenStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	const seed = `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100000)
 INSERT INTO records(kind,id,payload,updated_at)
 SELECT 'import_plan','bench-'||x,json_object('id','bench-'||x,'files',json_array(json_object('id','file-'||x,'source','/source/'||x,'target','/target/'||x,'status','imported'))),printf('%09d',x) FROM n;
 INSERT INTO transfer_history_attempts
 SELECT id,'$.files[0]',NULL,updated_at,'job-'||id,'succeeded','',updated_at,updated_at,
 'file-'||id,'file-'||id,updated_at,'Title','/source/'||id,'/target/'||id,'/source/'||id,'success',updated_at,1 FROM records WHERE kind='import_plan';
 INSERT INTO transfer_history_current SELECT *,execution_file_id,plan_created_at FROM transfer_history_attempts;
 DELETE FROM transfer_history_dirty;`
	if _, err = s.DB.Exec(seed); err != nil {
		b.Fatal(err)
	}
	for _, legacy := range []bool{false, true} {
		b.Run(fmt.Sprintf("legacy_%v", legacy), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				tx, err := s.reader().Begin()
				if err != nil {
					b.Fatal(err)
				}
				query := transferHistoryPageSQL(` WHERE 1=1`)
				if legacy {
					query = transferHistoryIndexedBase + transferHistoryRanked + `,page AS MATERIALIZED (SELECT h.*,COUNT(*) OVER () AS total FROM history h` + transferHistoryOrder + ` LIMIT ? OFFSET ?) SELECT ` + transferHistoryColumns + ` FROM page h` + transferHistoryHydrate
				} else {
					var total int
					if err = tx.QueryRow(`SELECT COUNT(*) FROM transfer_history_current`).Scan(&total); err != nil || total != 100000 {
						b.Fatalf("count %d: %v", total, err)
					}
				}
				rows, err := tx.Query(query, 50, 0)
				if err != nil {
					b.Fatal(err)
				}
				count := 0
				for rows.Next() {
					if _, err = scanTransferHistory(rows); err != nil {
						b.Fatal(err)
					}
					count++
				}
				err = rows.Err()
				rows.Close()
				tx.Rollback()
				if err != nil || count != 50 {
					b.Fatalf("page %d: %v", count, err)
				}
			}
		})
	}
	b.Run("refresh_one_source", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			tx, err := s.DB.Begin()
			if err != nil {
				b.Fatal(err)
			}
			if _, err = tx.Exec(`INSERT INTO transfer_history_dirty_sources VALUES('/source/bench-1')`); err != nil {
				b.Fatal(err)
			}
			if err = refreshTransferHistoryIndex(context.Background(), tx); err != nil {
				b.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				b.Fatal(err)
			}
		}
	})

}

func TestHistoryCurrentProjectionUpgradeAndOnlyDirtySources(t *testing.T) {
	a, source, target := importTestApp(t)
	first, _ := historyParityPlan(t, a, source, target, "dirty-current", "First", "failed", "error", "one.mkv")
	historyParityPlan(t, a, source, target, "clean-current", "Other", "succeeded", "imported", "two.mkv")
	historyIndexMatchesExecutionRecords(t, a)
	// A v1 attempt index may already be clean during upgrade. Its source groups
	// still need a one-time backfill even though no source records are dirty.
	if _, err := a.Store.DB.Exec(`DELETE FROM transfer_history_current; DELETE FROM transfer_history_index_version WHERE version=2;` + transferHistoryIndexSchema()); err != nil {
		t.Fatal(err)
	}
	historyIndexMatchesExecutionRecords(t, a)
	if _, err := a.Store.DB.Exec(`CREATE TABLE current_writes(source TEXT);
 CREATE TRIGGER observe_current AFTER INSERT ON transfer_history_current BEGIN INSERT INTO current_writes VALUES(NEW.resource_source); END;`); err != nil {
		t.Fatal(err)
	}
	first.Media.Title = "Updated"
	if err := a.Store.Put(context.Background(), "import_plan", first.ID, first); err != nil {
		t.Fatal(err)
	}
	historyIndexMatchesExecutionRecords(t, a)
	historyIndexMatchesExecutionRecords(t, a)
	var own, other int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM current_writes WHERE source=?`, first.Files[0].Source).Scan(&own); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM current_writes WHERE source!=?`, first.Files[0].Source).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if own != 1 || other != 0 {
		t.Fatalf("refresh rewrote own=%d unrelated=%d groups", own, other)
	}
	rows, err := a.Store.DB.Query(`EXPLAIN QUERY PLAN ` + transferHistoryCurrentRefreshSQL())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seek := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "SCAN transfer_history_attempts") {
			t.Fatalf("dirty refresh scans all sources: %s", detail)
		}
		seek = seek || strings.Contains(detail, "SEARCH transfer_history_attempts USING INDEX transfer_history_source")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !seek {
		t.Fatal("refresh must seek only dirty source groups")
	}
}
