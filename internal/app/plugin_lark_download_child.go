package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// These jobs are durable children of one authenticated source step. A generic
// worker must not claim them: the source producer executes them directly and
// waits for the daemon response without retaining a SQL transaction.
func larkIntentIdentity(in Intent) string {
	in.State, in.Hash, in.UpdatedAt, in.Error, in.ImportPlanID = "", "", "", "", ""
	in.Progress = 0
	return tokenHash(string(mustCleanSeedJSON(in)))
}

func (a *App) larkDownloadSourceTx(ctx context.Context, tx *sql.Tx, in Intent) (*pluginLarkP0Commit, error) {
	handle, ok := ctx.Value(pluginLarkP0CommitKey{}).(*pluginLarkP0Commit)
	if !ok || handle == nil || handle.a != a || handle.operation != "download.submit" || handle.core == nil || handle.ordinal != handle.core.ordinal || handle.inputSHA != tokenHash(string(mustCleanSeedJSON(in.ID))) {
		return nil, bad("LARK_CORE_SCOPE_DENIED", "下载任务缺少原消息执行授权")
	}
	_, f, e := a.larkCoreScopeTx(ctx, tx, handle.core)
	if e != nil {
		return nil, e
	}
	steps, e := a.pluginStore.larkInboxStepsTx(ctx, tx, f)
	if e != nil {
		return nil, e
	}
	if in.LarkInbox != f.ID || handle.ordinal != len(steps)-1 {
		return nil, larkEvidenceError()
	}
	step := steps[handle.ordinal]
	if step.Kind != "write" || step.Operation != "download.submit" || step.Phase != "intent" || step.InputSHA != handle.inputSHA {
		return nil, larkEvidenceError()
	}
	for _, previous := range steps[:handle.ordinal] {
		if previous.Kind != "write" || previous.Operation != "download.confirm" || previous.Phase != "confirmed" {
			continue
		}
		var confirmed Intent
		if json.Unmarshal(previous.Result, &confirmed) == nil && confirmed.ID == in.ID && confirmed.State == "planned" && sameHash(confirmed.Hash, in.Hash) && larkIntentIdentity(confirmed) == larkIntentIdentity(in) {
			return handle, nil
		}
	}
	return nil, larkEvidenceError()
}

func larkChildIdentity(j Job, handle *pluginLarkP0Commit, in Intent) bool {
	var payload struct {
		ID string `json:"id"`
	}
	return j.Kind == "lark_download" && json.Unmarshal(j.Payload, &payload) == nil && payload.ID == in.ID && j.WorkflowID == in.WorkflowID && j.ParentJobID == handle.core.job.ID
}

func (a *App) larkClaimDownloadChild(ctx context.Context, in Intent) (Job, error) {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return Job{}, e
	}
	defer tx.Rollback()
	handle, e := a.larkDownloadSourceTx(ctx, tx, in)
	if e != nil {
		return Job{}, e
	}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind='lark_download' AND json_extract(payload,'$.id')=?`, in.ID).Scan(&count); e != nil || count != 1 {
		return Job{}, larkEvidenceError()
	}
	j, e := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind='lark_download' AND json_extract(payload,'$.id')=?`, in.ID))
	if e != nil {
		return Job{}, e
	}
	if !larkChildIdentity(j, handle, in) || j.State != "queued" || j.Attempt != 0 || j.Generation != 0 {
		return Job{}, larkEvidenceError()
	}
	j.State, j.Attempt, j.Generation = "running", 1, 1
	j.LeaseUntil, j.UpdatedAt = time.Now().Add(10*time.Minute).Unix(), now()
	if _, e = tx.ExecContext(ctx, `UPDATE jobs SET state='running',attempt=1,generation=1,lease_until=?,updated_at=? WHERE id=? AND state='queued' AND attempt=0 AND generation=0`, j.LeaseUntil, j.UpdatedAt, j.ID); e != nil {
		return Job{}, e
	}
	return j, tx.Commit()
}

func (a *App) larkDownloadChildTx(ctx context.Context, tx *sql.Tx, j Job, in Intent) error {
	handle, e := a.larkDownloadSourceTx(ctx, tx, in)
	if e != nil {
		return e
	}
	actual, e := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, j.ID))
	if e != nil {
		return e
	}
	if !larkChildIdentity(actual, handle, in) || actual.State != "running" || actual.Generation != j.Generation || !larkSameRaw(actual.Payload, j.Payload) {
		return bad("LARK_CORE_SCOPE_DENIED", "原消息下载任务已变化或终止")
	}
	return nil
}

func (a *App) guardLarkDownloadChild(ctx context.Context, j Job, in Intent) error {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	return a.larkDownloadChildTx(ctx, tx, j, in)
}

func (a *App) larkAcceptDownloadChildTx(ctx context.Context, tx *sql.Tx, in Intent, j Job) error {
	if in.LarkInbox == "" && j.Kind != "lark_download" {
		return nil
	}
	if e := a.larkDownloadChildTx(ctx, tx, j, in); e != nil {
		return e
	}
	if e := a.larkP0CommitTx(ctx, tx, "download.submit", in); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, `UPDATE jobs SET state='succeeded',lease_until=0,error_code='',error_message='',updated_at=? WHERE id=? AND state='running' AND generation=?`, now(), j.ID, j.Generation); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO events(time,workflow_id,job_id,level,module,event,message) VALUES(?,?,?,?,?,?,?)`, now(), j.WorkflowID, j.ID, "INFO", "lark_download", "completed", "下载器已接收原消息选择的资源")
	return e
}

// A planned state proves that the worker stopped before persisting its external
// Add intent. Release that failed candidate's reservation so the original batch
// can try its next candidate. Submitting/selection/unknown states never qualify.
func (a *App) larkRejectUnsubmittedChild(ctx context.Context, j Job, in Intent, cause error) error {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var raw []byte
	if e = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='intent' AND id=?`, in.ID).Scan(&raw); e != nil {
		return e
	}
	var current Intent
	if json.Unmarshal(raw, &current) != nil || current.State != "planned" || larkIntentIdentity(current) != larkIntentIdentity(in) {
		return cause
	}
	if e = a.larkDownloadChildTx(ctx, tx, j, current); e != nil {
		return e
	}
	current.State, current.Error, current.UpdatedAt = "failed", a.clean(cause.Error()), now()
	if e = txPut(ctx, tx, "intent", current.ID, current); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM reservations WHERE intent_id=?`, in.ID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE jobs SET state='failed',lease_until=0,error_code=?,error_message=?,updated_at=? WHERE id=? AND generation=? AND state='running'`, pluginErrorCodeInternal(cause), current.Error, now(), j.ID, j.Generation); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	return larkP0Failure(ctx, false, cause)
}

func (a *App) larkSubmitDownload(ctx context.Context, core *pluginLarkCoreExecution, in Intent) (Intent, error) {
	return larkCoreWrite(ctx, a, core, "download.submit", in.ID, func(ctx context.Context) (Intent, error) {
		j, e := a.larkClaimDownloadChild(ctx, in)
		if e != nil {
			return Intent{}, e
		}
		e = a.runDownload(ctx, j, in.ID)
		if e != nil {
			return Intent{}, a.larkRejectUnsubmittedChild(ctx, j, in, e)
		}
		var accepted Intent
		if e = a.Store.Get(ctx, "intent", in.ID, &accepted); e != nil {
			return Intent{}, e
		}
		if accepted.State != "accepted" {
			return Intent{}, errors.New("native download child returned without daemon acceptance")
		}
		return accepted, nil
	})
}
