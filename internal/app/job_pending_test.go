package app

import (
	"context"
	"testing"
)

// A task registered inside a transaction must not become dispatchable before
// that transaction commits, and must be dispatchable immediately after.
func TestPendingJobReleasesOnCommitOnly(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	j, err := a.enqueueTx(ctx, tx, "scrape", map[string]string{"id": "asset_pending"}, "wf_pending")
	if err != nil {
		t.Fatal(err)
	}
	if !j.Ephemeral {
		t.Fatal("scrape must be rowless now")
	}
	if a.indexDepth() != 0 {
		t.Fatal("work must stay invisible until the transaction commits")
	}
	// Commit first: querying while the transaction holds the single test
	// connection would block on itself.
	if err := a.commitWithJobs(tx); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE id=?`, j.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("no row may be written, got %d", rows)
	}
	if a.indexDepth() != 1 {
		t.Fatalf("work must be dispatchable after commit, depth = %d", a.indexDepth())
	}
}

// A rolled-back transaction must not leave work in the index.
func TestPendingJobDroppedOnRollback(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.enqueueTx(ctx, tx, "scrape", map[string]string{"id": "asset_rollback"}, "wf_rb"); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	a.dropPendingJobs(tx)
	if a.indexDepth() != 0 {
		t.Fatalf("a rolled-back task must not be dispatched, depth = %d", a.indexDepth())
	}
}
