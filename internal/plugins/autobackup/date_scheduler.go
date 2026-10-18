package autobackup

import (
	"context"
	"errors"
	"sync"
	"time"
)

// DateScheduler implements only the BackgroundScheduler surface consumed by
// init_plugin: a date job, default one-second misfire grace, print/start/stop.
// Durable occurrence ownership remains the formal App host's responsibility.
type DateScheduler struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	date    *time.Time
	running bool
	wait    sync.WaitGroup
	now     func() time.Time
	run     func(context.Context) error
	onError func(error)
}

func NewDateScheduler(now func() time.Time, run func(context.Context) error, onError func(error)) *DateScheduler {
	if now == nil {
		now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &DateScheduler{ctx: ctx, cancel: cancel, now: now, run: run, onError: onError}
}
func (s *DateScheduler) AddDate(_ context.Context, date time.Time, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running || s.date != nil {
		return errors.New("date job already registered")
	}
	s.date = &date
	return nil
}
func (s *DateScheduler) Jobs(context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.date == nil {
		return 0, nil
	}
	return 1, nil
}
func (s *DateScheduler) Print(context.Context) error { return nil }
func (s *DateScheduler) Running() bool               { s.mu.Lock(); defer s.mu.Unlock(); return s.running }
func (s *DateScheduler) Clear(context.Context) error {
	s.mu.Lock()
	s.date = nil
	s.cancel()
	s.mu.Unlock()
	return nil
}
func (s *DateScheduler) Start(context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("scheduler already running")
	}
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return errors.New("scheduler stopped")
	}
	s.running = true
	if s.date == nil {
		s.mu.Unlock()
		return nil
	}
	date := *s.date
	delay := date.Sub(s.now())
	if delay < 0 {
		delay = 0
	}
	s.wait.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wait.Done()
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-s.ctx.Done():
			return
		case <-timer.C:
		}
		s.mu.Lock()
		if s.date == nil || s.ctx.Err() != nil {
			s.mu.Unlock()
			return
		}
		s.date = nil
		expired := s.now().Sub(date) > time.Second
		s.mu.Unlock()
		if !expired && s.run != nil {
			if e := s.run(context.WithoutCancel(s.ctx)); e != nil && s.onError != nil {
				s.onError(e)
			}
		}
	}()
	return nil
}
func (s *DateScheduler) Shutdown(context.Context) error {
	s.cancel()
	s.wait.Wait()
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
	return nil
}
