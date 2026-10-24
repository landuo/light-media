package app

import (
	"context"
	"light-media/internal/plugins/schedule"
	"time"
)

func (p *originalPluginInstance) getServices(ctx context.Context) ([]pluginScheduleDefinition, error) {
	p.methodMu.Lock()
	defer p.methodMu.Unlock()
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
	at := time.Now()
	trigger, err := schedule.New(definition.Trigger, at)
	if err != nil {
		return err
	}
	if definition.MisfireGraceMS == 0 {
		definition.MisfireGraceMS = 1000
	}
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.jobs == nil {
		state.jobs = make(map[originalServiceKey]*originalDirectJob)
	}
	state.jobs[originalServiceKey{in.InstanceID, definition.ID}] = &originalDirectJob{object: object, definition: definition, trigger: trigger, due: trigger.First(at)}
	return nil
}
