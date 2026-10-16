package durable

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestJournalRetiredRunCannotAppendUnderLaterActiveRun(t *testing.T) {
	store, binding, _ := fixture(t)
	ctx := context.Background()
	if _, err := store.Start(ctx, binding, "old", "test", marshal(nil), Baseline{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(ctx, binding, Effect{RunID: "old", Lane: "main", Index: 0, Kind: "local", Digest: "old-digest", Value: marshal(nil)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(ctx, binding, "old", marshal(nil), marshal(nil), "interrupted"); err != nil {
		t.Fatal(err)
	}
	if err := store.AbortKnown(ctx, binding, "old"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Start(ctx, binding, "current", "test", marshal(nil), Baseline{}); err != nil {
		t.Fatal(err)
	}
	before, err := store.State(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(ctx, binding, Effect{RunID: "old", Lane: "main", Index: 1, Kind: "config", Digest: "late-local", Value: marshal(nil), Mutation: Mutation{Kind: "config", Value: marshal(map[string]bool{"unexpected": true})}}); !errors.Is(err, ErrConflict) {
		t.Fatal("retired execution wrote config under new active run", err)
	}
	if _, _, err := store.BeginNetwork(ctx, binding, NetworkIntent{RunID: "old", Lane: "main", Index: 1, Kind: "http", Digest: "late-remote", Guard: "site-action"}); !errors.Is(err, ErrConflict) {
		t.Fatal("retired execution acquired a new external intent", err)
	}
	state, err := store.State(ctx, binding)
	if err != nil || state.ActiveRun != "current" || !bytes.Equal(state.Config, before.Config) || len(state.Runs["old"].Lanes["main"]) != 1 {
		t.Fatal("rejected old execution changed live state", err)
	}
	// Historical read-only receipt replay is still allowed; it grants no new
	// mutation and must not replace the newer active run or runtime.
	if _, err := store.Apply(ctx, binding, Effect{RunID: "old", Lane: "main", Index: 0, Kind: "local", Digest: "old-digest", Value: marshal(nil)}); err != nil {
		t.Fatal("known old receipt could not be read", err)
	}
}

func TestJournalExternalReceiptChecksKindGuardAndUnresolvedCompletion(t *testing.T) {
	store, binding, _ := fixture(t)
	ctx := context.Background()
	if _, err := store.Start(ctx, binding, "network", "test", marshal(nil), Baseline{}); err != nil {
		t.Fatal(err)
	}
	n := NetworkIntent{RunID: "network", Lane: "site", Kind: "http", Digest: "bound-request", Guard: "site-action"}
	if _, _, err := store.BeginNetwork(ctx, binding, n); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*NetworkIntent){func(n *NetworkIntent) { n.Kind = "browser" }, func(n *NetworkIntent) { n.Guard = "different-site-action" }} {
		forged := n
		change(&forged)
		if err := store.CompleteNetwork(ctx, binding, forged, marshal("unrelated response"), ""); !errors.Is(err, ErrConflict) {
			t.Fatal("changed network identity accepted", err)
		}
		if err := store.Unknown(ctx, binding, forged); !errors.Is(err, ErrConflict) {
			t.Fatal("changed network identity altered guard", err)
		}
	}
	if err := store.Finish(ctx, binding, n.RunID, marshal(nil), marshal(nil), "completed"); !errors.Is(err, ErrUncertain) {
		t.Fatal("pending remote intent marked completed", err)
	}
	if err := store.Finish(ctx, binding, n.RunID, marshal(nil), marshal(nil), "discarded"); !errors.Is(err, ErrConflict) {
		t.Fatal("arbitrary phase accepted", err)
	}
	if err := store.Unknown(ctx, binding, n); err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(ctx, binding, n.RunID, marshal(nil), marshal(nil), "completed"); !errors.Is(err, ErrUncertain) {
		t.Fatal("unknown remote outcome marked completed", err)
	}
	if err := store.Finish(ctx, binding, n.RunID, marshal(nil), marshal(nil), "interrupted"); err != nil {
		t.Fatal(err)
	}
	if err := store.Reconcile(ctx, binding, n.RunID, n.Lane, 0, marshal("trusted observed response")); err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(ctx, binding, n.RunID, marshal(nil), marshal(nil), "completed"); err != nil {
		t.Fatal(err)
	}
	forged := n
	forged.Guard = "other-site"
	if _, _, err := store.BeginNetwork(ctx, binding, forged); !errors.Is(err, ErrConflict) {
		t.Fatal("known network receipt replay changed guard", err)
	}
	state, err := store.State(ctx, binding)
	if err != nil || state.ActiveRun != "" || len(state.Blocks) != 0 || state.Runs[n.RunID].Lanes[n.Lane][0].Kind != "http" {
		t.Fatal("trusted reconciliation did not retain exact source request", err)
	}
}

func TestJournalUnknownCannotRewriteLocalEffect(t *testing.T) {
	store, binding, _ := fixture(t)
	ctx := context.Background()
	if _, err := store.Start(ctx, binding, "local", "test", marshal(nil), Baseline{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(ctx, binding, Effect{RunID: "local", Lane: "main", Kind: "clock", Digest: "clock-digest", Value: marshal("2026-01-02")}); err != nil {
		t.Fatal(err)
	}
	if err := store.Unknown(ctx, binding, NetworkIntent{RunID: "local", Lane: "main", Kind: "clock", Digest: "clock-digest"}); !errors.Is(err, ErrConflict) {
		t.Fatal("local fact was reclassified as network uncertainty", err)
	}
	state, err := store.State(ctx, binding)
	if err != nil || len(state.Blocks) != 0 || state.Runs["local"].Lanes["main"][0].Phase != "applied" {
		t.Fatal("known local receipt changed", err)
	}
}

func TestJournalCorruptStateCannotPanicOrAcquireNewEffects(t *testing.T) {
	for _, which := range []string{"schema", "history", "cache", "cache_ready", "stats", "runs", "blocks", "jobs", "run_id", "lanes"} {
		t.Run(which, func(t *testing.T) {
			store, binding, _ := fixture(t)
			ctx := context.Background()
			if _, err := store.Start(ctx, binding, "active", "test", marshal(nil), Baseline{}); err != nil {
				t.Fatal(err)
			}
			if err := store.update(ctx, func(s *State) error {
				switch which {
				case "schema":
					s.Schema = 2
				case "history":
					s.History = nil
				case "cache":
					s.Cache = nil
				case "cache_ready":
					s.CacheReady = nil
				case "stats":
					s.Stats = nil
				case "runs":
					s.Runs = nil
				case "blocks":
					s.Blocks = nil
				case "jobs":
					s.Jobs = nil
				case "run_id":
					s.Runs["active"].ID = "different"
				case "lanes":
					s.Runs["active"].Lanes = nil
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Apply(ctx, binding, Effect{RunID: "active", Lane: "main", Kind: "config", Digest: "new", Mutation: Mutation{Kind: "config", Value: marshal(map[string]bool{"unexpected": true})}}); !errors.Is(err, ErrStore) {
				t.Fatal("corrupt persistent state was used", err)
			}
		})
	}
}
