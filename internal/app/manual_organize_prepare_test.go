package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/catalog"
)

func TestManualOrganizeDirectoryQueuePreparesAndReplays(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	identity := importFixture(t, a, "tv")
	metadataRequests := 0
	metadataFixture := func(r *http.Request) (int, string) {
		metadataRequests++
		if r.URL.Path != "/3/tv/123" {
			t.Errorf("unexpected fixture endpoint %s", r.URL.Path)
			return http.StatusNotFound, `{}`
		}
		return http.StatusOK, `{"id":123,"name":"测试剧集","original_name":"Original Show","first_air_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`
	}
	originalHostMediaInstallOwnedTransport(t, a, metadataFixture)
	selected := filepath.Join(source, "电视剧")
	season := catalog.Season{ID: 20, SeriesID: identity.ID, SeasonNumber: 2}
	for _, episode := range []int{3, 4} {
		season.Episodes = append(season.Episodes, catalog.Episode{ID: int64(200 + episode), SeasonNumber: 2, EpisodeNumber: episode})
		path := filepath.Join(selected, "Original Show", fmt.Sprintf("Original.Show.S02E%02d.mkv", episode))
		importWrite(t, path, []byte("isolated manual directory queue test"))
	}
	importCache(t, a, "season:123:2", season)
	payload, err := json.Marshal(manualOrganizeRequest{
		Paths: []string{selected}, Background: true, TargetPath: target, Mode: "hardlink",
		MediaType: "tv", TMDBID: identity.ID, Scraping: importFlag(false), LibraryTypeFolder: importFlag(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), http.MethodPost, "/api/files/organize", string(payload), setupTestAdmin(t, a), "")
	var out struct {
		Data struct {
			PreparationJobID string `json:"preparation_job_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil || response.Code != http.StatusOK || out.Data.PreparationJobID == "" {
		t.Fatalf("queue request should return a preparation job: status=%d body=%s err=%v", response.Code, response.Body, err)
	}
	jobs, err := a.jobs(ctx, 10)
	if err != nil || len(jobs) != 1 || jobs[0].ID != out.Data.PreparationJobID || jobs[0].Kind != "organize_prepare" || jobs[0].State != "queued" {
		t.Fatalf("expected only a durable queued preparation job, got %+v: %v", jobs, err)
	}
	if metadataRequests != 0 {
		t.Fatalf("directory submit did %d metadata lookups before the worker claimed it", metadataRequests)
	}
	parent, err := a.claim(ctx)
	if err != nil || parent.ID != out.Data.PreparationJobID {
		t.Fatalf("worker did not claim preparation: %+v %v", parent, err)
	}
	workerCtx := a.traceContext(ctx, parent.WorkflowID, parent.ID, parent.RequestID)
	if err := a.safeRun(workerCtx, parent); err != nil {
		t.Fatal(err)
	}
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE kind='import' ORDER BY id`
	children, err := a.queryJobs(ctx, query)
	if err != nil || len(children) != 2 {
		t.Fatalf("preparation should queue one import for each episode, got %+v: %v", children, err)
	}
	for _, child := range children {
		if child.ParentJobID != parent.ID || child.State != "queued" {
			t.Fatalf("expected a queued child of preparation %s, got %+v", parent.ID, child)
		}
		reorgRunJob(t, a, child)
	}
	for _, episode := range []int{3, 4} {
		path := filepath.Join(selected, "Original Show", fmt.Sprintf("Original.Show.S02E%02d.mkv", episode))
		result := filepath.Join(target, "测试剧集 (2024)", "Season 2", fmt.Sprintf("测试剧集 - S02E%02d - 第 %d 集.mkv", episode, episode))
		src, srcErr := os.Stat(path)
		dst, dstErr := os.Stat(result)
		if srcErr != nil || dstErr != nil || !os.SameFile(src, dst) {
			t.Fatalf("episode %d was not hardlinked to %s: source=%v destination=%v", episode, result, srcErr, dstErr)
		}
	}
	// Restart after the children completed while preparation is still marked running.
	settings := a.Settings()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopened.config = settings
	originalHostMediaInstallOwnedTransport(t, reopened, metadataFixture)
	recovered, err := reopened.claim(ctx)
	if err != nil || recovered.ID != parent.ID || recovered.Generation <= parent.Generation {
		t.Fatalf("restart did not recover preparation: %+v %v", recovered, err)
	}
	if err := reopened.safeRun(reopened.traceContext(ctx, recovered.WorkflowID, recovered.ID, recovered.RequestID), recovered); err != nil {
		t.Fatal(err)
	}
	replayed, err := reopened.queryJobs(ctx, query)
	if err != nil || len(replayed) != len(children) {
		t.Fatalf("preparation replay queued duplicate imports, got %+v: %v", replayed, err)
	}
	for i, child := range replayed {
		if child.ID != children[i].ID || child.State != "succeeded" {
			t.Fatalf("preparation replay replaced a completed import: %+v", child)
		}
	}
}
