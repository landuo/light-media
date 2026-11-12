package app

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"light-media/internal/catalog"
)

func displayQueryPlan(t *testing.T, a *App, query string, args ...any) string {
	t.Helper()
	rows, err := a.Store.reader().QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(plan, "\n")
}
func TestDisplayReadsUseSelectiveIndexes(t *testing.T) {
	a := newTestApp(t)
	for _, kind := range []string{"asset", "subscription", "intent"} {
		plan := displayQueryPlan(t, a, `SELECT payload FROM records WHERE kind=? AND (json_extract(payload,'$.media.media_type')||':'||json_extract(payload,'$.media.id')) IN (?,?)`, kind, "tv:1", "movie:2")
		if !strings.Contains(plan, kind+"_media_key") || !strings.Contains(plan, "<expr>=?") {
			t.Fatalf("media lookup is not an indexed seek: %s", plan)
		}
	}
	plan := displayQueryPlan(t, a, `SELECT id,`+jobDisplayPriority+` AS priority FROM jobs ORDER BY priority,created_at DESC,id LIMIT 100`)
	if !strings.Contains(plan, "jobs_display_priority") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatalf("queue priority sorts history: %s", plan)
	}
	plan = displayQueryPlan(t, a, `SELECT payload FROM records WHERE kind='subscription' AND json_extract(payload,'$.state') IS NOT 'cancelled' ORDER BY updated_at DESC,id LIMIT 500`)
	if !strings.Contains(plan, "subscription_display_order") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatalf("subscription list sorts all records: %s", plan)
	}
	// These indexes must not evaluate JSON on unrelated encrypted payloads.
	if _, err := a.Store.DB.Exec(`INSERT INTO records VALUES('opaque-test','encrypted',x'01020304','now')`); err != nil {
		t.Fatal(err)
	}
}
func TestDisplayReadsDoNotWaitForWriterAndHideUncommittedState(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	media := catalog.Media{ID: 91, MediaType: "movie", Title: "snapshot"}
	summaryRecord(t, a, "asset", "asset", Asset{ID: "asset", Media: media, State: "present"})
	summaryRecord(t, a, "subscription", "sub", Subscription{ID: "sub", Media: media, State: "active"})
	summaryRecord(t, a, "intent", "intent", Intent{ID: "intent", Media: media, State: "downloaded"})
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = txPut(ctx, tx, "asset", "asset", Asset{ID: "asset", Media: media, State: "removed"}); err != nil {
		t.Fatal(err)
	}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	items := []catalog.Media{{ID: 91, MediaType: "movie", Recommendations: []catalog.Media{media}}}
	if err = a.decorateMedia(readCtx, items); err != nil {
		t.Fatal(err)
	}
	for _, item := range []catalog.Media{items[0], items[0].Recommendations[0]} {
		if item.LibraryState != "present" || item.SubscriptionID != "sub" || item.DownloadState != "downloaded" {
			t.Fatalf("bad committed badges: %+v", item)
		}
	}
	if _, err = a.subscriptionList(readCtx); err != nil {
		t.Fatal(err)
	}
	if _, err = a.managedDirectories(readCtx); err != nil {
		t.Fatal(err)
	}
	if _, err = a.jobQueue(readCtx); err != nil {
		t.Fatal(err)
	}
	if _, err = a.subscriptionWords(readCtx, "sub"); err != nil {
		t.Fatal(err)
	}
}
func TestSubscriptionDisplayBatchMatchesCoverageAndWords(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	sub := Subscription{ID: "display", Media: catalog.Media{ID: 206484, MediaType: "tv"}, Season: 4, State: "active", StartEpisode: 1, Episodes: []int{1, 2, 3, 4, 5}, Excluded: []int{5}}
	summaryRecord(t, a, "subscription", sub.ID, sub)
	other := sub
	other.ID = "display-other"
	other.Excluded = nil
	other.StartEpisode = 2
	summaryRecord(t, a, "subscription", other.ID, other)
	cancelled := sub
	cancelled.ID = "cancelled"
	cancelled.State = "cancelled"
	summaryRecord(t, a, "subscription", cancelled.ID, cancelled)
	summaryRecord(t, a, "intent", "pending", Intent{ID: "pending", State: "downloaded"})
	summaryRecord(t, a, "exclusion", "tv:206484:4:3", map[string]bool{"blocked": true})
	summaryRecord(t, a, "exclusion", "tv:206484:40:4", map[string]bool{"blocked": true})
	for _, statement := range []string{`INSERT INTO asset_slots VALUES('tv:206484',4,1,'asset')`, `INSERT INTO reservations VALUES('tv:206484',4,2,'pending')`} {
		if _, err := a.Store.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	words := "private recognition words"
	if err = a.saveSubscriptionWordsTx(ctx, tx, sub.ID, &words); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := a.loadSubscriptionDisplay(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.subscriptions) != 2 || snapshot.words[sub.ID] != words || snapshot.words[other.ID] != "" {
		t.Fatalf("unexpected display snapshot: %+v", snapshot)
	}
	for _, s := range snapshot.subscriptions {
		want, err := a.subscriptionCoverage(ctx, s, a.cachedSeasonEpisodes(ctx, s))
		if err != nil {
			t.Fatal(err)
		}
		data := snapshot.coverage[subscriptionDisplayKey(s)]
		got, err := a.subscriptionCoverageLoaded(ctx, s, data.known, nil, data)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s batch=%+v single=%+v", s.ID, got, want)
		}
	}
	value, err := a.subscriptionList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	response := value.(map[string]any)
	if response["total"] != 2 {
		t.Fatalf("unexpected response: %+v", response)
	}
	for _, entry := range response["items"].([]map[string]any) {
		if entry["id"] == sub.ID && entry["custom_words"] != words {
			t.Fatal("editor words missing")
		}
	}
}

func TestSubscriptionDisplayKeepsAtomicCoverageSnapshot(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	sub := Subscription{ID: "atomic", Media: catalog.Media{ID: 19, MediaType: "movie"}, State: "active"}
	summaryRecord(t, a, "subscription", sub.ID, sub)
	if _, err := a.Store.DB.Exec(`INSERT INTO reservations VALUES('movie:19',0,0,'intent')`); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				done <- err
				return
			}
			_, err = tx.Exec(`DELETE FROM reservations WHERE media_key='movie:19';DELETE FROM asset_slots WHERE media_key='movie:19'`)
			if err == nil {
				if i%2 == 0 {
					_, err = tx.Exec(`INSERT INTO asset_slots VALUES('movie:19',0,0,'asset')`)
				} else {
					_, err = tx.Exec(`INSERT INTO reservations VALUES('movie:19',0,0,'intent')`)
				}
			}
			if err != nil {
				tx.Rollback()
				done <- err
				return
			}
			if err = tx.Commit(); err != nil {
				done <- err
				return
			}
		}
	}()
	defer func() {
		close(stop)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	for i := 0; i < 50; i++ {
		snapshot, err := a.loadSubscriptionDisplay(ctx)
		if err != nil {
			t.Fatal(err)
		}
		data := snapshot.coverage[subscriptionDisplayKey(sub)]
		if data.library[0] == data.reserved[0] {
			t.Fatalf("mixed reservation/asset commits: library=%v reserved=%v", data.library, data.reserved)
		}
	}
}

func BenchmarkDecorateMediaIndexed100k(b *testing.B) {
	a, err := New(Options{DataDir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO records VALUES(?,?,?,?)`)
	if err != nil {
		b.Fatal(err)
	}
	for _, kind := range []string{"asset", "subscription", "intent"} {
		for i := 1; i <= 100000; i++ {
			payload := fmt.Sprintf(`{"id":"%d","media":{"id":%d,"media_type":"movie"},"state":"present"}`, i, i)
			if _, err = stmt.Exec(kind, fmt.Sprint(i), []byte(payload), "now"); err != nil {
				b.Fatal(err)
			}
		}
	}
	stmt.Close()
	if err = tx.Commit(); err != nil {
		b.Fatal(err)
	}
	items := make([]catalog.Media, 20)
	for i := range items {
		items[i] = catalog.Media{ID: int64(i*4999 + 1), MediaType: "movie"}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err = a.decorateMedia(ctx, items); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
}

func TestManagedDirectoryDisplayAggregatesPendingKinds(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	a.config.Directories = []DirectoryRule{{ID: "one", SourcePath: t.TempDir(), Enabled: true, MonitorType: "poll"}, {ID: "two", SourcePath: t.TempDir()}}
	for _, kind := range []string{"intent", "directory_file"} {
		for i, state := range []string{"planned", "downloaded", "imported", "cancelled", "complete", "baseline", "tracking"} {
			summaryRecord(t, a, kind, fmt.Sprintf("%s-%d", kind, i), map[string]string{"directory_id": "one", "state": state})
		}
	}
	summaryRecord(t, a, "directory_status", "one", managedConnection{Status: "connected"})
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = txPut(ctx, tx, "intent", "new", map[string]string{"directory_id": "two", "state": "planned"}); err != nil {
		t.Fatal(err)
	}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := a.managedDirectories(readCtx)
	if err != nil {
		t.Fatal(err)
	}
	dirs := result.(map[string]any)["directories"].([]map[string]any)
	// Intent excludes only imported/cancelled (5); directory files exclude
	// complete/imported/baseline/tracking (3). Preserve the distinct policies.
	if dirs[0]["pending_count"] != 8 || dirs[1]["pending_count"] != 0 {
		t.Fatalf("pending counts %+v", dirs)
	}
	if dirs[0]["connection"].(managedConnection).Status != "connected" || dirs[1]["connection"].(managedConnection).Status != "disabled" {
		t.Fatalf("directory states %+v", dirs)
	}
}
