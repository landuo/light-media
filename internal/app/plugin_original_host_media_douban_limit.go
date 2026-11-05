package app

import (
	"context"
	"errors"
	"sync"
	"time"
)

type originalHostDoubanLimit struct{ next, wait float64 }
type originalHostDoubanState struct {
	searchStamp   string
	mu            sync.Mutex
	match, detail originalHostDoubanLimit
	// Trusted test clock counterpart; no plugin or public configuration path.
	wait func(context.Context, time.Duration) error
}

// Fixed rate_limit_exponential instances belong to the two decorated methods,
// share the host lifetime, and do not survive a restart. Recognition calls omit
// raise_exception, so even douban_info's signature default True is overridden
// by the decorator's default False.
func (a *App) originalHostDoubanLimited(ctx context.Context, match bool, call func() (map[string]any, error)) (map[string]any, error) {
	s := &a.originalHostDouban
	limit := &s.detail
	if match {
		limit = &s.match
	}
	s.mu.Lock()
	allowed := a.originalHostMediaWallTime() >= limit.next
	s.mu.Unlock()
	if !allowed {
		return nil, nil
	}
	result, err := call()
	var api *APIError
	limited := errors.As(err, &api) && api.Code == "MEDIA_RATE_LIMITED"
	s.mu.Lock()
	if limited {
		if limit.wait == 0 {
			limit.wait = 60
		}
		limit.next = a.originalHostMediaWallTime() + limit.wait
		limit.wait *= 2
		if limit.wait > 600 {
			limit.wait = 600
		}
	} else if err == nil {
		limit.next = 0
		limit.wait = 60
	}
	s.mu.Unlock()
	if limited {
		return nil, nil
	}
	return result, err
}

func (a *App) originalHostDoubanRetry(ctx context.Context, call func() (map[string]any, error)) (map[string]any, error) {
	delay := 3 * time.Second
	for attempt := 0; ; attempt++ {
		result, err := a.originalHostDoubanLimited(ctx, true, call)
		if err == nil || attempt == 4 || ctx.Err() != nil {
			return result, err
		}
		if wait := a.originalHostDouban.wait; wait != nil {
			if err = wait(ctx, delay); err != nil {
				return nil, err
			}
		} else {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		delay *= 3
	}
}
