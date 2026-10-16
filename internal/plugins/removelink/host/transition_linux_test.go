//go:build linux

package cleanuphost

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeTransitionPublicationGapCapturesWithoutStaleGuardOrTimer(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	c, d := filepath.Join(f.src, "second.mkv"), filepath.Join(f.media, "second.mkv")
	write(t, c, "second-owned-entity")
	if err := os.Link(c, d); err != nil {
		t.Fatal(err)
	}
	f.opts.Watch = nil
	f.opts.Now = time.Now
	var coreRevision atomic.Int64
	coreRevision.Store(f.opts.Binding.Revision)
	var rejected atomic.Int64
	f.opts.Guard = func(_ context.Context, binding Binding, _ string) (func(), error) {
		if binding.Revision != coreRevision.Load() {
			rejected.Add(1)
			return nil, ErrUnsafe
		}
		return func() {}, nil
	}
	h := f.open(t)
	ctx := context.Background()
	if err := h.Configure(ctx, f.opts.Binding, f.config(true, map[string]any{"delay_seconds": 10})); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	waitState(t, h, func(s Durable) bool { return len(s.Machine.Queue) == 1 })
	ticket := begin(t, h, f.opts.Binding, "native-publication-gap", "init")
	to := f.opts.Binding
	to.Revision++
	publish(t, f, ticket, to, "actual-new-revision-run")
	coreRevision.Store(to.Revision)
	if err := os.Remove(c); err != nil {
		t.Fatal(err)
	}
	waitState(t, h, func(s Durable) bool {
		for _, r := range s.Events {
			if r.Event.Path == c && r.State == "pending" && r.Binding == ticket.From {
				return true
			}
		}
		return false
	})
	// Cross the actual old deadline, not a mocked/manual Tick. The old binding
	// is now rejected by Guard, so any accidental background work is observable.
	time.Sleep(10500 * time.Millisecond)
	state := snapshot(t, h)
	if !exists(b) || !exists(d) || state.Blocked != "" || rejected.Load() != 0 {
		t.Fatal("publication gap executed stale event/timer", rejected.Load(), state.Blocked)
	}
	if err := h.ApplyTransition(ctx, ticket, "actual-new-revision-run", f.config(false, map[string]any{"delete_history": false, "delete_torrents": false, "notify": false})); err != nil {
		t.Fatal(err)
	}
	state = snapshot(t, h)
	if exists(b) || exists(d) || state.Transition.Phase != "applied" || f.effects.count("history_src") != 0 || rejected.Load() != 0 {
		t.Fatal("new config did not drain the actual captured/queued files")
	}
	if err := h.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
