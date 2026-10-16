package cleanuphost

import "context"

// ExpandRoots is a trusted owner operation, never a worker RPC. The caller must
// derive the complete ordered plan from current core authorization and the
// published configuration. Existing capabilities cannot be replaced or removed.
// Guard validates that exact plan and the persisted running invocation before
// this method publishes it; no file Effects lease is opened by this action.
func (h *Host) ExpandRoots(ctx context.Context, ticket TransitionTicket, runID string, roots []Root) error {
	plan := append([]Root(nil), roots...)
	return h.call(ctx, func() error {
		s, t, err := h.readTransition(ctx, ticket)
		if err != nil {
			return err
		}
		if !h.paused || t.From != h.binding || t.RunID != runID || !validOperation(runID) || (t.Kind != "init" && t.Kind != "reload") || (t.Phase != "published" && t.Phase != "published_drained") {
			return ErrUnsafe
		}
		if err = h.checkedCaptures(s, t.From); err != nil {
			return err
		}
		candidate, err := newLocal(h, plan)
		if err != nil {
			return err
		}
		if len(candidate.roots) < len(h.ops.roots) || !rootsEqual(s.Roots, h.ops.roots) {
			return ErrUnsafe
		}
		for n, old := range h.ops.roots {
			if candidate.roots[n].Root != old.Root || !sameEntity(candidate.roots[n].File, old.File) {
				return ErrUnsafe
			}
			// A live directory's mtime/size can change. Retain the actual sealed
			// capability rather than rewriting its original observation.
			candidate.roots[n] = old
		}
		release, err := h.guard(ctx, "transition_roots")
		if err != nil {
			return err
		}
		defer release()
		err = h.opts.Journal.Update(ctx, func(d *Durable) error {
			if !matchTicket(d.Transition, ticket) || d.Transition.RunID != runID || d.Transition.To != t.To || d.Transition.Phase != t.Phase || d.Active != nil || d.Blocked != "" || !rootsEqual(d.Roots, h.ops.roots) {
				return ErrUnsafe
			}
			d.Roots = append([]RootStamp(nil), candidate.roots...)
			return nil
		})
		if err != nil {
			h.blocked = "authority root publication requires reconciliation"
			return err
		} // May have committed: stay paused, no replay.
		h.ops.roots = candidate.roots
		h.opts.Roots = plan
		return nil
	})
}
