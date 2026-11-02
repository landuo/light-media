package app

import (
	"context"
	"light-media/internal/systemcommands"
)

func (a *App) attachOriginalNativeSystemCommands(parent context.Context, processAuthority bool) error {
	version, err := a.newOriginalNativeVersion()
	if err != nil {
		return err
	}
	return a.attachOriginalSystemCommandHandlers(parent, a.originalNativeSystemHandlers(version, processAuthority), processAuthority)
}

// Private assembly shares exactly the production native effects; tests may
// record effects without installing a public provider or network override.
func (a *App) originalNativeSystemHandlers(version *systemcommands.NativeVersion, processAuthority bool) originalSystemCommandHandlers {
	return originalSystemCommandHandlers{
		restart: a.originalNativeRestartHandler(processAuthority),
		finish:  version.RestartFinish,
		version: func(ctx context.Context, in originalSystemCommandInput) error {
			in, err := originalSystemRestartInput(in)
			if err != nil {
				return err
			}
			channel, source, err := originalSystemRestartArguments(in)
			if err != nil {
				return err
			}
			return version.Version(ctx, channel, in.User, source)
		},
	}
}

// MarkSelfServiceListenerReady is a trusted executable-host seam, not a route,
// plugin method, health shortcut or process-control API. The exact service
// child calls it only after successful Listen and starting Serve. It cannot
// mark successful normal plugin sync and does not itself deliver a notice.
func (a *App) MarkSelfServiceListenerReady(ctx context.Context) error {
	if ctx == nil {
		return originalSystemAdapterBoundary("listener readiness requires context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state := &a.pluginOriginalSystemCommands
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.attached || state.closed || state.lifetime.Err() != nil || state.handlers.finish == nil || state.listenerReady == nil {
		return originalSystemAdapterBoundary("native system completion is not attached")
	}
	if !state.listenerMarked {
		state.listenerMarked = true
		close(state.listenerReady)
	}
	return nil
}

// The caller has reached the exact successful source startup branch. Waiting
// is outside pluginOriginalOperationMu. The private latch + lifetime prevent
// notification before listener bind, after close, or on startup sync failure.
func (a *App) finishOriginalSystemStartup(ctx context.Context) error {
	if ctx == nil {
		return originalSystemAdapterBoundary("startup finish requires context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state := &a.pluginOriginalSystemCommands
	state.mu.Lock()
	if !state.attached || state.handlers.finish == nil {
		state.mu.Unlock()
		return nil
	}
	if state.closed || state.lifetime.Err() != nil {
		state.mu.Unlock()
		return originalSystemAdapterBoundary("system startup lifetime closed")
	}
	if state.finishClaimed {
		state.mu.Unlock()
		return nil
	}
	state.finishClaimed = true
	ready, lifetime, finish := state.listenerReady, state.lifetime, state.handlers.finish
	state.mu.Unlock()
	if !a.admitOriginalBackground() {
		return originalSystemAdapterBoundary("system startup admission closed")
	}
	defer a.wg.Done()
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	unlink := context.AfterFunc(lifetime, cancel)
	defer unlink()
	if lifetime.Err() != nil {
		cancel()
	}
	select {
	case <-ready:
	case <-work.Done():
		return work.Err()
	}
	if err := work.Err(); err != nil {
		return err
	}
	return finish(work)
}
func (a *App) finishOriginalSystemStartupAndReport(ctx context.Context) {
	if err := a.finishOriginalSystemStartup(ctx); err != nil && ctx.Err() == nil {
		a.reportOriginalPluginExecutionError(ctx, "system", "重启完成通知失败", nil, err)
	}
}
func (a *App) startOriginalSystemSafeFinish(ctx context.Context) {
	// Safe Start must return so the executable can start Serve and mark the
	// listener. The owned admission token makes Close cancel/join this task.
	if !a.admitOriginalBackground() {
		return
	}
	go func() { defer a.wg.Done(); a.finishOriginalSystemStartupAndReport(ctx) }()
}
