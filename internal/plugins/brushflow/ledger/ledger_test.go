package brushledger

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func fixture(t *testing.T) (*sql.DB, *Store) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "proof.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	s, err := New(bytes.Repeat([]byte{1}, 32), "BrushFlowLanduo", 1)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = CreateSchema(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db, s
}
func transaction(t *testing.T, db *sql.DB, fn func(*sql.Tx)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fn(tx)
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func put(t *testing.T, s *Store, tx *sql.Tx, c Collection, r Root, key string, b []byte) Root {
	t.Helper()
	next, err := s.Put(context.Background(), tx, c, r, key, b)
	if err != nil {
		t.Fatal(err)
	}
	return next
}
func expect(t *testing.T, s *Store, tx *sql.Tx, c Collection, r Root, key string, b []byte, exists bool) {
	t.Helper()
	got, yes, err := s.Lookup(context.Background(), tx, c, r, key)
	if err != nil || yes != exists || !bytes.Equal(got, b) {
		t.Fatalf("lookup %q exists=%v err=%v exact=%v", key, yes, err, bytes.Equal(got, b))
	}
}
func targetLeaf(t *testing.T, s *Store, tx *sql.Tx, c Collection, r Root, key string) page {
	t.Helper()
	id := string(r)
	path := s.keyPath(c, key)
	for {
		p, err := s.read(context.Background(), tx, c, id)
		if err != nil {
			t.Fatal(err)
		}
		if p.Kind == "leaf" {
			return p
		}
		id = p.Children[nibble(path[len(p.Path)])]
		if id == "" {
			t.Fatal("fixture key absent")
		}
	}
}
func TestExactValuesRootsAndCompressedPages(t *testing.T) {
	db, s := fixture(t)
	ctx := context.Background()
	r := s.Empty(Runs)
	empty := r
	var old Root
	big := bytes.Repeat([]byte("opaque\x00\xff{\"n\":9007199254740993}\n"), 10000)
	transaction(t, db, func(tx *sql.Tx) {
		old = put(t, s, tx, Runs, r, "run-a", []byte(`{"n":1.0}`))
		r = put(t, s, tx, Runs, old, "run-b", big)
		expect(t, s, tx, Runs, old, "run-a", []byte(`{"n":1.0}`), true)
		expect(t, s, tx, Runs, old, "run-b", nil, false)
		expect(t, s, tx, Runs, r, "run-b", big, true)
		expect(t, s, tx, Runs, empty, "run-a", nil, false)
		r = put(t, s, tx, Runs, r, "run-a", []byte(`{"n":1}`))
		expect(t, s, tx, Runs, r, "run-a", []byte(`{"n":1}`), true)
		seen := map[string][]byte{}
		if err := s.Iterate(ctx, tx, Runs, r, func(k string, b []byte) error { seen[k] = b; return nil }); err != nil || len(seen) != 2 {
			t.Fatal("authenticated iteration", err, len(seen))
		}
		next, err := s.Delete(ctx, tx, Runs, r, "run-a")
		if err != nil {
			t.Fatal(err)
		}
		expect(t, s, tx, Runs, next, "run-b", big, true)
		expect(t, s, tx, Runs, next, "run-a", nil, false)
		next, err = s.Delete(ctx, tx, Runs, next, "run-b")
		if err != nil || next != empty {
			t.Fatal("last key did not restore scoped empty root", err, next)
		}
		for i := 0; i < 300; i++ {
			r = put(t, s, tx, Runs, r, fmt.Sprintf("history-%03d", i), []byte(fmt.Sprint(i)))
		}
		var pages int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM plugin_brush_flow_ledger_pages`).Scan(&pages); err != nil || pages >= 300*12 {
			t.Fatal("uncompressed unary proof paths", err, pages)
		}
		if err = s.Audit(ctx, tx, Runs, r); err != nil {
			t.Fatal(err)
		}
	})
	transaction(t, db, func(tx *sql.Tx) {
		expect(t, s, tx, Runs, old, "run-a", []byte(`{"n":1.0}`), true)
		expect(t, s, tx, Runs, r, "history-173", []byte("173"), true)
	})
}
func TestMissingOldChunksCannotBeHealedBySamePutOrDelete(t *testing.T) {
	for _, operation := range []string{"same-put", "replace-put", "delete"} {
		t.Run(operation, func(t *testing.T) {
			db, s := fixture(t)
			ctx := context.Background()
			r := s.Empty(Effects)
			value := bytes.Repeat([]byte("accepted bytes"), 10000)
			transaction(t, db, func(tx *sql.Tx) { r = put(t, s, tx, Effects, r, "run:7", value) })
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			l := targetLeaf(t, s, tx, Effects, r, "run:7")
			if _, err = tx.Exec(`DELETE FROM plugin_brush_flow_ledger_pages WHERE namespace=? AND collection=? AND id=?`, s.namespace, string(Effects), l.Head); err != nil {
				t.Fatal(err)
			}
			var count int
			tx.QueryRow(`SELECT COUNT(*) FROM plugin_brush_flow_ledger_pages`).Scan(&count)
			if operation == "delete" {
				_, err = s.Delete(ctx, tx, Effects, r, "run:7")
			} else {
				nextValue := value
				if operation == "replace-put" {
					nextValue = []byte("replacement")
				}
				_, err = s.Put(ctx, tx, Effects, r, "run:7", nextValue)
			}
			if !errors.Is(err, ErrIntegrity) {
				t.Fatal("missing history was repaired/removed by mutation", err)
			}
			var after int
			tx.QueryRow(`SELECT COUNT(*) FROM plugin_brush_flow_ledger_pages`).Scan(&after)
			if after != count {
				t.Fatal("Put inserted repair pages before checking old proof", count, after)
			}
		})
	}
}
func TestMissingSwappedOrCorruptPagesAreNotAbsence(t *testing.T) {
	for _, damage := range []string{"root", "leaf", "chunk", "swap-root", "swap-chunk", "flip", "bad-location"} {
		t.Run(damage, func(t *testing.T) {
			db, s := fixture(t)
			ctx := context.Background()
			r := s.Empty(Sources)
			transaction(t, db, func(tx *sql.Tx) {
				r = put(t, s, tx, Sources, r, "queue-a", []byte("original"))
				r = put(t, s, tx, Sources, r, "queue-b", []byte("other"))
			})
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			l := targetLeaf(t, s, tx, Sources, r, "queue-a")
			leafRaw, _ := json.Marshal(l)
			leafID := s.id(Sources, leafRaw)
			target := string(r)
			if damage == "leaf" {
				target = leafID
			}
			if damage == "chunk" || damage == "swap-chunk" {
				target = l.Head
			}
			switch damage {
			case "swap-root", "swap-chunk":
				other := targetLeaf(t, s, tx, Sources, r, "queue-b")
				var sealed []byte
				if err = tx.QueryRow(`SELECT sealed FROM plugin_brush_flow_ledger_pages WHERE namespace=? AND collection=? AND id=?`, s.namespace, string(Sources), other.Head).Scan(&sealed); err != nil {
					t.Fatal(err)
				}
				_, err = tx.Exec(`UPDATE plugin_brush_flow_ledger_pages SET sealed=? WHERE namespace=? AND collection=? AND id=?`, sealed, s.namespace, string(Sources), target)
			case "flip":
				var sealed []byte
				tx.QueryRow(`SELECT sealed FROM plugin_brush_flow_ledger_pages WHERE namespace=? AND collection=? AND id=?`, s.namespace, string(Sources), target).Scan(&sealed)
				sealed[len(sealed)-1] ^= 1
				_, err = tx.Exec(`UPDATE plugin_brush_flow_ledger_pages SET sealed=? WHERE namespace=? AND collection=? AND id=?`, sealed, s.namespace, string(Sources), target)
			case "bad-location":
				p, e := s.read(ctx, tx, Sources, string(r))
				if e != nil {
					t.Fatal(e)
				}
				slots := []int{}
				for i, child := range p.Children {
					if child != "" {
						slots = append(slots, i)
					}
				}
				p.Children[slots[0]], p.Children[slots[1]] = p.Children[slots[1]], p.Children[slots[0]]
				target, e = s.putPage(ctx, tx, Sources, p)
				if e != nil {
					t.Fatal(e)
				}
				r = Root(target)
			default:
				_, err = tx.Exec(`DELETE FROM plugin_brush_flow_ledger_pages WHERE namespace=? AND collection=? AND id=?`, s.namespace, string(Sources), target)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, found, err := s.Lookup(ctx, tx, Sources, r, "queue-a")
			if found || !errors.Is(err, ErrIntegrity) {
				t.Fatal("damaged proof became absence/member", found, err)
			}
			if err = s.Audit(ctx, tx, Sources, r); !errors.Is(err, ErrIntegrity) {
				t.Fatal("audit ignored reachable damaged page", err)
			}
		})
	}
}
func TestScopesRollbackAndCancellation(t *testing.T) {
	db, s := fixture(t)
	ctx := context.Background()
	r := s.Empty(Unresolved)
	transaction(t, db, func(tx *sql.Tx) { r = put(t, s, tx, Unresolved, r, "run:0", []byte("unknown selected write")) })
	transaction(t, db, func(tx *sql.Tx) {
		for _, scope := range []struct {
			key        []byte
			instance   string
			generation int64
		}{{bytes.Repeat([]byte{2}, 32), "BrushFlowLanduo", 1}, {bytes.Repeat([]byte{1}, 32), "other", 1}, {bytes.Repeat([]byte{1}, 32), "BrushFlowLanduo", 2}} {
			foreign, err := New(scope.key, scope.instance, scope.generation)
			if err != nil {
				t.Fatal(err)
			}
			if _, found, err := foreign.Lookup(ctx, tx, Unresolved, r, "run:0"); found || !errors.Is(err, ErrIntegrity) {
				t.Fatal("foreign owner root accepted", found, err)
			}
		}
		if _, _, err := s.Lookup(ctx, tx, Runs, r, "run:0"); !errors.Is(err, ErrIntegrity) {
			t.Fatal("foreign collection accepted", err)
		}
		if _, _, err := s.Lookup(ctx, tx, Unresolved, Root(""), "run:0"); !errors.Is(err, ErrInput) {
			t.Fatal("missing root treated as genesis", err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, _, err := s.Lookup(canceled, tx, Unresolved, r, "run:0"); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		stop := errors.New("stop iteration")
		if err := s.Iterate(ctx, tx, Unresolved, r, func(string, []byte) error { return stop }); !errors.Is(err, stop) {
			t.Fatal(err)
		}
	})
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.Put(ctx, tx, Unresolved, r, "run:1", []byte("not committed"))
	if err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	transaction(t, db, func(tx *sql.Tx) {
		expect(t, s, tx, Unresolved, r, "run:0", []byte("unknown selected write"), true)
		expect(t, s, tx, Unresolved, r, "run:1", nil, false)
		if _, _, err := s.Lookup(ctx, tx, Unresolved, next, "run:1"); !errors.Is(err, ErrIntegrity) {
			t.Fatal("rolled back root became live", err)
		}
	})
}
func TestCurrentRootAuthorityCASInSameSingleConnectionTransaction(t *testing.T) {
	db, s := fixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`CREATE TABLE owned_authority(id INTEGER PRIMARY KEY,root TEXT NOT NULL);INSERT INTO owned_authority VALUES(1,?)`, string(s.Empty(Runs))); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				errs <- err
				return
			}
			defer tx.Rollback()
			var old Root
			if err = tx.QueryRowContext(ctx, `SELECT root FROM owned_authority WHERE id=1`).Scan(&old); err != nil {
				errs <- err
				return
			}
			next, err := s.Put(ctx, tx, Runs, old, fmt.Sprint(i), []byte(fmt.Sprint(i)))
			if err != nil {
				errs <- err
				return
			}
			changed, err := tx.ExecContext(ctx, `UPDATE owned_authority SET root=? WHERE id=1 AND root=?`, string(next), string(old))
			if err != nil {
				errs <- err
				return
			}
			n, err := changed.RowsAffected()
			if err != nil || n != 1 {
				errs <- fmt.Errorf("CAS %d %w", n, err)
				return
			}
			errs <- tx.Commit()
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	transaction(t, db, func(tx *sql.Tx) {
		var root Root
		if err := tx.QueryRow(`SELECT root FROM owned_authority WHERE id=1`).Scan(&root); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 8; i++ {
			expect(t, s, tx, Runs, root, fmt.Sprint(i), []byte(fmt.Sprint(i)), true)
		}
	})
}

func TestPersistentRandomSequencesAgreeWithLogicalHistory(t *testing.T) {
	db, s := fixture(t)
	ctx := context.Background()
	r := s.Empty(Runs)
	model := map[string][]byte{}
	type snapshot struct {
		root   Root
		values map[string][]byte
	}
	old := []snapshot{}
	random := rand.New(rand.NewSource(928341))
	transaction(t, db, func(tx *sql.Tx) {
		for i := 0; i < 500; i++ {
			key := fmt.Sprint(random.Intn(41))
			if random.Intn(4) == 0 {
				var err error
				r, err = s.Delete(ctx, tx, Runs, r, key)
				if err != nil {
					t.Fatal(i, err)
				}
				delete(model, key)
			} else {
				value := make([]byte, random.Intn(200))
				random.Read(value)
				r = put(t, s, tx, Runs, r, key, value)
				model[key] = value
			}
			for _, sample := range []string{key, fmt.Sprint(random.Intn(41)), "never-present"} {
				value, yes := model[sample]
				expect(t, s, tx, Runs, r, sample, value, yes)
			}
			if i%50 == 0 {
				copy := map[string][]byte{}
				for k, v := range model {
					copy[k] = append([]byte{}, v...)
				}
				old = append(old, snapshot{r, copy})
			}
		}
		for _, history := range old {
			for key, value := range history.values {
				expect(t, s, tx, Runs, history.root, key, value, true)
			}
			if err := s.Audit(ctx, tx, Runs, history.root); err != nil {
				t.Fatal("old authority root changed", err)
			}
		}
		seen := map[string][]byte{}
		if err := s.Iterate(ctx, tx, Runs, r, func(k string, v []byte) error { seen[k] = v; return nil }); err != nil {
			t.Fatal(err)
		}
		if len(seen) != len(model) {
			t.Fatal("logical key set changed", len(seen), len(model))
		}
		for k, v := range model {
			if !bytes.Equal(seen[k], v) {
				t.Fatal("logical history changed", k)
			}
		}
	})
}
