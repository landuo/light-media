package app

import (
	"context"
	"sync"

	"light-media/internal/subscriptionstats"
)

// Registration and closing share one lock: no new Add can race a Wait, and
// both inline API producers and their statistics workers finish before DB
// closure. Context cancellation precedes waiting and HTTP pool shutdown.
func (a *App) beginSubscriptionStatistics(parent context.Context) (context.Context, func(), error) {
	runtime := &a.subscriptionStats
	runtime.lifeMu.Lock()
	if runtime.closed {
		runtime.lifeMu.Unlock()
		return nil, nil, subscriptionstats.ErrDependency
	}
	if runtime.lifeContext == nil {
		runtime.lifeContext, runtime.lifeCancel = context.WithCancel(context.Background())
	}
	runtime.lifeWG.Add(1)
	lifetime := runtime.lifeContext
	runtime.lifeMu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(lifetime, cancel)
	var once sync.Once
	return ctx, func() { once.Do(func() { stop(); cancel(); runtime.lifeWG.Done() }) }, nil
}
func (a *App) stopSubscriptionStatistics() {
	runtime := &a.subscriptionStats
	runtime.lifeMu.Lock()
	runtime.closed = true
	if runtime.lifeCancel != nil {
		runtime.lifeCancel()
	}
	runtime.lifeMu.Unlock()
	runtime.lifeWG.Wait()
}
func (a *App) closeSubscriptionStatisticsHTTP() {
	runtime := &a.subscriptionStats
	runtime.closeHTTPOnce.Do(func() {
		runtime.lifeMu.Lock()
		closeHTTP := runtime.closeHTTP
		runtime.lifeMu.Unlock()
		if closeHTTP != nil {
			closeHTTP()
		}
	})
}
