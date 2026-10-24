package cleanuphost

import (
	"context"
	"errors"
	"light-media/internal/plugins/removelink"
	"testing"
)

func TestOriginalStateRealHost(t *testing.T) {
	f := newFixture(t)
	h, err := Open(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	defer h.Suspend(ctx)
	before, err := h.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state, err := h.OriginalState(ctx)
	if err != nil || state != false {
		t.Fatal(state, err)
	}
	after, err := h.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Effects) != len(after.Effects) || len(before.Events) != len(after.Events) {
		t.Fatal("getter wrote journal")
	}
	if err = h.Suspend(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = h.OriginalState(ctx); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

// Pure getter test: no host recovery, durable-state decoder, observer, or lease.
// Machine.Init has no monitored paths and therefore no filesystem effects.
func TestOriginalStateLiveMachineIndependence(t *testing.T) {
	makeHost := func(enabled string) *Host {
		m := removelink.New(nil)
		if err := m.Init([]byte(`{"enabled":` + enabled + `}`)); err != nil {
			t.Fatal(err)
		}
		h := &Host{machine: m, commands: make(chan request), done: make(chan struct{})}
		go h.loop()
		t.Cleanup(func() { _ = h.call(context.Background(), func() error { h.closing = true; return nil }) })
		return h
	}
	target, sibling := makeHost("true"), makeHost("false")
	if v, e := target.OriginalState(context.Background()); e != nil || !v {
		t.Fatal(v, e)
	}
	if v, e := sibling.OriginalState(context.Background()); e != nil || v {
		t.Fatal(v, e)
	}
	if err := target.call(context.Background(), func() error { return target.machine.Init([]byte(`{"enabled":false}`)) }); err != nil {
		t.Fatal(err)
	}
	if v, e := target.OriginalState(context.Background()); e != nil || v {
		t.Fatal("stale state", v, e)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sibling.OriginalState(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
