package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"light-media/internal/catalog"
	"light-media/internal/download"
)

func summaryRecord(t *testing.T, a *App, kind, key string, value any) {
	t.Helper()
	if err := a.Store.Put(context.Background(), kind, key, value); err != nil {
		t.Fatal(err)
	}
}

func summaryJob(t *testing.T, a *App, kind string, payload any, workflow string) Job {
	t.Helper()
	job, err := a.enqueue(context.Background(), kind, payload, workflow)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestJobSummaryResolvesEveryWorkerKind(t *testing.T) {
	a := newTestApp(t)
	base := t.TempDir()
	source := filepath.Join(base, "source", "Queue.Show.S03E02.mkv")
	target := filepath.Join(base, "library", "队列测试剧集", "Season 03", "Queue.Show.S03E02.mkv")
	identity := catalog.Media{ID: 7654321, MediaType: "tv", Title: "队列测试剧集", Year: 2024}
	a.config.Downloaders = []DownloaderConfig{{Name: "测试下载器", Config: download.Config{ID: "summary-qb", Kind: "qb", BaseURL: "https://downloader.invalid"}}}
	a.config.Roots = []Root{{ID: "summary-root", Name: "测试影视目录", Path: filepath.Join(base, "library"), Kind: "mixed"}}
	asset := Asset{ID: "summary-asset", Media: identity, Season: 3, Episodes: []int{2}, Path: target, SourcePath: source, RootID: "summary-root", State: "present"}
	summaryRecord(t, a, "asset", asset.ID, asset)
	summaryRecord(t, a, "native_download", "summary-native", NativeDownload{ID: "summary-native", ResourceTitle: "Queue.Show.S03E02.1080p.WEB-DL", Media: &identity, DownloaderID: "summary-qb", DownloadPath: filepath.Dir(source)})
	summaryRecord(t, a, "intent", "summary-intent", Intent{ID: "summary-intent", Media: identity, Season: 3, Episodes: []int{2}, ResourceTitle: "Queue.Show.S03E02.1080p.WEB-DL", DownloaderID: "summary-qb", DownloadPath: filepath.Dir(source)})
	plan := ImportPlan{ID: "summary-plan", Media: identity, Mode: "hardlink", RootID: "summary-root", Files: []PlanFile{{ID: "summary-file", Source: source, Target: target, Season: 3, Episodes: []int{2}, Status: "ready"}}}
	summaryRecord(t, a, "import_plan", plan.ID, plan)
	summaryRecord(t, a, "subscription", "summary-subscription", Subscription{ID: "summary-subscription", Media: identity, Season: 3, StartEpisode: 2, Revision: 1, DownloaderID: "summary-qb"})
	summaryRecord(t, a, "reorganize_cleanup", plan.ID, reorganizeCleanup{ID: plan.ID, OldAssetID: asset.ID, NewAssetID: "summary-new-asset", NewPath: filepath.Join(base, "new-library", "Queue.Show.S03E02.mkv")})

	cases := []struct {
		kind    string
		payload any
		subject string
	}{
		{"native_download", map[string]string{"id": "summary-native"}, identity.Title},
		{"download", map[string]string{"id": "summary-intent"}, identity.Title},
		{"import", map[string]any{"id": plan.ID, "file_ids": []string{"summary-file"}}, identity.Title},
		{"reorganize_cleanup", map[string]string{"id": plan.ID}, identity.Title},
		{"scrape", map[string]string{"id": asset.ID}, identity.Title},
		{"fnos_sync", map[string]string{"id": asset.ID}, identity.Title},
		{"subscription", map[string]any{"id": "summary-subscription", "revision": 1}, identity.Title},
		{"library_scan", map[string]string{"root_id": "summary-root"}, "测试影视目录"},
		{"asset_check", map[string]string{"id": asset.ID}, identity.Title},
	}
	jobs := make([]Job, 0, len(cases))
	for _, tc := range cases {
		jobs = append(jobs, summaryJob(t, a, tc.kind, tc.payload, "summary-"+tc.kind))
	}
	views, err := a.jobQueue(context.Background())
	if err != nil || len(views) != len(cases) {
		t.Fatalf("jobQueue count=%d err=%v", len(views), err)
	}
	byID := make(map[string]jobView, len(views))
	for _, view := range views {
		byID[view.ID] = view
	}
	for i, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			view := byID[jobs[i].ID]
			if view.ID != jobs[i].ID || view.Kind != tc.kind {
				t.Fatalf("job identity/order changed: %+v", view)
			}
			summary := view.Summary
			if summary.TypeLabel == "" || summary.TypeLabel == tc.kind || !strings.ContainsFunc(summary.TypeLabel, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
				t.Fatalf("task type has no readable Chinese label: %+v", summary)
			}
			if !strings.Contains(summary.Subject, tc.subject) || summary.Title != summary.TypeLabel+" · "+summary.Subject || summary.Stage == "" {
				t.Fatalf("task lacks its actual object/action: %+v", summary)
			}
			if tc.kind == "download" || tc.kind == "import" || tc.kind == "scrape" || tc.kind == "fnos_sync" || tc.kind == "asset_check" || tc.kind == "subscription" {
				if !strings.Contains(summary.Subject, "第3季") {
					t.Fatalf("TV subject lost season: %+v", summary)
				}
			}
			if tc.kind == "download" || tc.kind == "import" || tc.kind == "scrape" || tc.kind == "fnos_sync" || tc.kind == "asset_check" {
				if !strings.Contains(summary.Subject, "第2集") {
					t.Fatalf("episode task lost episode: %+v", summary)
				}
			}
			if tc.kind == "native_download" || tc.kind == "download" {
				if summary.Downloader != "测试下载器" || summary.Target != filepath.Dir(source) {
					t.Fatalf("download destination not identified: %+v", summary)
				}
			}
		})
	}
}

func TestJobSummaryImportUsesOnlyThisJobsFiles(t *testing.T) {
	a := newTestApp(t)
	base := t.TempDir()
	first := PlanFile{ID: "first-file", Source: filepath.Join(base, "Not.Selected.S01E01.mkv"), Target: filepath.Join(base, "library", "Not.Selected.S01E01.mkv"), Season: 1, Episodes: []int{1}, Status: "ready"}
	second := PlanFile{ID: "second-file", Source: filepath.Join(base, "Selected.S01E02.mkv"), Target: filepath.Join(base, "library", "Selected.S01E02.mkv"), Season: 1, Episodes: []int{2}, Status: "ready"}
	plan := ImportPlan{ID: "selected-plan", Media: catalog.Media{ID: 987654, MediaType: "tv", Title: "本次整理", Year: 2024}, Mode: "copy", Files: []PlanFile{first, second}}
	summaryRecord(t, a, "import_plan", plan.ID, plan)
	job := summaryJob(t, a, "import", map[string]any{"id": plan.ID, "file_ids": []string{second.ID}}, "selected-workflow")
	view, err := a.jobView(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if view.Summary.FileCount != 1 || view.Summary.Source != second.Source || view.Summary.Target != second.Target {
		t.Fatalf("summary includes unrelated plan files: %+v", view.Summary)
	}
	data, err := json.Marshal(view.Summary)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Not.Selected") || strings.Contains(view.Summary.Subject, "第1集") || !strings.Contains(view.Summary.Subject, "第2集") {
		t.Fatalf("another job's file escaped into summary: %s", data)
	}
}

func TestJobSummaryMissingRecordsKeepReadableFallback(t *testing.T) {
	a := newTestApp(t)
	for _, kind := range []string{"native_download", "download", "import", "reorganize_cleanup", "scrape", "fnos_sync", "subscription", "library_scan", "asset_check"} {
		t.Run(kind, func(t *testing.T) {
			job := summaryJob(t, a, kind, map[string]string{"id": "missing-record", "root_id": "missing-root"}, "missing-"+kind)
			view, err := a.jobView(context.Background(), job)
			if err != nil {
				t.Fatal(err)
			}
			if view.Summary.Subject == "" || view.Summary.Title != view.Summary.TypeLabel+" · "+view.Summary.Subject || view.Summary.TypeLabel == kind {
				t.Fatalf("deleted/missing record made queue unreadable: %+v", view.Summary)
			}
		})
	}
}

func TestJobSummaryActivityBelongsToTheExactJob(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	job := summaryJob(t, a, "download", map[string]string{"id": "event-intent"}, "shared-workflow")
	sibling := summaryJob(t, a, "import", map[string]string{"id": "event-plan"}, job.WorkflowID)
	insert := func(time, jobID, message string) {
		t.Helper()
		if _, err := a.Store.DB.ExecContext(ctx, `INSERT INTO events(time,workflow_id,job_id,level,module,event,message) VALUES(?,?,?,?,?,?,?)`, time, job.WorkflowID, jobID, "INFO", "test", "stage", message); err != nil {
			t.Fatal(err)
		}
	}
	insert("2026-10-15T12:01:00Z", job.ID, "正在核对当前资源")
	insert("2026-10-15T12:02:00Z", job.ID, "当前资源已提交下载器")
	insert("2026-10-15T12:03:00Z", sibling.ID, "兄弟任务正在刮削另一部影片")
	views, err := a.jobQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]jobSummary, len(views))
	for _, view := range views {
		byID[view.ID] = view.Summary
	}
	if summary := byID[job.ID]; summary.Activity != "当前资源已提交下载器" || summary.ActivityAt != "2026-10-15T12:02:00Z" {
		t.Fatalf("activity did not use latest event for job_id: %+v", summary)
	}
	if summary := byID[sibling.ID]; summary.Activity != "兄弟任务正在刮削另一部影片" {
		t.Fatalf("sibling's own activity missing: %+v", summary)
	}
}

func TestJobSummaryProjectionDoesNotExposePrivateRecordFields(t *testing.T) {
	a := newTestApp(t)
	const snapshot = "synthetic-downloader-private-snapshot"
	const material = "synthetic-torrent-material-reference"
	const key = "synthetic-url-api-key"
	const password = "synthetic-downloader-password"
	const overview = "synthetic-private-whole-media-object"
	url := "https://fixture.invalid/download?api_key=" + key
	a.config.Downloaders = []DownloaderConfig{{Name: "安全下载器", Config: download.Config{ID: "safe-qb", Kind: "qb", BaseURL: url, Password: password}}}
	media := catalog.Media{ID: 99991, MediaType: "movie", Title: "安全摘要测试", Year: 2024, Overview: overview}
	summaryRecord(t, a, "native_download", "private-native", NativeDownload{ID: "private-native", ResourceTitle: "安全资源 " + url, Media: &media, DownloaderID: "safe-qb", DownloadPath: "/downloads", DownloaderSnapshot: snapshot, MaterialRef: material})
	summaryRecord(t, a, "native_download", "private-unrecognized-native", NativeDownload{ID: "private-unrecognized-native", ResourceTitle: "安全资源 " + url, DownloaderID: "safe-qb", DownloadPath: "/downloads", DownloaderSnapshot: snapshot, MaterialRef: material})
	summaryRecord(t, a, "intent", "private-intent", Intent{ID: "private-intent", Media: media, ResourceTitle: "安全资源 " + url, DownloaderID: "safe-qb", DownloadPath: "/downloads", DownloaderSnapshot: snapshot, MaterialRef: material})
	jobs := []Job{
		summaryJob(t, a, "native_download", map[string]string{"id": "private-native"}, "private-native-workflow"),
		summaryJob(t, a, "native_download", map[string]string{"id": "private-unrecognized-native"}, "private-unrecognized-native-workflow"),
		summaryJob(t, a, "download", map[string]string{"id": "private-intent"}, "private-intent-workflow"),
	}
	views, err := a.jobQueue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"type_label": true, "title": true, "subject": true, "stage": true, "source": true, "target": true, "file_count": true, "downloader": true, "activity": true, "activity_at": true}
	for _, view := range views {
		if view.ID == jobs[1].ID && !strings.Contains(view.Summary.Subject, "安全资源") {
			t.Fatalf("unrecognized resource lost its readable filename fallback: %+v", view.Summary)
		}
		data, err := json.Marshal(view)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{snapshot, material, key, password, overview, "https://fixture.invalid", "downloader_snapshot", "material_ref"} {
			if strings.Contains(string(data), forbidden) {
				t.Fatalf("private record data leaked in job projection: forbidden=%q", forbidden)
			}
		}
		summaryJSON, err := json.Marshal(view.Summary)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(summaryJSON, &fields); err != nil {
			t.Fatal(err)
		}
		for field := range fields {
			if !allowed[field] {
				t.Fatalf("summary contains non-allowlisted field %q", field)
			}
		}
	}
}

func TestJobSummaryDownloadSuccessMeansSubmissionOnly(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	summaryRecord(t, a, "native_download", "accepted-native", NativeDownload{ID: "accepted-native", ResourceTitle: "Still.Downloading.2024", State: "accepted"})
	summaryRecord(t, a, "intent", "accepted-intent", Intent{ID: "accepted-intent", Media: catalog.Media{ID: 22222, MediaType: "movie", Title: "仍在下载"}, State: "downloading", Progress: .1})
	for _, tc := range []struct{ kind, id string }{{"native_download", "accepted-native"}, {"download", "accepted-intent"}} {
		t.Run(tc.kind, func(t *testing.T) {
			job := summaryJob(t, a, tc.kind, map[string]string{"id": tc.id}, "accepted-"+tc.kind)
			job.State = "succeeded"
			view, err := a.jobView(ctx, job)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(view.Summary.Stage, "已提交下载器") || strings.Contains(view.Summary.Stage, "下载完成") {
				t.Fatalf("submission task falsely claims download completion: %+v", view.Summary)
			}
		})
	}
}

func TestJobSummaryDetailEventsDoNotIncludeSiblingTasks(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	parent := summaryJob(t, a, "subscription", map[string]string{"id": "parent"}, "shared-flow")
	child := summaryJob(t, a, "download", map[string]string{"id": "child"}, "shared-flow")
	a.event(ctx, parent, "INFO", "subscriptions", "searching", "正在检查订阅")
	a.event(ctx, child, "INFO", "downloads", "accepted", "另一个任务已提交下载")
	cookie := setupTestAdmin(t, a)
	response := callTest(t, a.Handler(), "GET", "/api/jobs/"+parent.ID, "", cookie, "")
	var body struct {
		Data struct {
			Events []Event `json:"events"`
		} `json:"data"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Data.Events) != 2 {
		t.Fatalf("wrong task timeline: status=%d body=%s", response.Code, response.Body)
	}
	for _, event := range body.Data.Events {
		if event.JobID != parent.ID {
			t.Fatal("sibling task activity leaked into detail", event)
		}
	}
}

func TestJobSummaryHTTPListAndDetailStayConsistent(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	media := catalog.Media{ID: 564738, MediaType: "tv", Title: "接口摘要测试", Year: 2024}
	summaryRecord(t, a, "subscription", "http-summary-subscription", Subscription{ID: "http-summary-subscription", Media: media, Season: 2, Revision: 1})
	job := summaryJob(t, a, "subscription", map[string]any{"id": "http-summary-subscription", "revision": 1}, "http-summary-workflow")
	a.event(ctx, job, "INFO", "subscriptions", "searching", "正在搜索这部剧的缺集资源")
	cookie := setupTestAdmin(t, a)
	list := callTest(t, a.Handler(), "GET", "/api/jobs", "", cookie, "")
	if list.Code != 200 {
		t.Fatalf("jobs list status=%d body=%s", list.Code, list.Body)
	}
	var listBody struct {
		Data []jobView `json:"data"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listBody); err != nil {
		t.Fatal(err)
	}
	if len(listBody.Data) != 1 || listBody.Data[0].ID != job.ID {
		t.Fatalf("unexpected jobs list: %+v", listBody.Data)
	}
	detail := callTest(t, a.Handler(), "GET", "/api/jobs/"+job.ID, "", cookie, "")
	if detail.Code != 200 {
		t.Fatalf("job detail status=%d body=%s", detail.Code, detail.Body)
	}
	var detailBody struct {
		Data struct {
			Job jobView `json:"job"`
		} `json:"data"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &detailBody); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(listBody.Data[0].Summary, detailBody.Data.Job.Summary) {
		t.Fatalf("list and detail disagree: list=%+v detail=%+v", listBody.Data[0].Summary, detailBody.Data.Job.Summary)
	}
	if !strings.Contains(detailBody.Data.Job.Summary.Title, "接口摘要测试") || detailBody.Data.Job.Summary.Activity != "正在搜索这部剧的缺集资源" {
		t.Fatalf("HTTP response omitted real task summary: %+v", detailBody.Data.Job.Summary)
	}
}

func TestJobQueueKeepsSubscriptionVisibleDuringBulkOrganize(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	running := summaryJob(t, a, "subscription", map[string]any{"id": "currently-checking"}, "subscriptions")
	waiting := summaryJob(t, a, "subscription", map[string]any{"id": "waiting-for-check"}, "subscriptions")
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='running' WHERE id=?`, running.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`WITH RECURSIVE seq(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM seq WHERE i<125)
		INSERT INTO jobs(id,workflow_id,kind,state,payload,run_at,created_at,updated_at)
		SELECT 'new-file-'||i,'bulk-organize','import',CASE WHEN i%2=0 THEN 'failed' ELSE 'queued' END,CAST('{}' AS BLOB),0,?,? FROM seq`, now(), now()); err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), "GET", "/api/jobs", "", cookie, "")
	var body struct {
		Data []jobView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != 200 {
		t.Fatalf("jobs list status=%d error=%v", response.Code, err)
	}
	if len(body.Data) != 100 || body.Data[0].ID != running.ID || body.Data[1].ID != waiting.ID {
		t.Fatal("bulk organizing tasks hid a running or waiting subscription")
	}
}
