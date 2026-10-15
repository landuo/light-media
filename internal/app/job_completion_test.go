package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func completionRunningJob(t *testing.T, a *App) Job {
	t.Helper()
	job, err := a.enqueue(context.Background(), "scrape", map[string]string{"id": "owned-asset"}, "wf-completion")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := a.claim(context.Background())
	if err != nil || claimed.ID != job.ID || claimed.Attempt != 1 {
		t.Fatalf("claim %+v %v", claimed, err)
	}
	return claimed
}
func jobEventCount(t *testing.T, a *App, job string) int {
	t.Helper()
	var count int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE job_id=?`, job).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
func TestFinishJobFailureAndDomainFactShareCommit(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	job := completionRunningJob(t, a)
	before := jobEventCount(t, a, job.ID)
	changed, err := a.finishJob(ctx, job, "failed", "SYNTHETIC_FAILURE", "owned test failure", time.Now().Unix())
	if err != nil || !changed {
		t.Fatal("failure completion", changed, err)
	}
	stored, err := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
	if err != nil || stored.State != "failed" || stored.Attempt != 1 || stored.ErrorCode != "SYNTHETIC_FAILURE" || stored.LeaseUntil != 0 {
		t.Fatalf("stored failed job %+v %v", stored, err)
	}
	if jobEventCount(t, a, job.ID) != before+1 {
		t.Fatal("committed failure lacks exactly one timeline fact")
	}
	page, err := a.domainEvents(ctx, 0, 100)
	if err != nil || len(page.Events) != 1 {
		t.Fatalf("domain facts %+v %v", page, err)
	}
	event := page.Events[0]
	if event.Type != "job.failed" || event.JobID != job.ID || event.WorkflowID != job.WorkflowID || event.SubjectID != job.ID || event.SchemaVersion != 1 || event.Purpose != materialPurpose {
		t.Fatalf("failure lost versioned identity %+v", event)
	}
	changed, err = a.finishJob(ctx, job, "succeeded", "", "", time.Now().Unix())
	if err != nil || changed || jobEventCount(t, a, job.ID) != before+1 {
		t.Fatal("stale terminal completion emitted a fake success", changed, err)
	}
}
func TestFinishJobRollsBackStateAndTimelineWhenDomainPersistenceFails(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	job := completionRunningJob(t, a)
	before := jobEventCount(t, a, job.ID)
	if _, err := a.domainEvents(ctx, 0, 100); err != nil {
		t.Fatal(err)
	}
	_, err := a.Store.DB.ExecContext(ctx, `CREATE TRIGGER deny_failure_domain BEFORE INSERT ON domain_events WHEN json_extract(NEW.payload,'$.event_type')='job.failed' BEGIN SELECT RAISE(ABORT,'synthetic outbox unavailable'); END`)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := a.finishJob(ctx, job, "failed", "SYNTHETIC_FAILURE", "test", time.Now().Unix())
	if err == nil || changed {
		t.Fatal("failed outbox claimed committed terminal state", changed, err)
	}
	stored, err := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
	if err != nil || stored.State != "running" || stored.ErrorCode != "" || jobEventCount(t, a, job.ID) != before {
		t.Fatalf("transaction leaked state or timeline %+v %v", stored, err)
	}
	page, err := a.domainEvents(ctx, 0, 100)
	if err != nil || len(page.Events) != 0 {
		t.Fatal("uncommitted job.failed escaped", page, err)
	}
	if _, err = a.Store.DB.ExecContext(ctx, `DROP TRIGGER deny_failure_domain`); err != nil {
		t.Fatal(err)
	}
	changed, err = a.finishJob(ctx, job, "failed", "SYNTHETIC_FAILURE", "test", time.Now().Unix())
	if err != nil || !changed {
		t.Fatal("retry after storage recovery", changed, err)
	}
}
func TestFinishJobWaitingPausePreservesAttemptAndRejectsStaleGeneration(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	job := completionRunningJob(t, a)
	before := jobEventCount(t, a, job.ID)
	if _, err := a.Store.DB.ExecContext(ctx, `UPDATE jobs SET generation=generation+1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	changed, err := a.finishJob(ctx, job, "failed", "STALE", "old lease", time.Now().Unix())
	if err != nil || changed || jobEventCount(t, a, job.ID) != before {
		t.Fatal("old worker generation fabricated a failure", changed, err)
	}
	page, err := a.domainEvents(ctx, 0, 100)
	if err != nil || len(page.Events) != 0 {
		t.Fatal("stale worker generated domain failure", page, err)
	}
	job.Generation++
	changed, err = a.finishJob(ctx, job, "waiting_paused", "AUTOMATION_PAUSED", "waiting for resume", time.Now().Unix())
	if err != nil || !changed {
		t.Fatal("pause completion", changed, err)
	}
	stored, err := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
	if err != nil || stored.State != "waiting_paused" || stored.Attempt != 0 {
		t.Fatalf("pause consumed retry %+v %v", stored, err)
	}
	page, err = a.domainEvents(ctx, 0, 100)
	if err != nil || len(page.Events) != 0 {
		t.Fatal("pause produced job.failed", page, err)
	}
	if _, err = a.Store.DB.ExecContext(ctx, `UPDATE jobs SET state='queued',run_at=0 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	resumed, err := a.claim(ctx)
	if err != nil || resumed.Attempt != 1 {
		t.Fatalf("resumed attempt %+v %v", resumed, err)
	}
	changed, err = a.finishJob(ctx, resumed, "succeeded", "", "", time.Now().Unix())
	if err != nil || !changed {
		t.Fatal("resumed completion", changed, err)
	}
	var raw []byte
	if err = a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM jobs WHERE id=?`, job.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil || payload["id"] != "owned-asset" {
		t.Fatal("completion rewrote retry input")
	}
}
