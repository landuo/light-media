package apprise

import (
	"context"
	"sync"
	"time"
)

type providerThrottle struct {
	mu       sync.Mutex
	lastSend time.Time
}

func (t *providerThrottle) wait(ctx context.Context, interval time.Duration) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if delay := time.Until(t.lastSend.Add(interval)); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	t.lastSend = time.Now()
	return nil
}
