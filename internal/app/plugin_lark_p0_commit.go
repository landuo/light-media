package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type pluginLarkP0CommitKey struct{}
type pluginLarkP0Commit struct {
	a                   *App
	core                *pluginLarkCoreExecution
	ordinal             int
	operation, inputSHA string
}
type pluginLarkP0NoEffect struct{ cause error }

func (e *pluginLarkP0NoEffect) Error() string { return e.cause.Error() }
func (e *pluginLarkP0NoEffect) Unwrap() error { return e.cause }

// Only a source-bound native local write creates this private context. The P0
// transaction confirms its real result together with the business mutation;
// caller/network errors after Commit cannot allocate another operation.
func (a *App) larkP0CommitTx(ctx context.Context, tx *sql.Tx, operation string, value any) error {
	handle, ok := ctx.Value(pluginLarkP0CommitKey{}).(*pluginLarkP0Commit)
	if !ok {
		return nil
	}
	if handle == nil || handle.a != a || handle.operation != operation || handle.core.ordinal != handle.ordinal {
		return bad("LARK_CORE_SCOPE_DENIED", "本地变更与原消息授权不一致")
	}
	root, f, e := a.larkCoreScopeTx(ctx, tx, handle.core)
	if e != nil {
		return e
	}
	steps, e := a.pluginStore.larkInboxStepsTx(ctx, tx, f)
	if e != nil {
		return e
	}
	if handle.ordinal != len(steps)-1 {
		return larkEvidenceError()
	}
	step := steps[handle.ordinal]
	if step.Kind != "write" || step.Operation != operation || step.InputSHA != handle.inputSHA || step.Phase != "intent" {
		return larkEvidenceError()
	}
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	step.Phase, step.Result = "confirmed", raw
	return a.larkSaveCoreStepTx(ctx, tx, root, f, step, false)
}
func larkP0Failure(ctx context.Context, commitAttempted bool, cause error) error {
	if cause == nil {
		return nil
	}
	if handle, ok := ctx.Value(pluginLarkP0CommitKey{}).(*pluginLarkP0Commit); ok && handle != nil && !commitAttempted {
		return &pluginLarkP0NoEffect{cause}
	}
	return cause
}
func larkKnownP0Failure(cause error) bool {
	var known *pluginLarkP0NoEffect
	return errors.As(cause, &known)
}
