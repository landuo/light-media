package durable

import (
	"context"
	"time"
)

// Acknowledgement identifies the exact idempotency key confirmed by a trusted
// host sink. Neither a worker-selected receiver nor a bare success bool is an
// acknowledgement. No actual notifications/events are sent by this package.
type Acknowledgement struct {
	ID        string
	Committed bool
}
type Sink func(context.Context, OutboxItem) (Acknowledgement, error)

func (j *StateJournal) DispatchOne(ctx context.Context, b Binding, sink Sink) (bool, error) {
	release, err := j.backend.Hold(ctx, b)
	if err != nil {
		return false, err
	}
	if release == nil {
		return false, ErrStore
	}
	defer release()
	if sink == nil {
		return false, ErrStore
	}
	var item OutboxItem
	found := false
	err = j.backend.Update(ctx, b, func(s *State) error {
		if err := check(s, b); err != nil {
			return err
		}
		for i := range s.Outbox {
			if s.Outbox[i].Phase == "uncertain" || s.Outbox[i].Phase == "intent" {
				return ErrUncertain
			}
			if s.Outbox[i].Phase == "pending" {
				s.Outbox[i].Phase = "intent"
				item = copyJSON(s.Outbox[i])
				found = true
				return nil
			}
		}
		return ErrNoChange
	})
	if err != nil || !found {
		return found, err
	}
	ack, deliveryErr := sink(ctx, item)
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	err = j.backend.Update(finishCtx, b, func(s *State) error {
		if err := check(s, b); err != nil {
			return err
		}
		for i := range s.Outbox {
			if s.Outbox[i].ID == item.ID {
				if s.Outbox[i].Phase != "intent" {
					return ErrConflict
				}
				if deliveryErr == nil && ack.ID == item.ID && ack.Committed {
					s.Outbox[i].Phase = "delivered"
				} else {
					s.Outbox[i].Phase = "uncertain"
				}
				return nil
			}
		}
		return ErrConflict
	})
	if err != nil {
		_ = j.markOutboxUnknown(finishCtx, b, item.ID)
		return true, ErrUncertain
	}
	if deliveryErr != nil || ack.ID != item.ID || !ack.Committed {
		return true, ErrUncertain
	}
	return true, nil
}

func (j *StateJournal) markOutboxUnknown(ctx context.Context, b Binding, id string) error {
	return j.backend.Update(ctx, b, func(s *State) error {
		if err := check(s, b); err != nil {
			return err
		}
		for i := range s.Outbox {
			if s.Outbox[i].ID != id {
				continue
			}
			switch s.Outbox[i].Phase {
			case "delivered", "uncertain":
				return ErrNoChange
			case "intent":
				s.Outbox[i].Phase = "uncertain"
				return nil
			default:
				return ErrConflict
			}
		}
		return ErrConflict
	})
}
func (j *StateJournal) ReconcileOutbox(ctx context.Context, b Binding, id string, ack Acknowledgement) error {
	return j.backend.Update(ctx, b, func(s *State) error {
		if err := check(s, b); err != nil {
			return err
		}
		if !ack.Committed || ack.ID != id {
			return ErrConflict
		}
		for i := range s.Outbox {
			if s.Outbox[i].ID == id && s.Outbox[i].Phase == "uncertain" {
				s.Outbox[i].Phase = "delivered"
				return nil
			}
		}
		return ErrConflict
	})
}
