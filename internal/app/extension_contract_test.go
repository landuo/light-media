package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func domainFixture(typ, key string) DomainEvent {
	return DomainEvent{EventID: domainEventID(typ, key), Type: typ, WorkflowID: "wf_fixture", JobID: "job_fixture", ParentJobID: "job_parent", MediaID: "movie:123", IntentID: "intent_fixture", AssetID: "asset_fixture"}
}
func commitDomainFixture(t *testing.T, a *App, event DomainEvent) {
	t.Helper()
	tx, e := a.Store.DB.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = recordDomainEventTx(context.Background(), tx, event); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
}
func TestDomainEventSharesBusinessCommitAndRollback(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	if _, e := a.domainEvents(ctx, 0, 100); e != nil {
		t.Fatal(e)
	}
	event := domainFixture("download.accepted", "intent_fixture")
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = txPut(ctx, tx, "intent", "intent_fixture", map[string]string{"state": "accepted"}); e != nil {
		t.Fatal(e)
	}
	if e = recordDomainEventTx(ctx, tx, event); e != nil {
		t.Fatal(e)
	}
	_ = tx.Rollback()
	page, e := a.domainEvents(ctx, 0, 100)
	if e != nil || len(page.Events) != 0 {
		t.Fatal("uncommitted event escaped", page, e)
	}
	var state any
	if e = a.Store.Get(ctx, "intent", "intent_fixture", &state); e != sql.ErrNoRows {
		t.Fatal("business transaction was not rolled back", e)
	}
	tx, e = a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = txPut(ctx, tx, "intent", "intent_fixture", map[string]string{"state": "accepted"}); e != nil {
		t.Fatal(e)
	}
	if e = recordDomainEventTx(ctx, tx, event); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	page, e = a.domainEvents(ctx, 0, 100)
	if e != nil || len(page.Events) != 1 || page.Events[0].SubjectID != "intent_fixture" || page.Events[0].SchemaVersion != 1 || page.Events[0].Purpose != "core_download" || page.Events[0].Origin != "core" {
		t.Fatal(page, e)
	}
}
func TestDomainEventReplayDeduplicatesAndRejectsReferenceRewrite(t *testing.T) {
	a := newTestApp(t)
	event := domainFixture("scrape.completed", "asset_fixture:job_fixture")
	commitDomainFixture(t, a, event)
	first, _ := a.domainEvents(context.Background(), 0, 100)
	event.OccurredAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	commitDomainFixture(t, a, event)
	again, e := a.domainEvents(context.Background(), 0, 100)
	if e != nil || len(again.Events) != 1 || again.Events[0] != first.Events[0] {
		t.Fatal("retry rewrote event or advanced sequence", again, e)
	}
	event.AssetID = "asset_other"
	tx, e := a.Store.DB.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = recordDomainEventTx(context.Background(), tx, event); e == nil {
		t.Fatal("same ID changed subject")
	}
	_ = tx.Rollback()
}
func TestDomainEventWhitelistRejectsAuthorityAndSecretPayloadShapes(t *testing.T) {
	a := newTestApp(t)
	base := domainFixture("import.completed", "import")
	cases := []DomainEvent{}
	for _, change := range []func(*DomainEvent){func(e *DomainEvent) { e.SchemaVersion = 2 }, func(e *DomainEvent) { e.Type = "plugin.execute" }, func(e *DomainEvent) { e.Purpose = "cross_seed" }, func(e *DomainEvent) { e.Origin = "plugin" }, func(e *DomainEvent) { e.AssetID = "/private/secret/file" }, func(e *DomainEvent) { e.WorkflowID = "https://private.invalid/?token=secret" }, func(e *DomainEvent) { e.JobID = "Cookie: private" }, func(e *DomainEvent) { e.SubjectID = "asset_unrelated" }, func(e *DomainEvent) { e.EventID = "user-defined-no-version" }} {
		v := base
		change(&v)
		cases = append(cases, v)
	}
	for _, event := range cases {
		tx, e := a.Store.DB.BeginTx(context.Background(), nil)
		if e != nil {
			t.Fatal(e)
		}
		e = recordDomainEventTx(context.Background(), tx, event)
		_ = tx.Rollback()
		if e == nil {
			t.Fatal("unsupported event accepted", event.Type, event.Purpose, event.Origin)
		}
	}
	commitDomainFixture(t, a, base)
	page, e := a.domainEvents(context.Background(), 0, 100)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(page)
	for _, forbidden := range []string{"payload", "credential", "cookie", "token", "path", "torrent", "plugin_id"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("unbounded/private event field", forbidden)
		}
	}
	fail := DomainEvent{EventID: domainEventID("job.failed", "job_without_media:1"), Type: "job.failed", JobID: "job_without_media"}
	commitDomainFixture(t, a, fail)
}
func TestDomainEventCursorSurvivesRestartAndReplaysSameIDs(t *testing.T) {
	dir := t.TempDir()
	a, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	for _, typ := range []string{"download.accepted", "download.completed", "import.completed", "scrape.completed", "library.visible", "job.failed"} {
		commitDomainFixture(t, a, domainFixture(typ, typ))
	}
	first, e := a.domainEvents(context.Background(), 0, 2)
	if e != nil || first.Complete || len(first.Events) != 2 || first.NextCursor != first.Events[1].Sequence {
		a.Close()
		t.Fatal(first, e)
	}
	repeated, _ := a.domainEvents(context.Background(), 0, 2)
	if repeated.Events[0] != first.Events[0] {
		a.Close()
		t.Fatal("at-least-once replay changed event")
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	b, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	next, e := b.domainEvents(context.Background(), first.NextCursor, 200)
	if e != nil || len(next.Events) != 4 || !next.Complete || next.Events[0].Sequence <= first.NextCursor {
		t.Fatal("persisted event cursor lost", next, e)
	}
	for _, args := range []struct {
		after int64
		limit int
	}{{-1, 1}, {0, 0}, {0, 201}} {
		if _, e = b.domainEvents(context.Background(), args.after, args.limit); e == nil {
			t.Fatal("unbounded cursor accepted")
		}
	}
}
func TestDomainEventsFollowImportAndScrapeFactsOnce(t *testing.T) {
	a, source, _ := importTestApp(t)
	importFixture(t, a, "movie")
	path := source + "/Test.2024.mkv"
	importWrite(t, path, []byte("own video"))
	plan, e := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: path}}})
	if e != nil {
		t.Fatal(e)
	}
	job, e := a.executeImport(context.Background(), plan.ID, nil)
	if e != nil {
		t.Fatal(e)
	}
	job.Generation = 1
	job.State = "running"
	_, _ = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, job.ID)
	if e = a.runImport(context.Background(), job, plan.ID, nil); e != nil {
		t.Fatal(e)
	}
	var saved ImportPlan
	if e = a.Store.Get(context.Background(), "import_plan", plan.ID, &saved); e != nil {
		t.Fatal(e)
	}
	var scrape Job
	scrape, e = scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='scrape' LIMIT 1`))
	if e != nil {
		t.Fatal(e)
	}
	scrape.Generation = 1
	scrape.State = "running"
	_, _ = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, scrape.ID)
	for i := 0; i < 2; i++ {
		if e = a.runScrape(context.Background(), scrape, saved.Files[0].AssetID); e != nil {
			t.Fatal(e)
		}
	}
	page, e := a.domainEvents(context.Background(), 0, 200)
	if e != nil {
		t.Fatal(e)
	}
	counts := map[string]int{}
	for _, event := range page.Events {
		counts[event.Type]++
		if event.AssetID != saved.Files[0].AssetID || event.WorkflowID == "" || event.JobID == "" || event.SubjectID != event.AssetID {
			t.Fatal("event lost source links", event)
		}
	}
	if counts["import.completed"] != 1 || counts["scrape.completed"] != 1 || counts["library.visible"] != 0 {
		t.Fatal("event preceded fact, duplicated retry or FNOS disabled visible", counts)
	}
}

func TestDomainDownloadObservationWithoutJobDoesNotInventID(t *testing.T) {
	a := newTestApp(t)
	event := domainFixture("download.completed", "intent_fixture")
	event.JobID = ""
	event.ParentJobID = ""
	commitDomainFixture(t, a, event)
	page, e := a.domainEvents(context.Background(), 0, 100)
	if e != nil || len(page.Events) != 1 || page.Events[0].JobID != "" || page.Events[0].SubjectID != "intent_fixture" {
		t.Fatal(page, e)
	}
}
