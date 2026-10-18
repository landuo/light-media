package torrenttransfer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func integrityGuard(ctx context.Context) error { return ctx.Err() }

func integrityJournal(t *testing.T) (*Journal, RunIdentity, *JournalRun) {
	t.Helper()
	j, e := OpenJournal(filepath.Join(t.TempDir(), "journal.sqlite"), bytes.Repeat([]byte{67}, 32))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = j.Close() })
	id := RunIdentity{Instance: "owned-instance", Generation: 1, Revision: 4, Fingerprint: "synthetic-input", Run: "owned-run"}
	r, e := j.Begin(context.Background(), id, map[string]any{"n": json.Number("9007199254740993")}, integrityGuard)
	if e != nil {
		t.Fatal(e)
	}
	return j, id, r
}
func integrityInitial() map[string]any { return map[string]any{"n": json.Number("9007199254740993")} }
func integrityExec(t *testing.T, j *Journal, query string, args ...any) {
	t.Helper()
	if _, e := j.db.Exec(query, args...); e != nil {
		t.Fatal(e)
	}
}
func integrityMutation(t *testing.T, r *JournalRun, calls *int) {
	t.Helper()
	var out bool
	if e := r.step(context.Background(), "selected-write", map[string]string{"hash": "synthetic"}, true, &out, func() (any, string, error) { *calls++; return true, "confirmed", nil }); e != nil || !out {
		t.Fatalf("mutation=%v err=%v", out, e)
	}
}
func integrityTrigger(t *testing.T, j *Journal) {
	t.Helper()
	integrityExec(t, j, fmt.Sprintf("CREATE TRIGGER owned_fail_root BEFORE UPDATE OF root ON torrenttransfer_registry WHEN NEW.namespace='%s' BEGIN SELECT RAISE(ABORT,'owned manifest failure'); END", j.namespace))
}

func TestJournalIntegrityTamperNeverReissues(t *testing.T) {
	tests := []struct {
		name, query string
		args        func(*Journal, *JournalRun) []any
	}{
		{"run-delete", "DELETE FROM torrenttransfer_runs WHERE id=?", func(_ *Journal, r *JournalRun) []any { return []any{r.key} }},
		{"run-binding", "UPDATE torrenttransfer_runs SET binding=X'00' WHERE id=?", func(_ *Journal, r *JournalRun) []any { return []any{r.key} }},
		{"run-initial", "UPDATE torrenttransfer_runs SET initial=X'00' WHERE id=?", func(_ *Journal, r *JournalRun) []any { return []any{r.key} }},
		{"run-started", "UPDATE torrenttransfer_runs SET started=started+1 WHERE id=?", func(_ *Journal, r *JournalRun) []any { return []any{r.key} }},
		{"run-manifest-delete", "DELETE FROM torrenttransfer_run_manifests WHERE namespace=? AND run=?", func(j *Journal, r *JournalRun) []any { return []any{j.namespace, r.key} }},
		{"run-manifest-forged", "UPDATE torrenttransfer_run_manifests SET body=X'00' WHERE namespace=? AND run=?", func(j *Journal, r *JournalRun) []any { return []any{j.namespace, r.key} }},
		{"owner-manifest-delete", "DELETE FROM torrenttransfer_owner_manifests WHERE namespace=? AND owner=?", func(j *Journal, r *JournalRun) []any {
			return []any{j.namespace, j.ownerKey(r.identity.Instance, r.identity.Generation)}
		}},
		{"owner-manifest-forged", "UPDATE torrenttransfer_owner_manifests SET body=X'00' WHERE namespace=? AND owner=?", func(j *Journal, r *JournalRun) []any {
			return []any{j.namespace, j.ownerKey(r.identity.Instance, r.identity.Generation)}
		}},
		{"root-delete", "DELETE FROM torrenttransfer_registry WHERE namespace=?", func(j *Journal, _ *JournalRun) []any { return []any{j.namespace} }},
		{"root-forged", "UPDATE torrenttransfer_registry SET root=X'00' WHERE namespace=?", func(j *Journal, _ *JournalRun) []any { return []any{j.namespace} }},
		{"binding-delete", "DELETE FROM torrenttransfer_values WHERE owner=? AND key=?", func(j *Journal, r *JournalRun) []any {
			return []any{j.ownerKey(r.identity.Instance, r.identity.Generation), "binding/" + r.key + "/credentials"}
		}},
		{"binding-forged", "UPDATE torrenttransfer_values SET value=X'00' WHERE owner=? AND key=?", func(j *Journal, r *JournalRun) []any {
			return []any{j.ownerKey(r.identity.Instance, r.identity.Generation), "binding/" + r.key + "/credentials"}
		}},
		{"history-delete", "DELETE FROM torrenttransfer_values WHERE owner=? AND key='history/owned'", func(j *Journal, r *JournalRun) []any {
			return []any{j.ownerKey(r.identity.Instance, r.identity.Generation)}
		}},
		{"checkpoint-delete", "DELETE FROM torrenttransfer_values WHERE owner=? AND key='runtime/1'", func(j *Journal, r *JournalRun) []any {
			return []any{j.ownerKey(r.identity.Instance, r.identity.Generation)}
		}},
		{"step-delete", "DELETE FROM torrenttransfer_steps WHERE run=? AND seq=3", func(_ *Journal, r *JournalRun) []any { return []any{r.key} }},
		{"step-request", "UPDATE torrenttransfer_steps SET request='forged' WHERE run=? AND seq=3", func(_ *Journal, r *JournalRun) []any { return []any{r.key} }},
		{"step-kind", "UPDATE torrenttransfer_steps SET kind='read' WHERE run=? AND seq=3", func(_ *Journal, r *JournalRun) []any { return []any{r.key} }},
		{"step-state", "UPDATE torrenttransfer_steps SET state='pending',result=NULL WHERE run=? AND seq=3", func(_ *Journal, r *JournalRun) []any { return []any{r.key} }},
		{"step-result", "UPDATE torrenttransfer_steps SET result=X'00' WHERE run=? AND seq=3", func(_ *Journal, r *JournalRun) []any { return []any{r.key} }},
		{"step-sequence", "UPDATE torrenttransfer_steps SET seq=7 WHERE run=? AND seq=3", func(_ *Journal, r *JournalRun) []any { return []any{r.key} }},
		{"local-kind", "UPDATE torrenttransfer_steps SET kind='read' WHERE run=? AND seq=1", func(_ *Journal, r *JournalRun) []any { return []any{r.key} }},
		{"local-result", "UPDATE torrenttransfer_steps SET result=X'00' WHERE run=? AND seq=1", func(_ *Journal, r *JournalRun) []any { return []any{r.key} }},
		{"extra-value", "INSERT INTO torrenttransfer_values(owner,key,value) VALUES(?,'history/injected',X'00')", func(j *Journal, r *JournalRun) []any {
			return []any{j.ownerKey(r.identity.Instance, r.identity.Generation)}
		}},
		{"extra-step", "INSERT INTO torrenttransfer_steps(run,seq,request,kind,state,result) VALUES(?,4,'forged','local','complete',X'00')", func(_ *Journal, r *JournalRun) []any { return []any{r.key} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			j, id, r := integrityJournal(t)
			ctx := context.Background()
			if e := r.bind(ctx, "credentials", map[string]string{"cookie": "synthetic"}); e != nil {
				t.Fatal(e)
			}
			if e := r.localWrite(ctx, "history", "history/owned", map[string]string{"seed": "synthetic"}); e != nil {
				t.Fatal(e)
			}
			if e := r.Checkpoint(ctx, RuntimeCheckpoint{Revision: 4}); e != nil {
				t.Fatal(e)
			}
			calls := 0
			integrityMutation(t, r, &calls)
			integrityExec(t, j, tc.query, tc.args(j, r)...)
			if _, e := j.Begin(ctx, id, integrityInitial(), integrityGuard); !errors.Is(e, ErrJournalIntegrity) {
				t.Fatalf("accepted changed journal: %v", e)
			}
			var out bool
			if e := r.step(ctx, "another-write", nil, true, &out, func() (any, string, error) { calls++; return true, "confirmed", nil }); !errors.Is(e, ErrJournalIntegrity) {
				t.Fatalf("continued changed journal: %v", e)
			}
			if calls != 1 {
				t.Fatalf("reissued mutation %d", calls)
			}
		})
	}
}

func TestJournalIntegrityPlaintextCompleteCannotForgeLocalCommit(t *testing.T) {
	j, id, r := integrityJournal(t)
	ctx := context.Background()
	commits := 0
	raw, _ := json.Marshal(struct {
		Op, Key string
		Value   any
	}{"config", "config", map[string]any{"enabled": true}})
	integrityExec(t, j, "INSERT INTO torrenttransfer_steps(run,seq,request,kind,state,result) VALUES(?,1,?,'local','complete',X'00')", r.key, digest(raw))
	r.LocalCommit = func(context.Context, *sql.Tx, RunIdentity, string, string, any) (func(), error) {
		commits++
		return nil, nil
	}
	if e := r.SourceConfig(ctx, map[string]any{"enabled": true}); !errors.Is(e, ErrJournalIntegrity) {
		t.Fatal(e)
	}
	if commits != 0 {
		t.Fatal("forged row reached actual local effect")
	}
	if _, _, e := j.ReadRunInitial(ctx, id); e == nil {
		t.Fatal("forged complete accepted")
	}
}

func TestJournalIntegrityAtomicIntentAndCompletion(t *testing.T) {
	t.Run("intent-failure-does-not-dispatch", func(t *testing.T) {
		j, id, r := integrityJournal(t)
		ctx := context.Background()
		integrityTrigger(t, j)
		calls := 0
		var out bool
		e := r.step(ctx, "selected-write", nil, true, &out, func() (any, string, error) { calls++; return true, "confirmed", nil })
		if e == nil || calls != 0 {
			t.Fatalf("calls=%d err=%v", calls, e)
		}
		integrityExec(t, j, "DROP TRIGGER owned_fail_root")
		unknown, e := j.HasUnconfirmed(ctx, id.Instance, id.Generation)
		if e != nil || unknown {
			t.Fatalf("rolled-back intent persisted: %v %v", unknown, e)
		}
		r, e = j.Begin(ctx, id, integrityInitial(), integrityGuard)
		if e != nil {
			t.Fatal(e)
		}
		if e = r.step(ctx, "selected-write", nil, true, &out, func() (any, string, error) { calls++; return true, "confirmed", nil }); e != nil || calls != 1 {
			t.Fatalf("safe retry=%d err=%v", calls, e)
		}
	})
	t.Run("completion-failure-keeps-pending-barrier", func(t *testing.T) {
		j, id, r := integrityJournal(t)
		ctx := context.Background()
		calls := 0
		var out bool
		e := r.step(ctx, "selected-write", nil, true, &out, func() (any, string, error) { calls++; integrityTrigger(t, j); return true, "confirmed", nil })
		if e == nil || calls != 1 {
			t.Fatalf("calls=%d err=%v", calls, e)
		}
		integrityExec(t, j, "DROP TRIGGER owned_fail_root")
		unknown, e := j.HasUnconfirmed(ctx, id.Instance, id.Generation)
		if e != nil || !unknown {
			t.Fatalf("lost intent barrier: %v %v", unknown, e)
		}
		r, e = j.Begin(ctx, id, integrityInitial(), integrityGuard)
		if e != nil {
			t.Fatal(e)
		}
		e = r.step(ctx, "selected-write", nil, true, &out, func() (any, string, error) { calls++; return true, "confirmed", nil })
		if !errors.Is(e, ErrUnconfirmed) || calls != 1 {
			t.Fatalf("reissued ambiguous mutation: %d %v", calls, e)
		}
	})
	t.Run("deleted-pending-is-integrity-error", func(t *testing.T) {
		j, id, r := integrityJournal(t)
		ctx := context.Background()
		var out bool
		func() {
			defer func() {
				if recover() == nil {
					t.Error("expected synthetic crash")
				}
			}()
			_ = r.step(ctx, "selected-write", nil, true, &out, func() (any, string, error) { panic("synthetic crash") })
		}()
		integrityExec(t, j, "DELETE FROM torrenttransfer_steps WHERE run=?", r.key)
		if _, e := j.Begin(ctx, id, integrityInitial(), integrityGuard); !errors.Is(e, ErrJournalIntegrity) {
			t.Fatal(e)
		}
		if _, e := j.HasUnconfirmed(ctx, id.Instance, id.Generation); !errors.Is(e, ErrJournalIntegrity) {
			t.Fatal(e)
		}
	})
}

func TestJournalIntegrityLocalEffectAndReceiptShareTransaction(t *testing.T) {
	j, id, r := integrityJournal(t)
	ctx := context.Background()
	integrityExec(t, j, "CREATE TABLE owned_app_effect(id INTEGER PRIMARY KEY, value TEXT NOT NULL)")
	after := 0
	r.LocalCommit = func(ctx context.Context, tx *sql.Tx, _ RunIdentity, _ string, _ string, _ any) (func(), error) {
		if _, e := tx.ExecContext(ctx, "INSERT INTO owned_app_effect(id,value) VALUES(1,'synthetic')"); e != nil {
			return nil, e
		}
		return func() { after++ }, nil
	}
	integrityTrigger(t, j)
	if e := r.SourceConfig(ctx, map[string]any{"enabled": true}); e == nil {
		t.Fatal("expected injected commit failure")
	}
	var count int
	if e := j.db.QueryRow("SELECT COUNT(*) FROM owned_app_effect").Scan(&count); e != nil {
		t.Fatal(e)
	}
	if count != 0 || after != 0 {
		t.Fatalf("partial App CAS: %d %d", count, after)
	}
	integrityExec(t, j, "DROP TRIGGER owned_fail_root")
	r, e := j.Begin(ctx, id, integrityInitial(), integrityGuard)
	if e != nil {
		t.Fatal(e)
	}
	r.LocalCommit = func(ctx context.Context, tx *sql.Tx, _ RunIdentity, _ string, _ string, _ any) (func(), error) {
		if _, e := tx.ExecContext(ctx, "INSERT INTO owned_app_effect(id,value) VALUES(1,'synthetic')"); e != nil {
			return nil, e
		}
		// A checkpoint published from the same App hook must not be lost when
		// localWrite subsequently publishes its receipt and config value.
		if e := j.WriteCheckpointTx(ctx, tx, id.Instance, id.Generation, RuntimeCheckpoint{Revision: 5, Run: id.Run, Identity: id}); e != nil {
			return nil, e
		}
		return func() { after++ }, nil
	}
	if e = r.SourceConfig(ctx, map[string]any{"enabled": true}); e != nil {
		t.Fatal(e)
	}
	checkpoint, found, e := j.ReadCheckpoint(ctx, id.Instance, id.Generation)
	if e != nil || !found || checkpoint.Revision != 5 {
		t.Fatalf("lost nested checkpoint: %v %v %+v", found, e, checkpoint)
	}
	if e = j.db.QueryRow("SELECT COUNT(*) FROM owned_app_effect").Scan(&count); e != nil || count != 1 || after != 1 {
		t.Fatalf("effect=%d after=%d err=%v", count, after, e)
	}
	r, e = j.Begin(ctx, id, integrityInitial(), integrityGuard)
	if e != nil {
		t.Fatal(e)
	}
	observed := 0
	r.LocalObserved = func(op, key string, v any) { observed++ }
	r.LocalCommit = func(context.Context, *sql.Tx, RunIdentity, string, string, any) (func(), error) {
		t.Error("repeated App CAS on replay")
		return nil, nil
	}
	if e = r.SourceConfig(ctx, map[string]any{"enabled": true}); e != nil || observed != 1 {
		t.Fatalf("replay observed=%d err=%v", observed, e)
	}
}

func TestJournalIntegrityOriginalIdentityAndReplay(t *testing.T) {
	j, id, r := integrityJournal(t)
	ctx := context.Background()
	calls := 0
	if e := r.bind(ctx, "credentials", map[string]string{"cookie": "synthetic"}); e != nil {
		t.Fatal(e)
	}
	integrityMutation(t, r, &calls)
	for _, v := range []string{"first", "latest"} {
		if e := r.localWrite(ctx, "history", "history/owned", v); e != nil {
			t.Fatal(e)
		}
	}
	if e := r.Checkpoint(ctx, RuntimeCheckpoint{Revision: 5, Core: State{Config: map[string]any{"n": json.Number("9007199254740993")}}}); e != nil {
		t.Fatal(e)
	}
	original, found, e := j.ReadRunIdentity(ctx, id.Instance, id.Generation, id.Run)
	if e != nil || !found || original != id {
		t.Fatalf("identity=%+v found=%v err=%v", original, found, e)
	}
	changed := id
	changed.Revision++
	if _, e = j.Begin(ctx, changed, integrityInitial(), integrityGuard); !errors.Is(e, ErrReplayDiverged) {
		t.Fatalf("created new revision timeline: %v", e)
	}
	r, e = j.Begin(ctx, id, integrityInitial(), integrityGuard)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.bind(ctx, "credentials", map[string]string{"cookie": "synthetic"}); e != nil {
		t.Fatal(e)
	}
	integrityMutation(t, r, &calls)
	for _, v := range []string{"first", "latest"} {
		if e = r.localWrite(ctx, "history", "history/owned", v); e != nil {
			t.Fatal(e)
		}
	}
	if e = r.Checkpoint(ctx, RuntimeCheckpoint{Revision: 5, Core: State{Config: map[string]any{"n": json.Number("9007199254740993")}}}); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatalf("replayed external request %d", calls)
	}
	initial, found, e := j.ReadRunInitial(ctx, id)
	if e != nil || !found || !strings.Contains(string(initial), "9007199254740993") {
		t.Fatalf("initial=%s found=%v err=%v", initial, found, e)
	}
	checkpoint, found, e := j.ReadCheckpoint(ctx, id.Instance, id.Generation)
	if e != nil || !found || checkpoint.Core.Config["n"] != json.Number("9007199254740993") {
		t.Fatalf("checkpoint=%+v found=%v err=%v", checkpoint, found, e)
	}
}

func TestJournalIntegrityUnknownBarriersAndCancellation(t *testing.T) {
	for _, mode := range []string{"unknown", "rejected", "confirmed-cancelled"} {
		t.Run(mode, func(t *testing.T) {
			j, id, r := integrityJournal(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			var out bool
			e := r.step(ctx, "selected-write", nil, true, &out, func() (any, string, error) {
				calls++
				switch mode {
				case "unknown":
					return nil, "unknown", errors.New("synthetic lost response")
				case "rejected":
					return false, "rejected", nil
				default:
					cancel()
					return true, "confirmed", nil
				}
			})
			if mode == "unknown" && !errors.Is(e, ErrUnconfirmed) {
				t.Fatal(e)
			}
			if mode == "rejected" && (e != nil || out) {
				t.Fatalf("rejection=%v err=%v", out, e)
			}
			if mode == "confirmed-cancelled" && !errors.Is(e, context.Canceled) {
				t.Fatalf("cancellation=%v", e)
			}
			unknown, e := j.HasUnconfirmed(context.Background(), id.Instance, id.Generation)
			if e != nil || unknown != (mode == "unknown") {
				t.Fatalf("barrier=%v err=%v", unknown, e)
			}
			r, e = j.Begin(context.Background(), id, integrityInitial(), integrityGuard)
			if e != nil {
				t.Fatal(e)
			}
			e = r.step(context.Background(), "selected-write", nil, true, &out, func() (any, string, error) { calls++; return true, "confirmed", nil })
			if calls != 1 {
				t.Fatalf("mutation repeated %d", calls)
			}
			if mode == "unknown" && !errors.Is(e, ErrUnconfirmed) {
				t.Fatal(e)
			}
			if mode != "unknown" && e != nil {
				t.Fatal(e)
			}
			id2 := id
			id2.Run = "later-clean-run"
			if _, e = j.Begin(context.Background(), id2, nil, integrityGuard); e != nil {
				t.Fatal(e)
			}
			unknown, e = j.HasUnconfirmed(context.Background(), id.Instance, id.Generation)
			if e != nil || unknown != (mode == "unknown") {
				t.Fatalf("lost older run barrier: %v %v", unknown, e)
			}
		})
	}
	t.Run("pending-read-can-retry", func(t *testing.T) {
		j, id, r := integrityJournal(t)
		ctx := context.Background()
		var out string
		calls := 0
		func() {
			defer func() { _ = recover() }()
			_ = r.step(ctx, "selected-read", nil, false, &out, func() (any, string, error) { calls++; panic("synthetic process exit") })
		}()
		unknown, e := j.HasUnconfirmed(ctx, id.Instance, id.Generation)
		if e != nil || unknown {
			t.Fatalf("read became write barrier: %v %v", unknown, e)
		}
		r, e = j.Begin(ctx, id, integrityInitial(), integrityGuard)
		if e != nil {
			t.Fatal(e)
		}
		if e = r.step(ctx, "selected-read", nil, false, &out, func() (any, string, error) { calls++; return "read-result", "read", nil }); e != nil || out != "read-result" || calls != 2 {
			t.Fatalf("read retry %q %d %v", out, calls, e)
		}
	})
}

func TestJournalIntegrityGenerationValuesDoNotRevive(t *testing.T) {
	j, id, r := integrityJournal(t)
	ctx := context.Background()
	for _, pair := range []struct {
		op, key string
		value   any
	}{{"config", "config", map[string]any{"token": "synthetic"}}, {"history", "history/owned", "old-history"}, {"notification-outbox", "outbox/old", "old-notification"}} {
		if e := r.localWrite(ctx, pair.op, pair.key, pair.value); e != nil {
			t.Fatal(e)
		}
	}
	if e := r.Checkpoint(ctx, RuntimeCheckpoint{Revision: 4}); e != nil {
		t.Fatal(e)
	}
	next := id
	next.Generation++
	r, e := j.Begin(ctx, next, integrityInitial(), integrityGuard)
	if e != nil {
		t.Fatal(e)
	}
	for _, key := range []string{"config", "history/owned", "outbox/old"} {
		value, e := r.readValue(ctx, key)
		if e != nil || value != nil {
			t.Fatalf("revived %s=%v err=%v", key, value, e)
		}
	}
	if _, found, e := j.ReadCheckpoint(ctx, id.Instance, next.Generation); e != nil || found {
		t.Fatalf("revived checkpoint %v %v", found, e)
	}
	old, found, e := j.ReadCheckpoint(ctx, id.Instance, id.Generation)
	if e != nil || !found || old.Revision != 4 {
		t.Fatalf("old generation corrupted %v %v", found, e)
	}
}

func TestJournalIntegrityKeyNamespacesShareDatabase(t *testing.T) {
	ctx := context.Background()
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "app.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ja, e := AttachJournal(ctx, db, bytes.Repeat([]byte{1}, 32))
	if e != nil {
		t.Fatal(e)
	}
	jb, e := AttachJournal(ctx, db, bytes.Repeat([]byte{2}, 32))
	if e != nil {
		t.Fatal(e)
	}
	id := RunIdentity{Instance: "same-public-instance", Generation: 1, Revision: 1, Fingerprint: "same-input", Run: "same-public-run"}
	for index, j := range []*Journal{ja, jb} {
		r, e := j.Begin(ctx, id, nil, integrityGuard)
		if e != nil {
			t.Fatal(e)
		}
		if e = r.localWrite(ctx, "history", "history/owned", fmt.Sprintf("namespace-%d", index)); e != nil {
			t.Fatal(e)
		}
		if e = r.Checkpoint(ctx, RuntimeCheckpoint{Revision: uint64(index + 1)}); e != nil {
			t.Fatal(e)
		}
	}
	if ja.runKey(id) == jb.runKey(id) || ja.ownerKey(id.Instance, 1) == jb.ownerKey(id.Instance, 1) {
		t.Fatal("key namespace collision")
	}
	integrityExec(t, ja, "DELETE FROM torrenttransfer_registry WHERE namespace=?", ja.namespace)
	checkpoint, found, e := jb.ReadCheckpoint(ctx, id.Instance, 1)
	if e != nil || !found || checkpoint.Revision != 2 {
		t.Fatalf("other namespace damaged: %+v %v %v", checkpoint, found, e)
	}
	if _, e = AttachJournal(ctx, db, bytes.Repeat([]byte{1}, 32)); !errors.Is(e, ErrJournalIntegrity) {
		t.Fatalf("deleted registry bootstrapped over owned rows: %v", e)
	}
}

func TestJournalIntegritySharedKeyInstancesAndConcurrentHandles(t *testing.T) {
	ctx := context.Background()
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "app.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	key := bytes.Repeat([]byte{79}, 32)
	ja, e := AttachJournal(ctx, db, key)
	if e != nil {
		t.Fatal(e)
	}
	jb, e := AttachJournal(ctx, db, key)
	if e != nil {
		t.Fatal(e)
	}
	ids := []RunIdentity{{Instance: "a", Generation: 1, Revision: 1, Fingerprint: "input", Run: "run"}, {Instance: "b", Generation: 1, Revision: 1, Fingerprint: "input", Run: "run"}}
	runs := make([]*JournalRun, 2)
	for i, j := range []*Journal{ja, jb} {
		runs[i], e = j.Begin(ctx, ids[i], nil, integrityGuard)
		if e != nil {
			t.Fatal(e)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i, r := range runs {
		wg.Add(1)
		go func(i int, r *JournalRun) {
			defer wg.Done()
			for n := 0; n < 4; n++ {
				if e := r.localWrite(ctx, "history", "history/owned", fmt.Sprintf("%d/%d", i, n)); e != nil {
					errs <- e
					return
				}
			}
			errs <- r.Checkpoint(ctx, RuntimeCheckpoint{Revision: uint64(i + 1)})
		}(i, r)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	for i, id := range ids {
		checkpoint, found, e := ja.ReadCheckpoint(ctx, id.Instance, 1)
		if e != nil || !found || checkpoint.Revision != uint64(i+1) {
			t.Fatalf("instance %s checkpoint=%+v %v %v", id.Instance, checkpoint, found, e)
		}
	}
}

func TestJournalIntegrityExternalCheckpointUsesOnlySuppliedTransaction(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("commit-%v", commit), func(t *testing.T) {
			j, id, _ := integrityJournal(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			tx, e := j.db.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
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
			writeDone := make(chan error, 1)
			go func() {
				writeDone <- j.WriteCheckpointTx(ctx, tx, id.Instance, id.Generation, RuntimeCheckpoint{Revision: 7})
			}()
			select {
			case e = <-writeDone:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(time.Second):
				_ = tx.Rollback()
				t.Fatal("external checkpoint waited for mutex/DB pool held by reader")
			}
			if commit {
				e = tx.Commit()
			} else {
				e = tx.Rollback()
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = <-readDone; e != nil {
				t.Fatal(e)
			}
			checkpoint, found, e := j.ReadCheckpoint(ctx, id.Instance, id.Generation)
			if e != nil || found != commit || commit && checkpoint.Revision != 7 {
				t.Fatalf("atomic external checkpoint=%+v %v %v", checkpoint, found, e)
			}
		})
	}
}

func TestJournalIntegrityReopenAndRootRollbackBoundary(t *testing.T) {
	ctx := context.Background()
	filename := filepath.Join(t.TempDir(), "journal.sqlite")
	key := bytes.Repeat([]byte{99}, 32)
	j, e := OpenJournal(filename, key)
	if e != nil {
		t.Fatal(e)
	}
	id := RunIdentity{Instance: "owned", Generation: 1, Revision: 1, Fingerprint: "input", Run: "run"}
	r, e := j.Begin(ctx, id, nil, integrityGuard)
	if e != nil {
		t.Fatal(e)
	}
	var oldRoot []byte
	if e = j.db.QueryRow("SELECT root FROM torrenttransfer_registry WHERE namespace=?", j.namespace).Scan(&oldRoot); e != nil {
		t.Fatal(e)
	}
	calls := 0
	integrityMutation(t, r, &calls)
	if e = j.Close(); e != nil {
		t.Fatal(e)
	}
	j, e = OpenJournal(filename, key)
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	r, e = j.Begin(ctx, id, nil, integrityGuard)
	if e != nil {
		t.Fatal(e)
	}
	integrityMutation(t, r, &calls)
	if calls != 1 {
		t.Fatal("reissued after reopen")
	}
	// Restoring just the old valid root cannot erase later confirmed steps.
	// A rollback of every matching cell, head and root is outside this journal's
	// freshness guarantee and needs an anchor outside the entire SQLite file.
	integrityExec(t, j, "UPDATE torrenttransfer_registry SET root=? WHERE namespace=?", oldRoot, j.namespace)
	if _, e = j.Begin(ctx, id, nil, integrityGuard); !errors.Is(e, ErrJournalIntegrity) {
		t.Fatalf("accepted stale root with current rows: %v", e)
	}
}

func TestJournalIntegrityExternalAndJournalWritersRace(t *testing.T) {
	j, id, r := integrityJournal(t)
	ctx := context.Background()
	var count atomic.Int64
	r.LocalCommit = func(context.Context, *sql.Tx, RunIdentity, string, string, any) (func(), error) {
		return func() { count.Add(1) }, nil
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 8; i++ {
			if e := r.localWrite(ctx, "history", fmt.Sprintf("history/%d", i), i); e != nil {
				errs <- e
				return
			}
		}
		errs <- nil
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 8; i++ {
			tx, e := j.db.BeginTx(ctx, nil)
			if e != nil {
				errs <- e
				return
			}
			e = j.WriteCheckpointTx(ctx, tx, id.Instance, id.Generation, RuntimeCheckpoint{Revision: uint64(i + 1)})
			if e != nil {
				_ = tx.Rollback()
				errs <- e
				return
			}
			if e = tx.Commit(); e != nil {
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
	if count.Load() != 8 {
		t.Fatalf("lost afterCommit hooks=%d", count.Load())
	}
	checkpoint, found, e := j.ReadCheckpoint(ctx, id.Instance, id.Generation)
	if e != nil || !found || checkpoint.Revision != 8 {
		t.Fatalf("lost external value %+v %v %v", checkpoint, found, e)
	}
	if _, e = j.Begin(ctx, id, integrityInitial(), integrityGuard); e != nil {
		t.Fatalf("lost journal manifest: %v", e)
	}
}
