package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"light-media/internal/catalog"
)

func TestOriginalCatalogSearchActualSourceProviderAndModel(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-catalog-modules/search-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Movies, TV, Multi json.RawMessage
		Cases             []struct {
			Name string
			Meta struct {
				Name, Type string
				Year       *string
				Season     *int `json:"begin_season"`
			}
			Source       *string
			SearchSource string `json:"search_source"`
			Rows         []json.RawMessage
			Calls        []struct{ Kind string }
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 8 {
		t.Fatal("actual source search/module/provider/MediaInfo constructor")
	}
	for _, row := range oracle.Cases {
		t.Run(row.Name, func(t *testing.T) {
			a := newTestApp(t)
			settings := a.Settings()
			settings.TMDB = TMDBConfig{APIKey: "owned-source-search"}
			settings.SearchSource = &row.SearchSource
			a.cfgMu.Lock()
			a.config = settings
			a.cfgMu.Unlock()
			proxy, _ := a.proxyConfig()
			calls := []string{}
			a.catMu.Lock()
			a.catCreds = settings.TMDB
			a.catProxy = proxy
			a.catClient = catalog.NewWithTransport(catalog.Credentials{APIKey: settings.TMDB.APIKey}, imageTransport(func(r *http.Request) (*http.Response, error) {
				kind := strings.TrimPrefix(r.URL.Path, "/3/search/")
				calls = append(calls, kind)
				var body json.RawMessage
				switch kind {
				case "movie":
					body = oracle.Movies
				case "tv":
					body = oracle.TV
				case "multi":
					body = oracle.Multi
				default:
					t.Fatal("unexpected provider path", r.URL.Path)
				}
				if r.URL.Host != "api.themoviedb.org" || r.URL.Query().Get("page") != "1" || r.URL.Query().Get("query") != "Owned" || r.URL.Query().Has("include_adult") {
					t.Fatal("fixed source provider query", r.URL.Query())
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"results":` + string(body) + `}`))}, nil
			}))
			a.catMu.Unlock()
			kind, title := "", row.Meta.Name
			if row.Meta.Type == "电影" {
				kind = "movie"
			} else if row.Meta.Type == "电视剧" {
				kind = "tv"
			}
			if row.Meta.Year != nil {
				title += " " + *row.Meta.Year
			}
			if row.Meta.Season != nil {
				if *row.Meta.Season == 0 {
					title += " S00"
				} else {
					title += " S02"
				}
			}
			source := ""
			if row.Source != nil {
				source = *row.Source
			}
			got, e := a.originalCatalogSourceSearch(context.Background(), title, kind, source, 1, 20)
			if e != nil {
				t.Fatal(e)
			}
			if len(calls) != len(row.Calls) || len(got.Results) != len(row.Rows) {
				t.Fatal("fixed search source/predicate/provider count", calls, len(got.Results), len(row.Rows))
			}
			for i, call := range calls {
				if call != row.Calls[i].Kind {
					t.Fatal("provider branch order", calls, row.Calls)
				}
			}
			for i, want := range row.Rows {
				if !originalBaseJSONEqual(got.Results[i].OriginalMedia, want) {
					t.Fatal("actual source constructor/full original media differs", string(got.Results[i].OriginalMedia), string(want))
				}
			}
		})
	}
}

func TestOriginalCatalogSearchActualSDKRawPagingNativePageTwo(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-catalog-modules/search-paging-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Input json.RawMessage
		Cases []struct {
			Name, Order string
			Page, Count int
			Output      json.RawMessage
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 10 {
		t.Fatal("actual fixed source caller paging")
	}
	a, cookie, object := ownedModuleFixture(t, true)
	settings := a.Settings()
	settings.TMDB = TMDBConfig{APIKey: "owned-source-sdk-search"}
	a.cfgMu.Lock()
	a.config = settings
	a.cfgMu.Unlock()
	proxy, _ := a.proxyConfig()
	calls := []string{}
	a.catMu.Lock()
	a.catCreds = settings.TMDB
	a.catProxy = proxy
	a.catClient = catalog.NewWithTransport(catalog.Credentials{APIKey: settings.TMDB.APIKey}, imageTransport(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.URL.Path+"?page="+r.URL.Query().Get("page"))
		body := `{"results":[]}`
		if r.URL.Path == "/3/search/movie" && r.URL.Query().Get("page") == "2" {
			body = `{"page":2,"total_pages":3,"total_results":60,"results":[{"id":220,"title":"native second page","release_date":"2026-01-01"}]}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	a.catMu.Unlock()
	cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_search_medias": "module_execute", "search_medias": "module_fail"}, "result": oracle.Input})
	saveOwnedModule(t, a, cookie, object, string(cfg))
	object = originalBaseSDKLoaded(t, a, ownedModuleID)
	for _, row := range oracle.Cases {
		t.Run(row.Name, func(t *testing.T) {
			a.cfgMu.Lock()
			a.config.SearchSource = &row.Order
			a.cfgMu.Unlock()
			query := url.Values{"title": {"Owned"}, "page": {strconv.Itoa(row.Page)}, "count": {strconv.Itoa(row.Count)}}
			before := ownedModuleRead(t, object)
			response := callTest(t, a.Handler(), "GET", "/api/v1/media/search?"+query.Encode(), "", cookie, "")
			if response.Code != 200 || !originalBaseJSONEqual(response.Body.Bytes(), row.Output) {
				t.Fatal("actual raw source sort/slice/default model", response.Code, response.Body.String(), string(row.Output))
			}
			after := ownedModuleRead(t, object)
			if after.Calls["module_execute"] != before.Calls["module_execute"]+1 || after.Calls["module_fail"] != before.Calls["module_fail"] {
				t.Fatal("actual async search getter method")
			}
			var input struct{ Kwargs map[string]json.RawMessage }
			_ = json.Unmarshal(after.Inputs["module_execute"], &input)
			var meta map[string]json.RawMessage
			_ = json.Unmarshal(input.Kwargs["meta"], &meta)
			if string(meta["name"]) != `"Owned"` || input.Kwargs["page"] != nil || input.Kwargs["count"] != nil || string(input.Kwargs["source"]) != "null" {
				t.Fatal("fixed source module kwargs/complete metadata", input.Kwargs)
			}
		})
	}
	// None keeps the established native provider's real second-page request.
	cfg, _ = json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_search_medias": "module_execute"}, "result": nil})
	saveOwnedModule(t, a, cookie, object, string(cfg))
	response := callTest(t, a.Handler(), "GET", "/api/catalog/search?q=Owned&kind=movie&page=2", "", cookie, "")
	var native struct{ Data catalog.ExplorePage }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &native) != nil || native.Data.Page != 2 || native.Data.TotalResults != 60 || len(native.Data.Results) != 1 || native.Data.Results[0].ID != 220 || calls[len(calls)-1] != "/3/search/movie?page=2" {
		t.Fatal("native provider pagination regressed", response.Code, response.Body.String(), calls)
	}
}
