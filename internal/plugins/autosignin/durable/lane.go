package durable

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	source "light-media/internal/plugins/autosignin"
	"strconv"
	"sync"
	"time"
)

func itoa(i int) string { return strconv.Itoa(i) }

type Execution struct {
	Store                 Journal
	Binding               Binding
	RunID                 string
	Epoch                 string
	DigestKey             [32]byte
	mu                    sync.Mutex
	lanes                 map[string]*Lane
	fault                 error
	sourceFailure         *source.SourceFailure
	sourceFailureIdentity SourceIdentity
}
type Lane struct {
	execution   *Execution
	Name, Guard string
	index       int
	fault       error
}

func (e *Execution) Lane(name, guard string) *Lane {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lanes == nil {
		e.lanes = map[string]*Lane{}
	}
	if lane := e.lanes[name]; lane != nil {
		return lane
	}
	lane := &Lane{execution: e, Name: name, Guard: guard}
	e.lanes[name] = lane
	return lane
}
func (l *Lane) digest(kind string, value any) string {
	mac := hmac.New(sha256.New, l.execution.DigestKey[:])
	mac.Write([]byte(kind))
	mac.Write([]byte{0})
	mac.Write(marshal(value))
	return hex.EncodeToString(mac.Sum(nil))
}
func (l *Lane) Fault() error {
	l.execution.mu.Lock()
	defer l.execution.mu.Unlock()
	return l.execution.fault
}
func (l *Lane) stop(err error) error {
	l.execution.mu.Lock()
	defer l.execution.mu.Unlock()
	if l.execution.fault == nil {
		l.execution.fault = err
	}
	l.fault = err
	return err
}
func (l *Lane) Effect(ctx context.Context, kind, key string, value any, mutation Mutation) error {
	if err := l.Fault(); err != nil {
		return err
	}
	e := l.execution
	effect := Effect{RunID: e.RunID, Lane: l.Name, Index: l.index, Kind: kind, Value: marshal(value), Mutation: mutation}
	effect.Digest = l.digest(kind, struct {
		Key      string
		Value    any
		Mutation Mutation
	}{key, value, mutation})
	entry, err := e.Store.Apply(ctx, e.Binding, effect)
	if err != nil {
		return l.stop(err)
	}
	l.index++
	if entry.Phase == "known_error" {
		return &source.SourceFailure{Message: entry.Error, Class: entry.ErrorClass}
	}
	return nil
}
func (l *Lane) lookup(ctx context.Context, kind, digest string) (Entry, bool, error) {
	entry, exists, err := readEntry(ctx, l.execution.Store, l.execution.Binding, l.execution.RunID, l.Name, l.index)
	if err != nil {
		return Entry{}, false, l.stop(err)
	}
	if exists && (entry.Kind != kind || entry.Digest != digest || entry.Phase != "applied") {
		return entry, true, l.stop(ErrConflict)
	}
	return entry, exists, nil
}
func (l *Lane) Value(ctx context.Context, kind string, args any, fetch func() (json.RawMessage, error)) (json.RawMessage, error) {
	if err := l.Fault(); err != nil {
		return nil, err
	}
	digest := l.digest(kind, args)
	entry, exists, err := l.lookup(ctx, kind, digest)
	if err != nil {
		return nil, err
	}
	if exists {
		l.index++
		return entry.Value, nil
	}
	value, err := fetch()
	if err != nil {
		return nil, l.stop(err)
	}
	e := l.execution
	if _, err = e.Store.Apply(ctx, e.Binding, Effect{RunID: e.RunID, Lane: l.Name, Index: l.index, Kind: kind, Digest: digest, Value: value}); err != nil {
		return nil, l.stop(err)
	}
	l.index++
	return value, nil
}
func (l *Lane) Clock(ctx context.Context, now func() time.Time) func() time.Time {
	return func() time.Time {
		raw, err := l.Value(ctx, "clock", nil, func() (json.RawMessage, error) { return marshal(now().Format(time.RFC3339Nano)), nil })
		if err != nil {
			return time.Unix(0, 0)
		}
		var text string
		if json.Unmarshal(raw, &text) != nil {
			l.stop(ErrStore)
			return time.Unix(0, 0)
		}
		value, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			l.stop(ErrStore)
		}
		return value
	}
}
func (l *Lane) Choose(ctx context.Context, choose func(int, int) (int, error)) func(int, int) (int, error) {
	return func(a, b int) (int, error) {
		raw, err := l.Value(ctx, "entropy", []int{a, b}, func() (json.RawMessage, error) {
			v, err := choose(a, b)
			out := struct {
				Value int
				Error string
			}{Value: v}
			if err != nil {
				out.Error = err.Error()
			}
			return marshal(out), nil
		})
		if err != nil {
			return 0, err
		}
		var out struct {
			Value int
			Error string
		}
		if json.Unmarshal(raw, &out) != nil {
			return 0, l.stop(ErrStore)
		}
		if out.Error != "" {
			return 0, errors.New(out.Error)
		}
		return out.Value, nil
	}
}
func (l *Lane) Sleep(ctx context.Context, now func() time.Time, sleep func(context.Context, time.Duration) error) func(context.Context, time.Duration) error {
	return func(_ context.Context, duration time.Duration) error {
		raw, err := l.Value(ctx, "sleep", duration, func() (json.RawMessage, error) { return marshal(now().Add(duration).Format(time.RFC3339Nano)), nil })
		if err != nil {
			return err
		}
		var text string
		json.Unmarshal(raw, &text)
		wake, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return l.stop(ErrStore)
		}
		remaining := wake.Sub(now())
		if remaining > 0 {
			return sleep(ctx, remaining)
		}
		return nil
	}
}

type KnownUnsent struct{ Cause string }

func (e KnownUnsent) Error() string { return "provably unsent external request" }
func (l *Lane) External(ctx context.Context, kind string, request any, call func() (json.RawMessage, bool, error)) (json.RawMessage, error) {
	if err := l.Fault(); err != nil {
		return nil, err
	}
	e := l.execution
	n := NetworkIntent{RunID: e.RunID, Lane: l.Name, Index: l.index, Kind: kind, Digest: l.digest(kind, request), Guard: l.Guard}
	old, replay, err := e.Store.BeginNetwork(ctx, e.Binding, n)
	if err != nil {
		return nil, l.stop(err)
	}
	if replay {
		l.index++
		if old.Error != "" {
			return nil, errors.New(old.Error)
		}
		return old.Value, nil
	}
	value, known, callErr := call()
	if !known {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = e.Store.Unknown(cleanup, e.Binding, n)
		return nil, l.stop(ErrUncertain)
	}
	errorText := ""
	if callErr != nil {
		errorText = callErr.Error()
	}
	// Failure to durably record a received result also leaves the original intent
	// blocking. No fallback/request can proceed under this execution's fault.
	if err = e.Store.CompleteNetwork(ctx, e.Binding, n, value, errorText); err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		// A lost commit reply may already have a receipt; Unknown preserves
		// that receipt. Otherwise it makes same-owner reconciliation possible.
		_ = e.Store.Unknown(cleanup, e.Binding, n)
		return nil, l.stop(ErrUncertain)
	}
	l.index++
	return value, callErr
}
