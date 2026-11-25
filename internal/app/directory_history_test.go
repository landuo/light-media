package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func removeDirectoryHistoryForTest(t *testing.T, a *App, id string, target bool) {
	t.Helper()
	ctx := t.Context()
	item, err := a.transferHistoryDeleteSelection(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(historyDeleteRequest{Confirm: true, FileID: item.executionFileID(), DeleteTarget: target})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/", bytes.NewReader(body))
	if _, err = a.transferHistoryAPI(req, "transfers/"+id); err != nil {
		t.Fatal(err)
	}
}

func reopenDirectoryHistoryApp(t *testing.T, a *App) *App {
	t.Helper()
	dir := a.Dir
	a.wg.Wait()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fresh.Close() })
	return fresh
}

func TestDirectoryHistoryDeletionKeepsUnchangedSourceAcrossRestart(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target bool
	}{
		{"record_only", false},
		{"library_file_and_record", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			a, rule, source := directoryFileFixture(t, true)
			if err := a.saveSettings(ctx, a.config); err != nil {
				t.Fatal(err)
			}
			path := directoryMovieFile(t, source)
			directoryPoll(t, a, rule)
			directoryAge(t, a, rule, path)
			directoryPoll(t, a, rule)
			f := directoryRecognize(t, a, rule, path)
			job, err := a.claimImportJob(ctx)
			if err != nil || job.ID != f.JobID {
				t.Fatalf("could not claim selected import job: %s, %v", job.ID, err)
			}
			a.runClaimedJob(ctx, job)
			directoryPoll(t, a, rule)
			var id, target string
			if err := a.Store.DB.QueryRow(`SELECT id,dest FROM transferhistory WHERE src=? AND status=1`, path).Scan(&id, &target); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			library, err := os.Stat(target)
			if err != nil || !os.SameFile(before, library) {
				t.Fatalf("fixture needs a published hard link: %v", err)
			}
			removeDirectoryHistoryForTest(t, a, id, tc.target)
			check := func(app *App) {
				t.Helper()
				for i := 0; i < 2; i++ {
					directoryAge(t, app, rule, path)
					directoryPoll(t, app, rule)
				}
				var rows, jobs int
				if err := app.Store.DB.QueryRow(`SELECT (SELECT count(*) FROM transferhistory WHERE src=?),(SELECT count(*) FROM jobs WHERE kind IN ('organize_file','import') AND id!=?)`, path, f.JobID).Scan(&rows, &jobs); err != nil || rows != 0 || jobs != 0 {
					t.Fatalf("unchanged source replayed with %d history rows and %d new jobs: %v", rows, jobs, err)
				}
				src, err := os.Stat(path)
				if err != nil || !os.SameFile(src, before) {
					t.Fatalf("seeding source changed: %v", err)
				}
				dst, err := os.Stat(target)
				if tc.target && !os.IsNotExist(err) || !tc.target && (err != nil || !os.SameFile(src, dst)) {
					t.Fatalf("wrong target after delete_target=%t: %v", tc.target, err)
				}
			}
			check(a)
			fresh := reopenDirectoryHistoryApp(t, a)
			check(fresh)
			if tc.target {
				out, err := fresh.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{path}, TargetPath: fresh.Settings().Roots[0].Path, MediaType: "movie", TMDBID: 123, Mode: "hardlink", Scraping: importFlag(false)})
				if err != nil || out.Submitted != 1 || out.Failed != 0 {
					t.Fatalf("explicit manual reorganization: %+v, %v", out, err)
				}
				reorgRunJob(t, fresh, reorgJob(t, fresh, "import", out.Items[0].PlanID))
				var newID string
				if err := fresh.Store.DB.QueryRow(`SELECT id FROM transferhistory WHERE src=? AND status=1`, path).Scan(&newID); err != nil || newID == id {
					t.Fatalf("manual reorganization did not create new history: %s, %v", newID, err)
				}
			}
		})
	}
}

func TestDirectoryHistoryDeletionUpgradeStopsPreexistingAutomaticJobs(t *testing.T) {
	for _, phase := range []string{"organize_file", "import"} {
		t.Run(phase, func(t *testing.T) {
			ctx := t.Context()
			a, item := historyRemovalFixture(t)
			rule := DirectoryRule{ID: "watch", Name: "测试目录", SourcePath: filepath.Dir(item.Source), RootID: "root", MediaType: "movie", Mode: "hardlink", MonitorType: "monitor", Enabled: true, IncludeExisting: true}
			settings := a.Settings()
			settings.Directories = []DirectoryRule{rule}
			if err := a.saveSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			movie := importFixture(t, a, "movie")
			movie.Title, movie.OriginalTitle = "History", "History"
			importCache(t, a, "media:movie:123", movie)
			importWrite(t, strings.TrimSuffix(item.Source, ".mkv")+".nfo", []byte(`<movie><title>History</title><uniqueid type="tmdb" default="true">123</uniqueid></movie>`))
			stamp, err := snapshotTransferPath(item.Source)
			if err != nil {
				t.Fatal(err)
			}
			removeDirectoryHistoryForTest(t, a, item.ID, true)
			// A pre-upgrade deletion retained the exact plan/file IDs only. A
			// second receipt with no original plan must never guess a source.
			if _, err = a.Store.DB.Exec(`UPDATE history_hidden SET payload=json_remove(payload,'$.source','$.source_snapshot') WHERE id=?`, item.ID); err != nil {
				t.Fatal(err)
			}
			if err = a.Store.Put(ctx, "transfer_history_hidden", "orphan", map[string]string{"plan_id": "lost-plan", "file_id": item.FileID}); err != nil {
				t.Fatal(err)
			}
			directoryPoll(t, a, rule)
			directoryAge(t, a, rule, item.Source)
			directoryPoll(t, a, rule)
			jobID := directoryObservation(t, a, rule, item.Source).JobID
			if phase == "import" {
				directoryRecognize(t, a, rule, item.Source)
			}
			var state string
			if err = a.Store.DB.QueryRow(`SELECT state FROM jobs WHERE id=? AND kind=?`, jobID, phase).Scan(&state); err != nil || state != "queued" {
				t.Fatalf("pre-upgrade %s job was not queued: %s %v", phase, state, err)
			}
			fresh := reopenDirectoryHistoryApp(t, a)
			if match, err := directorySourceHistoryDeleted(ctx, fresh.Store.DB, item.Source, stamp); err != nil || !match {
				t.Fatalf("old receipt lost its exact source: %v", err)
			}
			var sourceNull bool
			if err = fresh.Store.DB.QueryRow(`SELECT json_type(payload,'$.source')='null' AND json_type(payload,'$.source_snapshot') IS NULL FROM history_hidden WHERE id='orphan'`).Scan(&sourceNull); err != nil || !sourceNull {
				t.Fatalf("unknown original plan was assigned a source: %v", err)
			}
			j, err := scanJob(fresh.Store.DB.QueryRow(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=? AND kind=? AND state='queued' RETURNING `+jobColumns, jobID, phase))
			if err != nil {
				t.Fatal(err)
			}
			fresh.runClaimedJob(ctx, j)
			var verdict, code string
			if err = fresh.Store.DB.QueryRow(`SELECT state,coalesce(error_code,'') FROM jobs WHERE id=?`, jobID).Scan(&verdict, &code); err != nil {
				t.Fatal(err)
			}
			if verdict != "succeeded" && !(verdict == "cancelled" && code == "DIRECTORY_HISTORY_DELETED") {
				t.Fatalf("expected an intentional skip, got %s (%s)", verdict, code)
			}
			directoryPoll(t, fresh, rule)
			directoryAge(t, fresh, rule, item.Source)
			directoryPoll(t, fresh, rule)
			var rows, active int
			if err = fresh.Store.DB.QueryRow(`SELECT (SELECT count(*) FROM transferhistory WHERE src=?),(SELECT count(*) FROM jobs WHERE kind IN ('organize_file','import') AND state IN ('queued','running'))`, item.Source).Scan(&rows, &active); err != nil || rows != 0 || active != 0 {
				t.Fatalf("old %s work replayed: %d records, %d pending; %v", phase, rows, active, err)
			}
			if _, err = os.Lstat(item.Target); !os.IsNotExist(err) {
				t.Fatalf("library entry was recreated: %v", err)
			}
			if after, err := snapshotTransferPath(item.Source); err != nil || after != stamp {
				t.Fatalf("seeding source changed: %v", err)
			}
		})
	}
}

func TestDirectoryHistoryDeletionKeepsReplacementSeenBeforeDeleteEligible(t *testing.T) {
	ctx := t.Context()
	a, item := historyRemovalFixture(t)
	rule := DirectoryRule{ID: "watch", Name: "测试目录", SourcePath: filepath.Dir(item.Source), RootID: "root", MediaType: "movie", Mode: "hardlink", MonitorType: "monitor", Enabled: true, IncludeExisting: true}
	settings := a.Settings()
	settings.Directories = []DirectoryRule{rule}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	directoryPoll(t, a, rule)
	before, err := os.Stat(item.Source)
	if err != nil {
		t.Fatal(err)
	}
	tmp := item.Source + ".new"
	importWrite(t, tmp, []byte("new source version, same filename"))
	if err = os.Rename(tmp, item.Source); err != nil {
		t.Fatal(err)
	}
	stamp, err := snapshotTransferPath(item.Source)
	if err != nil {
		t.Fatal(err)
	}
	directoryPoll(t, a, rule)
	observed := directoryObservation(t, a, rule, item.Source)
	if observed.State != "imported" || observed.Snapshot != stamp {
		t.Fatalf("expected old visible history alongside replacement identity: %+v", observed)
	}
	removeDirectoryHistoryForTest(t, a, item.ID, false)
	for i := 0; i < 2; i++ {
		directoryAge(t, a, rule, item.Source)
		directoryPoll(t, a, rule)
	}
	var job organizeFilePayload
	var raw []byte
	if err = a.Store.DB.QueryRow(`SELECT payload FROM jobs WHERE kind='organize_file' AND state='queued'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &job); err != nil || job.Snapshot != stamp || job.File.Path != item.Source {
		t.Fatalf("replacement did not reach the queue with its own identity: %v", err)
	}
	if info, err := os.Stat(item.Target); err != nil || !os.SameFile(before, info) {
		t.Fatalf("record-only deletion changed original library inode: %v", err)
	}
}
