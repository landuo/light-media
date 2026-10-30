package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

// The source executes registered modules even when no native storage provider
// matches null. Exercise the real worker, registry and dispatcher, not a stub.
func TestSharedLocalStorageNullStorageModuleSourceContract(t *testing.T) {
	testSharedLocalStorageModuleSourceContract(t, "null-module-source.json", 15)
}

func TestSharedLocalStoragePathlessDeleteModuleSourceContract(t *testing.T) {
	testSharedLocalStorageModuleSourceContract(t, "pathless-module-source.json", 10)
}

func testSharedLocalStorageModuleSourceContract(t *testing.T, oracle string, cases int) {
	a, cookie, _ := sharedLocalStorageFixture(t, sharedLocalStorageAll)
	root := sharedLocalStorageRoot(t, a)
	path := filepath.Join(root, "module.mkv")
	content := []byte("owned null-storage module file retained")
	if e := sharedLocalStorageFixtureWrite(t, path, content, 0600); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile("../../testdata/plugin-local-storage-20261030/" + oracle)
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Operations []struct {
			Case          string          `json:"case"`
			Method        string          `json:"method"`
			Input         json.RawMessage `json:"input"`
			Returned      json.RawMessage `json:"returned"`
			Override      json.RawMessage `json:"override_value"`
			CallbackCount int             `json:"callback_count"`
			FileRetained  bool            `json:"file_retained"`
		} `json:"operations"`
	}
	if e := json.Unmarshal([]byte(strings.ReplaceAll(string(raw), "$ROOT", root)), &source); e != nil {
		t.Fatal(e)
	}
	if len(source.Operations) != cases {
		t.Fatal("source module matrix incomplete")
	}
	for _, op := range source.Operations {
		t.Run(op.Case, func(t *testing.T) {
			module := op.Method
			if module == "exists" {
				module = "get_file_item"
			}
			cfg := mustCleanSeedJSON(map[string]any{"module_method": module, "module_result": op.Override})
			r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedLocalStorage", string(cfg), cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			a.wg.Wait()
			worker := sharedLocalStorageCurrent(t, a, "OwnedLocalStorage")
			capability := map[string]string{"exists": plugins.LocalStorageExistsCapability, "list_files": plugins.LocalStorageListCapability, "delete_file": plugins.LocalStorageDeleteCapability}[op.Method]
			out, e := sharedLocalStorageCall(t, worker, capability, plugins.LocalStorageRequest{FileItem: sharedLocalStorageItemInput(t, op.Input)})
			if e != nil {
				t.Fatal(e)
			}
			assertSharedLocalJSON(t, out, op.Returned)
			var snapshot struct {
				ModuleCalls []struct {
					Args   []any                      `json:"args"`
					Kwargs map[string]json.RawMessage `json:"kwargs"`
				} `json:"module_calls"`
			}
			if e := worker.readSession(context.Background(), "snapshot", &snapshot); e != nil {
				t.Fatal(e)
			}
			if len(snapshot.ModuleCalls) != op.CallbackCount || op.CallbackCount != 1 {
				t.Fatalf("source callback count %d, actual %d", op.CallbackCount, len(snapshot.ModuleCalls))
			}
			call := snapshot.ModuleCalls[0]
			if len(call.Args) != 0 {
				t.Fatal("storage callback gained positional arguments")
			}
			if op.Method == "exists" {
				assertSharedLocalJSON(t, mustCleanSeedJSON(call.Kwargs), mustCleanSeedJSON(map[string]any{"storage": nil, "path": path}))
			} else {
				want := map[string]any{"fileitem": sharedLocalStorageItemInput(t, op.Input)}
				if op.Method == "list_files" {
					want["recursion"] = false
				}
				assertSharedLocalJSON(t, mustCleanSeedJSON(call.Kwargs), mustCleanSeedJSON(want))
			}
			if actual, e := os.ReadFile(path); e != nil || string(actual) != string(content) || !op.FileRetained {
				t.Fatal("null provider must not mutate the ordinary file", e)
			}
		})
	}
}
