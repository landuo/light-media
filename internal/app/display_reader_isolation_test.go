package app

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/download"
)

func TestDownloadOwnershipAndCalendarReadCommittedDuringWrite(t *testing.T) {
	a := newTestApp(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	record := NativeDownload{ID: "owned", Hash: strings.Repeat("a", 40), DownloaderID: "qb", State: "accepted"}
	if err := a.Store.Put(ctx, "native_download", record.ID, record); err != nil {
		t.Fatal(err)
	}
	s := Subscription{ID: "calendar", State: "active", Media: catalog.Media{ID: 7, MediaType: "tv", Title: "fixture"}, Season: 1, Episodes: []int{1}}
	if err := a.Store.Put(ctx, "subscription", s.ID, s); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "cache", "season:7:1", map[string]any{"data": catalog.Season{Episodes: []catalog.Episode{{EpisodeNumber: 1, AirDate: "2026-01-01"}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.ExecContext(ctx, `INSERT INTO asset_slots VALUES('tv:7',1,1,'asset')`); err != nil {
		t.Fatal(err)
	}
	before, err := a.calendar(ctx, "2026-01-01", "2026-01-02")
	if err != nil {
		t.Fatal(err)
	}
	entries := before.(map[string]any)["entries"].([]CalendarEntry)
	if len(entries) != 1 || entries[0].State != "in_library" {
		t.Fatalf("invalid committed calendar fixture: %+v", entries)
	}
	writer, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	record.State = "cancelled"
	if err = txPut(ctx, writer, "native_download", record.ID, record); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.ExecContext(ctx, `DELETE FROM asset_slots WHERE asset_id='asset'`); err != nil {
		t.Fatal(err)
	}
	owned, err := a.downloaderTaskOwnership(ctx, "qb")
	if err != nil {
		t.Fatal("ownership waited for writer:", err)
	}
	if !owned.contains(download.Task{Hash: record.Hash}) {
		t.Fatal("reader observed uncommitted cancellation")
	}
	during, err := a.calendar(ctx, "2026-01-01", "2026-01-02")
	if err != nil {
		t.Fatal("calendar waited for writer:", err)
	}
	if !reflect.DeepEqual(before, during) {
		t.Fatalf("calendar observed uncommitted changes: before=%+v during=%+v", before, during)
	}
	if err = writer.Commit(); err != nil {
		t.Fatal(err)
	}
	owned, err = a.downloaderTaskOwnership(ctx, "qb")
	if err != nil || owned.contains(download.Task{Hash: record.Hash}) {
		t.Fatalf("committed cancellation missing: %v %v", owned, err)
	}
	coverage, err := a.subscriptionCoverage(ctx, s, []int{1})
	if err != nil || len(coverage.Library) != 0 {
		t.Fatalf("committed asset removal missing: %+v %v", coverage, err)
	}
}
