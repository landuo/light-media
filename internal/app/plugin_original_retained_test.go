package app

import (
	"sync"
	"sync/atomic"
	"testing"
)

func retainedTestLease(t *testing.T, lifetime *originalRetainedLifetime, kind originalRetainedOwner, parent *originalRetainedLease) *originalRetainedLease {
	t.Helper()
	lease, err := lifetime.retain(kind, parent)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

// Pure ownership-state tests. Simulated completion facts below are NOT SDK
// process/drain evidence; that separate integration must bind real Drained.
func TestOriginalRetainedOwnersAndExactAdmission(t *testing.T) {
	object := &originalPluginInstance{}
	life := newOriginalRetainedLifetime(object)
	call := retainedTestLease(t, life, originalRetainedCall, nil)
	job := retainedTestLease(t, life, originalRetainedJob, nil)
	api := retainedTestLease(t, life, originalRetainedAPI, nil)
	submission := retainedTestLease(t, life, originalRetainedSubmission, nil)
	operation := retainedTestLease(t, life, originalRetainedOperation, nil)
	if life.detachManager() || !call.callPhaseAllowed(object) {
		t.Fatal("detach killed an active call or lost owners")
	}
	for _, owner := range []*originalRetainedLease{job, api, submission, operation} {
		if owner.callPhaseAllowed(object) {
			t.Fatal("occupancy became callback authority")
		}
	}
	if call.callPhaseAllowed(&originalPluginInstance{}) {
		t.Fatal("same-shape replacement borrowed authority")
	}
	if _, err := life.retain(originalRetainedCall, nil); err == nil {
		t.Fatal("detached textual lookup admitted a call")
	}
	other := newOriginalRetainedLifetime(&originalPluginInstance{})
	foreign := retainedTestLease(t, other, originalRetainedAPI, nil)
	if _, err := life.retain(originalRetainedCall, foreign); err == nil {
		t.Fatal("foreign object owner admitted")
	}
	bound := retainedTestLease(t, life, originalRetainedCall, api)
	if !bound.callPhaseAllowed(object) {
		t.Fatal("fresh exact API-derived call missing")
	}
	if claimed, err := call.callReturned(); err != nil || claimed || call.callPhaseAllowed(object) {
		t.Fatal("returned phase kept authority or retired early", err)
	}
	if _, err := life.retain(originalRetainedCall, call); err == nil {
		t.Fatal("closed call admitted a descendant")
	}
	if claimed, err := call.callDrained(); err != nil || claimed {
		t.Fatal("other owners lost", err)
	}
	for _, owner := range []*originalRetainedLease{job, api, submission, operation} {
		if claimed, err := owner.releaseOwner(); err != nil || claimed {
			t.Fatal("bound call did not retain object", err)
		}
	}
	if _, err := life.retain(originalRetainedCall, api); err == nil {
		t.Fatal("released API registration admitted")
	}
	if claimed, err := bound.callDrained(); err != nil || claimed || !bound.callPhaseAllowed(object) {
		t.Fatal("drained-before-return revoked successful call early", err)
	}
	if claimed, err := bound.callReturned(); err != nil || !claimed {
		t.Fatal("last actual call did not claim retirement", err)
	}
	if life.detachManager() || life.claimForcedRetirement() {
		t.Fatal("retirement claimed twice")
	}
	select {
	case <-life.retired():
		t.Fatal("claim pretended actual termination")
	default:
	}
	if err := life.finishRetirement(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-life.retired():
	default:
		t.Fatal("completion notification missing")
	}
	if err := life.finishRetirement(); err != nil {
		t.Fatal("completion not idempotent", err)
	}
}

func TestOriginalRetainedCallFactsAndForcedClose(t *testing.T) {
	for _, returnedFirst := range []bool{true, false} {
		life := newOriginalRetainedLifetime(&originalPluginInstance{})
		call := retainedTestLease(t, life, originalRetainedCall, nil)
		if _, err := call.releaseOwner(); err == nil {
			t.Fatal("generic release bypassed two facts")
		}
		if life.detachManager() {
			t.Fatal("live call retired")
		}
		first, second := call.callDrained, call.callReturned
		if returnedFirst {
			first, second = call.callReturned, call.callDrained
		}
		if claim, err := first(); err != nil || claim {
			t.Fatal("single fact retired", err)
		}
		if claim, err := second(); err != nil || !claim {
			t.Fatal("two facts did not retire", err)
		}
		if claim, err := second(); err != nil || claim {
			t.Fatal("duplicate fact retired twice", err)
		}
	}
	life := newOriginalRetainedLifetime(&originalPluginInstance{})
	call := retainedTestLease(t, life, originalRetainedCall, nil)
	job := retainedTestLease(t, life, originalRetainedJob, nil)
	if err := life.finishRetirement(); err == nil {
		t.Fatal("unclaimed completion accepted")
	}
	if !life.claimForcedRetirement() {
		t.Fatal("forced close waited for live owners")
	}
	if call.callPhaseAllowed(life.object) {
		t.Fatal("forced close retained callback authority")
	}
	if _, err := life.retain(originalRetainedCall, job); err == nil {
		t.Fatal("forced seal accepted work")
	}
	if claim, err := job.releaseOwner(); err != nil || claim {
		t.Fatal("release replaced forced claim", err)
	}
	if claim, err := call.callReturned(); err != nil || claim {
		t.Fatal(err)
	}
	if claim, err := call.callDrained(); err != nil || claim {
		t.Fatal("late drain claimed twice", err)
	}
	if err := life.finishRetirement(); err != nil {
		t.Fatal(err)
	}
}

func TestOriginalRetainedConcurrentRetirementOnce(t *testing.T) {
	life := newOriginalRetainedLifetime(&originalPluginInstance{})
	const n = 32
	owners := make([]*originalRetainedLease, n)
	for i := range owners {
		owners[i] = retainedTestLease(t, life, originalRetainedJob, nil)
	}
	if life.detachManager() {
		t.Fatal("owners lost")
	}
	var claims atomic.Int32
	var group sync.WaitGroup
	for _, owner := range owners {
		group.Add(1)
		go func(owner *originalRetainedLease) {
			defer group.Done()
			claimed, err := owner.releaseOwner()
			if err != nil {
				t.Error(err)
			}
			if claimed {
				claims.Add(1)
			}
		}(owner)
	}
	group.Wait()
	if claims.Load() != 1 {
		t.Fatal("retirement claimant count", claims.Load())
	}
	if _, err := life.retain(originalRetainedJob, nil); err == nil {
		t.Fatal("retired object reopened")
	}
}
