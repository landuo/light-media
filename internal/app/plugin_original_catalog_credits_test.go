package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"light-media/internal/catalog"
)

func TestOriginalCatalogPersonCreditsActualSourceSDK(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-catalog-modules/credits-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name   string
			Plugin json.RawMessage
			Status int
			Output []struct {
				ID    int64 `json:"tmdb_id"`
				Title string
			}
			Calls []struct {
				Kind   string
				Kwargs json.RawMessage
			}
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 7 {
		t.Fatal("fixed credits caller/wrapper/outer dispatch cases")
	}
	for _, row := range oracle.Cases {
		t.Run(row.Name, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			config, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_tmdb_person_credits": "module_execute"}, "result": row.Plugin})
			saveOwnedModule(t, a, cookie, object, string(config))
			calls := 0
			got, e := a.originalCatalogPersonCredits(context.Background(), 120, 2, func() (catalog.Page, error) {
				calls++
				return catalog.Page{Results: []catalog.Media{{ID: 120, MediaType: "movie", Title: "core", Year: 2026}}, Page: 2, TotalPages: 2, TotalResults: 1}, nil
			})
			if (e == nil) != (row.Status == 200) {
				t.Fatal("source credits result error differs", e, row.Status)
			}
			wantCalls := 0
			for _, call := range row.Calls {
				if call.Kind == "core" {
					wantCalls++
				}
			}
			if calls != wantCalls {
				t.Fatal("source scalar/list/native ordering", calls, wantCalls)
			}
			if e == nil {
				if len(got.Results) != len(row.Output) {
					t.Fatal("source result count", len(got.Results), len(row.Output))
				}
				for i, want := range row.Output {
					if got.Results[i].ID != want.ID || got.Results[i].Title != want.Title {
						t.Fatal("source plugin/core list order", i)
					}
				}
			}
			snapshot := ownedModuleRead(t, originalBaseSDKLoaded(t, a, ownedModuleID))
			var input struct {
				Args   []json.RawMessage
				Kwargs json.RawMessage
			}
			if json.Unmarshal(snapshot.Inputs["module_execute"], &input) != nil || len(input.Args) != 0 || !originalBaseJSONEqual(input.Kwargs, json.RawMessage(`{"person_id":120,"page":2}`)) {
				t.Fatal("fixed async credits kwargs", string(input.Kwargs))
			}
		})
	}
}
