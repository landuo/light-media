package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

const pluginLarkReceiptTimeout = 3 * time.Second

// Local P0 operations participate in the same authenticated, ordered source
// transcript as module calls. Read results are frozen before any dependent
// reply. A write intent without its committed result is never replayed.
func larkLocalStepValid(s pluginLarkInboxStep) bool {
	if s.Kind != "read" && s.Kind != "write" && s.Kind != "transaction" {
		return false
	}
	if s.Operation == "" || !pluginStorageDigest.MatchString(s.InputSHA) || s.Call != "" || s.Run != "" {
		return false
	}
	switch s.Phase {
	case "intent":
		return s.Kind != "transaction" && len(s.Result) == 0 && s.ErrorCode == ""
	case "confirmed":
		return json.Valid(s.Result) && s.ErrorCode == ""
	case "failed":
		return s.ErrorCode != "" && len(s.Result) == 0
	case "unknown":
		return s.Kind == "write" && len(s.Result) == 0
	}
	return false
}
func (a *App) larkSaveCoreStepTx(ctx context.Context, tx *sql.Tx, root pluginLarkRootView, f pluginLarkInbox, step pluginLarkInboxStep, appendStep bool) error {
	if step.Inbox != f.ID || (appendStep && step.Ordinal != f.Count) || (!appendStep && step.Ordinal != f.Count-1) {
		return larkEvidenceError()
	}
	sealed, e := a.pluginStore.seal("lark_inbox_step", f.Instance, f.ID+"/"+notificationChannelRevisionKey(int64(step.Ordinal)), mustCleanSeedJSON(step))
	if e != nil {
		return e
	}
	if appendStep {
		_, e = tx.ExecContext(ctx, `INSERT INTO plugin_lark_inbox_steps VALUES(?,?,?)`, f.ID, step.Ordinal, sealed)
		f.Count++
	} else {
		_, e = tx.ExecContext(ctx, `UPDATE plugin_lark_inbox_steps SET sealed=? WHERE inbox_id=? AND ordinal=?`, sealed, f.ID, step.Ordinal)
	}
	if e != nil {
		return e
	}
	f.LastSHA = larkInboxStepSHA(step)
	return a.pluginStore.larkSaveInboxTx(ctx, tx, root, f)
}
func (a *App) larkCoreLocal(ctx context.Context, core *pluginLarkCoreExecution, kind, operation string, input any, fn func(context.Context) (json.RawMessage, error)) (json.RawMessage, error) {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	root, f, e := a.larkCoreScopeTx(ctx, tx, core)
	if e != nil {
		return nil, e
	}
	steps, e := a.pluginStore.larkInboxStepsTx(ctx, tx, f)
	if e != nil {
		return nil, e
	}
	step := pluginLarkInboxStep{Version: 1, Inbox: f.ID, Ordinal: core.ordinal, Kind: kind, Operation: operation, InputSHA: tokenHash(string(mustCleanSeedJSON(input))), PreviousSHA: f.LastSHA, Phase: "intent"}
	if core.ordinal < len(steps) {
		step = steps[core.ordinal]
		if step.Kind != kind || step.Operation != operation || step.InputSHA != tokenHash(string(mustCleanSeedJSON(input))) {
			return nil, larkEvidenceError()
		}
		switch step.Phase {
		case "confirmed":
			core.ordinal++
			return step.Result, tx.Commit()
		case "failed":
			core.ordinal++
			return nil, bad(step.ErrorCode, "原消息步骤执行失败，保留原始结果")
		case "unknown":
			return nil, larkUnknownError()
		case "intent":
			if kind == "write" {
				return nil, larkUnknownError()
			}
		}
	} else {
		if core.ordinal != len(steps) {
			return nil, larkEvidenceError()
		}
		if e = a.larkSaveCoreStepTx(ctx, tx, root, f, step, true); e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	operationCtx := ctx
	if kind == "write" {
		operationCtx = context.WithValue(operationCtx, jobTraceKey{}, jobTrace{Job: core.job.ID, Request: core.job.RequestID})
		operationCtx = context.WithValue(operationCtx, pluginLarkP0CommitKey{}, &pluginLarkP0Commit{a, core, core.ordinal, operation, step.InputSHA})
	}
	result, cause := fn(operationCtx)
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), pluginLarkReceiptTimeout)
	defer cancel()
	tx, e = a.Store.DB.BeginTx(finish, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	root, f, e = a.larkCoreScopeTx(finish, tx, core)
	if e != nil {
		return nil, fmt.Errorf("local result source scope: %w", e)
	}
	steps, e = a.pluginStore.larkInboxStepsTx(finish, tx, f)
	if e != nil {
		return nil, e
	}
	if core.ordinal < len(steps) && steps[core.ordinal].Phase == "confirmed" {
		accepted := steps[core.ordinal]
		if accepted.Kind != kind || accepted.Operation != operation || accepted.InputSHA != step.InputSHA {
			return nil, larkEvidenceError()
		}
		if cause == nil && !larkSameRaw(mustCleanSeedJSON(json.RawMessage(result)), mustCleanSeedJSON(accepted.Result)) {
			return nil, larkEvidenceError()
		}
		if e = tx.Commit(); e != nil {
			return nil, e
		}
		core.ordinal++
		return accepted.Result, nil
	}
	if core.ordinal != len(steps)-1 || larkInboxStepSHA(steps[core.ordinal]) != larkInboxStepSHA(step) {
		return nil, fmt.Errorf("local result step differs: %w", larkEvidenceError())
	}
	step.Phase = "confirmed"
	step.Result = append(json.RawMessage(nil), result...)
	if cause != nil {
		step.Result = nil
		step.ErrorCode = pluginErrorCodeInternal(cause)
		step.Phase = "failed"
		if kind == "write" && !larkKnownP0Failure(cause) {
			step.Phase = "unknown"
		}
	} else if !json.Valid(step.Result) {
		return nil, larkEvidenceError()
	}
	if e = a.larkSaveCoreStepTx(finish, tx, root, f, step, false); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	if cause != nil {
		if kind == "write" && !larkKnownP0Failure(cause) {
			return nil, larkUnknownError()
		}
		core.ordinal++
		return nil, cause
	}
	core.ordinal++
	return result, nil
}
func larkCoreRead[T any](ctx context.Context, a *App, core *pluginLarkCoreExecution, operation string, input any, fn func(context.Context) (T, error)) (T, error) {
	return larkCoreLocal(ctx, a, core, "read", operation, input, fn)
}
func larkCoreWrite[T any](ctx context.Context, a *App, core *pluginLarkCoreExecution, operation string, input any, fn func(context.Context) (T, error)) (T, error) {
	return larkCoreLocal(ctx, a, core, "write", operation, input, fn)
}
func larkCoreLocal[T any](ctx context.Context, a *App, core *pluginLarkCoreExecution, kind, operation string, input any, fn func(context.Context) (T, error)) (T, error) {
	var out T
	raw, e := a.larkCoreLocal(ctx, core, kind, operation, input, func(ctx context.Context) (json.RawMessage, error) {
		v, e := fn(ctx)
		if e != nil {
			return nil, e
		}
		return json.Marshal(v)
	})
	if e != nil {
		return out, e
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if e = decoder.Decode(&out); e != nil {
		return out, larkEvidenceError()
	}
	return out, nil
}
