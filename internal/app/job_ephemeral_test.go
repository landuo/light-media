package app

import (
	"context"
	"testing"
)

// A subscription check is re-derived from the schedule after a restart, so it
// needs no durable row. This is the first chain moved off the jobs table.
func TestEphemeralSubscriptionNeedsNoRow(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	j, err := a.enqueue(ctx, "subscription", map[string]any{"id": "sub_probe", "revision": 1}, "wf_probe")
	if err != nil {
		t.Fatal(err)
	}
	if !j.Ephemeral {
		t.Fatal("a subscription check must be queued in memory only")
	}
	var rows int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE id=?`, j.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("no jobs row may exist for an ephemeral task, got %d", rows)
	}
	if a.indexDepth() != 1 {
		t.Fatalf("the task must be dispatchable from memory, depth = %d", a.indexDepth())
	}

	claimed, err := a.claimFromIndex(ctx, subscriptionJobs)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != j.ID || claimed.State != "running" {
		t.Fatalf("claimed %+v", claimed)
	}
	// Cancellation reads the index for these tasks.
	if err := a.jobCancelled(ctx, claimed); err != nil {
		t.Fatalf("a running ephemeral task must not report cancellation: %v", err)
	}

	if _, err := a.finishJob(ctx, claimed, "succeeded", "", "", 0); err != nil {
		t.Fatal(err)
	}
	if a.indexDepth() != 0 {
		t.Fatalf("a finished ephemeral task must leave the index, depth = %d", a.indexDepth())
	}
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE id=?`, j.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("finishing must not create a row either, got %d", rows)
	}
}

// Durability must not regress for kinds that still need a row.
func TestDurableKindsStillWriteRows(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	j, err := a.enqueue(ctx, "download_file_deleted", map[string]string{"id": "file_durable"}, "wf_probe")
	if err != nil {
		t.Fatal(err)
	}
	if j.Ephemeral {
		t.Fatal("an external file-event task must stay durable")
	}
	var rows int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE id=?`, j.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("durable work needs its row, got %d", rows)
	}
}

// The scheduler ticks repeatedly while a subject's timestamp is stale, so a
// second enqueue for the same subject must be refused or the queue grows.
func TestEphemeralEnqueueDeduplicatesSubject(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	if _, err := a.enqueue(ctx, "subscription", map[string]any{"id": "sub_dup", "revision": 1}, "wf_dup"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.enqueue(ctx, "subscription", map[string]any{"id": "sub_dup", "revision": 1}, "wf_dup"); err == nil {
		t.Fatal("a second check for the same subscription must be refused")
	}
	if depth := a.indexDepth(); depth != 1 {
		t.Fatalf("depth = %d, want 1", depth)
	}
	// A different subject is unaffected.
	if _, err := a.enqueue(ctx, "subscription", map[string]any{"id": "sub_other", "revision": 1}, "wf_dup"); err != nil {
		t.Fatalf("an unrelated subject must be accepted: %v", err)
	}
}
