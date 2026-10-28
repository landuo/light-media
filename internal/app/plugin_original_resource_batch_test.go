package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/sites"
)

type resourceBatchOracle struct {
	Commit string
	Cases  []struct {
		Name         string
		Plugin       json.RawMessage `json:"plugin_result"`
		Returned     []map[string]any
		Observations []resourceStreamObservation
		Error        *string
	}
	Production struct {
		Observations []resourceStreamObservation
		Returned     []map[string]any
	} `json:"production_empty_list"`
	Legacy struct {
		Plugin       json.RawMessage `json:"plugin_legacy"`
		Observations []resourceStreamObservation
		Returned     []map[string]any
	} `json:"legacy_override"`
}

func TestOriginalResourceBatchFallbackActualSDKSource(t *testing.T) {
	t.Setenv("SEARCH_RESOURCE_PAGES", "1")
	a, cookie, object := ownedModuleFixture(t, true)
	reference, browse := readResourceBatchOracle(t), readResourceBrowseOracle(t)
	site := installResourceBrowseSite(t, a, browse.Site["url"].(string))
	// The fixture's second independent callable returns the configured legacy
	// model list, while its first callable returns the source false batch value.
	cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_batch_search_torrents": "module_execute", "async_search_torrents": "module_page_size"}, "result": false, "page_size": reference.Legacy.Plugin})
	saveOwnedModule(t, a, cookie, object, string(cfg))
	object = originalBaseSDKLoaded(t, a, ownedModuleID)
	before := ownedModuleRead(t, object)
	native := 0
	op := &originalResourceBatchSearch{app: a, keyword: "Owned keyword", page: 2, native: func(context.Context, SiteConfig, int) (sites.SearchResult, error) {
		native++
		return sites.SearchResult{Resources: []sites.Resource{{ID: "22", SiteID: site.ID, Title: "Owned core resource"}, {ID: "22", SiteID: site.ID, Title: "Owned core resource"}}}, nil
	}}
	op.attach(context.Background(), []SiteConfig{site})
	out, err := op.site(site)
	if err != nil || native != 1 || len(out.Resources) != len(reference.Legacy.Returned) || out.Resources[0].Title != "Owned plugin resource" {
		t.Fatal("actual fallback plugin/core accumulation differs", err, native, len(out.Resources))
	}
	after := ownedModuleRead(t, object)
	if after.Calls["module_execute"]-before.Calls["module_execute"] != 1 || after.Calls["module_page_size"]-before.Calls["module_page_size"] != 1 {
		t.Fatal("actual source async_search_torrents not invoked after false batch")
	}
	var input struct{ Args, Kwargs json.RawMessage }
	_ = json.Unmarshal(after.Inputs["module_page_size"], &input)
	for _, row := range reference.Legacy.Observations {
		if row.Kind == "plugin" && row.Method == "async_search_torrents" && (string(input.Args) != "[]" || !originalBaseJSONEqual(input.Kwargs, row.Kwargs)) {
			t.Fatal("actual legacy source four kwargs differ")
		}
	}
}

func readResourceBatchOracle(t *testing.T) resourceBatchOracle {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/standard-resource-modules/batch-source-oracle.json")
	var out resourceBatchOracle
	if err != nil || json.Unmarshal(raw, &out) != nil || out.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(out.Cases) != 8 {
		t.Fatal("actual fixed batch/fallback source replay unavailable", err)
	}
	return out
}

func TestOriginalResourceBatchActualSDKSourceOracle(t *testing.T) {
	reference := readResourceBatchOracle(t)
	browse := readResourceBrowseOracle(t)
	t.Setenv("SEARCH_RESOURCE_PAGES", "1")
	for _, sample := range reference.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			site := installResourceBrowseSite(t, a, browse.Site["url"].(string))
			handler := "module_execute"
			if sample.Name == "failed" {
				handler = "module_fail"
			}
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_batch_search_torrents": handler, "async_worker_search_site": "module_fail", "async_refresh_torrents": "module_fail"}, "result": sample.Plugin})
			saveOwnedModule(t, a, cookie, object, string(cfg))
			object = originalBaseSDKLoaded(t, a, ownedModuleID)
			before := ownedModuleRead(t, object)
			nativeCalls := 0
			op := &originalResourceBatchSearch{app: a, keyword: "Owned keyword", page: 2, native: func(ctx context.Context, actual SiteConfig, page int) (sites.SearchResult, error) {
				nativeCalls++
				if actual.ID != site.ID || page != 2 {
					t.Fatal("native batch/fallback site/page differs")
				}
				return sites.SearchResult{Page: page, Resources: []sites.Resource{{ID: "22", SiteID: site.ID, Title: "Owned core resource"}}}, nil
			}}
			op.attach(context.Background(), []SiteConfig{site})
			out, err := op.site(site)
			if (err != nil) != (sample.Error != nil) {
				t.Fatal("source batch/fallback result boundary differs", err)
			}
			wantCalls, wantGetters := 0, 1
			for _, row := range sample.Observations {
				if row.Kind == "core" && (row.Method == "async_batch_search_torrents" || row.Method == "async_search_torrents") {
					wantCalls++
				}
				if row.Method == "get_search_page_size" || row.Method == "async_search_torrents" {
					wantGetters++
				}
			}
			if nativeCalls != wantCalls {
				t.Fatal("source batch/list/scalar fallback real core count differs", nativeCalls, wantCalls)
			}
			got, want := []string{}, []string{}
			for _, row := range out.Resources {
				got = append(got, row.Title)
			}
			for _, row := range sample.Returned {
				want = append(want, row["title"].(string))
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("source batch reset/dict bypass/fallback output differs", got, want)
			}
			after := ownedModuleRead(t, object)
			if after.Calls["get_state"]-before.Calls["get_state"] != wantGetters || after.Calls["get_module"]-before.Calls["get_module"] != wantGetters || after.Calls[handler]-before.Calls[handler] != 1 {
				t.Fatal("source batch/fallback/page-capacity declaration frames differ")
			}
			var input struct{ Args, Kwargs json.RawMessage }
			if json.Unmarshal(after.Inputs[handler], &input) != nil || string(input.Args) != "[]" || !originalBaseJSONEqual(input.Kwargs, sample.Observations[0].Kwargs) {
				t.Fatal("source batch exact ordered sites/keyword/mtype/page kwargs differ")
			}
		})
	}
}

func TestOriginalResourceBatchActualHTTPHostSDK(t *testing.T) {
	t.Setenv("SEARCH_RESOURCE_PAGES", "1")
	a, cookie, object := ownedModuleFixture(t, true)
	browse, batch := readResourceBrowseOracle(t), readResourceBatchOracle(t)
	html, err := os.ReadFile("../sites/testdata/ubits-search.sanitized.html")
	if err != nil {
		t.Fatal(err)
	}
	var nativeCalls atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nativeCalls.Add(1)
		if r.URL.Path != "/torrents.php" || r.URL.Query().Get("page") != "1" || r.URL.Query().Get("search") != "Owned keyword" || r.Header.Get("Cookie") != "owned-site-cookie" {
			t.Error("actual native batch/fallback HTTP query differs")
		}
		_, _ = w.Write(html)
	}))
	defer native.Close()
	site := installResourceBrowseSite(t, a, native.URL)
	for _, sample := range batch.Cases {
		if sample.Name != "plugin_dict" && sample.Name != "empty_list" {
			continue
		}
		plugin := json.RawMessage(strings.ReplaceAll(string(sample.Plugin), browse.Site["url"].(string), native.URL))
		cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_batch_search_torrents": "module_execute", "async_worker_search_site": "module_fail"}, "result": plugin})
		saveOwnedModule(t, a, cookie, object, string(cfg))
		object = originalBaseSDKLoaded(t, a, ownedModuleID)
		before := nativeCalls.Load()
		response := callTest(t, a.Handler(), "POST", "/api/search/resources", `{"query":"Owned keyword","page":2}`, cookie, "")
		var out struct{ Data SearchOutput }
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &out) != nil || len(out.Data.Sites) != 1 || out.Data.Sites[0].ID != site.ID || out.Data.Sites[0].Status != "ok" {
			t.Fatal("actual non-stream batch host rejected result", response.Code)
		}
		if sample.Name == "plugin_dict" {
			if len(out.Data.Candidates) != len(sample.Returned) || out.Data.Candidates[0].Resource.Title != "Owned plugin resource" || nativeCalls.Load() != before {
				t.Fatal("actual source batch dict did not bypass core")
			}
		} else {
			wantCalls := 0
			for _, row := range batch.Production.Observations {
				if row.Kind == "core" && (row.Method == "async_batch_search_torrents" || row.Method == "async_search_torrents") {
					wantCalls++
				}
			}
			if len(out.Data.Candidates) != len(batch.Production.Returned) || int(nativeCalls.Load()-before) != wantCalls {
				t.Fatal("actual empty-list native batch then legacy repeat/reset differs", len(out.Data.Candidates), nativeCalls.Load()-before)
			}
		}
		if strings.Contains(response.Body.String(), "owned-plugin-secret") || strings.Contains(response.Body.String(), "owned-private-material") || strings.Contains(response.Body.String(), "enclosure") || ownedModuleRead(t, object).Calls["module_fail"] != 0 {
			t.Fatal("wrong stream method or private source output in native non-stream host")
		}
	}
}
