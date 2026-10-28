package app

import (
	"context"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/sites"
)

type resourceStreamObservation struct {
	Kind, Method string
	Kwargs       json.RawMessage
}
type resourceStreamOracle struct {
	Commit string
	Cases  []struct {
		Name         string
		Plugin       json.RawMessage `json:"plugin_result"`
		Observations []resourceStreamObservation
		Frames       []struct{ Items []map[string]any }
		Error        *string
	}
	Pagination []struct {
		Name         string
		Pages        int             `json:"configured_pages"`
		PageSize     json.RawMessage `json:"page_size"`
		Observations []resourceStreamObservation
		Error        *string
	}
	PageConfig []struct {
		Value  *string
		Result int
	} `json:"page_config"`
	PageErrors []struct{ Value, Error string } `json:"page_builder_errors"`
	Production struct {
		Pages        int `json:"configured_pages"`
		PageSize     int `json:"page_size"`
		Observations []resourceStreamObservation
		Frames       []struct {
			Type  string
			Items []map[string]any
		}
	} `json:"production_pagination"`
}

func readResourceStreamOracle(t *testing.T) resourceStreamOracle {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/standard-resource-modules/stream-source-oracle.json")
	var out resourceStreamOracle
	if err != nil || json.Unmarshal(raw, &out) != nil || out.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(out.Cases) != 8 || len(out.Pagination) != 7 {
		t.Fatal("actual fixed resource stream replay unavailable", err)
	}
	return out
}

func TestOriginalResourceStreamActualSDKSourceOracle(t *testing.T) {
	reference := readResourceStreamOracle(t)
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
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_worker_search_site": handler, "async_refresh_torrents": "module_fail", "async_search_torrents": "module_fail"}, "result": sample.Plugin})
			saveOwnedModule(t, a, cookie, object, string(cfg))
			object = originalBaseSDKLoaded(t, a, ownedModuleID)
			before := ownedModuleRead(t, object)
			nativeCalls := 0
			out, err := a.originalStreamResourceSite(context.Background(), site, "Owned keyword", 2, func(page int) (sites.SearchResult, error) {
				nativeCalls++
				if page != 2 {
					t.Fatal("native stream page mapping")
				}
				return sites.SearchResult{Resources: []sites.Resource{{ID: "22", SiteID: site.ID, Title: "Owned core resource"}}, Page: page}, nil
			})
			if (err != nil) != (sample.Error != nil) {
				t.Fatal("source stream error boundary differs", err)
			}
			wantCore, wantGetters := 0, 1
			for _, row := range sample.Observations {
				if row.Kind == "core" && row.Method == "async_worker_search_site" {
					wantCore++
				}
				if row.Method == "get_search_page_size" {
					wantGetters++
				}
			}
			if nativeCalls != wantCore {
				t.Fatal("source falsey/list/null worker core count", nativeCalls, wantCore)
			}
			if err == nil {
				want := []string{}
				for _, frame := range sample.Frames {
					for _, row := range frame.Items {
						want = append(want, row["title"].(string))
					}
				}
				got := []string{}
				for _, row := range out.Resources {
					got = append(got, row.Title)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatal("actual source stream append order differs", got, want)
				}
			}
			after := ownedModuleRead(t, object)
			if after.Calls["get_state"]-before.Calls["get_state"] != wantGetters || after.Calls["get_module"]-before.Calls["get_module"] != wantGetters || after.Calls[handler]-before.Calls[handler] != 1 {
				t.Fatal("source wrapper/page-capacity discovery sequence differs")
			}
			var input struct{ Args, Kwargs json.RawMessage }
			if json.Unmarshal(after.Inputs[handler], &input) != nil || string(input.Args) != "[]" || !originalBaseJSONEqual(input.Kwargs, sample.Observations[0].Kwargs) {
				t.Fatal("pinned worker exact five kwargs differ")
			}
		})
	}
}

func TestOriginalResourceStreamPageCapacityActualSDKSource(t *testing.T) {
	reference := readResourceStreamOracle(t)
	browse := readResourceBrowseOracle(t)
	var plugin json.RawMessage
	for _, sample := range browse.Cases {
		if sample.Name == "plugin_list" {
			plugin = sample.Plugin
		}
	}
	for _, sample := range reference.Pagination {
		t.Run(sample.Name, func(t *testing.T) {
			t.Setenv("SEARCH_RESOURCE_PAGES", "3")
			a, cookie, object := ownedModuleFixture(t, true)
			site := installResourceBrowseSite(t, a, browse.Site["url"].(string))
			handler := "module_page_size"
			if sample.Name == "capacity_failure" {
				handler = "module_fail"
			}
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_worker_search_site": "module_execute", "get_search_page_size": handler}, "result": plugin, "page_size": sample.PageSize})
			saveOwnedModule(t, a, cookie, object, string(cfg))
			object = originalBaseSDKLoaded(t, a, ownedModuleID)
			before := ownedModuleRead(t, object)
			pages := []int{}
			out, err := a.originalStreamResourceSite(context.Background(), site, "Owned keyword", 2, func(page int) (sites.SearchResult, error) {
				pages = append(pages, page)
				return sites.SearchResult{Page: page, Resources: []sites.Resource{{ID: "22", SiteID: site.ID, Title: "Owned core resource"}}}, nil
			})
			if (err != nil) != (sample.Error != nil) {
				t.Fatal("source capacity numeric/comparison/error boundary differs", err)
			}
			wantPages := []int{}
			var wantInput json.RawMessage
			for _, row := range sample.Observations {
				if row.Kind == "core" && row.Method == "async_worker_search_site" {
					var kwargs struct{ Page int }
					_ = json.Unmarshal(row.Kwargs, &kwargs)
					wantPages = append(wantPages, kwargs.Page+1)
				}
				if row.Kind == "plugin" && row.Method == "get_search_page_size" {
					wantInput = row.Kwargs
				}
			}
			if !reflect.DeepEqual(pages, wantPages) || len(out.Resources) != 2*len(wantPages) {
				t.Fatal("source page continuation/short stop differs", pages, wantPages)
			}
			after := ownedModuleRead(t, object)
			if after.Calls[handler]-before.Calls[handler] != len(wantPages) || after.Calls["get_state"]-before.Calls["get_state"] != len(wantPages)*2 {
				t.Fatal("actual source worker/capacity getters not per-page")
			}
			var input struct{ Args, Kwargs json.RawMessage }
			if json.Unmarshal(after.Inputs[handler], &input) != nil || string(input.Args) != "[]" || !originalBaseJSONEqual(input.Kwargs, wantInput) {
				t.Fatal("source page-size site/keyword kwargs differ")
			}
		})
	}
	for _, sample := range reference.PageConfig {
		if sample.Value == nil {
			continue // absent env has the same separately asserted default1
		}
		t.Run("config="+*sample.Value, func(t *testing.T) {
			t.Setenv("SEARCH_RESOURCE_PAGES", *sample.Value)
			if originalResourceSearchPages().String() != strconv.Itoa(sample.Result) {
				t.Fatal("source config int/fallback/clamp differs")
			}
		})
	}
	for _, sample := range reference.PageErrors {
		t.Run(sample.Error, func(t *testing.T) {
			t.Setenv("SEARCH_RESOURCE_PAGES", sample.Value)
			a := newTestApp(t)
			calls := 0
			_, err := a.originalStreamResourceSite(context.Background(), SiteConfig{}, "Owned keyword", 2, func(int) (sites.SearchResult, error) { calls++; return sites.SearchResult{}, nil })
			if err == nil || calls != 0 {
				t.Fatal("source oversized page list did not fail before provider calls")
			}
		})
	}
}

func TestOriginalResourceStreamActualHTTPHostSDK(t *testing.T) {
	t.Setenv("SEARCH_RESOURCE_PAGES", "2")
	reference := readResourceStreamOracle(t)
	wantCount, wantPages, wantPlugin := 0, 0, 0
	for _, frame := range reference.Production.Frames {
		if frame.Type == "append" {
			wantPages++
			wantCount += len(frame.Items)
			for _, row := range frame.Items {
				if row["title"] == "Owned plugin resource" {
					wantPlugin++
				}
			}
		}
	}
	a, cookie, object := ownedModuleFixture(t, true)
	browse := readResourceBrowseOracle(t)
	html, err := os.ReadFile("../sites/testdata/ubits-search.sanitized.html")
	if err != nil {
		t.Fatal(err)
	}
	var nativeCalls atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := nativeCalls.Add(1)
		if r.URL.Path != "/torrents.php" || r.URL.Query().Get("page") != strconv.Itoa(int(n)) || r.URL.Query().Get("search") != "Owned keyword" || r.Header.Get("Cookie") != "owned-site-cookie" {
			t.Error("actual streamed native HTTP page/query/auth differs")
		}
		_, _ = w.Write(html)
	}))
	defer native.Close()
	site := installResourceBrowseSite(t, a, native.URL)
	var plugin json.RawMessage
	for _, sample := range browse.Cases {
		if sample.Name == "plugin_list" {
			plugin = json.RawMessage(strings.ReplaceAll(string(sample.Plugin), browse.Site["url"].(string), native.URL))
		}
	}
	cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_worker_search_site": "module_execute", "get_search_page_size": "module_page_size", "async_refresh_torrents": "module_fail"}, "result": plugin, "page_size": 3})
	saveOwnedModule(t, a, cookie, object, string(cfg))
	object = originalBaseSDKLoaded(t, a, ownedModuleID)
	before := ownedModuleRead(t, object)
	host := httptest.NewServer(a.Handler())
	defer host.Close()
	r, _ := http.NewRequest("POST", host.URL+"/api/search/resources", strings.NewReader(`{"query":"Owned keyword","page":2}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "text/event-stream")
	r.AddCookie(cookie)
	response, err := host.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil || response.StatusCode != 200 || !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatal("actual authenticated resource SSE unavailable", err)
	}
	var complete SearchOutput
	event := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") && event == "complete" {
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &complete) != nil {
				t.Fatal("actual SSE final payload invalid")
			}
		}
	}
	if wantCount == 0 || len(complete.Candidates) != wantCount || len(complete.Sites) != 1 || complete.Sites[0].ID != site.ID || complete.Sites[0].Status != "ok" || int(nativeCalls.Load()) != wantPages {
		t.Fatal("actual source page-capacity loop did not reach streamed host", len(complete.Candidates), nativeCalls.Load())
	}
	plugins := 0
	for _, candidate := range complete.Candidates {
		if candidate.Resource.Title == "Owned plugin resource" {
			plugins++
		}
	}
	if plugins != wantPlugin || strings.Contains(string(raw), "owned-plugin-secret") || strings.Contains(string(raw), "owned-private-material") || strings.Contains(string(raw), "enclosure") {
		t.Fatal("actual worker model/public privacy or page multiplicity differs")
	}
	after := ownedModuleRead(t, object)
	if after.Calls["module_execute"]-before.Calls["module_execute"] != wantPages || after.Calls["module_page_size"]-before.Calls["module_page_size"] != wantPages || after.Calls["get_state"]-before.Calls["get_state"] != wantPages*2 {
		t.Fatal("actual SSE did not invoke independent worker/capacity wrappers each page")
	}
}

func TestOriginalResourceSearchSettingsFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-resource-modules/configuration-source-oracle.json")
	var source struct {
		Commit string
		Inputs []struct {
			Raw, Integer, Pages string
			Normalized          bool
		}
	}
	if err != nil || json.Unmarshal(raw, &source) != nil || source.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(source.Inputs) != 12 {
		t.Fatal("fixed complete settings normalization replay missing", err)
	}
	for _, sample := range source.Inputs {
		t.Run(sample.Raw, func(t *testing.T) {
			got, normalized := ResourceSearchPagesEnvironment(func(name string) (string, bool) { return sample.Raw, name == "SEARCH_RESOURCE_PAGES" })
			if got.String() != sample.Integer || normalized != sample.Normalized {
				t.Fatal("actual Settings generic normalization differs", got, normalized, sample.Integer, sample.Normalized)
			}
			t.Setenv("SEARCH_RESOURCE_PAGES", sample.Raw)
			if originalResourceSearchPages().String() != sample.Pages {
				t.Fatal("actual source normalized setting/fallback/clamp differs")
			}
		})
	}
	pages := big.NewInt(2)
	a, err := New(Options{DataDir: t.TempDir(), ResourceSearchPages: pages})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	pages.SetInt64(99)
	if a.resourceSearchPages.String() != "2" {
		t.Fatal("trusted normalized startup value shared mutable option storage")
	}
}
