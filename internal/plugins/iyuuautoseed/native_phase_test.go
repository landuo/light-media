package iyuuautoseed

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestJournalPostIntentGuardHasPrivateNoDispatchProof(t *testing.T) {
	ctx := context.Background()
	j, e := OpenJournal(filepath.Join(t.TempDir(), "journal.sqlite"), bytes.Repeat([]byte{48}, 32))
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	id := RunIdentity{Instance: "Owned", Generation: 1, Revision: 1, Fingerprint: "owned", Run: "guard-between-intent-and-call"}
	checks := 0
	guard := func(context.Context) error {
		checks++
		if checks >= 3 {
			return ErrNativeOwnerDenied
		}
		return nil
	}
	run, e := j.Begin(ctx, id, nil, guard)
	if e != nil {
		t.Fatal(e)
	}
	var result any
	calls := 0
	e = run.step(ctx, "native-owned-write", nil, true, &result, func() (any, string, error) { calls++; return true, "confirmed", nil })
	if e == nil || calls != 0 {
		t.Fatal("post-intent denial delegated mutation", calls, e)
	}
	unknown, e := j.HasUnconfirmed(ctx, id.Instance, id.Generation)
	if e != nil || unknown {
		t.Fatal("known callback never entered but left unknown", unknown, e)
	}
	replay, e := j.Begin(ctx, id, nil, liveGuard)
	if e != nil {
		t.Fatal(e)
	}
	e = replay.step(ctx, "native-owned-write", nil, true, &result, func() (any, string, error) { t.Fatal("known rejected callback replayed"); return nil, "", nil })
	if !errors.Is(e, ErrNativeOwnerDenied) {
		t.Fatal("replay swallowed original owner denial", e)
	}
}
func TestJournalRejectsForgedNoDispatchError(t *testing.T) {
	ctx := context.Background()
	j, e := OpenJournal(filepath.Join(t.TempDir(), "journal.sqlite"), bytes.Repeat([]byte{49}, 32))
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	id := RunIdentity{Instance: "Owned", Generation: 1, Revision: 1, Fingerprint: "owned", Run: "forged-no-dispatch"}
	run, e := j.Begin(ctx, id, nil, liveGuard)
	if e != nil {
		t.Fatal(e)
	}
	var result any
	e = run.step(ctx, "native-owned-write", nil, true, &result, func() (any, string, error) { return false, "rejected", &nativeStopped{class: "owner_denied"} })
	if !errors.Is(e, ErrUnconfirmed) {
		t.Fatal("unsealed error became native proof", e)
	}
}
