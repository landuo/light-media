package app

import (
	"errors"
	"sync"
)

// These private references describe object occupancy, not caller authority.
// A registered job/API must first be selected and authorized by its own host
// registry before it can supply an exact reference for a new invocation.
type originalRetainedOwner uint8

const (
	originalRetainedCall originalRetainedOwner = iota + 1
	originalRetainedJob
	originalRetainedAPI
	originalRetainedSubmission
	originalRetainedOperation // temporary pin across remove/registration work
)

// This finite core has no RPC, process callback, registry or persistent state.
// The caller which receives retirement=true performs termination outside all
// these locks. A claim is NOT evidence of worker/process completion.
type originalRetainedLifetime struct {
	mu                sync.Mutex
	object            *originalPluginInstance
	managerOwned      bool
	sealed            bool
	retirementClaimed bool
	retirementDone    bool
	owners            map[*originalRetainedLease]struct{}
	done              chan struct{}
}

type originalRetainedLease struct {
	lifetime    *originalRetainedLifetime
	kind        originalRetainedOwner
	phaseActive bool
	appReturned bool
	sdkDrained  bool
}

func newOriginalRetainedLifetime(object *originalPluginInstance) *originalRetainedLifetime {
	return &originalRetainedLifetime{object: object, managerOwned: true, owners: make(map[*originalRetainedLease]struct{}), done: make(chan struct{})}
}

func (s *originalRetainedLifetime) containsLocked(lease *originalRetainedLease) bool {
	if lease == nil || lease.lifetime != s {
		return false
	}
	_, ok := s.owners[lease]
	return ok
}

func (s *originalRetainedLifetime) retain(kind originalRetainedOwner, parent *originalRetainedLease) (*originalRetainedLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.object == nil || kind < originalRetainedCall || kind > originalRetainedOperation || s.sealed {
		return nil, errors.New("original object retention is closed or invalid")
	}
	// A detached object cannot be rediscovered by textual instance ID. Only a
	// still-current exact owner may pin another operation after manager remove.
	if !s.managerOwned && (!s.containsLocked(parent) || !parent.phaseActive) {
		return nil, errors.New("detached original object requires an exact live owner")
	}
	lease := &originalRetainedLease{lifetime: s, kind: kind, phaseActive: true}
	s.owners[lease] = struct{}{}
	return lease, nil
}

// Only a fresh call lease can contribute to host callback admission. Job/API
// ownership alone never grants a plugin HostCaller or HTTP authentication.
func (lease *originalRetainedLease) callPhaseAllowed(object *originalPluginInstance) bool {
	if lease == nil || lease.lifetime == nil {
		return false
	}
	s := lease.lifetime
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.sealed && s.object == object && lease.kind == originalRetainedCall && lease.phaseActive && s.containsLocked(lease)
}

func (s *originalRetainedLifetime) claimIfUnownedLocked() bool {
	if s.managerOwned || len(s.owners) != 0 || s.retirementClaimed {
		return false
	}
	s.sealed, s.retirementClaimed = true, true
	return true
}

func (s *originalRetainedLifetime) detachManager() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.managerOwned = false
	return s.claimIfUnownedLocked()
}

// Ordinary owners are released after their real registry/admission removal.
// Calls must instead establish BOTH completion facts below.
func (lease *originalRetainedLease) releaseOwner() (bool, error) {
	if lease == nil || lease.lifetime == nil || lease.kind == originalRetainedCall {
		return false, errors.New("call owners require returned and actual-drained facts")
	}
	s := lease.lifetime
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.containsLocked(lease) {
		return false, nil
	}
	lease.phaseActive = false
	delete(s.owners, lease)
	return s.claimIfUnownedLocked(), nil
}

// called after App Wait returns AND its callback phase is closed; cancellation
// can reach here while the worker handler is still running.
func (lease *originalRetainedLease) callReturned() (bool, error) {
	return lease.recordCallFact(true)
}

// Called only after SDK PendingCall.Drained actually closes. This core does not
// infer it from Cancel, PendingCall.Wait, a zero-count snapshot or an ACK.
func (lease *originalRetainedLease) callDrained() (bool, error) {
	return lease.recordCallFact(false)
}

func (lease *originalRetainedLease) recordCallFact(returned bool) (bool, error) {
	if lease == nil || lease.lifetime == nil || lease.kind != originalRetainedCall {
		return false, errors.New("not an original call owner")
	}
	s := lease.lifetime
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.containsLocked(lease) {
		return false, nil
	}
	if returned {
		lease.appReturned, lease.phaseActive = true, false
	} else {
		lease.sdkDrained = true
	}
	if !lease.appReturned || !lease.sdkDrained {
		return false, nil
	}
	delete(s.owners, lease)
	return s.claimIfUnownedLocked(), nil
}

// App.Close/package replacement can claim physical termination immediately,
// even with live owners. Never wait for drain before initiating forced stop.
// A prior natural claimant already owns termination; other callers wait on
// retired(), not on owners reaching zero. The real termination path is bounded.
func (s *originalRetainedLifetime) claimForcedRetirement() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.managerOwned, s.sealed = false, true
	for lease := range s.owners {
		lease.phaseActive = false
	}
	if s.retirementClaimed {
		return false
	}
	s.retirementClaimed = true
	return true
}

func (s *originalRetainedLifetime) retired() <-chan struct{} { return s.done }

// The integration calls this only after actual process termination/Wait, not
// merely after sending transport-close. Notification occurs outside the lock.
func (s *originalRetainedLifetime) finishRetirement() error {
	s.mu.Lock()
	if !s.retirementClaimed {
		s.mu.Unlock()
		return errors.New("original object retirement was not claimed")
	}
	if s.retirementDone {
		s.mu.Unlock()
		return nil
	}
	s.retirementDone = true
	done := s.done
	s.mu.Unlock()
	close(done)
	return nil
}
