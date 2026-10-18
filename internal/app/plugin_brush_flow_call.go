package app

import "context"

// Core methods already carry the source task context through every Host call.
// Only this package's real deferred Host can install this unexported binding;
// worker JSON, URL parameters and public plugin callbacks cannot select a run.
type brushFlowCallKey struct{}

func (o *pluginBrushFlowOwner) call(ctx context.Context) *brushFlowCall {
	c, _ := ctx.Value(brushFlowCallKey{}).(*brushFlowCall)
	if c == nil || c.o != o || c.run.InstanceID != o.instance || c.run.Generation != o.generation || c.method == "" {
		return nil
	}
	return c
}
func (o *pluginBrushFlowOwner) callIOGuard(ctx context.Context) error {
	c := o.call(ctx)
	if c == nil {
		return brushFlowEvidenceError()
	}
	return c.ioGuard(ctx)
}
