package app

import (
	"context"
	"light-media/internal/plugins/schedule"
	"strings"
	"time"
)

func (p *originalPluginInstance) getServices(ctx context.Context) ([]pluginScheduleDefinition, error) {
	unlock := p.lockOriginalMethod()
	defer unlock()
	var defs []pluginScheduleDefinition
	err := p.readSession(ctx, "get_service", &defs)
	return defs, err
}
func (a *App) updateOriginalPluginJobs(ctx context.Context, instance string) {
	a.pluginOriginalServicesMu.Lock()
	defer a.pluginOriginalServicesMu.Unlock()
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	for key := range state.jobs {
		if key.instance == instance {
			delete(state.jobs, key)
		}
	}
	state.mu.Unlock()
	value, ok := a.pluginRunningInstances.Load(instance)
	if !ok {
		return
	}
	object := value.(*originalPluginInstance)
	enabled, err := object.getState(ctx)
	if err != nil || !enabled {
		return
	}
	defs, err := object.getServices(ctx)
	if err != nil {
		a.log("ERROR", "plugins", "register_services_failed", "读取插件定时服务失败", "", "")
		return
	}
	for _, definition := range defs {
		if err := a.addOriginalPluginJob(ctx, object.installation, definition); err != nil {
			a.log("ERROR", "plugins", "register_service_failed", "注册插件定时服务失败", "", "")
			a.reportOriginalPluginExecutionError(ctx, "system", "插件 "+object.manifest.Name+" 服务注册失败", nil, err)
		}
	}
}
func (a *App) addOriginalPluginJob(ctx context.Context, in PluginInstallation, definition pluginScheduleDefinition) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	value, ok := a.pluginRunningInstances.Load(in.InstanceID)
	if !ok {
		return bad("PLUGIN_NOT_LOADED", "插件未加载")
	}
	object := value.(*originalPluginInstance)
	provider, err := object.originalDisplayName(ctx)
	if err != nil {
		return err
	}
	at := time.Now()
	trigger, triggerErr := schedule.New(definition.Trigger, at)
	options, optionsErr := originalServiceOptions(&definition)
	if optionsErr != nil {
		triggerErr = optionsErr
	}
	// Scheduler uses the prefix as the executable job identity. Its explicit
	// remove_plugin_job call removes every prior suffixed timer before replacing
	// the method, so the final declaration wins (including invalid triggers).
	timerID := in.InstanceID + "_" + definition.ID
	definition.ID, _, _ = strings.Cut(definition.ID, "|")
	if definition.MisfireGraceMS == 0 {
		definition.MisfireGraceMS = 1000
	}
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.jobs == nil {
		state.jobs = make(map[originalServiceKey]*originalDirectJob)
	}
	state.nextRegistration++
	job := &originalDirectJob{object: object, definition: definition, trigger: trigger, provider: provider, registrationOrder: state.nextRegistration, coalesceDisabled: options.coalesceDisabled, misfireUnlimited: options.unlimited, executorUnavailable: options.executorUnavailable, clockRegistered: triggerErr == nil, timerID: timerID, maxInstances: options.maxInstances}
	if triggerErr == nil {
		job.due = trigger.First(at)
		if options.hasNextRun {
			job.due = options.nextRun
		}
	}
	state.jobs[originalServiceKey{in.InstanceID, definition.ID}] = job
	return triggerErr
}
