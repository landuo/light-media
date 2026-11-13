package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

type cachedReadResult struct {
	at             time.Time
	data           json.RawMessage
	err            error
	leaderCanceled bool
}
type cachedReadFlight struct {
	done   chan struct{}
	result cachedReadResult
}
type cachedReadKey struct {
	key string
	ttl time.Duration
}
type cachedReadFlights struct {
	mu     sync.Mutex
	active map[cachedReadKey]*cachedReadFlight
}

// The leader uses its own request context; no detached upstream work survives
// the caller. Other callers can abandon their wait independently. If the leader
// is canceled, live waiters retry with their own fetch/context rather than
// inheriting its cancellation. Serialized results never share mutable models.
func (a *App) cachedRead(ctx context.Context, key string, ttl time.Duration, fetch func() (any, error)) cachedReadResult {
	for {
		if err := ctx.Err(); err != nil {
			return cachedReadResult{err: err}
		}
		m := &a.cacheReads
		flightKey := cachedReadKey{key: key, ttl: ttl}
		m.mu.Lock()
		if m.active == nil {
			m.active = make(map[cachedReadKey]*cachedReadFlight)
		}
		if running := m.active[flightKey]; running != nil {
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return cachedReadResult{err: ctx.Err()}
			case <-running.done:
				if err := ctx.Err(); err != nil {
					return cachedReadResult{err: err}
				}
				if running.result.leaderCanceled {
					continue
				}
				return running.result
			}
		}
		flight := &cachedReadFlight{done: make(chan struct{}), result: cachedReadResult{err: errors.New("cache fetch interrupted")}}
		m.active[flightKey] = flight
		m.mu.Unlock()
		return func() cachedReadResult {
			defer func() {
				m.mu.Lock()
				delete(m.active, flightKey)
				close(flight.done)
				m.mu.Unlock()
			}()
			flight.result = a.fetchCachedRead(ctx, key, ttl, fetch)
			flight.result.leaderCanceled = flight.result.err != nil && ctx.Err() != nil
			return flight.result
		}()
	}
}

func (a *App) fetchCachedRead(ctx context.Context, key string, ttl time.Duration, fetch func() (any, error)) cachedReadResult {
	var cached struct {
		At   time.Time       `json:"at"`
		Data json.RawMessage `json:"data"`
	}
	err := a.Store.Get(ctx, "cache", key, &cached)
	exists := err == nil
	if exists && time.Since(cached.At) < ttl {
		return cachedReadResult{at: cached.At, data: cached.Data}
	}
	if err := ctx.Err(); err != nil {
		return cachedReadResult{err: err}
	}
	value, err := fetch()
	if err != nil {
		// A canceled caller must not dictate a stale fallback to still-live waiters.
		if ctx.Err() != nil {
			return cachedReadResult{err: ctx.Err()}
		}
		if exists {
			a.log("WARN", "catalog", "cache_stale", "元数据服务不可用，使用历史缓存", "", "")
			return cachedReadResult{at: cached.At, data: cached.Data}
		}
		return cachedReadResult{err: err}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return cachedReadResult{err: err}
	}
	cached.At, cached.Data = time.Now().UTC(), data
	if err = a.Store.Put(ctx, "cache", key, cached); err != nil {
		return cachedReadResult{err: err}
	}
	return cachedReadResult{at: cached.At, data: data}
}
