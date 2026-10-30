package app

import (
	"bytes"
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Independent overlong component, never created; no identity/permission/symlink manipulations.
func TestSharedLocalStorageOrdinaryLongComponent(t *testing.T) {
	a, cookie, _ := sharedLocalStorageFixture(t, sharedLocalStorageAll)
	root := sharedLocalStorageRoot(t, a)
	regular := filepath.Join(root, "regular.mkv")
	content := []byte("independent synthetic regular file preserved")
	if e := sharedLocalStorageFixtureWrite(t, regular, content, 0600); e != nil {
		t.Fatal(e)
	}
	type sourceOp struct {
		Case          string          `json:"case"`
		Method        string          `json:"method"`
		Returned      json.RawMessage `json:"returned"`
		CallbackCount int             `json:"callback_count"`
		EventCount    int             `json:"event_count"`
	}
	var oracle struct {
		Operations   []sourceOp `json:"operations"`
		SystemEvents []struct {
			Type string                     `json:"type"`
			Data map[string]json.RawMessage `json:"data"`
		} `json:"system_events"`
	}
	raw, e := os.ReadFile("../../testdata/plugin-local-storage-20261030/longname-source.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &oracle); e != nil {
		t.Fatal(e)
	}
	if len(oracle.Operations) != 6 {
		t.Fatal("source native and override matrix incomplete")
	}
	results := []map[string]any{}
	for _, op := range oracle.Operations {
		module := ""
		var value any
		if op.CallbackCount > 0 {
			module = op.Method
			if module == "exists" {
				module = "get_file_item"
			}
			value = map[string]any{"exists": map[string]any{"synthetic": true}, "list_files": []any{"override"}, "delete_file": false}[op.Method]
		}
		cfg := mustCleanSeedJSON(map[string]any{"module_method": module, "module_result": value})
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedLocalStorage", string(cfg), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
		p := sharedLocalStorageCurrent(t, a, "OwnedLocalStorage")
		cap := map[string]string{"exists": plugins.LocalStorageExistsCapability, "list_files": plugins.LocalStorageListCapability, "delete_file": plugins.LocalStorageDeleteCapability}[op.Method]
		out, err := sharedLocalStorageCall(t, p, cap, plugins.LocalStorageRequest{FileItem: sharedLocalStorageItemInput(t, map[string]any{"path": filepath.Join(root, strings.Repeat("x", 300))})})
		row := map[string]any{"case": op.Case, "expected": op.Returned, "returned": out}
		if err != nil {
			row["error"] = err.Error()
		}
		var snapshot struct {
			ModuleCalls []json.RawMessage `json:"module_calls"`
			Events      []json.RawMessage `json:"events"`
		}
		a.wg.Wait()
		if err := p.readSession(context.Background(), "snapshot", &snapshot); err != nil {
			t.Fatal(err)
		}
		row["callback_count"] = len(snapshot.ModuleCalls)
		results = append(results, row)
		var want bytes.Buffer
		if e := json.Compact(&want, op.Returned); e != nil {
			t.Fatal(e)
		}
		if err != nil || string(out) != want.String() || len(snapshot.ModuleCalls) != op.CallbackCount || len(snapshot.Events) != op.EventCount {
			t.Errorf("%s: expected result=%s calls=%d events=%d; actual=%s calls=%d events=%d error=%v", op.Case, op.Returned, op.CallbackCount, op.EventCount, out, len(snapshot.ModuleCalls), len(snapshot.Events), err)
		}
	}
	cfg := mustCleanSeedJSON(map[string]any{})
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedLocalStorage", string(cfg), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	out, err := sharedLocalStorageCall(t, sharedLocalStorageCurrent(t, a, "OwnedLocalStorage"), plugins.LocalStorageExistsCapability, plugins.LocalStorageRequest{FileItem: sharedLocalStorageItemInput(t, map[string]any{"path": regular})})
	if err != nil || string(out) != "true" {
		t.Errorf("ordinary next-call recovery: %s %v", out, err)
	}
	data, err := os.ReadFile(regular)
	if err != nil || string(data) != string(content) {
		t.Errorf("content changed: %v", err)
	}
	var delivered struct {
		Events []struct {
			Type string                     `json:"type"`
			Data map[string]json.RawMessage `json:"data"`
		} `json:"events"`
	}
	if e := sharedLocalStorageCurrent(t, a, "OwnedLocalStorage").readSession(context.Background(), "snapshot", &delivered); e != nil {
		t.Fatal(e)
	}
	if len(oracle.SystemEvents) != 4 || len(delivered.Events) != len(oracle.SystemEvents) {
		t.Fatalf("native errors were not delivered to the actual subscriber: source=%d target=%d", len(oracle.SystemEvents), len(delivered.Events))
	}
	for i, actual := range delivered.Events {
		expected := oracle.SystemEvents[i]
		if actual.Type != expected.Type {
			t.Fatal("system error type changed", actual.Type)
		}
		for _, key := range []string{"type", "module_id", "module_name", "module_method"} {
			assertSharedLocalJSON(t, actual.Data[key], expected.Data[key])
		}
		if string(actual.Data["traceback"]) != `""` || string(actual.Data["diagnostic_source"]) != `"go-sdk-error-code/v1"` || bytes.Contains(mustCleanSeedJSON(actual), []byte(root)) {
			t.Fatal("native diagnostics bypassed the declared safe error adaptation")
		}
	}
	// Source deduplicates identical notices per minute. Do not turn a real clock
	// minute boundary during a race run into a false behavior difference.
	noticeMinutes := map[string]bool{}
	for raw := a.systemMessages.get("system"); len(raw) > 0; raw = a.systemMessages.get("system") {
		var message systemMessage
		if e := json.Unmarshal(raw, &message); e != nil || message.Type != "system" || message.Title == nil || *message.Title != "文件整理发生了错误" || len(message.Date) < 16 || strings.Contains(message.Text, root) {
			t.Fatal("native system notice not delivered or unsanitized", e, string(raw))
		}
		minute := message.Date[:16]
		if noticeMinutes[minute] {
			t.Fatal("same native error was not deduplicated within a minute")
		}
		noticeMinutes[minute] = true
	}
	if len(noticeMinutes) == 0 || len(noticeMinutes) > len(oracle.SystemEvents) {
		t.Fatal("native error system notice missing", len(noticeMinutes))
	}
	receipt, _ := json.MarshalIndent(map[string]any{"operations": results, "next_call_exists": json.RawMessage(out), "content_unchanged": string(data) == string(content)}, "", "  ")
	t.Logf("source-bound ordinary long-component observations %s", receipt)
}
