package iyuuautoseed

import (
	"context"
	"fmt"

	sdk "light-media/builtinplugins/internal/sdk"
)

func newHostAPITransport(ctx context.Context, caller sdk.HostCaller, token string, guard func(context.Context) error) (*APITransport, error) {
	// Workers intentionally do not inherit host proxy environment variables.
	// IyuuHelper passes proxies=None: use only the trusted host environment,
	// not the application's explicit site proxy settings.
	proxy, err := (sdk.Network{Host: caller}).ProxySettings(ctx)
	if err != nil {
		return nil, err
	}
	transport, err := NewAPITransport(TrustedAPIBinding{Token: token}, APITransportOptions{Guard: guard, Environment: proxy.Environment, TransientRetry: true})
	if err != nil {
		return nil, err
	}
	transport.onRetry = func(callCtx context.Context, event apiRetryEvent) {
		logSeedWarning(callCtx, fmt.Sprintf("IYUU传输暂时失败，%s将在%s后重试（尝试%d/2，类型%s）", event.Operation, event.Delay, event.Attempt, event.Kind))
	}
	return transport, nil
}
