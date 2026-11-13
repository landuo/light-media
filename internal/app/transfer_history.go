package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"light-media/internal/catalog"
)

type transferHistoryItem struct {
	ID                   string        `json:"id"`
	FileID               string        `json:"file_id"`
	ResourceSource       string        `json:"-"`
	ExecutionKey         string        `json:"-"`
	PlanID               string        `json:"plan_id"`
	WorkflowID           string        `json:"workflow_id"`
	JobID                string        `json:"job_id"`
	JobState             string        `json:"job_state"`
	Media                catalog.Media `json:"media"`
	Season               int           `json:"season"`
	Episodes             []int         `json:"episodes"`
	Part                 string        `json:"part,omitempty"`
	Source               string        `json:"source"`
	Target               string        `json:"target"`
	Mode                 string        `json:"mode"`
	Size                 int64         `json:"size"`
	Status               string        `json:"status"`
	FileStatus           string        `json:"file_status"`
	Error                string        `json:"error"`
	CreatedAt            string        `json:"created_at"`
	UpdatedAt            string        `json:"updated_at"`
	CanRetry             bool          `json:"can_retry"`
	CanRecognize         bool          `json:"can_recognize"`
	CapabilitiesDeferred bool          `json:"capabilities_deferred,omitempty"`
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
const transferHistoryJobsSelect = `WITH import_jobs AS NOT MATERIALIZED (
 SELECT *,substr(created_at,1,19)||'.'||substr(
 CASE WHEN substr(created_at,20,1)='.' THEN substr(created_at,21,length(created_at)-21) ELSE '' END
 ||'000000000',1,9)||'/'||id AS execution_key FROM jobs WHERE kind='import'`

const transferHistoryJobsCTE = transferHistoryJobsSelect + `)`

const transferHistoryPlanSource = `COALESCE(NULLIF(json_extract(f.value,'$.history_source'),''),
 NULLIF(json_extract(meta.payload,'$.reorganize.asset.source_path'),''),
 NULLIF(json_extract(f.value,'$.source'),''),'file:'||json_extract(f.value,'$.id'))`

const transferHistoryMainFile = `COALESCE(json_extract(cb.payload,'$.file'),f.value)`

const transferHistoryExecutedFiles = `, executed_seed AS MATERIALIZED (
 SELECT r.id AS plan_id, f.fullkey AS file_json_path, cb.id AS callback_id, COALESCE(cb.updated_at,r.updated_at) AS updated_at,
 j.id AS job_id,j.state AS job_state,
 CASE WHEN EXISTS(SELECT 1 FROM records cancelled WHERE cancelled.kind='organize_file_cancel' AND cancelled.id=j.id||'/'||json_extract(f.value,'$.id')) THEN '此文件已从整理队列移除' ELSE j.error_message END AS job_error,j.created_at AS executed_at,j.execution_key,
 json_extract(f.value,'$.id') AS file_id,
 json_extract(` + transferHistoryMainFile + `,'$.id') AS execution_file_id,
 json_extract(r.payload,'$.created_at') AS plan_created_at,
 json_extract(r.payload,'$.media.title') AS title,
 json_extract(` + transferHistoryMainFile + `,'$.source') AS source_path,
 json_extract(` + transferHistoryMainFile + `,'$.target') AS target_path,
 ` + transferHistoryPlanSource + ` AS resource_source,
 CASE WHEN json_extract(cb.payload,'$.file.status')='imported' THEN 'success'
 WHEN json_extract(cb.payload,'$.file.status')='error' THEN 'failed'
 WHEN json_extract(f.value,'$.status') IN ('imported','already_imported') THEN 'success'
 WHEN EXISTS(SELECT 1 FROM records cancelled WHERE cancelled.kind='organize_file_cancel' AND cancelled.id=j.id||'/'||json_extract(f.value,'$.id')) THEN 'failed'
 WHEN j.state IN ('queued','running','retry_wait','waiting_paused') THEN 'pending'
 ELSE 'failed' END AS status
 FROM records r,json_each(r.payload,'$.files') f
 JOIN import_jobs j ON ` + transferHistoryJobMatch + `
 LEFT JOIN records meta ON meta.kind='import_meta' AND meta.id=r.id
 LEFT JOIN records cb ON cb.kind='original_transfer_callback' AND cb.id=r.id||'/'||json_extract(f.value,'$.id') AND json_extract(cb.payload,'$.job_id')=j.id
 WHERE r.kind='import_plan'`

const transferHistoryAuxiliaryFiles = `), auxiliary_seed AS MATERIALIZED (
 SELECT r.id AS plan_id, NULL AS file_json_path, cb.id AS callback_id, cb.updated_at AS updated_at,
 j.id AS job_id,j.state AS job_state,j.error_message AS job_error,j.created_at AS executed_at,j.execution_key,
 json_extract(cb.payload,'$.file.id') AS file_id,
 json_extract(cb.payload,'$.file.id') AS execution_file_id,
 json_extract(r.payload,'$.created_at') AS plan_created_at,
 json_extract(r.payload,'$.media.title') AS title,
 json_extract(cb.payload,'$.file.source') AS source_path,
 json_extract(cb.payload,'$.file.target') AS target_path,
 json_extract(cb.payload,'$.file.source') AS resource_source,
 CASE WHEN json_extract(cb.payload,'$.file.status')='imported' THEN 'success' ELSE 'failed' END AS status
 FROM records r,json_each(r.payload,'$.files') f
 JOIN import_jobs j ON ` + transferHistoryJobMatch + `
 JOIN records cb ON cb.kind='original_transfer_callback' AND json_extract(cb.payload,'$.plan_id')=(r.id||'')
 AND json_extract(cb.payload,'$.parent_file_id')=json_extract(f.value,'$.id')
 AND json_extract(cb.payload,'$.file.id')!=json_extract(f.value,'$.id') AND json_extract(cb.payload,'$.job_id')=j.id
 WHERE r.kind='import_plan'`

// Rank references and scalar fields, then read the JSON for the selected rows.
// Carrying plan/file documents through each sorter can exhaust container /tmp.
const transferHistoryRankFile = `SELECT *, MIN(execution_key) OVER (PARTITION BY plan_id,file_id) AS first_execution_key,
 ROW_NUMBER() OVER (PARTITION BY plan_id,file_id ORDER BY execution_key DESC) AS file_rank FROM `

const transferHistoryAttempts = `), executed_files AS (` + transferHistoryRankFile + `executed_seed),
 auxiliary_files AS (` + transferHistoryRankFile + `auxiliary_seed),
 attempts AS (SELECT * FROM executed_files WHERE file_rank=1 UNION ALL SELECT * FROM auxiliary_files WHERE file_rank=1) `

const transferHistoryBaseCTE = transferHistoryJobsCTE + transferHistoryExecutedFiles + transferHistoryAuxiliaryFiles + transferHistoryAttempts

// Materialize the small deletion-marker set once. Probing the main records
// B-tree for every historical attempt adds thousands of cold random reads.
const transferHistoryRanked = `, hidden_history AS MATERIALIZED (
 SELECT id,payload,updated_at FROM records WHERE kind='transfer_history_hidden'
), ranked_history AS (
 SELECT *,
 FIRST_VALUE(execution_file_id) OVER (PARTITION BY resource_source ORDER BY first_execution_key,execution_file_id) AS history_id,
 FIRST_VALUE(plan_created_at) OVER (PARTITION BY resource_source ORDER BY first_execution_key,execution_file_id) AS history_created_at,
 ROW_NUMBER() OVER (PARTITION BY resource_source ORDER BY execution_key DESC,execution_file_id) AS history_rank
 FROM attempts
), history AS (
 SELECT * FROM ranked_history h WHERE history_rank=1 AND NOT EXISTS (
 SELECT 1 FROM hidden_history hidden JOIN attempts old ON hidden.id=old.execution_file_id
 WHERE old.resource_source=h.resource_source
 AND (CASE WHEN COALESCE(json_extract(hidden.payload,'$.execution_key'),'')!=''
 THEN json_extract(hidden.payload,'$.execution_key')>=h.execution_key
 ELSE julianday(hidden.updated_at)>=julianday(h.executed_at) END)
 )
) `

const transferHistoryCTE = transferHistoryBaseCTE + transferHistoryRanked

const transferHistoryJSON = `json_remove(hp.payload,'$.files','$.media.cast','$.media.recommendations','$.media.seasons'),
 COALESCE(json_extract(hc.payload,'$.file'),json_extract(hp.payload,h.file_json_path))`

const transferHistoryColumns = transferHistoryJSON + `,h.job_id,h.job_state,h.job_error,h.updated_at,h.status,h.history_id,h.history_created_at,h.resource_source,h.execution_key`

const transferHistoryHydrate = ` JOIN records hp ON hp.kind='import_plan' AND hp.id=h.plan_id
 LEFT JOIN records hc ON hc.kind='original_transfer_callback' AND hc.id=h.callback_id`

const transferHistorySelect = `SELECT ` + transferHistoryColumns + ` FROM history h` + transferHistoryHydrate

const transferHistoryOrder = ` ORDER BY h.updated_at DESC,h.execution_key DESC,h.history_id`

// Restrict both kinds of attempt before ranking. The queue worker already
// knows the source and only needs its current history, including older retries.
const transferHistoryBySource = transferHistoryJobsCTE +
	transferHistoryExecutedFiles + ` AND ` + transferHistoryPlanSource + `=?1` +
	transferHistoryAuxiliaryFiles + ` AND json_extract(cb.payload,'$.file.source')=?1` +
	transferHistoryAttempts + transferHistoryRanked + transferHistorySelect

const transferHistoryBySourceFile = transferHistoryBySource + ` WHERE EXISTS (
 SELECT 1 FROM attempts WHERE execution_file_id=?2
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
	phase := a.performanceStages(ctx, "history_list", "read_snapshot")
	defer phase("")
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
	// Concrete predicates let SQLite seek the status/order index. An optional
	// OR predicate prevents that seek, even when the status is known.
	predicates := []string{}
	args := []any{}
	if status != "" {
		predicates = append(predicates, `status=?`)
		args = append(args, status)
	}
	if query != "" {
		predicates = append(predicates, `lower(COALESCE(title,'')||' '||COALESCE(source_path,'')||' '||COALESCE(target_path,'')) LIKE lower(?) ESCAPE '\'`)
		args = append(args, pattern)
	}
	where := ""
	if len(predicates) > 0 {
		where = " WHERE " + strings.Join(predicates, " AND ")
	}
	var total int
	tx, e := a.transferHistoryReadTx(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	phase("query_and_hydrate")
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM transfer_history_current`+where, args...).Scan(&total); e != nil {
		return nil, e
	}
	rows, e := tx.QueryContext(ctx, transferHistoryPageSQL(where), append(args, size, (page-1)*size)...)
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
	phase("commit")
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	// Keep the page order without sending the hydrated documents through
	// another SQLite sorter, even when the caller requests 1,000 rows.
	slices.SortFunc(items, func(a, b transferHistoryItem) int {
		if order := strings.Compare(b.UpdatedAt, a.UpdatedAt); order != 0 {
			return order
		}
		if order := strings.Compare(b.ExecutionKey, a.ExecutionKey); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	// The page only displays durable history. Do not walk NAS directories or
	// revalidate execution permissions for every visible row (and every poll).
	// Opt-in preserves the checked-capability contract of existing API clients;
	// single-item reads and all actual operations still perform strict checks.
	if q.Get("capabilities") == "deferred" {
		for i := range items {
			items[i].CanRetry = false
			items[i].CanRecognize = false
			items[i].CapabilitiesDeferred = true
		}
		return map[string]any{"items": items, "total": total, "page": page, "page_size": size}, nil
	}
	phase("file_capabilities")
	// Filesystem checks happen after closing the single-connection SQL cursor.
	for i := range items {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		a.transferHistoryCapabilities(ctx, &items[i])
	}
	return map[string]any{"items": items, "total": total, "page": page, "page_size": size}, nil
}

// LIMIT is applied to the scalar projection before hydrating plan JSON.
// COUNT runs separately in the same snapshot, avoiding a window-function
// sorter over every row just to return the first page. CROSS JOIN fixes the
// outer loop to the bounded page; otherwise SQLite can scan every import plan
// and probe an automatic index on the page, despite the LIMIT.
func transferHistoryPageSQL(where string) string {
	return `WITH page AS MATERIALIZED (SELECT h.* FROM transfer_history_current h` + where + transferHistoryOrder +
		` LIMIT ? OFFSET ?) SELECT ` + transferHistoryColumns + ` FROM page h` +
		strings.Replace(transferHistoryHydrate, " JOIN records hp", " CROSS JOIN records hp", 1)
}

func scanTransferHistory(row interface{ Scan(...any) error }, extra ...any) (transferHistoryItem, error) {
	var planRaw, fileRaw []byte
	var jobID, jobState, jobError, updated, status string
	var historyID, createdAt, resourceSource string
	var executionKey string
	columns := []any{&planRaw, &fileRaw, &jobID, &jobState, &jobError, &updated, &status, &historyID, &createdAt, &resourceSource, &executionKey}
	if e := row.Scan(append(columns, extra...)...); e != nil {
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
	item, e := a.readIndexedTransferHistoryItem(ctx, transferHistoryIndexedCTE+transferHistorySelect+` WHERE h.history_id=? OR h.resource_source=(SELECT resource_source FROM attempts WHERE execution_file_id=? LIMIT 1) LIMIT 1`, id, id)
	if e != nil {
		return item, e
	}
	a.transferHistoryCapabilities(ctx, &item)
	return item, nil
}

// Both stable history IDs and execution aliases appear in attempts. Resolve
// their sources together, then hydrate only the current visible selections.
func (a *App) transferHistoryItemsForOrganize(ctx context.Context, ids []string) ([]transferHistoryItem, error) {
	items, err := a.transferHistorySelections(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.ID == "" {
			return nil, sql.ErrNoRows
		}
	}
	return items, nil
}

// Preserve the requested positions so a stale selection cannot suppress its peers.
func (a *App) transferHistorySelections(ctx context.Context, ids []string) ([]transferHistoryItem, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	tx, err := a.transferHistoryReadTx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, transferHistoryIndexedCTE+`, selected AS MATERIALIZED (
 SELECT CAST(requested.key AS INTEGER) AS position,
 (SELECT resource_source FROM attempts WHERE execution_file_id=requested.value LIMIT 1) AS source
 FROM json_each(?) requested
) SELECT `+transferHistoryColumns+`,selected.position FROM history h
 JOIN selected ON selected.source=h.resource_source`+transferHistoryHydrate, string(raw))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]transferHistoryItem, len(ids))
	for rows.Next() {
		var position int
		item, err := scanTransferHistory(rows, &position)
		if err != nil {
			return nil, err
		}
		items[position] = item
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return items, tx.Commit()
}

func (a *App) transferHistoryPlanFile(ctx context.Context, planID, fileID string) (transferHistoryItem, error) {
	var source string
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT `+transferHistoryPlanSource+`
		FROM records r,json_each(r.payload,'$.files') f
		LEFT JOIN records meta ON meta.kind='import_meta' AND meta.id=r.id
		WHERE r.kind='import_plan' AND r.id=? AND json_extract(f.value,'$.id')=?`, planID, fileID).Scan(&source); err != nil {
		return transferHistoryItem{}, err
	}
	return a.readIndexedTransferHistoryItem(ctx, transferHistoryIndexedBySourceFile, source, fileID)
}

// Queue controls address an exact execution, never a stable-history redirect.
func (a *App) transferHistoryExecution(ctx context.Context, fileID string) (transferHistoryItem, error) {
	return a.readIndexedTransferHistoryItem(ctx, transferHistoryIndexedBase+`SELECT `+transferHistoryJSON+`,h.job_id,h.job_state,h.job_error,h.updated_at,h.status,h.execution_file_id,h.plan_created_at,h.resource_source,h.execution_key FROM attempts h`+transferHistoryHydrate+` WHERE h.execution_file_id=? LIMIT 1`, fileID)
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
		if e := a.Store.reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind='import' AND json_extract(payload,'$.id')=? AND state IN ('queued','running','retry_wait','waiting_paused')`, item.PlanID).Scan(&active); e != nil || active > 0 {
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
