package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
)

func auditS05Seed(t *testing.T, a *App, id string) {
	t.Helper()
	s := Subscription{ID: id, Media: catalog.Media{Title: "Owned source semantics", MediaType: "tv", ID: 7001},
		SourceState: "N", State: "active", Revision: 1, Season: 1}
	if e := a.Store.Put(context.Background(), "subscription", id, s); e != nil {
		t.Fatal(e)
	}
}

func auditS05Row(t *testing.T, p *originalPluginInstance, method string, input any) map[string]json.RawMessage {
	t.Helper()
	raw, e := sharedSubscriptionsCall(t, p, method, input)
	if e != nil {
		t.Fatalf("worker %s: %v", method, e)
	}
	var value plugins.SharedSubscriptionV1
	if e = json.Unmarshal(raw, &value); e != nil {
		t.Fatal(e, string(raw))
	}
	var row map[string]json.RawMessage
	if e = json.Unmarshal(value.Row, &row); e != nil {
		t.Fatal(e, string(value.Row))
	}
	return row
}

func TestAuditS05UnrelatedFilterEditPreservesNull(t *testing.T) {
	a, _, p := sharedSubscriptionsFixture(t, []string{plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionsUpdate})
	auditS05Seed(t, a, "owned-null-filter")
	row := auditS05Row(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{
		ID: "owned-null-filter", Patch: json.RawMessage(`{"include":null,"quality":"A"}`)})
	if string(row["include"]) != "null" {
		t.Fatal("initial raw NULL absent", string(row["include"]))
	}
	var sub Subscription
	if e := a.Store.Get(context.Background(), "subscription", "owned-null-filter", &sub); e != nil {
		t.Fatal(e)
	}
	sub.Policy.Source = "B"
	params := *sub.Policy.FilterParams
	quality := "B"
	params.Quality = &quality
	sub.Policy.FilterParams = &params
	if e := a.Store.Put(context.Background(), "subscription", sub.ID, sub); e != nil {
		t.Fatal(e)
	}
	row = auditS05Row(t, p, plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: sub.ID})
	if string(row["include"]) != "null" {
		t.Fatalf("unrelated native quality edit changed source include NULL: got %s", row["include"])
	}
	if string(row["quality"]) != `"B"` {
		t.Fatalf("native edit not visible: %s", row["quality"])
	}
}

func TestAuditS05NativeStaleABAAndRawPrecision(t *testing.T) {
	a, _, p := sharedSubscriptionsFixture(t, []string{plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionsUpdate})
	auditS05Seed(t, a, "owned-aba")
	row := auditS05Row(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{
		ID: "owned-aba", Patch: json.RawMessage(`{"total_episode":null,"tmdbid":9007199254740993,"note":{"owned":[null,true,2.5]}}`)})
	if string(row["tmdbid"]) != "9007199254740993" {
		t.Fatal("int64 precision lost", string(row["tmdbid"]))
	}
	if !originalBaseJSONEqual(row["note"], json.RawMessage(`{"owned":[null,true,2.5]}`)) {
		t.Fatal("JSON shape lost", string(row["note"]))
	}
	var original Subscription
	if e := a.Store.Get(context.Background(), "subscription", "owned-aba", &original); e != nil {
		t.Fatal(e)
	}
	middle := original
	middle.TotalEpisodes = 9
	if e := a.Store.Put(context.Background(), "subscription", middle.ID, middle); e != nil {
		t.Fatal(e)
	}
	if e := a.Store.Put(context.Background(), "subscription", original.ID, original); e != nil {
		t.Fatal(e)
	}
	row = auditS05Row(t, p, plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: original.ID})
	if string(row["total_episode"]) != "0" {
		t.Fatal("stale A snapshot resurrected raw NULL", string(row["total_episode"]))
	}
	if string(row["tmdbid"]) != "9007199254740993" {
		t.Fatal("unrelated source fact lost", string(row["tmdbid"]))
	}
	row = auditS05Row(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{
		ID: original.ID, Patch: json.RawMessage(`{"extension_note":{"ordinary":1},"state":"outside-NRPS"}`)})
	if string(row["state"]) != `"outside-NRPS"` {
		t.Fatal("source mixed unknown update lost known state", string(row["state"]))
	}
	if _, ok := row["extension_note"]; ok {
		t.Fatal("unknown transient attribute persisted as column")
	}
}

func TestAuditS05FloatGrammarFromRealSource(t *testing.T) {
	a, _, p := sharedSubscriptionsFixture(t, []string{plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionsUpdate})
	auditS05Seed(t, a, "owned-float-grammar")
	var source struct {
		Observations []struct {
			Case      string `json:"case"`
			Arguments struct {
				Payload json.RawMessage `json:"payload"`
			} `json:"arguments"`
			Error json.RawMessage            `json:"error"`
			Fresh map[string]json.RawMessage `json:"fresh_get"`
		} `json:"observations"`
	}
	file := filepath.Join(os.Getenv("S05_AUDIT_FIXTURES"), "source-float.json")
	data, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(data, &source); e != nil {
		t.Fatal(e)
	}
	for _, observation := range source.Observations[:8] {
		t.Run(observation.Case, func(t *testing.T) {
			auditS05Row(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: "owned-float-grammar", Patch: json.RawMessage(`{"vote":7}`)})
			raw, e := sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: "owned-float-grammar", Patch: observation.Arguments.Payload})
			if len(observation.Error) > 0 {
				if e == nil {
					t.Errorf("source rejected FLOAT input %s but actual worker accepted %s", observation.Arguments.Payload, raw)
				}
				after := auditS05Row(t, p, plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: "owned-float-grammar"})
				if !originalBaseJSONEqual(after["vote"], json.RawMessage(`7`)) {
					t.Errorf("source rollback not preserved: %s", after["vote"])
				}
				return
			}
			if e != nil {
				t.Fatalf("source accepted %s -> %s but worker rejected: %v", observation.Arguments.Payload, observation.Fresh["vote"], e)
			}
			var result plugins.SharedSubscriptionV1
			if e = json.Unmarshal(raw, &result); e != nil {
				t.Fatal(e)
			}
			var row map[string]json.RawMessage
			if e = json.Unmarshal(result.Row, &row); e != nil {
				t.Fatal(e)
			}
			if !originalBaseJSONEqual(row["vote"], observation.Fresh["vote"]) {
				t.Fatalf("source FLOAT mismatch: got %s expected %s", row["vote"], observation.Fresh["vote"])
			}
		})
	}
}

func TestAuditS05NativeFailedWriteDoesNotInvalidateSourceNull(t *testing.T) {
	a, _, p := sharedSubscriptionsFixture(t, []string{plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionsUpdate})
	auditS05Seed(t, a, "owned-native-rollback")
	auditS05Row(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{
		ID: "owned-native-rollback", Patch: json.RawMessage(`{"total_episode":null}`)})
	var original Subscription
	if e := a.Store.Get(context.Background(), "subscription", "owned-native-rollback", &original); e != nil {
		t.Fatal(e)
	}
	if _, e := a.Store.DB.Exec(`CREATE TRIGGER owned_native_write_abort BEFORE UPDATE ON records WHEN OLD.kind='subscription' BEGIN SELECT RAISE(ABORT,'owned native write failure'); END`); e != nil {
		t.Fatal(e)
	}
	changed := original
	changed.TotalEpisodes = 9
	if e := a.Store.Put(context.Background(), "subscription", changed.ID, changed); e == nil {
		t.Fatal("native SQL failure did not propagate")
	}
	after := auditS05Row(t, p, plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: original.ID})
	if string(after["total_episode"]) != "null" {
		t.Fatal("failed native write invalidated source NULL", string(after["total_episode"]))
	}
	if _, e := a.Store.DB.Exec(`DROP TRIGGER owned_native_write_abort`); e != nil {
		t.Fatal(e)
	}
	if e := a.Store.Put(context.Background(), "subscription", original.ID, original); e != nil {
		t.Fatal(e)
	}
	after = auditS05Row(t, p, plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: original.ID})
	if string(after["total_episode"]) != "null" {
		t.Fatal("aborted intermediate B falsely invalidated old A", string(after["total_episode"]))
	}
}

func TestAuditS05DeclaredReadOnlyAndExpiredCallback(t *testing.T) {
	a, _, p := sharedSubscriptionsFixture(t, []string{plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionsList})
	auditS05Seed(t, a, "owned-declared-readonly")
	before := auditS05Row(t, p, plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: "owned-declared-readonly"})
	if _, e := sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{
		ID: "owned-declared-readonly", Patch: json.RawMessage(`{"name":"should-not-write"}`)}); e == nil {
		t.Fatal("undeclared update unexpectedly admitted")
	}
	after := auditS05Row(t, p, plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: "owned-declared-readonly"})
	if !originalBaseJSONEqual(before["name"], after["name"]) {
		t.Fatal("undeclared update changed native row")
	}
	first := sharedSubscriptionsSnapshot(t, p)
	second := sharedSubscriptionsSnapshot(t, p)
	if string(second["had_previous"]) != "true" || string(second["stale_denied"]) != "true" {
		t.Fatal("old callback was not exercised", second)
	}
	if !originalBaseJSONEqual(first["pid"], second["pid"]) {
		t.Fatal("callback check restarted worker")
	}
	if _, e := sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsList, plugins.SharedSubscriptionListV1{}); e != nil {
		t.Fatal("fresh declared list failed", e)
	}
}

func TestAuditS05ValidJSONExponentOverflowHasSourceSideEffects(t *testing.T) {
	a, _, p := sharedSubscriptionsFixture(t, []string{plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionsUpdate})
	auditS05Seed(t, a, "owned-exponent")
	// The literal token is legal JSON 1e400, not the non-standard Infinity token.
	raw, e := sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{
		ID: "owned-exponent", Patch: json.RawMessage(`{"name":1e400}`)})
	if e != nil {
		t.Errorf("source persisted TEXT 'Inf' from legal JSON 1e400, worker rejected: %v", e)
	} else {
		var out plugins.SharedSubscriptionV1
		if e = json.Unmarshal(raw, &out); e != nil {
			t.Fatal(e)
		}
		var row map[string]json.RawMessage
		if e = json.Unmarshal(out.Row, &row); e != nil {
			t.Fatal(e)
		}
		if string(row["name"]) != `"Inf"` {
			t.Errorf("source TEXT affinity differs: %s", row["name"])
		}
	}
	if _, e = sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{
		ID: "owned-exponent", Patch: json.RawMessage(`{"tmdbid":1e400}`)}); e == nil {
		t.Fatal("nonfinite source row unexpectedly serializable")
	}
	var sub Subscription
	if e = a.Store.Get(context.Background(), "subscription", "owned-exponent", &sub); e != nil {
		t.Fatal(e)
	}
	if sub.SharedSourceFields["tmdbid"].Nonfinite != "+Inf" {
		t.Errorf("source committed INTEGER-column infinity before JSON response failure; target lost side effect: %#v", sub.SharedSourceFields["tmdbid"])
	}
}

func TestAuditS05NestedJSONExponentPreservesCommitAndBoundary(t *testing.T) {
	a, _, p := sharedSubscriptionsFixture(t, []string{plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionsUpdate})
	auditS05Seed(t, a, "owned-nested-exponent")
	for _, patch := range []json.RawMessage{json.RawMessage(`{"note":{"n":1e400}}`), json.RawMessage(`{"sites":[-1e400]}`)} {
		var before Subscription
		if e := a.Store.Get(context.Background(), "subscription", "owned-nested-exponent", &before); e != nil {
			t.Fatal(e)
		}
		if raw, e := sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: before.ID, Patch: patch}); e == nil {
			t.Errorf("source committed nested infinity then actual JSONResponse failed; target returned success: patch=%s row=%s", patch, raw)
		}
		var after Subscription
		if e := a.Store.Get(context.Background(), "subscription", before.ID, &after); e != nil {
			t.Fatal(e)
		}
		if after.Revision != before.Revision+1 {
			t.Errorf("source nested infinity write not committed: revision %d -> %d", before.Revision, after.Revision)
		}
		if _, e := sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: before.ID}); e == nil {
			t.Error("fresh get of persisted nested infinity incorrectly serialized")
		}
		row := auditS05Row(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: before.ID, Patch: json.RawMessage(`{"note":null,"sites":[]}`)})
		if string(row["note"]) != "null" || string(row["sites"]) != "[]" {
			t.Fatal("repair did not restore normal JSON boundary", row)
		}
	}
}

func TestAuditS05JSONFiniteNumberUsesSourceDecode(t *testing.T) {
	a, _, p := sharedSubscriptionsFixture(t, []string{plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionsUpdate})
	auditS05Seed(t, a, "owned-json-finite")
	data, e := os.ReadFile(filepath.Join(os.Getenv("S05_AUDIT_FIXTURES"), "source-finite.json"))
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Observations []struct {
			Fresh map[string]json.RawMessage `json:"fresh_get"`
		} `json:"observations"`
	}
	if e = json.Unmarshal(data, &source); e != nil {
		t.Fatal(e)
	}
	row := auditS05Row(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: "owned-json-finite", Patch: json.RawMessage(`{"note":{"n":1.234567890123456789}}`)})
	assertSharedOracleJSON(t, row["note"], source.Observations[0].Fresh["note"])
}

func TestAuditS05AllJSONColumnsWithSourceCold(t *testing.T) {
	dir := t.TempDir()
	markets := []string{}
	a, e := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = a.Close() }()
	cookie := setupTestAdmin(t, a)
	p := installSharedSubscriptionsFixture(t, a, cookie, []string{plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionsUpdate})
	auditS05Seed(t, a, "owned-four-json")
	var oracle struct {
		Stages map[string]struct {
			Run struct {
				Observations []struct {
					JSONColumns map[string]json.RawMessage `json:"json_columns"`
				} `json:"observations"`
			} `json:"run"`
		} `json:"stages"`
	}
	b, e := os.ReadFile(filepath.Join(os.Getenv("S05_AUDIT_FIXTURES"), "canonical-four.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &oracle); e != nil {
		t.Fatal(e)
	}
	cold := func() {
		before := sharedSubscriptionsSnapshot(t, p)
		if e = a.Close(); e != nil {
			t.Fatal(e)
		}
		a, e = New(Options{DataDir: dir, PluginMarketsOverride: &markets})
		if e != nil {
			t.Fatal(e)
		}
		p = a.startOriginalPlugin(context.Background(), "OwnedSharedHost")
		if p == nil {
			t.Fatal("cold real artifact worker missing")
		}
		after := sharedSubscriptionsSnapshot(t, p)
		if string(before["pid"]) == string(after["pid"]) {
			t.Fatal("cold reused real worker process")
		}
		t.Logf("independent JSON cold old_pid=%s new_pid=%s", before["pid"], after["pid"])
	}
	check := func(stage string) {
		t.Helper()
		if stage == "mixed" {
			if _, e := sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: "owned-four-json"}); e == nil {
				t.Fatal("persisted nested infinity serialized")
			}
			return
		}
		row := auditS05Row(t, p, plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: "owned-four-json"})
		for _, key := range []string{"note", "sites", "episode_priority", "filter_groups"} {
			assertSharedOracleJSON(t, row[key], oracle.Stages[stage].Run.Observations[0].JSONColumns[key])
		}
	}
	for _, stage := range []string{"mixed", "finite", "null"} {
		b, e := os.ReadFile(filepath.Join(os.Getenv("S05_AUDIT_FIXTURES"), "input-four-"+stage+".json"))
		if e != nil {
			t.Fatal(e)
		}
		var plan struct {
			Operations []struct {
				Arguments struct {
					Payload json.RawMessage `json:"payload"`
				} `json:"arguments"`
			} `json:"operations"`
		}
		if e = json.Unmarshal(b, &plan); e != nil {
			t.Fatal(e)
		}
		var before Subscription
		if e = a.Store.Get(context.Background(), "subscription", "owned-four-json", &before); e != nil {
			t.Fatal(e)
		}
		_, e = sharedSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: before.ID, Patch: plan.Operations[0].Arguments.Payload})
		if (e != nil) != (stage == "mixed") {
			t.Fatalf("source update JSON boundary differs at %s: %v", stage, e)
		}
		var after Subscription
		if e = a.Store.Get(context.Background(), "subscription", before.ID, &after); e != nil {
			t.Fatal(e)
		}
		if after.Revision != before.Revision+1 {
			t.Fatalf("update not committed at %s", stage)
		}
		check(stage)
		cold()
		check(stage)
	}
}
