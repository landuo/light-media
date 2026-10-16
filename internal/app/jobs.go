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

func enqueueTx(ctx context.Context, tx *sql.Tx, kind string, payload any, wf string) (Job, error) {
	b, e := json.Marshal(payload)
	if e != nil {
		return Job{}, e
	}
	var envelope map[string]any
	if e = json.Unmarshal(b, &envelope); e != nil || envelope == nil {
		return Job{}, bad("INVALID_JOB", "任务参数须为对象")
	}
	if trace, ok := ctx.Value(jobTraceKey{}).(jobTrace); ok {
		if trace.Job != "" {
			envelope["_parent_job_id"] = trace.Job
		}
		if trace.Request != "" {
			envelope["_request_id"] = trace.Request
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
	if identity.ID != "" {
		var found string
		e = tx.QueryRowContext(ctx, `SELECT id FROM jobs WHERE kind=? AND json_extract(payload,'$.id')=? AND state IN ('queued','running','retry_wait','waiting_paused') LIMIT 1`, kind, identity.ID).Scan(&found)
		if e == nil {
			return Job{}, bad("JOB_EXISTS", "已有该对象的任务正在等待或执行")
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return Job{}, e
		}
	}
	j := Job{ID: id("job_"), WorkflowID: wf, Kind: kind, State: "queued", Payload: b, RunAt: time.Now().Unix(), CreatedAt: now(), UpdatedAt: now()}
	readJobTrace(&j)
	_, e = tx.ExecContext(ctx, `INSERT INTO jobs(id,workflow_id,kind,state,payload,run_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, j.ID, wf, kind, j.State, b, j.RunAt, j.CreatedAt, j.UpdatedAt)
	if e == nil {
		_, e = tx.ExecContext(ctx, `INSERT INTO events(time,workflow_id,job_id,level,module,event,message) VALUES(?,?,?,?,?,?,?)`, now(), wf, j.ID, "INFO", kind, "queued", "任务已加入队列")
	}
	return j, e
}
func (a *App) enqueue(ctx context.Context, kind string, payload any, wf string) (Job, error) {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return Job{}, e
	}
	defer tx.Rollback()
	j, e := enqueueTx(ctx, tx, kind, payload, wf)
	if e != nil {
		return Job{}, e
	}
	if e = tx.Commit(); e != nil {
		return Job{}, e
	}
	select {
	case a.jobWake <- struct{}{}:
	default:
	}
	return j, nil
}
func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	var j Job
	e := row.Scan(&j.ID, &j.WorkflowID, &j.Kind, &j.State, &j.Payload, &j.Attempt, &j.Generation, &j.RunAt, &j.LeaseUntil, &j.ErrorCode, &j.ErrorMessage, &j.CreatedAt, &j.UpdatedAt)
	readJobTrace(&j)
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

const jobColumns = `id,workflow_id,kind,state,payload,attempt,generation,run_at,lease_until,error_code,error_message,created_at,updated_at`

func (a *App) jobs(ctx context.Context, limit int) ([]Job, error) {
	rows, e := a.Store.DB.QueryContext(ctx, `SELECT `+jobColumns+` FROM jobs ORDER BY created_at DESC LIMIT ?`, limit)
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
func (a *App) claim(ctx context.Context) (Job, error) {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return Job{}, e
	}
	defer tx.Rollback()
	j, e := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE state IN ('queued','retry_wait') AND run_at<=? ORDER BY run_at,created_at LIMIT 1`, time.Now().Unix()))
	if e != nil {
		return Job{}, e
	}
	j.State = "running"
	j.Attempt++
	j.Generation++
	j.LeaseUntil = time.Now().Add(10 * time.Minute).Unix()
	j.UpdatedAt = now()
	_, e = tx.ExecContext(ctx, `UPDATE jobs SET state=?,attempt=?,generation=?,lease_until=?,updated_at=? WHERE id=?`, j.State, j.Attempt, j.Generation, j.LeaseUntil, j.UpdatedAt, j.ID)
	if e != nil {
		return Job{}, e
	}
	return j, tx.Commit()
}
func (a *App) worker(ctx context.Context) {
	timer := time.NewTicker(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		j, e := a.claim(ctx)
		if e != nil {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			case <-a.jobWake:
			}
			continue
		}
		if a.paused() && j.Kind != "asset_check" {
			_, _ = a.Store.DB.ExecContext(ctx, `UPDATE jobs SET state='waiting_paused',attempt=attempt-1,updated_at=? WHERE id=? AND generation=?`, now(), j.ID, j.Generation)
			continue
		}
		a.event(ctx, j, "INFO", j.Kind, "started", "开始执行任务")
		e = a.safeRun(a.traceContext(ctx, j.WorkflowID, j.ID, j.RequestID), j)
		if ctx.Err() != nil {
			return
		}
		var currentState string
		var currentGeneration int
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT state,generation FROM jobs WHERE id=?`, j.ID).Scan(&currentState, &currentGeneration); err != nil || currentState != "running" || currentGeneration != j.Generation {
			continue
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
			var pendingRefresh *pluginRefreshWait
			if errors.As(e, &pendingRefresh) {
				code, state, runAt = "PLUGIN_REFRESH_PENDING", "retry_wait", pendingRefresh.Until.Unix()
			} else if code == "DOWNLOADER_DISABLED" {
				state = "retry_wait"
				runAt = time.Now().Add(time.Minute).Unix()
			} else if code == "AUTOMATION_PAUSED" {
				state = "waiting_paused"
			} else if j.Attempt < 3 && !strings.Contains(code, "REVIEW") && !strings.Contains(code, "CONFLICT") && !strings.Contains(code, "INVALID") && !strings.Contains(code, "EXCLUDED") {
				state = "retry_wait"
				runAt = time.Now().Add(time.Duration(1<<j.Attempt) * 30 * time.Second).Unix()
			}
		}
		changed, updateErr := a.finishJob(ctx, j, state, code, message, runAt)
		if updateErr != nil {
			a.log("ERROR", "jobs", "state_persist_failed", "任务状态无法保存，自动化已暂停，请检查配置盘", j.WorkflowID, j.ID)
			_ = a.setControl(ctx, true)
			continue
		}
		if changed && state == "failed" {
			a.notifyFailed(ctx, j, message)
		}
		if changed {
			if code == "PLUGIN_REFRESH_PENDING" {
				a.log("DEBUG", j.Kind, code, message, j.WorkflowID, j.ID)
			} else if e != nil {
				a.log("ERROR", j.Kind, code, message, j.WorkflowID, j.ID)
			} else {
				a.log("INFO", j.Kind, "completed", "任务完成", j.WorkflowID, j.ID)
			}
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
	case "site_refresh":
		return a.runSiteRefresh(ctx, j)
	case "native_download":
		return a.runNativeDownload(ctx, j, p.ID)
	case "download":
		return a.runDownload(ctx, j, p.ID)
	case "import":
		return a.runImport(ctx, j, p.ID, p.FileIDs)
	case "reorganize_cleanup":
		return a.runReorganizeCleanup(ctx, j, p.ID)
	case "scrape":
		return a.runScrape(ctx, j, p.ID)
	case "fnos_sync":
		return a.runFNOSSync(ctx, j, p.ID)
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
func (a *App) jobAPI(w http.ResponseWriter, r *http.Request, p string) (any, error) {
	parts := strings.Split(p, "/")
	if len(parts) > 2 || parts[0] == "" {
		return nil, bad("INVALID_JOB", "任务ID无效")
	}
	j, e := scanJob(a.Store.DB.QueryRowContext(r.Context(), `SELECT `+jobColumns+` FROM jobs WHERE id=?`, parts[0]))
	if e != nil {
		return nil, e
	}
	if len(parts) == 1 && r.Method == "GET" {
		rows, e := a.Store.DB.QueryContext(r.Context(), `SELECT seq,time,workflow_id,job_id,level,module,event,message FROM events WHERE job_id=? ORDER BY seq LIMIT 1000`, j.ID)
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
		return map[string]any{"job": view, "events": events, "decisions": decisions}, nil
	}
	if len(parts) == 2 && r.Method == "POST" {
		var result sql.Result
		switch parts[1] {
		case "retry":
			if j.State != "failed" && j.State != "cancelled" && j.State != "needs_review" {
				return nil, bad("JOB_NOT_RETRYABLE", "只能重试失败、取消或待核对的任务")
			}
			result, e = a.Store.DB.ExecContext(r.Context(), `UPDATE jobs SET state='queued',attempt=0,generation=generation+1,run_at=?,error_code='',error_message='',updated_at=? WHERE id=? AND generation=? AND state=?`, time.Now().Unix(), now(), j.ID, j.Generation, j.State)
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
		raw, e := a.Store.List(ctx, "subscription", 500, 0)
		if e != nil {
			continue
		}
		for _, b := range raw {
			var s Subscription
			if json.Unmarshal(b, &s) != nil || s.State != "active" || s.Schedule.Mode == "manual" {
				continue
			}
			t, e := time.Parse(time.RFC3339Nano, s.NextCheck)
			if e == nil && t.After(time.Now()) {
				continue
			}
			_, e = a.enqueue(ctx, "subscription", map[string]any{"id": s.ID, "revision": s.Revision}, id("wf_"))
			if e != nil {
				continue
			}
		}
	}
}
func jobErr(code string, e error) error { return bad(code, fmt.Sprint(e)) }
