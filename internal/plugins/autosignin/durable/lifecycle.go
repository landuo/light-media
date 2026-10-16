package durable

import "encoding/json"

// NewState constructs an unclaimed state. It performs no IO and grants no
// execution capability. Provisioning belongs to the trusted host transaction.
func NewState(scope Scope, runtime json.RawMessage) (State, error) {
	if scope.Instance == "" || scope.Implementation == "" || scope.Generation == 0 || scope.Revision == 0 {
		return State{}, ErrStale
	}
	if len(runtime) != 0 && !json.Valid(runtime) {
		return State{}, ErrStore
	}
	return State{Schema: 1, Scope: scope, Runtime: append(json.RawMessage(nil), runtime...), History: map[string]json.RawMessage{}, Cache: map[string][]byte{}, CacheReady: map[string]bool{}, Stats: map[string]Statistic{}, Runs: map[string]*Run{}, Blocks: map[string]string{}, Jobs: map[string]json.RawMessage{}}, nil
}

// RestoreOwnerState must run in the host's exclusive owner-claim transaction,
// after all former executions and outbox deliveries have stopped. Binding is
// an identity to verify, never evidence of authority. This pure transition does
// not send requests, abandon runs, or make uncertain work safe to repeat.
func RestoreOwnerState(s *State, b Binding) error {
	if s == nil || s.Scope != b.Scope || b.Epoch == "" {
		return ErrStale
	}
	if err := stateShape(s); err != nil {
		return err
	}
	// Validate before mutating so callers cannot accidentally retain a partial
	// restore after a malformed state, even without a transactional fixture.
	for id, run := range s.Runs {
		if run == nil || run.ID != id || run.Lanes == nil || run.Scope != b.Scope {
			return ErrStore
		}
	}
	if s.ActiveRun != "" && (s.Runs[s.ActiveRun] == nil || !activeRunPhase(s.Runs[s.ActiveRun].Phase)) {
		return ErrStore
	}
	s.Epoch = b.Epoch
	for i := range s.Outbox {
		if s.Outbox[i].Phase == "intent" {
			s.Outbox[i].Phase = "uncertain"
		}
	}
	for _, run := range s.Runs {
		for lane, entries := range run.Lanes {
			for i := range entries {
				if entries[i].Phase == "intent" {
					entries[i].Phase = "uncertain"
				}
				if entries[i].Phase == "uncertain" {
					s.Blocks[entries[i].Guard] = run.ID
				}
			}
			run.Lanes[lane] = entries
		}
	}
	return nil
}

func stateShape(s *State) error {
	if s == nil || s.Schema != 1 || s.Runs == nil || s.History == nil || s.Cache == nil || s.CacheReady == nil || s.Stats == nil || s.Blocks == nil || s.Jobs == nil {
		return ErrStore
	}
	return nil
}
