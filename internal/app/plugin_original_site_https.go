package app

import "context"

// Only the exact native original-module callsites create this marker. It is
// not derived from user input and conveys no origin, credential or site lease.
type originalPluginSiteHTTPSKey struct{}

func originalPluginSiteContext(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, originalPluginSiteHTTPSKey{}, true)
}

func originalPluginSiteRequest(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	enabled, _ := ctx.Value(originalPluginSiteHTTPSKey{}).(bool)
	return enabled
}
