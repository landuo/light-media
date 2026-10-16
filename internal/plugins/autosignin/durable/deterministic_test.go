package durable

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	source "light-media/internal/plugins/autosignin"
)

func TestClockEntropyAndSleepRemainingDurationReplay(t *testing.T) {
	store, binding, _ := fixture(t)
	store.Start(context.Background(), binding, "deterministic", "test", json.RawMessage(`{}`), Baseline{})
	ctx := context.Background()
	wall := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	clockReads, rngReads, sleeps := 0, 0, 0
	now := func() time.Time { clockReads++; return wall }
	random := func(a, b int) (int, error) { rngReads++; return 2, nil }
	sleep := func(context.Context, time.Duration) error { sleeps++; wall = wall.Add(5 * time.Second); return nil }
	run := func() {
		lane := (&Execution{Store: store, Binding: binding, RunID: "deterministic", DigestKey: fixtureKey}).Lane("site", "")
		if lane.Clock(ctx, now)().Year() != 2026 {
			t.Fatal("clock replay lost")
		}
		if value, err := lane.Choose(ctx, random)(0, 3); err != nil || value != 2 {
			t.Fatal(value, err)
		}
		if err := lane.Sleep(ctx, func() time.Time { return wall }, sleep)(ctx, 5*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	run()
	run()
	if clockReads != 1 || rngReads != 1 || sleeps != 1 {
		t.Fatal("replay reran nondeterministic dependencies", clockReads, rngReads, sleeps)
	}
}
func TestStaleInstallationImplementationAndForgedPrivateSiteCannotRun(t *testing.T) {
	store, binding, _ := fixture(t)
	c := controller(store, binding, &fixedHTTP{Response: confirmed})
	boot(t, c)
	for _, bad := range []Scope{{Instance: binding.Instance, Generation: 2, Revision: 1, Implementation: binding.Implementation}, {Instance: binding.Instance, Generation: 1, Revision: 2, Implementation: binding.Implementation}, {Instance: binding.Instance, Generation: 1, Revision: 1, Implementation: "other-binary"}} {
		copy := *c
		copy.Binding.Scope = bad
		if _, err := copy.Execute(context.Background(), "stale", Command{Action: "service"}); !errors.Is(err, ErrStale) {
			t.Fatal("stale scope allowed", err)
		}
	}
	old := c.Dependencies.Factory
	c.Dependencies.Factory = func(ctx context.Context, ref source.Object) (BoundSite, error) {
		bound, err := old(ctx, ref)
		bound.Site["id"] = 2
		return bound, err
	}
	if _, err := c.Execute(context.Background(), "wrong-site", Command{Action: "service"}); !errors.Is(err, ErrStale) {
		t.Fatal("factory mapped selected ID to another credential target", err)
	}
}
func TestStorageCapacityAndBadKeyFailWithoutLeakingOrTruncation(t *testing.T) {
	store, binding, _ := fixture(t)
	badKey := fixtureKey
	badKey[0]++
	other, err := OpenFileStore(store.dir, binding.Instance, badKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.State(context.Background(), binding); !errors.Is(err, ErrStore) {
		t.Fatal("wrong key read private state", err)
	}
	file, err := os.OpenFile(store.path, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(maxStateBytes + 65); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if _, err = store.State(context.Background(), binding); !errors.Is(err, ErrCapacity) {
		t.Fatal("oversized ciphertext was silently read/truncated", err)
	}
}
