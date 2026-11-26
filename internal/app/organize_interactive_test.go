package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/catalog"
)

func TestInteractiveOrganizeReturnsPreviewAndPublishesAcceptedFile(t *testing.T) {
	a, src, library := importTestApp(t)
	cookie := setupTestAdmin(t, a)
	importFixture(t, a, "tv")
	importCache(t, a, "season:123:2", catalog.Season{SeriesID: 123, SeasonNumber: 2, Episodes: []catalog.Episode{{ID: 321, SeasonNumber: 2, EpisodeNumber: 7, Name: "Episode"}}})
	path := filepath.Join(src, "Test.Series.S02E07.mkv")
	target := filepath.Join(library, "selected")
	if err := os.Mkdir(target, 0750); err != nil {
		t.Fatal(err)
	}
	importWrite(t, path, []byte("synthetic television episode"))
	request := manualOrganizeRequest{Interactive: true, Preview: true, Paths: []string{path}, TargetPath: target, TMDBID: 123, MediaType: "tv", Mode: "hardlink", Scraping: importFlag(false)}
	call := func(in manualOrganizeRequest, want int) manualOrganizeOutput {
		t.Helper()
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		resp := callTest(t, a.Handler(), http.MethodPost, "/api/files/organize", string(b), cookie, "")
		if resp.Code != want {
			t.Fatalf("status %d body %s", resp.Code, resp.Body)
		}
		var r struct{ Data manualOrganizeOutput }
		if err := json.Unmarshal(resp.Body.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if want == 200 {
			var pending, audits int
			if err := a.organizeInbox.db.QueryRow("SELECT count(*) FROM audit WHERE request_id=?", resp.Header().Get("X-Request-ID")).Scan(&pending); err != nil {
				t.Fatal(err)
			}
			if err := a.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE json_extract(payload,'$.request_id')=? AND json_extract(payload,'$.http_status')=200`, resp.Header().Get("X-Request-ID")).Scan(&audits); err != nil || audits+pending != 1 {
				t.Fatal("request audit", audits, err)
			}
		}
		return r.Data
	}
	preview := call(request, 200)
	if len(preview.Items) != 1 || preview.Items[0].Status != "ready" || preview.Items[0].PlanID != "" || !within(target, preview.Items[0].Target) {
		t.Fatalf("preview %+v", preview)
	}
	var count int
	if err := a.Store.DB.QueryRow("SELECT count(*) FROM import_plan").Scan(&count); err != nil || count != 0 {
		t.Fatal("ephemeral preview persisted a plan", count, err)
	}
	for _, r := range a.Settings().Roots {
		if r.Path == target {
			t.Fatal("preview saved target")
		}
	}
	request.Preview = false
	accepted := call(request, 200)
	if accepted.Submitted != 1 || len(accepted.Items) != 1 || accepted.Items[0].JobID == "" || accepted.Items[0].PlanID != "" {
		t.Fatalf("not durably queued: %+v", accepted)
	}
	jobID := accepted.Items[0].JobID
	if retry := call(request, 200); retry.Items[0].JobID != jobID {
		t.Fatalf("retry duplicated queue item: %+v", retry)
	}
	run := func(kind string) {
		t.Helper()
		if _, err := a.Store.DB.Exec("UPDATE jobs SET state='running',generation=generation+1 WHERE id=? AND kind=? AND state='queued'", jobID, kind); err != nil {
			t.Fatal(err)
		}
		job, err := scanJob(a.Store.DB.QueryRow("SELECT "+jobColumns+" FROM jobs WHERE id=?", jobID))
		if err != nil || job.Kind != kind || job.State != "running" {
			t.Fatal(kind, job, err)
		}
		a.runClaimedJob(t.Context(), job)
	}
	if ok, err := a.deliverOrganizeInbox(t.Context()); err != nil || !ok {
		t.Fatal("durable handoff", ok, err)
	}
	run("organize_file")
	if retry := call(request, 200); retry.Items[0].JobID != jobID {
		t.Fatal("retry after recognition duplicated import")
	}
	run("import")
	var actual string
	if err := a.Store.DB.QueryRow("SELECT dest FROM transferhistory WHERE src=? AND status=1", path).Scan(&actual); err != nil || actual != preview.Items[0].Target {
		t.Fatalf("published path %q preview %q error %v", actual, preview.Items[0].Target, err)
	}
	s, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := os.Stat(actual)
	if err != nil || !os.SameFile(s, d) {
		t.Fatal("source and target are not hardlinks", err)
	}
}

func TestInteractiveOrganizePreparationReturnsEachFileAndSkip(t *testing.T) {
	a, source, target := importTestApp(t)
	cookie := setupTestAdmin(t, a)
	episode := filepath.Join(source, "Show.E07.mkv")
	excluded := filepath.Join(source, "Unnumbered.mkv")
	for _, path := range []string{episode, excluded} {
		importWrite(t, path, []byte("fixture"))
	}
	form := manualOrganizeRequest{Interactive: true, Paths: []string{episode, excluded}, Mode: "hardlink", TargetPath: target, EpisodeFormat: "Show.E{ep}.mkv"}
	submit := func(in manualOrganizeRequest) manualOrganizeOutput {
		t.Helper()
		raw, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		response := callTest(t, a.Handler(), http.MethodPost, "/api/files/organize", string(raw), cookie, "")
		var out struct{ Data manualOrganizeOutput }
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil || response.Code != http.StatusOK {
			t.Fatalf("submit: %d %s %v", response.Code, response.Body, err)
		}
		return out.Data
	}
	run := func(id string) manualOrganizeOutput {
		t.Helper()
		if ok, err := a.deliverOrganizeInbox(t.Context()); err != nil || !ok {
			t.Fatal("durable prepare handoff", ok, err)
		}
		job, err := a.claim(t.Context())
		if err != nil || job.ID != id || job.Kind != "organize_prepare" {
			t.Fatalf("claim: %s, %v", job.ID, err)
		}
		a.runClaimedJob(t.Context(), job)
		response := callTest(t, a.Handler(), http.MethodGet, "/api/jobs/"+id+"?view=organize", "", cookie, "")
		var out struct {
			Data struct {
				Job         Job
				Preparation *manualOrganizeOutput
				Decisions   json.RawMessage
				Events      json.RawMessage
			}
		}
		if err = json.Unmarshal(response.Body.Bytes(), &out); err != nil || response.Code != http.StatusOK || out.Data.Preparation == nil || out.Data.Job.ID != job.ID || len(out.Data.Decisions)+len(out.Data.Events) > 0 {
			t.Fatalf("prepare results: %d %s %v", response.Code, response.Body, err)
		}
		return *out.Data.Preparation
	}
	skips := form
	skips.Paths = []string{excluded}
	if out := submit(skips); out.Submitted != 0 || out.Failed != 0 || len(out.Items) != 1 || out.Items[0].Status != "skipped" || out.PreparationJobID != "" {
		t.Fatalf("direct skip: %+v", out)
	}
	missing := form
	missing.Paths, missing.HistoryIDs = nil, []string{"100000", "100001"}
	missingID := submit(missing).PreparationJobID
	if result := run(missingID); result.Failed != 2 || result.Submitted != 0 || len(result.Items) != 2 {
		t.Fatalf("missing history: %+v", result)
	}
	parentID := submit(form).PreparationJobID
	if parentID == "" || submit(form).PreparationJobID != parentID {
		t.Fatal("repeated selection did not resume the existing preparation")
	}
	out := run(parentID)
	if out.Total != 2 || out.Submitted != 1 || out.Failed != 0 {
		t.Fatalf("multi preparation: %+v", out)
	}
	for _, item := range out.Items {
		switch item.Source {
		case excluded:
			if item.Status != "skipped" || item.JobID != "" {
				t.Fatalf("excluded %+v", item)
			}
		case episode:
			if item.Status != "queued" || item.JobID == "" {
				t.Fatalf("accepted %+v", item)
			}
		default:
			t.Fatalf("unknown %+v", item)
		}
	}
	folder := form
	folder.EpisodeFormat = ""
	folder.Paths = []string{source}
	if out := submit(folder); out.PreparationJobID == "" || out.Submitted != 0 {
		t.Fatalf("folder did not return durable preparation: %+v", out)
	}
}

func TestOrganizeInboxReplaysAcrossBothCommitBoundaries(t *testing.T) {
	a, src, target := importTestApp(t)
	ctx := t.Context()
	if err := a.saveSettings(ctx, a.Settings()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(src, "Fallout.S02E07.mkv")
	importWrite(t, path, []byte("independent test media"))
	form := manualOrganizeRequest{Interactive: true, Paths: []string{path}, TargetPath: target, Mode: "hardlink", audit: &organizeRequestAudit{id: "audit_fixture_1", request: "req_fixture_1"}}
	main, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer main.Rollback()
	// Holding the production writer must not prevent a durable admission or
	// an equivalent retry. Conflicting selections cannot acquire the source.
	out, handled, err := a.queueInteractiveOrganizeFile(ctx, form)
	if err != nil || !handled || len(out.Items) != 1 {
		t.Fatal(out, handled, err)
	}
	jobID := out.Items[0].JobID
	form.audit = &organizeRequestAudit{id: "audit_fixture_2", request: "req_fixture_2"}
	again, _, err := a.queueInteractiveOrganizeFile(ctx, form)
	if err != nil || again.Items[0].JobID != jobID {
		t.Fatal(again, err)
	}
	// Concurrent HTTP retries must share one stable ID, even while the main
	// writer is unavailable and the new job exists only in the inbox.
	type reply struct {
		id  string
		err error
	}
	replies := make(chan reply, 6)
	for range cap(replies) {
		request := form
		request.audit = nil
		go func() {
			accepted, _, err := a.queueInteractiveOrganizeFile(ctx, request)
			var id string
			if len(accepted.Items) == 1 {
				id = accepted.Items[0].JobID
			}
			replies <- reply{id, err}
		}()
	}
	for range cap(replies) {
		if r := <-replies; r.err != nil || r.id != jobID {
			t.Fatal(r)
		}
	}
	form.Mode = "copy"
	if _, _, err = a.queueInteractiveOrganizeFile(ctx, form); err == nil {
		t.Fatal("incompatible form accepted")
	}
	if err = main.Rollback(); err != nil {
		t.Fatal(err)
	}
	reopen := func(app *App) *App {
		t.Helper()
		if err := app.Close(); err != nil {
			t.Fatal(err)
		}
		resumed, err := New(Options{DataDir: app.Dir})
		if err != nil {
			t.Fatal(err)
		}
		return resumed
	}
	a = reopen(a)
	if job, err := a.pendingOrganizeJob(ctx, jobID); err != nil || job.State != "queued" {
		t.Fatal(job, err)
	}
	// Crash/failure before main commit: no half-written job, data, or audit.
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER reject_inbox_audit BEFORE INSERT ON audit_log BEGIN SELECT RAISE(ABORT,'fixture writer interrupted'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = a.deliverOrganizeInbox(ctx); err == nil {
		t.Fatal("failed main transaction acknowledged")
	}
	if _, err = a.Store.DB.Exec("DROP TRIGGER reject_inbox_audit"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.pendingOrganizeJob(ctx, jobID); err != nil {
		t.Fatal("inbox lost after main rollback", err)
	}
	var n int
	if err = a.Store.DB.QueryRow("SELECT COUNT(*) FROM jobs WHERE id=?", jobID).Scan(&n); err != nil || n != 0 {
		t.Fatal("main rollback", n, err)
	}
	// Crash after main commit but before retiring the receipt. Replay must keep
	// the very same main job and audit rows even after they reached a terminal state.
	if _, err = a.organizeInbox.db.Exec(`CREATE TRIGGER hold_receipt BEFORE DELETE ON request BEGIN SELECT RAISE(ABORT,'fixture interruption after main commit'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = a.deliverOrganizeInbox(ctx); err == nil {
		t.Fatal("failed receipt retirement was lost")
	}
	if _, err = a.Store.DB.Exec("UPDATE jobs SET state='succeeded' WHERE id=?", jobID); err != nil {
		t.Fatal(err)
	}
	a = reopen(a)
	defer a.Close()
	if changed, err := a.cancelPendingOrganizeJob(ctx, jobID); err != nil || changed {
		t.Fatal("stale receipt accepted cancellation", changed, err)
	}
	if _, err = a.organizeInbox.db.Exec("DROP TRIGGER hold_receipt"); err != nil {
		t.Fatal(err)
	}
	if ok, err := a.deliverOrganizeInbox(ctx); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := a.deliverOrganizeInbox(ctx); err != nil || ok {
		t.Fatal(ok, err)
	}
	var state string
	if err = a.Store.DB.QueryRow("SELECT state FROM jobs WHERE id=?", jobID).Scan(&state); err != nil || state != "succeeded" {
		t.Fatal(state, err)
	}
	if err = a.Store.DB.QueryRow("SELECT count(*) FROM jobs WHERE kind='organize_file'").Scan(&n); err != nil || n != 1 {
		t.Fatal("duplicate file transfer", n, err)
	}
	if err = a.Store.DB.QueryRow("SELECT count(*) FROM audit_log WHERE id IN ('audit_fixture_1','audit_fixture_2')").Scan(&n); err != nil || n != 2 {
		t.Fatal("audit count", n, err)
	}
}
