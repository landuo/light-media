package brushledger

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
)

func TestCursorMatchesAuthenticatedIterationAndReleasesSingleConnection(t *testing.T) {
	db, s := fixture(t)
	root := s.Empty(Effects)
	ctx := context.Background()
	transaction(t, db, func(tx *sql.Tx) {
		for i := 0; i < 257; i++ {
			root = put(t, s, tx, Effects, root, fmt.Sprintf("run:%d", i), bytes.Repeat([]byte{byte(i)}, i*300))
		}
	})
	var want []string
	transaction(t, db, func(tx *sql.Tx) {
		if e := s.Iterate(ctx, tx, Effects, root, func(k string, _ []byte) error { want = append(want, k); return nil }); e != nil {
			t.Fatal(e)
		}
	})
	cursor := ""
	n := 0
	for {
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		key, value, next, ok, e := s.Next(ctx, tx, Effects, root, cursor)
		tx.Rollback()
		if e != nil {
			t.Fatal(e)
		}
		if !ok {
			break
		}
		if n >= len(want) || key != want[n] || next <= cursor {
			t.Fatal("cursor order or membership changed", n)
		}
		// Simulates the owner yielding to a fresh transaction/guard or network callback.
		transaction(t, db, func(tx *sql.Tx) { expect(t, s, tx, Effects, root, key, value, true) })
		cursor = next
		n++
	}
	if n != len(want) {
		t.Fatal(n, len(want))
	}
	transaction(t, db, func(tx *sql.Tx) {
		_, _, _, _, e := s.Next(ctx, tx, Effects, root, "not-a-native-path")
		if !errors.Is(e, ErrInput) {
			t.Fatal(e)
		}
		_, _, _, _, e = s.Next(ctx, tx, Runs, root, "")
		if !errors.Is(e, ErrIntegrity) {
			t.Fatal(e)
		}
	})
}
