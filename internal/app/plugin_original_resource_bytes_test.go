package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"light-media/internal/priorityrules"
	"light-media/internal/sites"
)

type resourceBytesSourceOracle struct {
	Commit string
	Cases  []struct {
		Entry, Name  string
		Plugin       json.RawMessage `json:"plugin_result"`
		Returned     json.RawMessage
		Error        *string
		Observations []resourceStreamObservation
	}
}

func readResourceBytesOracle(t *testing.T, path string) resourceBytesSourceOracle {
	t.Helper()
	if path == "" {
		path = "../../testdata/standard-resource-modules/bytes-source-oracle.json"
	}
	raw, err := os.ReadFile(path)
	var out resourceBytesSourceOracle
	if err != nil || json.Unmarshal(raw, &out) != nil || out.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(out.Cases) != 6 {
		t.Fatal("actual fixed bytes/caller replay unavailable", err)
	}
	return out
}

func TestOriginalResourceBytesActualSDKSource(t *testing.T) {
	t.Setenv("SEARCH_RESOURCE_PAGES", "1")
	for _, sample := range readResourceBytesOracle(t, "").Cases {
		t.Run(sample.Entry+"/"+sample.Name, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			site := installResourceBrowseSite(t, a, readResourceBrowseOracle(t).Site["url"].(string))
			method := map[string]string{"browse": "async_refresh_torrents", "batch": "async_batch_search_torrents", "title": "filter_torrents"}[sample.Entry]
			handler := "module_execute"
			if sample.Entry == "title" {
				handler = "module_filter"
			}
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{method: handler}, "result": sample.Plugin})
			saveOwnedModule(t, a, cookie, object, string(cfg))
			object = originalBaseSDKLoaded(t, a, ownedModuleID)
			before := ownedModuleRead(t, object)
			nativeCalls := 0
			native := func(context.Context, SiteConfig, int) (sites.SearchResult, error) {
				nativeCalls++
				return sites.SearchResult{Page: 2, Resources: []sites.Resource{{ID: "22", SiteID: site.ID, Title: "Owned core resource"}, {ID: "22", SiteID: site.ID, Title: "Owned core resource"}}}, nil
			}
			var count int
			var err error
			switch sample.Entry {
			case "browse":
				var out sites.SearchResult
				out, err = a.originalBrowseSiteResources(context.Background(), site, "", 2, func() (sites.SearchResult, error) { return native(context.Background(), site, 2) })
				count = len(out.Resources)
			case "batch":
				op := &originalResourceBatchSearch{app: a, keyword: "Owned keyword", page: 2, native: native}
				op.attach(context.Background(), []SiteConfig{site})
				out, failure := op.site(site)
				err, count = failure, len(out.Resources)
			case "title":
				settings := a.Settings()
				settings.PriorityRules = &priorityrules.Configuration{SearchGroups: []string{"owned-rule"}}
				filter, failure := a.preparePriorityFilter(context.Background(), SearchRequest{}, settings)
				if failure != nil {
					t.Fatal(failure)
				}
				out, failure := filter.apply(context.Background(), a, SearchOutput{Candidates: []Candidate{{Resource: sites.Resource{ID: "22", SiteID: site.ID, Title: "Owned core resource"}}}})
				err, count = failure, len(out.Candidates)
			}
			wantCore := 0
			for _, call := range sample.Observations {
				if call.Kind == "core" && (call.Method == "async_refresh_torrents" || call.Method == "async_batch_search_torrents" || call.Method == "async_search_torrents") {
					wantCore++
				}
			}
			// Truthy title bytes survive the source title caller; this native
			// typed public projection rejects bytes instead of forging models.
			wantError := sample.Error != nil || sample.Entry == "title" && sample.Name == "full_bytes"
			wantRows := 0
			if sample.Entry == "batch" {
				var rows []json.RawMessage
				_ = json.Unmarshal(sample.Returned, &rows)
				wantRows = len(rows)
			}
			if (err != nil) != wantError || nativeCalls != wantCore || count != wantRows {
				t.Fatal("actual bytes terminal/falsey/legacy fallback differs from source", err, nativeCalls, wantCore, count, wantRows)
			}
			after := ownedModuleRead(t, object)
			if after.Calls[handler]-before.Calls[handler] != 1 {
				t.Fatal("real SDK bytes callback did not execute exactly once")
			}
		})
	}
}
