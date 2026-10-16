package cleanuphost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins/removelink"
)

func publish(t *testing.T, f *fixture, ticket TransitionTicket, to Binding, run string) {
	t.Helper()
	if err := f.j.Update(context.Background(), func(s *Durable) error { return PublishTransition(s, ticket, to, run) }); err != nil {
		t.Fatal(err)
	}
}
func begin(t *testing.T, h *Host, from Binding, id, kind string) TransitionTicket {
	t.Helper()
	ticket, err := h.BeginTransition(context.Background(), from, id, kind)
	if err != nil {
		t.Fatal(err)
	}
	return ticket
}
func delayedFixture(t *testing.T) (*fixture, *Host, string, string) {
	t.Helper()
	f := newFixture(t)
	a, b := f.links(t)
	h := f.open(t)
	if err := h.Configure(context.Background(), f.opts.Binding, f.config(true, nil)); err != nil {
		t.Fatal(err)
	}
	f.remove(t, h, a, "queue-before-transition")
	return f, h, a, b
}

func TestTransitionCommitPausesOldTimerAndOrdinaryInitUsesNewFlags(t *testing.T) {
	f, h, _, b := delayedFixture(t)
	ctx := context.Background()
	before := snapshot(t, h)
	ticket := begin(t, h, f.opts.Binding, "save-new-flags", "init")
	to := f.opts.Binding
	to.Revision++
	publish(t, f, ticket, to, "actual-init-run")
	f.clock.Add(int64(31 * time.Second))
	if err := h.Tick(ctx); !errors.Is(err, ErrTransition) {
		t.Fatal("old timer ran across publication", err)
	}
	if err := h.Configure(ctx, to, f.config(false, nil)); !errors.Is(err, ErrTransition) {
		t.Fatal("unguarded Configure bypassed transition", err)
	}
	if !exists(b) || snapshot(t, h).Blocked != "" {
		t.Fatal("normal publication ran or permanently blocked old work")
	}
	if snapshot(t, h).Machine.TimerDue == nil || !snapshot(t, h).Machine.TimerDue.Equal(*before.Machine.TimerDue) {
		t.Fatal("pause reset queue deadline")
	}
	if err := h.ApplyTransition(ctx, ticket, "wrong-run", f.config(false, nil)); !errors.Is(err, ErrUnsafe) {
		t.Fatal("unbound run applied", err)
	}
	cfg := f.config(false, map[string]any{"delete_history": false, "delete_torrents": false, "notify": false})
	if err := h.ApplyTransition(ctx, ticket, "actual-init-run", cfg); err != nil {
		t.Fatal(err)
	}
	after := snapshot(t, h)
	if exists(b) || len(after.Machine.Queue) != 0 || after.Binding != to || after.Transition.Phase != "applied" {
		t.Fatal("new init did not finish selected work")
	}
	if f.effects.count("history_src") != 0 || f.effects.count("download_file_deleted") != 0 || f.effects.count("notification") != 0 {
		t.Fatal("ordinary init drained with old flags")
	}
	r := after.Effects["lifecycle:actual-init-run"]
	if r.Kind != "lifecycle_applied" || r.Binding != to || r.Scope != ticket.ID || r.State != "confirmed" {
		t.Fatal("missing atomic lifecycle receipt")
	}
	if err := h.ApplyTransition(ctx, ticket, "actual-init-run", cfg); err != nil {
		t.Fatal("committed reply retry", err)
	}
}

func TestTransitionCASFailureAndUnknownPublicationNeverGuessRollback(t *testing.T) {
	for _, mode := range []string{"rejected", "committed_reply_lost", "read_unknown"} {
		t.Run(mode, func(t *testing.T) {
			f, h, _, b := delayedFixture(t)
			ctx := context.Background()
			before := snapshot(t, h)
			ticket := begin(t, h, f.opts.Binding, "save-"+mode, "init")
			to := f.opts.Binding
			to.Revision++
			switch mode {
			case "rejected":
				f.j.beforeCommit = func() error { return errors.New("config/run CAS rejected") }
				err := f.j.Update(ctx, func(s *Durable) error { return PublishTransition(s, ticket, to, "not-committed-run") })
				f.j.beforeCommit = nil
				if err == nil {
					t.Fatal("synthetic failed CAS committed")
				}
				if err = h.AbortTransition(ctx, ticket); err != nil {
					t.Fatal(err)
				}
				state := snapshot(t, h)
				if state.Transition != nil || state.Binding != f.opts.Binding || !state.Machine.TimerDue.Equal(*before.Machine.TimerDue) {
					t.Fatal("abort changed old state/deadline")
				}
				f.clock.Add(int64(31 * time.Second))
				if err = h.Tick(ctx); err != nil {
					t.Fatal(err)
				}
				if exists(b) || f.effects.count("history_src") != 2 {
					t.Fatal("old timer did not resume")
				}
			case "committed_reply_lost":
				publish(t, f, ticket, to, "committed-run") // client reports unknown after this commit
				if err := h.AbortTransition(ctx, ticket); !errors.Is(err, ErrUnsafe) {
					t.Fatal("published outcome rolled back", err)
				}
				f.clock.Add(int64(31 * time.Second))
				if err := h.Tick(ctx); !errors.Is(err, ErrTransition) {
					t.Fatal(err)
				}
				if !exists(b) || snapshot(t, h).Transition.Phase != "published" {
					t.Fatal("commit uncertainty resumed old behavior")
				}
				if err := h.ApplyTransition(ctx, ticket, "committed-run", f.config(false, nil)); err != nil {
					t.Fatal(err)
				}
			case "read_unknown":
				j := &uncertainReadJournal{Journal: f.j}
				h.opts.Journal = j
				j.fail.Store(true)
				if err := h.AbortTransition(ctx, ticket); !errors.Is(err, ErrUncertain) {
					t.Fatal("unknown read resumed", err)
				}
				j.fail.Store(false)
				if !exists(b) || snapshot(t, h).Transition.Phase != "prepared" {
					t.Fatal("unknown outcome cleared pause")
				}
				if err := h.AbortTransition(ctx, ticket); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

type uncertainReadJournal struct {
	Journal
	fail atomic.Bool
}

func (j *uncertainReadJournal) Read(ctx context.Context) (Durable, error) {
	if j.fail.Load() {
		return Durable{}, ErrUncertain
	}
	return j.Journal.Read(ctx)
}

func TestTransitionLiveCapturedEventsCheckedAndDrainedWithNewFlags(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	ctx := context.Background()
	var watcher *idleWatcher
	f.opts.Watch = func(ctx context.Context, specs []removelink.Watch, open func(string) (*os.File, error)) (Watcher, error) {
		w, err := idleWatch(ctx, specs, open)
		watcher = w.(*idleWatcher)
		return w, err
	}
	h := f.open(t)
	if err := h.Configure(ctx, f.opts.Binding, f.config(false, nil)); err != nil {
		t.Fatal(err)
	}
	w := watcher
	ticket := begin(t, h, f.opts.Binding, "capture-save", "init")
	to := f.opts.Binding
	to.Revision++
	publish(t, f, ticket, to, "capture-run")
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	w.events <- Event{ID: "paused-native", Kind: "deleted", Path: a, Mode: "hardlink", At: f.opts.Now()}
	deadline := time.Now().Add(time.Second)
	for snapshot(t, h).Events["paused-native"].State != "pending" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	s := snapshot(t, h)
	if s.Events["paused-native"].Binding != ticket.From || s.Events["paused-native"].State != "pending" || !exists(b) || f.effects.count("history_src") != 0 {
		t.Fatal("paused capture performed effects or lost binding")
	}
	if err := h.ApplyTransition(ctx, ticket, "capture-run", f.config(false, map[string]any{"delete_history": false, "delete_torrents": false, "notify": false})); err != nil {
		t.Fatal(err)
	}
	s = snapshot(t, h)
	if exists(b) || s.Events["paused-native"].State != "confirmed" || f.effects.count("history_src") != 0 {
		t.Fatal("captured event not handled under new flags")
	}
}

func TestTransitionDoesNotBlessFabricatedOrRestoredPendingState(t *testing.T) {
	for _, mode := range []string{"pending", "planned", "active", "blocked", "wrong_binding", "duplicate_sequence"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			a, b := f.links(t)
			h := f.open(t)
			ctx := context.Background()
			if err := h.Configure(ctx, f.opts.Binding, f.config(false, nil)); err != nil {
				t.Fatal(err)
			}
			e := Event{ID: "unknown-pending", Kind: "deleted", Path: a, Mode: "hardlink", At: f.opts.Now()}
			if mode == "duplicate_sequence" {
				if err := h.RecordEvent(ctx, e); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.j.Update(ctx, func(s *Durable) error {
				switch mode {
				case "active":
					s.Active = &e
				case "blocked":
					s.Blocked = "prior uncertain unlink"
				case "wrong_binding":
					s.Binding.Revision++
				case "duplicate_sequence":
					r := s.Events[e.ID]
					r.Event.ID = "forged-second"
					s.Events[r.Event.ID] = r
				default:
					s.EventSerial++
					s.Events[e.ID] = EventReceipt{Event: e, Binding: f.opts.Binding, Sequence: s.EventSerial, State: mode}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := h.BeginTransition(ctx, f.opts.Binding, "no-blessing", "init"); err == nil {
				t.Fatal("fabricated state was blessed")
			}
			if mode == "pending" {
				if err := h.RecordEvent(ctx, e); !errors.Is(err, ErrUncertain) {
					t.Fatal("record blessed unknown pending", err)
				}
			}
			if !exists(b) || f.effects.count("history_src") != 0 {
				t.Fatal("unverified old event caused side effects")
			}
			s, _ := f.j.Read(ctx)
			if mode == "active" && s.Active == nil || mode == "blocked" && s.Blocked == "" {
				t.Fatal("old uncertainty was cleared")
			}
		})
	}
}

func TestTransitionExplicitReloadStopsOldFlagsThenStartsFreshObject(t *testing.T) {
	for _, kind := range []string{"init", "reload"} {
		t.Run(kind, func(t *testing.T) {
			f, h, _, b := delayedFixture(t)
			ctx := context.Background()
			ticket := begin(t, h, f.opts.Binding, "different-"+kind, kind)
			to := f.opts.Binding
			if kind == "init" {
				to.Revision++
			}
			publish(t, f, ticket, to, "run-"+kind)
			cfg := f.config(false, map[string]any{"delete_history": false, "delete_torrents": false, "notify": false})
			if err := h.ApplyTransition(ctx, ticket, "run-"+kind, cfg); err != nil {
				t.Fatal(err)
			}
			if exists(b) {
				t.Fatal("old queue was not stopped")
			}
			want := 0
			if kind == "reload" {
				want = 2
			}
			if f.effects.count("history_src") != want {
				t.Fatal("platform reload and ordinary init flags were conflated", f.effects.count("history_src"), want)
			}
		})
	}
}

func TestTransitionFalseyInitPersistsEffectiveConfigButReloadUsesDefaults(t *testing.T) {
	for _, kind := range []string{"init", "reload"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			a, b := f.links(t)
			h := f.open(t)
			ctx := context.Background()
			old := f.config(false, nil)
			if err := h.Configure(ctx, f.opts.Binding, old); err != nil {
				t.Fatal(err)
			}
			ticket := begin(t, h, f.opts.Binding, "falsey-"+kind, kind)
			to := f.opts.Binding
			if kind == "init" {
				to.Revision++
			}
			publish(t, f, ticket, to, "falsey-run-"+kind)
			if err := h.ApplyTransition(ctx, ticket, "falsey-run-"+kind, json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			state := snapshot(t, h)
			if kind == "init" && !strings.Contains(string(state.Config), `"enabled":true`) {
				t.Fatal("ordinary falsey init lost effective config")
			}
			if kind == "reload" && string(state.Config) != "{}" {
				t.Fatal("reload did not start fresh defaults")
			}
			if err := h.Close(ctx); err != nil {
				t.Fatal(err)
			}
			reopened := f.open(t)
			if err := reopened.Recover(ctx, to); err != nil {
				t.Fatal(err)
			}
			state = snapshot(t, reopened)
			if kind == "init" {
				if err := os.Remove(a); err != nil {
					t.Fatal(err)
				}
				if err := reopened.Observe(ctx, Event{ID: "after-effective-config-restart", Kind: "deleted", Path: a, Mode: "hardlink", At: f.opts.Now()}); err != nil {
					t.Fatal(err)
				}
				if exists(b) {
					t.Fatal("effective config did not survive restart")
				}
			} else if len(state.Machine.Watches) != 0 {
				t.Fatal("falsey reload was enabled after recovery")
			}
		})
	}
}

func TestTransitionRetirementDrainBeforeGenerationChangeAndAbortAfterCASFailure(t *testing.T) {
	for _, kind := range []string{"reset", "uninstall"} {
		for _, outcome := range []string{"commit", "cas_failed"} {
			t.Run(kind+"/"+outcome, func(t *testing.T) {
				f, h, _, b := delayedFixture(t)
				ctx := context.Background()
				ticket := begin(t, h, f.opts.Binding, "retire-"+kind, kind)
				to := f.opts.Binding
				to.Generation++
				to.Revision = 0
				if err := f.j.Update(ctx, func(s *Durable) error { return PublishTransition(s, ticket, to, "") }); !errors.Is(err, ErrUnsafe) {
					t.Fatal("published before old stop", err)
				}
				if err := h.DrainTransition(ctx, ticket); err != nil {
					t.Fatal(err)
				}
				if exists(b) || snapshot(t, h).Transition.Phase != "drained" || f.effects.count("history_src") != 2 {
					t.Fatal("old queue was not immediately drained")
				}
				if outcome == "cas_failed" {
					if err := h.AbortTransition(ctx, ticket); err != nil {
						t.Fatal(err)
					}
					a := filepath.Join(f.src, "new.mkv")
					target := filepath.Join(f.media, "new.mkv")
					write(t, a, "new-after-failed-reset")
					if err := os.Link(a, target); err != nil {
						t.Fatal(err)
					}
					for _, p := range []string{a, target} {
						if err := h.Observe(ctx, Event{ID: "created:" + filepath.Base(filepath.Dir(p)), Kind: "created", Path: p, Mode: "hardlink", At: f.opts.Now()}); err != nil {
							t.Fatal(err)
						}
					}
					f.remove(t, h, a, "delete-after-failed-reset")
					f.clock.Add(int64(31 * time.Second))
					if err := h.Tick(ctx); err != nil {
						t.Fatal(err)
					}
					if exists(target) || f.effects.count("history_src") != 4 {
						t.Fatal("abort failed to restart monitor or replayed old effects")
					}
					return
				}
				publish(t, f, ticket, to, "")
				if err := h.ApplyTransition(ctx, ticket, "", nil); err != nil {
					t.Fatal(err)
				}
				s, err := f.j.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if s.Binding != ticket.From || s.Transition.To != to || s.Transition.Phase != "applied" || s.Effects["lifecycle:"+ticket.ID].Binding != ticket.From {
					t.Fatal("retirement crossed journal generation scope")
				}
				if err = h.Tick(ctx); !errors.Is(err, ErrClosed) {
					t.Fatal("retired owner stayed active", err)
				}
			})
		}
	}
}

type rejectLifecycleJournal struct {
	Journal
	fail atomic.Bool
}

func (j *rejectLifecycleJournal) Update(ctx context.Context, fn func(*Durable) error) error {
	return j.Journal.Update(ctx, func(s *Durable) error {
		if err := fn(s); err != nil {
			return err
		}
		if j.fail.Load() && s.Transition != nil && s.Transition.Phase == "applied" {
			return errors.New("synthetic final receipt transaction failure")
		}
		return nil
	})
}
func TestTransitionFinalReceiptFailurePreservesEffectsAndBlocksReplay(t *testing.T) {
	f, h, _, b := delayedFixture(t)
	ctx := context.Background()
	ticket := begin(t, h, f.opts.Binding, "final-sql-fault", "init")
	to := f.opts.Binding
	to.Revision++
	publish(t, f, ticket, to, "final-fault-run")
	j := &rejectLifecycleJournal{Journal: f.j}
	j.fail.Store(true)
	h.opts.Journal = j
	if err := h.ApplyTransition(ctx, ticket, "final-fault-run", f.config(false, nil)); err == nil {
		t.Fatal("final receipt fault reported success")
	}
	s, err := f.j.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if exists(b) || s.Effects["lifecycle:final-fault-run"].State == "confirmed" || s.Transition.Phase == "applied" || s.Blocked == "" {
		t.Fatal("partial transaction invented lifecycle success")
	}
	j.fail.Store(false)
	if err = h.ApplyTransition(ctx, ticket, "final-fault-run", f.config(false, nil)); !errors.Is(err, ErrUncertain) {
		t.Fatal("uncertain final save replayed init", err)
	}
	if f.effects.count("history_src") != 2 {
		t.Fatal("partial effects replayed")
	}
}

func TestTransitionPublishedRestartAndCapturedRestartHaveDifferentEvidence(t *testing.T) {
	for _, captured := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean_published", true: "unapplied_capture"}[captured], func(t *testing.T) {
			f, h, _, b := delayedFixture(t)
			ctx := context.Background()
			ticket := begin(t, h, f.opts.Binding, "restart-publication", "init")
			to := f.opts.Binding
			to.Revision++
			publish(t, f, ticket, to, "restart-run")
			if captured {
				// A private native capture is legitimate in the old process, but no
				// in-memory proof may be assumed after abrupt loss of that process.
				if err := h.call(ctx, func() error {
					return h.captureTransitionEvent(ctx, Event{ID: "native-before-crash", Kind: "created", Path: filepath.Join(f.src, "unknown.mkv"), Mode: "hardlink", At: f.opts.Now()})
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := h.Suspend(ctx); err != nil {
				t.Fatal(err)
			}
			reopened := f.open(t)
			err := reopened.ApplyTransition(ctx, ticket, "restart-run", f.config(false, nil))
			if captured {
				if !errors.Is(err, ErrUncertain) || !exists(b) || f.effects.count("history_src") != 0 {
					t.Fatal("restored pending capture was blindly applied", err)
				}
				if err = reopened.AbortTransition(ctx, ticket); !errors.Is(err, ErrUncertain) {
					t.Fatal("abort cleared restored uncertainty", err)
				}
			} else if err != nil || exists(b) || snapshot(t, reopened).Transition.Phase != "applied" {
				t.Fatal("clean committed transition did not resume existing run", err)
			}
		})
	}
}

func TestTransitionRunAuthorizationAndPriorReceiptSurviveLaterSave(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h := f.open(t)
	if err := h.Configure(ctx, f.opts.Binding, f.config(false, nil)); err != nil {
		t.Fatal(err)
	}
	ticket := begin(t, h, f.opts.Binding, "authorized-save", "init")
	to := f.opts.Binding
	to.Revision++
	publish(t, f, ticket, to, "authorized-run")
	type invocationKey struct{}
	if err := h.call(ctx, func() error {
		h.opts.Guard = func(ctx context.Context, binding Binding, action string) (func(), error) {
			if action == "configure" && binding == to && ctx.Value(invocationKey{}) != "authorized-run" {
				return nil, ErrUnsafe
			}
			return func() {}, nil
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.ApplyTransition(ctx, ticket, "authorized-run", f.config(false, nil)); !errors.Is(err, ErrUnsafe) {
		t.Fatal("missing trusted invocation lease accepted", err)
	}
	authorized := context.WithValue(ctx, invocationKey{}, "authorized-run")
	if err := h.ApplyTransition(authorized, ticket, "authorized-run", f.config(false, nil)); err != nil {
		t.Fatal(err)
	}
	next := begin(t, h, to, "later-save", "init")
	newBinding := to
	newBinding.Revision++
	publish(t, f, next, newBinding, "later-run")
	s := snapshot(t, h)
	if s.Transition.ID != next.ID || s.Effects["lifecycle:authorized-run"].State != "confirmed" || s.Effects["lifecycle:authorized-run"].Binding != to {
		t.Fatal("later config overwrote earlier run evidence")
	}
	if err := h.ApplyTransition(ctx, ticket, "authorized-run", f.config(false, nil)); !errors.Is(err, ErrUnsafe) {
		t.Fatal("old ticket applied over new publication", err)
	}
	if err := h.Close(ctx); !errors.Is(err, ErrTransition) {
		t.Fatal("shutdown falsely completed a pending publication", err)
	}
	s, err := f.j.Read(ctx)
	if err != nil || s.Transition.Phase != "published" || s.Effects["lifecycle:later-run"].State == "confirmed" {
		t.Fatal("shutdown erased uncompleted run", err)
	}
}

func TestTransitionPublicationCannotReuseAnAppliedRun(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h := f.open(t)
	if err := h.Configure(ctx, f.opts.Binding, f.config(false, nil)); err != nil {
		t.Fatal(err)
	}
	first := begin(t, h, f.opts.Binding, "first-save", "init")
	to := f.opts.Binding
	to.Revision++
	publish(t, f, first, to, "already-applied-run")
	if err := h.ApplyTransition(ctx, first, "already-applied-run", f.config(false, nil)); err != nil {
		t.Fatal(err)
	}
	second := begin(t, h, to, "second-save", "init")
	next := to
	next.Revision++
	if err := f.j.Update(ctx, func(s *Durable) error { return PublishTransition(s, second, next, "already-applied-run") }); !errors.Is(err, ErrUnsafe) {
		t.Fatal("applied run rebound to a later config", err)
	}
	if s := snapshot(t, h); s.Transition.Phase != "prepared" || s.Effects["lifecycle:already-applied-run"].Scope != first.ID {
		t.Fatal("failed publication overwrote prior evidence")
	}
}

func TestTransitionAppliedReceiptRequiresMatchingIdentityAndOutcome(t *testing.T) {
	for _, field := range []string{"binding", "result", "id"} {
		t.Run(field, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			h := f.open(t)
			if err := h.Configure(ctx, f.opts.Binding, f.config(false, nil)); err != nil {
				t.Fatal(err)
			}
			ticket := begin(t, h, f.opts.Binding, "receipt-validation", "init")
			to := f.opts.Binding
			to.Revision++
			publish(t, f, ticket, to, "receipt-run")
			if err := h.ApplyTransition(ctx, ticket, "receipt-run", f.config(false, nil)); err != nil {
				t.Fatal(err)
			}
			if err := f.j.Update(ctx, func(s *Durable) error {
				r := s.Effects["lifecycle:receipt-run"]
				switch field {
				case "binding":
					r.Binding.Revision++
				case "result":
					r.Result = false
				case "id":
					r.ID = "other-run"
				}
				s.Effects["lifecycle:receipt-run"] = r
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := h.ApplyTransition(ctx, ticket, "receipt-run", f.config(false, nil)); !errors.Is(err, ErrUncertain) {
				t.Fatal("invalid receipt became success", err)
			}
		})
	}
}
