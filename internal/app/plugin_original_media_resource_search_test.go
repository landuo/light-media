package app

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/sites"
)

type mediaResourceSearchOracle struct {
	Commit string
	Cases  []struct {
		Entry         string
		MediaType     string `json:"media_type"`
		Area, Keyword string
		ActualKeyword string          `json:"actual_keyword"`
		Plugin        json.RawMessage `json:"plugin_result"`
		Observations  []resourceStreamObservation
		Returned      []map[string]any
	}
	Plans []struct {
		Fields   map[string]any
		Keyword  *string
		Limit    int `json:"maximum_names"`
		Keywords []string
	} `json:"keyword_plans"`
}

func TestOriginalMediaSearchKeywordPlansSource(t *testing.T) {
	for _, sample := range readMediaResourceSearchOracle(t).Plans {
		query := ""
		if sample.Keyword != nil {
			query = *sample.Keyword
		}
		actual, err := originalMediaSearchKeywords(sample.Fields, query, big.NewInt(int64(sample.Limit)))
		if err != nil || !reflect.DeepEqual(actual, sample.Keywords) {
			t.Fatal("fixed prepare_params order/dedup/truth/slice differs", err, actual, sample.Keywords)
		}
	}
}

func TestOriginalMediaSearchActualHTTPAndSDKCaller(t *testing.T) {
	t.Setenv("SEARCH_RESOURCE_PAGES", "1")
	t.Setenv("SEARCH_MULTIPLE_NAME", "false")
	t.Setenv("SEARCH_KEYWORD_MIN_INTERVAL", "0")
	for _, entry := range []string{"web", "stream", "subscription"} {
		t.Run(entry, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			var nativeCalls atomic.Int32
			html, err := os.ReadFile("../sites/testdata/ubits-search.sanitized.html")
			if err != nil {
				t.Fatal(err)
			}
			native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nativeCalls.Add(1)
				if r.URL.Path != "/torrents.php" || r.URL.Query().Get("search") != "Owned title" || r.URL.Query().Get("page") != "0" || r.Header.Get("Cookie") != "owned-site-cookie" {
					t.Error("actual media caller guarded native page/query differs")
				}
				_, _ = w.Write(html)
			}))
			defer native.Close()
			site := installResourceBrowseSite(t, a, native.URL)
			provider := json.RawMessage(`{"id":900043,"title":"Owned title","original_title":"Owned original","release_date":"2026-01-01","genres":[],"alternative_titles":{"titles":[{"title":"Owned title"}]},"credits":{"cast":[],"crew":[]},"external_ids":{}}`)
			full, err := sourceTMDBMediaInfo(provider, "movie", "image.tmdb.org", "")
			if err != nil {
				t.Fatal(err)
			}
			info, err := a.originalRecognitionProjection(catalog.Media{ID: 900043, MediaType: "movie"}, full)
			if err != nil {
				t.Fatal(err)
			}
			reference := readMediaResourceSearchOracle(t)
			sample := reference.Cases[2]
			method := "async_batch_search_torrents"
			if entry == "subscription" {
				sample = reference.Cases[0]
				method = "batch_search_torrents"
			}
			if entry == "stream" {
				sample = reference.Cases[4]
				method = "async_worker_search_site"
			}
			result := json.RawMessage(strings.ReplaceAll(string(sample.Plugin), readResourceBrowseOracle(t).Site["url"].(string), native.URL))
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{method: "module_execute", "async_recognize_media": "module_page_size", "recognize_media": "module_fail"}, "result": result, "page_size": full})
			saveOwnedModule(t, a, cookie, object, string(cfg))
			object = originalBaseSDKLoaded(t, a, ownedModuleID)
			before := ownedModuleRead(t, object)
			var out SearchOutput
			if entry == "subscription" {
				out, err = a.search(context.Background(), SearchRequest{originalSearchInfo: &info, MediaType: "movie", TMDBID: 900043, SiteIDs: []string{site.ID}}, true)
			} else {
				request := map[string]any{"media_type": "movie", "tmdb_id": 900043, "site_ids": []string{site.ID}}
				raw, _ := json.Marshal(request)
				if entry == "stream" {
					r := httptest.NewRequest("POST", "/api/search", strings.NewReader(string(raw)))
					r.Header.Set("Content-Type", "application/json")
					r.Header.Set("Accept", "text/event-stream")
					r.AddCookie(cookie)
					response := httptest.NewRecorder()
					a.Handler().ServeHTTP(response, r)
					if response.Code != 200 || !strings.Contains(response.Body.String(), "event: complete") || strings.Contains(response.Body.String(), "event: error") {
						t.Fatal("actual media SSE failed", response.Code)
					}
					for _, frame := range strings.Split(response.Body.String(), "\n\n") {
						if strings.HasPrefix(frame, "event: complete\ndata: ") {
							_ = json.Unmarshal([]byte(strings.TrimPrefix(frame, "event: complete\ndata: ")), &out)
						}
					}
				} else {
					response := callTest(t, a.Handler(), "POST", "/api/search", string(raw), cookie, "")
					var body struct{ Data SearchOutput }
					if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil {
						t.Fatal("actual media HTTP failed", response.Code)
					}
					out = body.Data
				}
			}
			wantRows, wantNative := 1, int32(0)
			if entry == "stream" {
				wantRows, wantNative = 3, 1
			}
			if err != nil || len(out.Candidates) != wantRows || nativeCalls.Load() != wantNative {
				t.Fatal("actual existing media host did not consume source module result", err, len(out.Candidates), nativeCalls.Load())
			}
			after := ownedModuleRead(t, object)
			if after.Calls["module_execute"]-before.Calls["module_execute"] != 1 || after.Calls["module_fail"] != before.Calls["module_fail"] {
				t.Fatal("media host selected wrong sync/async method or searched after first raw result")
			}
			var input struct{ Args, Kwargs json.RawMessage }
			_ = json.Unmarshal(after.Inputs["module_execute"], &input)
			var actual, want map[string]json.RawMessage
			_ = json.Unmarshal(input.Kwargs, &actual)
			_ = json.Unmarshal(sample.Observations[0].Kwargs, &want)
			if string(input.Args) != "[]" || len(actual) != len(want) {
				t.Fatal("media host source kwargs shape differs")
			}
			for key, value := range want {
				if key == "site" || key == "sites" {
					continue
				}
				if !originalBaseJSONEqual(actual[key], value) {
					t.Fatal("media source caller kwargs differs", key)
				}
			}
			if entry != "subscription" {
				if after.Calls["module_page_size"]-before.Calls["module_page_size"] != 1 {
					t.Fatal("initial Web recognition repeats despite actual nonempty source names")
				}
				var recognition struct{ Kwargs map[string]json.RawMessage }
				_ = json.Unmarshal(after.Inputs["module_page_size"], &recognition)
				if len(recognition.Kwargs) != 10 || string(recognition.Kwargs["mediaid"]) != "null" || string(recognition.Kwargs["meta"]) != "null" || string(recognition.Kwargs["cache"]) != "true" {
					t.Fatal("Web search incorrectly reused async_add cachefalse/meta/ID caller")
				}
			}
		})
	}
}

func readMediaResourceSearchOracle(t *testing.T) mediaResourceSearchOracle {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/standard-resource-modules/media-search-source-oracle.json")
	var out mediaResourceSearchOracle
	if err != nil || json.Unmarshal(raw, &out) != nil || out.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(out.Cases) != 5 {
		t.Fatal("actual fixed media-aware site caller replay unavailable", err)
	}
	return out
}

func TestOriginalMediaResourceModuleArgumentsActualSDKSource(t *testing.T) {
	t.Setenv("SEARCH_RESOURCE_PAGES", "1")
	for _, sample := range readMediaResourceSearchOracle(t).Cases {
		t.Run(sample.Entry+"/"+sample.Area, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			site := installResourceBrowseSite(t, a, readResourceBrowseOracle(t).Site["url"].(string))
			method := map[string]string{"sync": "batch_search_torrents", "async": "async_batch_search_torrents", "stream": "async_worker_search_site"}[sample.Entry]
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{method: "module_execute"}, "result": sample.Plugin})
			saveOwnedModule(t, a, cookie, object, string(cfg))
			object = originalBaseSDKLoaded(t, a, ownedModuleID)
			before := ownedModuleRead(t, object)
			nativeCalls := 0
			native := func(context.Context, SiteConfig, int) (sites.SearchResult, error) {
				nativeCalls++
				return sites.SearchResult{Page: 1, Resources: []sites.Resource{{ID: "22", SiteID: site.ID, Title: "Owned core resource"}}}, nil
			}
			var out sites.SearchResult
			var err error
			if sample.Entry == "stream" {
				out, err = a.originalResourceSiteSearchPagesMedia(context.Background(), method, site, sample.ActualKeyword, sample.MediaType, 1, func(page int) (sites.SearchResult, error) { return native(context.Background(), site, page) })
			} else {
				op := &originalResourceBatchSearch{app: a, method: method, mtype: sample.MediaType, keyword: sample.ActualKeyword, page: 1, native: native}
				op.attach(context.Background(), []SiteConfig{site})
				out, err = op.site(site)
			}
			wantNative := 0
			for _, call := range sample.Observations {
				if call.Kind == "core" && call.Method != "get_search_page_size" {
					wantNative++
				}
			}
			if err != nil || len(out.Resources) != len(sample.Returned) || nativeCalls != wantNative {
				t.Fatal("source sync/async/type/fallback result differs", err, len(out.Resources), nativeCalls, wantNative)
			}
			for i, row := range sample.Returned {
				if out.Resources[i].Title != row["title"] {
					t.Fatal("source media caller result order differs")
				}
			}
			after := ownedModuleRead(t, object)
			var input struct{ Args, Kwargs json.RawMessage }
			_ = json.Unmarshal(after.Inputs["module_execute"], &input)
			if after.Calls["module_execute"]-before.Calls["module_execute"] != 1 || string(input.Args) != "[]" || !originalBaseJSONEqual(input.Kwargs, sample.Observations[0].Kwargs) {
				t.Fatal("source media caller method/mtype/keyword/page differs")
			}
		})
	}
}
