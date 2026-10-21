package durable

import (
	"context"
	"encoding/json"
)

// These optional read capabilities belong to the trusted host, not the worker.
// Each read must enforce the same binding, owner and storage-integrity checks as
// StateBackend.View and return an owned snapshot. Binding alone is not authority.
// A backend must distinguish a verified absent value from an unloaded value and
// must never pass its partial projection to StateBackend.Update.
type EntryReader interface {
	ReadEntry(context.Context, Binding, string, string, int) (Entry, bool, error)
}

type CacheValue struct {
	Value            []byte
	Found, KeyExists bool
}

// Found is Value != nil, including for a present empty byte slice. KeyExists
// additionally distinguishes a stored nil value from an absent cache key.
type CacheReader interface {
	ReadCache(context.Context, Binding, string) (CacheValue, error)
}

// RunStartHead is explicitly not a complete Run: it omits all lanes. For an
// existing run it carries that run's frozen baseline and immutable identity.
// For an absent run, Baseline contains current runtime/history/config/keys;
// Controller supplies the new inventory and timezone. RuntimeEpoch always
// describes the current instance, whereas Epoch belongs to an existing run.
type RunStartHead struct {
	Exists                   bool
	Fresh                    bool
	RuntimeEpoch             string
	ID, Action, Epoch, Phase string
	Scope                    Scope
	Plan                     json.RawMessage
	Baseline                 Baseline
	FinalRuntime, Result     json.RawMessage
}

type RunStartReader interface {
	ReadRunStartHead(context.Context, Binding, string) (RunStartHead, error)
}

// StateJournal forwards optional native reads while retaining the original
// complete-state implementation as its fallback. A typed read error never
// causes a fallback or a second source/external operation.
func (j *StateJournal) ReadEntry(ctx context.Context, b Binding, run, lane string, index int) (Entry, bool, error) {
	if reader, ok := j.backend.(EntryReader); ok {
		return reader.ReadEntry(ctx, b, run, lane, index)
	}
	return stateEntry(ctx, j, b, run, lane, index)
}

func (j *StateJournal) ReadCache(ctx context.Context, b Binding, key string) (CacheValue, error) {
	if reader, ok := j.backend.(CacheReader); ok {
		return reader.ReadCache(ctx, b, key)
	}
	return stateCache(ctx, j, b, key)
}

func (j *StateJournal) ReadRunStartHead(ctx context.Context, b Binding, id string) (RunStartHead, error) {
	if reader, ok := j.backend.(RunStartReader); ok {
		return reader.ReadRunStartHead(ctx, b, id)
	}
	return stateRunStartHead(ctx, j, b, id)
}

func readEntry(ctx context.Context, journal Journal, b Binding, run, lane string, index int) (Entry, bool, error) {
	if reader, ok := journal.(EntryReader); ok {
		return reader.ReadEntry(ctx, b, run, lane, index)
	}
	return stateEntry(ctx, journal, b, run, lane, index)
}

func stateEntry(ctx context.Context, journal Journal, b Binding, run, lane string, index int) (Entry, bool, error) {
	state, err := journal.State(ctx, b)
	if err != nil {
		return Entry{}, false, err
	}
	if state.Runs[run] == nil {
		return Entry{}, false, ErrConflict
	}
	return at(state.Runs[run], lane, index)
}

func readCache(ctx context.Context, journal Journal, b Binding, key string) (CacheValue, error) {
	if reader, ok := journal.(CacheReader); ok {
		return reader.ReadCache(ctx, b, key)
	}
	return stateCache(ctx, journal, b, key)
}

func stateCache(ctx context.Context, journal Journal, b Binding, key string) (CacheValue, error) {
	state, err := journal.State(ctx, b)
	if err != nil {
		return CacheValue{}, err
	}
	value, exists := state.Cache[key]
	return CacheValue{Value: value, Found: value != nil, KeyExists: exists}, nil
}

func readRunStartHead(ctx context.Context, journal Journal, b Binding, id string) (RunStartHead, error) {
	if reader, ok := journal.(RunStartReader); ok {
		return reader.ReadRunStartHead(ctx, b, id)
	}
	return stateRunStartHead(ctx, journal, b, id)
}

func stateRunStartHead(ctx context.Context, journal Journal, b Binding, id string) (RunStartHead, error) {
	state, err := journal.State(ctx, b)
	if err != nil {
		return RunStartHead{}, err
	}
	head := RunStartHead{Fresh: len(state.Runs) == 0 && state.RuntimeEpoch == "" && state.ActiveRun == "", RuntimeEpoch: state.RuntimeEpoch, Scope: state.Scope}
	if run := state.Runs[id]; run != nil {
		head.Exists, head.ID, head.Action, head.Epoch, head.Phase = true, run.ID, run.Action, run.Epoch, run.Phase
		head.Scope, head.Plan, head.Baseline, head.FinalRuntime, head.Result = run.Scope, run.Plan, run.Baseline, run.FinalRuntime, run.Result
	} else {
		head.Baseline = Baseline{Runtime: state.Runtime, History: marshal(state.History), Config: state.Config, Keys: append([]string{}, state.Keys...)}
	}
	return head, nil
}
