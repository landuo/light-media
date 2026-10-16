package seedhost

import (
	"context"
	"errors"
	"testing"
)

func TestTrustedRebindPreservesWarmObjectAndRejectsForeignBackend(t *testing.T) {
	f := newFixture(t, nil, nil)
	f.config(t, map[string]any{"enabled": true})
	before, err := f.o.RuntimeConfig(context.Background())
	if err != nil || len(before) == 0 {
		t.Fatal(err)
	}
	inv := f.run(t, "trusted-rebind", "init", "")
	opts := f.o.opts
	opts.Binding = inv.Binding
	if err = f.o.Rebind(context.Background(), inv, opts); err != nil {
		t.Fatal(err)
	}
	after, err := f.o.RuntimeConfig(context.Background())
	if err != nil || string(after) != string(before) {
		t.Fatal("rebind replaced ordinary source object", err)
	}
	foreign, err := NewSQLJournal(f.db, f.key, "foreign-scope")
	if err != nil {
		t.Fatal(err)
	}
	opts.Journal = foreign
	if err = f.o.Rebind(context.Background(), inv, opts); !errors.Is(err, ErrUnsafe) {
		t.Fatal("rebind borrowed another journal", err)
	}
}
