package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

func (a *App) enqueueTx(ctx context.Context, tx *sql.Tx, kind string, payload any, wf string) (Job, error) {
	return a.enqueueAcceptedTx(ctx, tx, kind, payload, wf, nil)
}

func (a *App) enqueueAcceptedTx(ctx context.Context, tx *sql.Tx, kind string, payload any, wf string, accepted *Job) (Job, error) {
	phase := a.performanceStages(ctx, "job_enqueue", "identity")
	defer phase("")
	if kind == "import" {
		if err := guardOrganizePreparation(ctx, tx); err != nil {
			return Job{}, err
		}
	}
	b, e := json.Marshal(payload)
	if e != nil {
		return Job{}, e
	}
	// Preserve plugin DTO numbers while adding host trace fields. Decoding the
	// whole envelope into interface values would round integers through float64.
	var envelope map[string]json.RawMessage
	if e = json.Unmarshal(b, &envelope); e != nil || envelope == nil {
		return Job{}, bad("INVALID_JOB", "任务参数须为对象")
	}
	if trace, ok := ctx.Value(jobTraceKey{}).(jobTrace); ok {
		if trace.Job != "" {
			envelope["_parent_job_id"], _ = json.Marshal(trace.Job)
		}
		if trace.Request != "" {
			envelope["_request_id"], _ = json.Marshal(trace.Request)
		}
	}
	b, e = json.Marshal(envelope)
	if e != nil {
		return Job{}, e
	}
	var identity struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &identity)
	if accepted == nil && identity.ID != "" && kind != "import" && kind != "organize_prepare" && a.subjectBusy(kind, identity.ID) {
		// The pending set lives in memory, so this refusal no longer costs a
		// query inside the caller's transaction.
		return Job{}, bad("JOB_EXISTS", "已有该对象的任务正在等待或执行")
	}
	if identity.ID != "" && (kind == "import" || kind == "organize_prepare") {
		var found string
		active := "state IN " + activeOrganizeStates
		if kind == "organize_prepare" {
			active = `(` + active + ` OR EXISTS(SELECT 1 FROM jobs child WHERE child.kind IN ('organize_file','import')
				AND child.state IN ` + activeOrganizeStates + ` AND json_extract(child.payload,'$._parent_job_id')=jobs.id))`
		}
		query := `SELECT id FROM jobs WHERE kind=? AND json_extract(payload,'$.id')=? AND ` + active
		args := []any{kind, identity.ID}
		if kind == "import" {
			// Disjoint files from one downloaded pack are independent tasks.
			query += ` AND (COALESCE(json_array_length(payload,'$.file_ids'),0)=0 OR COALESCE(json_array_length(?,'$.file_ids'),0)=0 OR EXISTS(SELECT 1 FROM json_each(payload,'$.file_ids') old JOIN json_each(?,'$.file_ids') new ON old.value=new.value))`
			args = append(args, string(b), string(b))
		}
		e = tx.QueryRowContext(ctx, query+` LIMIT 1`, args...).Scan(&found)
		if e == nil {
			if preparation, ok := payload.(organizePreparationPayload); ok && preparation.Request.Interactive {
				return Job{ID: found, Kind: kind}, nil
			}
			return Job{}, bad("JOB_EXISTS", "已有该对象的任务正在等待或执行")
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return Job{}, e
		}
	}
	phase("job_insert")
	j := Job{WorkflowID: wf, Kind: kind, State: "queued", Payload: b, RunAt: time.Now().Unix(), CreatedAt: now(), UpdatedAt: now()}
	if accepted != nil {
		j.ID, j.CreatedAt, j.RunAt = accepted.ID, accepted.CreatedAt, accepted.RunAt
	} else {
		j.ID = id("job_")
	}
	readJobTrace(&j)
	_, e = tx.ExecContext(ctx, `INSERT INTO jobs(id,workflow_id,kind,state,payload,run_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, j.ID, wf, kind, j.State, b, j.RunAt, j.CreatedAt, j.UpdatedAt)
	if e == nil {
		phase("event_insert")
		_, e = tx.ExecContext(ctx, `INSERT INTO events(time,workflow_id,job_id,level,module,event,message) VALUES(?,?,?,?,?,?,?)`, now(), wf, j.ID, "INFO", kind, "queued", "任务已加入队列")
	}
	if e == nil {
		phase("queue_group")
		e = a.registerOrganizeQueueTx(ctx, tx, j)
	}
	if e == nil {
		if kind == "organize_prepare" && a.organizeInbox != nil {
			a.organizeInbox.subjects.add(identity.ID)
		}
		// Registered now so a waiter wakes immediately after commit; the claim
		// confirms against the row, so an aborted transaction is harmless.
		a.indexEnqueue(j)
	}
	return j, e
}
func (a *App) enqueue(ctx context.Context, kind string, payload any, wf string) (Job, error) {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return Job{}, e
	}
	defer tx.Rollback()
	j, e := a.enqueueTx(ctx, tx, kind, payload, wf)
	if e != nil {
		return Job{}, e
	}
	if e = tx.Commit(); e != nil {
		return Job{}, e
	}
	a.wakeQueuedJob(j.Kind)
	return j, nil
}

// Signal after commit so an idle worker can see the new row when it wakes.
func (a *App) wakeQueuedJob(kind string) {
	wake := a.jobWake
	if kind == "import" {
		wake = a.importWake
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	j, e := scanJobRow(row)
	if e == nil {
		readJobTrace(&j)
	}
	return j, e
}

func scanJobRow(row interface{ Scan(...any) error }, extra ...any) (Job, error) {
	var j Job
	columns := []any{&j.ID, &j.WorkflowID, &j.Kind, &j.State, &j.Payload, &j.Attempt, &j.Generation, &j.RunAt, &j.ErrorCode, &j.ErrorMessage, &j.CreatedAt, &j.UpdatedAt}
	e := row.Scan(append(columns, extra...)...)
	return j, e
}

func readJobTrace(j *Job) {
	var v struct {
		Parent  string `json:"_parent_job_id"`
		Request string `json:"_request_id"`
	}
	if json.Unmarshal(j.Payload, &v) == nil {
		j.ParentJobID, j.RequestID = v.Parent, v.Request
	}
}

const jobColumns = `id,workflow_id,kind,state,payload,attempt,generation,run_at,error_code,error_message,created_at,updated_at`

func privateOrdinarySearchDownloadJob(j Job) bool {
	var payload struct {
		Parent json.RawMessage `json:"_ordinary_search_attempt"`
	}
	return j.Kind == "download" && json.Unmarshal(j.Payload, &payload) == nil &&
		len(payload.Parent) != 0 && string(payload.Parent) != "null" && string(payload.Parent) != `""`
}

func (a *App) jobs(ctx context.Context, limit int) ([]Job, error) {
	return a.queryJobs(ctx, `SELECT `+jobColumns+` FROM jobs ORDER BY created_at DESC LIMIT ?`, limit)
}

func (a *App) queryJobs(ctx context.Context, query string, args ...any) ([]Job, error) {
	rows, e := a.Store.reader().QueryContext(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, e := scanJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

const importWorkerCount = 2

type jobLane uint8

const (
	allJobs jobLane = iota
	generalJobs
	subscriptionJobs
	importJobs
)

func (a *App) claim(ctx context.Context) (Job, error) {
	return a.claimJob(ctx, allJobs)
}

func (a *App) claimJob(ctx context.Context, lane jobLane) (Job, error) {
	if lane == importJobs {
		return a.claimImportJob(ctx)
	}
	// Dispatch reads the in-memory index; the row is only confirmed, never
	// searched. That removes the per-tick claim query entirely.
	if j, err := a.claimFromIndex(ctx, lane); err == nil {
		return j, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Job{}, err
	}
	// Fallback: the index could be empty because loading failed or because a
	// row was written by another path. One table lookup re-admits whatever it
	// finds, so a damaged index degrades to the old behaviour instead of a
	// queue that silently stops moving.
	j, err := a.claimJobByQuery(ctx, lane)
	if err == nil {
		a.indexRefresh(j)
	}
	return j, err
}
func (a *App) claimJobByQuery(ctx context.Context, lane jobLane) (Job, error) {
	if lane == importJobs {
		return a.claimImportJob(ctx)
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return Job{}, e
	}
	defer tx.Rollback()
	// One pending state. Delayed work waits in "queued" behind run_at instead of
	// a separate "retry_wait", and a paused automation simply stops claiming
	// instead of parking rows in "waiting_paused".
	const due = `SELECT ` + jobColumns + ` FROM jobs WHERE state='queued'
		AND kind NOT IN ('lark_download','subscription_download')
		AND (kind!='download' OR COALESCE(json_extract(payload,'$._ordinary_search_attempt'),'')='')
		AND run_at<=?
		AND (kind='asset_check' OR ?)`
	claimAt := time.Now()
	runnable := 0
	if !a.paused() {
		runnable = 1
	}
	find := func(filter string) (Job, error) {
		return scanJob(tx.QueryRowContext(ctx, due+filter+` ORDER BY run_at,created_at LIMIT 1`, claimAt.Unix(), runnable))
	}
	var j Job
	switch lane {
	case subscriptionJobs:
		j, e = find(` AND kind='subscription'`)
	case generalJobs:
		j, e = find(` AND kind NOT IN ('subscription','import')`)
		if e != nil {
			break
		}
		// Subscription downloads keep the existing file safety checks, but
		// can submit ahead of bulk organizing. Preserve FIFO for other work.
		// Look up the intent so jobs already queued by older builds also qualify.
		switch j.Kind {
		case "organize_prepare", "organize_file", "reorganize_cleanup", "quality_cleanup", "scrape", "fnos_sync":
			download, err := find(` AND kind='download' AND EXISTS (
				SELECT 1 FROM downloadhistory AS intent WHERE intent.record_kind='intent'
				AND intent.id=json_extract(jobs.payload,'$.id')
				AND COALESCE(json_extract(intent.payload,'$.subscription_id'),'')!='')`)
			if err == nil {
				j = download
			} else if !errors.Is(err, sql.ErrNoRows) {
				return Job{}, err
			}
		}
	default:
		j, e = find("")
	}
	if e != nil {
		return Job{}, e
	}
	j.State = "running"
	j.Attempt++
	j.Generation++
	j.UpdatedAt = now()
	_, e = tx.ExecContext(ctx, `UPDATE jobs SET state=?,attempt=?,generation=?,updated_at=? WHERE id=?`, j.State, j.Attempt, j.Generation, j.UpdatedAt, j.ID)
	if e != nil {
		return Job{}, e
	}
	return j, tx.Commit()
}
func (a *App) worker(ctx context.Context) {
	a.jobWorker(ctx, allJobs)
}

// jobWorker claims until the queue is empty, then sleeps for exactly as long as
// the earliest queued row needs, or until something wakes it. There is no fixed
// poll: an idle queue costs one indexed MIN(run_at) query per drained batch
// instead of a query every tick.
func (a *App) jobWorker(ctx context.Context, lane jobLane) {
	wake := (<-chan struct{})(a.jobWake)
	if lane == importJobs {
		wake = a.importWake
	} else if lane == subscriptionJobs {
		wake = nil // Leave general job wakeups for the workers that can claim them.
	}
	for {
		if ctx.Err() != nil {
			return
		}
		j, e := a.claimJob(ctx, lane)
		if e == nil {
			a.runClaimedJob(ctx, j)
			continue
		}
		if ctx.Err() != nil {
			return
		}
		delay := a.nextQueueDelay(ctx)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-wake:
			timer.Stop()
		}
	}
}

// nextQueueDelay reports how long until the earliest queued row is due, bounded
// so a clock change or a row inserted by another path is still picked up.
func (a *App) nextQueueDelay(ctx context.Context) time.Duration {
	var runAt sql.NullInt64
	err := a.Store.DB.QueryRowContext(ctx,
		`SELECT MIN(run_at) FROM jobs WHERE state='queued'`).Scan(&runAt)
	if err != nil || !runAt.Valid {
		return 2 * time.Second
	}
	wait := time.Until(time.Unix(runAt.Int64, 0))
	if wait < 50*time.Millisecond {
		return 50 * time.Millisecond
	}
	if wait > 30*time.Second {
		return 30 * time.Second
	}
	return wait
}

func (a *App) runClaimedJob(ctx context.Context, j Job) {
	if j.Kind == "import" || j.Kind == "organize_file" {
		defer a.organizeLive.copies.Delete(j.ID)
		a.organizeLive.changed(j.ID)
		defer a.organizeLive.changed(j.ID)
	}
	if j.importRun != nil {
		defer j.importRun.release()
	}
	a.event(ctx, j, "INFO", j.Kind, "started", "开始执行任务")
	e := a.safeRun(a.traceContext(ctx, j.WorkflowID, j.ID, j.RequestID), j)
	if ctx.Err() != nil {
		return
	}
	var currentState string
	var currentGeneration int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT state,generation FROM jobs WHERE id=?`, j.ID).Scan(&currentState, &currentGeneration); err != nil || currentState != "running" || currentGeneration != j.Generation {
		return
	}
	state, code, message := "succeeded", "", ""
	runAt := time.Now().Unix()
	if e != nil {
		message = a.clean(e.Error())
		state = "failed"
		code = "TASK_FAILED"
		var api *APIError
		if errors.As(e, &api) {
			code = api.Code
		}
		// Two cases ask for their own delay; everything else fails outright and
		// waits for the next scheduled cycle, which is what a periodic schedule
		// already provides. No attempt counter, no exponential backoff.
		if code == "DIRECTORY_HISTORY_DELETED" || code == "DIRECTORY_SOURCE_GONE" || code == "ORGANIZE_FILE_SKIPPED" {
			state = "cancelled"
		} else if code == "PLUGIN_REFRESH_PENDING" {
			state = "queued"
			runAt = time.Now().Add(2 * time.Second).Unix()
		} else if code == "DOWNLOADER_DISABLED" {
			state = "queued"
			runAt = time.Now().Add(time.Minute).Unix()
		} else if code == "AUTOMATION_PAUSED" {
			state = "queued"
		}
	}
	changed, updateErr := a.finishJob(ctx, j, state, code, message, runAt)
	if updateErr != nil {
		a.log("ERROR", "jobs", "state_persist_failed", "任务状态无法保存，自动化已暂停，请检查配置盘", j.WorkflowID, j.ID)
		_ = a.setControl(ctx, true)
		return
	}
	if changed && state == "failed" {
		a.notifyFailed(ctx, j, message)
	}
	if changed {
		if code == "PLUGIN_REFRESH_PENDING" {
			a.log("DEBUG", j.Kind, code, message, j.WorkflowID, j.ID)
		} else if state == "cancelled" {
			a.log("INFO", j.Kind, code, message, j.WorkflowID, j.ID)
		} else if e != nil {
			a.log("ERROR", j.Kind, code, message, j.WorkflowID, j.ID)
		} else {
			a.log("INFO", j.Kind, "completed", "任务完成", j.WorkflowID, j.ID)
		}
	}
}

func (a *App) safeRun(ctx context.Context, j Job) (err error) {
	defer func() {
		if recover() != nil {
			err = bad("INTERNAL_TASK_ERROR", "任务出现内部错误，已保留状态供重试")
			a.writeLog(map[string]any{"id": id("log_"), "time": now(), "level": "ERROR", "module": "jobs", "event": "panic_recovered", "message": err.Error(), "stack": a.clean(string(debug.Stack())), "workflow_id": j.WorkflowID, "job_id": j.ID, "error_code": "INTERNAL_TASK_ERROR"})
		}
	}()
	var p struct {
		ID       string   `json:"id"`
		Revision int      `json:"revision"`
		RootID   string   `json:"root_id"`
		FileIDs  []string `json:"file_ids"`
	}
	if e := json.Unmarshal(j.Payload, &p); e != nil {
		return bad("INVALID_JOB", "任务参数无效")
	}
	switch j.Kind {
	case "lark_message":
		return bad("PLUGIN_ADAPTER_UNAVAILABLE", "历史插件消息任务尚未适配新的标准模块")
	case "site_refresh":
		return a.runSiteRefresh(ctx, j)
	case "download_file_deleted":
		return a.runDownloadFileDeleted(ctx, j, p.ID)
	case "native_download":
		return a.runNativeDownload(ctx, j, p.ID)
	case "download":
		return a.runDownload(ctx, j, p.ID)
	case "import":
		return a.runImport(ctx, j, p.ID, p.FileIDs)
	case "organize_prepare":
		return a.runOrganizePreparation(ctx, j)
	case "organize_file":
		return a.runOrganizeFile(ctx, j)
	case "quality_cleanup":
		return a.runQualityCleanup(ctx, j, p.ID)
	case "reorganize_cleanup":
		return a.runReorganizeCleanup(ctx, j, p.ID)
	case "scrape":
		return a.runScrape(ctx, j, p.ID)
	case "fnos_sync":
		return a.runFNOSSync(ctx, j, p.ID)
	case "subscription_add":
		return a.runUISubscriptionAdd(ctx, j, p.ID)
	case "subscription_acquisition_receipt":
		return a.runAcquisitionReceipt(ctx, j, p.ID)
	case "subscription_stats":
		return a.runSubscriptionStats(ctx, j, p.ID)
	case "subscription_completion_prepare":
		return a.prepareSubscriptionCompletion(ctx, j, p.ID, p.Revision)
	case "subscription_completion":
		return a.runSubscriptionCompletion(ctx, j, p.ID)
	case "subscription":
		return a.checkSubscription(ctx, j, p.ID, p.Revision)
	case "library_scan":
		return a.scanLibrary(ctx, j, p.RootID)
	case "asset_check":
		return a.checkAsset(ctx, p.ID)
	}
	return bad("INVALID_JOB", "未知任务类型")
}
func (a *App) jobCancelled(ctx context.Context, j Job) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if a.paused() {
		return &APIError{"AUTOMATION_PAUSED", "自动化已暂停，将在恢复后继续", 409}
	}
	var state string
	var gen int
	e := a.Store.DB.QueryRowContext(ctx, `SELECT state,generation FROM jobs WHERE id=?`, j.ID).Scan(&state, &gen)
	if e != nil {
		return e
	}
	if state != "running" || gen != j.Generation {
		return bad("JOB_CANCELLED", "任务已取消")
	}
	return nil
}

func organizeJobState(j Job) map[string]string {
	return map[string]string{"id": j.ID, "kind": j.Kind, "state": j.State, "error_code": j.ErrorCode, "error_message": j.ErrorMessage}
}

func (a *App) jobAPI(w http.ResponseWriter, r *http.Request, p string) (any, error) {
	parts := strings.Split(p, "/")
	if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost {
		defer a.organizeLive.changed(parts[0])
	}
	if len(parts) > 2 || parts[0] == "" {
		return nil, bad("INVALID_JOB", "任务ID无效")
	}
	ctx := r.Context()
	if len(parts) == 2 && r.Method == http.MethodPost {
		var changed bool
		var err error
		switch parts[1] {
		case "cancel":
			changed, err = a.cancelPendingOrganizeJob(ctx, parts[0])
		case "retry":
			changed, err = a.retryPendingOrganizeJob(ctx, parts[0])
		}
		if err != nil || changed {
			return map[string]bool{"accepted": changed}, err
		}
	}
	// In that order, a handoff cannot leave the job missing from both reads.
	pending, err := a.pendingOrganizeJob(ctx, parts[0])
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	j, e := scanJob(a.Store.reader().QueryRowContext(r.Context(), `SELECT `+jobColumns+` FROM jobs WHERE id=?`, parts[0]))
	if errors.Is(e, sql.ErrNoRows) && pending.ID != "" {
		if len(parts) == 1 && r.Method == http.MethodGet {
			if r.URL.Query().Get("view") == "organize" {
				return map[string]any{"job": organizeJobState(pending)}, nil
			}
			settings := a.settingsSnapshot()
			view, err := summarizeJob(pending, jobSummaryInput{}, *settings, settingsRedactions(*settings))
			if err != nil {
				return nil, err
			}
			return map[string]any{"job": view, "events": []Event{}, "decisions": []json.RawMessage{}}, nil
		}
		if len(parts) == 2 && r.Method == http.MethodPost && parts[1] == "cancel" && pending.State == "cancelled" {
			return nil, bad("JOB_TERMINAL", "该任务已结束")
		}
		if len(parts) == 2 && r.Method == http.MethodPost && parts[1] == "retry" && pending.State == "queued" {
			return nil, bad("JOB_NOT_RETRYABLE", "只能重试失败、取消或待核对的任务")
		}
	}
	if e != nil {
		return nil, e
	}
	if len(parts) == 1 && r.Method == "GET" {
		if r.URL.Query().Get("view") == "organize" {
			result := map[string]any{"job": organizeJobState(j)}
			return a.organizeJobResult(r.Context(), j, result)
		}

		rows, e := a.Store.reader().QueryContext(r.Context(), `SELECT seq,time,workflow_id,job_id,level,module,event,message FROM events WHERE job_id=? ORDER BY seq LIMIT 1000`, j.ID)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		events := []Event{}
		for rows.Next() {
			var v Event
			if e = rows.Scan(&v.Seq, &v.Time, &v.WorkflowID, &v.JobID, &v.Level, &v.Module, &v.Event, &v.Message); e != nil {
				return nil, e
			}
			events = append(events, v)
		}
		rowErr := rows.Err()
		rows.Close()
		if rowErr != nil {
			return nil, rowErr
		}
		decisions, e := a.decisionRecords(r.Context(), j.WorkflowID)
		if e != nil {
			return nil, e
		}
		view, e := a.jobView(r.Context(), j)
		if e != nil {
			return nil, e
		}
		result := map[string]any{"job": view, "events": events, "decisions": decisions}
		return a.organizeJobResult(r.Context(), j, result)
	}
	if len(parts) == 2 && r.Method == "POST" {
		var result sql.Result
		switch parts[1] {
		case "retry":
			if privateOrdinarySearchDownloadJob(j) {
				return nil, bad("JOB_NOT_RETRYABLE", "该下载任务仅由原订阅搜索处理")
			}
			if j.State != "failed" && j.State != "cancelled" && j.State != "needs_review" {
				return nil, bad("JOB_NOT_RETRYABLE", "只能重试失败、取消或待核对的任务")
			}
			result, e = a.retryOriginalScrapeJob(r.Context(), j)
		case "cancel":
			if j.State == "succeeded" || j.State == "cancelled" || j.State == "failed" {
				return nil, bad("JOB_TERMINAL", "该任务已结束")
			}
			result, e = a.Store.DB.ExecContext(r.Context(), `UPDATE jobs SET state='cancelled',generation=generation+1,updated_at=? WHERE id=? AND generation=? AND state=?`, now(), j.ID, j.Generation, j.State)
		default:
			return nil, bad("INVALID_ACTION", "未知任务操作")
		}
		if e != nil {
			return nil, e
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return nil, bad("JOB_CHANGED", "任务状态已变化，请刷新后操作")
		}
		if (j.Kind == "import" || j.Kind == "organize_file" || j.Kind == "reorganize_cleanup" || j.Kind == "quality_cleanup") && parts[1] == "cancel" {
			if e = a.closeOriginalScrapeBatch(r.Context(), originalScrapeBatchID(j), false); e != nil {
				return nil, e
			}
		}
		a.event(r.Context(), j, "INFO", "jobs", parts[1], "管理员更新了任务状态")
		return map[string]bool{"accepted": true}, nil
	}
	return nil, &APIError{"NOT_FOUND", "接口不存在", 404}
}
func (a *App) scheduler(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	lastPoll := time.Time{}
	lastMaterialCleanup := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if time.Since(lastMaterialCleanup) >= time.Hour {
			if e := a.cleanupMaterialOrphans(ctx); e != nil {
				a.log("WARN", "downloads", "material_cleanup_failed", "未确认的种子材料清理失败", "", "")
			}
			lastMaterialCleanup = time.Now()
		}
		if time.Since(lastPoll) >= 30*time.Second {
			if e := a.pollDownloads(ctx); e != nil {
				a.log("WARN", "downloads", "poll_failed", e.Error(), "", "")
			}
			if !a.paused() {
				if e := a.pollDirectoryFiles(ctx); e != nil {
					a.log("WARN", "directories", "poll_failed", e.Error(), "", "")
				}
			}
			lastPoll = time.Now()
		}
		if a.paused() {
			continue
		}
		if err := a.scheduleSiteDataRefresh(ctx, time.Now()); err != nil {
			a.log("ERROR", "site", "refresh_schedule_failed", "站点数据刷新调度失败，已保留原调度状态", "", "")
		}
	}
}

func (a *App) subscriptionScheduler(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		a.scheduleSubscriptions(ctx, time.Now())
	}
}

// scheduleSubscriptions runs one scheduler tick; the supplied clock also makes
// the exact due boundary testable without waiting for the background ticker.
func (a *App) scheduleSubscriptions(ctx context.Context, at time.Time) {
	if a.paused() {
		return
	}
	due, err := a.dueSubscriptionChecks(ctx, at)
	if err != nil {
		return
	}
	for _, check := range due {
		_, _ = a.enqueue(ctx, "subscription", check, id("wf_"))
	}
}

type subscriptionPollJob struct {
	ID       string `json:"id"`
	Revision int    `json:"revision"`
}

func (a *App) dueSubscriptionChecks(ctx context.Context, at time.Time) ([]subscriptionPollJob, error) {
	rows, err := a.Store.reader().QueryContext(ctx, `SELECT payload FROM subscribe WHERE record_kind='subscription' ORDER BY updated_at DESC,uid LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var due []subscriptionPollJob
	var payload sql.RawBytes
	for rows.Next() {
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		// Borrow this row only until Next. A normal idle tick needs none of
		// the episode maps, media or policy data in the full subscription.
		var clock struct {
			State      string `json:"state"`
			NextCheck  string `json:"next_check"`
			LastReason string `json:"last_reason"`
			Schedule   struct {
				Mode string `json:"mode"`
			} `json:"schedule"`
		}
		if json.Unmarshal(payload, &clock) != nil || clock.State != "active" || clock.Schedule.Mode == "manual" {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, clock.NextCheck)
		// Earlier versions could store a delay caused by the retired daily
		// site budget. Let that check run and calculate its normal schedule.
		oldBudgetDelay := clock.LastReason == "站点调用预算不足，已按最早恢复时间延期检查" ||
			strings.HasSuffix(clock.LastReason, "；部分站点预算不足，检查将按恢复时间延期")
		if !oldBudgetDelay && err == nil && t.After(at) {
			continue
		}
		// Preserve the full record validation for candidates. A malformed
		// episode map, for example, must not result in a queued check.
		var s Subscription
		if json.Unmarshal(payload, &s) == nil {
			due = append(due, subscriptionPollJob{ID: s.ID, Revision: s.Revision})
		}
	}
	return due, rows.Err()
}

func jobErr(code string, e error) error { return bad(code, fmt.Sprint(e)) }
