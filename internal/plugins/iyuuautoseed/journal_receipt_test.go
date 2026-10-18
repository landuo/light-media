package iyuuautoseed

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func receiptFixture(t *testing.T) (*Journal, RunIdentity, *JournalRun, string, map[string]string) {
	t.Helper()
	j, id, r := integrityJournal(t)
	key := "outbox/" + r.key + "/1"
	value := map[string]string{"type": "SiteMessage", "title": "synthetic title", "text": "synthetic text"}
	if e := r.localWrite(context.Background(), "notification-outbox", key, value); e != nil {
		t.Fatal(e)
	}
	return j, id, r, key, value
}
func receiptVerify(t *testing.T, j *Journal, id RunIdentity, seq int, op, key string, value any) error {
	t.Helper()
	tx, e := j.db.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	return j.ValidateLocalReceipt(context.Background(), tx, id, seq, op, key, value)
}

func TestJournalReceiptExactScopeAndValue(t *testing.T) {
	j, id, _, key, value := receiptFixture(t)
	if e := receiptVerify(t, j, id, 1, "notification-outbox", key, value); e != nil {
		t.Fatal(e)
	}
	tests := []struct {
		name    string
		id      RunIdentity
		seq     int
		op, key string
		value   any
	}{
		{"wrong-sequence", id, 2, "notification-outbox", key, value},
		{"zero-sequence", id, 0, "notification-outbox", key, value},
		{"wrong-operation", id, 1, "history", key, value},
		{"wrong-key", id, 1, "notification-outbox", key + "changed", value},
		{"wrong-value", id, 1, "notification-outbox", key, map[string]string{"type": "SiteMessage", "title": "changed", "text": "synthetic text"}},
	}
	for _, field := range []string{"instance", "generation", "revision", "fingerprint", "run"} {
		changed := id
		switch field {
		case "instance":
			changed.Instance = "different"
		case "generation":
			changed.Generation++
		case "revision":
			changed.Revision++
		case "fingerprint":
			changed.Fingerprint = "different"
		case "run":
			changed.Run = "different"
		}
		tests = append(tests, struct {
			name    string
			id      RunIdentity
			seq     int
			op, key string
			value   any
		}{"wrong-" + field, changed, 1, "notification-outbox", key, value})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if e := receiptVerify(t, j, tc.id, tc.seq, tc.op, tc.key, tc.value); e == nil {
				t.Fatal("accepted mismatched receipt")
			}
		})
	}
	if e := j.ValidateLocalReceipt(context.Background(), nil, id, 1, "notification-outbox", key, value); e == nil {
		t.Fatal("accepted nil transaction")
	}
}

func TestJournalReceiptRejectsReadMutationAndChangedCells(t *testing.T) {
	tests := []struct {
		name, query string
		args        func(*Journal, *JournalRun, string) []any
	}{
		{"receipt-delete", "DELETE FROM iyuu_steps WHERE run=? AND seq=1", func(_ *Journal, r *JournalRun, _ string) []any { return []any{r.key} }},
		{"receipt-result", "UPDATE iyuu_steps SET result=X'00' WHERE run=? AND seq=1", func(_ *Journal, r *JournalRun, _ string) []any { return []any{r.key} }},
		{"receipt-kind", "UPDATE iyuu_steps SET kind='read' WHERE run=? AND seq=1", func(_ *Journal, r *JournalRun, _ string) []any { return []any{r.key} }},
		{"value-delete", "DELETE FROM iyuu_values WHERE owner=? AND key=?", func(j *Journal, r *JournalRun, key string) []any {
			return []any{j.ownerKey(r.identity.Instance, r.identity.Generation), key}
		}},
		{"value-forged", "UPDATE iyuu_values SET value=X'00' WHERE owner=? AND key=?", func(j *Journal, r *JournalRun, key string) []any {
			return []any{j.ownerKey(r.identity.Instance, r.identity.Generation), key}
		}},
		{"run-delete", "DELETE FROM iyuu_runs WHERE id=?", func(_ *Journal, r *JournalRun, _ string) []any { return []any{r.key} }},
		{"run-head-delete", "DELETE FROM iyuu_run_manifests WHERE namespace=? AND run=?", func(j *Journal, r *JournalRun, _ string) []any { return []any{j.namespace, r.key} }},
		{"owner-head-delete", "DELETE FROM iyuu_owner_manifests WHERE namespace=? AND owner=?", func(j *Journal, r *JournalRun, _ string) []any {
			return []any{j.namespace, j.ownerKey(r.identity.Instance, r.identity.Generation)}
		}},
		{"root-delete", "DELETE FROM iyuu_registry WHERE namespace=?", func(j *Journal, _ *JournalRun, _ string) []any { return []any{j.namespace} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			j, id, r, key, value := receiptFixture(t)
			integrityExec(t, j, tc.query, tc.args(j, r, key)...)
			if e := receiptVerify(t, j, id, 1, "notification-outbox", key, value); e == nil {
				t.Fatal("accepted unauthenticated receipt/value")
			}
		})
	}
	for _, mutation := range []bool{false, true} {
		t.Run(fmt.Sprintf("real-nonlocal-%v", mutation), func(t *testing.T) {
			j, id, r := integrityJournal(t)
			var out bool
			if e := r.step(context.Background(), "notification-outbox", nil, mutation, &out, func() (any, string, error) { return true, "confirmed", nil }); e != nil {
				t.Fatal(e)
			}
			if e := receiptVerify(t, j, id, 1, "notification-outbox", "outbox/nonlocal", nil); e == nil {
				t.Fatal("native/read receipt used as local source receipt")
			}
		})
	}
}

func TestJournalReceiptMutableKeyNeedsCurrentValue(t *testing.T) {
	j, id, r, key, value := receiptFixture(t)
	updated := map[string]string{"type": "SiteMessage", "title": "later", "text": "later"}
	if e := r.localWrite(context.Background(), "notification-outbox", key, updated); e != nil {
		t.Fatal(e)
	}
	if e := receiptVerify(t, j, id, 1, "notification-outbox", key, value); e == nil {
		t.Fatal("old overwritten value verified as current")
	}
	if e := receiptVerify(t, j, id, 2, "notification-outbox", key, updated); e != nil {
		t.Fatal(e)
	}
}

func TestJournalReceiptNumberIdentityAndNamespace(t *testing.T) {
	j, id, r := integrityJournal(t)
	ctx := context.Background()
	key := "outbox/" + r.key + "/1"
	value := map[string]any{"large": json.Number("9007199254740993"), "decimal": json.Number("1.0"), "unicode": "通知"}
	if e := r.localWrite(ctx, "notification-outbox", key, value); e != nil {
		t.Fatal(e)
	}
	if e := receiptVerify(t, j, id, 1, "notification-outbox", key, value); e != nil {
		t.Fatal(e)
	}
	if e := receiptVerify(t, j, id, 1, "notification-outbox", key, map[string]any{"large": json.Number("9007199254740993"), "decimal": json.Number("1"), "unicode": "通知"}); e == nil {
		t.Fatal("number lexical identity lost")
	}
	other, e := NewJournalVerifier(j.db, bytes.Repeat([]byte{9}, 32))
	if e != nil {
		t.Fatal(e)
	}
	tx, e := j.db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = other.ValidateLocalReceipt(ctx, tx, id, 1, "notification-outbox", key, value); e == nil {
		t.Fatal("accepted different owner-key namespace")
	}
}

func TestJournalReceiptVerifierConstructionDoesNotInitialize(t *testing.T) {
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "empty.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, e = NewJournalVerifier(db, bytes.Repeat([]byte{1}, 32)); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'iyuu_%'").Scan(&count); e != nil || count != 0 {
		t.Fatalf("verifier initialized schema: %d %v", count, e)
	}
	if _, e = NewJournalVerifier(nil, bytes.Repeat([]byte{1}, 32)); e == nil {
		t.Fatal("accepted nil DB")
	}
	if _, e = NewJournalVerifier(db, []byte("wrong length")); e == nil {
		t.Fatal("accepted wrong key")
	}
}

func TestJournalReceiptExistingTxAndBlockedJournalReader(t *testing.T) {
	j, id, _, key, value := receiptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, e := j.db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	var before []byte
	if e = tx.QueryRowContext(ctx, "SELECT root FROM iyuu_registry WHERE namespace=?", j.namespace).Scan(&before); e != nil {
		t.Fatal(e)
	}
	readDone := make(chan error, 1)
	go func() { _, _, e := j.ReadCheckpoint(ctx, id.Instance, id.Generation); readDone <- e }()
	deadline := time.Now().Add(time.Second)
	for j.mu.TryLock() {
		j.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("reader did not acquire journal mutex")
		}
		time.Sleep(time.Millisecond)
	}
	verified := make(chan error, 1)
	go func() {
		v, e := NewJournalVerifier(j.db, bytes.Repeat([]byte{67}, 32))
		if e == nil {
			e = v.ValidateLocalReceipt(ctx, tx, id, 1, "notification-outbox", key, value)
		}
		verified <- e
	}()
	select {
	case e = <-verified:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		_ = tx.Rollback()
		t.Fatal("verifier attempted DB pool/mutex instead of supplied transaction")
	}
	var after []byte
	if e = tx.QueryRowContext(ctx, "SELECT root FROM iyuu_registry WHERE namespace=?", j.namespace).Scan(&after); e != nil || !bytes.Equal(before, after) {
		t.Fatalf("validation changed root: %v", e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if e = <-readDone; e != nil {
		t.Fatal(e)
	}
}

func TestJournalReceiptConcurrentValidationAndLocalWriters(t *testing.T) {
	j, id, r, key, value := receiptFixture(t)
	ctx := context.Background()
	verifier, e := NewJournalVerifier(j.db, bytes.Repeat([]byte{67}, 32))
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				tx, e := j.db.BeginTx(ctx, nil)
				if e != nil {
					errs <- e
					return
				}
				e = verifier.ValidateLocalReceipt(ctx, tx, id, 1, "notification-outbox", key, value)
				_ = tx.Rollback()
				if e != nil {
					errs <- e
					return
				}
			}
			errs <- nil
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			if e := r.localWrite(ctx, "history", fmt.Sprintf("history/%d", i), i); e != nil {
				errs <- e
				return
			}
		}
		errs <- nil
	}()
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if e := receiptVerify(t, j, id, 1, "notification-outbox", key, value); e != nil {
		t.Fatal(e)
	}
}
