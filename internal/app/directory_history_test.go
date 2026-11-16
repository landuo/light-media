package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryHistorySkipsPreviouslyManualSourceAcrossRuleChange(t *testing.T) {
	a, rule, source := directoryFileFixture(t, true)
	path := directoryMovieFile(t, source)
	ctx := context.Background()
	plan, err := a.previewImport(ctx, ImportRequest{Manual: true, MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: path}}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.executeImport(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, job)
	directoryPoll(t, a, rule)
	rule.Mode = "copy"
	a.config.Directories[0] = rule
	directoryPoll(t, a, rule)
	directoryAge(t, a, rule, path)
	directoryPoll(t, a, rule)
	f := directoryObservation(t, a, rule, path)
	if f.State != "imported" || f.PlanID != plan.ID || f.JobID != job.ID {
		t.Fatalf("manual history not reused by monitor: %+v", f)
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind='organize_file'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rule edit requeued historical source: %d %v", count, err)
	}
}

func TestDirectoryHistoryCancellationBeforeRecognitionStaysCancelled(t *testing.T) {
	a, rule, source := directoryFileFixture(t, true)
	path := directoryMovieFile(t, source)
	directoryPoll(t, a, rule)
	directoryAge(t, a, rule, path)
	directoryPoll(t, a, rule)
	queued := directoryObservation(t, a, rule, path)
	if queued.PlanID != "" || queued.JobID == "" {
		t.Fatalf("expected recognition queue: %+v", queued)
	}
	if _, err := a.cancelOrganizeQueueFile(context.Background(), queued.JobID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		directoryPoll(t, a, rule)
		f := directoryObservation(t, a, rule, path)
		if f.State != "cancelled" || f.JobID != queued.JobID || f.PlanID != "" {
			t.Fatalf("cancelled observation was stale or revived: %+v", f)
		}
	}
	var count int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind='organize_file'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("cancellation recreated preparation: %d %v", count, err)
	}
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM transferhistory`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unattempted recognition created execution history: %d %v", count, err)
	}
	if bytes, err := os.ReadFile(path); err != nil || string(bytes) != "synthetic monitor video" {
		t.Fatalf("cancel modified source: %q %v", bytes, err)
	}
}

func TestDirectoryHistoryBlockedPlanRetainsKnownContext(t *testing.T) {
	a, rule, source := directoryFileFixture(t, true)
	rule.LibraryTypeFolder = importFlag(true)
	never := "never"
	rule.OverwriteMode, rule.OverwriteModeKnown = &never, true
	a.config.Directories[0] = rule
	path := directoryMovieFile(t, source)
	target := filepath.Join(a.config.Roots[0].Path, "华语电影", "测试影片 (2024)", "测试影片 (2024).mkv")
	importWrite(t, target, []byte("foreign target"))
	if _, err := a.Store.DB.Exec(`INSERT INTO reservations(media_key,season,episode,intent_id) VALUES('movie:123',0,0,'other-download')`); err != nil {
		t.Fatal(err)
	}
	directoryPoll(t, a, rule)
	directoryAge(t, a, rule, path)
	directoryPoll(t, a, rule)
	f := directoryRecognize(t, a, rule, path)
	var plan ImportPlan
	if err := a.Store.Get(context.Background(), "import_plan", f.PlanID, &plan); err != nil {
		t.Fatal(err)
	}
	h, err := a.transferHistoryItem(context.Background(), plan.Files[0].ID)
	if err != nil || h.Status != "failed" || h.Media.ID != 123 || h.Media.Title != "测试影片" || h.Category != "华语电影" || h.Mode != "hardlink" || h.Target != target || h.Error == "" {
		t.Fatalf("directory admission failure lost known facts: %+v %v", h, err)
	}
	if bytes, err := os.ReadFile(target); err != nil || string(bytes) != "foreign target" {
		t.Fatalf("failed organize changed target: %q %v", bytes, err)
	}
}
