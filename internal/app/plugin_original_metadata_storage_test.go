package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestOriginalMetadataStorageActualSDKFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/metadata-module-oracle.json")
	var oracle struct {
		Storage []struct {
			Name, Method string
			Kwargs       map[string]any
			PluginResult json.RawMessage `json:"plugin_result"`
			CoreResult   any             `json:"core_result"`
			Returned     json.RawMessage
			Calls        []struct{ Role string }
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Storage) != 6 {
		t.Fatal("fixed StorageChain wrappers/source oracle", err)
	}
	for _, sample := range oracle.Storage {
		t.Run(sample.Name, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			config, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{sample.Method: "module_execute"}, "result": sample.PluginResult})
			saveOwnedModule(t, a, cookie, object, string(config))
			object = originalBaseSDKLoaded(t, a, ownedModuleID)
			before := ownedModuleRead(t, object)
			calls := 0
			actual, err := a.originalMetadataStorage(context.Background(), sample.Method, sample.Kwargs, func() (any, error) { calls++; return sample.CoreResult, nil })
			wantCalls := 0
			for _, call := range sample.Calls {
				if call.Role == "core" {
					wantCalls++
				}
			}
			if err != nil || !originalBaseJSONEqual(actual, sample.Returned) || calls != wantCalls {
				t.Fatal("source scalar/list/native effects", string(actual), string(sample.Returned), calls, wantCalls, err)
			}
			after := ownedModuleRead(t, object)
			var input struct {
				Args   []json.RawMessage
				Kwargs json.RawMessage
			}
			expected, _ := json.Marshal(sample.Kwargs)
			if json.Unmarshal(after.Inputs["module_execute"], &input) != nil || input.Args == nil || len(input.Args) != 0 || !originalBaseJSONEqual(input.Kwargs, expected) || after.Calls["module_execute"]-before.Calls["module_execute"] != 1 {
				t.Fatal("actual storage SDK kwargs mismatch", string(input.Kwargs), string(expected))
			}
		})
	}
}
