package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"light-media/internal/catalog"
)

func TestOriginalCatalogFeedAPIKwargsActualFixedEndpointCalls(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-catalog-modules/endpoint-calls-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Endpoint string
			Input    map[string]json.RawMessage
			Calls    []struct {
				Method       string
				Args, Kwargs json.RawMessage
			}
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 22 {
		t.Fatal("actual fixed API->Chain->module oracle")
	}
	count := 0
	for _, row := range oracle.Cases {
		if row.Endpoint == "recommend.tmdb_tvs" {
			continue
		} // Native shelf corresponds to the explicit language query in fixed recommend.vue:92.
		t.Run(row.Endpoint, func(t *testing.T) {
			var method string
			var kwargs map[string]any
			if strings.HasPrefix(row.Endpoint, "recommend.") {
				id := strings.TrimPrefix(row.Endpoint, "recommend.")
				id = strings.TrimSuffix(id, ".source-ui")
				method, kwargs = originalCatalogRecommendModule(id, 2)
			} else if row.Endpoint == "anilist.anilist_trending" || row.Endpoint == "anilist.anilist_popular_this_season" {
				id := strings.TrimPrefix(row.Endpoint, "anilist.")
				method, kwargs = originalCatalogRecommendModule(id, 2)
			} else {
				q := url.Values{"page": {"2"}}
				switch row.Endpoint {
				case "discover.tmdb_movies":
					q.Set("source", "themoviedb")
					q.Set("type", "movie")
				case "discover.tmdb_tvs":
					q.Set("source", "themoviedb")
					q.Set("type", "tv")
				case "discover.douban_movies":
					q.Set("source", "douban")
					q.Set("type", "movie")
				case "discover.douban_tvs":
					q.Set("source", "douban")
					q.Set("type", "tv")
				case "discover.bangumi":
					q.Set("source", "bangumi")
				case "anilist.anilist_discover":
					q.Set("source", "anilist")
					q.Set("format", "TV")
					q.Set("season_year", "2026")
				default:
					t.Fatal("unmapped existing source endpoint", row.Endpoint)
				}
				parsed, e := catalog.ParseExploreQuery(q)
				if e != nil {
					t.Fatal(e)
				}
				method, kwargs = originalCatalogExploreModule(parsed, q)
			}
			encoded, _ := json.Marshal(kwargs)
			if len(row.Calls) != 1 || method != row.Calls[0].Method || string(row.Calls[0].Args) != "[]" || !originalBaseJSONEqual(encoded, row.Calls[0].Kwargs) {
				t.Fatal("fixed endpoint module kwargs differ", method, string(encoded), string(row.Calls[0].Kwargs))
			}
		})
		count++
	}
	if count != 21 {
		t.Fatal("source callsites not all compared", count)
	}
}
func TestOriginalCatalogFeedsActualSDKFullDispatcherOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-catalog-modules/feed-dispatch-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name, State    string
			Plugin, Output json.RawMessage
			NativeFailure  bool `json:"native_failure"`
			Raise          bool `json:"raise_exception"`
			Calls          []struct{ Kind string }
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 12 {
		t.Fatal("actual fixed full feed dispatcher/caller source")
	}
	for _, row := range oracle.Cases {
		t.Run(row.Name, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			handler := "module_execute"
			if row.Name == "raise_plugin" {
				handler = "module_fail"
			}
			config, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_tmdb_discover": handler, "tmdb_discover": "module_fail"}, "result": row.Plugin})
			saveOwnedModule(t, a, cookie, object, string(config))
			coreCalls := 0
			kwargs := originalCatalogTMDBDiscover("movie", 2, url.Values{})
			kwargs["raise_exception"] = row.Raise
			result, e := a.originalCatalogFeed(context.Background(), "async_tmdb_discover", kwargs, 2, func() (catalog.ExplorePage, error) {
				coreCalls++
				if row.NativeFailure {
					return catalog.ExplorePage{}, errors.New("owned native provider failure")
				}
				return catalog.ExplorePage{Results: []catalog.ExploreMedia{{Media: catalog.Media{ID: 120, MediaType: "movie", Title: "core"}, Source: "themoviedb", SourceID: "120"}}, Page: 2, TotalResults: 1}, nil
			})
			expectedCalls := 0
			for _, call := range row.Calls {
				if call.Kind == "core" {
					expectedCalls++
				}
			}
			if coreCalls != expectedCalls {
				t.Fatal("source core invocation count differs", coreCalls, expectedCalls)
			}
			if row.State == "error" {
				if e == nil {
					t.Fatal("source caller raises")
				}
				return
			}
			if e != nil {
				t.Fatal("source caller succeeds", e)
			}
			var expected []map[string]any
			if json.Unmarshal(row.Output, &expected) != nil || len(expected) != len(result.Results) {
				t.Fatal("source list count differs", string(row.Output), len(result.Results))
			}
			for i, item := range result.Results {
				if item.Title != expected[i]["title"] || item.Source != "themoviedb" || item.ID != 120 {
					t.Fatal("source list append/order/projection differs", i, item.Title)
				}
				if item.Title == "plugin" && len(item.OriginalMedia) == 0 {
					t.Fatal("source complete plugin object was discarded")
				}
			}
		})
	}
}
func TestOriginalCatalogRecommendationSourceCacheSkipsEmptyAndExpires24h(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	calls := 0
	var result catalog.ExplorePage
	fetch := func() (catalog.ExplorePage, error) {
		calls++
		return catalog.ExplorePage{Results: []catalog.ExploreMedia{}, Page: 1}, nil
	}
	for n := 0; n < 2; n++ {
		if _, e := a.cachedOriginalRecommendation(ctx, "owned-source-cache", &result, fetch); e != nil {
			t.Fatal(e)
		}
	}
	if calls != 2 {
		t.Fatal("fixed skip_empty cache suppressed plugin invocation")
	}
	fetch = func() (catalog.ExplorePage, error) {
		calls++
		return catalog.ExplorePage{Results: []catalog.ExploreMedia{{Media: catalog.Media{Title: "owned cached"}}}, Page: 1}, nil
	}
	for n := 0; n < 2; n++ {
		if _, e := a.cachedOriginalRecommendation(ctx, "owned-source-cache", &result, fetch); e != nil {
			t.Fatal(e)
		}
	}
	if calls != 3 {
		t.Fatal("nonempty source wrapper cache missed")
	}
	importCacheAt := func(at time.Time) {
		data, _ := json.Marshal(result)
		if e := a.Store.Put(ctx, "cache", "owned-source-cache", map[string]any{"at": at, "data": json.RawMessage(data)}); e != nil {
			t.Fatal(e)
		}
	}
	importCacheAt(time.Now().Add(-23 * time.Hour))
	_, _ = a.cachedOriginalRecommendation(ctx, "owned-source-cache", &result, fetch)
	if calls != 3 {
		t.Fatal("recommend source TTL shorter than24h")
	}
	importCacheAt(time.Now().Add(-25 * time.Hour))
	_, _ = a.cachedOriginalRecommendation(ctx, "owned-source-cache", &result, fetch)
	if calls != 4 {
		t.Fatal("recommend source cache failed to expire")
	}
}
