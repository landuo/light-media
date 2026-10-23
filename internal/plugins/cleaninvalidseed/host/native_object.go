package seedhost

import (
	"context"
	seed "light-media/internal/plugins/cleaninvalidseed"
)

type nativeMachineKey struct{}

// WithMachine binds the literal source object to an already authorized native
// invocation. It grants no permissions; enter still performs every admission.
// The caller must serialize this pointer with the enclosing App owner gate.
func WithMachine(ctx context.Context, machine *seed.Machine) context.Context {
	return context.WithValue(ctx, nativeMachineKey{}, machine)
}
func nativeMachine(ctx context.Context) *seed.Machine {
	machine, _ := ctx.Value(nativeMachineKey{}).(*seed.Machine)
	return machine
}

// WithOriginalService records only the exact invocation already authenticated
// by the App's central original-services proof. It cannot authorize Core effects.
// enter still requires the normal fresh BeforeRun, Guard and journal admission.
type originalServiceKey struct{}

func WithOriginalService(ctx context.Context, inv Invocation) context.Context {
	return context.WithValue(ctx, originalServiceKey{}, inv)
}
func originalServiceMatches(ctx context.Context, inv Invocation) bool {
	bound, ok := ctx.Value(originalServiceKey{}).(Invocation)
	return ok && bound == inv && inv.Action == "service" && inv.EventID != "" && nativeMachine(ctx) != nil
}
