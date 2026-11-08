package app

import (
	"context"
	"errors"
	"math/big"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"light-media/internal/plugins/schedule"
)

// Source-only state belongs to the existing ephemeral scheduler. It is neither
// a durable job nor a new executor. Logical methods precede native construction.
func originalDirectDuePresent(job *originalDirectJob) bool {
	if job.definition.Trigger.Kind == "source_interval" {
		return job.sourceDuePresent
	}
	return !job.due.IsZero()
}

// Actual ConfigModel.TZ is a stripped string, defaulting only when unset. Do
// not use identifiersTimezone(), whose invalid/empty fallback is different.
// Cache the scheduler clock once for this App, like BackgroundScheduler(TZ).
func (s *originalDirectJobs) sourceIntervalClockLocked() (string, *time.Location, error) {
	if !s.sourceClockSet {
		s.sourceClockSet = true
		name, exists := os.LookupEnv("TZ")
		if !exists {
			name = "Asia/Shanghai"
		}
		name = strings.TrimFunc(name, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
		s.sourceClockName = name
		if name == "" || name == "Local" || len(name) > 128 {
			s.sourceClockError = bad("PLUGIN_SCHEDULE_TIMEZONE_INVALID", "源调度器时区无效")
		} else {
			s.sourceClockLocation, s.sourceClockError = time.LoadLocation(name)
			if s.sourceClockError != nil {
				s.sourceClockError = bad("PLUGIN_SCHEDULE_TIMEZONE_INVALID", "源调度器时区无效")
			}
		}
	}
	return s.sourceClockName, s.sourceClockLocation, s.sourceClockError
}

func (a *App) addOriginalSourceIntervalJob(ctx context.Context, p *originalPluginInstance, definition pluginScheduleDefinition, provider string) error {
	return a.addOriginalSourceIntervalJobAt(ctx, p, definition, provider, time.Now())
}

func (a *App) addOriginalSourceIntervalJobAt(ctx context.Context, p *originalPluginInstance, definition pluginScheduleDefinition, provider string, at time.Time) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Source error diagnostics retain the original service descriptor even when
	// logical method identity strips an optional native job suffix.
	diagnosticDefinition := definition
	timerID := p.installation.InstanceID + "_" + definition.ID
	definition.ID, _, _ = strings.Cut(definition.ID, "|")
	state := &a.pluginOriginalDirectJobs
	job := &originalDirectJob{object: p, definition: definition, provider: provider, timerID: timerID}
	key := originalServiceKey{p.installation.InstanceID, definition.ID}
	a.pluginOriginalRunningMu.Lock()
	if !originalObjectStillLoaded(p) || p.stopping.Load() {
		a.pluginOriginalRunningMu.Unlock()
		return bad("PLUGIN_NOT_LOADED", "插件刷新对象已替换")
	}
	state.mu.Lock()
	if state.jobs == nil {
		state.jobs = map[originalServiceKey]*originalDirectJob{}
	}
	state.nextRegistration++
	job.registrationOrder = state.nextRegistration
	if old := state.jobs[key]; old != nil && old.sourceScanCancel != nil {
		old.sourceScanCancel()
	}
	state.jobs[key] = job
	// The method is already registered when the source clock/trigger conversion
	// runs. Invalid NaN/infinity/overflow must not erase that logical method.
	zone, clock, clockErr := state.sourceIntervalClockLocked()
	a.pluginOriginalRunningMu.Unlock()
	defer func() {
		state.notifyOriginalSourceDeadlineLocked()
		state.mu.Unlock()
		a.reportOriginalSourceIntervalRegistration(ctx, provider, diagnosticDefinition, err)
	}()
	spec := definition.Trigger
	if spec.Timezone == "" && !spec.LocalTimezone {
		spec.Timezone = zone
	}
	var trigger *schedule.Trigger
	err = clockErr
	if err == nil {
		trigger, err = schedule.New(spec, at.In(clock))
	}
	var options originalResolvedServiceOptions
	if err == nil {
		definition.Trigger = trigger.Spec()
		options, err = originalServiceOptions(&definition)
	}
	var due time.Time
	present := false
	if err == nil {
		if options.hasNextRun {
			due, present = options.nextRun, options.nextRunPresent
		} else {
			// ZoneInfo cache identity follows its actual key, not Go Location pointers.
			due, present, err = trigger.SourceFirst(at.In(clock), !spec.LocalTimezone && spec.Timezone == zone)
		}
	}
	// MemoryJobStore converts next_run_time with utctimetuple(). An explicit
	// aware local year1/9999 may overflow UTC even though its local date parses;
	// preserve that native-add error after logical registration as in the source.
	if err == nil && present && (due.UTC().Year() < 1 || due.UTC().Year() > 9999) {
		err = &schedule.SourceIntervalError{Kind: "OverflowError", Message: "date value out of range"}
	}
	if state.jobs[key] != job {
		return bad("PLUGIN_NOT_LOADED", "插件服务注册已替换")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	job.definition = definition
	job.trigger = trigger
	job.coalesceDisabled = options.coalesceDisabled
	job.misfireUnlimited = options.unlimited
	job.maxInstances = options.maxInstances
	job.executorUnavailable = options.executorUnavailable
	job.clockRegistered = err == nil
	if err == nil {
		job.due, job.sourceDuePresent = due, present
	}
	return err
}

func (a *App) reportOriginalSourceIntervalRegistration(ctx context.Context, provider string, definition pluginScheduleDefinition, err error) {
	if err == nil {
		a.log("INFO", "plugins", "register_source_interval", a.clean("注册插件"+provider+"服务："+definition.Name+" - interval"), "", "")
		return
	}
	var source *schedule.SourceIntervalError
	if !errors.As(err, &source) {
		a.reportOriginalPluginExecutionError(ctx, "system", "插件 "+provider+" 服务注册失败", nil, err)
		return
	}
	// These errors are generated by the finite pure numeric helper, never copied
	// from arbitrary worker exception payload. Preserve the actual source reason.
	reason := source.Message
	a.log("ERROR", "plugins", "register_source_interval_failed", a.clean("注册插件"+provider+"服务失败："+reason+" - "+originalSourceIntervalServiceDiagnostic(definition)), "", "")
	title := a.clean("插件 " + provider + " 服务注册失败")
	a.systemMessages.put("system", &title, reason, nil, time.Now())
}
func originalSourceIntervalServiceDiagnostic(d pluginScheduleDefinition) string {
	// Go callable identity and float transport spelling are explicit runtime
	// adaptations. Source name/id/trigger/hours remain present in the diagnostic.
	hours := "<missing>"
	if d.Trigger.SourceIntervalHours != nil {
		hours = *d.Trigger.SourceIntervalHours
	}
	hours = originalSourceIntervalFloatRepr(hours)
	return "{'id': " + originalSourceIntervalNameRepr(d.ID) + ", 'name': " + originalSourceIntervalNameRepr(d.Name) + ", 'trigger': 'interval', 'func': <Go callable>, 'kwargs': {'hours': " + hours + "}}"
}

type originalSourceIntervalScanFault struct{ reason string }

func (e *originalSourceIntervalScanFault) Error() string { return e.reason }

// Complete collection is separate from coalescing and admission. The existing
// scheduler wakes every100ms; a single100ms resource slice bounds Go scanning.
// Negative/non-progressing valid triggers fault only the automatic clock, with
// their original descriptor/due/method retained. No collected prefix is sent.
func originalCollectSourceInterval(ctx context.Context, trigger *schedule.Trigger, due, at time.Time, present bool) ([]time.Time, error) {
	deadline := time.Now().Add(100 * time.Millisecond)
	times := []time.Time{}
	for present && !due.After(at) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !time.Now().Before(deadline) {
			return nil, &originalSourceIntervalScanFault{"Go源间隔自动时钟达到100毫秒扫描资源界限；已暂停自动时钟，保留原服务与到期时间，可手工运行或重新加载"}
		}
		times = append(times, due)
		next, exists, err := trigger.SourceNext(due)
		if err != nil {
			return nil, err
		}
		if exists && !next.After(due) {
			return nil, &originalSourceIntervalScanFault{"Go源间隔自动时钟无法向前推进；已暂停自动时钟，保留原服务与到期时间，可手工运行或重新加载"}
		}
		due, present = next, exists
	}
	return times, nil
}

func (a *App) dispatchOriginalSourceIntervalJobs(ctx context.Context, at time.Time, selected ...*originalDirectJob) {
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	if state.serviceAdmissionClosed {
		state.mu.Unlock()
		return
	}
	type scan struct {
		job *originalDirectJob
		ctx context.Context
	}
	jobs := []scan{}
	for _, job := range state.jobs {
		if len(selected) != 0 && selected[0] != job {
			continue
		}
		if job.definition.Trigger.Kind != "source_interval" || !job.clockRegistered || job.sourceClockFault != "" || job.sourceScanning || !job.sourceDuePresent || job.due.After(at) {
			continue
		}
		if job.executorUnavailable {
			job.clockRegistered = false
			job.sourceDuePresent = false
			a.log("ERROR", "plugins", "service_executor_unavailable", "服务执行器不存在，已移除时钟", "", "")
			continue
		}
		scanCtx, scanCancel := context.WithCancel(ctx)
		job.sourceScanCancel = scanCancel
		job.sourceScanning = true
		jobs = append(jobs, scan{job, scanCtx})
	}
	state.mu.Unlock()
	for _, item := range jobs {
		job := item.job
		state.mu.Lock()
		due, present, trigger := job.due, job.sourceDuePresent, job.trigger
		state.mu.Unlock()
		runTimes, err := originalCollectSourceInterval(item.ctx, trigger, due, at, present)
		key := originalServiceLookupKey(job)
		state.mu.Lock()
		current := state.jobs[key] == job && originalServiceActive(job)
		job.sourceScanCancel()
		job.sourceScanCancel = nil
		job.sourceScanning = false
		if !current || state.serviceAdmissionClosed || ctx.Err() != nil {
			state.mu.Unlock()
			continue
		}
		if err != nil {
			job.sourceClockFault = err.Error()
			job.lastError = err.Error()
			state.mu.Unlock()
			a.log("ERROR", "plugins", "source_interval_clock_paused", a.clean(err.Error()), "", "")
			continue
		}
		if len(runTimes) == 0 {
			state.mu.Unlock()
			continue
		}
		if !job.coalesceDisabled {
			runTimes = runTimes[len(runTimes)-1:]
		}
		maximum := job.maxInstances
		if maximum == nil {
			maximum = big.NewInt(1)
		}
		if big.NewInt(int64(state.clockInstances[job.timerID])).Cmp(maximum) >= 0 {
			a.log("WARN", "plugins", "service_max_instances", "服务时钟已达到最大并发实例数", "", "")
		} else if a.admitOriginalBackground() {
			state.serviceLifetimes.Add(1)
			if state.clockInstances == nil {
				state.clockInstances = map[string]int{}
			}
			state.clockInstances[job.timerID]++
			grace := time.Duration(job.definition.MisfireGraceMS) * time.Millisecond
			go func(scheduled []time.Time, unlimited bool) {
				defer a.wg.Done()
				defer state.serviceLifetimes.Done()
				defer func() {
					state.mu.Lock()
					state.clockInstances[job.timerID]--
					if state.clockInstances[job.timerID] == 0 {
						delete(state.clockInstances, job.timerID)
					}
					state.mu.Unlock()
				}()
				for _, when := range scheduled {
					if !unlimited && grace > 0 && time.Since(when) > grace {
						continue
					}
					_ = a.runOriginalAcceptedService(ctx, key.instance, key.service, true, true)
				}
			}(append([]time.Time(nil), runTimes...), job.misfireUnlimited)
		} else {
			state.mu.Unlock()
			continue
		}
		// APScheduler recomputes after executor submission, including another jitter
		// draw. Do not reuse the discarded collection lookahead or advance early.
		next, exists, nextErr := job.trigger.SourceNext(runTimes[len(runTimes)-1])
		if nextErr != nil {
			job.sourceClockFault = nextErr.Error()
			job.lastError = nextErr.Error()
		} else {
			job.due, job.sourceDuePresent = next, exists
			if !exists {
				job.clockRegistered = false
			}
		}
		state.mu.Unlock()
		if nextErr != nil {
			a.log("ERROR", "plugins", "source_interval_clock_paused", a.clean(nextErr.Error()), "", "")
		}
	}
}

// APS MemoryJobStore sorts datetime_to_utc_timestamp values, then full native
// job IDs. That helper is timegm(UTC tuple) + microsecond/1e6, not exact datetime
// ordering or datetime.timestamp's single rational rounding. Far-year microsecond
// instants may intentionally tie and must then use the full suffixed timer ID.
type originalDirectJobSnapshot struct {
	key       originalServiceKey
	job       *originalDirectJob
	present   bool
	timestamp float64
	nativeID  string
}

func originalOrderedDirectJobsLocked(jobs map[originalServiceKey]*originalDirectJob) []originalDirectJobSnapshot {
	out := make([]originalDirectJobSnapshot, 0, len(jobs))
	for key, job := range jobs {
		nativeID := job.timerID
		if nativeID == "" {
			nativeID = key.service
			if key.instance != "" {
				nativeID = key.instance + "_" + key.service
			}
		}
		present := originalDirectDuePresent(job)
		stamp := 0.0
		if present {
			seconds := float64(job.due.Unix())
			fraction := float64(job.due.Nanosecond()/1000) / 1000000
			stamp = float64(seconds + fraction)
		}
		out = append(out, originalDirectJobSnapshot{key, job, present, stamp, nativeID})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.present != b.present {
			return a.present
		}
		if a.timestamp != b.timestamp {
			return a.timestamp < b.timestamp
		}
		return a.nativeID < b.nativeID
	})
	return out
}

// One capacity-one event belongs to the existing owner. It coalesces deadline
// changes and carries no work item, grant, persistent state or new executor.
func (s *originalDirectJobs) notifyOriginalSourceDeadlineLocked() {
	if s.sourceWake == nil {
		s.sourceWake = make(chan struct{}, 1)
	}
	select {
	case s.sourceWake <- struct{}{}:
	default:
	}
}
func (a *App) originalSourceIntervalWait(at time.Time) time.Duration {
	delay := 100 * time.Millisecond
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.serviceAdmissionClosed {
		return delay
	}
	for _, job := range state.jobs {
		if job.definition.Trigger.Kind != "source_interval" || !job.clockRegistered || !job.sourceDuePresent || job.sourceClockFault != "" || job.sourceScanning || !originalServiceActive(job) {
			continue
		}
		if job.object != nil && job.object.stopping.Load() {
			continue
		}
		remaining := job.due.Sub(at)
		if remaining <= 0 {
			return time.Nanosecond
		} // Timer.Reset(0) is never required.
		if remaining < delay {
			delay = remaining
		}
	}
	return delay
}
