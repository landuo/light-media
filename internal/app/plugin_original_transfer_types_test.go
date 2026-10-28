package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

type ownedTransferTypesOracle struct {
	Cases []struct {
		Name           string
		Plugin, Output json.RawMessage
		Fail           bool
		Status         int
		ErrorCount     int `json:"error_count"`
		Calls          []struct {
			Kind   string
			Kwargs json.RawMessage
		}
	}
}

func readTransferTypesOracle(t *testing.T, path string) ownedTransferTypesOracle {
	t.Helper()
	raw, err := os.ReadFile(path)
	var out ownedTransferTypesOracle
	if err != nil || json.Unmarshal(raw, &out) != nil || len(out.Cases) != 10 {
		t.Fatal("fixed transfer-type oracle", err)
	}
	return out
}

func TestOriginalTransferTypesActualSDKAndHTTP(t *testing.T) {
	a, cookie, source := ownedModuleFixture(t, true)
	clone := ownedModuleClone(t, a, cookie, "types")
	for _, c := range readTransferTypesOracle(t, "../../testdata/standard-module-host/transfer-types-source-oracle.json").Cases {
		t.Run(c.Name, func(t *testing.T) {
			method := "module_execute"
			if c.Fail {
				method = "module_fail"
			}
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"support_transtype": method}, "result": c.Plugin})
			saveOwnedModule(t, a, cookie, source, string(cfg))
			saveOwnedModule(t, a, cookie, clone, `{"enabled":true,"modules":{"support_transtype":"module_execute"},"result":null}`)
			before := [2]ownedModuleSnapshot{ownedModuleRead(t, source), ownedModuleRead(t, clone)}
			nativeCalls := 0
			result, err := a.originalTransferTypesWithNative(context.Background(), func() (any, error) { nativeCalls++; return originalLocalTransferTypes, nil })
			if (err == nil) != (c.Status == 200) {
				t.Fatal("source DTO status", err, c.Status)
			}
			if err == nil {
				raw, _ := json.Marshal(result)
				if !originalBaseJSONEqual(raw, c.Output) {
					t.Fatal("source result", string(raw), string(c.Output))
				}
			}
			after := [2]ownedModuleSnapshot{ownedModuleRead(t, source), ownedModuleRead(t, clone)}
			wantClone, wantNative := 0, 0
			for _, x := range c.Calls {
				if x.Kind == "clone" {
					wantClone++
				}
				if x.Kind == "native" {
					wantNative++
				}
			}
			if after[0].Calls[method]-before[0].Calls[method] != 1 || after[1].Calls["module_execute"]-before[1].Calls["module_execute"] != wantClone || nativeCalls != wantNative {
				t.Fatal("source/clone/native dispatch", before, after, nativeCalls, c.Calls)
			}
			var input struct{ Kwargs json.RawMessage }
			if json.Unmarshal(after[0].Inputs[method], &input) != nil || !originalBaseJSONEqual(input.Kwargs, c.Calls[0].Kwargs) {
				t.Fatal("source kwargs", string(input.Kwargs))
			}
			response := callTest(t, a.Handler(), "GET", "/api/directories/transfer-types", "", cookie, "")
			if response.Code != c.Status {
				t.Fatal("real HTTP status", response.Code, response.Body.String())
			}
			if c.Status == 200 {
				var out struct{ Data json.RawMessage }
				if json.Unmarshal(response.Body.Bytes(), &out) != nil || !originalBaseJSONEqual(out.Data, c.Output) {
					t.Fatal("real HTTP result", response.Body.String())
				}
			}
		})
	}
}
