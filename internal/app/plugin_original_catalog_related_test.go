package app

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"testing"
	"time"

	"light-media/internal/catalog"
)

type ownedRelatedOracle struct {
	Cases []struct {
		Source, Kind, Part, Name, Method string
		Plugin                           json.RawMessage
		Fail                             bool
		Status                           int
		Output                           []map[string]any
		Calls                            []struct {
			Kind, Method string
			Kwargs       json.RawMessage
		}
	}
}

func readOwnedRelatedOracle(t *testing.T, path string) ownedRelatedOracle {
	t.Helper()
	raw, e := os.ReadFile(path)
	var out ownedRelatedOracle
	if e != nil || json.Unmarshal(raw, &out) != nil || len(out.Cases) != 16 {
		t.Fatal("fixed related oracle", e)
	}
	return out
}
func seedOwnedRelatedNative(t *testing.T, a *App) {
	t.Helper()
	for _, source := range []string{"themoviedb", "douban", "bangumi"} {
		for _, kind := range []string{"movie", "tv"} {
			m := catalog.Media{ID: 120, MediaType: kind, Title: "Owned Detail Browser", Year: 2026, Cast: []catalog.CastMember{{ID: 120, Name: "native actor"}}, Recommendations: []catalog.Media{{ID: 120, MediaType: "movie", Title: "native recommended", Year: 2026}}}
			key := "media:" + kind + ":120"
			var data any = m
			if source != "themoviedb" {
				key = "explore-detail:v1:" + source + ":" + kind + ":120"
				data = catalog.ExploreMedia{Media: m, Source: source, SourceID: "120"}
			}
			if e := a.Store.Put(context.Background(), "cache", key, map[string]any{"at": time.Now(), "data": data}); e != nil {
				t.Fatal(e)
			}
		}
	}
}
func TestOriginalCatalogRelatedActualSDKSource(t *testing.T) {
	oracle := readOwnedRelatedOracle(t, "../../testdata/standard-catalog-modules/related-source-oracle.json")
	a, cookie, source := ownedModuleFixture(t, true)
	clone := ownedModuleClone(t, a, cookie, "related")
	seedOwnedRelatedNative(t, a)
	for _, c := range oracle.Cases {
		t.Run(c.Source+"/"+c.Kind+"/"+c.Part+"/"+c.Name, func(t *testing.T) {
			method := "module_execute"
			if c.Fail {
				method = "module_fail"
			}
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{c.Method: method}, "result": c.Plugin})
			saveOwnedModule(t, a, cookie, source, string(cfg))
			cfg, _ = json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{c.Method: "module_execute"}, "result": nil})
			saveOwnedModule(t, a, cookie, clone, string(cfg))
			before := [2]ownedModuleSnapshot{ownedModuleRead(t, source), ownedModuleRead(t, clone)}
			query := url.Values{"source": {c.Source}, "kind": {c.Kind}, "source_id": {"120"}}
			response := callTest(t, a.Handler(), "GET", "/api/catalog/detail/"+c.Part+"?"+query.Encode(), "", cookie, "")
			if response.Code != c.Status {
				t.Fatal("fixed endpoint status", response.Code, response.Body.String(), c.Status)
			}
			if c.Status == 200 {
				var out struct{ Data []map[string]any }
				if json.Unmarshal(response.Body.Bytes(), &out) != nil || len(out.Data) != len(c.Output) {
					t.Fatal("fixed ordered list length", response.Body.String(), len(c.Output))
				}
				key := "name"
				if c.Part == "recommendations" {
					key = "title"
				}
				for i, want := range c.Output {
					if out.Data[i][key] != want[key] {
						t.Fatal("fixed ordered labels", i, out.Data[i][key], want[key])
					}
				}
			}
			after := [2]ownedModuleSnapshot{ownedModuleRead(t, source), ownedModuleRead(t, clone)}
			cloneCalls := 0
			for _, call := range c.Calls {
				if call.Kind == "clone" {
					cloneCalls++
				}
			}
			if after[0].Calls[method]-before[0].Calls[method] != 1 || after[1].Calls["module_execute"]-before[1].Calls["module_execute"] != cloneCalls {
				t.Fatal("fixed dispatch order", before, after)
			}
			var input struct{ Kwargs json.RawMessage }
			if json.Unmarshal(after[0].Inputs[method], &input) != nil || !originalBaseJSONEqual(input.Kwargs, c.Calls[0].Kwargs) {
				t.Fatal("fixed source kwargs", string(input.Kwargs), string(c.Calls[0].Kwargs))
			}
		})
	}
}
