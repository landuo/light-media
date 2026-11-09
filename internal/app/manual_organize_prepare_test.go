package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	for _, episode := range []int{3, 4, 5} {
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
	readQueue := func(app *App) []organizeQueuePreparation {
		t.Helper()
		value, err := app.organizeQueue(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var queue struct {
			Preparations []organizeQueuePreparation `json:"preparations"`
		}
		historyParityDecode(t, value, &queue)
		return queue.Preparations
	}
	if entries := readQueue(a); len(entries) != 1 || entries[0].ID != jobs[0].ID || entries[0].State != "queued" || len(entries[0].Paths) != 1 || entries[0].Paths[0] != selected {
		t.Fatalf("organize queue should show the selected folder before any file job exists: %+v", entries)
	}
	parent, err := a.claim(ctx)
	if err != nil || parent.ID != out.Data.PreparationJobID {
		t.Fatalf("worker did not claim preparation: %+v %v", parent, err)
	}
	workerCtx := a.traceContext(ctx, parent.WorkflowID, parent.ID, parent.RequestID)
	if err := a.safeRun(workerCtx, parent); err != nil {
		t.Fatal(err)
	}
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE kind IN ('organize_file','import') ORDER BY id`
	candidates, err := a.queryJobs(ctx, query)
	if err != nil || len(candidates) != 3 || metadataRequests != 0 {
		t.Fatalf("all episodes must be queued before metadata lookup, got %d jobs and %d lookups: %v", len(candidates), metadataRequests, err)
	}
	queuedIDs := map[string]bool{}
	for _, candidate := range candidates {
		if candidate.Kind != "organize_file" || candidate.ParentJobID != parent.ID || candidate.State != "queued" {
			t.Fatalf("expected a waiting file job, got %s %s %s", candidate.ID, candidate.Kind, candidate.State)
		}
		queuedIDs[candidate.ID] = true
	}
	groups := historyParityReadQueue(t, a)
	if len(groups) != 3 || len(readQueue(a)) != 0 {
		t.Fatalf("expected three waiting files and no scanning row: %+v", groups)
	}
	for _, group := range groups {
		if len(group.Files) != 1 || !queuedIDs[group.Files[0].JobID] || group.Files[0].Source == "" {
			t.Fatalf("missing waiting source file: %+v", group)
		}
	}
	// Restart after all files are committed but before the scanning job ends.
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
	if changed, err := reopened.finishJob(ctx, recovered, "succeeded", "", "", time.Now().Unix()); err != nil || !changed {
		t.Fatalf("finish preparation: changed=%v error=%v", changed, err)
	}
	if err := os.Remove(filepath.Join(selected, "Original Show", "Original.Show.S02E05.mkv")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ { // Three recognitions and two actual imports.
		job, err := reopened.claim(ctx)
		if err != nil || !queuedIDs[job.ID] {
			t.Fatalf("unexpected execution after restart: %+v %v", job, err)
		}
		if err := reopened.safeRun(reopened.traceContext(ctx, job.WorkflowID, job.ID, job.RequestID), job); err != nil {
			t.Fatal(err)
		}
		if changed, err := reopened.finishJob(ctx, job, "succeeded", "", "", time.Now().Unix()); err != nil || changed != (job.Kind == "import") {
			t.Fatalf("finish %s: changed=%v error=%v", job.Kind, changed, err)
		}
	}
	for _, episode := range []int{3, 4} {
		path := filepath.Join(selected, "Original Show", fmt.Sprintf("Original.Show.S02E%02d.mkv", episode))
		result := filepath.Join(target, "测试剧集 (2024)", "Season 2", fmt.Sprintf("测试剧集 - S02E%02d - 第 %d 集.mkv", episode, episode))
		src, srcErr := os.Stat(path)
		dst, dstErr := os.Stat(result)
		if srcErr != nil || dstErr != nil || !os.SameFile(src, dst) {
			t.Fatalf("episode %d was not hardlinked: source=%v destination=%v", episode, srcErr, dstErr)
		}
	}
	if groups := historyParityReadQueue(t, reopened); len(groups) != 0 {
		t.Fatalf("terminal files should leave the organize queue: %+v", groups)
	}
	history := historyParityReadPage(t, reopened, "")
	states := map[string]int{}
	for _, item := range history.Items {
		states[item.Status]++
	}
	if history.Total != 3 || states["success"] != 2 || states["failed"] != 1 {
		t.Fatalf("expected two successes and one failed file in history, got %+v", states)
	}
}

func TestManualOrganizeHistoryQueueAcknowledgesBatchBeforeRecognition(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	identity := importFixture(t, a, "tv")
	metadataRequests := 0
	originalHostMediaInstallOwnedTransport(t, a, func(r *http.Request) (int, string) {
		metadataRequests++
		if r.URL.Path != "/3/tv/123" {
			t.Errorf("unexpected fixture endpoint %s", r.URL.Path)
			return http.StatusNotFound, `{}`
		}
		return http.StatusOK, `{"id":123,"name":"测试剧集","original_name":"Original Show","first_air_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`
	})
	const count = 32
	season := catalog.Season{ID: 20, SeriesID: identity.ID, SeasonNumber: 2}
	for episode := 1; episode <= count; episode++ {
		season.Episodes = append(season.Episodes, catalog.Episode{ID: int64(200 + episode), SeasonNumber: 2, EpisodeNumber: episode})
		path := filepath.Join(source, fmt.Sprintf("Original.Show.S02E%02d.mkv", episode))
		importWrite(t, path, []byte("isolated history retry fixture"))
		if _, err := a.recordManualOrganizeFailure(ctx, path, id("wf_"), bad("REVIEW_IMPORT", "初次整理失败")); err != nil {
			t.Fatal(err)
		}
	}
	importCache(t, a, "season:123:2", season)
	history := historyParityReadPage(t, a, "")
	ids := make([]string, 0, count)
	for _, item := range history.Items {
		ids = append(ids, item.ID)
	}
	if len(ids) != count {
		t.Fatalf("expected %d failed fixtures, got %d", count, len(ids))
	}
	payload, err := json.Marshal(manualOrganizeRequest{
		HistoryIDs: ids, Background: true, TargetPath: target, Mode: "hardlink",
		MediaType: "tv", TMDBID: identity.ID, Scraping: importFlag(false), LibraryTypeFolder: importFlag(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), http.MethodPost, "/api/files/organize", string(payload), setupTestAdmin(t, a), "")
	var envelope struct {
		Data manualOrganizeOutput `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || response.Code != http.StatusOK || envelope.Data.PreparationJobID == "" {
		t.Fatalf("expected a durable preparation job: status=%d body=%s err=%v", response.Code, response.Body, err)
	}
	value, err := a.organizeQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var queue struct {
		Preparations []organizeQueuePreparation `json:"preparations"`
	}
	historyParityDecode(t, value, &queue)
	if len(queue.Preparations) != 1 || queue.Preparations[0].HistoryCount != count || queue.Preparations[0].ID != envelope.Data.PreparationJobID || metadataRequests != 0 {
		t.Fatalf("batch must be visible before recognition: queue=%+v lookups=%d", queue, metadataRequests)
	}
	parent, err := a.claim(ctx)
	if err != nil || parent.ID != envelope.Data.PreparationJobID {
		t.Fatalf("claim preparation: job=%+v err=%v", parent, err)
	}
	parentCtx := a.traceContext(ctx, parent.WorkflowID, parent.ID, parent.RequestID)
	if err := a.safeRun(parentCtx, parent); err != nil {
		t.Fatal(err)
	}
	if groups := historyParityReadQueue(t, a); len(groups) != count {
		t.Fatalf("expected %d individual file tasks, got %d", count, len(groups))
	}
	// A recovered preparation must skip every item it already committed.
	if err := a.safeRun(parentCtx, parent); err != nil {
		t.Fatal(err)
	}
	var children int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE kind='import' AND json_extract(payload,'$._parent_job_id')=?`, parent.ID).Scan(&children); err != nil || children != count {
		t.Fatalf("replaying preparation duplicated children: count=%d err=%v", children, err)
	}
	if changed, err := a.finishJob(ctx, parent, "succeeded", "", "", time.Now().Unix()); err != nil || !changed {
		t.Fatalf("finish preparation: changed=%v err=%v", changed, err)
	}
	for range count {
		job, err := a.claim(ctx)
		if err != nil || job.Kind != "import" || job.ParentJobID != parent.ID {
			t.Fatalf("claim retry: job=%+v err=%v", job, err)
		}
		a.runClaimedJob(ctx, job)
	}
	for episode := 1; episode <= count; episode++ {
		path := filepath.Join(source, fmt.Sprintf("Original.Show.S02E%02d.mkv", episode))
		result := filepath.Join(target, "测试剧集 (2024)", "Season 2", fmt.Sprintf("测试剧集 - S02E%02d - 第 %d 集.mkv", episode, episode))
		src, srcErr := os.Stat(path)
		dst, dstErr := os.Stat(result)
		if srcErr != nil || dstErr != nil || !os.SameFile(src, dst) {
			t.Fatalf("episode %d was not hardlinked: source=%v destination=%v", episode, srcErr, dstErr)
		}
	}
	current, err := a.transferHistoryItemsForOrganize(ctx, ids)
	if err != nil || len(current) != count {
		t.Fatalf("retry history: count=%d err=%v", len(current), err)
	}
	for i, item := range current {
		if item.ID != ids[i] || item.Status != "success" {
			t.Fatalf("retry should update the same record: id=%s status=%s", item.ID, item.Status)
		}
	}
}
