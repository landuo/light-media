package seedhost

import (
	"context"
	"encoding/json"
	"reflect"
)

// RuntimeConfig returns a copy of the warm source object's fields. It performs
// no effects and is not a worker capability. A cold owner returns nil so an
// ordinary init with {} on a fresh object does not inherit a prior live object.
func (o *Owner) RuntimeConfig(ctx context.Context) (json.RawMessage, error) {
	select {
	case o.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-o.gate }()
	return append(json.RawMessage(nil), o.runtimeConfig...), nil
}
func sameControlBackend(a, b any) bool {
	return a != nil && b != nil && reflect.TypeOf(a).Comparable() && reflect.TypeOf(b).Comparable() && a == b
}

// Rebind refreshes trusted native clients and local roots without replacing the
// ordinary source object. The App must hold its outer publication gate; this
// method waits for the internal action gate before acquiring any Core lease.
// bind is control-only: its Guard cannot authorize file/native effects.
func (o *Owner) Rebind(ctx context.Context, inv Invocation, opts Options) error {
	select {
	case o.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-o.gate }()
	if inv.Binding.InstanceID != o.opts.Binding.InstanceID || inv.Binding.Generation != o.opts.Binding.Generation || opts.Binding != inv.Binding || inv.RunID == "" || inv.InputSHA == "" || !sameControlBackend(opts.Core, o.opts.Core) || !sameControlBackend(opts.Journal, o.opts.Journal) {
		return ErrUnsafe
	}
	next, err := Open(opts)
	if err != nil {
		return err
	}
	state, err := o.opts.Journal.Read(ctx)
	if err != nil {
		return err
	}
	for _, run := range state.Runs {
		if run.Phase == "started" || run.Phase == "unknown" {
			return ErrUnknown
		}
	}
	for _, effect := range state.Effects {
		if effect.Phase == "started" || effect.Phase == "unknown" || effect.Phase == "fs_confirmed" {
			return ErrUnknown
		}
	}
	_, release, err := o.opts.Core.Guard(ctx, inv, Effect{Kind: "bind", Binding: inv.Binding, Authority: inv.Binding, RunID: inv.RunID})
	if err != nil {
		return err
	}
	if release == nil {
		return ErrUnsafe
	}
	defer release()
	o.opts = next.opts
	return nil
}
