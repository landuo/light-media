package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"sort"
	"sync"
)

// Preparatory host-private core only. No App, Store, claim or lifecycle path is
// wired to this registry yet. All durable proofs remain the adapter's duty.
var (
	errPluginRunSupervisorRequired = errors.New("plugin run supervisor required")
	errPluginRunIdentity           = errors.New("plugin run admission identity mismatch")
	errPluginRunAdmissionClosed    = errors.New("plugin run admissions closed")
	errPluginRunAdmissionStale     = errors.New("plugin run claim crossed an instance transition")
	errPluginRunFenced             = errors.New("plugin instance transition fenced")
	errPluginRunDuplicate          = errors.New("plugin run attempt already registered")
	errPluginRunTicketStale        = errors.New("plugin run attempt token stale")
	errPluginRunClaimState         = errors.New("plugin run claim state invalid")
	errPluginRunCanceled           = errors.New("plugin run cancellation requested")
	errPluginRunUnresolved         = errors.New("plugin run completion unresolved")
	errPluginRunTransitionState    = errors.New("plugin run transition state invalid")
)

// This is the immutable ORIGINAL claim admission, never a mutable PluginRun.
// Revision propagation and authenticated input rewrites cannot rename a ticket.
// InputSHA is computed before reserve, outside the memory mutex; no raw config
// or input is retained. Plugin identity comes from the trusted installation.
type pluginRunAdmission struct {
	InstanceID, PluginID, Version, RunID, Action string
	Generation, AdmissionRevision                int64
	InputSHA                                     [sha256.Size]byte
}

func (v pluginRunAdmission) valid() bool {
	return v.InstanceID != "" && v.PluginID != "" && v.Version != "" &&
		v.RunID != "" && v.Action != "" && v.Generation > 0 && (v.AdmissionRevision > 0 || (v.AdmissionRevision == 0 && (v.Action == "init" || v.Action == "reload")))
}

type pluginRunAttemptKey struct{ InstanceID, RunID string }

type pluginRunAttemptPhase uint8

const (
	pluginRunReserved pluginRunAttemptPhase = iota + 1
	pluginRunCommitted
	pluginRunActive
	pluginRunClaimAborted
	pluginRunClaimQuarantined
	pluginRunFinishing
	pluginRunDone
)

type pluginRunAttempt struct {
	admission       pluginRunAdmission
	token           uint64
	phase           pluginRunAttemptPhase
	cancelRequested bool
	ctx             context.Context // Always a standard WithCancel child.
	cancel          context.CancelFunc
	done            chan struct{}
	lease           *pluginExecutionLease // private runtime adapter; never serialized
}

// A ticket is never serialized or supplied by a plugin. Registry ownership plus
// the monotonic attempt token prevents a late Done from deleting a later retry.
type pluginRunTicket struct {
	supervisor *pluginRunSupervisor
	attempt    *pluginRunAttempt
}

type pluginRunSupervisor struct {
	mu          sync.Mutex
	initialized bool
	closed      bool
	nextToken   uint64
	attempts    map[pluginRunAttemptKey]*pluginRunAttempt
	fences      map[string]*pluginRunTransition
	lastFence   map[string]uint64
}

func newPluginRunSupervisor() *pluginRunSupervisor {
	return &pluginRunSupervisor{initialized: true,
		attempts:  make(map[pluginRunAttemptKey]*pluginRunAttempt),
		fences:    make(map[string]*pluginRunTransition),
		lastFence: make(map[string]uint64)}
}

func (s *pluginRunSupervisor) usable() bool { return s != nil && s.initialized }

// nextTokenLocked must never wrap, including for an App that runs indefinitely.
func (s *pluginRunSupervisor) nextTokenLocked() (uint64, error) {
	if s.nextToken == ^uint64(0) {
		s.closed = true
		return 0, errPluginRunAdmissionClosed
	}
	s.nextToken++
	return s.nextToken, nil
}

// Capture before BeginTx, not after reading a potentially stale SQL snapshot.
// An instance's fence BEGIN and RELEASE both invalidate earlier windows. This
// also rejects a transaction opened during a fence but reserved after release.
type pluginRunClaimWindow struct {
	supervisor *pluginRunSupervisor
	sequence   uint64
}

func (s *pluginRunSupervisor) beginClaim() (pluginRunClaimWindow, error) {
	if !s.usable() {
		return pluginRunClaimWindow{}, errPluginRunSupervisorRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return pluginRunClaimWindow{}, errPluginRunAdmissionClosed
	}
	return pluginRunClaimWindow{s, s.nextToken}, nil
}

// reserve MUST run before the pending -> running transaction may commit. It is
// permitted inside that transaction: this operation only holds a short memory
// mutex. The caller must end the transaction before cancellation joins.
func (s *pluginRunSupervisor) reserve(parent context.Context, window pluginRunClaimWindow, identity pluginRunAdmission) (*pluginRunTicket, error) {
	if !s.usable() {
		return nil, errPluginRunSupervisorRequired
	}
	if parent == nil || !identity.valid() || window.supervisor != s {
		return nil, errPluginRunIdentity
	}
	ctx, cancel := context.WithCancel(parent) // No context callbacks under mu.
	s.mu.Lock()
	var err error
	key := pluginRunAttemptKey{identity.InstanceID, identity.RunID}
	switch {
	case s.closed:
		err = errPluginRunAdmissionClosed
	case s.fences[identity.InstanceID] != nil:
		err = errPluginRunFenced
	case window.sequence < s.lastFence[identity.InstanceID]:
		err = errPluginRunAdmissionStale
	case s.attempts[key] != nil:
		err = errPluginRunDuplicate
	case ctx.Err() != nil: // ctx is the standard child, not a user callback.
		err = ctx.Err()
	}
	var token uint64
	if err == nil {
		token, err = s.nextTokenLocked()
	}
	if err != nil {
		s.mu.Unlock()
		cancel()
		return nil, err
	}
	attempt := &pluginRunAttempt{admission: identity, token: token,
		phase: pluginRunReserved, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	s.attempts[key] = attempt
	s.mu.Unlock()
	return &pluginRunTicket{supervisor: s, attempt: attempt}, nil
}

func (t *pluginRunTicket) valid() bool {
	return t != nil && t.supervisor.usable() && t.attempt != nil
}

func (t *pluginRunTicket) currentLocked() bool {
	a := t.attempt
	actual := t.supervisor.attempts[pluginRunAttemptKey{a.admission.InstanceID, a.admission.RunID}]
	return actual == a && actual.token == a.token
}

// recordClaimCommitted is called only after a successful exact claim Commit.
// It cannot resolve quarantine: a row merely found running is not proof of the
// original attempt's ambiguous commit. No recovery bypass is supplied here.
func (t *pluginRunTicket) recordClaimCommitted(identity pluginRunAdmission) error {
	return t.recordClaim(identity, pluginRunCommitted)
}

// recordClaimAborted requires proof the transaction cannot have committed.
// SQL ErrTxDone/Commit error alone is not that proof. Caller still owes Done
// after releasing its non-SQL claim resources; abort does not close done.
func (t *pluginRunTicket) recordClaimAborted(identity pluginRunAdmission) error {
	return t.recordClaim(identity, pluginRunClaimAborted)
}

func (t *pluginRunTicket) quarantineClaim(identity pluginRunAdmission) error {
	return t.recordClaim(identity, pluginRunClaimQuarantined)
}

func (t *pluginRunTicket) recordClaim(identity pluginRunAdmission, phase pluginRunAttemptPhase) error {
	if phase != pluginRunCommitted && phase != pluginRunClaimAborted && phase != pluginRunClaimQuarantined {
		return errPluginRunClaimState
	}
	if !t.valid() {
		return errPluginRunTicketStale
	}
	s := t.supervisor
	s.mu.Lock()
	defer s.mu.Unlock()
	if !t.currentLocked() {
		return errPluginRunTicketStale
	}
	if t.attempt.admission != identity {
		return errPluginRunIdentity
	}
	if t.attempt.phase != pluginRunReserved {
		return errPluginRunClaimState
	}
	t.attempt.phase = phase
	return nil
}

// activate grants a context only to this exact committed attempt. A fence's
// sticky cancellation wins even if actual cancel delivery is still in flight,
// or the lifecycle transition later aborts. Cancellation is not completion.
func (t *pluginRunTicket) activate(identity pluginRunAdmission) (context.Context, error) {
	if !t.valid() {
		return nil, errPluginRunTicketStale
	}
	s := t.supervisor
	s.mu.Lock()
	defer s.mu.Unlock()
	if !t.currentLocked() {
		return nil, errPluginRunTicketStale
	}
	a := t.attempt
	if a.admission != identity {
		return nil, errPluginRunIdentity
	}
	if a.phase != pluginRunCommitted {
		return nil, errPluginRunClaimState
	}
	if a.cancelRequested || a.ctx.Err() != nil {
		return nil, errPluginRunCanceled
	}
	a.phase = pluginRunActive
	return a.ctx, nil
}

func (t *pluginRunTicket) requestCancel() error {
	if !t.valid() {
		return errPluginRunTicketStale
	}
	s := t.supervisor
	s.mu.Lock()
	if !t.currentLocked() {
		s.mu.Unlock()
		return errPluginRunTicketStale
	}
	t.attempt.cancelRequested = true
	s.mu.Unlock()
	t.attempt.cancel()
	return nil
}

type pluginRunCompletion uint8

const (
	pluginRunRollbackComplete pluginRunCompletion = iota + 1
	pluginRunDurableCompletion
)

// Done is a TRUSTED host acknowledgement, not durable proof produced by this
// memory core. Call only after ALL execution/Runner/owner/pool cleanup and the
// exact durable final outcome (including a retained reconcile_pending outcome)
// are confirmed. A canceled context, SDK exit or finish-write error is not Done.
// Durable reconcile_pending still requires the separate native effect barrier.
func (t *pluginRunTicket) Done(identity pluginRunAdmission, completion pluginRunCompletion) error {
	if !t.valid() {
		return errPluginRunTicketStale
	}
	s := t.supervisor
	s.mu.Lock()
	if !t.currentLocked() {
		s.mu.Unlock()
		return errPluginRunTicketStale
	}
	a := t.attempt
	if a.admission != identity {
		s.mu.Unlock()
		return errPluginRunIdentity
	}
	valid := completion == pluginRunRollbackComplete && a.phase == pluginRunClaimAborted ||
		completion == pluginRunDurableCompletion && (a.phase == pluginRunCommitted || a.phase == pluginRunActive)
	if !valid {
		s.mu.Unlock()
		return errPluginRunUnresolved
	}
	a.phase = pluginRunFinishing // No concurrent activation while cancel detaches.
	s.mu.Unlock()
	a.cancel()
	s.mu.Lock()
	a.phase = pluginRunDone
	delete(s.attempts, pluginRunAttemptKey{a.admission.InstanceID, a.admission.RunID})
	close(a.done)
	s.mu.Unlock()
	return nil
}

// A join is a frozen set of attempts, not a live instance query. New retries
// cannot be confused with captured tokens. Only registry methods create joins.
type pluginRunJoin struct {
	valid    bool
	attempts []*pluginRunAttempt
}

func (j pluginRunJoin) Wait(ctx context.Context) error {
	if !j.valid || ctx == nil {
		return errPluginRunUnresolved
	}
	for _, a := range j.attempts {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-a.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return ctx.Err()
}

// completeLocked never waits; it only observes Done's publication channels.
func (j pluginRunJoin) completeLocked() bool {
	if !j.valid {
		return false
	}
	for _, a := range j.attempts {
		select {
		case <-a.done:
		default:
			return false
		}
	}
	return true
}

type pluginRunTransitionPhase uint8

const (
	pluginRunTransitionOpen pluginRunTransitionPhase = iota + 1
	pluginRunTransitionQuarantined
	pluginRunTransitionReleased
)

type pluginRunTransition struct {
	supervisor *pluginRunSupervisor
	token      uint64
	instances  []string
	join       pluginRunJoin
	phase      pluginRunTransitionPhase
}

// beginTransition atomically fences all exact instance IDs, captures every
// already reserved attempt and requests cancellation. No exclusions exist in
// this phase. In particular this API MUST NOT yet wire MediaServerRefresh's
// old-config drain or PluginUnInstall's self invocation; see the design note.
func (s *pluginRunSupervisor) beginTransition(instances []string) (*pluginRunTransition, error) {
	if !s.usable() {
		return nil, errPluginRunSupervisorRequired
	}
	ids := append([]string(nil), instances...)
	sort.Strings(ids)
	if len(ids) == 0 {
		return nil, errPluginRunIdentity
	}
	for n, instance := range ids {
		if instance == "" || n > 0 && ids[n-1] == instance {
			return nil, errPluginRunIdentity
		}
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errPluginRunAdmissionClosed
	}
	for _, instance := range ids {
		if s.fences[instance] != nil {
			s.mu.Unlock()
			return nil, errPluginRunFenced
		}
	}
	token, err := s.nextTokenLocked()
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	t := &pluginRunTransition{supervisor: s, token: token, instances: ids,
		join: pluginRunJoin{valid: true}, phase: pluginRunTransitionOpen}
	for _, instance := range ids {
		s.fences[instance] = t
		s.lastFence[instance] = token
	}
	for key, a := range s.attempts {
		if s.fences[key.InstanceID] == t {
			a.cancelRequested = true
			t.join.attempts = append(t.join.attempts, a)
		}
	}
	s.mu.Unlock()
	for _, a := range t.join.attempts {
		a.cancel()
	}
	return t, nil
}

func (t *pluginRunTransition) Wait(ctx context.Context) error {
	if t == nil {
		return errPluginRunTransitionState
	}
	return t.join.Wait(ctx)
}

// releaseAfterResolution is called only after a known commit OR known abort of
// the lifecycle transaction and owner resolution. Both require prior cleanup
// of every captured ticket. An aborted transition never uncancels a ticket.
// Unknown lifecycle outcomes must quarantine instead; no automatic reopening.
func (t *pluginRunTransition) releaseAfterResolution() error {
	if t == nil || !t.supervisor.usable() {
		return errPluginRunTransitionState
	}
	s := t.supervisor
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.phase != pluginRunTransitionOpen {
		return errPluginRunTransitionState
	}
	for _, instance := range t.instances {
		if s.fences[instance] != t || s.fences[instance].token != t.token {
			return errPluginRunTransitionState
		}
	}
	if !t.join.completeLocked() {
		return errPluginRunUnresolved
	}
	sequence, err := s.nextTokenLocked()
	if err != nil {
		return err // Keep fences closed on sequence exhaustion.
	}
	for _, instance := range t.instances {
		s.lastFence[instance] = sequence
		delete(s.fences, instance)
	}
	t.phase = pluginRunTransitionReleased
	return nil
}

func (t *pluginRunTransition) quarantine() error {
	if t == nil || !t.supervisor.usable() {
		return errPluginRunTransitionState
	}
	s := t.supervisor
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.phase != pluginRunTransitionOpen {
		return errPluginRunTransitionState
	}
	t.phase = pluginRunTransitionQuarantined
	return nil
}

// closeAdmissions covers request-context fastlanes as well as App workers,
// once ALL claim adapters are wired. It neither joins nor closes SQL storage.
func (s *pluginRunSupervisor) closeAdmissions() (pluginRunJoin, error) {
	if !s.usable() {
		return pluginRunJoin{}, errPluginRunSupervisorRequired
	}
	s.mu.Lock()
	s.closed = true
	join := pluginRunJoin{valid: true}
	for _, a := range s.attempts {
		a.cancelRequested = true
		join.attempts = append(join.attempts, a)
	}
	s.mu.Unlock()
	for _, a := range join.attempts {
		a.cancel()
	}
	return join, nil
}

type pluginRunAttemptSnapshot struct {
	Admission       pluginRunAdmission
	AttemptToken    uint64
	Phase           pluginRunAttemptPhase
	CancelRequested bool // Supervisor request, distinct from parent cancellation.
}

// snapshot returns value copies only; never expose attempt pointers/cancel or
// allow callers to mutate identity, fences, phases or the stored join set.
func (s *pluginRunSupervisor) snapshot() ([]pluginRunAttemptSnapshot, error) {
	if !s.usable() {
		return nil, errPluginRunSupervisorRequired
	}
	s.mu.Lock()
	out := make([]pluginRunAttemptSnapshot, 0, len(s.attempts))
	for _, a := range s.attempts {
		out = append(out, pluginRunAttemptSnapshot{a.admission, a.token, a.phase, a.cancelRequested})
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].AttemptToken < out[j].AttemptToken })
	return out, nil
}
