package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins/schedule"
	"strings"
	"time"
)

// Refresh is synchronous, as in the source Scheduler.update_plugin_job. New
// concurrent SDK calls permit getters while the business call awaits this
// callback. No storage, object method or worker-shared business lock is held.
func (a *App) originalRefreshServicesResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	p := scope.object
	if p == nil || p.app != a || scope.phase == "stop" || scope.phase == "get_state" || scope.phase == "get_service" {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "服务刷新不能从声明读取或清理阶段重入")
	}
	var empty struct{}
	if err := originalResourceDecode(raw, &empty); err != nil {
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
	a.pluginOriginalServicesMu.Lock()
	defer a.pluginOriginalServicesMu.Unlock()
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	// An initial or retained failed object is not in PluginManager's running
	// map. Its submitted methods may run, but it has no published services.
	// A same-ID replacement must not have its registrations removed here.
	if !originalObjectStillLoaded(p) {
		return json.RawMessage(`null`), nil
	}
	a.pluginOriginalRunningMu.Lock()
	if !originalObjectStillLoaded(p) || p.stopping.Load() {
		a.pluginOriginalRunningMu.Unlock()
		return nil, bad("PLUGIN_NOT_LOADED", "插件刷新对象已替换")
	}
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	for key := range state.jobs {
		if key.instance == p.installation.InstanceID {
			if job := state.jobs[key]; job != nil && job.sourceScanCancel != nil {
				job.sourceScanCancel()
			}
			delete(state.jobs, key)
		}
	}
	state.notifyOriginalSourceDeadlineLocked()
	state.mu.Unlock()
	a.pluginOriginalRunningMu.Unlock()
	// Removal happens before reading declarations. Failure does not restore
	// the old service list or turn a failed refresh into deferred success.
	enabled, err := p.getState(ctx)
	if err != nil {
		return nil, err
	}
	if !enabled {
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
		provider, err := p.originalDisplayName(ctx)
		if err != nil {
			return nil, err
		}
		if definition.Trigger.Kind == "source_interval" {
			_ = a.addOriginalSourceIntervalJob(ctx, p, definition, provider)
			continue
		}
		at := time.Now()
		trigger, triggerErr := schedule.New(definition.Trigger, at)
		options, optionsErr := originalServiceOptions(&definition)
		if optionsErr != nil {
			triggerErr = optionsErr
		}
		timerID := p.installation.InstanceID + "_" + definition.ID
		definition.ID, _, _ = strings.Cut(definition.ID, "|")
		if definition.MisfireGraceMS == 0 {
			definition.MisfireGraceMS = 1000
		}
		a.pluginOriginalRunningMu.Lock()
		if !originalObjectStillLoaded(p) || p.stopping.Load() {
			a.pluginOriginalRunningMu.Unlock()
			return nil, bad("PLUGIN_NOT_LOADED", "插件刷新对象已替换")
		}
		state.mu.Lock()
		if state.jobs == nil {
			state.jobs = make(map[originalServiceKey]*originalDirectJob)
		}
		state.nextRegistration++
		job := &originalDirectJob{object: p, definition: definition, trigger: trigger, provider: provider, registrationOrder: state.nextRegistration, coalesceDisabled: options.coalesceDisabled, misfireUnlimited: options.unlimited, executorUnavailable: options.executorUnavailable, clockRegistered: triggerErr == nil, timerID: timerID, maxInstances: options.maxInstances}
		if triggerErr == nil {
			job.due = trigger.First(at)
			if options.hasNextRun {
				job.due = options.nextRun
			}
		}
		state.jobs[originalServiceKey{p.installation.InstanceID, definition.ID}] = job
		state.notifyOriginalSourceDeadlineLocked()
		state.mu.Unlock()
		a.pluginOriginalRunningMu.Unlock()
		if triggerErr != nil {
			a.writeOriginalPluginCallLog(scope.object, scope.installation, scope.logCall, "ERROR", "register_service_failed", "注册插件定时服务失败")
			a.reportOriginalPluginExecutionError(ctx, "system", "插件 "+provider+" 服务注册失败", nil, triggerErr)
		}
	}
	return json.RawMessage(`null`), nil
}
