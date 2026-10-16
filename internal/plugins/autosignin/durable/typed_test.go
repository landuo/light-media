package durable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	source "light-media/internal/plugins/autosignin"
)

// The readers below deliberately execute the unchanged full-state fixture for
// their answer. They prove dispatch/replay compatibility, not sparse SQL cost.
// App SQL selected-cell authentication/masks remain a separate candidate.
type typedFixtureJournal struct {
	Journal
	entryReads, cacheReads, headReads, fullReads atomic.Int32
	fail                                         string
}

func (j *typedFixtureJournal) State(context.Context, Binding) (State, error) {
	j.fullReads.Add(1)
	return State{}, ErrStore
}
func (j *typedFixtureJournal) ReadEntry(ctx context.Context, b Binding, run, lane string, index int) (Entry, bool, error) {
	j.entryReads.Add(1)
	if j.fail == "entry" {
		return Entry{}, false, ErrCapacity
	}
	return stateEntry(ctx, j.Journal, b, run, lane, index)
}
func (j *typedFixtureJournal) ReadCache(ctx context.Context, b Binding, key string) (CacheValue, error) {
	j.cacheReads.Add(1)
	if j.fail == "cache" {
		return CacheValue{}, ErrCapacity
	}
	return stateCache(ctx, j.Journal, b, key)
}
func (j *typedFixtureJournal) ReadRunStartHead(ctx context.Context, b Binding, run string) (RunStartHead, error) {
	j.headReads.Add(1)
	if j.fail == "head" {
		return RunStartHead{}, ErrCapacity
	}
	return stateRunStartHead(ctx, j.Journal, b, run)
}

type typedFixtureBackend struct {
	StateBackend
	reader *typedFixtureJournal
	views  atomic.Int32
}

func (b *typedFixtureBackend) View(ctx context.Context, binding Binding, fn func(*State) error) error {
	b.views.Add(1)
	return b.StateBackend.View(ctx, binding, fn)
}
func (b *typedFixtureBackend) ReadEntry(ctx context.Context, binding Binding, run, lane string, index int) (Entry, bool, error) {
	return b.reader.ReadEntry(ctx, binding, run, lane, index)
}
func (b *typedFixtureBackend) ReadCache(ctx context.Context, binding Binding, key string) (CacheValue, error) {
	return b.reader.ReadCache(ctx, binding, key)
}
func (b *typedFixtureBackend) ReadRunStartHead(ctx context.Context, binding Binding, run string) (RunStartHead, error) {
	return b.reader.ReadRunStartHead(ctx, binding, run)
}

func TestTypedStateJournalForwardsNativeReadersAndKeepsLegacyFallback(t *testing.T) {
	store, b, _ := fixture(t)
	ctx := context.Background()
	if _, err := store.Start(ctx, b, "run", "test", marshal(nil), Baseline{Runtime: marshal("frozen")}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(ctx, b, Effect{RunID: "run", Lane: "main", Kind: "clock", Digest: "clock", Value: marshal("original")}); err != nil {
		t.Fatal(err)
	}
	reader := &typedFixtureJournal{Journal: store}
	backend := &typedFixtureBackend{StateBackend: fileStoreBackend{store}, reader: reader}
	j, _ := NewStateJournal(backend)
	legacy, _ := NewStateJournal(fileStoreBackend{store})
	for _, journal := range []*StateJournal{legacy, j} {
		entry, exists, err := journal.ReadEntry(ctx, b, "run", "main", 0)
		if err != nil || !exists || string(entry.Value) != `"original"` {
			t.Fatal(entry, exists, err)
		}
		if _, _, err := journal.ReadEntry(ctx, b, "missing", "main", 0); !errors.Is(err, ErrConflict) {
			t.Fatal("missing run became a next receipt", err)
		}
		if _, exists, err := journal.ReadEntry(ctx, b, "run", "main", 1); err != nil || exists {
			t.Fatal("contiguous append location changed", exists, err)
		}
		if _, _, err := journal.ReadEntry(ctx, b, "run", "main", 2); !errors.Is(err, ErrConflict) {
			t.Fatal("receipt gap accepted", err)
		}
		head, err := journal.ReadRunStartHead(ctx, b, "run")
		if err != nil || !head.Exists || head.ID != "run" || head.Scope != b.Scope || string(head.Baseline.Runtime) != `"frozen"` {
			t.Fatal("frozen run head lost identity", head, err)
		}
		newHead, err := journal.ReadRunStartHead(ctx, b, "new")
		if err != nil || newHead.Exists || newHead.Scope != b.Scope || len(newHead.Baseline.History) == 0 {
			t.Fatal("new run seed changed", newHead, err)
		}
		if _, err := journal.ReadCache(ctx, Binding{Scope: b.Scope, Epoch: "foreign-owner"}, "cache"); !errors.Is(err, ErrStale) {
			t.Fatal("typed read lost owner validation", err)
		}
	}
	if backend.views.Load() != 0 || reader.entryReads.Load() != 4 || reader.headReads.Load() != 2 || reader.cacheReads.Load() != 1 {
		t.Fatal("native reader was bypassed or repeated", backend.views.Load())
	}
	for _, kind := range []string{"entry", "cache", "head"} {
		reader.fail = kind
		var err error
		switch kind {
		case "entry":
			_, _, err = j.ReadEntry(ctx, b, "run", "main", 0)
		case "cache":
			_, err = j.ReadCache(ctx, b, "cache")
		case "head":
			_, err = j.ReadRunStartHead(ctx, b, "run")
		}
		if !errors.Is(err, ErrCapacity) || backend.views.Load() != 0 {
			t.Fatal("native backend error fell back to View", kind, err)
		}
	}
}

func TestTypedErrorsStopBeforeFallbackOrSourceFetch(t *testing.T) {
	for _, fail := range []string{"entry", "cache", "head"} {
		t.Run(fail, func(t *testing.T) {
			store, b, _ := fixture(t)
			ctx := context.Background()
			if _, err := store.Start(ctx, b, "run", "test", marshal(nil), Baseline{}); err != nil {
				t.Fatal(err)
			}
			reader := &typedFixtureJournal{Journal: store, fail: fail}
			fetches := 0
			execution := &Execution{Store: reader, Binding: b, RunID: "run", DigestKey: fixtureKey}
			var err error
			switch fail {
			case "entry":
				_, err = execution.Lane("main", "").Value(ctx, "clock", nil, func() (json.RawMessage, error) { fetches++; return marshal("new"), nil })
			case "cache":
				_, err = (Cache{Lane: execution.Lane("main", ""), Key: "cache"}).Read(ctx)
			case "head":
				c := controller(reader, b, &fixedHTTP{})
				c.Dependencies.Inventory = func(context.Context) (source.Inventory, error) { fetches++; return inventory(), nil }
				_, err = c.Execute(ctx, "new", Command{Action: "cold_init"})
			}
			if !errors.Is(err, ErrCapacity) || reader.fullReads.Load() != 0 || fetches != 0 {
				t.Fatal("typed error fell back or executed source", err, reader.fullReads.Load(), fetches)
			}
			s, _ := store.State(ctx, b)
			if len(s.Runs["run"].Lanes) != 0 || s.Runs["new"] != nil {
				t.Fatal("failed read appended a receipt/run")
			}
		})
	}
}

func TestTypedCachePreservesNilEmptyAbsentAndFrozenObservation(t *testing.T) {
	raw, err := readAutoSignInFixture("typed-read-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Cases []struct {
			Name, Value string
			KeyExists   bool
			Found       bool
			Nil         bool
		}
	}
	if json.Unmarshal(raw, &reference) != nil || len(reference.Cases) != 4 {
		t.Fatal("typed fallback reference inventory")
	}
	for _, row := range reference.Cases {
		t.Run(row.Name, func(t *testing.T) {
			store, b, _ := fixture(t)
			ctx := context.Background()
			if err := store.update(ctx, func(s *State) error {
				if row.KeyExists {
					if row.Nil {
						s.Cache["cache"] = nil
					} else {
						s.Cache["cache"] = []byte(row.Value)
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			reader := &typedFixtureJournal{Journal: store}
			value, err := reader.ReadCache(ctx, b, "cache")
			if err != nil || value.Found != row.Found || value.KeyExists != row.KeyExists || (value.Value == nil) != row.Nil || string(value.Value) != row.Value {
				t.Fatal("cache presence semantics changed", value, err)
			}
			if _, err = store.Start(ctx, b, "cache-run", "test", marshal(nil), Baseline{}); err != nil {
				t.Fatal(err)
			}
			read := func() ([]byte, error) {
				e := &Execution{Store: reader, Binding: b, RunID: "cache-run", DigestKey: fixtureKey}
				return (Cache{Lane: e.Lane("main", ""), Key: "cache"}).Read(ctx)
			}
			first, firstErr := read()
			reads := reader.cacheReads.Load()
			if err = store.update(ctx, func(s *State) error { s.Cache["cache"] = []byte("changed-after-observation"); return nil }); err != nil {
				t.Fatal(err)
			}
			again, againErr := read()
			if row.Found && (firstErr != nil || againErr != nil || !bytes.Equal(first, again) || string(first) != row.Value || (first == nil) != (again == nil)) || !row.Found && (!errors.Is(firstErr, source.ErrTjuptCacheIO) || !errors.Is(againErr, source.ErrTjuptCacheIO)) {
				t.Fatal("cache replay reread changed bytes", firstErr, againErr)
			}
			if reader.cacheReads.Load() != reads || reader.fullReads.Load() != 0 {
				t.Fatal("cache replay performed a new fetch")
			}
		})
	}
}

func TestTypedLaneKeepsOriginalAppliedOnlyLookupChecks(t *testing.T) {
	for _, scenario := range []string{"applied", "known_error", "kind", "digest"} {
		t.Run(scenario, func(t *testing.T) {
			store, b, _ := fixture(t)
			ctx := context.Background()
			if _, err := store.Start(ctx, b, "run", "test", marshal(nil), Baseline{}); err != nil {
				t.Fatal(err)
			}
			reader := &typedFixtureJournal{Journal: store}
			e := &Execution{Store: reader, Binding: b, RunID: "run", DigestKey: fixtureKey}
			lane := e.Lane("main", "")
			effect := Effect{RunID: "run", Lane: "main", Kind: "clock", Digest: lane.digest("clock", nil), Value: marshal("original")}
			switch scenario {
			case "known_error":
				effect.ErrorClass, effect.KnownError = "OSError", "known failure"
			case "kind":
				effect.Kind = "http"
			case "digest":
				effect.Digest = "foreign"
			}
			if _, err := store.Apply(ctx, b, effect); err != nil {
				t.Fatal(err)
			}
			fetches := 0
			value, err := lane.Value(ctx, "clock", nil, func() (json.RawMessage, error) { fetches++; return marshal("new"), nil })
			if scenario == "applied" && (err != nil || string(value) != `"original"`) || scenario != "applied" && !errors.Is(err, ErrConflict) || fetches != 0 || reader.fullReads.Load() != 0 {
				t.Fatal("typed lookup changed phase/digest checks", string(value), err, fetches)
			}
		})
	}
}

func TestTypedControllerMatchesLegacyAndNeverRefreshesExistingBaseline(t *testing.T) {
	var states []State
	for _, typed := range []bool{false, true} {
		store, b, _ := fixture(t)
		var journal Journal = struct{ Journal }{store} // Hide optional readers.
		reader := &typedFixtureJournal{Journal: store}
		if typed {
			journal = reader
		}
		target := &fixedHTTP{Response: confirmed}
		c := controller(journal, b, target)
		boot(t, c)
		first, err := c.Execute(context.Background(), "service", Command{Action: "service"})
		if err != nil {
			t.Fatal(err)
		}
		inventoryCalls := 0
		c.Dependencies.Inventory = func(context.Context) (source.Inventory, error) { inventoryCalls++; return source.Inventory{}, ErrStore }
		again, err := c.Execute(context.Background(), "service", Command{Action: "service"})
		if err != nil || !bytes.Equal(marshal(first), marshal(again)) || inventoryCalls != 0 || target.Calls.Load() != 1 {
			t.Fatal("replay changed inventory/result/network", err, inventoryCalls, target.Calls.Load())
		}
		if _, err = c.Execute(context.Background(), "service", Command{Action: "api"}); !errors.Is(err, ErrConflict) || inventoryCalls != 0 {
			t.Fatal("typed head bypassed Start's frozen action/plan", err)
		}
		if typed && (reader.headReads.Load() != 5 || reader.entryReads.Load() == 0 || reader.fullReads.Load() != 0) {
			t.Fatal("controller did not use typed reads", reader.headReads.Load(), reader.entryReads.Load(), reader.fullReads.Load())
		}
		state, err := store.State(context.Background(), b)
		if err != nil {
			t.Fatal(err)
		}
		states = append(states, state)
	}
	if !bytes.Equal(marshal(states[0]), marshal(states[1])) {
		t.Fatal("typed dispatch changed final source effects or baseline")
	}
}
