package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"light-media/internal/catalog"
)

type historyParityPageResult struct {
	Items    []transferHistoryItem `json:"items"`
	Total    int                   `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
}

type historyParityQueueFile struct {
	ID        string `json:"id"`
	Source    string `json:"source"`
	Target    string `json:"target"`
	Status    string `json:"status"`
	Error     string `json:"error"`
	JobID     string `json:"job_id"`
	JobState  string `json:"job_state"`
	Size      int64  `json:"size"`
	CanCancel bool   `json:"can_cancel"`
}

type historyParityQueueGroup struct {
	ID     string                   `json:"id"`
	Title  string                   `json:"title"`
	Status string                   `json:"status"`
	Error  string                   `json:"error"`
	Files  []historyParityQueueFile `json:"files"`
}

func historyParityDecode(t *testing.T, value any, out any) {
	t.Helper()
	data, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(data, out); e != nil {
		t.Fatalf("decode history response: %s: %v", data, e)
	}
}

func historyParityReadPage(t *testing.T, a *App, query string) historyParityPageResult {
	t.Helper()
	out, e := a.transferHistoryAPI(httptest.NewRequest("GET", "/history/transfers?"+query, nil), "transfers")
	if e != nil {
		t.Fatal(e)
	}
	var page historyParityPageResult
	historyParityDecode(t, out, &page)
	return page
}

func historyParityReadQueue(t *testing.T, a *App) []historyParityQueueGroup {
	t.Helper()
	out, e := a.transferHistoryAPI(httptest.NewRequest("GET", "/history/transfers/queue", nil), "transfers/queue")
	if e != nil {
		t.Fatal(e)
	}
	var queue struct {
		Groups []historyParityQueueGroup `json:"groups"`
	}
	historyParityDecode(t, out, &queue)
	return queue.Groups
}

// The durable-record projection also has to support plans created by earlier
// versions. Jobs are not run here: every file is an isolated synthetic source.
func historyParityPlan(t *testing.T, a *App, source, target, key, title, jobState, fileState string, names ...string) (ImportPlan, Job) {
	t.Helper()
	ctx := context.Background()
	plan := ImportPlan{ID: "parity-plan-" + key, WorkflowID: "parity-wf-" + key, RootID: "root", State: "queued", CreatedAt: now(), Mode: "copy", Media: catalog.Media{ID: 123, MediaType: "movie", Title: title, Year: 2024}}
	for index, name := range names {
		path := filepath.Join(source, key, name)
		content := []byte("isolated history parity fixture " + key + name)
		importWrite(t, path, content)
		plan.Files = append(plan.Files, PlanFile{ID: fmt.Sprintf("%s-file-%d", key, index), Source: path, Target: filepath.Join(target, key, name), Method: "copy", Status: fileState, Size: int64(len(content))})
	}
	if e := a.Store.Put(ctx, "import_plan", plan.ID, plan); e != nil {
		t.Fatal(e)
	}
	job, e := a.enqueue(ctx, "import", map[string]any{"id": plan.ID}, plan.WorkflowID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`UPDATE jobs SET state=?,error_message=? WHERE id=?`, jobState, "", job.ID); e != nil {
		t.Fatal(e)
	}
	job.State = jobState
	return plan, job
}

func TestHistoryParityV031SupportsThousandRowPages(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	plan := ImportPlan{ID: "thousand-row-history", WorkflowID: "thousand-row-wf", Media: catalog.Media{ID: 124, MediaType: "tv", Title: "分页合成样本"}, State: "complete", CreatedAt: now(), Mode: "copy"}
	for index := 0; index < 1005; index++ {
		plan.Files = append(plan.Files, PlanFile{ID: fmt.Sprintf("page-file-%04d", index), Source: filepath.Join(source, fmt.Sprintf("page-%04d.mkv", index)), Target: filepath.Join(target, fmt.Sprintf("page-%04d.mkv", index)), Status: "imported", Season: 1, Episodes: []int{index + 1}})
	}
	if e := a.Store.Put(ctx, "import_plan", plan.ID, plan); e != nil {
		t.Fatal(e)
	}
	job, e := a.enqueue(ctx, "import", map[string]string{"id": plan.ID}, plan.WorkflowID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID); e != nil {
		t.Fatal(e)
	}
	first := historyParityReadPage(t, a, "page=1&page_size=1000")
	second := historyParityReadPage(t, a, "page=2&page_size=1000")
	if first.Total != 1005 || first.PageSize != 1000 || len(first.Items) != 1000 || second.Total != 1005 || second.Page != 2 || second.PageSize != 1000 || len(second.Items) != 5 {
		t.Fatalf("wrong large-page boundaries: first=%d/%d size=%d second=%d/%d size=%d", len(first.Items), first.Total, first.PageSize, len(second.Items), second.Total, second.PageSize)
	}
	seen := map[string]bool{}
	for _, item := range append(first.Items, second.Items...) {
		if seen[item.ID] {
			t.Fatalf("overlapping pages repeated %s", item.ID)
		}
		seen[item.ID] = true
	}
	if len(seen) != 1005 {
		t.Fatal("page boundary lost a record", len(seen))
	}
}

func TestHistoryParityV031ChineseStatusAndOriginalWildcards(t *testing.T) {
	a, source, target := importTestApp(t)
	historyParityPlan(t, a, source, target, "literal", "百分号下划线样本", "succeeded", "imported", "Release%_A.mkv")
	historyParityPlan(t, a, source, target, "near", "普通样本", "succeeded", "imported", "ReleaseXXA.mkv")
	historyParityPlan(t, a, source, target, "one", "第一集样本", "succeeded", "imported", "Show.2024.E01.mkv")
	historyParityPlan(t, a, source, target, "eleven", "第十一集样本", "succeeded", "imported", "Show.2024.E11.mkv")
	historyParityPlan(t, a, source, target, "three-digits", "三位集号样本", "succeeded", "imported", "Show.2024.E001.mkv")
	failed, _ := historyParityPlan(t, a, source, target, "failed", "失败样本", "failed", "ready", "Broken.mkv")
	pending, _ := historyParityPlan(t, a, source, target, "pending", "等待样本", "queued", "ready", "Waiting.mkv")
	for _, test := range []struct {
		query string
		want  []string
	}{
		{"%", []string{"literal-file-0"}},
		{"_A.mkv", []string{"literal-file-0"}},
		{"%_", []string{"literal-file-0"}},
		{"Show*E?1.mkv", []string{"eleven-file-0", "one-file-0"}},
		{"Release*", []string{"literal-file-0", "near-file-0"}},
		{"成功", []string{"eleven-file-0", "literal-file-0", "near-file-0", "one-file-0", "three-digits-file-0"}},
		{"失败", []string{failed.Files[0].ID}},
	} {
		t.Run(test.query, func(t *testing.T) {
			page := historyParityReadPage(t, a, url.Values{"q": {test.query}, "page_size": {"1000"}}.Encode())
			var ids []string
			for _, item := range page.Items {
				ids = append(ids, item.ID)
			}
			sort.Strings(ids)
			if page.Total != len(test.want) || !reflect.DeepEqual(ids, test.want) {
				t.Fatalf("search %q matched wrong records: total=%d ids=%v want=%v", test.query, page.Total, ids, test.want)
			}
		})
	}
	page := historyParityReadPage(t, a, "status=pending")
	if page.Total != 1 || page.Items[0].ID != pending.Files[0].ID {
		t.Fatalf("pending leaked into success/failure or was lost: %+v", page)
	}
}

func TestHistoryParityV031QueueContainsOnlyActiveImportJobs(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	want := map[string]Job{}
	for _, state := range []string{"queued", "running", "retry_wait", "waiting_paused", "failed", "succeeded", "cancelled"} {
		fileState := "ready"
		if state == "succeeded" {
			fileState = "imported"
		}
		_, job := historyParityPlan(t, a, source, target, state, state+"媒体", state, fileState, state+".mkv")
		if state == "failed" {
			if _, e := a.Store.DB.Exec(`UPDATE jobs SET error_message='synthetic full disk' WHERE id=?`, job.ID); e != nil {
				t.Fatal(e)
			}
		}
		switch state {
		case "queued", "running", "retry_wait", "waiting_paused":
			want[job.ID] = job
		}
	}
	for _, kind := range []string{"download", "scrape", "fnos_sync", "library_scan"} {
		if _, e := a.enqueue(ctx, kind, map[string]string{"id": "unrelated-" + kind}, "other-workflow"); e != nil {
			t.Fatal(e)
		}
	}
	groups := historyParityReadQueue(t, a)
	if len(groups) != len(want) {
		t.Fatalf("queue did not contain exactly the active import jobs: got=%d want=%d", len(groups), len(want))
	}
	for _, group := range groups {
		job, ok := want[group.ID]
		if !ok || group.Status != job.State || len(group.Files) != 1 {
			t.Fatalf("queue disguised job state or mixed jobs: %+v", group)
		}
		file := group.Files[0]
		if file.JobID != job.ID || file.JobState != job.State || file.Size <= 0 || file.Source == "" || file.Target == "" || !file.CanCancel {
			t.Fatalf("per-file queue facts lost: %+v job=%+v", file, job)
		}
	}
	for _, job := range want {
		if _, e := a.Store.DB.Exec(`UPDATE jobs SET state='cancelled' WHERE id=?`, job.ID); e != nil {
			t.Fatal(e)
		}
	}
	if groups = historyParityReadQueue(t, a); len(groups) != 0 {
		t.Fatalf("queue retained jobs after all active imports ended: %+v", groups)
	}
	history := historyParityReadPage(t, a, "")
	if history.Total != 7 {
		t.Fatalf("hiding terminal jobs from the queue lost history: %+v", history)
	}
	for _, item := range history.Items {
		if item.JobState == "failed" && (item.Status != "failed" || item.Error != "synthetic full disk") {
			t.Fatalf("failed history lost actual job error: %+v", item)
		}
		if item.JobState == "succeeded" && item.Status != "success" {
			t.Fatalf("successful history lost its outcome: %+v", item)
		}
	}
}

func TestHistoryParityV031QueueCancellationIsExactAndPreservesFiles(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	selected, selectedJob := historyParityPlan(t, a, source, target, "selected", "选中文件", "queued", "ready", "Selected.mkv")
	other, otherJob := historyParityPlan(t, a, source, target, "other", "其他文件", "running", "ready", "Other.mkv")
	multi, multiJob := historyParityPlan(t, a, source, target, "multi", "旧批量文件", "queued", "ready", "First.mkv", "Second.mkv")
	finished, finishedJob := historyParityPlan(t, a, source, target, "finished", "已完成文件", "succeeded", "imported", "Finished.mkv")
	scrapeJob, e := a.enqueue(ctx, "scrape", map[string]string{"id": "unrelated-scrape"}, "other-workflow")
	if e != nil {
		t.Fatal(e)
	}
	for index, plan := range []ImportPlan{selected, other, multi, finished} {
		if _, e = a.Store.DB.Exec(`INSERT INTO reservations VALUES(?,?,?,?)`, fmt.Sprintf("movie:%d", 2000+index), 0, 0, plan.ID); e != nil {
			t.Fatal(e)
		}
		for _, file := range plan.Files {
			importWrite(t, file.Target, []byte("existing isolated target "+file.ID))
		}
	}
	before := map[string][]byte{}
	for _, plan := range []ImportPlan{selected, other, multi, finished} {
		for _, file := range plan.Files {
			for _, path := range []string{file.Source, file.Target} {
				before[path], e = os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
			}
		}
	}
	out, e := a.transferHistoryAPI(httptest.NewRequest("POST", "/history/transfers/queue/"+selected.Files[0].ID+"/cancel", nil), "transfers/queue/"+selected.Files[0].ID+"/cancel")
	if e != nil || out.(map[string]bool)["cancelled"] != true {
		t.Fatalf("single-file cancel failed: %+v %v", out, e)
	}
	for key, code := range map[string]string{selected.Files[0].ID: "QUEUE_FILE_FINISHED", finished.Files[0].ID: "QUEUE_FILE_FINISHED", multi.Files[0].ID: "QUEUE_FILE_NOT_CANCELLABLE"} {
		if _, e = a.cancelOrganizeQueueFile(ctx, key); fileCode(e) != code {
			t.Fatalf("unsafe/repeated cancel %s: got=%v want=%s", key, e, code)
		}
	}
	for id, expected := range map[string]string{selectedJob.ID: "cancelled", otherJob.ID: "running", multiJob.ID: "queued", finishedJob.ID: "succeeded", scrapeJob.ID: "queued"} {
		job, e := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, id))
		if e != nil || job.State != expected {
			t.Fatalf("cancel touched unrelated job: %s state=%s err=%v want=%s", id, job.State, e, expected)
		}
		if id == selectedJob.ID && (job.Generation != 1 || job.LeaseUntil != 0) {
			t.Fatalf("cancel did not invalidate worker lease: %+v", job)
		}
	}
	for _, plan := range []ImportPlan{selected, other, multi, finished} {
		var count int
		if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM reservations WHERE intent_id=?`, plan.ID).Scan(&count); e != nil {
			t.Fatal(e)
		}
		wantCount := 1
		if plan.ID == selected.ID {
			wantCount = 0
		}
		if count != wantCount {
			t.Fatalf("cancel released wrong reservation: %s count=%d want=%d", plan.ID, count, wantCount)
		}
	}
	for path, old := range before {
		current, e := os.ReadFile(path)
		if e != nil || !bytes.Equal(old, current) {
			t.Fatalf("cancel changed source/target bytes: %s %v", path, e)
		}
	}
}

func TestHistoryParityV031QueueExcludesUnsubmittedPlanFiles(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	plan, job := historyParityPlan(t, a, source, target, "selection", "部分提交的旧计划", "queued", "ready", "Selected.mkv", "NotSubmitted.mkv")
	payload, e := json.Marshal(map[string]any{"id": plan.ID, "file_ids": []string{plan.Files[0].ID}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`UPDATE jobs SET payload=? WHERE id=?`, payload, job.ID); e != nil {
		t.Fatal(e)
	}
	// Existing history correctly includes only the actually selected file. The
	// queue must use the same execution boundary rather than the entire preview.
	history := historyParityReadPage(t, a, "")
	if history.Total != 1 || history.Items[0].ID != plan.Files[0].ID {
		t.Fatalf("history execution selection fixture invalid: %+v", history)
	}
	groups := historyParityReadQueue(t, a)
	if len(groups) != 1 || len(groups[0].Files) != 1 || groups[0].Files[0].ID != plan.Files[0].ID {
		t.Fatalf("unsubmitted preview files appeared as queue tasks: %+v", groups)
	}
	if _, e = a.cancelOrganizeQueueFile(ctx, plan.Files[0].ID); fileCode(e) != "QUEUE_FILE_NOT_CANCELLABLE" {
		t.Fatal("legacy multi-file plan cancellation must not cancel its whole group", e)
	}
}

func TestHistoryParityV031CancelReleasesOnlyActualIntentReservation(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	plan, job := historyParityPlan(t, a, source, target, "intent-owner", "带下载上下文的整理", "queued", "ready", "Owned.mkv")
	plan.IntentID = "download-intent-owner"
	if e := a.Store.Put(ctx, "import_plan", plan.ID, plan); e != nil {
		t.Fatal(e)
	}
	intent := Intent{ID: plan.IntentID, WorkflowID: plan.WorkflowID, Media: plan.Media, State: "importing", RootID: "root", ImportPlanID: plan.ID, CreatedAt: now()}
	if e := a.Store.Put(ctx, "intent", intent.ID, intent); e != nil {
		t.Fatal(e)
	}
	for _, owner := range []struct{ media, id string }{{mediaKey(plan.Media), plan.IntentID}, {"movie:8002", "unrelated-intent"}} {
		if _, e := a.Store.DB.Exec(`INSERT INTO reservations VALUES(?,?,?,?)`, owner.media, 0, 0, owner.id); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := a.cancelOrganizeQueueFile(ctx, plan.Files[0].ID); e != nil {
		t.Fatal(e)
	}
	var selected, unrelated int
	if e := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM reservations WHERE intent_id=?`, plan.IntentID).Scan(&selected); e != nil {
		t.Fatal(e)
	}
	if e := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM reservations WHERE intent_id='unrelated-intent'`).Scan(&unrelated); e != nil {
		t.Fatal(e)
	}
	if selected != 0 || unrelated != 1 {
		t.Fatalf("cancel left its actual download-intent slot occupied or released another intent: selected=%d unrelated=%d job=%s", selected, unrelated, job.ID)
	}
}

func TestHistoryParityV031QueueShowsActiveRetryAndKeepsOtherSelectionInHistory(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	plan, first := historyParityPlan(t, a, source, target, "separate", "同一计划分别提交", "failed", "ready", "One.mkv", "Two.mkv", "NeverSubmitted.mkv")
	firstPayload, e := json.Marshal(map[string]any{"id": plan.ID, "file_ids": []string{plan.Files[0].ID}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`UPDATE jobs SET payload=?,created_at='2026-10-15T10:00:00.000000001Z' WHERE id=?`, firstPayload, first.ID); e != nil {
		t.Fatal(e)
	}
	second, e := a.enqueue(ctx, "import", map[string]any{"id": plan.ID, "file_ids": []string{plan.Files[1].ID}}, plan.WorkflowID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='failed',created_at='2026-10-15T10:00:00.000000002Z',error_message='only second file failed' WHERE id=?`, second.ID); e != nil {
		t.Fatal(e)
	}
	retry, e := a.enqueue(ctx, "import", map[string]any{"id": plan.ID, "file_ids": []string{plan.Files[0].ID}}, plan.WorkflowID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='running',created_at='2026-10-15T10:00:00.000000003Z' WHERE id=?`, retry.ID); e != nil {
		t.Fatal(e)
	}
	files := map[string]historyParityQueueFile{}
	for _, group := range historyParityReadQueue(t, a) {
		for _, file := range group.Files {
			if _, exists := files[file.ID]; exists {
				t.Fatalf("older retry duplicated a queue file: %+v", file)
			}
			files[file.ID] = file
		}
	}
	if len(files) != 1 || files[plan.Files[0].ID].JobID != retry.ID || files[plan.Files[0].ID].JobState != "running" {
		t.Fatalf("queue lost the active retry or included an ended selection: %+v", files)
	}
	history := historyParityReadPage(t, a, "status=failed")
	if history.Total != 1 || history.Items[0].ID != plan.Files[1].ID || history.Items[0].JobID != second.ID || history.Items[0].Error != "only second file failed" {
		t.Fatalf("failed selection did not remain in history: %+v", history)
	}
}
