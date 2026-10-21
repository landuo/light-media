package durable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
)

// ErrNoChange is an internal transaction outcome: return nil without a write.
// Backends must not publish another receipt or update a revision on this replay.
var ErrNoChange = errors.New("signin journal read-only replay")

// StateBackend belongs to the trusted host. Update must validate a live owned
// lifecycle, binding and execution in its transaction, then atomically commit
// the exact mutation and indexed receipt. Possession of Binding is not authority.
// View/Update must never cover network, browser/OCR, sleep or outbox sink IO.
// Implementations reserve response capacity before admitting network intents.
// A replacement owner must invoke RestoreOwnerState in its exclusive lifecycle
// claim transaction before admitting any execution or outbox IO.
// This interface does not create another task queue or store user credentials.
type StateBackend interface {
	AcquireExecution(context.Context, Binding) (func(), error)
	Hold(context.Context, Binding) (func(), error)
	View(context.Context, Binding, func(*State) error) error
	Update(context.Context, Binding, func(*State) error) error
}

type StateJournal struct{ backend StateBackend }

func NewStateJournal(backend StateBackend) (*StateJournal, error) {
	if backend == nil {
		return nil, ErrStore
	}
	return &StateJournal{backend: backend}, nil
}
func (j *StateJournal) AcquireExecution(ctx context.Context, b Binding) (func(), error) {
	release, err := j.backend.AcquireExecution(ctx, b)
	if err == nil && release == nil {
		return nil, ErrStore
	}
	return release, err
}

var _ Journal = (*StateJournal)(nil)

func (j *StateJournal) State(ctx context.Context, b Binding) (State, error) {
	var out State
	err := j.backend.View(ctx, b, func(s *State) error {
		if err := check(s, b); err != nil {
			return err
		}
		out = copyJSON(*s)
		return nil
	})
	return out, err
}

func (j *StateJournal) Start(ctx context.Context, b Binding, id, action string, plan json.RawMessage, base Baseline) (Run, error) {
	var out Run
	err := j.backend.Update(ctx, b, func(s *State) error {
		if err := check(s, b); err != nil {
			return err
		}
		if old := s.Runs[id]; old != nil {
			if old.ID != id || old.Lanes == nil {
				return ErrStore
			}
			if old.Scope != b.Scope {
				return ErrStale
			}
			if old.Phase != "completed" && old.Phase != "failed_source" && s.ActiveRun != id {
				return ErrConflict
			}
			if old.Action != action || !bytes.Equal(old.Plan, plan) {
				return ErrConflict
			}
			out = copyJSON(*old)
			return ErrNoChange
		}
		if s.ActiveRun != "" {
			return ErrBusy
		}
		s.ActiveRun = id
		run := &Run{Scope: b.Scope, ID: id, Action: action, Plan: plan, Epoch: b.Epoch, Phase: "running", Baseline: base, Lanes: map[string][]Entry{}}
		s.Runs[id] = run
		out = copyJSON(*run)
		return nil
	})
	return out, err
}

func (j *StateJournal) Apply(ctx context.Context, b Binding, e Effect) (Entry, error) {
	if sourceKind(e.Kind) {
		return Entry{}, ErrConflict
	}
	var out Entry
	err := j.backend.Update(ctx, b, func(s *State) error {
		run, err := getRun(s, b, e.RunID)
		if err != nil {
			return err
		}
		old, exists, err := at(run, e.Lane, e.Index)
		if err != nil {
			return err
		}
		if exists {
			if old.Digest != e.Digest || old.Kind != e.Kind || old.Phase != "applied" && old.Phase != "known_error" {
				return ErrConflict
			}
			out = copyJSON(old)
			return ErrNoChange
		}
		if s.ActiveRun != e.RunID || !activeRunPhase(run.Phase) {
			return ErrConflict
		}
		if e.ErrorClass == "" {
			if err = applyMutation(s, e); err != nil {
				return err
			}
		}
		out = Entry{Kind: e.Kind, Digest: e.Digest, Phase: "applied", Value: e.Value, Error: e.KnownError, ErrorClass: e.ErrorClass}
		if e.ErrorClass != "" {
			out.Phase = "known_error"
		}
		run.Lanes[e.Lane] = append(run.Lanes[e.Lane], out)
		return nil
	})
	return out, err
}

func (j *StateJournal) BeginNetwork(ctx context.Context, b Binding, n NetworkIntent) (Entry, bool, error) {
	if sourceKind(n.Kind) {
		return Entry{}, false, ErrConflict
	}
	var out Entry
	replay := false
	err := j.backend.Update(ctx, b, func(s *State) error {
		run, err := getRun(s, b, n.RunID)
		if err != nil {
			return err
		}
		old, exists, err := at(run, n.Lane, n.Index)
		if err != nil {
			return err
		}
		if exists {
			if old.Kind != n.Kind || old.Guard != n.Guard || old.Digest != n.Digest {
				return ErrConflict
			}
			if old.Phase != "receipt" {
				return ErrUncertain
			}
			out = copyJSON(old)
			replay = true
			return ErrNoChange
		}
		if s.ActiveRun != n.RunID || !activeRunPhase(run.Phase) {
			return ErrConflict
		}
		if s.Blocks[n.Guard] != "" {
			return ErrUncertain
		}
		for _, otherRun := range s.Runs {
			for _, entries := range otherRun.Lanes {
				for _, entry := range entries {
					if entry.Guard == n.Guard && (entry.Phase == "intent" || entry.Phase == "uncertain") {
						return ErrUncertain
					}
				}
			}
		}
		out = Entry{Kind: n.Kind, Digest: n.Digest, Guard: n.Guard, Phase: "intent"}
		run.Lanes[n.Lane] = append(run.Lanes[n.Lane], out)
		return nil
	})
	return out, replay, err
}

func (j *StateJournal) CompleteNetwork(ctx context.Context, b Binding, n NetworkIntent, value json.RawMessage, knownError string) error {
	if sourceKind(n.Kind) {
		return ErrConflict
	}
	return j.backend.Update(ctx, b, func(s *State) error {
		run, err := getRun(s, b, n.RunID)
		if err != nil {
			return err
		}
		if s.ActiveRun != n.RunID || !activeRunPhase(run.Phase) {
			return ErrConflict
		}
		old, exists, err := at(run, n.Lane, n.Index)
		if err != nil || !exists || old.Kind != n.Kind || old.Guard != n.Guard || old.Digest != n.Digest || old.Phase != "intent" {
			return ErrConflict
		}
		old.Phase = "receipt"
		old.Value = value
		old.Error = knownError
		run.Lanes[n.Lane][n.Index] = old
		return nil
	})
}

func (j *StateJournal) Unknown(ctx context.Context, b Binding, n NetworkIntent) error {
	if sourceKind(n.Kind) {
		return ErrConflict
	}
	return j.backend.Update(ctx, b, func(s *State) error {
		run, err := getRun(s, b, n.RunID)
		if err != nil {
			return err
		}
		old, exists, err := at(run, n.Lane, n.Index)
		if err != nil || !exists || old.Kind != n.Kind || old.Guard != n.Guard || old.Digest != n.Digest {
			return ErrConflict
		}
		if old.Phase == "receipt" {
			return ErrNoChange
		}
		if s.ActiveRun != n.RunID || !activeRunPhase(run.Phase) || old.Phase != "intent" && old.Phase != "uncertain" {
			return ErrConflict
		}
		old.Phase = "uncertain"
		run.Lanes[n.Lane][n.Index] = old
		s.Blocks[n.Guard] = n.RunID
		return nil
	})
}

func (j *StateJournal) Finish(ctx context.Context, b Binding, id string, runtime, result json.RawMessage, phase string) error {
	if phase != "completed" && phase != "interrupted" {
		return ErrConflict
	}
	return j.backend.Update(ctx, b, func(s *State) error {
		run, err := getRun(s, b, id)
		if err != nil {
			return err
		}
		if run.Phase == "completed" {
			if phase == "completed" && bytes.Equal(run.FinalRuntime, runtime) && bytes.Equal(run.Result, result) {
				return ErrNoChange
			}
			return ErrConflict
		}
		if s.ActiveRun != id || !activeRunPhase(run.Phase) {
			return ErrConflict
		}
		if phase == "completed" {
			for _, entries := range run.Lanes {
				for _, entry := range entries {
					if entry.Phase == "intent" || entry.Phase == "uncertain" {
						return ErrUncertain
					}
				}
			}
		}
		run.Phase = phase
		run.FinalRuntime = runtime
		run.Result = result
		s.Runtime = runtime
		s.RuntimeEpoch = b.Epoch
		if phase == "completed" {
			s.ActiveRun = ""
		}
		return nil
	})
}

func (j *StateJournal) Reconcile(ctx context.Context, b Binding, runID, lane string, index int, value json.RawMessage) error {
	return j.backend.Update(ctx, b, func(s *State) error {
		run, err := getRun(s, b, runID)
		if err != nil {
			return err
		}
		if s.ActiveRun != runID || !activeRunPhase(run.Phase) {
			return ErrConflict
		}
		old, exists, err := at(run, lane, index)
		if err != nil || !exists || sourceKind(old.Kind) || old.Phase != "uncertain" {
			return ErrConflict
		}
		old.Phase = "receipt"
		old.Value = value
		old.Error = ""
		run.Lanes[lane][index] = old
		delete(s.Blocks, old.Guard)
		return nil
	})
}

func (j *StateJournal) AbortKnown(ctx context.Context, b Binding, id string) error {
	return j.backend.Update(ctx, b, func(s *State) error {
		run, err := getRun(s, b, id)
		if err != nil {
			return err
		}
		if s.ActiveRun != id || run.Phase != "interrupted" {
			return ErrConflict
		}
		for _, entries := range run.Lanes {
			for _, entry := range entries {
				if entry.Phase == "intent" || entry.Phase == "uncertain" {
					return ErrUncertain
				}
			}
		}
		run.Phase = "abandoned"
		s.ActiveRun = ""
		return nil
	})
}

func check(state *State, b Binding) error {
	if state.Scope != b.Scope || state.Epoch != b.Epoch {
		return ErrStale
	}
	return stateShape(state)
}

func activeRunPhase(phase string) bool { return phase == "running" || phase == "interrupted" }

func getRun(s *State, b Binding, id string) (*Run, error) {
	if err := check(s, b); err != nil {
		return nil, err
	}
	r := s.Runs[id]
	if r == nil {
		return nil, ErrConflict
	}
	if r.ID != id || r.Lanes == nil {
		return nil, ErrStore
	}
	if r.Scope != b.Scope {
		return nil, ErrStale
	}
	return r, nil
}

func applyMutation(s *State, e Effect) error {
	m := e.Mutation
	switch m.Kind {
	case "":
	case "config":
		s.Config = append([]byte{}, m.Value...)
	case "history.put":
		if _, ok := s.History[m.Key]; !ok {
			s.Keys = append(s.Keys, m.Key)
		}
		s.History[m.Key] = append([]byte{}, m.Value...)
	case "history.delete":
		delete(s.History, m.Key)
		keys := []string{}
		for _, key := range s.Keys {
			if key != m.Key {
				keys = append(keys, key)
			}
		}
		s.Keys = keys
	case "cache.ensure":
		s.CacheReady[m.Key] = true
	case "cache.write":
		var data []byte
		if json.Unmarshal(m.Value, &data) != nil {
			return ErrStore
		}
		s.Cache[m.Key] = append([]byte{}, data...)
	case "stat.apply":
		var input struct {
			Success bool
			Seconds *int
			Date    string
		}
		if json.Unmarshal(m.Value, &input) != nil {
			return ErrStore
		}
		stat, exists := s.Stats[m.Key]
		s.Stats[m.Key] = UpdateStatistic(stat, exists, input.Success, input.Seconds, input.Date)
	case "stat":
		var stat Statistic
		if json.Unmarshal(m.Value, &stat) != nil {
			return ErrStore
		}
		s.Stats[m.Key] = stat
	case "outbox":
		id := e.RunID + ":" + e.Lane + ":" + itoa(e.Index)
		s.Outbox = append(s.Outbox, OutboxItem{ID: id, Kind: e.Kind, Payload: e.Value, RunID: e.RunID, Lane: e.Lane, Index: e.Index, Phase: "pending"})
	case "scheduler_job":
		if _, exists := s.Jobs[m.Key]; !exists {
			s.Jobs[m.Key] = append([]byte{}, m.Value...)
		}
	case "scheduler_remove":
		s.Jobs = map[string]json.RawMessage{}
	case "services":
		s.Services = append([]byte{}, m.Value...)
	case "runtime":
		s.Runtime = append([]byte{}, m.Value...)
	default:
		return ErrConflict
	}
	return nil
}
