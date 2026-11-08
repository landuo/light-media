package app

import (
	"context"
	"sync/atomic"
)

// A reverse call made by a synchronous initializer belongs to the operation
// already holding the manager mutex. The lease is created only on the host
// after that lock was acquired. No worker field, method name or installation
// identifier can create it or keep it alive after the original operation.
type originalOperationOwnerKey struct{}
type originalOperationOwner struct {
	app    *App
	closed atomic.Bool
}

func (a *App) originalOperationOwner(ctx context.Context) *originalOperationOwner {
	owner, _ := ctx.Value(originalOperationOwnerKey{}).(*originalOperationOwner)
	if owner == nil || owner.app != a || owner.closed.Load() {
		return nil
	}
	return owner
}

// The caller must hold pluginOriginalOperationMu for this entire lifetime.
func (a *App) bindOriginalOperationOwner(ctx context.Context) (context.Context, func()) {
	owner := &originalOperationOwner{app: a}
	return context.WithValue(ctx, originalOperationOwnerKey{}, owner), func() { owner.closed.Store(true) }
}

func (a *App) enterOriginalManagerOperation(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if a.originalOperationOwner(ctx) != nil {
		return ctx, func() {}, nil
	}
	if !a.pluginOriginalOperationMu.TryLock() {
		return nil, nil, bad("PLUGIN_OPERATION_BUSY", "另一插件生命周期操作正在执行，请重试")
	}
	ctx, end := a.bindOriginalOperationOwner(ctx)
	return ctx, func() { end(); a.pluginOriginalOperationMu.Unlock() }, nil
}
