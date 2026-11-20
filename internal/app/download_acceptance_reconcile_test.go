package app

import (
	"context"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/download"
)

// Recovery from a lost accept receipt is gated on three independent facts.
// Any one of them missing must leave the intent exactly where it was.
func TestReconcileAcceptanceRequiresAllEvidence(t *testing.T) {
	const hash = "6b6a6aaf7dc53e416fe03ac50882e5592861280d"
	owned := download.Task{Hash: hash, TotalSize: 769180886, Tags: []string{"light-media"}}
	in := Intent{ID: "dl_probe", State: "reconcile_pending", Hash: hash}

	cases := []struct {
		name string
		task download.Task
		want bool
	}{
		{"all evidence present", owned, true},
		{"tag missing", download.Task{Hash: hash, TotalSize: 1, Tags: []string{"other"}}, false},
		{"foreign tag only", download.Task{Hash: hash, TotalSize: 1, Tags: []string{"已整理"}}, false},
		{"size unknown", download.Task{Hash: hash, TotalSize: 0, Tags: []string{"light-media"}}, false},
		{"hash mismatch", download.Task{Hash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TotalSize: 1, Tags: []string{"light-media"}}, false},
		{"short hash cannot prove ownership", download.Task{Hash: "6b6a6aaf", TotalSize: 1, Tags: []string{"light-media"}}, false},
	}
	for _, c := range cases {
		if got := downloadOwnedByObservation(c.task, in); got != c.want {
			t.Fatalf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// The recovery only touches a pending reconciliation, never a live or finished one.
func TestConfirmReconciledAcceptanceOnlyAdvancesPending(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	const hash = "6b6a6aaf7dc53e416fe03ac50882e5592861280d"
	task := download.Task{Hash: hash, TotalSize: 769180886, Progress: 1, Tags: []string{"light-media"}}

	for _, state := range []string{"accepted", "downloading", "downloaded", "imported", "cancelled", "planned"} {
		in := Intent{ID: "dl_" + state, State: state, Hash: hash}
		if err := a.Store.Put(ctx, "intent", in.ID, in); err != nil {
			t.Fatal(err)
		}
		advanced, err := a.confirmReconciledAcceptance(ctx, in, task)
		if err != nil {
			t.Fatal(err)
		}
		if advanced {
			t.Fatalf("%s must not be advanced by the reconcile path", state)
		}
	}

	pending := Intent{ID: "dl_pending", State: "reconcile_pending", Hash: hash, Media: catalog.Media{ID: 206484, MediaType: "tv"}, Season: 4}
	if err := a.Store.Put(ctx, "intent", pending.ID, pending); err != nil {
		t.Fatal(err)
	}
	advanced, err := a.confirmReconciledAcceptance(ctx, pending, task)
	if err != nil {
		t.Fatal(err)
	}
	if !advanced {
		t.Fatal("a pending reconciliation with full evidence must advance")
	}
	var stored Intent
	if err := a.Store.Get(ctx, "intent", pending.ID, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.State != "downloaded" {
		t.Fatalf("state = %q, want downloaded", stored.State)
	}
}
