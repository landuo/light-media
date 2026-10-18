package plugins

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// runnerBudget has a single monotonic remaining budget, including process
// start, handshake, all wire exchanges, stop and process exit. A stale timer
// callback may still run after Stop; its generation cannot cancel a resumed
// or paused session. Parent cancellation is never paused.
type runnerBudget struct {
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	timer     *time.Timer
	remaining time.Duration
	started   time.Time
	epoch     uint64
	paused    bool
	closed    bool
}

func runnerContext(parent context.Context, timeout time.Duration, excludeHost bool) (context.Context, context.CancelFunc, *runnerBudget) {
	if !excludeHost {
		ctx, cancel := context.WithTimeout(parent, timeout)
		return ctx, cancel, nil
	}
	ctx, cancel := context.WithCancel(parent)
	b := &runnerBudget{ctx: ctx, cancel: cancel, remaining: timeout}
	b.armLocked()
	return ctx, b.close, b
}

// The caller holds mu except during construction, before b is published.
func (b *runnerBudget) armLocked() {
	b.started = time.Now()
	b.epoch++
	epoch := b.epoch
	b.timer = time.AfterFunc(b.remaining, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.closed || b.paused || b.epoch != epoch {
			return
		}
		b.closed = true
		b.remaining = 0
		b.cancel()
	})
}

func (b *runnerBudget) pause() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.paused || b.ctx.Err() != nil {
		return false
	}
	b.remaining -= time.Since(b.started)
	b.epoch++
	b.timer.Stop()
	if b.remaining <= 0 {
		b.closed = true
		b.cancel()
		return false
	}
	b.paused = true
	return true
}

func (b *runnerBudget) resume() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || !b.paused {
		return
	}
	b.paused = false
	if b.ctx.Err() != nil {
		b.closed = true
		return
	}
	b.armLocked()
}

func (b *runnerBudget) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.epoch++
	b.timer.Stop()
	b.cancel()
}

func (s *wireSession) callHost(method string, payload json.RawMessage) (json.RawMessage, error) {
	if s.hostBudget != nil {
		if !s.hostBudget.pause() {
			return nil, failure("execution_canceled")
		}
		defer s.hostBudget.resume()
	}
	return s.host(s.ctx, s.identity, method, payload)
}
