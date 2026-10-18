package seedhost

import (
	"context"
	"light-media/internal/download"
)

type nativeMutationNotStartedKey struct{}
type nativeMutationNotStartedProof struct {
	inv       Invocation
	effectSHA string
	cause     error
}

func nativeNotStartedContext(ctx context.Context, inv Invocation, e Effect, cause error) context.Context {
	return context.WithValue(ctx, nativeMutationNotStartedKey{}, &nativeMutationNotStartedProof{inv: inv, effectSHA: digest(e), cause: cause})
}

// NativeMutationNotStarted validates only a live proof constructed by the bound
// native call from a real Client error. Worker JSON and stored Effect fields do
// not create this context capability; the Core receipt preserves its result.
func NativeMutationNotStarted(ctx context.Context, inv Invocation, e Effect) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	p, ok := ctx.Value(nativeMutationNotStartedKey{}).(*nativeMutationNotStartedProof)
	return ok && p != nil && p.inv == inv && p.effectSHA == digest(e) && download.MutationNotStarted(p.cause)
}
