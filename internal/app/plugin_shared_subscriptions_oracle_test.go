package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

type sharedSubscriptionOracleOperation struct {
	Case      string                     `json:"case"`
	Method    string                     `json:"method"`
	Arguments map[string]json.RawMessage `json:"arguments"`
	Returned  json.RawMessage            `json:"returned"`
	FreshGet  json.RawMessage            `json:"fresh_get"`
	Error     json.RawMessage            `json:"error"`
}
type sharedSubscriptionOracleGroup struct {
	Initial    []map[string]json.RawMessage        `json:"initial_rows"`
	Operations []sharedSubscriptionOracleOperation `json:"operations"`
	Cold       []sharedSubscriptionOracleOperation `json:"cold_operations"`
}

func sharedOracleID(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	return "owned-source-" + string(raw)
}
func sharedOracleCanonical(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if e := d.Decode(&v); e != nil {
		t.Fatalf("invalid comparison JSON: %v", e)
	}
	var norm func(any) any
	norm = func(x any) any {
		switch z := x.(type) {
		case json.Number:
			r, ok := new(big.Rat).SetString(string(z))
			if !ok {
				t.Fatalf("bad JSON number %s", z)
			}
			return struct{ Number string }{r.RatString()}
		case map[string]any:
			for k, v := range z {
				z[k] = norm(v)
			}
		case []any:
			for i, v := range z {
				z[i] = norm(v)
			}
		}
		return x
	}
	return norm(v)
}
func assertSharedOracleJSON(t *testing.T, got, want json.RawMessage) {
	t.Helper()
	if !reflect.DeepEqual(sharedOracleCanonical(t, got), sharedOracleCanonical(t, want)) {
		t.Fatalf("full source row mismatch\n got %s\nwant %s", got, want)
	}
}
func remapSharedOracleExpected(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if e := d.Decode(&v); e != nil {
		t.Fatal(e)
	}
	row := func(m map[string]any) {
		if n, ok := m["id"].(json.Number); ok {
			m["id"] = "owned-source-" + string(n)
		}
	}
	switch z := v.(type) {
	case map[string]any:
		row(z)
	case []any:
		for _, r := range z {
			row(r.(map[string]any))
		}
	}
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func sharedOracleActualRows(t *testing.T, raw json.RawMessage, list bool) json.RawMessage {
	t.Helper()
	if list {
		var in []plugins.SharedSubscriptionV1
		if e := json.Unmarshal(raw, &in); e != nil {
			t.Fatal(e)
		}
		out := []json.RawMessage{}
		for _, r := range in {
			out = append(out, r.Row)
		}
		return mustCleanSeedJSON(out)
	}
	if string(raw) == "null" {
		return raw
	}
	return decodeSharedSubscription(t, raw).Row
}
func seedSharedOracleRows(t *testing.T, a *App, rows []map[string]json.RawMessage) {
	t.Helper()
	ctx := context.Background()
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	// Arrange only: load independently captured full source facts into the same
	// native record. All asserted expected values remain untouched source output.
	for _, row := range rows {
		sid := sharedOracleID(row["id"])
		s := Subscription{ID: sid, Revision: 1, Schedule: Schedule{Mode: "manual"}}
		patch := map[string]json.RawMessage{}
		for k, v := range row {
			if k != "id" {
				patch[k] = v
			}
		}
		if e = a.applySharedSubscriptionColumns(ctx, tx, &s, row, patch, nil); e != nil {
			t.Fatal(e)
		}
		b, e := json.Marshal(s)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('subscription',?,?,?)`, sid, b, now()); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
}
func TestSharedSubscriptionsIndependentSourceOracle(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/plugin-shared-host-20261030/subscriptions/source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	if hash := fmt.Sprintf("%x", sha256.Sum256(raw)); hash != "5177722afe3b90812aacbd862f5efac8fc43a3d0bc1b7f6d389167092f292aa0" {
		t.Fatalf("independent source oracle changed: %s", hash)
	}
	var groups map[string]sharedSubscriptionOracleGroup
	if e = json.Unmarshal(raw, &groups); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"queries", "raw-columns", "failures-null", "affinity"} {
		t.Run(name, func(t *testing.T) {
			group := groups[name]
			dir := t.TempDir()
			markets := []string{}
			a, e := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = a.Close() }()
			var sqliteVersion string
			if e = a.Store.DB.QueryRow(`SELECT sqlite_version()`).Scan(&sqliteVersion); e != nil {
				t.Fatal(e)
			}
			t.Logf("target SQLite=%s; independent source SQLite=3.53.1; predicate order is observed index behavior, not a cross-version ORDER BY promise", sqliteVersion)
			cookie := setupTestAdmin(t, a)
			p := installSharedSubscriptionsFixture(t, a, cookie, sharedSubscriptionsAll)
			seedSharedOracleRows(t, a, group.Initial)
			cold := func() {
				before := sharedSubscriptionsSnapshot(t, p)
				if e := a.Close(); e != nil {
					t.Fatal(e)
				}
				a, e = New(Options{DataDir: dir, PluginMarketsOverride: &markets})
				if e != nil {
					t.Fatal(e)
				}
				p = a.startOriginalPlugin(context.Background(), "OwnedSharedHost")
				if p == nil {
					t.Fatal("cold artifact worker absent")
				}
				after := sharedSubscriptionsSnapshot(t, p)
				if bytes.Equal(before["pid"], after["pid"]) {
					t.Fatal("cold reused process")
				}
				t.Logf("actual cold old_pid=%s new_pid=%s", before["pid"], after["pid"])
			}
			run := func(op sharedSubscriptionOracleOperation) {
				t.Helper()
				t.Logf("source operation %s/%s", name, op.Case)
				if op.Case == "sqlite-trigger-rollback" {
					if _, e = a.Store.DB.Exec(`CREATE TRIGGER owned_shared_oracle_abort BEFORE UPDATE ON records WHEN OLD.kind='subscription' BEGIN SELECT RAISE(ABORT,'owned synthetic failure'); END`); e != nil {
						t.Fatal(e)
					}
				}
				if op.Case == "after-trigger-removal" {
					if _, e = a.Store.DB.Exec(`DROP TRIGGER owned_shared_oracle_abort`); e != nil {
						t.Fatal(e)
					}
				}
				// sql-null-json is an original setup operation, not a fifth API. At this
				// point all four JSON columns are already NULL from all-nullable-null.
				var input any
				method := "subscriptions." + op.Method
				switch op.Method {
				case "get":
					input = plugins.SharedSubscriptionGetV1{ID: sharedOracleID(op.Arguments["sid"])}
				case "list":
					input = plugins.SharedSubscriptionListV1{State: op.Arguments["state"]}
				case "list_by_tmdbid":
					input = plugins.SharedSubscriptionTMDBV1{TMDBID: op.Arguments["tmdbid"], Season: op.Arguments["season"]}
				case "update":
					input = plugins.SharedSubscriptionUpdateV1{ID: sharedOracleID(op.Arguments["sid"]), Patch: op.Arguments["payload"]}
				default:
					t.Fatalf("unknown oracle operation %s", op.Method)
				}
				got, err := sharedSubscriptionsCall(t, p, method, input)
				expected := op.Returned
				if len(op.FreshGet) > 0 {
					expected = op.FreshGet
				}
				nonfinite := bytes.Contains(expected, []byte(`"$source_python_float"`))
				if len(op.Error) > 0 && string(op.Error) != "null" {
					if err == nil {
						t.Fatalf("source failure accepted: %s", got)
					}
					t.Logf("expected source DAO failure: %s; target: %v", op.Error, err)
					if op.Method == "update" {
						got, err = sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: sharedOracleID(op.Arguments["sid"])})
						if err != nil {
							t.Fatal(err)
						}
						assertSharedOracleJSON(t, sharedOracleActualRows(t, got, false), remapSharedOracleExpected(t, expected))
					}
					return
				}
				if nonfinite {
					if err == nil {
						t.Fatal("nonfinite raw JSON unexpectedly succeeded")
					}
					var saved Subscription
					if e = a.Store.Get(context.Background(), "subscription", sharedOracleID(op.Arguments["sid"]), &saved); e != nil {
						t.Fatal(e)
					}
					var patch map[string]json.RawMessage
					_ = json.Unmarshal(op.Arguments["payload"], &patch)
					if saved.SharedSourceFields["vote"].Nonfinite != "+Inf" || saved.Keyword != sharedSubscriptionString(patch["keyword"]) {
						t.Fatal("nonfinite commit lost")
					}

					cold()
					if _, e = sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: saved.ID}); e == nil {
						t.Fatal("cold nonfinite raw JSON succeeded")
					}
					return
				}
				if err != nil {
					t.Fatalf("source success rejected: %v", err)
				}
				assertSharedOracleJSON(t, sharedOracleActualRows(t, got, op.Method == "list" || op.Method == "list_by_tmdbid"), remapSharedOracleExpected(t, expected))
			}
			for _, op := range group.Operations {
				run(op)
			}
			cold()
			for _, op := range group.Cold {
				run(op)
			}
		})
	}
}

func TestSharedSubscriptionsIndependentFloatGrammar(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/plugin-shared-host-20261030/subscriptions/source-float-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	if hash := fmt.Sprintf("%x", sha256.Sum256(raw)); hash != "7cda25ab8d108dcfc4d29c4cba0e2dc54e47ee5940e74a4c1e7495fe87ed4d1d" {
		t.Fatal("independent FLOAT oracle changed")
	}
	var fixture struct {
		Operations []sharedSubscriptionOracleOperation `json:"observations"`
	}
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	a, _, p := sharedSubscriptionsFixture(t, sharedSubscriptionsAll)
	// First source operation fails and rolls back; its independent fresh row is
	// the complete unchanged setup state, not output computed by this target.
	var initial map[string]json.RawMessage
	if e = json.Unmarshal(fixture.Operations[0].FreshGet, &initial); e != nil {
		t.Fatal(e)
	}
	seedSharedOracleRows(t, a, []map[string]json.RawMessage{initial})
	for _, op := range fixture.Operations {
		t.Logf("independent source %s", op.Case)
		var before Subscription
		if e = a.Store.Get(context.Background(), "subscription", "owned-source-1", &before); e != nil {
			t.Fatal(e)
		}
		got, err := sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: "owned-source-1", Patch: op.Arguments["payload"]})
		isError := len(op.Error) > 0 && string(op.Error) != "null"
		nonfinite := bytes.Contains(op.FreshGet, []byte(`"$source_python_float"`))
		if isError || nonfinite {
			if err == nil {
				t.Fatal("source error/raw nonfinite JSON accepted")
			}
		} else if err != nil {
			t.Fatalf("source success rejected: %v", err)
		}
		if isError {
			var after Subscription
			if e = a.Store.Get(context.Background(), "subscription", "owned-source-1", &after); e != nil {
				t.Fatal(e)
			}
			assertSharedOracleJSON(t, mustCleanSeedJSON(after), mustCleanSeedJSON(before))
		}
		if nonfinite {
			var after Subscription
			if e = a.Store.Get(context.Background(), "subscription", "owned-source-1", &after); e != nil {
				t.Fatal(e)
			}
			sign := "+Inf"
			if bytes.Contains(op.FreshGet, []byte(`"-inf"`)) {
				sign = "-Inf"
			}
			if after.SharedSourceFields["vote"].Nonfinite != sign {
				t.Fatal("nonfinite commit/rollback changed sign")
			}
			if _, e = sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: "owned-source-1"}); e == nil {
				t.Fatal("nonfinite fresh read accepted")
			}
			continue
		}
		if isError {
			got, e = sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: "owned-source-1"})
			if e != nil {
				t.Fatal(e)
			}
		}
		assertSharedOracleJSON(t, sharedOracleActualRows(t, got, false), remapSharedOracleExpected(t, op.FreshGet))
	}
}

func TestSharedSubscriptionsIndependentExponentOverflow(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/plugin-shared-host-20261030/subscriptions/source-exponent-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	if hash := fmt.Sprintf("%x", sha256.Sum256(raw)); hash != "c73f756034a8375b7837ebef504452d70fed92baed534865f2b95db8f5287e32" {
		t.Fatal("independent exponent oracle changed")
	}
	var oracle struct {
		Operations []sharedSubscriptionOracleOperation `json:"observations"`
	}
	if e = json.Unmarshal(raw, &oracle); e != nil {
		t.Fatal(e)
	}
	input, e := os.ReadFile("../../testdata/plugin-shared-host-20261030/subscriptions/source/inputs/exponent.json")
	if e != nil {
		t.Fatal(e)
	}
	var sourceInput struct {
		Seed       []map[string]json.RawMessage        `json:"seed"`
		Operations []sharedSubscriptionOracleOperation `json:"operations"`
	}
	if e = json.Unmarshal(input, &sourceInput); e != nil {
		t.Fatal(e)
	}
	a, _, p := sharedSubscriptionsFixture(t, sharedSubscriptionsAll)
	var initial map[string]json.RawMessage
	if e = json.Unmarshal(oracle.Operations[0].FreshGet, &initial); e != nil {
		t.Fatal(e)
	}
	initial["name"] = sourceInput.Seed[0]["name"]
	seedSharedOracleRows(t, a, []map[string]json.RawMessage{initial})
	for i, op := range sourceInput.Operations {
		want := oracle.Operations[i]
		if op.Case != want.Case {
			t.Fatal("source input/observation mismatch")
		}
		t.Logf("literal source JSON %s patch=%s", op.Case, op.Arguments["payload"])
		got, err := sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: "owned-source-1", Patch: op.Arguments["payload"]})
		if bytes.Contains(want.FreshGet, []byte(`"$source_python_float"`)) {
			if err == nil {
				t.Fatal("nonfinite raw JSON succeeded")
			}
			var native Subscription
			if e = a.Store.Get(context.Background(), "subscription", "owned-source-1", &native); e != nil {
				t.Fatal(e)
			}
			if native.SharedSourceFields["tmdbid"].Nonfinite != "+Inf" {
				t.Fatal("nonfinite INTEGER-affinity update was rolled back")
			}
			continue
		}
		if err != nil {
			t.Fatalf("source accepted legal exponent JSON but worker failed: %v", err)
		}
		assertSharedOracleJSON(t, sharedOracleActualRows(t, got, false), remapSharedOracleExpected(t, want.FreshGet))
	}
}

func TestSharedSubscriptionsIndependentNestedJSON(t *testing.T) {
	for _, name := range []string{"nested-json", "finite-json"} {
		t.Run(name, func(t *testing.T) {
			source, e := os.ReadFile("../../testdata/plugin-shared-host-20261030/subscriptions/source-" + name + "-oracle.json")
			if e != nil {
				t.Fatal(e)
			}
			expectedHash := map[string]string{"nested-json": "c1b6a2a280a64054b2358b18b8db1ead3d432950f6fff7fc9c6e04fe84c35121", "finite-json": "0b0e8e16529db123827cd6d6d984ce8dab1540e9552de67e45f8d506336bd747"}
			if hash := fmt.Sprintf("%x", sha256.Sum256(source)); hash != expectedHash[name] {
				t.Fatal("independent nested source changed")
			}
			var oracle struct {
				Initial []struct {
					Values map[string]json.RawMessage `json:"values"`
				} `json:"initial_database"`
				Operations []sharedSubscriptionOracleOperation `json:"observations"`
			}
			if e = json.Unmarshal(source, &oracle); e != nil {
				t.Fatal(e)
			}
			input, e := os.ReadFile("../../testdata/plugin-shared-host-20261030/subscriptions/source/inputs/" + name + ".json")
			if e != nil {
				t.Fatal(e)
			}
			var ops struct {
				Operations []sharedSubscriptionOracleOperation `json:"operations"`
			}
			if e = json.Unmarshal(input, &ops); e != nil {
				t.Fatal(e)
			}
			dir := t.TempDir()
			markets := []string{}
			a, e := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = a.Close() }()
			cookie := setupTestAdmin(t, a)
			p := installSharedSubscriptionsFixture(t, a, cookie, sharedSubscriptionsAll)
			initial := oracle.Initial[0].Values
			// Source's independent SQL setup snapshot stores JSON as SQL text; decode
			// only those four declared columns for fixture arrangement, not expectations.
			for _, column := range []string{"note", "sites", "filter_groups", "episode_priority"} {
				var text string
				if json.Unmarshal(initial[column], &text) == nil && json.Valid([]byte(text)) {
					initial[column] = json.RawMessage(text)
				}
			}
			seedSharedOracleRows(t, a, []map[string]json.RawMessage{initial})
			for i, op := range ops.Operations {
				want := oracle.Operations[i]
				if op.Case != want.Case {
					t.Fatal("source operation mismatch")
				}
				t.Logf("source JSON case=%s", op.Case)
				got, err := sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: "owned-source-1", Patch: op.Arguments["payload"]})
				if bytes.Contains(want.FreshGet, []byte(`"$source_python_float"`)) {
					if err == nil {
						t.Fatal("nonfinite nested raw row unexpectedly serialized")
					}
					var saved Subscription
					if e = a.Store.Get(context.Background(), "subscription", "owned-source-1", &saved); e != nil {
						t.Fatal(e)
					}
					if len(sharedSubscriptionNonfinite(saved)) == 0 {
						t.Fatal("nested nonfinite source commit missing")
					}
					revision := saved.Revision
					if e = a.Close(); e != nil {
						t.Fatal(e)
					}
					a, e = New(Options{DataDir: dir, PluginMarketsOverride: &markets})
					if e != nil {
						t.Fatal(e)
					}
					p = a.startOriginalPlugin(context.Background(), "OwnedSharedHost")
					if p == nil {
						t.Fatal("nested nonfinite cold worker absent")
					}
					if _, e = sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: "owned-source-1"}); e == nil {
						t.Fatal("cold nested source raw JSON did not fail")
					}
					// An unrelated ordinary column change must still commit without discarding
					// the exact nested fact; its raw-row response continues to fail.
					if _, e = sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: "owned-source-1", Patch: json.RawMessage(`{"keyword":null}`)}); e == nil {
						t.Fatal("unrelated update hid nested nonfinite")
					}
					if e = a.Store.Get(context.Background(), "subscription", "owned-source-1", &saved); e != nil || saved.Revision != revision+1 {
						t.Fatal("unrelated commit lost", e)
					}
					continue
				}
				if err != nil {
					t.Fatalf("source finite JSON success rejected: %v", err)
				}
				assertSharedOracleJSON(t, sharedOracleActualRows(t, got, false), remapSharedOracleExpected(t, want.FreshGet))
			}
		})
	}
}

func TestSharedSubscriptionsIndependentAllJSONColumnsCold(t *testing.T) {
	root := "../../testdata/plugin-shared-host-20261030/subscriptions/"
	raw, e := os.ReadFile(root + "source-four-json-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	if hash := fmt.Sprintf("%x", sha256.Sum256(raw)); hash != "7be94a54fef6d1c44eb8881917c66a702d7a5d0db8560c58ab3cd2f09f10cf03" {
		t.Fatal("independent four JSON source changed")
	}
	type observation struct {
		Case   string          `json:"case"`
		Method string          `json:"method"`
		Row    json.RawMessage `json:"row"`
	}
	type stage struct {
		Observations []observation `json:"observations"`
	}
	var source struct {
		Stages map[string]struct {
			Run  stage `json:"run"`
			Cold stage `json:"cold"`
		} `json:"stages"`
	}
	if e = json.Unmarshal(raw, &source); e != nil {
		t.Fatal(e)
	}
	setup, e := os.ReadFile(root + "source-four-json-initial.json")
	if e != nil {
		t.Fatal(e)
	}
	var initial struct {
		Rows []struct {
			Values map[string]json.RawMessage `json:"values"`
		} `json:"initial_database"`
	}
	if e = json.Unmarshal(setup, &initial); e != nil {
		t.Fatal(e)
	}
	row := initial.Rows[0].Values
	for _, col := range []string{"note", "sites", "episode_priority", "filter_groups"} {
		var s string
		if json.Unmarshal(row[col], &s) == nil && json.Valid([]byte(s)) {
			row[col] = json.RawMessage(s)
		}
	}
	dir := t.TempDir()
	markets := []string{}
	a, e := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = a.Close() }()
	cookie := setupTestAdmin(t, a)
	p := installSharedSubscriptionsFixture(t, a, cookie, sharedSubscriptionsAll)
	seedSharedOracleRows(t, a, []map[string]json.RawMessage{row})
	// This is a test-only observation encoder for internal facts. Business SDK
	// responses never expose source audit tags or private nonfinite sentinels.
	observe := func(raw json.RawMessage) json.RawMessage {
		v, e := sharedSubscriptionDecode(raw)
		if e != nil {
			t.Fatal(e)
		}
		var visit func(any) any
		visit = func(v any) any {
			switch x := v.(type) {
			case json.Number:
				if strings.ContainsAny(string(x), ".eE") {
					f, _ := x.Float64()
					if math.IsInf(f, 0) {
						sign := "inf"
						if math.IsInf(f, -1) {
							sign = "-inf"
						}
						return map[string]any{"$source_python_float": sign}
					}
				}
			case map[string]any:
				for k, item := range x {
					x[k] = visit(item)
				}
			case []any:
				for i, item := range x {
					x[i] = visit(item)
				}
			}
			return v
		}
		return mustCleanSeedJSON(visit(v))
	}
	assertStored := func(want json.RawMessage) {
		t.Helper()
		ctx := context.Background()
		tx, e := a.Store.DB.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		var b []byte
		if e = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='subscription' AND id='owned-source-1'`).Scan(&b); e != nil {
			t.Fatal(e)
		}
		var s Subscription
		if e = json.Unmarshal(b, &s); e != nil {
			t.Fatal(e)
		}
		projected, e := a.sharedSubscriptionWorkingRow(ctx, tx, s)
		if e != nil {
			t.Fatal(e)
		}
		assertSharedOracleJSON(t, observe(projected.Row), remapSharedOracleExpected(t, want))
	}
	for _, name := range []string{"mixed", "finite", "null"} {
		input, e := os.ReadFile(root + "source/inputs/input-four-" + name + ".json")
		if e != nil {
			t.Fatal(e)
		}
		var in struct {
			Operations []sharedSubscriptionOracleOperation `json:"operations"`
		}
		if e = json.Unmarshal(input, &in); e != nil {
			t.Fatal(e)
		}
		want := source.Stages[name].Run.Observations[0]
		t.Logf("four JSON source stage=%s", name)
		got, err := sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: "owned-source-1", Patch: in.Operations[0].Arguments["payload"]})
		if name == "mixed" {
			if err == nil {
				t.Fatal("mixed inf response succeeded")
			}
			assertStored(want.Row)
		} else {
			if err != nil {
				t.Fatal(err)
			}
			assertSharedOracleJSON(t, sharedOracleActualRows(t, got, false), remapSharedOracleExpected(t, want.Row))
		}
		if e = a.Close(); e != nil {
			t.Fatal(e)
		}
		a, e = New(Options{DataDir: dir, PluginMarketsOverride: &markets})
		if e != nil {
			t.Fatal(e)
		}
		p = a.startOriginalPlugin(context.Background(), "OwnedSharedHost")
		if p == nil {
			t.Fatal("four JSON cold worker absent")
		}
		for _, obs := range source.Stages[name].Cold.Observations {
			var input any
			switch obs.Method {
			case "get":
				input = plugins.SharedSubscriptionGetV1{ID: "owned-source-1"}
			case "list":
				input = plugins.SharedSubscriptionListV1{}
			case "list_by_tmdbid":
				input = plugins.SharedSubscriptionTMDBV1{TMDBID: json.RawMessage(`null`)}
			}
			got, err = sharedSubscriptionsCall(t, p, "subscriptions."+obs.Method, input)
			if name == "mixed" {
				if err == nil {
					t.Fatalf("cold %s serialized mixed inf", obs.Method)
				}
				assertStored(obs.Row)
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if obs.Method != "get" {
				var rows []plugins.SharedSubscriptionV1
				if e = json.Unmarshal(got, &rows); e != nil || len(rows) != 1 {
					t.Fatal("unexpected cold row list", e)
				}
				got = rows[0].Row
			} else {
				got = sharedOracleActualRows(t, got, false)
			}
			assertSharedOracleJSON(t, got, remapSharedOracleExpected(t, obs.Row))
		}
	}
}
