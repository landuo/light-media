package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func TestSharedSubscriptionsUnicodeCommittedBeforeResponseFailure(t *testing.T) {
	a, _, p := sharedSubscriptionsFixture(t, sharedSubscriptionsAll)
	id := "owned-unicode-committed"
	seedSharedSubscription(t, a, id)
	raw := json.RawMessage(`{"note":{"value":"\ud800","\udc00":"key-value"},"keyword":"committed"}`)
	fields, err := sharedSubscriptionPatch(raw)
	if err != nil {
		t.Fatal(err)
	}
	encodedFields, _ := json.Marshal(fields)
	t.Logf("parsed patch: %s", encodedFields)
	normalized, _, err := sharedSubscriptionNormalizeJSON(fields["note"])
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("normalized: %s", normalized)
	b, err := unicodeSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: id, Patch: raw})
	var saved []byte
	if e := a.Store.DB.QueryRowContext(context.Background(), "SELECT payload FROM records WHERE kind='subscription' AND id=?", id).Scan(&saved); e != nil {
		t.Fatal(e)
	}
	t.Logf("response=%s error=%v saved=%s", b, err, saved)
	if err == nil {
		t.Fatal("surrogate response incorrectly succeeded")
	}
	var sub Subscription
	if e := json.Unmarshal(saved, &sub); e != nil {
		t.Fatal(e)
	}
	if sub.Keyword != "committed" || sub.Revision != 2 {
		t.Fatal("response error rolled back committed source update")
	}
	if !bytes.Contains(sub.SharedSourceFields["note"].Value, []byte(`\ud800`)) {
		t.Fatal("source codepoint lost")
	}
}

var unicodeRequestSequence atomic.Uint64

func unicodeSubscriptionsCall(t *testing.T, p *originalPluginInstance, method string, input any) (json.RawMessage, error) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"method": method, "payload": input})
	if err != nil {
		t.Fatal(err)
	}
	sequence := unicodeRequestSequence.Add(1)
	t.Logf("actual typed worker request SHA256=%x bytes=%d", sha256.Sum256(raw), len(raw))
	if dir := os.Getenv("S05_UNICODE_EVIDENCE"); dir != "" {
		name := fmt.Sprintf("%s-%04d-request.json", strings.ReplaceAll(t.Name(), "/", "_"), sequence)
		if e := os.WriteFile(filepath.Join(dir, name), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := p.callSession(ctx, plugins.Runner{Timeout: 20 * time.Second}, "resource", raw, nil)
	return out.Result, err
}

func unicodeStored(t *testing.T, a *App, id string) (Subscription, []byte) {
	t.Helper()
	var raw []byte
	if e := a.Store.DB.QueryRowContext(context.Background(), "SELECT payload FROM records WHERE kind='subscription' AND id=?", id).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var sub Subscription
	if e := json.Unmarshal(raw, &sub); e != nil {
		t.Fatal(e)
	}
	return sub, raw
}
func unicodeCompact(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var b bytes.Buffer
	if e := json.Compact(&b, raw); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func unicodeExact(t *testing.T, actual, expected json.RawMessage) {
	t.Helper()
	if !bytes.Equal(unicodeCompact(t, actual), unicodeCompact(t, expected)) {
		t.Fatalf("codepoint-preserving JSON differs\nactual %s\nsource %s", actual, expected)
	}
}

type unicodeSourceRecord struct {
	ID       int                            `json:"id"`
	Column   string                         `json:"column"`
	Input    string                         `json:"input_raw"`
	Get      map[string]json.RawMessage     `json:"get"`
	Response struct{ Status, Class string } `json:"get_response"`
}

func unicodeSource(t *testing.T, phase string) []unicodeSourceRecord {
	t.Helper()
	root := os.Getenv("S05_UNICODE_SOURCE_EVIDENCE")
	if root == "" {
		root = filepath.Join("..", "..", "testdata", "plugin-shared-host-20261030", "subscriptions", "unicode")
	}
	path := filepath.Join(root, "source-"+phase+".json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Optional owner raw observation root; the default is fully portable.
		path = filepath.Join(root, "source-"+phase+"-02", "observed.json")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var out struct {
		Records []unicodeSourceRecord `json:"records"`
		Network []string              `json:"network_attempts"`
	}
	if e = json.Unmarshal(raw, &out); e != nil {
		t.Fatal(e)
	}
	if len(out.Network) != 0 || len(out.Records) != 6 {
		t.Fatal("source observations incomplete")
	}
	return out.Records
}
func TestSharedSubscriptionsUnicodeFourColumnsColdRecovery(t *testing.T) {
	warm, recovered := unicodeSource(t, "warm"), unicodeSource(t, "recover")
	sourceCold := unicodeSource(t, "cold")
	a, _, p := sharedSubscriptionsFixture(t, sharedSubscriptionsAll)
	beforePID := sharedSubscriptionsSnapshot(t, p)["pid"]
	for _, record := range warm {
		id := fmt.Sprintf("unicode-source-%d", record.ID)
		seedSharedSubscription(t, a, id)
		patch := json.RawMessage(fmt.Sprintf(`{"%s":%s,"keyword":"committed-%d"}`, record.Column, record.Input, record.ID))
		response, e := unicodeSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: id, Patch: patch})
		if e == nil || len(response) != 0 || record.Response.Status != "error" {
			t.Fatalf("expected committed response encoding failure: %d %s %v", record.ID, response, e)
		}
		sub, _ := unicodeStored(t, a, id)
		if sub.Revision != 2 || sub.Keyword != fmt.Sprintf("committed-%d", record.ID) {
			t.Fatal("response failure incorrectly rolled back native columns", sub.Revision, sub.Keyword)
		}
		if record.ID != 6 {
			unicodeExact(t, sub.SharedSourceFields[record.Column].Value, record.Get[record.Column])
		} else {
			field := sub.SharedSourceFields[record.Column]
			if field.Nonfinite != sharedSubscriptionNestedNonfinite || !bytes.Contains(field.Value, []byte(`\ud800`)) || !bytes.Contains(field.Value, []byte(`1e999`)) {
				t.Fatal("mixed nonfinite/surrogate facts lost", string(field.Value), field.Nonfinite)
			}
		}
	}
	if e := a.Close(); e != nil {
		t.Fatal(e)
	}
	cold, e := New(Options{DataDir: a.Dir})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = cold.Close() })
	if e = cold.initializeOriginalPlugins(context.Background()); e != nil {
		t.Fatal(e)
	}
	cold.wg.Wait()
	obj, ok := cold.pluginRunningInstances.Load("OwnedSharedHost")
	if !ok {
		t.Fatal("cold actual worker missing")
	}
	p = obj.(*originalPluginInstance)
	if bytes.Equal(sharedSubscriptionsSnapshot(t, p)["pid"], beforePID) {
		t.Fatal("cold worker reused old PID")
	}
	for _, record := range sourceCold {
		id := fmt.Sprintf("unicode-source-%d", record.ID)
		sub, _ := unicodeStored(t, cold, id)
		if record.ID != 6 {
			unicodeExact(t, sub.SharedSourceFields[record.Column].Value, record.Get[record.Column])
		}
		if _, e = unicodeSubscriptionsCall(t, p, plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: id}); e == nil {
			t.Fatal("cold get silently encoded surrogate")
		}
	}
	if _, e = unicodeSubscriptionsCall(t, p, plugins.SharedSubscriptionsList, plugins.SharedSubscriptionListV1{}); e == nil {
		t.Fatal("list silently encoded source-invalid UTF8/nonfinite")
	}
	if _, e = unicodeSubscriptionsCall(t, p, plugins.SharedSubscriptionsListByTMDBID, plugins.SharedSubscriptionTMDBV1{TMDBID: json.RawMessage(`7001`)}); e == nil {
		t.Fatal("tmdb list silently encoded source-invalid UTF8/nonfinite")
	}
	out, e := unicodeSubscriptionsCall(t, p, plugins.SharedSubscriptionsList, plugins.SharedSubscriptionListV1{State: json.RawMessage(`"NO_MATCH"`)})
	if e != nil || string(out) != "[]" {
		t.Fatal("unselected invalid source row blocked empty query", string(out), e)
	}
	for _, record := range recovered {
		id := fmt.Sprintf("unicode-source-%d", record.ID)
		// A legal unrelated field still commits despite the unrepaired JSON response.
		if _, e = unicodeSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: id, Patch: json.RawMessage(`{"keyword":"second-commit"}`)}); e == nil {
			t.Fatal("unrepaired response succeeded")
		}
		sub, before := unicodeStored(t, cold, id)
		if sub.Revision != 3 || sub.Keyword != "second-commit" {
			t.Fatal("unrelated update failed to commit")
		}
		wrong := 1
		patch := json.RawMessage(fmt.Sprintf(`{"%s":%s,"keyword":"recovered-%d"}`, record.Column, record.Get[record.Column], record.ID))
		if _, e = unicodeSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: id, Patch: patch, ExpectedRevision: &wrong}); e == nil {
			t.Fatal("stale CAS passed")
		}
		_, after := unicodeStored(t, cold, id)
		if !bytes.Equal(before, after) {
			t.Fatal("stale CAS modified row")
		}
		expected := 3
		out, e = unicodeSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: id, Patch: patch, ExpectedRevision: &expected})
		if e != nil {
			t.Fatal("legal recovery blocked", record.ID, e)
		}
		row := decodeSharedSubscription(t, out)
		var values map[string]json.RawMessage
		if e = json.Unmarshal(row.Row, &values); e != nil {
			t.Fatal(e)
		}
		if row.Revision != 4 {
			t.Fatal("recovery revision", row.Revision)
		}
		unicodeExact(t, values[record.Column], record.Get[record.Column])
	}
	if _, e = unicodeSubscriptionsCall(t, p, plugins.SharedSubscriptionsList, plugins.SharedSubscriptionListV1{}); e != nil {
		t.Fatal("list did not recover", e)
	}
	if _, e = unicodeSubscriptionsCall(t, p, plugins.SharedSubscriptionsListByTMDBID, plugins.SharedSubscriptionTMDBV1{TMDBID: json.RawMessage(`7001`)}); e != nil {
		t.Fatal("tmdb did not recover", e)
	}
}

func TestSharedSubscriptionsUnicodeScalarBindingRollsBack(t *testing.T) {
	a, _, p := sharedSubscriptionsFixture(t, sharedSubscriptionsAll)
	id := "unicode-bind"
	seedSharedSubscription(t, a, id)
	for _, patch := range []json.RawMessage{json.RawMessage(`{"keyword":"\ud800"}`), json.RawMessage(`{"keyword":"\ud800","note":{"bad":"\udc00"},"name":"must-rollback"}`)} {
		_, before := unicodeStored(t, a, id)
		if _, e := unicodeSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: id, Patch: patch}); e == nil {
			t.Fatal("invalid SQLite scalar string accepted")
		}
		_, after := unicodeStored(t, a, id)
		if !bytes.Equal(before, after) {
			t.Fatal("mixed scalar bind failure partially committed")
		}
	}
	for _, q := range []struct {
		method string
		input  any
	}{
		{plugins.SharedSubscriptionsList, plugins.SharedSubscriptionListV1{State: json.RawMessage(`"\ud800"`)}},
		{plugins.SharedSubscriptionsListByTMDBID, plugins.SharedSubscriptionTMDBV1{TMDBID: json.RawMessage(`"\ud800"`)}},
		{plugins.SharedSubscriptionsListByTMDBID, plugins.SharedSubscriptionTMDBV1{TMDBID: json.RawMessage(`7001`), Season: json.RawMessage(`"\udc00"`)}},
	} {
		if _, e := unicodeSubscriptionsCall(t, p, q.method, q.input); e == nil {
			t.Fatal("invalid source query operand accepted", q.method)
		}
	}
	// Reopen SQLite and the actual worker before recovery: failed mixed JSON
	// and TEXT binding must not reappear as a partial commit after restart.
	_, beforeCold := unicodeStored(t, a, id)
	if e := a.Close(); e != nil {
		t.Fatal(e)
	}
	cold, e := New(Options{DataDir: a.Dir})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = cold.Close() })
	if e = cold.initializeOriginalPlugins(context.Background()); e != nil {
		t.Fatal(e)
	}
	cold.wg.Wait()
	obj, ok := cold.pluginRunningInstances.Load("OwnedSharedHost")
	if !ok {
		t.Fatal("cold worker missing after scalar rollback")
	}
	a, p = cold, obj.(*originalPluginInstance)
	_, afterCold := unicodeStored(t, a, id)
	if !bytes.Equal(beforeCold, afterCold) {
		t.Fatal("binding rollback changed durable payload on cold reopen")
	}
	if _, e = unicodeSubscriptionsCall(t, p, plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: id}); e != nil {
		t.Fatal("cold prior row cannot be queried after rollback", e)
	}
	patch := json.RawMessage(`{"unknown\ud800":"\udc00","keyword":"ordinary-committed"}`)
	if _, e := unicodeSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: id, Patch: patch}); e != nil {
		t.Fatal("unknown transient codepoint was globally rejected", e)
	}
	sub, _ := unicodeStored(t, a, id)
	if sub.Keyword != "ordinary-committed" || sub.Revision != 2 {
		t.Fatal("ordinary source commit blocked")
	}
	if _, e := unicodeSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: id, Patch: json.RawMessage(`{"keyword":"\ufffd","state":"\ud83d\ude42"}`)}); e != nil {
		t.Fatal("legal replacement/valid pair rejected", e)
	}
	if _, e := unicodeSubscriptionsCall(t, p, plugins.SharedSubscriptionsList, plugins.SharedSubscriptionListV1{State: json.RawMessage(`"\ud83d\ude42"`)}); e != nil {
		t.Fatal("legal query pair rejected", e)
	}
}

func TestSharedSubscriptionsUnicodeNativeABAAndCodepointIdentity(t *testing.T) {
	a, _, p := sharedSubscriptionsFixture(t, sharedSubscriptionsAll)
	id := "unicode-aba"
	seedSharedSubscription(t, a, id)
	update := func(raw string, wantError bool) {
		t.Helper()
		_, e := unicodeSubscriptionsCall(t, p, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: id, Patch: json.RawMessage(raw)})
		if (e != nil) != wantError {
			t.Fatalf("update error=%v expectedFailure=%v", e, wantError)
		}
	}
	update(`{"episode_priority":{"\ufffd":7}}`, false)
	stale, _ := unicodeStored(t, a, id)
	update(`{"episode_priority":{"\ud800":7}}`, true)
	stale.Keyword = "native-unrelated"
	if e := a.Store.Put(context.Background(), "subscription", id, stale); e != nil {
		t.Fatal(e)
	}
	current, _ := unicodeStored(t, a, id)
	unicodeExact(t, current.SharedSourceFields["episode_priority"].Value, json.RawMessage(`{"\ud800":7}`))
	stale = current
	update(`{"episode_priority":{"\udc00":7}}`, true)
	if e := a.Store.Put(context.Background(), "subscription", id, stale); e != nil {
		t.Fatal(e)
	}
	current, _ = unicodeStored(t, a, id)
	unicodeExact(t, current.SharedSourceFields["episode_priority"].Value, json.RawMessage(`{"\udc00":7}`))
	current.EpisodePriority = map[string]int{"1": 9}
	if e := a.Store.Put(context.Background(), "subscription", id, current); e != nil {
		t.Fatal(e)
	}
	current, _ = unicodeStored(t, a, id)
	if _, ok := current.SharedSourceFields["episode_priority"]; ok {
		t.Fatal("native edit retained stale source raw")
	}
	// Reusing an older raw snapshot after a genuine native mutation must not
	// resurrect it merely because the limited projection has returned to A.
	if e := a.Store.Put(context.Background(), "subscription", id, stale); e != nil {
		t.Fatal(e)
	}
	current, _ = unicodeStored(t, a, id)
	if _, ok := current.SharedSourceFields["episode_priority"]; ok {
		t.Fatal("native ABA resurrected source surrogate")
	}
	if _, e := unicodeSubscriptionsCall(t, p, plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: id}); e != nil {
		t.Fatal("native replacement failed to recover", e)
	}
}

func TestSharedSubscriptionsUnicodeJSONValueSemantics(t *testing.T) {
	raw := json.RawMessage(`{"\ud800":1,"\ud800":2,"\ufffd":3,"a":1,"\u0061":4,"\ud83d\ude42":5,"🙂":6,"integer":1208925819614629174706176,"negative_zero":-0.0,"float":1e0}`)
	value, nonfinite, e := sharedSubscriptionNormalizeJSON(raw)
	if e != nil || nonfinite {
		t.Fatal(e)
	}
	unicodeExact(t, value, json.RawMessage(`{"\ud800":2,"\ufffd":3,"a":4,"\ud83d\ude42":6,"integer":1208925819614629174706176,"negative_zero":-0.0,"float":1.0}`))
	if sharedSubscriptionJSONEqual(json.RawMessage(`"\ud800"`), json.RawMessage(`"\ufffd"`)) || sharedSubscriptionJSONEqual(json.RawMessage(`"\ud800"`), json.RawMessage(`"\udc00"`)) {
		t.Fatal("distinct source codepoints collapsed")
	}
	if !sharedSubscriptionJSONEqual(json.RawMessage(`{"\ud83d\ude42":1}`), json.RawMessage(`{"🙂":1}`)) {
		t.Fatal("valid pair identity differed")
	}
	if sharedSubscriptionJSONEqual(json.RawMessage(`false`), json.RawMessage(`0`)) {
		t.Fatal("boolean/number source facts merged")
	}
}
