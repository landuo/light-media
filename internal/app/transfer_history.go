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
	ID             string        `json:"id"`
	FileID         string        `json:"file_id"`
	ResourceSource string        `json:"-"`
	ExecutionKey   string        `json:"-"`
	PlanID         string        `json:"plan_id"`
	WorkflowID     string        `json:"workflow_id"`
	JobID          string        `json:"job_id"`
	JobState       string        `json:"job_state"`
	Media          catalog.Media `json:"media"`
	Season         int           `json:"season"`
	Episodes       []int         `json:"episodes"`
	Part           string        `json:"part,omitempty"`
	Source         string        `json:"source"`
	Target         string        `json:"target"`
	Mode           string        `json:"mode"`
	Size           int64         `json:"size"`
	Status         string        `json:"status"`
	FileStatus     string        `json:"file_status"`
	Error          string        `json:"error"`
	CreatedAt      string        `json:"created_at"`
	UpdatedAt      string        `json:"updated_at"`
	CanRetry       bool          `json:"can_retry"`
	CanRecognize   bool          `json:"can_recognize"`
}

// The visible record is stable per source resource; execution plans stay
// intact for recovery. Only submitted attempts participate, never previews.
// Job payloads store plan IDs as JSON strings. Concatenation keeps the ID
// text without column affinity, so SQLite can use both parts of jobs_subject.
const transferHistoryJobMatch = `j.kind='import' AND json_extract(j.payload,'$.id')=(r.id||'') AND (
 (COALESCE(json_array_length(j.payload,'$.file_ids'),0)=0 AND json_extract(f.value,'$.status')!='blocked')
 OR EXISTS(SELECT 1 FROM json_each(j.payload,'$.file_ids') selected WHERE selected.value=json_extract(f.value,'$.id'))
)`

// Normalize RFC3339Nano fractions before comparing creation times. This key
// survives VACUUM/backup restoration, unlike SQLite's implicit job rowid.
const transferHistoryJobsCTE = `WITH import_jobs AS NOT MATERIALIZED (
 SELECT *,substr(created_at,1,19)||'.'||substr(
 CASE WHEN substr(created_at,20,1)='.' THEN substr(created_at,21,length(created_at)-21) ELSE '' END
 ||'000000000',1,9)||'/'||id AS execution_key FROM jobs WHERE kind='import'
)`

const transferHistoryPlanSource = `COALESCE(NULLIF(json_extract(f.value,'$.history_source'),''),
 NULLIF(json_extract(meta.payload,'$.reorganize.asset.source_path'),''),
 NULLIF(json_extract(f.value,'$.source'),''),'file:'||json_extract(f.value,'$.id'))`

const transferHistoryExecutedFiles = `, executed_files AS (
 SELECT r.id AS plan_id, json_remove(r.payload,'$.files','$.media.cast','$.media.recommendations','$.media.seasons') AS plan, COALESCE(json_extract(cb.payload,'$.file'),f.value) AS file, COALESCE(cb.updated_at,r.updated_at) AS updated_at,
 j.id AS job_id,j.state AS job_state,j.error_message AS job_error,j.created_at AS executed_at,j.execution_key,
 MIN(j.execution_key) OVER (PARTITION BY r.id,json_extract(f.value,'$.id')) AS first_execution_key,
 ROW_NUMBER() OVER (PARTITION BY r.id,json_extract(f.value,'$.id') ORDER BY j.execution_key DESC) AS file_rank,
 ` + transferHistoryPlanSource + ` AS resource_source,
 CASE WHEN json_extract(cb.payload,'$.file.status')='imported' THEN 'success'
 WHEN json_extract(cb.payload,'$.file.status')='error' THEN 'failed'
 WHEN json_extract(f.value,'$.status') IN ('imported','already_imported') THEN 'success'
 WHEN j.state IN ('queued','running','retry_wait','waiting_paused') THEN 'pending'
 ELSE 'failed' END AS status
 FROM records r,json_each(r.payload,'$.files') f
 JOIN import_jobs j ON ` + transferHistoryJobMatch + `
 LEFT JOIN records meta ON meta.kind='import_meta' AND meta.id=r.id
 LEFT JOIN records cb ON cb.kind='original_transfer_callback' AND cb.id=r.id||'/'||json_extract(f.value,'$.id') AND json_extract(cb.payload,'$.job_id')=j.id
 WHERE r.kind='import_plan'`

const transferHistoryAuxiliaryFiles = `), auxiliary_files AS (
 SELECT r.id AS plan_id, json_remove(r.payload,'$.files','$.media.cast','$.media.recommendations','$.media.seasons') AS plan,
 json_extract(cb.payload,'$.file') AS file,cb.updated_at AS updated_at,
 j.id AS job_id,j.state AS job_state,j.error_message AS job_error,j.created_at AS executed_at,j.execution_key,
 MIN(j.execution_key) OVER (PARTITION BY r.id,json_extract(cb.payload,'$.file.id')) AS first_execution_key,
 ROW_NUMBER() OVER (PARTITION BY r.id,json_extract(cb.payload,'$.file.id') ORDER BY j.execution_key DESC) AS file_rank,
 json_extract(cb.payload,'$.file.source') AS resource_source,
 CASE WHEN json_extract(cb.payload,'$.file.status')='imported' THEN 'success' ELSE 'failed' END AS status
 FROM records r,json_each(r.payload,'$.files') f
 JOIN import_jobs j ON ` + transferHistoryJobMatch + `
 JOIN records cb ON cb.kind='original_transfer_callback' AND json_extract(cb.payload,'$.plan_id')=r.id
 AND json_extract(cb.payload,'$.parent_file_id')=json_extract(f.value,'$.id')
 AND json_extract(cb.payload,'$.file.id')!=json_extract(f.value,'$.id') AND json_extract(cb.payload,'$.job_id')=j.id
 WHERE r.kind='import_plan'`

const transferHistoryAttempts = `), attempts AS (SELECT * FROM executed_files WHERE file_rank=1 UNION ALL SELECT * FROM auxiliary_files WHERE file_rank=1) `

const transferHistoryBaseCTE = transferHistoryJobsCTE + transferHistoryExecutedFiles + transferHistoryAuxiliaryFiles + transferHistoryAttempts

const transferHistoryRanked = `, ranked_history AS (
 SELECT *,
 FIRST_VALUE(json_extract(file,'$.id')) OVER (PARTITION BY resource_source ORDER BY first_execution_key,json_extract(file,'$.id')) AS history_id,
 FIRST_VALUE(json_extract(plan,'$.created_at')) OVER (PARTITION BY resource_source ORDER BY first_execution_key,json_extract(file,'$.id')) AS history_created_at,
 ROW_NUMBER() OVER (PARTITION BY resource_source ORDER BY execution_key DESC,json_extract(file,'$.id')) AS history_rank
 FROM attempts
), history AS (
 SELECT * FROM ranked_history h WHERE history_rank=1 AND NOT EXISTS (
 SELECT 1 FROM records hidden JOIN attempts old ON hidden.id=json_extract(old.file,'$.id')
 WHERE hidden.kind='transfer_history_hidden' AND old.resource_source=h.resource_source
 AND (CASE WHEN COALESCE(json_extract(hidden.payload,'$.execution_key'),'')!=''
 THEN json_extract(hidden.payload,'$.execution_key')>=h.execution_key
 ELSE julianday(hidden.updated_at)>=julianday(h.executed_at) END)
 )
) `

const transferHistoryCTE = transferHistoryBaseCTE + transferHistoryRanked

const transferHistoryColumns = `plan,file,job_id,job_state,job_error,updated_at,status,history_id,history_created_at,resource_source,execution_key`

// Restrict both kinds of attempt before ranking. The queue worker already
// knows the source and only needs its current history, including older retries.
const transferHistoryBySource = transferHistoryJobsCTE +
	transferHistoryExecutedFiles + ` AND ` + transferHistoryPlanSource + `=?1` +
	transferHistoryAuxiliaryFiles + ` AND json_extract(cb.payload,'$.file.source')=?1` +
	transferHistoryAttempts + transferHistoryRanked + `SELECT ` + transferHistoryColumns + ` FROM history`

const transferHistoryBySourceFile = transferHistoryBySource + ` WHERE EXISTS (
 SELECT 1 FROM attempts WHERE json_extract(file,'$.id')=?2
)`

func (item transferHistoryItem) executionFileID() string {
	if item.FileID != "" {
		return item.FileID
	}
	return item.ID
}

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
			var plan ImportPlan
			if e = a.Store.Get(r.Context(), "import_plan", item.PlanID, &plan); e != nil {
				return nil, e
			}
			_, _, _, e := a.historyPlannedFile(r.Context(), plan, item)
			if e != nil {
				return nil, e
			}
			return a.retryOriginalTransferHistory(r.Context(), item, plan)
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
	rows, e := a.Store.DB.QueryContext(ctx, transferHistoryCTE+`SELECT `+transferHistoryColumns+` FROM history`+where+` ORDER BY updated_at DESC,execution_key DESC,history_id LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
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
	var historyID, createdAt, resourceSource string
	var executionKey string
	if e := row.Scan(&planRaw, &fileRaw, &jobID, &jobState, &jobError, &updated, &status, &historyID, &createdAt, &resourceSource, &executionKey); e != nil {
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
	if mode == "" && !file.OriginalTransferHistory {
		mode = plan.Mode
	}
	message := file.Reason
	if message == "" && status == "failed" {
		message = jobError
	}
	if message == "" && status == "failed" {
		message = "整理未完成，请查看关联任务"
	}
	return transferHistoryItem{ID: historyID, FileID: file.ID, ResourceSource: resourceSource, ExecutionKey: executionKey, PlanID: plan.ID, WorkflowID: plan.WorkflowID, JobID: jobID, JobState: jobState, Media: plan.Media, Season: file.Season, Episodes: file.Episodes, Part: file.Part, Source: file.Source, Target: file.Target, Mode: mode, Size: file.Size, Status: status, FileStatus: file.Status, Error: message, CreatedAt: createdAt, UpdatedAt: updated, CanRetry: status == "failed" && jobState != "running" && jobState != "queued" && jobState != "retry_wait" && jobState != "waiting_paused" && file.Status != "blocked"}, nil
}
func (a *App) transferHistoryItem(ctx context.Context, id string) (transferHistoryItem, error) {
	item, e := scanTransferHistory(a.Store.DB.QueryRowContext(ctx, transferHistoryCTE+`SELECT `+transferHistoryColumns+` FROM history WHERE history_id=? OR resource_source=(SELECT resource_source FROM attempts WHERE json_extract(file,'$.id')=? LIMIT 1) LIMIT 1`, id, id))
	if e != nil {
		return item, e
	}
	a.transferHistoryCapabilities(ctx, &item)
	return item, nil
}

func (a *App) transferHistoryPlanFile(ctx context.Context, planID, fileID string) (transferHistoryItem, error) {
	var source string
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT `+transferHistoryPlanSource+`
		FROM records r,json_each(r.payload,'$.files') f
		LEFT JOIN records meta ON meta.kind='import_meta' AND meta.id=r.id
		WHERE r.kind='import_plan' AND r.id=? AND json_extract(f.value,'$.id')=?`, planID, fileID).Scan(&source); err != nil {
		return transferHistoryItem{}, err
	}
	return scanTransferHistory(a.Store.DB.QueryRowContext(ctx, transferHistoryBySourceFile, source, fileID))
}

// Queue controls address an exact execution, never a stable-history redirect.
func (a *App) transferHistoryExecution(ctx context.Context, fileID string) (transferHistoryItem, error) {
	return scanTransferHistory(a.Store.DB.QueryRowContext(ctx, transferHistoryBaseCTE+`SELECT plan,file,job_id,job_state,job_error,updated_at,status,json_extract(file,'$.id'),json_extract(plan,'$.created_at'),resource_source,execution_key FROM attempts WHERE json_extract(file,'$.id')=? LIMIT 1`, fileID))
}
func (a *App) transferHistoryCapabilities(ctx context.Context, item *transferHistoryItem) {
	_, e := snapshotTransferPath(item.Source)
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
			if file.ID == item.executionFileID() {
				current, e := snapshotTransferPath(item.Source)
				if e != nil || current != planSnapshot(file) {
					item.CanRetry = false
				}
				break
			}
		}
	}
}
