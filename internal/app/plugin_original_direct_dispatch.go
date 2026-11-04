package app

import (
	"context"
	"encoding/json"
	"math/big"
	"sort"
	"sync"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/schedule"
)

// Registrations are bound methods of actual live objects. They are not durable
// jobs, configuration revisions, invocation receipts, or installed-list grants.
type originalDirectJobs struct {
	mu               sync.Mutex
	jobs             map[originalServiceKey]*originalDirectJob
	progress         map[string]*originalServiceProgress
	progressAccess   uint64
	nextRegistration uint64
	clockInstances   map[string]int
}

// Retained legacy/common clocks use these operations. Newly decoded ordinary
// plugin clocks use BuiltinTrigger optional results and a bounded turn instead.
type originalJobTrigger interface {
	FirstChecked(time.Time) (time.Time, error)
	NextChecked(time.Time) (time.Time, error)
}

type originalDirectJob struct {
	object                               *originalPluginInstance
	retained                             *originalRetainedLease // exact registry owner; released only outside jobs.mu
	commonModule                         bool
	definition                           pluginScheduleDefinition
	trigger                              originalJobTrigger
	builtinClock                         *schedule.BuiltinTrigger
	boundedClock                         bool
	duePresent                           bool
	due                                  time.Time
	running                              bool
	provider                             string
	lastStarted, lastFinished, lastError string
	lastErrorSet                         bool
	clockError                           string // trigger calculation only; callback completion must not clear it
	registrationOrder                    uint64
	coalesceDisabled, misfireUnlimited   bool
	executorUnavailable, clockRegistered bool
	timerID                              string
	maxInstances                         *big.Int
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
		case <-ticker.C:
			a.dispatchOriginalPluginJobs(ctx, time.Now())
		}
	}
}
func (a *App) dispatchOriginalPluginJobs(ctx context.Context, at time.Time) {
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	for _, job := range state.jobs {
		if job.boundedClock {
			a.dispatchOriginalBuiltinJobLocked(ctx, job, at)
			continue
		}
		if job.due.IsZero() || job.due.After(at) {
			continue
		}
		if job.executorUnavailable {
			job.due = time.Time{}
			job.clockRegistered = false
			a.log("ERROR", "plugins", "service_executor_unavailable", "服务执行器不存在，已移除时钟", "", "")
			continue
		}
		var calendarErr error
		runTimes := []time.Time{}
		for !job.due.IsZero() && !job.due.After(at) {
			if job.coalesceDisabled {
				runTimes = append(runTimes, job.due)
			} else if len(runTimes) == 0 {
				runTimes = append(runTimes, job.due)
			} else {
				runTimes[0] = job.due
			}
			job.due, calendarErr = job.trigger.NextChecked(job.due)
			if calendarErr != nil {
				break
			}
		}
		// Calendar collection errors precede submission, as in Job._get_run_times.
		// Only the approved Calendar calculation fault isolation differs from
		// source scheduler propagation; no collected callback was submitted yet.
		if calendarErr != nil {
			job.due = time.Time{}
			job.clockRegistered = false
			job.clockError = calendarErr.Error()
			a.log("ERROR", "plugins", "calendar_trigger_error", job.clockError, "", originalServiceID(job))
			continue
		}
		advanceCalendar := func() {
			if job.definition.Trigger.Kind != "calendarinterval" {
				return
			}
			job.due, calendarErr = job.trigger.NextChecked(runTimes[len(runTimes)-1])
			if calendarErr != nil {
				// Already submitted callbacks remain admitted. Retain the clock
				// fault separately from their eventual success/error progress.
				job.due = time.Time{}
				job.clockError = calendarErr.Error()
				a.log("ERROR", "plugins", "calendar_trigger_error", job.clockError, "", originalServiceID(job))
			}
			if job.due.IsZero() {
				job.clockRegistered = false
			}
		}
		if job.definition.Trigger.Kind != "calendarinterval" {
			// Old families retain their original calculation position/results.
			// Legacy adapters return the unchanged old result and nil error.
			job.due, calendarErr = job.trigger.NextChecked(runTimes[len(runTimes)-1])
			if calendarErr != nil {
				job.due = time.Time{}
				job.clockRegistered = false
				a.log("ERROR", "plugins", "calendar_trigger_error", calendarErr.Error(), "", "")
				continue
			}
			if job.due.IsZero() {
				job.clockRegistered = false
			}
		}
		if !originalServiceActive(job) {
			advanceCalendar()
			continue
		}
		maximum := job.maxInstances
		if maximum == nil {
			maximum = big.NewInt(1)
		}
		if big.NewInt(int64(state.clockInstances[job.timerID])).Cmp(maximum) >= 0 {
			a.log("WARN", "plugins", "service_max_instances", "服务时钟已达到最大并发实例数", "", "")
			advanceCalendar()
			continue
		}
		if !a.admitOriginalBackground() {
			advanceCalendar()
			continue
		}
		if state.clockInstances == nil {
			state.clockInstances = map[string]int{}
		}
		state.clockInstances[job.timerID]++
		key := originalServiceLookupKey(job)
		grace := time.Duration(job.definition.MisfireGraceMS) * time.Millisecond
		go func(job *originalDirectJob, key originalServiceKey, scheduled []time.Time, grace time.Duration, unlimited bool) {
			defer a.wg.Done()
			defer func() {
				state.mu.Lock()
				state.clockInstances[job.timerID]--
				if state.clockInstances[job.timerID] == 0 {
					delete(state.clockInstances, job.timerID)
				}
				state.mu.Unlock()
			}()
			// Each scheduled call uses exactly the same start operation as a
			// manual invocation. It resolves the current logical registration,
			// checks its running flag, and waits only for ordinary sync methods.
			for _, due := range scheduled {
				if !unlimited && grace > 0 && time.Since(due) > grace {
					continue
				}
				_ = a.runOriginalService(ctx, key.instance, key.service, true)
			}
		}(job, key, runTimes, grace, job.misfireUnlimited)
		// APS submits this batch before its second Next/fresh jitter call.
		advanceCalendar()
	}
	state.mu.Unlock()
}

// Broadcast uses the loaded registration and its current event-enabled flag.
// Each callback is independently submitted, as in the original EventManager.
func (a *App) broadcastOriginalPluginEvent(ctx context.Context, event string, data json.RawMessage) {
	a.broadcastOriginalPluginEventCoreState(ctx, event, data, false)
}

func (a *App) broadcastOriginalPluginEventCoreState(ctx context.Context, event string, data json.RawMessage, consumed bool) {
	value, err := originalNewEvent(event, data, nil)
	if err != nil {
		a.log("ERROR", "plugins", "event_invalid", "插件事件数据编码失败", "", "")
		return
	}
	a.enqueueOriginalBroadcastCoreState(ctx, value, consumed)
}

func (a *App) dispatchOriginalBroadcast(ctx context.Context, event plugins.MutableEvent, consumed bool) {
	data, target, restricted, err := originalBroadcastEventData(event.Type, event.Data)
	if err != nil {
		a.log("ERROR", "plugins", "event_invalid", "插件事件数据编码失败", "", "")
		return
	}
	if !consumed {
		a.submitOriginalCoreBroadcast(ctx, event.Type, data)
	}
	for _, handler := range a.originalBroadcastSnapshot(event.Type) {
		object := handler.object
		// InstanceID is the manager-bound source class identity, including a
		// clone's renamed class. The shared artifact PluginID is not a target.
		if restricted && object.installation.InstanceID != target {
			continue
		}
		if !a.admitOriginalBackground() {
			return
		}
		go func() {
			defer a.wg.Done()
			// Source constructs a distinct Event (and UUID) for each subscriber.
			isolated, err := originalNewEvent(event.Type, data, event.Priority)
			if err != nil {
				return
			}
			raw, _ := json.Marshal(isolated)
			_, _, err = object.callOriginalEventMethod(ctx, handler.Method, raw)
			if err != nil {
				a.log("ERROR", "plugins", "event_failed", "插件事件方法执行失败", "", "")
				a.reportOriginalEventError(ctx, object, event.Type, handler.Method, err)
			}
		}()
	}
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
	a.removeOriginalPluginJobsSelected(instance, "")
}

func (a *App) originalPublicServices(at time.Time) []PluginServiceInfo {
	items, _ := a.originalPublicServicesResult(at)
	return items
}

func (a *App) originalPublicServicesResult(at time.Time) ([]PluginServiceInfo, error) {
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	defer state.mu.Unlock()
	// The fixed source sorts raw APScheduler next_run_time values before
	// projecting rows. A paused timer has None; mixing it with another timer
	// raises TypeError. Preserve this failure instead of inventing a paused UI.
	registered, paused := 0, false
	for _, job := range state.jobs {
		if originalServiceActive(job) && job.clockError == "" && (job.clockRegistered || originalJobHasDue(job)) {
			registered++
			paused = paused || !originalJobHasDue(job)
		}
	}
	if registered > 1 && paused {
		return nil, &APIError{"PLUGIN_SERVICE_LIST_FAILED", "服务列表时间排序失败", 500}
	}
	items := []PluginServiceInfo{}
	for key, job := range state.jobs {
		if !originalServiceActive(job) || !job.clockRegistered && !originalJobHasDue(job) && !job.running && job.clockError == "" {
			continue
		}
		item := PluginServiceInfo{ID: originalServiceID(job), InstanceID: key.instance, ServiceID: key.service, Name: job.definition.Name, Provider: originalServiceProvider(job), Status: "等待", CanRun: !job.running, due: job.due, busy: job.running, registrationOrder: job.registrationOrder}
		// The source extracts running plugin jobs by provider_name. The
		// common system job has no provider_name and stays in clock order.
		item.busy = job.running && !job.commonModule
		item.ProgressDetail = a.originalServiceProgressSnapshotLocked(item.ID)
		if err := normalizeOriginalServiceProgressProjection(item.ProgressDetail); err != nil {
			return nil, err
		}
		if progress := state.progress[item.ID]; progress != nil {
			text := progress.text
			item.Progress, item.ProgressText, item.ProgressEnable = progress.value, &text, progress.enabled
		}
		if job.running {
			item.Status = "正在运行"
		} else if job.clockError != "" {
			item.Status = "停止"
		}
		if job.clockError != "" {
			// Keep the existing exact running status/button contract while a
			// submitted callback drains; future-clock failure remains visible.
			nextRun := "时钟已停止：" + job.clockError
			item.NextRun = &nextRun
		} else if !job.running || job.commonModule {
			nextRun := pluginServiceTimeDifference(job.due, at)
			item.NextRun = &nextRun
		}
		label := func(text string) *string {
			if text == "" {
				return nil
			}
			return &text
		}
		item.NameI18N, item.ProviderI18N, item.StatusI18N = label(item.Name), label(item.Provider), label(item.Status)
		if item.NextRun != nil {
			item.NextRunI18N = label(*item.NextRun)
		}
		if item.ProgressText != nil {
			item.ProgressTextI18N = label(*item.ProgressText)
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.busy != right.busy {
			return left.busy
		}
		if left.busy {
			return left.registrationOrder < right.registrationOrder
		}
		if left.due.Equal(right.due) {
			return left.ID < right.ID
		}
		return left.due.Before(right.due)
	})
	return items, nil
}
func (a *App) runOriginalPublicService(ctx context.Context, instance, id string) error {
	return a.runOriginalService(ctx, instance, id, false)
}

func (a *App) runOriginalService(ctx context.Context, instance, id string, wait bool) error {
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	jobID := id
	if instance != "" {
		jobID = instance + "_" + id
	}
	job := lookupOriginalJobIDLocked(state, jobID)
	if job == nil || !originalServiceActive(job) {
		state.mu.Unlock()
		return bad("PLUGIN_SERVICE_NOT_FOUND", "定时服务不存在")
	}
	if job.running {
		state.mu.Unlock()
		return bad("PLUGIN_BUSY", "定时服务正在运行")
	}
	var operation *originalRetainedLease
	if !job.commonModule {
		var err error
		operation, err = job.object.originalRetainedState().retain(originalRetainedOperation, job.retained)
		if err != nil {
			state.mu.Unlock()
			return err
		}
	}
	if !a.admitOriginalBackground() {
		state.mu.Unlock()
		if operation != nil {
			job.object.releaseOriginalRetainedOwner(operation)
		}
		return bad("PLUGIN_NOT_LOADED", "插件管理器已停止")
	}
	a.prepareOriginalServiceLocked(job)
	state.mu.Unlock()
	if wait && !job.definition.Async {
		defer a.wg.Done()
		a.executeOriginalService(context.WithoutCancel(ctx), job, operation)
		return nil
	}
	go func() {
		defer a.wg.Done()
		a.executeOriginalService(context.WithoutCancel(ctx), job, operation)
	}()
	return nil
}
