package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

type ownedSeasonsOracle struct {
	Cases []struct {
		Name               string
		Plugin, Native     json.RawMessage
		Fail               bool
		Status, ErrorCount int
		Output             json.RawMessage
		Calls              []struct {
			Kind   string
			Kwargs json.RawMessage
		}
	}
}

func readOwnedSeasonsOracle(t *testing.T, path string) ownedSeasonsOracle {
	t.Helper()
	raw, e := os.ReadFile(path)
	var out ownedSeasonsOracle
	if e != nil || json.Unmarshal(raw, &out) != nil || len(out.Cases) != 12 {
		t.Fatal("fixed seasons oracle", e)
	}
	return out
}
func TestOriginalCatalogSeasonsActualSDKSource(t *testing.T) {
	oracle := readOwnedSeasonsOracle(t, "../../testdata/standard-catalog-modules/seasons-source-oracle.json")
	a, cookie, source := ownedModuleFixture(t, true)
	clone := ownedModuleClone(t, a, cookie, "seasons")
	for _, c := range oracle.Cases {
		t.Run(c.Name, func(t *testing.T) {
			method := "module_execute"
			if c.Fail {
				method = "module_fail"
			}
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_tmdb_seasons": method}, "result": c.Plugin})
			saveOwnedModule(t, a, cookie, source, string(cfg))
			saveOwnedModule(t, a, cookie, clone, `{"enabled":true,"modules":{"async_tmdb_seasons":"module_execute"},"result":null}`)
			before := [2]ownedModuleSnapshot{ownedModuleRead(t, source), ownedModuleRead(t, clone)}
			nativeCalls := 0
			result, e := a.originalCatalogSeasonsWithNative(context.Background(), 120, func() (json.RawMessage, error) { nativeCalls++; return c.Native, nil })
			if (e == nil) != (c.Status == 200) {
				t.Fatal("fixed status", e, c.Status)
			}
			if e == nil {
				raw, _ := json.Marshal(result)
				if !originalBaseJSONEqual(raw, c.Output) {
					t.Fatal("source season DTO/order/falsey", string(raw), string(c.Output))
				}
			}
			after := [2]ownedModuleSnapshot{ownedModuleRead(t, source), ownedModuleRead(t, clone)}
			expectedClone, expectedNative := 0, 0
			for _, x := range c.Calls {
				if x.Kind == "clone" {
					expectedClone++
				}
				if x.Kind == "native" {
					expectedNative++
				}
			}
			if after[0].Calls[method]-before[0].Calls[method] != 1 || after[1].Calls["module_execute"]-before[1].Calls["module_execute"] != expectedClone || nativeCalls != expectedNative {
				t.Fatal("source call order", c.Calls, before, after, nativeCalls)
			}
			var input struct{ Kwargs json.RawMessage }
			if json.Unmarshal(after[0].Inputs[method], &input) != nil || !originalBaseJSONEqual(input.Kwargs, c.Calls[0].Kwargs) {
				t.Fatal("source kwargs", string(input.Kwargs))
			}
		})
	}
}

func TestOriginalCatalogSeasonsHTTPFullNativeFields(t *testing.T) {
	oracle := readOwnedSeasonsOracle(t, "../../testdata/standard-catalog-modules/seasons-source-oracle.json")
	a, cookie, source := ownedModuleFixture(t, true)
	saveOwnedModule(t, a, cookie, source, `{"enabled":true,"modules":{"async_tmdb_seasons":"module_execute"},"result":null}`)
	for _, c := range oracle.Cases {
		if c.Name != "native_special_and_missing" {
			continue
		}
		raw, _ := json.Marshal(map[string]any{"seasons": c.Native})
		native := sourceMediaInformation{ProviderInfo: raw}
		if e := a.Store.Put(context.Background(), "cache", "source-detail:tv:120", map[string]any{"at": time.Now(), "data": native}); e != nil {
			t.Fatal(e)
		}
		response := callTest(t, a.Handler(), "GET", "/api/catalog/seasons?id=120", "", cookie, "")
		var out struct{ Data json.RawMessage }
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &out) != nil || !originalBaseJSONEqual(out.Data, c.Output) {
			t.Fatal("real HTTP source missing/null versus explicit empty string", response.Body.String(), string(c.Output))
		}
	}
}
