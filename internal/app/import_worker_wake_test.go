package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManualSubmissionWakesSleepingImportWorker(t *testing.T) {
	a, source, library := importTestApp(t)
	cookie := setupTestAdmin(t, a)
	movie := importFixture(t, a, "movie")
	path := filepath.Join(source, "Test.Film.2024.mkv")
	importWrite(t, path, []byte("real hardlink from a sleeping worker"))

	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	delayed, err := a.enqueue(ctx, "asset_check", map[string]string{"id": "later"}, "unrelated")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.ExecContext(ctx, "UPDATE jobs SET run_at=? WHERE id=?", time.Now().Add(15*time.Second).Unix(), delayed.ID); err != nil {
		t.Fatal(err)
	}

	// The empty import claim uses the read pool. Hold the sole writer until
	// the worker requests it for MIN(run_at), so it cannot win a startup race
	// and claim our submission before ever entering its idle wait.
	writer, err := a.Store.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	waits := a.Store.DB.Stats().WaitCount
	done := make(chan struct{})
	go func() { defer close(done); a.jobWorker(ctx, importJobs) }()
	defer func() { cancel(); <-done }()
	waitForSubscriptionWorker(t, ctx, func() (bool, error) { return a.Store.DB.Stats().WaitCount > waits, nil })
	writer.Close()

	out := manualProviderCall(t, a, cookie, manualOrganizeRequest{Paths: []string{path}, TargetPath: library, Mode: "hardlink", MediaType: "movie", TMDBID: movie.ID, Scraping: importFlag(false)})
	if out.Submitted != 1 || out.Failed != 0 || len(out.Items) != 1 {
		t.Fatalf("manual submission: %+v", out)
	}
	item := out.Items[0]
	var state string
	waitForSubscriptionWorker(t, ctx, func() (bool, error) {
		err := a.Store.reader().QueryRowContext(ctx, "SELECT state FROM jobs WHERE id=?", item.JobID).Scan(&state)
		return jobTerminal(state), err
	})
	if state != "succeeded" {
		t.Fatalf("manual import ended in %s", state)
	}
	var queued, started string
	if err := a.Store.reader().QueryRowContext(ctx, `SELECT MIN(CASE WHEN event='queued' THEN time END), MIN(CASE WHEN event='started' THEN time END) FROM events WHERE job_id=?`, item.JobID).Scan(&queued, &started); err != nil {
		t.Fatal(err)
	}
	q, err := time.Parse(time.RFC3339Nano, queued)
	if err != nil {
		t.Fatal(err)
	}
	s, err := time.Parse(time.RFC3339Nano, started)
	if err != nil {
		t.Fatal(err)
	}
	target, err := os.Stat(item.Target)
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.Stat(path)
	if err != nil || !os.SameFile(src, target) {
		t.Fatalf("worker did not publish the requested hardlink: %v", err)
	}
	t.Logf("queued to started %s, hardlink confirmed", s.Sub(q))
	if s.Sub(q) >= 8*time.Second {
		t.Fatal("manual submission waited for the unrelated future job timer")
	}
}
