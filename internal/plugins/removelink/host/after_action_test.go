package cleanuphost

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestAfterActionFailurePreservesSelectionAndBlocksEveryCompletion(t *testing.T) {
	for _, action := range []string{"event", "apply", "timer", "configure", "stop", "retirement_drain", "reload"} {
		t.Run(action, func(t *testing.T) {
			f := newFixture(t)
			source, target := f.links(t)
			var held, fail atomic.Bool
			var calls atomic.Int64
			guard := f.opts.Guard
			f.opts.Guard = func(ctx context.Context, b Binding, action string) (func(), error) {
				release, err := guard(ctx, b, action)
				if err != nil {
					return nil, err
				}
				if held.Swap(true) {
					t.Error("nested or concurrent Guard lease")
				}
				return func() { held.Store(false); release() }, nil
			}
			sentinel := errors.New("fixture core reconciliation failed")
			f.opts.AfterAction = func(_ context.Context, _ Binding, action string) error {
				if !held.Load() {
					t.Error("AfterAction ran after Guard release")
				}
				if action == "" {
					t.Error("missing enclosing action")
				}
				calls.Add(1)
				if fail.Load() {
					return sentinel
				}
				return nil
			}
			h := f.open(t)
			ctx := context.Background()
			delayed := action != "event" && action != "apply"
			if err := h.Configure(ctx, f.opts.Binding, f.config(delayed, nil)); err != nil {
				t.Fatal(err)
			}
			var event Event
			var ticket TransitionTicket
			if delayed {
				event = f.remove(t, h, source, "queued-before-core-failure")
				if !exists(target) {
					t.Fatal("fixture was not delayed")
				}
			} else {
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
				event = Event{ID: "selected-before-core-failure", Kind: "deleted", Path: source, Mode: "hardlink", At: f.opts.Now()}
			}
			if action == "apply" {
				if err := h.RecordEvent(ctx, event); err != nil {
					t.Fatal(err)
				}
			}
			if action == "retirement_drain" {
				ticket = begin(t, h, f.opts.Binding, "retire-with-core-failure", "uninstall")
			}
			if action == "reload" {
				ticket = begin(t, h, f.opts.Binding, "reload-with-core-failure", "reload")
				publish(t, f, ticket, f.opts.Binding, "real-reload-run")
			}
			fail.Store(true)
			var err error
			switch action {
			case "event":
				err = h.Observe(ctx, event)
			case "apply":
				err = h.Apply(ctx, f.opts.Binding, event.ID)
			case "timer":
				f.clock.Add(int64(31 * time.Second))
				err = h.Tick(ctx)
			case "configure":
				err = h.Configure(ctx, f.opts.Binding, f.config(true, nil))
			case "stop":
				err = h.Stop(ctx, "fixture-core-failure")
			case "retirement_drain":
				err = h.DrainTransition(ctx, ticket)
			case "reload":
				err = h.ApplyTransition(ctx, ticket, "real-reload-run", f.config(true, nil))
			}
			if err == nil {
				t.Fatal("failed AfterAction returned success")
			}
			s := snapshot(t, h)
			if s.Active == nil || s.Blocked == "" {
				t.Fatal("failed action lost its durable active selection", s.Active, s.Blocked)
			}
			if exists(target) {
				t.Fatal("fixture did not reach actual filesystem side effect")
			}
			confirmed := false
			for _, x := range s.Effects {
				if x.Kind == "unlink" && x.Path == target && x.State == "confirmed" {
					confirmed = true
				}
				if x.Kind == "lifecycle_applied" {
					t.Fatal("core failure published lifecycle success")
				}
			}
			if !confirmed {
				t.Fatal("actual file deletion receipt lost")
			}
			if action == "event" || action == "apply" {
				if s.Events[event.ID].State != "planned" {
					t.Fatal("failed core reconciliation confirmed event")
				}
			}
			if action == "retirement_drain" && s.Transition.Phase != "prepared" {
				t.Fatal("failed drain allowed retirement publication", s.Transition)
			}
			if action == "reload" && s.Transition.Phase != "published" {
				t.Fatal("failed reload drain marked complete", s.Transition)
			}
			before := calls.Load()
			if err := h.Tick(ctx); !errors.Is(err, ErrUncertain) && !errors.Is(err, ErrTransition) {
				t.Fatal("blocked action could automatically resume", err)
			}
			if calls.Load() != before {
				t.Fatal("blocked execution replayed AfterAction")
			}
			if err := h.Suspend(ctx); err != nil {
				t.Fatal(err)
			}
			reopened := f.open(t)
			if err := reopened.Recover(ctx, f.opts.Binding); !errors.Is(err, ErrUncertain) && !errors.Is(err, ErrTransition) {
				t.Fatal("restart replayed partially completed core action", err)
			}
		})
	}
}
