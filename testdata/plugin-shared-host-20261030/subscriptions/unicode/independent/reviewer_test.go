package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"light-media/internal/plugins"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type unicodeIndependentCase struct {
	ID                             int `json:"id"`
	Handle, Name, Envelope, SHA256 string
	Fault                          bool `json:"deferred_commit_fault"`
}
type unicodeIndependentExpected struct {
	ID                     int             `json:"id"`
	OperationError         json.RawMessage `json:"operation_error"`
	OperationResponseError *string         `json:"operation_response_error"`
	FreshResponseError     *string         `json:"fresh_response_error"`
}

func unicodeIndependentSave(t *testing.T, name string, value any) {
	t.Helper()
	root := os.Getenv("S05_INDEPENDENT_EVIDENCE")
	if root == "" {
		t.Fatal("S05_INDEPENDENT_EVIDENCE required")
	}
	raw, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, name), raw, 0600); e != nil {
		t.Fatal(e)
	}
}
func unicodeIndependentRaw(t *testing.T, p *originalPluginInstance, envelope string) (json.RawMessage, error) {
	t.Helper()
	raw := json.RawMessage(envelope)
	out, e := p.callSession(context.Background(), plugins.Runner{Timeout: 20 * time.Second}, "resource", raw, nil)
	return out.Result, e
}
func unicodeIndependentStored(t *testing.T, a *App, id string) (map[string]any, []byte) {
	t.Helper()
	var raw []byte
	if e := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='subscription' AND id=?`, id).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(raw, &fields); e != nil {
		t.Fatal(e)
	}
	// RawMessage preserves codepoint escapes. Never decode source JSON into Go any/string.
	var native struct {
		Keyword  string
		Revision int
	}
	if e := json.Unmarshal(raw, &native); e != nil {
		t.Fatal(e)
	}
	return map[string]any{"keyword": native.Keyword, "revision": native.Revision, "source_fields": fields["shared_source_fields"], "raw_database": json.RawMessage(raw)}, raw
}

// 独立 literal envelope 穿过真实 SDK 子进程与生产宿主；预期来自同字节的新鲜原版运行。
func TestS05UnicodeIndependentExactInputMatrix(t *testing.T) {
	inputPath := os.Getenv("S05_INDEPENDENT_INPUTS")
	sourcePath := os.Getenv("S05_INDEPENDENT_SOURCE")
	raw, e := os.ReadFile(inputPath)
	if e != nil {
		t.Fatal(e)
	}
	var cases []unicodeIndependentCase
	if e = json.Unmarshal(raw, &cases); e != nil {
		t.Fatal(e)
	}
	source, e := os.ReadFile(sourcePath)
	if e != nil {
		t.Fatal(e)
	}
	var oracle struct {
		Records []unicodeIndependentExpected `json:"records"`
		Network []string                     `json:"network_attempts"`
	}
	if e = json.Unmarshal(source, &oracle); e != nil || len(oracle.Records) != 19 || len(oracle.Network) != 0 {
		t.Fatal("actual source matrix invalid", e)
	}
	a, _, p := sharedSubscriptionsFixture(t, sharedSubscriptionsAll)
	ctx := context.Background()
	snapshot := func(obj *originalPluginInstance) json.RawMessage {
		var s map[string]json.RawMessage
		if e := obj.readSession(ctx, "snapshot", &s); e != nil {
			t.Fatal(e)
		}
		return s["pid"]
	}
	warmPID := snapshot(p)
	results := map[string]any{}
	requests := []any{}
	for i, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if got := fmt.Sprintf("%x", sha256.Sum256([]byte(c.Envelope))); got != c.SHA256 {
				t.Fatal("input bytes mutated", got, c.SHA256)
			}
			seedSharedSubscription(t, a, c.Handle)
			if c.Fault {
				var enabled int
				if e := a.Store.DB.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled); e != nil || enabled != 1 {
					t.Fatal("FK disabled", e)
				}
				for _, stmt := range []string{`CREATE TABLE independent_parent(id INTEGER PRIMARY KEY)`, `CREATE TABLE independent_guard(id INTEGER REFERENCES independent_parent(id) DEFERRABLE INITIALLY DEFERRED)`, `CREATE TRIGGER independent_fault AFTER UPDATE ON records WHEN NEW.kind='subscription' AND NEW.id='` + c.Handle + `' BEGIN INSERT INTO independent_guard VALUES(999); END`} {
					if _, e := a.Store.DB.Exec(stmt); e != nil {
						t.Fatal(e)
					}
				}
			}
			_, before := unicodeIndependentStored(t, a, c.Handle)
			response, err := unicodeIndependentRaw(t, p, c.Envelope)
			stored, after := unicodeIndependentStored(t, a, c.Handle)
			expected := oracle.Records[i]
			if expected.ID != c.ID {
				t.Fatal("source order invalid")
			}
			operationFailed := len(expected.OperationError) > 0 && string(expected.OperationError) != "null"
			var envelope struct{ Method string }
			_ = json.Unmarshal([]byte(c.Envelope), &envelope)
			wantFailure := operationFailed || expected.OperationResponseError != nil || envelope.Method == plugins.SharedSubscriptionsUpdate && expected.FreshResponseError != nil
			if (err != nil) != wantFailure {
				t.Errorf("source boundary differs: got error=%v wanted failure=%v", err, wantFailure)
			}
			if operationFailed && !bytes.Equal(before, after) {
				t.Error("source bind/commit failure did not roll back full record")
			}
			if c.Fault {
				if _, e := a.Store.DB.Exec(`DROP TRIGGER independent_fault`); e != nil {
					t.Fatal(e)
				}
				var n int
				if e := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM independent_guard`).Scan(&n); e != nil || n != 0 {
					t.Fatal("deferred commit failure left guard rows", e, n)
				}
			}
			stored["operation_error"] = err != nil
			stored["response"] = response
			results[c.Handle] = stored
			requests = append(requests, map[string]any{"id": c.ID, "envelope": c.Envelope, "sha256": c.SHA256})
		})
	}
	unicodeIndependentSave(t, "requests.json", requests)
	unicodeIndependentSave(t, "warm.json", results)
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	cold, e := New(Options{DataDir: a.Dir})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = cold.Close() })
	if e = cold.initializeOriginalPlugins(ctx); e != nil {
		t.Fatal(e)
	}
	cold.wg.Wait()
	v, ok := cold.pluginRunningInstances.Load("OwnedSharedHost")
	if !ok {
		t.Fatal("cold worker missing")
	}
	p = v.(*originalPluginInstance)
	coldPID := snapshot(p)
	if bytes.Equal(warmPID, coldPID) {
		t.Fatal("cold reused actual process")
	}
	coldResults := map[string]any{}
	repairResults := map[string]any{}
	for i, c := range cases {
		stored, _ := unicodeIndependentStored(t, cold, c.Handle)
		envelope := `{"method":"subscriptions.get","payload":{"id":"` + c.Handle + `"}}`
		_, err := unicodeIndependentRaw(t, p, envelope)
		if (err != nil) != (oracle.Records[i].FreshResponseError != nil) {
			t.Errorf("cold response mismatch %s: %v", c.Name, err)
		}
		stored["get_error"] = err != nil
		coldResults[c.Handle] = stored
		_, before := unicodeIndependentStored(t, cold, c.Handle)
		// A stale CAS cannot repair or modify a still-unencodable row.
		stale := `{"method":"subscriptions.update","payload":{"id":"` + c.Handle + `","patch":{"keyword":"stale"},"expected_revision":-1}}`
		if _, err = unicodeIndependentRaw(t, p, stale); err == nil {
			t.Error("stale CAS passed")
		}
		_, after := unicodeIndependentStored(t, cold, c.Handle)
		if !bytes.Equal(before, after) {
			t.Error("stale CAS mutated persisted codepoints")
		}
		repair := `{"method":"subscriptions.update","payload":{"id":"` + c.Handle + `","patch":{"note":{"valid":"\ufffd","pair":"\ud83d\ude80"},"sites":[],"filter_groups":[],"episode_priority":{"1":2},"keyword":"repaired"}}}`
		response, err := unicodeIndependentRaw(t, p, repair)
		if err != nil {
			t.Errorf("ordinary source repair failed %s: %v", c.Name, err)
		}
		stored, _ = unicodeIndependentStored(t, cold, c.Handle)
		stored["response"] = response
		repairResults[c.Handle] = stored
	}
	unicodeIndependentSave(t, "cold.json", coldResults)
	unicodeIndependentSave(t, "repair.json", repairResults)
	unicodeIndependentSave(t, "processes.json", map[string]any{"warm": warmPID, "cold": coldPID})
}
