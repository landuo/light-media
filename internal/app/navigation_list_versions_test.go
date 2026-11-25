package app

import (
	"context"
	"light-media/internal/catalog"
	"strings"
	"testing"
)

func TestNavigationListsSeeCommittedChangesAndKeepCapabilitiesPerRequest(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	job := summaryJob(t, a, "asset_check", map[string]string{"id": "missing"}, "")
	queue, err := a.jobQueue(ctx)
	if err != nil || len(queue) != 1 {
		t.Fatalf("initial queue: %d rows, %v", len(queue), err)
	}
	if a.jobDisplayCache.Load() == nil {
		t.Fatal("common queue was not cached")
	}
	a.event(ctx, job, "INFO", "assets", "asset_check", "文件核对完成")
	queue, err = a.jobQueue(ctx)
	if err != nil || len(queue) != 1 || queue[0].Summary.Activity != "文件核对完成" {
		t.Fatalf("queue missed committed job event: %v", err)
	}
	_, err = a.Store.DB.ExecContext(ctx, "INSERT INTO transferhistory(src,title,date) VALUES('/owned/a','第一版','2026-11-01')")
	if err != nil {
		t.Fatal(err)
	}
	rows, total, err := a.transferHistoryMPPage(ctx, 1, 50)
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("initial history: %d rows, %v", total, err)
	}
	rows[0].CanRecognize = false
	rows, total, err = a.transferHistoryMPPage(ctx, 1, 50)
	if err != nil || total != 1 || len(rows) != 1 || !rows[0].CanRecognize {
		t.Fatalf("request mutated cached capabilities: %v", err)
	}
	_, err = a.Store.DB.ExecContext(ctx, "UPDATE transferhistory SET title='第二版'")
	if err != nil {
		t.Fatal(err)
	}
	rows, total, err = a.transferHistoryMPPage(ctx, 1, 50)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].Media.Title != "第二版" {
		t.Fatalf("history missed committed correction: %v", err)
	}
}

func TestCachedCatalogReadsObserveOutOfBandChange(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	if err := a.Store.Put(ctx, "cache", "owned", map[string]string{"value": "first"}); err != nil {
		t.Fatal(err)
	}
	var value map[string]string
	for i := 0; i < 2; i++ {
		if err := a.Store.Get(ctx, "cache", "owned", &value); err != nil || value["value"] != "first" {
			t.Fatal(value, err)
		}
	}
	// A direct SQL writer may replace the bytes without touching updated_at.
	if _, err := a.Store.DB.ExecContext(ctx, `UPDATE cache_entry SET payload='{"value":"second"}' WHERE id='owned'`); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Get(ctx, "cache", "owned", &value); err != nil || value["value"] != "second" {
		t.Fatal(value, err)
	}
	if err := a.Store.Delete(ctx, "cache", "owned"); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Get(ctx, "cache", "owned", &value); err == nil {
		t.Fatal("deleted metadata was served")
	}
}

func TestJobQueueIncrementReadsChangedSubjectAndNewMembership(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	aSub := Subscription{ID: "a", Media: catalog.Media{Title: "旧标题", MediaType: "tv"}}
	if err := a.Store.Put(ctx, "subscription", "a", aSub); err != nil {
		t.Fatal(err)
	}
	first := summaryJob(t, a, "subscription", map[string]string{"id": "a"}, "")
	another := summaryJob(t, a, "site_refresh", map[string]string{}, "")
	if _, err := a.jobQueue(ctx); err != nil {
		t.Fatal(err)
	}
	aSub.Media.Title = "新标题"
	if err := a.Store.Put(ctx, "subscription", "a", aSub); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec("DELETE FROM jobs WHERE id=?", another.ID); err != nil {
		t.Fatal(err)
	}
	added := summaryJob(t, a, "asset_check", map[string]string{"id": "asset"}, "")
	rows, err := a.jobQueue(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatal(len(rows), err)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.ID] = true
		if r.ID == first.ID && !strings.Contains(r.Summary.Subject, "新标题") {
			t.Fatal("stale related subscription title")
		}
	}
	if !seen[first.ID] || !seen[added.ID] || seen[another.ID] {
		t.Fatal("stale queue membership")
	}
}

func TestCatalogStampIndependentOfResourceWritesAndRollback(t *testing.T) {
	a := newTestApp(t)
	ctx := t.Context()
	if err := a.Store.Put(ctx, "cache", "page", map[string]string{"title": "first"}); err != nil {
		t.Fatal(err)
	}
	var first, after string
	stamp := func(out *string) {
		t.Helper()
		if err := a.Store.reader().QueryRowContext(ctx, "SELECT stamp FROM catalog_page_stamps WHERE id='page'").Scan(out); err != nil {
			t.Fatal(err)
		}
	}
	stamp(&first)
	if _, err := a.Store.DB.Exec("INSERT INTO cache_entry(id,payload,updated_at,record_kind) VALUES('other','{}','now','resource')"); err != nil {
		t.Fatal(err)
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("DELETE FROM cache_entry WHERE id='page'"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	stamp(&after)
	if after != first {
		t.Fatal("unrelated or rolled-back write invalidated page")
	}
	if _, err := a.Store.DB.Exec("INSERT OR REPLACE INTO cache_entry(id,payload,updated_at,record_kind) VALUES('page','{}','now','resource')"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := a.Store.reader().QueryRow("SELECT count(*) FROM catalog_page_stamps WHERE id='page'").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
