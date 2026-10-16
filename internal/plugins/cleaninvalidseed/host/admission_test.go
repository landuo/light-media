package seedhost

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestBeforeRunWaitsForActionGateAndKeepsLeaseThroughFinalization(t *testing.T) {
	d := newDaemon(t, "qbittorrent")
	f := newFixture(t, []Downloader{d.bind(t, "owned-qb")}, nil)
	f.config(t, map[string]any{"enabled": true})
	inv := f.run(t, "admitted-action", "run", "")
	var calls, releases atomic.Int32
	var active atomic.Bool
	admitted := make(chan struct{}, 1)
	base := f.o.opts
	base.BeforeRun = func(ctx context.Context, got Invocation) (Options, func(), error) {
		if got != inv {
			t.Error("admission received a different immutable invocation")
		}
		calls.Add(1)
		active.Store(true)
		admitted <- struct{}{}
		next := base
		next.Binding = got.Binding
		return next, func() { active.Store(false); releases.Add(1) }, nil
	}
	f.o.opts = base
	f.c.before = func(e Effect) {
		if !active.Load() {
			t.Error("Core action ran outside the actual action lease")
		}
	}
	f.o.gate <- struct{}{}
	done := make(chan error, 1)
	go func() { done <- f.o.Execute(context.Background(), inv) }()
	select {
	case <-admitted:
		t.Fatal("admission ran before the host acquired its action gate")
	case <-time.After(20 * time.Millisecond):
	}
	<-f.o.gate
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || releases.Load() != 1 || active.Load() {
		t.Fatalf("lease lifetime mismatch: admission=%d release=%d active=%v", calls.Load(), releases.Load(), active.Load())
	}
}

func TestBeforeRunReleasesOnEveryRejectedAdmissionAndFailedFinish(t *testing.T) {
	for _, mode := range []string{"callback_error", "foreign_backend", "missing_callback", "missing_release", "journal_unknown", "finalize_denied"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, nil, nil)
			f.config(t, map[string]any{"enabled": true})
			inv := f.run(t, "admission-"+mode, "run", "")
			if mode == "journal_unknown" {
				if err := f.j.Update(context.Background(), func(_ *sql.Tx, s *State) error {
					s.Runs["blocked"] = Run{Invocation: Invocation{Binding: inv.Binding, RunID: "blocked", Action: "run", InputSHA: "frozen"}, Effective: inv.Binding, Phase: "unknown"}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "finalize_denied" {
				f.c.before = func(e Effect) {
					if e.Kind == "finalize" {
						if _, err := f.db.Exec(`UPDATE core SET revision=revision+1`); err != nil {
							t.Error(err)
						}
					}
				}
			}
			var released atomic.Int32
			base := f.o.opts
			base.BeforeRun = func(ctx context.Context, got Invocation) (Options, func(), error) {
				next := base
				next.Binding = got.Binding
				release := func() { released.Add(1) }
				switch mode {
				case "callback_error":
					return Options{}, release, errors.New("synthetic admission failure")
				case "foreign_backend":
					next.Core = &fixtureCore{db: f.db}
				case "missing_callback":
					next.BeforeRun = nil
				case "missing_release":
					return next, nil, nil
				}
				return next, release, nil
			}
			f.o.opts = base
			if err := f.o.Execute(context.Background(), inv); err == nil {
				t.Fatal("rejected or unconfirmed action unexpectedly succeeded")
			}
			want := int32(1)
			if mode == "missing_release" {
				want = 0
			}
			if released.Load() != want || len(f.o.gate) != 0 {
				t.Fatalf("lease/gate leaked: release=%d gate=%d", released.Load(), len(f.o.gate))
			}
		})
	}
}

func TestTrustedManualServiceDoesNotUseCronMisfireGrace(t *testing.T) {
	d := newDaemon(t, "qbittorrent")
	f := newFixture(t, []Downloader{d.bind(t, "owned-qb")}, nil)
	f.config(t, map[string]any{"enabled": true})
	inv := f.run(t, "manual-source-service", "event", "manual-event")
	if err := f.j.Update(context.Background(), func(_ *sql.Tx, s *State) error {
		copyInv := inv
		s.Triggers[inv.EventID] = Trigger{ID: inv.EventID, Kind: "manual_service", Action: "clean_invalid_seed", Phase: "queued", Binding: inv.Binding, Due: f.now.Add(-24 * time.Hour), QueuedRunID: inv.RunID, Queued: &copyInv, OccurrenceID: "actual-private-manual-occurrence"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.o.Execute(context.Background(), inv); err != nil {
		t.Fatal("manual function incorrectly inherited cron grace", err)
	}
	s, err := f.j.Read(context.Background())
	if err != nil || s.Triggers[inv.EventID].Phase != "confirmed" {
		t.Fatal("manual event not confirmed", err)
	}
}
