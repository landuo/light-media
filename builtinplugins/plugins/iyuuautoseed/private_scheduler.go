package iyuuautoseed

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	sdk "light-media/builtinplugins/internal/sdk"
)

const privateCheckInterval = 3 * time.Minute
const privateMisfireGrace = time.Second

type privateSchedule struct {
	generation        uint64
	onceDue, checkDue time.Time
	stop, done        chan struct{}
	mu                sync.Mutex
	cycle             uint64
	active            int
	pending, stopped  bool
	checkRunning      bool
}

func newPrivateSchedule(generation uint64) *privateSchedule {
	return &privateSchedule{generation: generation, stop: make(chan struct{}), done: make(chan struct{})}
}

func privateSchedulerLocation() (*time.Location, error) {
	zone, present := os.LookupEnv("TZ")
	if !present {
		zone = "Asia/Shanghai"
	}
	zone = strings.TrimFunc(zone, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
	if zone == "" || zone == "Local" {
		return nil, errors.New("invalid IYUU private scheduler timezone")
	}
	return time.LoadLocation(zone)
}

func (s *privateSchedule) releaseLocked() {
	s.active--
	if s.stopped && s.active == 0 {
		close(s.done)
	}
}

func (s *privateSchedule) cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.stopped = true
	close(s.stop)
	if s.pending {
		s.pending = false
		s.releaseLocked()
	} else if s.active == 0 {
		close(s.done)
	}
}

func (r *pluginRuntime) stop(ctx context.Context) error {
	r.lifetimeMu.Lock()
	r.core.Stop()
	active := r.active
	r.lifetimeMu.Unlock()

	if job := r.private; job != nil {
		job.cancel()
		select {
		case <-job.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if active != nil {
		select {
		case <-active:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.closeOnce.Do(func() {
		if r.transport != nil {
			r.transport.Close()
		}
		if r.cache != nil {
			_ = r.cache.Close()
		}
	})
	return nil
}

type schedulerCall struct {
	Generation uint64 `json:"generation"`
	Cycle      uint64 `json:"cycle"`
}

func (s *privateSchedule) submit(ctx context.Context, caller sdk.HostCaller, cycle uint64) error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.cycle = cycle
	s.pending = true
	s.active++
	s.mu.Unlock()

	manager := sdk.Manager{Host: caller}
	payload := schedulerCall{Generation: s.generation, Cycle: cycle}
	submit := manager.Submit
	if cycle == 0 {
		submit = manager.SubmitPrivateScheduler
	}
	err := submit(ctx, "private_scheduler", payload)
	if err != nil {
		s.mu.Lock()
		if s.pending && s.cycle == cycle {
			s.pending = false
			s.releaseLocked()
		}
		stopped := s.stopped
		s.mu.Unlock()
		if stopped {
			return nil
		}
	}
	return err
}

func (p *Plugin) runPrivate(ctx context.Context, caller sdk.HostCaller, request schedulerCall) error {
	runtime := p.current()
	job := runtime.private
	if job == nil || job.generation != request.Generation {
		return nil
	}
	job.mu.Lock()
	if !job.pending || job.cycle != request.Cycle {
		job.mu.Unlock()
		return nil
	}
	job.pending = false
	due := job.checkDue
	if !job.onceDue.IsZero() && job.onceDue.Before(due) {
		due = job.onceDue
	}
	job.mu.Unlock()
	defer func() {
		job.mu.Lock()
		job.releaseLocked()
		job.mu.Unlock()
	}()

	timer := time.NewTimer(max(time.Duration(0), time.Until(due)))
	defer timer.Stop()
	select {
	case <-job.stop:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	now := p.now()
	var onceDue, checkDue time.Time
	job.mu.Lock()
	if job.stopped {
		job.mu.Unlock()
		return nil
	}
	if !job.onceDue.IsZero() && !now.Before(job.onceDue) {
		onceDue, job.onceDue = job.onceDue, time.Time{}
	}
	if !now.Before(job.checkDue) {
		latest := job.checkDue.Add((now.Sub(job.checkDue) / privateCheckInterval) * privateCheckInterval)
		job.checkDue = latest.Add(privateCheckInterval)
		if now.Sub(latest) <= privateMisfireGrace && !job.checkRunning {
			checkDue, job.checkRunning = latest, true
		}
	}
	job.mu.Unlock()
	if !checkDue.IsZero() {
		defer func() {
			job.mu.Lock()
			job.checkRunning = false
			job.mu.Unlock()
		}()
	}

	// An accepted child inherits private ownership and receives a new reverse
	// caller with a new host-call budget. Admit the next timer before this pass.
	if err := job.submit(ctx, caller, request.Cycle+1); err != nil {
		return err
	}
	run := func(recheck bool, at time.Time) {
		if !at.IsZero() {
			if err := p.run(ctx, runtime, caller, recheck, at); err != nil && !runtime.core.stopped.Load() {
				_ = (sdk.Logger{Host: caller}).Log(ctx, sdk.LogError, "IYUU 定时任务执行失败")
			}
		}
	}
	run(false, onceDue)
	run(true, checkDue)
	return nil
}
