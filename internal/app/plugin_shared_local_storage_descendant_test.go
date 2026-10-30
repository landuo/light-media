package app

import (
	"bytes"
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"os"
	"path/filepath"
	"testing"
)

// Independent static regular-file descendant. No identity/permission/symlink manipulations.
func TestSharedLocalStorageOrdinaryMissingDescendant(t *testing.T) {
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
	}
	var oracle struct {
		Operations []sourceOp `json:"operations"`
	}
	raw, e := os.ReadFile("../../testdata/plugin-local-storage-20261030/enotdir-source.json")
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
		out, err := sharedLocalStorageCall(t, p, cap, plugins.LocalStorageRequest{FileItem: sharedLocalStorageItemInput(t, map[string]any{"path": filepath.Join(regular, "missing-child")})})
		row := map[string]any{"case": op.Case, "expected": op.Returned, "returned": out}
		if err != nil {
			row["error"] = err.Error()
		}
		var snapshot struct {
			ModuleCalls []json.RawMessage `json:"module_calls"`
		}
		if err := p.readSession(context.Background(), "snapshot", &snapshot); err != nil {
			t.Fatal(err)
		}
		row["callback_count"] = len(snapshot.ModuleCalls)
		results = append(results, row)
		var want bytes.Buffer
		if e := json.Compact(&want, op.Returned); e != nil {
			t.Fatal(e)
		}
		if err != nil || string(out) != want.String() || len(snapshot.ModuleCalls) != op.CallbackCount {
			t.Errorf("%s: expected result=%s calls=%d; actual=%s calls=%d error=%v", op.Case, op.Returned, op.CallbackCount, out, len(snapshot.ModuleCalls), err)
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
	receipt, _ := json.MarshalIndent(map[string]any{"operations": results, "next_call_exists": json.RawMessage(out), "content_unchanged": string(data) == string(content)}, "", "  ")
	t.Logf("source-bound ordinary missing-descendant observations %s", receipt)
}
