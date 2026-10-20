package app

import (
	"context"
	"net/url"
	"strings"

	"light-media/internal/plugins/iyuuautoseed/compat/urlprep"
	"light-media/internal/subscriptionstats"
)

type subscriptionStatisticsHTTPKey struct{}
type subscriptionStatisticsHTTPGrant struct {
	a               *App
	Host, Operation string
}

func (a *App) subscriptionStatisticsHTTPGuard(ctx context.Context, target *url.URL) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	scope, ok := ctx.Value(subscriptionStatisticsHTTPKey{}).(*subscriptionStatisticsHTTPGrant)
	if !ok || scope == nil || scope.a != a || scope.Operation != "add" {
		return subscriptionStatsEvidenceError()
	}
	prepared, e := urlprep.Prepare(strings.TrimRight(scope.Host, "/") + "/subscribe/" + scope.Operation)
	if e != nil {
		return subscriptionstats.ErrDependency
	}
	want, e := url.Parse(prepared)
	if e != nil || target.Scheme != want.Scheme || target.Host != want.Host {
		return subscriptionStatsEvidenceError()
	}
	return nil
}
func (a *App) executeUISubscriptionStatistics(ctx context.Context, in subscriptionStatsInput) (subscriptionstats.Result, error) {
	if e := ctx.Err(); e != nil {
		return subscriptionstats.Result{}, e
	}
	runtime := &a.subscriptionStats
	runtime.lifeMu.Lock()
	if runtime.closed {
		runtime.lifeMu.Unlock()
		return subscriptionstats.Result{}, subscriptionstats.ErrDependency
	}
	if runtime.asyncClient == nil {
		client := &subscriptionstats.AsyncClient{Base: runtime.base, Guard: a.subscriptionStatisticsHTTPGuard}
		runtime.asyncClient = client
		runtime.closeHTTP = client.Close
	}
	client := runtime.asyncClient
	runtime.lifeMu.Unlock()
	granted := context.WithValue(ctx, subscriptionStatisticsHTTPKey{}, &subscriptionStatisticsHTTPGrant{a: a, Host: in.Settings.ServerHost, Operation: "add"})
	// SubRegFields preserves original lazy share/proxy/URL/headers/body order.
	// The asynchronous source does not use requests' env routing or encoder.
	return client.SubRegFields(granted, in.Settings, in.Registration)
}
