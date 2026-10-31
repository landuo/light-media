package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/siteaccess"
)

// These are real owned SQLite/component-handler tests. The authority closure
// below is explicitly supplied, not evidence of a compiled SDK worker.
func siteAccessRows(t *testing.T, db *sql.DB) []siteaccess.Record {
	t.Helper()
	rows, err := db.Query(`SELECT id,domain,success,fail,seconds,lst_state,lst_mod_date,note FROM source_site_access_statistic ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []siteaccess.Record{}
	for rows.Next() {
		var r siteaccess.Record
		var id int64
		var seconds sql.NullInt64
		var note sql.NullString
		if err = rows.Scan(&id, &r.Domain, &r.Success, &r.Fail, &seconds, &r.LastState, &r.LastModified, &note); err != nil {
			t.Fatal(err)
		}
		r.ID = &id
		if seconds.Valid {
			v := seconds.Int64
			r.Seconds = &v
		}
		if note.Valid {
			if err = json.Unmarshal([]byte(note.String), &r.Note); err != nil {
				t.Fatal(err)
			}
		}
		out = append(out, r)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func siteAccessWrite(t *testing.T, s *Store, method, domain string, seconds *int64, stamp string) error {
	t.Helper()
	ctx := context.Background()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = writeOriginalSiteAccess(ctx, tx, method, domain, seconds, stamp); err != nil {
		return err
	}
	return tx.Commit()
}

func TestSharedSiteAccessActualSourceSQLiteAndCold(t *testing.T) {
	// Existing pure-package fixture is the entire independently accepted source
	// raw, not a Go-built expected row. The coordinator binds this extra path.
	raw, err := os.ReadFile("../plugins/siteaccess/testdata/source-access-results.json")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	if hex.EncodeToString(hash[:]) != "9303332121f5265e8881f3330aa785d32d08aa4c2e00054111f41954fadc9fe3" {
		t.Fatal("source observation drift")
	}
	var source struct {
		Phase string `json:"phase"`
		Cases []struct {
			Name    string              `json:"case"`
			Domain  string              `json:"domain"`
			Seed    json.RawMessage     `json:"seed"`
			Before  []siteaccess.Record `json:"rows_before_actions"`
			Actions []struct {
				Input struct {
					Method  string `json:"method"`
					Time    string `json:"time"`
					Seconds *int64 `json:"seconds"`
				} `json:"input"`
				Outcome struct {
					Returned json.RawMessage `json:"returned"`
					Type     string          `json:"type"`
				} `json:"outcome"`
				After []siteaccess.Record `json:"rows_after"`
			} `json:"actions"`
		} `json:"cases"`
		Final []siteaccess.Record `json:"rows_final"`
	}
	if err = json.Unmarshal(raw, &source); err != nil || source.Phase != "warm" || len(source.Cases) != 11 {
		t.Fatal("invalid actual source", err)
	}
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s != nil {
			_ = s.Close()
		}
	}()
	calls := 0
	for _, c := range source.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if string(c.Seed) != "null" {
				var seed siteaccess.Record
				if err := json.Unmarshal(c.Seed, &seed); err != nil {
					t.Fatal(err)
				}
				var note any
				if seed.Note != nil {
					raw, err := json.Marshal(seed.Note)
					if err != nil {
						t.Fatal(err)
					}
					note = string(raw)
				}
				if _, err := s.DB.Exec(`INSERT INTO source_site_access_statistic(domain,success,fail,seconds,lst_state,lst_mod_date,note) VALUES(?,?,?,?,?,?,?)`, c.Domain, seed.Success, seed.Fail, seed.Seconds, seed.LastState, seed.LastModified, note); err != nil {
					t.Fatal(err)
				}
			}
			if got := siteAccessRows(t, s.DB); !reflect.DeepEqual(got, c.Before) {
				t.Fatalf("source before differs: got=%+v want=%+v", got, c.Before)
			}
			for _, action := range c.Actions {
				calls++
				if action.Outcome.Type != "NoneType" || string(action.Outcome.Returned) != "null" {
					t.Fatal("source outcome is not None")
				}
				method := plugins.SiteAccessSuccessCapability
				if action.Input.Method == "fail" {
					method = plugins.SiteAccessFailCapability
				}
				if err := siteAccessWrite(t, s, method, c.Domain, action.Input.Seconds, action.Input.Time); err != nil {
					t.Fatal(err)
				}
				if got := siteAccessRows(t, s.DB); !reflect.DeepEqual(got, action.After) {
					t.Fatalf("source stored row differs: got=%+v want=%+v", got, action.After)
				}
			}
		})
	}
	if calls != 19 {
		t.Fatal("source call count", calls)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = nil
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := siteAccessRows(t, s.DB); !reflect.DeepEqual(got, source.Final) {
		t.Fatalf("cold rows differ: %+v", got)
	}
}

func TestSharedSiteAccessSQLiteFailureAndRawPreservation(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	stamp := "2026-10-31 12:00:00"
	// Nullable/raw unrelated fields and valid non-timing JSON survive fail.
	if _, err = s.DB.Exec(`INSERT INTO source_site_access_statistic(domain,success,fail,seconds,lst_state,lst_mod_date,note) VALUES('raw',NULL,2,'owned-raw-seconds',0,'old','"owned-json-string"')`); err != nil {
		t.Fatal(err)
	}
	if err = siteAccessWrite(t, s, plugins.SiteAccessFailCapability, "raw", nil, stamp); err != nil {
		t.Fatal(err)
	}
	var success any
	var fail int64
	var seconds, note string
	if err = s.DB.QueryRow(`SELECT success,fail,seconds,note FROM source_site_access_statistic WHERE domain='raw'`).Scan(&success, &fail, &seconds, &note); err != nil {
		t.Fatal(err)
	}
	if success != nil || fail != 3 || seconds != "owned-raw-seconds" || note != `"owned-json-string"` {
		t.Fatal("fail touched unrelated raw values")
	}
	if err = siteAccessWrite(t, s, plugins.SiteAccessSuccessCapability, "raw", nil, stamp); err == nil {
		t.Fatal("NULL success silently became zero")
	}
	// Source get_by_domain hydrates the complete ORM JSON column, so invalid
	// JSON rejects fail before its counter update even though fail ignores note.
	if _, err = s.DB.Exec(`INSERT INTO source_site_access_statistic(domain,success,fail,seconds,lst_state,lst_mod_date,note) VALUES('bad-json',1,2,0,0,'old','owned-not-json')`); err != nil {
		t.Fatal(err)
	}
	if err = siteAccessWrite(t, s, plugins.SiteAccessFailCapability, "bad-json", nil, stamp); err == nil {
		t.Fatal("invalid ORM JSON bypassed source hydration")
	}
	if err = s.DB.QueryRow(`SELECT fail,note FROM source_site_access_statistic WHERE domain='bad-json'`).Scan(&fail, &note); err != nil || fail != 2 || note != "owned-not-json" {
		t.Fatal("JSON error changed the row", fail, note, err)
	}
	if _, err = s.DB.Exec(`INSERT INTO source_site_access_statistic(domain,success,fail,seconds,lst_state,lst_mod_date,note) VALUES('nullable-note',1,0,NULL,0,'old','{"x":null}')`); err != nil {
		t.Fatal(err)
	}
	// This is the explicit int64-note support boundary, NOT an assertion that
	// source success(None) rejects {"x":null}; source may copy that raw value.
	if err = siteAccessWrite(t, s, plugins.SiteAccessSuccessCapability, "nullable-note", nil, stamp); err == nil {
		t.Fatal("finite integer-note boundary was silently coerced")
	}
	if err = s.DB.QueryRow(`SELECT note FROM source_site_access_statistic WHERE domain='nullable-note'`).Scan(&note); err != nil || note != `{"x":null}` {
		t.Fatal("invalid note changed", note, err)
	}
	if _, err = s.DB.Exec(`INSERT INTO source_site_access_statistic(domain,success,fail,seconds,lst_state,lst_mod_date,note) VALUES('overflow',?,?,NULL,0,'old',NULL)`, int64(math.MaxInt64), int64(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{plugins.SiteAccessSuccessCapability, plugins.SiteAccessFailCapability} {
		if err = siteAccessWrite(t, s, method, "overflow", nil, stamp); !errors.Is(err, siteaccess.ErrIntegerRange) {
			t.Fatal("overflow result", err)
		}
	}
	var count int64
	if err = s.DB.QueryRow(`SELECT success FROM source_site_access_statistic WHERE domain='overflow'`).Scan(&count); err != nil || count != math.MaxInt64 {
		t.Fatal("overflow committed", count, err)
	}
	// A real SQLite trigger aborts the attempted statement. No mocked SQL error,
	// dropping/replacing tables, changing filesystem permissions or real DB IO.
	if _, err = s.DB.Exec(`CREATE TRIGGER source_site_access_owned_abort BEFORE UPDATE ON source_site_access_statistic WHEN NEW.domain='sql-error' AND NEW.fail=2 BEGIN SELECT RAISE(ABORT,'owned site access rejection'); END`); err != nil {
		t.Fatal(err)
	}
	if err = siteAccessWrite(t, s, plugins.SiteAccessFailCapability, "sql-error", nil, stamp); err != nil {
		t.Fatal(err)
	}
	if err = siteAccessWrite(t, s, plugins.SiteAccessFailCapability, "sql-error", nil, stamp); err == nil {
		t.Fatal("SQL write failure returned success")
	}
	if err = s.DB.QueryRow(`SELECT fail FROM source_site_access_statistic WHERE domain='sql-error'`).Scan(&fail); err != nil || fail != 1 {
		t.Fatal("failed statement committed", fail, err)
	}
	if err = siteAccessWrite(t, s, plugins.SiteAccessSuccessCapability, "sql-error", nil, stamp); err != nil {
		t.Fatal("same store did not recover", err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	tx, err := s.DB.BeginTx(cancelCtx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = writeOriginalSiteAccess(cancelCtx, tx, plugins.SiteAccessFailCapability, "canceled", nil, stamp); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err = tx.Commit(); err == nil {
		t.Fatal("canceled transaction committed")
	}
	_ = tx.Rollback()
	if err = s.DB.QueryRow(`SELECT count(*) FROM source_site_access_statistic WHERE domain='canceled'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("canceled transaction persisted", count, err)
	}
	// Nonunique exact domain is deliberately permitted; LIMIT 1 must touch only
	// the row actually selected by SQLite, not an invented normalized/upsert key.
	if _, err = s.DB.Exec(`INSERT INTO source_site_access_statistic(domain,success,fail) VALUES('dup',0,1),('dup',0,8)`); err != nil {
		t.Fatal(err)
	}
	var selected int64
	if err = s.DB.QueryRow(`SELECT id FROM source_site_access_statistic WHERE domain='dup' LIMIT 1`).Scan(&selected); err != nil {
		t.Fatal(err)
	}
	if err = siteAccessWrite(t, s, plugins.SiteAccessFailCapability, "dup", nil, stamp); err != nil {
		t.Fatal(err)
	}
	rows, err := s.DB.Query(`SELECT id,fail FROM source_site_access_statistic WHERE domain='dup' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for i := 0; rows.Next(); i++ {
		var id, v int64
		if err = rows.Scan(&id, &v); err != nil {
			t.Fatal(err)
		}
		want := int64(1)
		if i == 1 {
			want = 8
		}
		if id == selected {
			want++
		}
		if v != want {
			t.Fatal("nonselected duplicate changed", id, v, want)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestSharedSiteAccessResourceFiniteBoundary(t *testing.T) {
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
	in := PluginInstallation{PluginID: "OwnedSiteAccess", InstanceID: "OwnedSiteAccess", Version: "1", ManifestSchemaVersion: 1, Manifest: json.RawMessage(`{}`)}
	if err := a.pluginStore.Install(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	var err error
	in, err = a.pluginStore.Installation(context.Background(), in.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	p := &originalPluginInstance{app: a, installation: in}
	var active atomic.Bool
	active.Store(true)
	scope := originalBaseCallScope{object: p, phase: "query", installation: in, validate: func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !active.Load() {
			return errors.New("owned scope closed")
		}
		return nil
	}}
	identity := plugins.Identity{PluginID: in.PluginID, InstanceID: in.InstanceID, Version: in.Version}
	call := func(ctx context.Context, method, raw string) (json.RawMessage, error) {
		return a.pluginOriginalResourceHost(ctx, scope, identity, method, json.RawMessage(raw))
	}
	before := time.Now().Format("2006-01-02 15:04:05")
	for _, item := range []struct{ method, raw string }{
		{plugins.SiteAccessSuccessCapability, `{"domain":""}`},
		{plugins.SiteAccessSuccessCapability, `{"domain":"  Owned.雪.invalid/Path  ","seconds":0}`},
		{plugins.SiteAccessFailCapability, `{"domain":"  Owned.雪.invalid/Path  "}`},
		{plugins.SiteAccessSuccessCapability, `{"domain":"with\u0000control","seconds":null}`},
	} {
		out, err := call(context.Background(), item.method, item.raw)
		if err != nil || string(out) != "null" {
			t.Fatal("source None result", string(out), err)
		}
	}
	after := time.Now().Format("2006-01-02 15:04:05")
	rows := siteAccessRows(t, a.Store.DB)
	if len(rows) != 3 {
		t.Fatal(rows)
	}
	for _, r := range rows {
		if r.LastModified < before || r.LastModified > after {
			t.Fatal("not one local handler wall-clock value", r.LastModified)
		}
	}
	unchanged := siteAccessRows(t, a.Store.DB)
	for _, item := range []struct{ method, raw string }{
		{plugins.SiteAccessSuccessCapability, `{"domain":null}`},
		{plugins.SiteAccessSuccessCapability, `{"seconds":1}`},
		{plugins.SiteAccessSuccessCapability, `{"domain":"a","domain":"b"}`},
		{plugins.SiteAccessSuccessCapability, `{"domain":"a","seconds":1.5}`},
		{plugins.SiteAccessSuccessCapability, `{"domain":"a","seconds":9223372036854775808}`},
		{plugins.SiteAccessSuccessCapability, `{"domain":"a","seconds":true}`},
		{plugins.SiteAccessSuccessCapability, `{"domain":"\ud800"}`},
		{plugins.SiteAccessFailCapability, `{"domain":"a","seconds":null}`},
		{plugins.SiteAccessFailCapability, `{"domain":"a"} {}`},
	} {
		if out, err := call(context.Background(), item.method, item.raw); err == nil || len(out) != 0 {
			t.Fatal("invalid request became success", item, string(out), err)
		}
	}
	active.Store(false)
	if _, err = call(context.Background(), plugins.SiteAccessFailCapability, `{"domain":"late"}`); err == nil {
		t.Fatal("expired callback accepted")
	}
	active.Store(true)
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = call(cancelCtx, plugins.SiteAccessFailCapability, `{"domain":"late"}`); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	scope.installation.Generation++
	if _, err = call(context.Background(), plugins.SiteAccessFailCapability, `{"domain":"late"}`); err == nil {
		t.Fatal("stale installation committed")
	}
	scope.installation.Generation--
	a.closeOriginalBackgroundAdmission()
	if _, err = call(context.Background(), plugins.SiteAccessFailCapability, `{"domain":"late"}`); err == nil {
		t.Fatal("closed admission accepted")
	}
	if got := siteAccessRows(t, a.Store.DB); !reflect.DeepEqual(got, unchanged) {
		t.Fatal("rejected request mutated rows")
	}
}
