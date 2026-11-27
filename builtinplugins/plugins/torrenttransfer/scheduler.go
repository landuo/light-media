package torrenttransfer

import (
	"context"
	"errors"
	"sync"
	"time"

	"light-media/builtinplugins/internal/sdk"
)

var errSchedulerStopped = errors.New("private scheduler stopped")

func waitSourceTimer(ctx context.Context, stop <-chan struct{}, due time.Time) error {
	timer := time.NewTimer(max(time.Duration(0), time.Until(due)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-stop:
		return errSchedulerStopped
	case <-timer.C:
		return nil
	}
}
func (p *Plugin) releaseScheduler(s *privateScheduler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s.active--
	if s.removed && s.active == 0 && !s.closed {
		s.closed = true
		close(s.done)
	}
}
func (p *Plugin) submitScheduler(ctx context.Context, host plugins.HostCaller, s *privateScheduler, cycle uint64) error {
	p.mu.Lock()
	if s.removed {
		p.mu.Unlock()
		return errSchedulerStopped
	}
	s.active++
	s.started = true
	s.reserved[cycle] = true
	p.mu.Unlock()
	e := (plugins.Manager{Host: host}).Submit(ctx, "private_scheduler", map[string]any{"generation": s.id, "cycle": cycle})
	if e != nil {
		p.mu.Lock()
		pending := s.reserved[cycle]
		delete(s.reserved, cycle)
		p.mu.Unlock()
		if pending {
			p.releaseScheduler(s)
		}
	}
	return e
}
func (p *Plugin) runScheduler(ctx context.Context, id uint64, host plugins.HostCaller) error {
	return p.runSchedulerPhase(ctx, id, 0, host)
}
func (p *Plugin) runSchedulerPhase(ctx context.Context, id, cycle uint64, host plugins.HostCaller) error {
	p.mu.Lock()
	s := p.scheduler
	if s == nil || s.id != id || !s.reserved[cycle] {
		p.mu.Unlock()
		return nil
	}
	delete(s.reserved, cycle)
	if s.removed {
		p.mu.Unlock()
		p.releaseScheduler(s)
		return nil
	}
	if !s.initialized {
		for _, j := range s.jobs {
			if j.Kind == "date" {
				s.onceDue = s.start.Add(j.Delay)
			}
			if j.Kind == "interval" {
				s.interval = j.Interval
				s.nextCheck = s.start.Add(j.Interval)
			}
		}
		s.initialized = true
	}
	due := s.nextCheck
	if !s.onceConsumed && !s.onceDue.IsZero() && (due.IsZero() || s.onceDue.Before(due)) {
		due = s.onceDue
	}
	p.mu.Unlock()
	defer p.releaseScheduler(s)
	if due.IsZero() {
		select {
		case <-s.stop:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	wait := p.waitUntil
	if wait == nil {
		wait = waitSourceTimer
	}
	if e := wait(ctx, s.stop, due); e != nil {
		if errors.Is(e, errSchedulerStopped) {
			return nil
		}
		return e
	}
	now := p.now()
	p.mu.Lock()
	if s.removed {
		p.mu.Unlock()
		return nil
	}
	runOnce, runCheck := false, false
	if !s.onceConsumed && !s.onceDue.IsZero() && !s.onceDue.After(now) {
		s.onceConsumed = true
		runOnce = now.Sub(s.onceDue) <= time.Second
	}
	if !s.nextCheck.IsZero() && !s.nextCheck.After(now) {
		latest := s.nextCheck.Add((now.Sub(s.nextCheck) / s.interval) * s.interval)
		s.nextCheck = latest.Add(s.interval)
		if now.Sub(latest) <= time.Second && !s.checkRunning {
			runCheck = true
			s.checkRunning = true
		}
	}
	hasNext := !s.nextCheck.IsZero() || !s.onceConsumed && !s.onceDue.IsZero()
	p.mu.Unlock()
	// Admit the next timer using a fresh callback before this phase's jobs run.
	// This phase remains alive until its own private jobs have actually joined.
	if hasNext {
		if e := p.submitScheduler(ctx, host, s, cycle+1); e != nil && !errors.Is(e, errSchedulerStopped) {
			return e
		}
	}
	var workers sync.WaitGroup
	run := func(method, action string) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if method == "check_recheck" {
				defer func() { p.mu.Lock(); s.checkRunning = false; p.mu.Unlock() }()
			}
			_, e := p.Handle(ctx, method, nil, host)
			if e != nil {
				_ = (plugins.Logger{Host: host}).Log(ctx, plugins.LogError, action+"失败："+e.Error())
			}
		}()
	}
	if runOnce {
		run("transfer", "定时转移种子")
	}
	if runCheck {
		run("check_recheck", "检查种子校验状态")
	}
	workers.Wait()
	// A date-only source scheduler remains running after consuming its job.
	if !hasNext {
		select {
		case <-s.stop:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
