package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
)

// DRAFT ONLY: not compiled or executed in this preparation phase. These tests
// exercise memory ordering, not SQL/Runner/native lifecycle integration.
func supervisorDraftAdmission(instance, run string) pluginRunAdmission {
	return pluginRunAdmission{InstanceID: instance, PluginID: "Synthetic", Version: "1.0",
		RunID: run, Action: "task", Generation: 1, AdmissionRevision: 1,
		InputSHA: sha256.Sum256([]byte(`{"input":"original"}`))}
}

func supervisorDraftWindow(t *testing.T, s *pluginRunSupervisor) pluginRunClaimWindow {
	t.Helper()
	window, err := s.beginClaim()
	if err != nil {
		t.Fatal(err)
	}
	return window
}

func supervisorDraftReserve(t *testing.T, s *pluginRunSupervisor, identity pluginRunAdmission) *pluginRunTicket {
	t.Helper()
	ticket, err := s.reserve(context.Background(), supervisorDraftWindow(t, s), identity)
	if err != nil {
		t.Fatal(err)
	}
	return ticket
}

func supervisorDraftCommit(t *testing.T, ticket *pluginRunTicket) {
	t.Helper()
	if err := ticket.recordClaimCommitted(ticket.attempt.admission); err != nil {
		t.Fatal(err)
	}
}

func supervisorDraftDone(t *testing.T, ticket *pluginRunTicket, completion pluginRunCompletion) {
	t.Helper()
	if err := ticket.Done(ticket.attempt.admission, completion); err != nil {
		t.Fatal(err)
	}
}

func TestPluginRunSupervisorDraftFenceBetweenReserveAndCommit(t *testing.T) {
	s := newPluginRunSupervisor()
	id := supervisorDraftAdmission("first", "run")
	ticket := supervisorDraftReserve(t, s, id)
	if _, err := ticket.activate(id); !errors.Is(err, errPluginRunClaimState) {
		t.Fatal("uncommitted claim activated", err)
	}
	fence, err := s.beginTransition([]string{id.InstanceID})
	if err != nil {
		t.Fatal(err)
	}
	if len(fence.join.attempts) != 1 || fence.join.attempts[0] != ticket.attempt {
		t.Fatal("reservation missing from fence before SQL commit")
	}
	supervisorDraftCommit(t, ticket) // Simulate the claim commit winning in SQL.
	if _, err = ticket.activate(id); !errors.Is(err, errPluginRunCanceled) {
		t.Fatal("canceled committed attempt activated", err)
	}
	if err = fence.releaseAfterResolution(); !errors.Is(err, errPluginRunUnresolved) {
		t.Fatal("cancel request confused with completion", err)
	}
	supervisorDraftDone(t, ticket, pluginRunDurableCompletion)
	if err = fence.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = fence.releaseAfterResolution(); err != nil {
		t.Fatal(err)
	}
}

func TestPluginRunSupervisorDraftCancelBeforeAndAfterCommit(t *testing.T) {
	for _, when := range []string{"reserved", "committed", "active"} {
		t.Run(when, func(t *testing.T) {
			s := newPluginRunSupervisor()
			id := supervisorDraftAdmission("first", "run")
			ticket := supervisorDraftReserve(t, s, id)
			var execution context.Context
			if when != "reserved" {
				supervisorDraftCommit(t, ticket)
			}
			if when == "active" {
				var err error
				execution, err = ticket.activate(id)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := ticket.requestCancel(); err != nil {
				t.Fatal(err)
			}
			if when == "reserved" {
				supervisorDraftCommit(t, ticket)
			}
			if when != "active" {
				if _, err := ticket.activate(id); !errors.Is(err, errPluginRunCanceled) {
					t.Fatal(err)
				}
			} else if !errors.Is(execution.Err(), context.Canceled) {
				t.Fatal("active context not canceled")
			}
			snapshot, _ := s.snapshot()
			if len(snapshot) != 1 || !snapshot[0].CancelRequested {
				t.Fatal("cancellation prematurely removed the attempt")
			}
			supervisorDraftDone(t, ticket, pluginRunDurableCompletion)
		})
	}
}

func TestPluginRunSupervisorDraftParentCancellation(t *testing.T) {
	for _, active := range []bool{false, true} {
		s := newPluginRunSupervisor()
		parent, cancel := context.WithCancel(context.Background())
		id := supervisorDraftAdmission("first", "run")
		ticket, err := s.reserve(parent, supervisorDraftWindow(t, s), id)
		if err != nil {
			t.Fatal(err)
		}
		supervisorDraftCommit(t, ticket)
		var execution context.Context
		if active {
			execution, err = ticket.activate(id)
			if err != nil {
				t.Fatal(err)
			}
		}
		cancel()
		if active {
			if !errors.Is(execution.Err(), context.Canceled) {
				t.Fatal("parent cancellation not propagated")
			}
		} else if _, err = ticket.activate(id); !errors.Is(err, errPluginRunCanceled) {
			t.Fatal("canceled parent activated", err)
		}
		select {
		case <-ticket.attempt.done:
			t.Fatal("context cancellation incorrectly published Done")
		default:
		}
		supervisorDraftDone(t, ticket, pluginRunDurableCompletion)
	}
}

func TestPluginRunSupervisorDraftUnknownClaimFailsClosed(t *testing.T) {
	s := newPluginRunSupervisor()
	id := supervisorDraftAdmission("first", "run")
	ticket := supervisorDraftReserve(t, s, id)
	if err := ticket.quarantineClaim(id); err != nil {
		t.Fatal(err)
	}
	if _, err := ticket.activate(id); !errors.Is(err, errPluginRunClaimState) {
		t.Fatal(err)
	}
	if err := ticket.recordClaimCommitted(id); !errors.Is(err, errPluginRunClaimState) {
		t.Fatal("ambiguous commit reopened by a running-row observation", err)
	}
	if err := ticket.recordClaimAborted(id); !errors.Is(err, errPluginRunClaimState) {
		t.Fatal("ambiguous commit mislabeled as rollback", err)
	}
	for _, completion := range []pluginRunCompletion{pluginRunRollbackComplete, pluginRunDurableCompletion} {
		if err := ticket.Done(id, completion); !errors.Is(err, errPluginRunUnresolved) {
			t.Fatal("quarantine acknowledged complete", err)
		}
	}
	join, err := s.closeAdmissions()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started, result := make(chan struct{}), make(chan error, 1)
	go func() { close(started); result <- join.Wait(ctx) }()
	<-started
	cancel()
	if err = <-result; !errors.Is(err, context.Canceled) {
		t.Fatal("blocked Wait ignored caller cancellation", err)
	}
	if join.completeLocked() {
		t.Fatal("shutdown turned unknown claim into completion")
	}
}

func TestPluginRunSupervisorDraftDuplicateAndLateDone(t *testing.T) {
	s := newPluginRunSupervisor()
	id := supervisorDraftAdmission("first", "run")
	old := supervisorDraftReserve(t, s, id)
	changed := id
	changed.AdmissionRevision++
	if _, err := s.reserve(context.Background(), supervisorDraftWindow(t, s), changed); !errors.Is(err, errPluginRunDuplicate) {
		t.Fatal("changed revision disguised a duplicate run", err)
	}
	if err := old.recordClaimAborted(id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.reserve(context.Background(), supervisorDraftWindow(t, s), id); !errors.Is(err, errPluginRunDuplicate) {
		t.Fatal("rollback released ticket before caller cleanup", err)
	}
	supervisorDraftDone(t, old, pluginRunRollbackComplete)
	next := supervisorDraftReserve(t, s, id)
	if old.attempt.token == next.attempt.token {
		t.Fatal("attempt token reused")
	}
	if err := old.Done(id, pluginRunRollbackComplete); !errors.Is(err, errPluginRunTicketStale) {
		t.Fatal("late Done accepted", err)
	}
	snapshot, _ := s.snapshot()
	if len(snapshot) != 1 || snapshot[0].AttemptToken != next.attempt.token {
		t.Fatal("late Done removed retry")
	}
	if err := next.recordClaimAborted(id); err != nil {
		t.Fatal(err)
	}
	supervisorDraftDone(t, next, pluginRunRollbackComplete)
}

func TestPluginRunSupervisorDraftAbortedTransitionCannotReviveDelayedActivation(t *testing.T) {
	s := newPluginRunSupervisor()
	id := supervisorDraftAdmission("first", "run")
	old := supervisorDraftReserve(t, s, id)
	supervisorDraftCommit(t, old)
	fence, err := s.beginTransition([]string{id.InstanceID})
	if err != nil {
		t.Fatal(err)
	}
	// The lifecycle operation aborts before any config mutation. Even abort
	// cannot reopen admissions until exact canceled claims have cleaned up.
	if err = fence.releaseAfterResolution(); !errors.Is(err, errPluginRunUnresolved) {
		t.Fatal(err)
	}
	if _, err = old.activate(id); !errors.Is(err, errPluginRunCanceled) {
		t.Fatal(err)
	}
	supervisorDraftDone(t, old, pluginRunDurableCompletion)
	if err = fence.releaseAfterResolution(); err != nil {
		t.Fatal(err)
	}
	later := supervisorDraftReserve(t, s, id)
	supervisorDraftCommit(t, later)
	if _, err = old.activate(id); !errors.Is(err, errPluginRunTicketStale) {
		t.Fatal("delayed activation revived old attempt", err)
	}
	if _, err = later.activate(id); err != nil {
		t.Fatal(err)
	}
	supervisorDraftDone(t, later, pluginRunDurableCompletion)
}

func TestPluginRunSupervisorDraftImmutableAdmissionAndSnapshots(t *testing.T) {
	s := newPluginRunSupervisor()
	id := supervisorDraftAdmission("first", "run")
	ticket := supervisorDraftReserve(t, s, id)
	changed := id
	changed.InputSHA = sha256.Sum256([]byte("trusted rewritten input"))
	changed.AdmissionRevision++
	if err := ticket.recordClaimCommitted(changed); !errors.Is(err, errPluginRunIdentity) {
		t.Fatal("rewritten head replaced original admission", err)
	}
	supervisorDraftCommit(t, ticket)
	if _, err := ticket.activate(changed); !errors.Is(err, errPluginRunIdentity) {
		t.Fatal(err)
	}
	snapshot, err := s.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	snapshot[0].Admission.InstanceID = "other"
	snapshot[0].Admission.InputSHA[0]++
	snapshot[0].AttemptToken++
	snapshot[0].Phase = pluginRunDone
	fresh, _ := s.snapshot()
	if fresh[0].Admission != id || fresh[0].AttemptToken != ticket.attempt.token || fresh[0].Phase != pluginRunCommitted {
		t.Fatal("snapshot aliases registry state")
	}
	if _, err = ticket.activate(id); err != nil {
		t.Fatal(err)
	}
	if _, err = ticket.activate(id); !errors.Is(err, errPluginRunClaimState) {
		t.Fatal("same attempt activated twice", err)
	}
	if err = ticket.Done(changed, pluginRunDurableCompletion); !errors.Is(err, errPluginRunIdentity) {
		t.Fatal(err)
	}
	supervisorDraftDone(t, ticket, pluginRunDurableCompletion)
	if fresh[0].Phase != pluginRunCommitted {
		t.Fatal("old snapshot changed on Done")
	}
}

func TestPluginRunSupervisorDraftInstanceIsolation(t *testing.T) {
	s := newPluginRunSupervisor()
	first := supervisorDraftReserve(t, s, supervisorDraftAdmission("clone-a", "same-run"))
	second := supervisorDraftReserve(t, s, supervisorDraftAdmission("clone-b", "same-run"))
	supervisorDraftCommit(t, first)
	supervisorDraftCommit(t, second)
	firstCtx, _ := first.activate(first.attempt.admission)
	secondCtx, _ := second.activate(second.attempt.admission)
	ids := []string{"clone-a"}
	fence, err := s.beginTransition(ids)
	if err != nil {
		t.Fatal(err)
	}
	ids[0] = "clone-b" // The transition must own a copy.
	if firstCtx.Err() == nil || secondCtx.Err() != nil {
		t.Fatal("cross-instance cancellation or missing target cancellation")
	}
	if _, err = s.reserve(context.Background(), supervisorDraftWindow(t, s), supervisorDraftAdmission("clone-a", "next")); !errors.Is(err, errPluginRunFenced) {
		t.Fatal(err)
	}
	if _, err = s.beginTransition([]string{"clone-b", "clone-a"}); !errors.Is(err, errPluginRunFenced) {
		t.Fatal("overlapping transition not rejected atomically", err)
	}
	third := supervisorDraftReserve(t, s, supervisorDraftAdmission("clone-b", "next"))
	if err = third.recordClaimAborted(third.attempt.admission); err != nil {
		t.Fatal(err)
	}
	supervisorDraftDone(t, third, pluginRunRollbackComplete)
	supervisorDraftDone(t, first, pluginRunDurableCompletion)
	if err = fence.Wait(context.Background()); err != nil {
		t.Fatal("unrelated live instance blocked target join", err)
	}
	if err = fence.releaseAfterResolution(); err != nil {
		t.Fatal(err)
	}
	supervisorDraftDone(t, second, pluginRunDurableCompletion)
}

func TestPluginRunSupervisorDraftReserveFenceLinearization(t *testing.T) {
	for n := 0; n < 64; n++ {
		s := newPluginRunSupervisor()
		id := supervisorDraftAdmission("first", "run")
		start := make(chan struct{})
		var ticket *pluginRunTicket
		var fence *pluginRunTransition
		var claimErr, fenceErr error
		window := supervisorDraftWindow(t, s)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			ticket, claimErr = s.reserve(context.Background(), window, id)
		}()
		go func() { defer wg.Done(); <-start; fence, fenceErr = s.beginTransition([]string{id.InstanceID}) }()
		close(start)
		wg.Wait()
		if fenceErr != nil {
			t.Fatal(fenceErr)
		}
		if claimErr == nil {
			if len(fence.join.attempts) != 1 || fence.join.attempts[0] != ticket.attempt {
				t.Fatal("admitted reservation escaped transition snapshot")
			}
			supervisorDraftCommit(t, ticket)
			if _, err := ticket.activate(id); !errors.Is(err, errPluginRunCanceled) {
				t.Fatal(err)
			}
			supervisorDraftDone(t, ticket, pluginRunDurableCompletion)
		} else if !errors.Is(claimErr, errPluginRunFenced) || len(fence.join.attempts) != 0 {
			t.Fatal("invalid reserve/fence linearization", claimErr)
		}
		if err := fence.releaseAfterResolution(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPluginRunSupervisorDraftConcurrentDuplicateClaims(t *testing.T) {
	s := newPluginRunSupervisor()
	id := supervisorDraftAdmission("first", "run")
	start := make(chan struct{})
	tickets := make(chan *pluginRunTicket, 2)
	errs := make(chan error, 2)
	window := supervisorDraftWindow(t, s)
	for n := 0; n < 2; n++ {
		go func() {
			<-start
			ticket, err := s.reserve(context.Background(), window, id)
			tickets <- ticket
			errs <- err
		}()
	}
	close(start)
	var winner *pluginRunTicket
	for n := 0; n < 2; n++ {
		if ticket := <-tickets; ticket != nil {
			if winner != nil {
				t.Fatal("duplicate claims both admitted")
			}
			winner = ticket
		}
	}
	var duplicates int
	for n := 0; n < 2; n++ {
		if err := <-errs; errors.Is(err, errPluginRunDuplicate) {
			duplicates++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if winner == nil || duplicates != 1 {
		t.Fatal("expected exactly one claim")
	}
	if err := winner.recordClaimAborted(id); err != nil {
		t.Fatal(err)
	}
	supervisorDraftDone(t, winner, pluginRunRollbackComplete)
}

func TestPluginRunSupervisorDraftShutdownCapturesRequestContexts(t *testing.T) {
	s := newPluginRunSupervisor()
	// Neither ticket uses an App.Start worker context.
	active := supervisorDraftReserve(t, s, supervisorDraftAdmission("first", "http"))
	reserved := supervisorDraftReserve(t, s, supervisorDraftAdmission("second", "private"))
	supervisorDraftCommit(t, active)
	ctx, _ := active.activate(active.attempt.admission)
	join, err := s.closeAdmissions()
	if err != nil || len(join.attempts) != 2 || ctx.Err() == nil {
		t.Fatal("shutdown omitted request-context work", err)
	}
	if _, err = s.reserve(context.Background(), pluginRunClaimWindow{supervisor: s}, supervisorDraftAdmission("third", "new")); !errors.Is(err, errPluginRunAdmissionClosed) {
		t.Fatal(err)
	}
	supervisorDraftDone(t, active, pluginRunDurableCompletion)
	if join.completeLocked() {
		t.Fatal("shutdown ignored in-flight SQL reservation")
	}
	if err = reserved.recordClaimAborted(reserved.attempt.admission); err != nil {
		t.Fatal(err)
	}
	supervisorDraftDone(t, reserved, pluginRunRollbackComplete)
	if err = join.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.beginTransition([]string{"third"}); !errors.Is(err, errPluginRunAdmissionClosed) {
		t.Fatal("shutdown admissions reopened", err)
	}
}

func TestPluginRunSupervisorDraftTransitionUnknownRetainsFence(t *testing.T) {
	s := newPluginRunSupervisor()
	fence, err := s.beginTransition([]string{"first"})
	if err != nil {
		t.Fatal(err)
	}
	if err = fence.quarantine(); err != nil {
		t.Fatal(err)
	}
	if err = fence.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = fence.releaseAfterResolution(); !errors.Is(err, errPluginRunTransitionState) {
		t.Fatal("empty memory treated as known lifecycle commit", err)
	}
	if _, err = s.reserve(context.Background(), supervisorDraftWindow(t, s), supervisorDraftAdmission("first", "new")); !errors.Is(err, errPluginRunFenced) {
		t.Fatal(err)
	}
}

func TestPluginRunSupervisorDraftNoCancelUnderMutex(t *testing.T) {
	for _, operation := range []string{"ticket", "fence", "shutdown", "done"} {
		t.Run(operation, func(t *testing.T) {
			s := newPluginRunSupervisor()
			ticket := supervisorDraftReserve(t, s, supervisorDraftAdmission("first", "run"))
			supervisorDraftCommit(t, ticket)
			cancel := ticket.attempt.cancel
			ticket.attempt.cancel = func() {
				if !s.mu.TryLock() {
					t.Error("cancel invoked while registry mutex held")
				} else {
					s.mu.Unlock()
				}
				cancel()
			}
			switch operation {
			case "ticket":
				if err := ticket.requestCancel(); err != nil {
					t.Fatal(err)
				}
			case "fence":
				if _, err := s.beginTransition([]string{"first"}); err != nil {
					t.Fatal(err)
				}
			case "shutdown":
				if _, err := s.closeAdmissions(); err != nil {
					t.Fatal(err)
				}
			}
			supervisorDraftDone(t, ticket, pluginRunDurableCompletion)
		})
	}
}

func TestPluginRunSupervisorDraftMissingRegistryFailsClosed(t *testing.T) {
	for _, s := range []*pluginRunSupervisor{nil, {}} {
		if _, err := s.reserve(context.Background(), pluginRunClaimWindow{supervisor: s}, supervisorDraftAdmission("first", "run")); !errors.Is(err, errPluginRunSupervisorRequired) {
			t.Fatal("nil/zero registry became production bypass", err)
		}
		if _, err := s.closeAdmissions(); !errors.Is(err, errPluginRunSupervisorRequired) {
			t.Fatal(err)
		}
	}
	s := newPluginRunSupervisor()
	s.nextToken = ^uint64(0)
	if _, err := s.reserve(context.Background(), pluginRunClaimWindow{supervisor: s}, supervisorDraftAdmission("first", "run")); !errors.Is(err, errPluginRunAdmissionClosed) {
		t.Fatal("attempt sequence wrapped", err)
	}
}

func TestPluginRunSupervisorDraftClaimWindowRejectsCompletedTransition(t *testing.T) {
	s := newPluginRunSupervisor()
	id := supervisorDraftAdmission("first", "run")
	before := supervisorDraftWindow(t, s)
	fence, err := s.beginTransition([]string{"first"})
	if err != nil {
		t.Fatal(err)
	}
	during := supervisorDraftWindow(t, s)
	if err = fence.releaseAfterResolution(); err != nil {
		t.Fatal(err)
	}
	for _, window := range []pluginRunClaimWindow{before, during} {
		if _, err = s.reserve(context.Background(), window, id); !errors.Is(err, errPluginRunAdmissionStale) {
			t.Fatal("SQL snapshot crossed completed transition", err)
		}
	}
	// A transition on first must not invalidate second's same older window.
	other := supervisorDraftAdmission("second", "run")
	ticket, err := s.reserve(context.Background(), before, other)
	if err != nil {
		t.Fatal(err)
	}
	if err = ticket.recordClaimAborted(other); err != nil {
		t.Fatal(err)
	}
	supervisorDraftDone(t, ticket, pluginRunRollbackComplete)
	ticket = supervisorDraftReserve(t, s, id)
	if err = ticket.recordClaimAborted(id); err != nil {
		t.Fatal(err)
	}
	supervisorDraftDone(t, ticket, pluginRunRollbackComplete)
}

func TestPluginRunSupervisorDraftActivationFenceRace(t *testing.T) {
	for n := 0; n < 64; n++ {
		s := newPluginRunSupervisor()
		id := supervisorDraftAdmission("first", "run")
		ticket := supervisorDraftReserve(t, s, id)
		supervisorDraftCommit(t, ticket)
		start := make(chan struct{})
		var execution context.Context
		var fence *pluginRunTransition
		var activationErr, fenceErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); <-start; execution, activationErr = ticket.activate(id) }()
		go func() { defer wg.Done(); <-start; fence, fenceErr = s.beginTransition([]string{id.InstanceID}) }()
		close(start)
		wg.Wait()
		if fenceErr != nil {
			t.Fatal(fenceErr)
		}
		if activationErr == nil {
			if !errors.Is(execution.Err(), context.Canceled) {
				t.Fatal("activated context escaped fence cancellation")
			}
		} else if !errors.Is(activationErr, errPluginRunCanceled) {
			t.Fatal(activationErr)
		}
		if fence.join.completeLocked() {
			t.Fatal("canceled execution falsely acknowledged Done")
		}
		supervisorDraftDone(t, ticket, pluginRunDurableCompletion)
		if err := fence.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := fence.releaseAfterResolution(); err != nil {
			t.Fatal(err)
		}
	}
}
