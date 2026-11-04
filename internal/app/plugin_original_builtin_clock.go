package app

import (
	"context"
	"math/big"
	"time"

	"light-media/internal/plugins/schedule"
)

// Concrete implementation of the narrowly approved 2026-11-01 09:29:36 UTC
// bounded/cancellable trigger policy. These limits may reject an extremely
// large otherwise terminating tree/search; they are not source constructor
// limits. No callback failure, other job, manual operation or SDK deadline is
// folded into this clock-only policy.
const (
	originalBuiltinClockMaxNodes       = 1024
	originalBuiltinClockMaxDepth       = 32
	originalBuiltinClockMaxEvaluations = 65536
	originalBuiltinClockMaxDue         = 4096
	originalBuiltinClockTimeout        = 250 * time.Millisecond
)

func originalBuiltinClockLimits() schedule.BuiltinLimits {
	return schedule.BuiltinLimits{MaxNodes: originalBuiltinClockMaxNodes, MaxDepth: originalBuiltinClockMaxDepth, MaxEvaluations: originalBuiltinClockMaxEvaluations}
}

type originalBuiltinClockTurn struct {
	evaluation *schedule.BuiltinEvaluation
	cancel     context.CancelFunc
	collected  int
}

func originalBeginBuiltinClock(parent context.Context, trigger *schedule.BuiltinTrigger) (*originalBuiltinClockTurn, error) {
	ctx, cancel := context.WithTimeout(parent, originalBuiltinClockTimeout)
	evaluation, err := trigger.NewEvaluation(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	return &originalBuiltinClockTurn{evaluation: evaluation, cancel: cancel}, nil
}
func (turn *originalBuiltinClockTurn) close() { turn.cancel() }
func (turn *originalBuiltinClockTurn) collectDue() error {
	if turn.collected >= originalBuiltinClockMaxDue {
		return &schedule.BuiltinBoundaryError{Code: "due_collection_budget"}
	}
	turn.collected++
	return nil
}

// Presence belongs to the new optional evaluator, never to time.Time.IsZero.
// Legacy/common clocks retain their existing three-kind representation.
func originalJobHasDue(job *originalDirectJob) bool {
	if job.boundedClock {
		return job.duePresent
	}
	return !job.due.IsZero()
}
func originalSetBuiltinDue(job *originalDirectJob, value *time.Time) {
	job.duePresent = value != nil
	job.due = time.Time{}
	if value != nil {
		job.due = *value
	}
}
func (a *App) originalBuiltinClockFault(job *originalDirectJob, err error) {
	originalSetBuiltinDue(job, nil)
	job.clockRegistered = false
	job.clockError = err.Error()
	a.log("ERROR", "plugins", "builtin_trigger_error", job.clockError, "", originalServiceID(job))
}

// Caller holds jobs.mu. Callback admission/lifetime is the existing one; this
// branch only supplies original optional trigger computations and clock faults.
func (a *App) dispatchOriginalBuiltinJobLocked(ctx context.Context, job *originalDirectJob, at time.Time) {
	if !originalJobHasDue(job) || job.due.After(at) {
		return
	}
	if job.executorUnavailable {
		originalSetBuiltinDue(job, nil)
		job.clockRegistered = false
		a.log("ERROR", "plugins", "service_executor_unavailable", "服务执行器不存在，已移除时钟", "", "")
		return
	}
	turn, err := originalBeginBuiltinClock(ctx, job.builtinClock)
	if err != nil {
		a.originalBuiltinClockFault(job, err)
		return
	}
	defer turn.close()
	runTimes := []time.Time{}
	for originalJobHasDue(job) && !job.due.After(at) {
		if err = turn.collectDue(); err != nil {
			a.originalBuiltinClockFault(job, err)
			return
		}
		due := job.due
		if job.coalesceDisabled || len(runTimes) == 0 {
			runTimes = append(runTimes, due)
		} else {
			runTimes[0] = due
		}
		var next *time.Time
		next, err = turn.evaluation.Next(&due, at)
		if err != nil {
			a.originalBuiltinClockFault(job, err)
			return
		}
		originalSetBuiltinDue(job, next)
	}
	// Collection exceptions precede any submission. The separate second Next
	// below is AFTER submission and must never revoke an admitted callback.
	advance := func() {
		next, e := turn.evaluation.Next(&runTimes[len(runTimes)-1], at)
		if e != nil {
			a.originalBuiltinClockFault(job, e)
			return
		}
		originalSetBuiltinDue(job, next)
		if next == nil {
			job.clockRegistered = false
		}
	}
	if !originalServiceActive(job) {
		advance()
		return
	}
	state := &a.pluginOriginalDirectJobs
	maximum := job.maxInstances
	if maximum == nil {
		maximum = big.NewInt(1)
	}
	if big.NewInt(int64(state.clockInstances[job.timerID])).Cmp(maximum) >= 0 {
		a.log("WARN", "plugins", "service_max_instances", "服务时钟已达到最大并发实例数", "", "")
		advance()
		return
	}
	if !a.admitOriginalBackground() {
		advance()
		return
	}
	if state.clockInstances == nil {
		state.clockInstances = map[string]int{}
	}
	state.clockInstances[job.timerID]++
	key := originalServiceLookupKey(job)
	grace := time.Duration(job.definition.MisfireGraceMS) * time.Millisecond
	go func(scheduled []time.Time) {
		defer a.wg.Done()
		defer func() {
			state.mu.Lock()
			state.clockInstances[job.timerID]--
			if state.clockInstances[job.timerID] == 0 {
				delete(state.clockInstances, job.timerID)
			}
			state.mu.Unlock()
		}()
		for _, due := range scheduled {
			if !job.misfireUnlimited && grace > 0 && time.Since(due) > grace {
				continue
			}
			_ = a.runOriginalService(ctx, key.instance, key.service, true)
		}
	}(runTimes)
	advance()
}
