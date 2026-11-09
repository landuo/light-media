package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"light-media/internal/catalog"
)

func historyUpsertOnly(t *testing.T, a *App, query string) transferHistoryItem {
	t.Helper()
	page := historyParityReadPage(t, a, query)
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("expected one current resource history, got total=%d items=%+v", page.Total, page.Items)
	}
	return page.Items[0]
}

func historyUpsertFileID(t *testing.T, item transferHistoryItem) string {
	t.Helper()
	var values map[string]any
	historyParityDecode(t, item, &values)
	fileID, _ := values["file_id"].(string)
	if fileID == "" {
		t.Fatalf("history lost its exact execution file ID: %+v", item)
	}
	return fileID
}

func historyUpsertAttempt(t *testing.T, a *App, jobID string) (ImportPlan, Job) {
	t.Helper()
	job, err := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, jobID))
	if err != nil || job.Kind != "import" {
		t.Fatalf("missing actual import execution %s: %+v %v", jobID, job, err)
	}
	var payload struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(job.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	var plan ImportPlan
	if err = a.Store.Get(context.Background(), "import_plan", payload.ID, &plan); err != nil || len(plan.Files) != 1 {
		t.Fatalf("missing single-file execution plan: %+v %v", plan, err)
	}
	return plan, job
}

func historyUpsertSubmit(t *testing.T, a *App, request manualOrganizeRequest, failed bool) manualOrganizeItem {
	t.Helper()
	out, err := a.manualOrganize(context.Background(), request)
	if err != nil || len(out.Items) != 1 {
		t.Fatalf("manual organization did not produce one actual attempt: %+v %v", out, err)
	}
	if failed && (out.Failed != 1 || out.Submitted != 0 || out.Items[0].JobID == "") {
		t.Fatalf("expected durable failed execution: %+v", out)
	}
	if !failed && (out.Failed != 0 || out.Submitted != 1 || out.Items[0].JobID == "") {
		t.Fatalf("expected actual queued execution: %+v", out)
	}
	return out.Items[0]
}

func TestHistoryUpsertRepeatedFailuresThenSuccessUpdatesOriginalRecord(t *testing.T) {
	a, source, target := importTestApp(t)
	identity := importFixture(t, a, "movie")
	path := filepath.Join(source, "unknown.mkv")
	importWrite(t, path, []byte("same isolated resource retained through retries"))
	first := historyUpsertSubmit(t, a, manualOrganizeRequest{Paths: []string{path}}, true)
	firstPlan, _ := historyUpsertAttempt(t, a, first.JobID)
	firstHistory := historyUpsertOnly(t, a, "")
	if firstHistory.ID != firstPlan.Files[0].ID || firstHistory.Status != "failed" {
		t.Fatalf("initial failed resource history mismatch: %+v", firstHistory)
	}
	second := historyUpsertSubmit(t, a, manualOrganizeRequest{Paths: []string{path}}, true)
	secondPlan, _ := historyUpsertAttempt(t, a, second.JobID)
	secondHistory := historyUpsertOnly(t, a, "")
	if secondHistory.ID != firstHistory.ID || historyUpsertFileID(t, secondHistory) != secondPlan.Files[0].ID || secondHistory.PlanID != secondPlan.ID {
		t.Fatalf("repeated failure created or reused the wrong execution: %+v", secondHistory)
	}
	scrape := false
	third := historyUpsertSubmit(t, a, manualOrganizeRequest{Paths: []string{path}, TargetPath: target, Mode: "copy", MediaType: "movie", TMDBID: identity.ID, Scraping: &scrape}, false)
	thirdPlan, job := historyUpsertAttempt(t, a, third.JobID)
	reorgRunJob(t, a, job)
	latest := historyUpsertOnly(t, a, "")
	if latest.ID != firstHistory.ID || historyUpsertFileID(t, latest) != thirdPlan.Files[0].ID || latest.PlanID != thirdPlan.ID || latest.Status != "success" {
		t.Fatalf("successful repeat did not update original visible history: %+v", latest)
	}
	if page := historyParityReadPage(t, a, "status=failed"); page.Total != 0 {
		t.Fatalf("old failed attempts leaked through current-state filter: %+v", page)
	}
	for _, old := range []ImportPlan{firstPlan, secondPlan, thirdPlan} {
		var frozen ImportPlan
		if err := a.Store.Get(context.Background(), "import_plan", old.ID, &frozen); err != nil || frozen.Files[0].ID != old.Files[0].ID {
			t.Fatal("visible upsert erased actual execution/recovery history", old.ID, err)
		}
		resolved, err := a.transferHistoryItem(context.Background(), old.Files[0].ID)
		if err != nil || resolved.ID != firstHistory.ID || resolved.PlanID != thirdPlan.ID {
			t.Fatalf("old history alias did not resolve current resource: %+v %v", resolved, err)
		}
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "same isolated resource retained through retries" {
		t.Fatal("history upsert changed source bytes", err)
	}
}

func TestTransferHistoryItemsForOrganizeKeepsSelectionOrderAndResolvesAliases(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	seed := func(name string) ImportPlan {
		t.Helper()
		job, err := a.recordManualOrganizeFailure(ctx, filepath.Join(source, name), id("wf_"), bad("REVIEW_IMPORT", "整理失败"))
		if err != nil {
			t.Fatal(err)
		}
		plan, _ := historyUpsertAttempt(t, a, job.ID)
		return plan
	}
	first := seed("first.mkv")
	legacy := seed("first.mkv")
	second := seed("second.mkv")
	latest := seed("first.mkv")
	selected := []string{second.Files[0].ID, legacy.Files[0].ID, first.Files[0].ID}
	items, err := a.transferHistoryItemsForOrganize(ctx, selected)
	if err != nil || len(items) != len(selected) {
		t.Fatalf("expected each selected history in the result: %+v %v", items, err)
	}
	want := []struct{ history, execution, plan string }{
		{second.Files[0].ID, second.Files[0].ID, second.ID},
		{first.Files[0].ID, latest.Files[0].ID, latest.ID},
		{first.Files[0].ID, latest.Files[0].ID, latest.ID},
	}
	for i, item := range items {
		if item.ID != want[i].history || item.FileID != want[i].execution || item.PlanID != want[i].plan {
			t.Fatalf("selection %d resolved the wrong history or execution: %+v, want %+v", i, item, want[i])
		}
	}
	if _, err := a.transferHistoryItemsForOrganize(ctx, []string{selected[0], "missing"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing selection must fail the entire lookup: %v", err)
	}
	if _, err := a.deleteTransferHistory(ctx, items[0], historyDeleteRequest{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.transferHistoryItemsForOrganize(ctx, selected); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("hidden history must not be available for organization: %v", err)
	}
}

func TestHistoryUpsertRepeatRecoversFailedUnpublishedReservation(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	movie := importFixture(t, a, "movie")
	path := filepath.Join(source, "Resident.Evil.2026.mkv")
	importWrite(t, path, []byte("video that was never published by the old plan"))
	noScrape := false
	request := manualOrganizeRequest{Paths: []string{path}, TargetPath: target, Mode: "hardlink", MediaType: "movie", TMDBID: movie.ID, Scraping: &noScrape}
	first := historyUpsertSubmit(t, a, request, false)
	old, oldJob := historyUpsertAttempt(t, a, first.JobID)
	old.State, old.Files[0].Status = "partial", "error"
	old.Files[0].Reason = "overwrite policy or synchronous event dependency is unknown"
	receipt := originalTransferPlannedReceipt(old, old.Files[0])
	receipt.OriginalNamingResolved = true
	if err := a.Store.Put(ctx, "import_plan", old.ID, old); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "import_receipt", receipt.ID, receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='failed',error_code='TASK_FAILED' WHERE id=?`, oldJob.ID); err != nil {
		t.Fatal(err)
	}
	initial := historyUpsertOnly(t, a, "status=failed")
	other := filepath.Join(source, "different-resource.mkv")
	importWrite(t, other, []byte("different source for the same movie"))
	different, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: movie.ID, RootID: old.RootID, Mode: "hardlink", Files: []ImportFileRequest{{Path: other}}})
	if err != nil || len(different.Files) != 1 || different.Files[0].Status != "blocked" {
		t.Fatalf("an unrelated source took over the old reservation: %+v %v", different.Files, err)
	}

	next := historyUpsertSubmit(t, a, request, false)
	replacement, newJob := historyUpsertAttempt(t, a, next.JobID)
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, oldJob.ID); err != nil {
		t.Fatal(err)
	}
	oldJob = reorgJob(t, a, "import", old.ID)
	if err := a.safeRun(ctx, oldJob); err == nil || fileCode(err) != "IMPORT_CONFLICT" {
		t.Fatalf("old job could publish after new submission: %v", err)
	}
	if _, err := os.Stat(old.Files[0].Target); !os.IsNotExist(err) {
		t.Fatalf("old job touched the target: %v", err)
	}
	if _, err := a.finishJob(ctx, oldJob, "failed", "IMPORT_CONFLICT", "old execution no longer owns the slot", 0); err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, newJob)
	latest := historyUpsertOnly(t, a, "status=success")
	if latest.ID != initial.ID || latest.PlanID != replacement.ID {
		t.Fatalf("fresh result did not update the same history: %+v", latest)
	}
	var kept importReceipt
	if err := a.Store.Get(ctx, "import_receipt", receipt.ID, &kept); err != nil || kept.ID != receipt.ID || kept.State != "planned" {
		t.Fatalf("old recovery receipt was changed: %+v %v", kept, err)
	}
	before, err1 := os.Stat(path)
	after, err2 := os.Stat(replacement.Files[0].Target)
	if err1 != nil || err2 != nil || !os.SameFile(before, after) {
		t.Fatalf("successful retry did not publish a hardlink: %v %v", err1, err2)
	}
}

func TestHistoryUpsertDownloadedSourceRetryKeepsIntent(t *testing.T) {
	a, source, target := importTestApp(t)
	overwriteRule(a, source, target, "latest", true)
	ctx := context.Background()
	movie := importFixture(t, a, "movie")
	path := filepath.Join(source, "Resident.Evil.2026.mkv")
	importWrite(t, path, []byte("video that was never published by the old plan"))
	noScrape := false
	request := manualOrganizeRequest{Paths: []string{path}, TargetPath: target, Mode: "hardlink", MediaType: "movie", TMDBID: movie.ID, Scraping: &noScrape}
	first := historyUpsertSubmit(t, a, request, false)
	old, oldJob := historyUpsertAttempt(t, a, first.JobID)
	old.State, old.Files[0].Status = "partial", "error"
	old.Files[0].Reason = "overwrite policy or synchronous event dependency is unknown"
	receipt := originalTransferPlannedReceipt(old, old.Files[0])
	receipt.OriginalNamingResolved = true
	if err := a.Store.Put(ctx, "import_plan", old.ID, old); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "import_receipt", receipt.ID, receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='failed',error_code='TASK_FAILED' WHERE id=?`, oldJob.ID); err != nil {
		t.Fatal(err)
	}
	// Production has the source hardlink at the destination but no committed
	// asset. A different target must remain protected; this exact inode is reusable.
	if err := os.MkdirAll(filepath.Dir(old.Files[0].Target), 0750); err != nil {
		t.Fatal(err)
	}
	importWrite(t, old.Files[0].Target, []byte("unrelated preexisting target"))
	if allowed, err := failedImportSourceUnpublished(ctx, a.Store.DB, old, old.Files[0], 0, true); err != nil || allowed {
		t.Fatalf("unrelated target was reusable: %v %v", allowed, err)
	}
	if err := os.Remove(old.Files[0].Target); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, old.Files[0].Target); err != nil {
		t.Fatal(err)
	}
	originalRule := a.Settings().Directories[0]
	intent := Intent{ID: "dl-retry-own-file", Media: old.Media, RootID: old.RootID, State: "downloaded", ImportPlanID: old.ID, DirectoryID: originalRule.ID, DirectorySnapshot: directoryFingerprint(originalRule)}
	old.IntentID = intent.ID
	if err := a.Store.Put(ctx, "intent", intent.ID, intent); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "import_plan", old.ID, old); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`UPDATE reservations SET intent_id=? WHERE intent_id=?`, intent.ID, old.ID); err != nil {
		t.Fatal(err)
	}
	initial := historyUpsertOnly(t, a, "status=failed")
	// History capabilities must not reacquire the single writer through the
	// download's season/post-add guards, even when the list index is clean.
	func() {
		writer, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Rollback()
		readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		result, err := a.transferHistory(readCtx, httptest.NewRequest("GET", "/api/history/transfers?status=failed", nil).WithContext(readCtx))
		if err != nil || readCtx.Err() != nil {
			t.Fatalf("download-linked history waited for writer: %v %v", err, readCtx.Err())
		}
		items := result.(map[string]any)["items"].([]transferHistoryItem)
		if len(items) != 1 || !items[0].CanRetry {
			t.Fatalf("read-only history lost retry capability: %+v", items)
		}
	}()
	other := filepath.Join(source, "different-resource.mkv")
	importWrite(t, other, []byte("different source for the same movie"))
	different, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: movie.ID, RootID: old.RootID, Mode: "hardlink", Files: []ImportFileRequest{{Path: other}}})
	if err != nil || len(different.Files) != 1 || different.Files[0].Status != "blocked" {
		t.Fatalf("an unrelated source took over the old reservation: %+v %v", different.Files, err)
	}

	// A running original transfer cannot be taken over, even for the same file.
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='running' WHERE id=?`, oldJob.ID); err != nil {
		t.Fatal(err)
	}
	blocked, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{path}, TargetPath: target, Mode: "hardlink", MediaType: "movie", TMDBID: movie.ID, Scraping: &noScrape, Preview: true})
	if err != nil || blocked.Failed != 1 {
		t.Fatalf("active download import was replaced: %+v %v", blocked, err)
	}
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='failed' WHERE id=?`, oldJob.ID); err != nil {
		t.Fatal(err)
	}
	// Preview binds neither the intent nor its reservation to the new plan.
	preview, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{path}, TargetPath: target, Mode: "hardlink", MediaType: "movie", TMDBID: movie.ID, Scraping: &noScrape, Preview: true})
	if err != nil || preview.Failed != 0 {
		t.Fatalf("own downloaded source preview blocked: %+v %v", preview, err)
	}
	var unchanged Intent
	if err := a.Store.Get(ctx, "intent", intent.ID, &unchanged); err != nil || unchanged.ImportPlanID != old.ID {
		t.Fatalf("preview changed download: %+v %v", unchanged, err)
	}
	// A confirmed directory-policy correction must use the new manual
	// selection without erasing the original download destination snapshot.
	overwriteRule(a, source, target, "always", true)
	next := historyUpsertSubmit(t, a, request, false)
	replacement, newJob := historyUpsertAttempt(t, a, next.JobID)
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, oldJob.ID); err != nil {
		t.Fatal(err)
	}
	oldJob = reorgJob(t, a, "import", old.ID)
	if err := a.safeRun(ctx, oldJob); err == nil || (fileCode(err) != "REVIEW_IMPORT_REPLACED" && fileCode(err) != "REVIEW_DIRECTORY_CHANGED") {
		t.Fatalf("old job could publish after new submission: %v", err)
	}
	if stamp, err := snapshotPath(old.Files[0].Target); err != nil || stamp != planSnapshot(old.Files[0]) {
		t.Fatalf("old job changed source hardlink: %v", err)
	}
	if _, err := a.finishJob(ctx, oldJob, "failed", "IMPORT_CONFLICT", "old execution no longer owns the slot", 0); err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, newJob)
	var completed Intent
	if err := a.Store.Get(ctx, "intent", intent.ID, &completed); err != nil || completed.State != "imported" || completed.ImportPlanID != replacement.ID || completed.DirectorySnapshot != intent.DirectorySnapshot {
		t.Fatalf("download was not completed: %+v %v", completed, err)
	}
	var asset Asset
	if err := a.Store.DB.QueryRow(`SELECT json_extract(payload,'$.intent_id') FROM records WHERE kind='asset' AND json_extract(payload,'$.path')=?`, replacement.Files[0].Target).Scan(&asset.IntentID); err != nil || asset.IntentID != intent.ID {
		t.Fatalf("asset lost download provenance: %+v %v", asset, err)
	}
	latest := historyUpsertOnly(t, a, "status=success")
	if latest.ID != initial.ID || latest.PlanID != replacement.ID {
		t.Fatalf("fresh result did not update the same history: %+v", latest)
	}
	var kept importReceipt
	if err := a.Store.Get(ctx, "import_receipt", receipt.ID, &kept); err != nil || kept.ID != receipt.ID || kept.State != "planned" {
		t.Fatalf("old recovery receipt was changed: %+v %v", kept, err)
	}
	before, err1 := os.Stat(path)
	after, err2 := os.Stat(replacement.Files[0].Target)
	if err1 != nil || err2 != nil || !os.SameFile(before, after) {
		t.Fatalf("successful retry did not publish a hardlink: %v %v", err1, err2)
	}
}

func TestHistoryUpsertNewFailureReplacesSuccessBeforeStatusFiltering(t *testing.T) {
	a, path, old := reorgFixture(t)
	first := historyUpsertOnly(t, a, "")
	failed := historyUpsertSubmit(t, a, manualOrganizeRequest{Paths: []string{path}, TargetPath: filepath.Dir(filepath.Dir(old.Path)), Mode: "not-a-mode", MediaType: "movie", TMDBID: old.Media.ID}, true)
	failedPlan, _ := historyUpsertAttempt(t, a, failed.JobID)
	latest := historyUpsertOnly(t, a, "status=failed")
	if latest.ID != first.ID || latest.PlanID != failedPlan.ID || historyUpsertFileID(t, latest) != failedPlan.Files[0].ID {
		t.Fatalf("latest failure did not replace prior success: %+v", latest)
	}
	if page := historyParityReadPage(t, a, "status=success"); page.Total != 0 {
		t.Fatalf("filter selected obsolete successful attempt: %+v", page)
	}
	for _, file := range []string{path, old.Path} {
		if _, err := os.Stat(file); err != nil {
			t.Fatal("failed attempt changed original source/managed target", file, err)
		}
	}
}

func TestHistoryUpsertChangedIdentityAndTargetSearchOnlyCurrentResult(t *testing.T) {
	a, source, old := reorgFixture(t)
	first := historyUpsertOnly(t, a, "")
	corrected := old.Media
	corrected.ID, corrected.Title = 456, "已更正的影片名称"
	importCache(t, a, "media:movie:456", corrected)
	target := filepath.Join(filepath.Dir(source), "corrected-target")
	if err := os.MkdirAll(target, 0750); err != nil {
		t.Fatal(err)
	}
	scrape := false
	result := historyUpsertSubmit(t, a, manualOrganizeRequest{HistoryIDs: []string{first.ID}, TargetPath: target, Mode: "copy", MediaType: "movie", TMDBID: corrected.ID, Scraping: &scrape}, false)
	_, job := historyUpsertAttempt(t, a, result.JobID)
	reorgRunJob(t, a, job)
	reorgRunJob(t, a, reorgJob(t, a, "reorganize_cleanup", result.PlanID))
	latest := historyUpsertOnly(t, a, url.Values{"q": {corrected.Title}}.Encode())
	if latest.ID != first.ID || latest.Media.ID != corrected.ID || latest.Target == old.Path || latest.Target != result.Target {
		t.Fatalf("history retained obsolete target/identity: %+v", latest)
	}
	for _, query := range []string{old.Media.Title, old.Path} {
		if page := historyParityReadPage(t, a, url.Values{"q": {query}}.Encode()); page.Total != 0 {
			t.Fatalf("search leaked old attempt %q: %+v", query, page)
		}
	}
}

func TestHistoryUpsertPreviewCannotReplaceExecutedHistory(t *testing.T) {
	a, source, old := reorgFixture(t)
	first := historyUpsertOnly(t, a, "")
	target := filepath.Join(filepath.Dir(source), "preview-target")
	if err := os.MkdirAll(target, 0750); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"copy", "not-a-mode"} {
		out, err := a.manualOrganize(context.Background(), manualOrganizeRequest{HistoryIDs: []string{first.ID}, TargetPath: target, Mode: mode, MediaType: "movie", TMDBID: old.Media.ID, Preview: true})
		if err != nil || len(out.Items) != 1 || out.Submitted != 0 {
			t.Fatalf("unexpected preview result: %+v %v", out, err)
		}
		latest := historyUpsertOnly(t, a, "")
		if latest.ID != first.ID || latest.PlanID != first.PlanID || latest.JobID != first.JobID || latest.Status != "success" {
			t.Fatalf("preview changed actual history: %+v", latest)
		}
	}
	if _, err := os.Stat(old.Path); err != nil {
		t.Fatal("preview modified original target", err)
	}
}

func TestHistoryUpsertSameSeriesSeparateEpisodeFilesRemainSeparate(t *testing.T) {
	a, source, target := importTestApp(t)
	identity := importFixture(t, a, "tv")
	importCache(t, a, "season:123:1", catalog.Season{ID: 901, SeriesID: identity.ID, SeasonNumber: 1, Episodes: []catalog.Episode{{ID: 7101, SeasonNumber: 1, EpisodeNumber: 1, Name: "第一集"}, {ID: 7102, SeasonNumber: 1, EpisodeNumber: 2, Name: "第二集"}}})
	season := 1
	scrape := false
	ids := map[string]bool{}
	for _, item := range []struct{ name, episode string }{{"Test.Show.S01E01.mkv", "1"}, {"Test.Show.S01E02.mkv", "2"}} {
		input := filepath.Join(source, item.name)
		importWrite(t, input, []byte("isolated episode "+item.episode))
		out := historyUpsertSubmit(t, a, manualOrganizeRequest{Paths: []string{input}, TargetPath: target, Mode: "copy", MediaType: "tv", TMDBID: identity.ID, Season: &season, EpisodeDetail: item.episode, Scraping: &scrape}, false)
		plan, job := historyUpsertAttempt(t, a, out.JobID)
		reorgRunJob(t, a, job)
		ids[plan.Files[0].ID] = true
	}
	page := historyParityReadPage(t, a, "status=success&page_size=1")
	if page.Total != 2 || len(page.Items) != 1 || !ids[page.Items[0].ID] {
		t.Fatalf("same TMDB merged separate episodes or paginated before upsert: %+v", page)
	}
	page = historyParityReadPage(t, a, "status=success&page_size=1000")
	if len(page.Items) != 2 || page.Items[0].Source == page.Items[1].Source || page.Items[0].Media.ID != identity.ID || page.Items[1].Media.ID != identity.ID {
		t.Fatalf("different files for same series did not remain independent: %+v", page)
	}
}

func TestHistoryUpsertOldExecutionFinishingLateCannotReplaceNewAttempt(t *testing.T) {
	a, source, target := importTestApp(t)
	identity := importFixture(t, a, "movie")
	input := filepath.Join(source, "Late.Movie.2024.mkv")
	importWrite(t, input, []byte("late successful isolated source"))
	scrape := false
	first := historyUpsertSubmit(t, a, manualOrganizeRequest{Paths: []string{input}, TargetPath: target, Mode: "copy", MediaType: "movie", TMDBID: identity.ID, Scraping: &scrape}, false)
	firstPlan, firstJob := historyUpsertAttempt(t, a, first.JobID)
	second := historyUpsertSubmit(t, a, manualOrganizeRequest{Paths: []string{input}, TargetPath: target, Mode: "not-a-mode", MediaType: "movie", TMDBID: identity.ID}, true)
	secondPlan, _ := historyUpsertAttempt(t, a, second.JobID)
	// This writes the older plan and job after the newer attempt was committed.
	// Visibility must follow submission order, not completion/record timestamps.
	reorgRunJob(t, a, firstJob)
	if _, err := a.Store.DB.Exec(`UPDATE records SET updated_at='2099-12-31T23:59:59Z' WHERE kind='import_plan' AND id=?`, firstPlan.ID); err != nil {
		t.Fatal(err)
	}
	latest := historyUpsertOnly(t, a, "")
	if latest.ID != firstPlan.Files[0].ID || latest.PlanID != secondPlan.ID || latest.Status != "failed" || historyUpsertFileID(t, latest) != secondPlan.Files[0].ID {
		t.Fatalf("older late completion replaced newer submitted attempt: %+v", latest)
	}
	if page := historyParityReadPage(t, a, "status=success"); page.Total != 0 {
		t.Fatalf("late old success leaked through status filter: %+v", page)
	}
	if got, err := os.ReadFile(first.Target); err != nil || string(got) != "late successful isolated source" {
		t.Fatal("late execution did not finish its actual immutable plan", err)
	}
}

func TestHistoryUpsertQueueCancelAddressesOldExecutionNotLatestHistory(t *testing.T) {
	a, source, target := importTestApp(t)
	identity := importFixture(t, a, "movie")
	input := filepath.Join(source, "Queued.Movie.2024.mkv")
	importWrite(t, input, []byte("queued resource remains unchanged"))
	scrape := false
	first := historyUpsertSubmit(t, a, manualOrganizeRequest{Paths: []string{input}, TargetPath: target, Mode: "copy", MediaType: "movie", TMDBID: identity.ID, Scraping: &scrape}, false)
	firstPlan, firstJob := historyUpsertAttempt(t, a, first.JobID)
	second := historyUpsertSubmit(t, a, manualOrganizeRequest{Paths: []string{input}, TargetPath: target, Mode: "not-a-mode", MediaType: "movie", TMDBID: identity.ID}, true)
	secondPlan, secondJob := historyUpsertAttempt(t, a, second.JobID)
	current, err := a.transferHistoryItem(context.Background(), firstPlan.Files[0].ID)
	if err != nil || current.PlanID != secondPlan.ID {
		t.Fatal("history alias did not select newest execution", current, err)
	}
	exact, err := a.transferHistoryExecution(context.Background(), firstPlan.Files[0].ID)
	if err != nil || exact.PlanID != firstPlan.ID || exact.JobID != firstJob.ID {
		t.Fatal("exact queue lookup redirected to newest history", exact, err)
	}
	if _, err = a.cancelOrganizeQueueFile(context.Background(), firstPlan.Files[0].ID); err != nil {
		t.Fatal("old waiting queue item was not independently cancelled", err)
	}
	for jobID, state := range map[string]string{firstJob.ID: "cancelled", secondJob.ID: "failed"} {
		job, err := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, jobID))
		if err != nil || job.State != state {
			t.Fatal("queue cancellation changed wrong execution", jobID, job.State, state, err)
		}
	}
	if _, err = a.cancelOrganizeQueueFile(context.Background(), firstPlan.Files[0].ID); fileCode(err) != "QUEUE_FILE_FINISHED" {
		t.Fatal("cancelled old execution did not remain final", err)
	}
	latest := historyUpsertOnly(t, a, "")
	if latest.PlanID != secondPlan.ID || latest.Status != "failed" {
		t.Fatal("old queue cancellation changed latest visible history", latest)
	}
	if data, err := os.ReadFile(input); err != nil || string(data) != "queued resource remains unchanged" {
		t.Fatal("queue cancellation changed source", err)
	}
}

func TestHistoryUpsertLegacyDuplicatePlansConsolidateWithoutMutation(t *testing.T) {
	a, source, target := importTestApp(t)
	first, _ := historyParityPlan(t, a, source, target, "legacy-old", "旧标题", "succeeded", "imported", "Old.mkv")
	second, _ := historyParityPlan(t, a, source, target, "legacy-new", "新标题", "failed", "ready", "New.mkv")
	second.Files[0].Source = first.Files[0].Source
	if err := a.Store.Put(context.Background(), "import_plan", second.ID, second); err != nil {
		t.Fatal(err)
	}
	oldBytes, _ := json.Marshal(first)
	newBytes, _ := json.Marshal(second)
	latest := historyUpsertOnly(t, a, "")
	if latest.ID != first.Files[0].ID || latest.PlanID != second.ID || historyUpsertFileID(t, latest) != second.Files[0].ID || latest.Status != "failed" {
		t.Fatalf("legacy same-source rows were not consolidated: %+v", latest)
	}
	if page := historyParityReadPage(t, a, url.Values{"q": {"旧标题"}}.Encode()); page.Total != 0 {
		t.Fatal("old legacy title leaked through filtered projection", page)
	}
	for _, expected := range []struct {
		plan ImportPlan
		raw  []byte
	}{{first, oldBytes}, {second, newBytes}} {
		var actual ImportPlan
		if err := a.Store.Get(context.Background(), "import_plan", expected.plan.ID, &actual); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(actual)
		if string(raw) != string(expected.raw) {
			t.Fatal("reading consolidated history rewrote frozen legacy execution", expected.plan.ID)
		}
	}
}

func TestHistoryUpsertHiddenDuplicateCannotResurrectButNewSubmissionCan(t *testing.T) {
	a, source, _ := importTestApp(t)
	input := filepath.Join(source, "unknown.mkv")
	importWrite(t, input, []byte("retained hidden history source"))
	first := historyUpsertSubmit(t, a, manualOrganizeRequest{Paths: []string{input}}, true)
	firstPlan, _ := historyUpsertAttempt(t, a, first.JobID)
	second := historyUpsertSubmit(t, a, manualOrganizeRequest{Paths: []string{input}}, true)
	secondPlan, _ := historyUpsertAttempt(t, a, second.JobID)
	item := historyUpsertOnly(t, a, "")
	if _, err := a.deleteTransferHistory(context.Background(), item, historyDeleteRequest{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if page := historyParityReadPage(t, a, ""); page.Total != 0 {
		t.Fatalf("hiding latest history resurrected older duplicate: %+v", page)
	}
	third := historyUpsertSubmit(t, a, manualOrganizeRequest{Paths: []string{input}}, true)
	thirdPlan, _ := historyUpsertAttempt(t, a, third.JobID)
	latest := historyUpsertOnly(t, a, "")
	if latest.ID != firstPlan.Files[0].ID || latest.PlanID != thirdPlan.ID || historyUpsertFileID(t, latest) != thirdPlan.Files[0].ID {
		t.Fatalf("new attempt did not reappear as same resource: %+v", latest)
	}
	for _, plan := range []ImportPlan{firstPlan, secondPlan, thirdPlan} {
		var stored ImportPlan
		if err := a.Store.Get(context.Background(), "import_plan", plan.ID, &stored); err != nil {
			t.Fatal("record-only deletion lost recovery data", err)
		}
	}
	if data, err := os.ReadFile(input); err != nil || string(data) != "retained hidden history source" {
		t.Fatal("record-only hide changed input", err)
	}
}

func TestHistoryUpsertMissingCopySourceFailsAndExplicitManagedTargetKeepsResource(t *testing.T) {
	for _, fromHistory := range []bool{true, false} {
		t.Run(map[bool]string{true: "history-source-missing", false: "file-manager-managed-target"}[fromHistory], func(t *testing.T) {
			a, source, old := reorgFixture(t)
			first := historyUpsertOnly(t, a, "")
			if fromHistory {
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
			}
			target := filepath.Join(filepath.Dir(source), "lineage-target")
			if err := os.MkdirAll(target, 0750); err != nil {
				t.Fatal(err)
			}
			scrape := false
			request := manualOrganizeRequest{TargetPath: target, Mode: "copy", MediaType: "movie", TMDBID: old.Media.ID, Scraping: &scrape}
			if fromHistory {
				request.HistoryIDs = []string{first.ID}
			} else {
				request.Paths = []string{old.Path}
			}
			if fromHistory {
				out, err := a.manualOrganize(context.Background(), request)
				if err != nil || out.Failed != 1 || out.Submitted != 0 {
					t.Fatalf("MP retries a copied history from its source, not its library target: %+v %v", out, err)
				}
				latest := historyUpsertOnly(t, a, "")
				if latest.ID != first.ID || latest.Status != "failed" || latest.Source != source {
					t.Fatalf("failed retry changed stable resource: %+v", latest)
				}
				if data, err := os.ReadFile(old.Path); err != nil || string(data) != "reorganize original video" {
					t.Fatal("failed source retry touched library target", err)
				}
				return
			}
			result := historyUpsertSubmit(t, a, request, false)
			plan, job := historyUpsertAttempt(t, a, result.JobID)
			var raw map[string]any
			historyParityDecode(t, plan.Files[0], &raw)
			if raw["history_source"] != source {
				t.Fatalf("target-based reorganization did not inherit original source: %+v", raw)
			}
			reorgRunJob(t, a, job)
			reorgRunJob(t, a, reorgJob(t, a, "reorganize_cleanup", result.PlanID))
			latest := historyUpsertOnly(t, a, "")
			if latest.ID != first.ID || latest.PlanID != plan.ID || latest.Source != old.Path || historyUpsertFileID(t, latest) != plan.Files[0].ID || latest.Status != "success" {
				t.Fatalf("target fallback forked or rewrote old visible history: %+v", latest)
			}
			if data, err := os.ReadFile(result.Target); err != nil || string(data) != "reorganize original video" {
				t.Fatal("actual reorganization did not preserve video", err)
			}
			// A second file-manager reorganization must follow the original
			// resource, not fork at the intermediate asset's SourcePath.
			nextTarget := filepath.Join(filepath.Dir(source), "lineage-second-target")
			if err := os.MkdirAll(nextTarget, 0750); err != nil {
				t.Fatal(err)
			}
			next := historyUpsertSubmit(t, a, manualOrganizeRequest{Paths: []string{result.Target}, TargetPath: nextTarget, Mode: "copy", MediaType: "movie", TMDBID: old.Media.ID, Scraping: &scrape}, false)
			nextPlan, nextJob := historyUpsertAttempt(t, a, next.JobID)
			var nextRaw map[string]any
			historyParityDecode(t, nextPlan.Files[0], &nextRaw)
			if nextRaw["history_source"] != source {
				t.Fatalf("second managed-target reorganization forked lineage: %+v", nextRaw)
			}
			reorgRunJob(t, a, nextJob)
			reorgRunJob(t, a, reorgJob(t, a, "reorganize_cleanup", next.PlanID))
			afterSecond := historyUpsertOnly(t, a, "")
			if afterSecond.ID != first.ID || afterSecond.PlanID != nextPlan.ID || afterSecond.Target != next.Target {
				t.Fatalf("second target reorganization duplicated current history: %+v", afterSecond)
			}
		})
	}
}
