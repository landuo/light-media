package app

import (
	"context"
	"sync"
	"time"
)

// Global download budget is retained, but a slow provider can occupy at most
// half of it. Acquire the provider quota first so its queue holds no global slot.
type catalogImageWork struct {
	mu        sync.Mutex
	providers map[string]chan struct{}
	flights   map[string]chan struct{}
	pruning   map[string]bool
	closed    bool
	stop      chan struct{}
	wg        sync.WaitGroup
}

func (a *App) acquireImageFlight(ctx context.Context, key string) (func(), error) {
	m := &a.imageWork
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, context.Canceled
		}
		if m.flights == nil {
			m.flights = make(map[string]chan struct{})
		}
		if done := m.flights[key]; done != nil {
			m.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		done := make(chan struct{})
		m.flights[key] = done
		m.mu.Unlock()
		var once sync.Once
		return func() { once.Do(func() { m.mu.Lock(); delete(m.flights, key); close(done); m.mu.Unlock() }) }, nil
	}
}
func (a *App) acquireImageSlot(ctx context.Context, provider string) (func(), error) {
	m := &a.imageWork
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, context.Canceled
	}
	if m.providers == nil {
		m.providers = make(map[string]chan struct{})
	}
	slots := m.providers[provider]
	if slots == nil {
		slots = make(chan struct{}, 2)
		m.providers[provider] = slots
	}
	m.mu.Unlock()
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case catalogImageSlots <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-catalogImageSlots; <-slots }) }, nil
	case <-ctx.Done():
		<-slots
		return nil, ctx.Err()
	}
}

// Coalesce writes into at most one scan per directory per second, off the
// response path. Limits and age eviction are unchanged; Close joins all scans.
func (a *App) scheduleCatalogImagePrune(dir string) {
	m := &a.imageWork
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	if m.pruning == nil {
		m.pruning = make(map[string]bool)
		m.stop = make(chan struct{})
	}
	if _, running := m.pruning[dir]; running {
		m.pruning[dir] = true
		m.mu.Unlock()
		return
	}
	m.pruning[dir] = true
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		for {
			timer := time.NewTimer(time.Second)
			select {
			case <-timer.C:
			case <-m.stop:
			}
			timer.Stop()
			m.mu.Lock()
			m.pruning[dir] = false
			m.mu.Unlock()
			a.pruneCatalogImages(dir)
			m.mu.Lock()
			if !m.pruning[dir] || m.closed {
				delete(m.pruning, dir)
				m.mu.Unlock()
				return
			}
			m.mu.Unlock()
		}
	}()
}
func (a *App) closeImageWork() {
	m := &a.imageWork
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		if m.stop != nil {
			close(m.stop)
		}
	}
	m.mu.Unlock()
	m.wg.Wait()
}
