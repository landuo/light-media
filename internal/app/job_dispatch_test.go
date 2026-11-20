package app

import (
	"context"
	"testing"
	"time"
)

// An idle queue must not poll. The wait is derived from the earliest due row and
// bounded, so a far-future task does not spin and a clock change is still caught.
func TestNextQueueDelayFollowsEarliestDueRow(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	if d := a.nextQueueDelay(ctx); d > 3*time.Second {
		t.Fatalf("an empty queue must fall back to the short bound, got %v", d)
	}

	far := Job{ID: "job_far", WorkflowID: "wf_far", Kind: "scrape", State: "queued",
		Payload: []byte(`{"id":"x"}`), RunAt: time.Now().Add(10 * time.Minute).Unix(),
		CreatedAt: now(), UpdatedAt: now()}
	if err := a.Store.Put(ctx, "job", far.ID, far); err != nil {
		// Jobs live in their own table; insert directly when the store helper differs.
		if _, err := a.Store.DB.ExecContext(ctx,
			`INSERT INTO jobs(id,workflow_id,kind,state,payload,run_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
			far.ID, far.WorkflowID, far.Kind, far.State, far.Payload, far.RunAt, far.CreatedAt, far.UpdatedAt); err != nil {
			t.Fatal(err)
		}
	}
	if d := a.nextQueueDelay(ctx); d > 31*time.Second {
		t.Fatalf("a far-future task must clamp the wait, got %v", d)
	}

	near := Job{ID: "job_near", WorkflowID: "wf_near", Kind: "scrape", State: "queued",
		Payload: []byte(`{"id":"y"}`), RunAt: time.Now().Add(200 * time.Millisecond).Unix(),
		CreatedAt: now(), UpdatedAt: now()}
	if _, err := a.Store.DB.ExecContext(ctx,
		`INSERT INTO jobs(id,workflow_id,kind,state,payload,run_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		near.ID, near.WorkflowID, near.Kind, near.State, near.Payload, near.RunAt, near.CreatedAt, near.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if d := a.nextQueueDelay(ctx); d > 2*time.Second {
		t.Fatalf("a due-soon task must shorten the wait, got %v", d)
	}
	if d := a.nextQueueDelay(ctx); d < 50*time.Millisecond {
		t.Fatalf("the wait must stay above the floor, got %v", d)
	}
}
