package app

import (
	"context"
	"encoding/json"
)

// This existing self-only adapter retains its phase and exception contract.
// Registry operations are short; no services mutex is held over worker RPC.
func (a *App) originalRefreshServicesResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	p := scope.object
	if p == nil || p.app != a || scope.phase == "stop" || scope.phase == "get_state" || scope.phase == "get_service" {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "服务刷新不能从声明读取或清理阶段重入")
	}
	if err := originalResourceDecode(raw, &struct{}{}); err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	p.sessionMu.Lock()
	concurrent := p.session != nil && p.session.SupportsConcurrent()
	p.sessionMu.Unlock()
	if !concurrent {
		return nil, bad("PLUGIN_CONCURRENT_REQUIRED", "同步服务刷新需要协商并发worker")
	}
	object, operation, err := a.pinOriginalRunningObject(ctx, p.installation.InstanceID)
	if err != nil {
		return nil, err
	}
	if object == nil {
		return json.RawMessage(`null`), nil
	}
	defer object.releaseOriginalRetainedOwner(operation)
	if object != p || p.stopping.Load() {
		return nil, bad("PLUGIN_NOT_LOADED", "插件刷新对象已替换")
	}
	ctx, err = p.originalRetainedParentContext(ctx, operation)
	if err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	// Preserve old registration-removal timing and exact self identity. Owner
	// retirement is outside both manager and jobs locks.
	a.pluginOriginalRunningMu.Lock()
	if !originalObjectStillLoaded(p) || p.stopping.Load() {
		a.pluginOriginalRunningMu.Unlock()
		return nil, bad("PLUGIN_NOT_LOADED", "插件刷新对象已替换")
	}
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	removed := removeOriginalJobsLocked(state, p.installation.InstanceID, "")
	state.mu.Unlock()
	a.pluginOriginalRunningMu.Unlock()
	releaseOriginalJobOwners(removed)
	value, err := p.originalManagerRawState(ctx)
	if err != nil {
		return nil, err
	}
	if !originalBaseJSONTruthy(value) {
		return json.RawMessage(`null`), nil
	}
	defs, err := p.getServices(ctx)
	if err != nil {
		return nil, err
	}
	for _, definition := range defs {
		if err := scope.validate(ctx); err != nil {
			return nil, err
		}
		if !originalObjectStillLoaded(p) || p.stopping.Load() {
			return nil, bad("PLUGIN_NOT_LOADED", "插件刷新对象已替换")
		}
		if err := a.addOriginalBoundPluginJob(ctx, p, operation, definition, true); err != nil {
			// Existing adapter reports per-definition schedule failures but does
			// not turn a later declaration into a deferred job or stop the loop.
			if !originalObjectStillLoaded(p) || p.stopping.Load() {
				return nil, bad("PLUGIN_NOT_LOADED", "插件刷新对象已替换")
			}
			provider, providerErr := p.originalDisplayName(ctx)
			if providerErr != nil {
				return nil, providerErr
			}
			a.writeOriginalPluginCallLog(scope.installation, scope.logCall, "ERROR", "register_service_failed", "注册插件定时服务失败")
			a.reportOriginalPluginExecutionError(ctx, "system", "插件 "+provider+" 服务注册失败", nil, err)
		}
	}
	return json.RawMessage(`null`), nil
}
