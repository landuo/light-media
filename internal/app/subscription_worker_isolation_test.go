package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func installSubscriptionJadeDynastyMetadataFixture(t *testing.T, a *App) {
	t.Helper()
	installMetadataFixture(t, a, "synthetic-isolation-tmdb", []string{"/3/tv/206484"}, func(*http.Request) (*http.Response, error) {
		const body = `{"id":206484,"name":"诛仙","original_name":"Jade Dynasty","first_air_date":"2022-01-01","number_of_seasons":4,"number_of_episodes":5,"seasons":[{"season_number":4,"episode_count":5}],"genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
}

func TestSubscriptionCheckRunsWhileOrganizeWorkersAreBlocked(t *testing.T) {
	a := subscriptionTestApp(t)
	installSubscriptionJadeDynastyMetadataFixture(t, a)
	// Submit two real, independently reserved imports before holding execution.
	// Their owned source files and distinct media identities keep queue membership
	// and reservation validation active without competing for the same asset slot.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source, target := filepath.Join(base, "source"), filepath.Join(base, "library")
	if err := os.MkdirAll(target, 0750); err != nil {
		t.Fatal(err)
	}
	a.config.AllowedRoots = append(a.config.AllowedRoots, source)
	a.config.Roots = append(a.config.Roots, Root{ID: "isolation-root", Path: target, Kind: "mixed"})
	movie := importFixture(t, a, "movie")
	targets := map[string]bool{}
	for i := range 2 {
		identity := movie
		identity.ID += int64(i)
		identity.Title = fmt.Sprintf("Isolation Movie %d", i)
		identity.OriginalTitle = identity.Title
		importCache(t, a, fmt.Sprintf("media:movie:%d", identity.ID), identity)
		input := filepath.Join(source, fmt.Sprintf("Blocked.%d.2024.mkv", i))
		importWrite(t, input, []byte(fmt.Sprintf("owned isolation movie %d", i)))
		plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: identity.ID, RootID: "isolation-root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
		if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
			t.Fatal("real isolation preview", plan, err)
		}
		if targets[plan.Files[0].Target] {
			t.Fatal("isolation imports share a destination")
		}
		targets[plan.Files[0].Target] = true
		if _, err = a.executeImport(context.Background(), plan.ID, nil); err != nil {
			t.Fatal("real isolation submission", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	var workers sync.WaitGroup
	a.importMu.Lock()
	defer func() {
		cancel()
		a.importMu.Unlock()
		workers.Wait()
	}()

	// Occupy both unrestricted worker slots with valid queued imports. Both
	// workers must reach the held execution lock; the subscription lane remains free.
	for range 2 {
		workers.Go(func() { a.jobWorker(ctx, allJobs) })
	}
	waitForSubscriptionWorker(t, ctx, func() (bool, error) {
		var active int
		err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind='import' AND state='running'`).Scan(&active)
		return active == 2, err
	})
	if _, err := a.enqueue(ctx, "organize_file", map[string]string{"id": "waiting-file"}, "organize-test"); err != nil {
		t.Fatal(err)
	}

	request := subscriptionRequest(t)
	request.Episodes = []int{5} // The fixture episode has no air date; no tracker is contacted.
	sub, err := a.saveSubscription(ctx, "", request)
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.enqueue(ctx, "subscription", map[string]any{"id": sub.ID, "revision": sub.Revision}, "subscription-test")
	if err != nil {
		t.Fatal(err)
	}
	workers.Go(func() { a.jobWorker(ctx, subscriptionJobs) })
	waitForSubscriptionWorker(t, ctx, func() (bool, error) {
		var state string
		err := a.Store.DB.QueryRowContext(ctx, `SELECT state FROM jobs WHERE id=?`, job.ID).Scan(&state)
		if err == nil && (state == "failed" || state == "retry_wait") {
			return false, fmt.Errorf("subscription check ended in %s", state)
		}
		return state == "succeeded", err
	})
	var checked Subscription
	if err := a.Store.Get(ctx, "subscription", sub.ID, &checked); err != nil || checked.LastCheck == "" {
		t.Fatalf("subscription did not record a completed check: %+v, %v", checked, err)
	}
	var active, waiting int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM jobs WHERE kind='import' AND state='running'),
		(SELECT COUNT(*) FROM jobs WHERE kind='organize_file' AND state='queued')`).Scan(&active, &waiting); err != nil || active != 2 || waiting != 1 {
		t.Fatalf("the check should finish while both organizing workers remain occupied: %d running, %d waiting, %v", active, waiting, err)
	}
}

func waitForSubscriptionWorker(t *testing.T, ctx context.Context, done func() (bool, error)) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready, err := done()
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for the background job")
		case <-ticker.C:
		}
	}
}

func TestLegacySubscriptionDownloadRunsAheadOfOrganizing(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	organize, err := a.enqueue(ctx, "organize_file", map[string]string{"id": "waiting-file"}, "organize-test")
	if err != nil {
		t.Fatal(err)
	}
	intent := Intent{ID: "legacy-subscription-download", SubscriptionID: "subscription-test"}
	if err := a.Store.Put(ctx, "intent", intent.ID, intent); err != nil {
		t.Fatal(err)
	}
	legacy, err := a.enqueue(ctx, "download", map[string]string{"id": intent.ID}, "subscription-test")
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := a.claimJob(ctx, subscriptionJobs); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("subscription-check worker claimed a download or organizer: %+v, %v", claimed, err)
	}
	claimed, err := a.claimJob(ctx, generalJobs)
	if err != nil || claimed.ID != legacy.ID {
		t.Fatalf("general worker did not pick the subscription download first: %+v, %v", claimed, err)
	}
	claimed, err = a.claimJob(ctx, generalJobs)
	if err != nil || claimed.ID != organize.ID {
		t.Fatalf("organizing job was lost: %+v, %v", claimed, err)
	}
}
