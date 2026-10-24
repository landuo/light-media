package app

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/schedule"
)

// Registrations are bound methods of actual live objects. They are not durable
// jobs, configuration revisions, invocation receipts, or installed-list grants.
type originalDirectJobs struct {
	mu   sync.Mutex
	jobs map[originalServiceKey]*originalDirectJob
}
type originalDirectJob struct {
	object     *originalPluginInstance
	definition pluginScheduleDefinition
	trigger    *schedule.Trigger
	due        time.Time
	running    bool
}

func (p *originalPluginInstance) callOriginalMethod(ctx context.Context, method string, input json.RawMessage) (plugins.RunResult, error) {
	unlock := p.lockOriginalMethod()
	defer unlock()
	if !originalObjectStillLoaded(p) {
		return plugins.RunResult{}, bad("PLUGIN_NOT_LOADED", "插件对象已停止")
	}
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	return p.callSession(ctx, plugins.Runner{Timeout: 30 * time.Second}, method, input, nil)
}

func (a *App) runOriginalPluginScheduler(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case at := <-ticker.C:
			a.dispatchOriginalPluginJobs(ctx, at)
		}
	}
}
func (a *App) dispatchOriginalPluginJobs(ctx context.Context, at time.Time) {
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	for _, job := range state.jobs {
		if job.due.IsZero() || job.due.After(at) {
			continue
		}
		due := job.due
		// APScheduler's default single instance and coalescing are scheduling
		// behavior, not persistence/recovery admission. No missed job is replayed.
		for !job.due.IsZero() && !job.due.After(at) {
			due = job.due
			job.due = job.trigger.Next(job.due)
		}
		if job.running || !originalObjectStillLoaded(job.object) {
			continue
		}
		grace := time.Duration(job.definition.MisfireGraceMS) * time.Millisecond
		if grace > 0 && at.Sub(due) > grace {
			continue
		}
		if !a.admitOriginalBackground() {
			continue
		}
		job.running = true
		go func(job *originalDirectJob) {
			defer a.wg.Done()
			_, err := job.object.callOriginalMethod(ctx, job.definition.Handler, job.definition.Input)
			if err != nil {
				a.log("ERROR", "plugins", "service_failed", "插件定时方法执行失败", "", "")
			}
			state.mu.Lock()
			job.running = false
			state.mu.Unlock()
		}(job)
	}
	state.mu.Unlock()
}

// Broadcast uses the loaded registration and its current event-enabled flag.
// Each callback is independently submitted, as in the original EventManager.
func (a *App) broadcastOriginalPluginEvent(ctx context.Context, event string, data json.RawMessage) {
	data, target, restricted, err := originalBroadcastEventData(event, data)
	if err != nil {
		a.log("ERROR", "plugins", "event_invalid", "插件事件数据编码失败", "", "")
		return
	}
	a.pluginRunningInstances.Range(func(_, value any) bool {
		object := value.(*originalPluginInstance)
		// InstanceID is the manager-bound source class identity, including a
		// clone's renamed class. The shared artifact PluginID is not a target.
		if restricted && object.installation.InstanceID != target {
			return true
		}
		declared := false
		for _, name := range object.manifest.Events {
			if name == event {
				declared = true
				break
			}
		}
		object.configMu.RLock()
		enabled := object.eventsEnabled
		object.configMu.RUnlock()
		if !declared || !enabled {
			return true
		}
		if !a.admitOriginalBackground() {
			return false
		}
		go func() {
			defer a.wg.Done()
			_, err := object.callOriginalMethod(ctx, "event", mustOriginalEventJSON(event, data))
			if err != nil {
				a.log("ERROR", "plugins", "event_failed", "插件事件方法执行失败", "", "")
			}
		}()
		return true
	})
}
func mustOriginalEventJSON(event string, data json.RawMessage) json.RawMessage {
	if len(data) == 0 {
		data = json.RawMessage(`null`)
	}
	raw, _ := json.Marshal(struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}{event, data})
	return raw
}

func (a *App) removeOriginalPluginJobs(instance string) {
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	defer state.mu.Unlock()
	for key := range state.jobs {
		if key.instance == instance {
			delete(state.jobs, key)
		}
	}
}

func (a *App) originalPublicServices(at time.Time) []PluginServiceInfo {
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	defer state.mu.Unlock()
	items := []PluginServiceInfo{}
	for key, job := range state.jobs {
		if !originalObjectStillLoaded(job.object) || job.due.IsZero() && !job.running {
			continue
		}
		item := PluginServiceInfo{ID: key.instance + "_" + key.service, InstanceID: key.instance, ServiceID: key.service, Name: job.definition.Name, Provider: job.object.manifest.Name, Status: "等待", CanRun: !job.running, due: job.due, busy: job.running}
		if job.running {
			item.Status = "正在运行"
		}
		if !job.due.IsZero() {
			item.NextRun = pluginServiceTimeDifference(job.due, at)
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}
func (a *App) runOriginalPublicService(ctx context.Context, instance, id string) error {
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	job := state.jobs[originalServiceKey{instance, id}]
	if job == nil || !originalObjectStillLoaded(job.object) {
		state.mu.Unlock()
		return bad("PLUGIN_SERVICE_NOT_FOUND", "定时服务不存在")
	}
	if job.running {
		state.mu.Unlock()
		return bad("PLUGIN_BUSY", "定时服务正在运行")
	}
	if !a.admitOriginalBackground() {
		state.mu.Unlock()
		return bad("PLUGIN_NOT_LOADED", "插件管理器已停止")
	}
	job.running = true
	state.mu.Unlock()
	go func() {
		defer a.wg.Done()
		_, err := job.object.callOriginalMethod(context.WithoutCancel(ctx), job.definition.Handler, job.definition.Input)
		if err != nil {
			a.log("ERROR", "plugins", "service_failed", "插件定时方法执行失败", "", "")
		}
		state.mu.Lock()
		job.running = false
		state.mu.Unlock()
	}()
	return nil
}
