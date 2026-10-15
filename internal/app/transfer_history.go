package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"light-media/internal/catalog"
)

type transferHistoryItem struct {
	ID           string        `json:"id"`
	PlanID       string        `json:"plan_id"`
	WorkflowID   string        `json:"workflow_id"`
	JobID        string        `json:"job_id"`
	JobState     string        `json:"job_state"`
	Media        catalog.Media `json:"media"`
	Season       int           `json:"season"`
	Episodes     []int         `json:"episodes"`
	Part         string        `json:"part,omitempty"`
	Source       string        `json:"source"`
	Target       string        `json:"target"`
	Mode         string        `json:"mode"`
	Size         int64         `json:"size"`
	Status       string        `json:"status"`
	FileStatus   string        `json:"file_status"`
	Error        string        `json:"error"`
	CreatedAt    string        `json:"created_at"`
	UpdatedAt    string        `json:"updated_at"`
	CanRetry     bool          `json:"can_retry"`
	CanRecognize bool          `json:"can_recognize"`
}

// History is a paginated projection of durable per-file import plans and the
// latest execution job. Merely opening a preview does not create history.
const transferHistoryJobMatch = `j.kind='import' AND json_extract(j.payload,'$.id')=r.id AND (
 (COALESCE(json_array_length(j.payload,'$.file_ids'),0)=0 AND json_extract(f.value,'$.status')!='blocked')
 OR EXISTS(SELECT 1 FROM json_each(j.payload,'$.file_ids') selected WHERE selected.value=json_extract(f.value,'$.id'))
)`

const transferHistoryCTE = `WITH history AS (
 SELECT r.id AS plan_id, json_remove(r.payload,'$.files','$.media.cast','$.media.recommendations','$.media.seasons') AS plan, f.value AS file, r.updated_at AS updated_at,
 COALESCE((SELECT j.id FROM jobs j WHERE ` + transferHistoryJobMatch + ` ORDER BY j.created_at DESC LIMIT 1),'') AS job_id,
 COALESCE((SELECT j.state FROM jobs j WHERE ` + transferHistoryJobMatch + ` ORDER BY j.created_at DESC LIMIT 1),'') AS job_state,
 COALESCE((SELECT j.error_message FROM jobs j WHERE ` + transferHistoryJobMatch + ` ORDER BY j.created_at DESC LIMIT 1),'') AS job_error,
 CASE WHEN json_extract(f.value,'$.status') IN ('imported','already_imported') THEN 'success'
 WHEN EXISTS (SELECT 1 FROM jobs j WHERE ` + transferHistoryJobMatch + ` AND j.state IN ('queued','running','retry_wait','waiting_paused')) THEN 'pending'
 ELSE 'failed' END AS status
 FROM records r,json_each(r.payload,'$.files') f
 WHERE r.kind='import_plan' AND json_extract(r.payload,'$.state') NOT IN ('preview','superseded')
 AND NOT EXISTS(SELECT 1 FROM records hidden WHERE hidden.kind='transfer_history_hidden' AND hidden.id=json_extract(f.value,'$.id'))
 AND EXISTS(SELECT 1 FROM jobs j WHERE ` + transferHistoryJobMatch + `)
) `

func (a *App) transferHistoryAPI(r *http.Request, p string) (any, error) {
	parts := strings.Split(p, "/")
	if p == "transfers/queue" && r.Method == "GET" {
		return a.organizeQueue(r.Context())
	}
	if len(parts) == 4 && parts[0] == "transfers" && parts[1] == "queue" && parts[3] == "cancel" && r.Method == "POST" {
		return a.cancelOrganizeQueueFile(r.Context(), parts[2])
	}
	if len(parts) == 1 && p == "transfers" && r.Method == "GET" {
		return a.transferHistory(r.Context(), r)
	}
	if len(parts) >= 2 && parts[0] == "transfers" {
		item, e := a.transferHistoryItem(r.Context(), parts[1])
		if e != nil {
			return nil, e
		}
		if len(parts) == 2 && r.Method == "GET" {
			return item, nil
		}
		if len(parts) == 2 && r.Method == "DELETE" {
			var in historyDeleteRequest
			if e = decodeBody(nil, r, &in); e != nil {
				return nil, e
			}
			return a.deleteTransferHistory(r.Context(), item, in)
		}
		if len(parts) == 3 && parts[2] == "retry" && r.Method == "POST" {
			var in struct {
				Confirm bool `json:"confirm"`
			}
			if e = decodeBody(nil, r, &in); e != nil {
				return nil, e
			}
			if !in.Confirm {
				return nil, bad("CONFIRM_REQUIRED", "请确认重试所选整理记录")
			}
			if !item.CanRetry {
				return nil, bad("TRANSFER_RETRY_UNAVAILABLE", "该记录无法直接重试，请重新识别并预览")
			}
			return a.executeImport(r.Context(), item.PlanID, []string{item.ID})
		}
	}
	return nil, &APIError{"NOT_FOUND", "接口不存在", 404}
}
func (a *App) transferHistory(ctx context.Context, r *http.Request) (any, error) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("page_size"))
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		page = 100000
	}
	if size < 1 || size > 1000 {
		size = 50
	}
	status := q.Get("status")
	if status != "" && status != "success" && status != "failed" && status != "pending" {
		return nil, bad("INVALID_HISTORY_STATUS", "整理状态筛选无效")
	}
	query := q.Get("q")
	if query == "成功" {
		status = "success"
		query = ""
	}
	if query == "失败" {
		status = "failed"
		query = ""
	}
	if len(query) > 256 {
		return nil, bad("INVALID_QUERY", "搜索内容过长")
	}
	pattern := historySearchPattern(query)
	where := ` WHERE (?='' OR status=?) AND (?='' OR lower(COALESCE(json_extract(plan,'$.media.title'),'')||' '||COALESCE(json_extract(file,'$.source'),'')||' '||COALESCE(json_extract(file,'$.target'),'')) LIKE lower(?) ESCAPE '\')`
	args := []any{status, status, query, pattern}
	var total int
	if e := a.Store.DB.QueryRowContext(ctx, transferHistoryCTE+`SELECT COUNT(*) FROM history`+where, args...).Scan(&total); e != nil {
		return nil, e
	}
	rows, e := a.Store.DB.QueryContext(ctx, transferHistoryCTE+`SELECT plan,file,job_id,job_state,job_error,updated_at,status FROM history`+where+` ORDER BY updated_at DESC,plan_id,json_extract(file,'$.id') LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if e != nil {
		return nil, e
	}
	items := []transferHistoryItem{}
	for rows.Next() {
		item, e := scanTransferHistory(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		items = append(items, item)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	// Filesystem checks happen after closing the single-connection SQL cursor.
	for i := range items {
		a.transferHistoryCapabilities(ctx, &items[i])
	}
	return map[string]any{"items": items, "total": total, "page": page, "page_size": size}, nil
}
func scanTransferHistory(row interface{ Scan(...any) error }) (transferHistoryItem, error) {
	var planRaw, fileRaw []byte
	var jobID, jobState, jobError, updated, status string
	if e := row.Scan(&planRaw, &fileRaw, &jobID, &jobState, &jobError, &updated, &status); e != nil {
		return transferHistoryItem{}, e
	}
	var plan ImportPlan
	var file PlanFile
	if e := json.Unmarshal(planRaw, &plan); e != nil {
		return transferHistoryItem{}, e
	}
	if e := json.Unmarshal(fileRaw, &file); e != nil {
		return transferHistoryItem{}, e
	}
	mode := file.Method
	if mode == "" {
		mode = plan.Mode
	}
	message := file.Reason
	if message == "" && status == "failed" {
		message = jobError
	}
	if message == "" && status == "failed" {
		message = "整理未完成，请查看关联任务"
	}
	return transferHistoryItem{ID: file.ID, PlanID: plan.ID, WorkflowID: plan.WorkflowID, JobID: jobID, JobState: jobState, Media: plan.Media, Season: file.Season, Episodes: file.Episodes, Part: file.Part, Source: file.Source, Target: file.Target, Mode: mode, Size: file.Size, Status: status, FileStatus: file.Status, Error: message, CreatedAt: plan.CreatedAt, UpdatedAt: updated, CanRetry: status == "failed" && jobState != "running" && jobState != "queued" && jobState != "retry_wait" && jobState != "waiting_paused" && file.Status != "blocked"}, nil
}
func (a *App) transferHistoryItem(ctx context.Context, id string) (transferHistoryItem, error) {
	item, e := scanTransferHistory(a.Store.DB.QueryRowContext(ctx, transferHistoryCTE+`SELECT plan,file,job_id,job_state,job_error,updated_at,status FROM history WHERE json_extract(file,'$.id')=? LIMIT 1`, id))
	if e != nil {
		return item, e
	}
	a.transferHistoryCapabilities(ctx, &item)
	return item, nil
}
func (a *App) transferHistoryCapabilities(ctx context.Context, item *transferHistoryItem) {
	_, e := snapshotPath(item.Source)
	item.CanRecognize = e == nil && a.sourceAllowed(item.Source)
	if !item.CanRecognize {
		item.CanRetry = false
		return
	}
	if item.CanRetry {
		var active int
		if e := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind='import' AND json_extract(payload,'$.id')=? AND state IN ('queued','running','retry_wait','waiting_paused')`, item.PlanID).Scan(&active); e != nil || active > 0 {
			item.CanRetry = false
			return
		}
		var plan ImportPlan
		if a.Store.Get(ctx, "import_plan", item.PlanID, &plan) != nil {
			item.CanRetry = false
			return
		}
		root, rootErr := a.importRoot(plan.RootID)
		var meta importMetadata
		if rootErr != nil || a.Store.Get(ctx, "import_meta", plan.ID, &meta) != nil || meta.RootPath != root.Path || a.importIntentActive(ctx, plan) != nil {
			item.CanRetry = false
			return
		}
		for _, file := range plan.Files {
			if file.ID == item.ID {
				current, e := snapshotPath(item.Source)
				if e != nil || current != planSnapshot(file) {
					item.CanRetry = false
				}
				break
			}
		}
	}
}
