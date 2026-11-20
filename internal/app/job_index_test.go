package app

import (
	"context"
	"testing"
	"time"
)

// Dispatch reads the index, so a job must be claimable the moment it is
// registered, without a table scan.
func TestJobIndexClaimsRegisteredWork(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	j, err := a.enqueue(ctx, "scrape", map[string]string{"id": "asset_probe"}, "wf_probe")
	if err != nil {
		t.Fatal(err)
	}
	if depth := a.indexDepth(); depth != 1 {
		t.Fatalf("index depth = %d, want 1", depth)
	}
	claimed, err := a.claimFromIndex(ctx, generalJobs)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != j.ID || claimed.State != "running" {
		t.Fatalf("claimed %+v", claimed)
	}
	// A claimed job is no longer queued, so it must not be handed out twice.
	if _, err := a.claimFromIndex(ctx, generalJobs); err == nil {
		t.Fatal("a running job must not be claimed again")
	}
}

// A row that moved on (cancelled elsewhere) is dropped instead of executed.
func TestJobIndexDropsRowThatMovedOn(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	// A durable kind: this case is about the row moving on underneath the index.
	j, err := a.enqueue(ctx, "subscription_add", map[string]string{"id": "sub_add_probe2"}, "wf_probe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.ExecContext(ctx, `UPDATE jobs SET state='cancelled' WHERE id=?`, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.claimFromIndex(ctx, generalJobs); err == nil {
		t.Fatal("a cancelled row must not be claimed")
	}
	if depth := a.indexDepth(); depth != 0 {
		t.Fatalf("the stale entry must be dropped, depth = %d", depth)
	}
}

// Terminal work leaves the index; delayed work stays but is not due yet.
func TestJobIndexTracksTerminalAndDelayedWork(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	delayed := Job{ID: "job_later", WorkflowID: "wf_later", Kind: "scrape", State: "queued",
		Payload: []byte(`{"id":"later"}`), RunAt: time.Now().Add(time.Minute).Unix(),
		CreatedAt: now(), UpdatedAt: now()}
	a.indexEnqueue(delayed)
	if _, err := a.claimFromIndex(ctx, generalJobs); err == nil {
		t.Fatal("work that is not due yet must not be claimed")
	}
	if _, err := a.Store.DB.ExecContext(ctx,
		`INSERT INTO jobs(id,workflow_id,kind,state,payload,run_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		delayed.ID, delayed.WorkflowID, delayed.Kind, delayed.State, delayed.Payload, delayed.RunAt, delayed.CreatedAt, delayed.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	a.indexRefresh(Job{ID: delayed.ID, Kind: "scrape", State: "succeeded"})
	if depth := a.indexDepth(); depth != 0 {
		t.Fatalf("terminal work must leave the index, depth = %d", depth)
	}
}

// The in-transaction duplicate check now answers from the index.
func TestSubjectBusyUsesIndex(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	if a.subjectBusy("scrape", "asset_dup") {
		t.Fatal("an empty index must not report a conflict")
	}
	if _, err := a.enqueue(ctx, "scrape", map[string]string{"id": "asset_dup"}, "wf_dup"); err != nil {
		t.Fatal(err)
	}
	if !a.subjectBusy("scrape", "asset_dup") {
		t.Fatal("a queued job must report its subject as busy")
	}
}
