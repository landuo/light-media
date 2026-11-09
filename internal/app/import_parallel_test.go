package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"light-media/internal/catalog"
)

func queueParallelImport(t *testing.T, a *App, media catalog.Media, file ImportFileRequest, resolved json.RawMessage) (ImportPlan, Job) {
	t.Helper()
	importWrite(t, file.Path, []byte(filepath.Base(file.Path)))
	plan, err := a.previewImport(t.Context(), ImportRequest{
		MediaType: media.MediaType, TMDBID: media.ID, RootID: "root", Mode: "hardlink",
		Scraping: importFlag(false), Files: []ImportFileRequest{file}, originalResolvedMedia: resolved,
	})
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
		t.Fatalf("preview did not prepare an ordinary hardlink: %+v, %v", plan, err)
	}
	job, err := a.executeImport(t.Context(), plan.ID, nil)
	if err != nil || job.State != "queued" {
		t.Fatalf("submit did not queue import: %+v, %v", job, err)
	}
	return plan, job
}

func claimParallelImport(t *testing.T, a *App, id string) Job {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	job, err := a.claimJob(ctx, importJobs)
	if err != nil || job.ID != id || job.State != "running" || job.importRun == nil {
		if job.importRun != nil {
			job.importRun.release()
		}
		t.Fatalf("claimed %s, want %s with an import admission: %v", job.ID, id, err)
	}
	return job
}

func assertNoParallelImportClaim(t *testing.T, a *App, failure string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if job, err := a.claimJob(ctx, importJobs); !errors.Is(err, sql.ErrNoRows) {
		if job.importRun != nil {
			job.importRun.release()
		}
		t.Fatalf("%s: %s, %v", failure, job.ID, err)
	}
}

// Pause the first job at the real hardlink syscall, outside any database
// transaction. Other jobs must make progress while this one is held open.
func holdParallelImport(t *testing.T, a *App, source string) (chan struct{}, func()) {
	t.Helper()
	entered, gate := make(chan struct{}), make(chan struct{})
	var release, arrive sync.Once
	a.importSourceLink = func(from int, name string, to int, next string, flags int) error {
		if name == filepath.Base(source) {
			arrive.Do(func() { close(entered) })
			select {
			case <-gate:
			case <-t.Context().Done():
				return t.Context().Err()
			}
		}
		return unix.Linkat(from, name, to, next, flags)
	}
	return entered, func() { release.Do(func() { close(gate) }) }
}

func runParallelImport(a *App, ctx context.Context, workers *sync.WaitGroup, job Job) <-chan error {
	done := make(chan error, 1)
	workers.Go(func() {
		result := func() error {
			defer job.importRun.release()
			err := a.safeRun(ctx, job)
			state, code, message := "succeeded", "", ""
			if err != nil {
				state, code, message = "failed", "TASK_FAILED", err.Error()
			}
			changed, finishErr := a.finishJob(ctx, job, state, code, message, time.Now().Unix())
			if finishErr != nil {
				return errors.Join(err, finishErr)
			}
			if !changed {
				return errors.Join(err, errors.New("job state was not completed"))
			}
			return err
		}()
		done <- result
	})
	return done
}

func awaitParallelImport(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the claimed import did not finish")
		return nil
	}
}

func awaitParallelLink(t *testing.T, entered <-chan struct{}, done <-chan error) {
	t.Helper()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("first job finished before reaching the hardlink: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("first job did not reach the hardlink")
	}
}

func assertParallelAsset(t *testing.T, a *App, plan ImportPlan) string {
	t.Helper()
	var finished ImportPlan
	if err := a.Store.Get(t.Context(), "import_plan", plan.ID, &finished); err != nil || len(finished.Files) != 1 || finished.Files[0].Status != "imported" {
		t.Fatalf("import did not persist its result: %+v, %v", finished, err)
	}
	file := finished.Files[0]
	var asset Asset
	if err := a.assetAtPath(t.Context(), file.Target, &asset); err != nil || asset.ID != file.AssetID || asset.SourcePath != file.Source {
		t.Fatalf("import did not register the published asset: %+v, %v", asset, err)
	}
	input, inErr := os.Stat(file.Source)
	output, outErr := os.Stat(file.Target)
	if inErr != nil || outErr != nil || !os.SameFile(input, output) {
		t.Fatalf("published file is not a hardlink of the retained source: %v, %v", inErr, outErr)
	}
	return file.Target
}

func TestImportQueueRunsIndependentEpisodesInParallel(t *testing.T) {
	a, source, _ := importTestApp(t)
	media := importFixture(t, a, "tv")
	importCache(t, a, "season:123:1", catalog.Season{ID: 901, SeriesID: media.ID, SeasonNumber: 1, Episodes: []catalog.Episode{
		{ID: 9101, SeasonNumber: 1, EpisodeNumber: 1, Name: "第一集"},
		{ID: 9102, SeasonNumber: 1, EpisodeNumber: 2, Name: "第二集"},
	}})
	resolved, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"name":"测试剧集","original_name":"Test Film","first_air_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "tv", "image.tmdb.org", "")
	if err != nil {
		t.Fatal(err)
	}
	firstPath := filepath.Join(source, "Test.Show.S01E01.mkv")
	secondPath := filepath.Join(source, "Test.Show.S01E02.mkv")
	queue := func(path string, episode int) (ImportPlan, Job) {
		meta, err := a.originalTransferPathMeta(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		// Directory preparation freezes the parsed metadata for native naming.
		return queueParallelImport(t, a, media, ImportFileRequest{Path: path, Season: 1, Episodes: []int{episode}, originalResolvedMeta: meta}, resolved)
	}
	firstPlan, firstQueued := queue(firstPath, 1)
	secondPlan, secondQueued := queue(secondPath, 2)
	entered, release := holdParallelImport(t, a, firstPath)
	ctx, cancel := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	defer func() { release(); cancel(); workers.Wait() }()
	firstDone := runParallelImport(a, ctx, &workers, claimParallelImport(t, a, firstQueued.ID))
	awaitParallelLink(t, entered, firstDone)
	secondDone := runParallelImport(a, ctx, &workers, claimParallelImport(t, a, secondQueued.ID))
	if err := awaitParallelImport(t, secondDone); err != nil {
		t.Fatal("different episodes should be independently publishable:", err)
	}
	secondTarget := assertParallelAsset(t, a, secondPlan)
	select {
	case err := <-firstDone:
		t.Fatalf("first job escaped its hardlink gate: %v", err)
	default:
	}
	release()
	if err := awaitParallelImport(t, firstDone); err != nil {
		t.Fatal(err)
	}
	if firstTarget := assertParallelAsset(t, a, firstPlan); firstTarget == secondTarget {
		t.Fatal("the two episodes were published to the same target")
	}
}

func TestImportQueueSerializesSameTargetAndSkipsItForIndependentWork(t *testing.T) {
	for _, cancelWaiting := range []bool{false, true} {
		name := "wait then reject existing target"
		if cancelWaiting {
			name = "cancel while waiting"
		}
		t.Run(name, func(t *testing.T) {
			a, source, _ := importTestApp(t)
			firstMedia := importFixture(t, a, "movie")
			conflictingMedia := firstMedia
			conflictingMedia.ID = 124
			importCache(t, a, "media:movie:124", conflictingMedia)
			independentMedia := firstMedia
			independentMedia.ID, independentMedia.Title, independentMedia.OriginalTitle = 125, "另一个影片", "Another Film"
			importCache(t, a, "media:movie:125", independentMedia)
			firstPath := filepath.Join(source, "First.2024.mkv")
			secondPath := filepath.Join(source, "Second.2024.mkv")
			thirdPath := filepath.Join(source, "Third.2024.mkv")
			firstPlan, firstQueued := queueParallelImport(t, a, firstMedia, ImportFileRequest{Path: firstPath}, nil)
			secondPlan, secondQueued := queueParallelImport(t, a, conflictingMedia, ImportFileRequest{Path: secondPath}, nil)
			thirdPlan, thirdQueued := queueParallelImport(t, a, independentMedia, ImportFileRequest{Path: thirdPath}, nil)
			if firstPlan.Files[0].Target != secondPlan.Files[0].Target || firstPlan.Files[0].Target == thirdPlan.Files[0].Target {
				t.Fatal("fixture must have a colliding pair and a distinct third target")
			}
			entered, release := holdParallelImport(t, a, firstPath)
			ctx, cancel := context.WithCancel(t.Context())
			var workers sync.WaitGroup
			defer func() { release(); cancel(); workers.Wait() }()
			firstDone := runParallelImport(a, ctx, &workers, claimParallelImport(t, a, firstQueued.ID))
			awaitParallelLink(t, entered, firstDone)
			thirdDone := runParallelImport(a, ctx, &workers, claimParallelImport(t, a, thirdQueued.ID))
			if err := awaitParallelImport(t, thirdDone); err != nil {
				t.Fatal("independent job was blocked by an older conflicting job:", err)
			}
			assertParallelAsset(t, a, thirdPlan)
			assertNoParallelImportClaim(t, a, "conflicting target was claimable before the first publication")
			if cancelWaiting {
				req := httptest.NewRequest(http.MethodPost, "/api/jobs/"+secondQueued.ID+"/cancel", nil)
				if _, err := a.jobAPI(httptest.NewRecorder(), req, secondQueued.ID+"/cancel"); err != nil {
					t.Fatal("waiting job could not be cancelled:", err)
				}
			}
			release()
			if err := awaitParallelImport(t, firstDone); err != nil {
				t.Fatal(err)
			}
			if cancelWaiting {
				assertNoParallelImportClaim(t, a, "cancelled job was claimed")
			} else {
				second := claimParallelImport(t, a, secondQueued.ID)
				if !second.importRun.exclusive {
					second.importRun.release()
					t.Fatal("replacement of an existing managed target must run exclusively")
				}
				secondDone := runParallelImport(a, ctx, &workers, second)
				if err := awaitParallelImport(t, secondDone); err == nil {
					t.Fatal("later job overwrote a target without owning it")
				}
			}
			assertParallelAsset(t, a, firstPlan)
			if raw, err := os.ReadFile(secondPath); err != nil || string(raw) != filepath.Base(secondPath) {
				t.Fatalf("later job's source was changed: %q, %v", raw, err)
			}
		})
	}
}

func TestImportQueueColdRecoveryKeepsStagedLink(t *testing.T) {
	a, source, _ := importTestApp(t)
	firstMedia := importFixture(t, a, "movie")
	secondMedia := firstMedia
	secondMedia.ID, secondMedia.Title = 124, "另一个影片"
	importCache(t, a, "media:movie:124", secondMedia)
	firstPlan, firstQueued := queueParallelImport(t, a, firstMedia, ImportFileRequest{Path: filepath.Join(source, "First.2024.mkv")}, nil)
	secondPlan, secondQueued := queueParallelImport(t, a, secondMedia, ImportFileRequest{Path: filepath.Join(source, "Second.2024.mkv")}, nil)
	firstClaim := claimParallelImport(t, a, firstQueued.ID)
	secondClaim := claimParallelImport(t, a, secondQueued.ID)
	defer firstClaim.importRun.release()
	defer secondClaim.importRun.release()

	// Persist the exact staged receipt left if a process stops just before
	// the final Linkat. Both jobs are still running in the on-disk queue.
	file := firstPlan.Files[0]
	checks := 0
	receipt, err := a.publishImport(t.Context(), firstPlan.ID+"/"+file.ID, file.Source, file.Target, planSnapshot(file), "hardlink", func() error {
		checks++
		if checks == 2 {
			return context.Canceled
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || receipt.State != "staged" {
		t.Fatalf("did not leave a recoverable staged receipt: %+v, %v", receipt, err)
	}
	stage := filepath.Join(filepath.Dir(file.Target), receipt.Stage)
	if _, err := os.Stat(stage); err != nil {
		t.Fatal("staged hardlink missing:", err)
	}
	settings := a.Settings()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cold.Close() })
	cold.config = settings
	if cold.Store.RecoveredJobs != 2 {
		t.Fatalf("recovered %d jobs, want both admissions returned to queue", cold.Store.RecoveredJobs)
	}
	cold.importSourceLink = func(from int, name string, to int, next string, flags int) error {
		if name == filepath.Base(file.Source) {
			return errors.New("the staged file must not be linked from source a second time")
		}
		return unix.Linkat(from, name, to, next, flags)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	firstRetry := claimParallelImport(t, cold, firstQueued.ID)
	secondRetry := claimParallelImport(t, cold, secondQueued.ID)
	if firstRetry.Generation <= firstClaim.Generation || secondRetry.Generation <= secondClaim.Generation {
		t.Fatal("recovery reused a stale generation")
	}
	firstDone := runParallelImport(cold, ctx, &workers, firstRetry)
	secondDone := runParallelImport(cold, ctx, &workers, secondRetry)
	if err := errors.Join(awaitParallelImport(t, firstDone), awaitParallelImport(t, secondDone)); err != nil {
		t.Fatal(err)
	}
	assertParallelAsset(t, cold, firstPlan)
	assertParallelAsset(t, cold, secondPlan)
	if _, err := os.Stat(stage); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("recovered publication did not retire the staging link:", err)
	}
}

func TestImportQueueLatestPolicyProtectsTargetDirectory(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy := importOverwritePolicy{}
	mode := "latest"
	policy.Policy.Known, policy.Policy.Mode = true, &mode
	newScope := func(id, target string, policy *importOverwritePolicy) *importAdmission {
		t.Helper()
		run := &importAdmission{plan: id, paths: map[string]struct{}{}, targets: map[string]struct{}{}}
		if !run.addTarget(filepath.Join(root, target), importMetadata{RootPath: root, Overwrite: policy}) {
			t.Fatalf("could not scope target %s", target)
		}
		return run
	}
	latest := newScope("latest", "film/new.mkv", &policy)
	sibling := newScope("sibling", "film/other.mkv", nil)
	independent := newScope("independent", "another/new.mkv", nil)
	if !latest.conflicts(sibling) || latest.conflicts(independent) {
		t.Fatal("latest replacement must protect its target directory and allow a different directory")
	}
}
