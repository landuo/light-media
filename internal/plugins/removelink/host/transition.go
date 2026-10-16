package cleanuphost

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"light-media/internal/plugins/removelink"
)

// Transition references an existing App invocation. It is not a job or a worker
// DTO. App must publish this marker with its actual config/run SQL transaction.
type Transition struct {
	ID, Kind, Phase, RunID string
	From, To               Binding
	WasRunning             bool
}
type TransitionTicket struct {
	ID, Kind string
	From     Binding
}

func activeTransition(t *Transition) bool { return t != nil && t.Phase != "applied" }
func (t Transition) ticket() TransitionTicket {
	return TransitionTicket{ID: t.ID, Kind: t.Kind, From: t.From}
}
func validOperation(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	return strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._:-", r))
	}) < 0
}
func validKind(s string) bool {
	switch s {
	case "init", "reload", "reset", "uninstall", "shutdown":
		return true
	}
	return false
}
func matchTicket(t *Transition, ticket TransitionTicket) bool {
	return t != nil && t.ticket() == ticket
}

// PublishTransition is deliberately a pure in-transaction mutation. Calling it
// after config/run commit in another transaction leaves an unsafe stale window.
func PublishTransition(s *Durable, ticket TransitionTicket, to Binding, runID string) error {
	t := s.Transition
	if !matchTicket(t, ticket) || s.Binding != ticket.From || s.Blocked != "" || s.Active != nil {
		return ErrUnsafe
	}
	if to.InstanceID != t.From.InstanceID {
		return ErrUnsafe
	}
	if runID != "" {
		if _, used := s.Effects["lifecycle:"+runID]; used {
			return ErrUnsafe
		}
	}
	switch t.Kind {
	case "init":
		if t.Phase != "prepared" || to.Generation != t.From.Generation || to.Revision < t.From.Revision || !validOperation(runID) {
			return ErrUnsafe
		}
	case "reload":
		if t.Phase != "prepared" || to != t.From || !validOperation(runID) {
			return ErrUnsafe
		}
	case "reset", "uninstall":
		if t.Phase != "drained" || to.Generation != t.From.Generation+1 || to.Revision < 0 || runID != "" && !validOperation(runID) {
			return ErrUnsafe
		}
	default:
		return ErrUnsafe
	}
	t.To, t.RunID = to, runID
	if t.Phase == "drained" {
		t.Phase = "published_drained"
	} else {
		t.Phase = "published"
	}
	return nil
}

func (h *Host) readTransition(ctx context.Context, ticket TransitionTicket) (Durable, Transition, error) {
	s, err := h.opts.Journal.Read(ctx)
	if err != nil {
		return s, Transition{}, err
	}
	if h.blocked != "" || s.Blocked != "" || s.Active != nil {
		return s, Transition{}, ErrUncertain
	}
	if !matchTicket(s.Transition, ticket) {
		return s, Transition{}, ErrUnsafe
	}
	return s, *s.Transition, nil
}
func (h *Host) checkedCaptures(s Durable, from Binding) error {
	seen := map[uint64]bool{}
	for id, r := range s.Events {
		if r.State == "confirmed" {
			continue
		}
		if r.State != "pending" || !h.captures[id] {
			return ErrUncertain
		}
		if r.Binding != from || r.Event.ID != id || r.Sequence == 0 || r.Sequence > s.EventSerial || seen[r.Sequence] || !h.allowedEvent(r.Event) {
			return ErrUnsafe
		}
		seen[r.Sequence] = true
	}
	return nil
}

func (h *Host) BeginTransition(ctx context.Context, from Binding, opID, kind string) (TransitionTicket, error) {
	var ticket TransitionTicket
	err := h.call(ctx, func() error {
		var err error
		ticket, err = h.beginTransitionOwner(ctx, from, opID, kind)
		return err
	})
	return ticket, err
}
func (h *Host) beginTransitionOwner(ctx context.Context, from Binding, opID, kind string) (TransitionTicket, error) {
	ticket := TransitionTicket{ID: opID, Kind: kind, From: from}
	if from != h.binding || !validOperation(opID) || !validKind(kind) {
		return ticket, ErrUnsafe
	}
	if h.paused {
		return ticket, ErrTransition
	}
	if h.blocked != "" {
		return ticket, ErrUncertain
	}
	release, err := h.guard(ctx, "transition_begin:"+kind)
	if err != nil {
		return ticket, err
	}
	defer release()
	err = h.opts.Journal.Update(ctx, func(s *Durable) error {
		if s.Blocked != "" || s.Active != nil {
			return ErrUncertain
		}
		if activeTransition(s.Transition) {
			return ErrTransition
		}
		if s.Binding.InstanceID != "" && s.Binding != from {
			return ErrUnsafe
		}
		if err := h.checkedCaptures(*s, from); err != nil {
			return err
		}
		h.writeState(s, nil)
		s.Transition = &Transition{ID: opID, Kind: kind, From: from, Phase: "prepared", WasRunning: h.running}
		return nil
	})
	if err == nil {
		h.paused = true
	}
	return ticket, err
}

// captureTransitionEvent is invoked only by our existing native observer. It
// does no filesystem/core side effect and intentionally does not take Guard.
func (h *Host) captureTransitionEvent(ctx context.Context, e Event) error {
	if !h.paused || !h.allowedEvent(e) || !validOperation(e.ID) {
		return ErrUnsafe
	}
	err := h.opts.Journal.Update(ctx, func(s *Durable) error {
		if !activeTransition(s.Transition) || s.Transition.From != h.binding || s.Blocked != "" || s.Active != nil {
			return ErrUncertain
		}
		if old, exists := s.Events[e.ID]; exists {
			if !sameEvent(old.Event, e) || old.Binding != h.binding || old.State != "pending" || !h.captures[e.ID] {
				return ErrUnsafe
			}
			return nil
		}
		s.EventSerial++
		s.Events[e.ID] = EventReceipt{Event: e, Binding: h.binding, Sequence: s.EventSerial, State: "pending"}
		return nil
	})
	if err == nil {
		h.captures[e.ID] = true
	}
	return err
}

func (h *Host) finishLifecycle(ctx context.Context, t Transition) error {
	return h.opts.Journal.Update(ctx, func(s *Durable) error {
		if !matchTicket(s.Transition, t.ticket()) || s.Transition.Phase != t.Phase || s.Transition.RunID != t.RunID || s.Transition.To != t.To {
			return ErrUnsafe
		}
		h.writeState(s, nil)
		s.Transition.Phase = "applied"
		binding := t.To
		if t.Kind == "reset" || t.Kind == "uninstall" || t.Kind == "shutdown" {
			binding = t.From
		}
		id := t.RunID
		if id == "" {
			id = t.ID
		}
		if _, used := s.Effects["lifecycle:"+id]; used {
			return ErrUnsafe
		}
		payload, _ := json.Marshal(struct {
			Kind string
			To   Binding
		}{t.Kind, t.To})
		s.Effects["lifecycle:"+id] = Effect{ID: "lifecycle:" + id, Kind: "lifecycle_applied", Scope: t.ID, Binding: binding, State: "confirmed", Result: true, Payload: payload}
		return nil
	})
}

func (h *Host) DrainTransition(ctx context.Context, ticket TransitionTicket) error {
	return h.call(ctx, func() error { return h.drainTransitionOwner(ctx, ticket) })
}
func (h *Host) drainTransitionOwner(ctx context.Context, ticket TransitionTicket) error {
	s, t, err := h.readTransition(ctx, ticket)
	if err != nil {
		return err
	}
	if !h.paused || t.From != h.binding || t.Kind == "init" {
		return ErrUnsafe
	}
	if t.Phase == "drained" || t.Phase == "published_drained" {
		return nil
	}
	if t.Phase != "prepared" && t.Phase != "published" {
		return ErrUnsafe
	}
	if t.Kind == "reload" && t.Phase != "published" {
		return ErrUnsafe
	}
	if t.Kind != "reload" && t.Phase != "prepared" {
		return ErrUnsafe
	}
	if err = h.checkedCaptures(s, t.From); err != nil {
		return err
	}
	// stopOwner runs AfterAction under its Guard before this drained marker.
	// A failed core reconciliation must prevent publication/retirement.
	return h.stopOwner(ctx, "transition:"+ticket.ID, func(ctx context.Context) error {
		return h.opts.Journal.Update(ctx, func(s *Durable) error {
			if !matchTicket(s.Transition, ticket) || s.Transition.Phase != t.Phase {
				return ErrUnsafe
			}
			h.writeState(s, nil)
			if t.Phase == "published" {
				s.Transition.Phase = "published_drained"
			} else {
				s.Transition.Phase = "drained"
			}
			return nil
		})
	})
}

// runID and raw must come from the actual persisted invocation; App Guard must
// verify its running state and published To scope under its lifecycle lease.
func (h *Host) ApplyTransition(ctx context.Context, ticket TransitionTicket, runID string, raw json.RawMessage) error {
	return h.call(ctx, func() error {
		s, t, err := h.readTransition(ctx, ticket)
		if err != nil {
			return err
		}
		if runID != t.RunID {
			return ErrUnsafe
		}
		if t.Phase == "applied" {
			id := runID
			if id == "" {
				id = t.ID
			}
			r, ok := s.Effects["lifecycle:"+id]
			expected := t.To
			if t.Kind == "reset" || t.Kind == "uninstall" || t.Kind == "shutdown" {
				expected = t.From
			}
			if !ok || r.ID != "lifecycle:"+id || r.Kind != "lifecycle_applied" || r.Scope != t.ID || r.State != "confirmed" || !r.Result || r.Binding != expected {
				return ErrUncertain
			}
			release, err := h.guard(ctx, "transition_receipt")
			if err != nil {
				return err
			}
			release()
			return nil
		}
		if !h.paused || t.From != h.binding || t.Phase != "published" && t.Phase != "published_drained" {
			return ErrUnsafe
		}
		if err = h.checkedCaptures(s, t.From); err != nil {
			return err
		}
		switch t.Kind {
		case "init":
			if t.Phase != "published" {
				return ErrUnsafe
			}
		case "reload":
			if err = h.drainTransitionOwner(ctx, ticket); err != nil {
				return err
			}
			_, t, err = h.readTransition(ctx, ticket)
			if err != nil {
				return err
			}
			h.machine = removelink.New(h.ops)
			h.machine.Now = h.opts.Now
			h.ops.pins, h.ops.frozen = map[string]Pin{}, map[string]Pin{}
			h.config = json.RawMessage(`{}`)
		case "reset", "uninstall":
			if t.Phase != "published_drained" || t.To.Generation != t.From.Generation+1 {
				return ErrUnsafe
			}
			release, err := h.guard(ctx, "transition_retire:"+t.Kind)
			if err != nil {
				return err
			}
			defer release()
			if err = h.finishLifecycle(ctx, t); err != nil {
				return err
			}
			h.running = false
			h.closing = true
			return nil
		default:
			return ErrUnsafe
		}
		if err = h.configureOwner(ctx, t.To, raw, func(ctx context.Context) error { return h.finishLifecycle(ctx, t) }); err != nil {
			return err
		}
		h.paused = false
		return nil
	})
}

// Abort requires a fresh authoritative read. A published or unknown commit can
// never resume old behavior. App Guard must still verify the core is at From.
func (h *Host) AbortTransition(ctx context.Context, ticket TransitionTicket) error {
	var recoverBinding Binding
	var recoverWatch bool
	err := h.call(ctx, func() error {
		s, t, err := h.readTransition(ctx, ticket)
		if err != nil {
			return err
		}
		if !h.paused || t.From != h.binding || t.Phase != "prepared" && t.Phase != "drained" {
			return ErrUnsafe
		}
		if err = h.checkedCaptures(s, t.From); err != nil {
			return err
		}
		release, err := h.guard(ctx, "transition_abort")
		if err != nil {
			return err
		}
		defer release()
		if err = h.drainPending(ctx); err != nil {
			return err
		}
		err = h.opts.Journal.Update(ctx, func(s *Durable) error {
			if !matchTicket(s.Transition, ticket) || s.Transition.Phase != t.Phase {
				return ErrUnsafe
			}
			h.writeState(s, nil)
			s.Transition = nil
			return nil
		})
		if err != nil {
			return err
		}
		h.paused = false
		h.running = t.WasRunning && t.Phase != "drained"
		recoverWatch = t.WasRunning && t.Phase == "drained"
		recoverBinding = t.From
		return nil
	})
	if err == nil && recoverWatch {
		err = h.Recover(ctx, recoverBinding)
	}
	return err
}

func (h *Host) Close(ctx context.Context) error {
	return h.call(ctx, func() error {
		defer func() { _ = h.closeWatch(context.Background(), false); h.closing = true }()
		ticket, err := h.beginTransitionOwner(ctx, h.binding, "shutdown:"+digest([]any{h.binding, time.Now().UnixNano()}), "shutdown")
		if err != nil {
			return err
		}
		if err = h.drainTransitionOwner(ctx, ticket); err != nil {
			return err
		}
		_, t, err := h.readTransition(ctx, ticket)
		if err != nil {
			return err
		}
		t.To = t.From
		if err = h.opts.Journal.Update(ctx, func(s *Durable) error {
			if !matchTicket(s.Transition, ticket) || s.Transition.Phase != "drained" {
				return ErrUnsafe
			}
			s.Transition.To = t.To
			return nil
		}); err != nil {
			return err
		}
		return h.finishLifecycle(ctx, t)
	})
}
