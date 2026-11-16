package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"light-media/internal/catalog"
)

type transferHistoryItem struct {
	mpRecord             bool          `json:"-"`
	ID                   string        `json:"id"`
	FileID               string        `json:"file_id"`
	ResourceSource       string        `json:"-"`
	ExecutionKey         string        `json:"-"`
	PlanID               string        `json:"plan_id"`
	WorkflowID           string        `json:"workflow_id"`
	JobID                string        `json:"job_id"`
	JobState             string        `json:"job_state"`
	Media                catalog.Media `json:"media"`
	Category             string        `json:"category,omitempty"`
	EpisodeGroup         string        `json:"episode_group,omitempty"`
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

// Current histories carry their own compact snapshots. The execution join is
// retained only for migration and exact execution/queue lookups.
const transferHistoryStoredColumns = `h.plan_json,h.file_json,h.job_id,h.job_state,h.job_error,h.updated_at,h.status,h.history_id,h.history_created_at,h.resource_source,h.execution_key`
const transferHistoryStoredSelect = `SELECT ` + transferHistoryStoredColumns + ` FROM history h`
const transferHistorySelect = `SELECT ` + transferHistoryColumns + ` FROM history h` + transferHistoryHydrate

const transferHistoryOrder = ` ORDER BY h.updated_at DESC,h.execution_key DESC,h.history_id`

// MP reuses the destination only after a successful move. All history entry
// points must resolve the same source, including capability checks and queues.
func (item transferHistoryItem) organizeSource() string {
	if item.Status == "success" && item.Mode == "move" && item.Target != "" {
		return item.Target
	}
	return item.Source
}

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
	if status == "pending" {
		return map[string]any{"items": []transferHistoryItem{}, "total": 0, "page": page, "page_size": size}, nil
	}
	if status != "pending" {
		mpItems, mpTotal, mpErr := a.transferHistoryMPFiltered(ctx, page, size, status, query, pattern)
		if mpErr != nil {
			return nil, mpErr
		}
		if q.Get("capabilities") == "deferred" {
			for i := range mpItems {
				mpItems[i].CanRetry, mpItems[i].CanRecognize, mpItems[i].CapabilitiesDeferred = false, false, true
			}
		} else {
			for i := range mpItems {
				a.transferHistoryCapabilities(ctx, &mpItems[i])
			}
		}
		return map[string]any{"items": mpItems, "total": mpTotal, "page": page, "page_size": size}, nil
	}
	return map[string]any{"items": []transferHistoryItem{}, "total": 0, "page": page, "page_size": size}, nil
}

func (a *App) transferHistoryResolveID(ctx context.Context, id string) (transferHistoryItem, error) {
	item, found, err := a.transferHistoryMPByID(ctx, id)
	if err != nil {
		return transferHistoryItem{}, err
	}
	if found {
		return item, nil
	}
	item, err = a.scanTransferHistoryMP(a.Store.reader().QueryRowContext(ctx, `SELECT `+transferHistoryMPColumns+` FROM transferhistory WHERE json_extract(src_fileitem,'$.file_id')=? ORDER BY id DESC LIMIT 1`, id))
	return item, err
}

func (a *App) transferHistoryItem(ctx context.Context, id string) (transferHistoryItem, error) {
	item, err := a.transferHistoryResolveID(ctx, id)
	if err != nil {
		return transferHistoryItem{}, err
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
	items := make([]transferHistoryItem, len(ids))
	for i, id := range ids {
		item, err := a.transferHistoryResolveID(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		items[i] = item
	}
	return items, nil
}

func (a *App) transferHistoryPlanFile(ctx context.Context, planID, fileID string) (transferHistoryItem, error) {
	item, err := a.scanTransferHistoryMP(a.Store.reader().QueryRowContext(ctx, `SELECT `+transferHistoryMPColumns+` FROM transferhistory WHERE json_extract(src_fileitem,'$.plan_id')=? AND json_extract(src_fileitem,'$.file_id')=? ORDER BY id DESC LIMIT 1`, planID, fileID))
	if err == nil {
		return item, nil
	}
	return transferHistoryItem{}, err
}

// Queue controls address an exact execution, never a stable-history redirect.
func (a *App) transferHistoryExecution(ctx context.Context, fileID string) (transferHistoryItem, error) {
	item, err := a.scanTransferHistoryMP(a.Store.reader().QueryRowContext(ctx, `SELECT `+transferHistoryMPColumns+` FROM transferhistory WHERE json_extract(src_fileitem,'$.file_id')=? ORDER BY id DESC LIMIT 1`, fileID))
	return item, err
}
func (a *App) transferHistoryCapabilities(ctx context.Context, item *transferHistoryItem) {
	path := item.organizeSource()
	_, e := snapshotTransferPath(path)
	item.CanRecognize = e == nil && a.importSourceAllowed(path, true)
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

func isMPHistoryID(id string) bool {
	number, err := strconv.ParseInt(id, 10, 64)
	return err == nil && number > 0
}

func (a *App) transferHistoryReadTx(ctx context.Context) (*sql.Tx, error) {
	return a.Store.reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
}
