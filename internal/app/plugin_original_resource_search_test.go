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

type resourceBrowseSourceOracle struct {
	Commit string
	Site   map[string]any
	Cases  []struct {
		Name         string
		Plugin       json.RawMessage  `json:"plugin_result"`
		Public       []map[string]any `json:"endpoint_returned"`
		Error        *string          `json:"endpoint_error"`
		Observations []struct {
			Kind, Method string
			Kwargs       json.RawMessage
		}
	}
}

func readResourceBrowseOracle(t *testing.T) resourceBrowseSourceOracle {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/standard-resource-modules/source-oracle.json")
	var out resourceBrowseSourceOracle
	if err != nil || json.Unmarshal(raw, &out) != nil || out.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(out.Cases) != 8 {
		t.Fatal("fixed browse/module source oracle missing", err)
	}
	return out
}
func installResourceBrowseSite(t *testing.T, a *App, origin string) SiteConfig {
	t.Helper()
	site := SiteConfig{Config: sites.Config{ID: "owned-resource-site", Kind: "ubits", Name: "Owned resource site", BaseURL: origin, Cookie: "owned-site-cookie", UserAgent: "Owned-UA", Timeout: 15}, SiteOptions: SiteOptions{Domain: "127.0.0.1", Priority: 1}, Enabled: true}
	a.cfgMu.Lock()
	a.config.Sites = []SiteConfig{site}
	a.cfgMu.Unlock()
	if err := a.bootstrapNativeSiteIdentities(context.Background()); err != nil {
		t.Fatal(err)
	}
	identity, err := a.Store.nativeSiteByAppID(context.Background(), site.ID)
	if err != nil || identity.SourceID != 1 {
		t.Fatal("owned actual source site identity missing", err)
	}
	return site
}

func TestOriginalSiteBrowseModuleActualSDKSourceOracle(t *testing.T) {
	reference := readResourceBrowseOracle(t)
	for _, sample := range reference.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			site := installResourceBrowseSite(t, a, reference.Site["url"].(string))
			handler := "module_execute"
			if sample.Name == "failed" {
				handler = "module_fail"
			}
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_refresh_torrents": handler, "refresh_torrents": "module_fail"}, "result": sample.Plugin})
			saveOwnedModule(t, a, cookie, object, string(cfg))
			object = originalBaseSDKLoaded(t, a, ownedModuleID)
			before := ownedModuleRead(t, object)
			nativeCalls := 0
			result, err := a.originalBrowseSiteResources(context.Background(), site, "", 2, func() (sites.SearchResult, error) {
				nativeCalls++
				return sites.SearchResult{Page: 2, Resources: []sites.Resource{{ID: "22", SiteID: site.ID, Title: "Owned core resource", Size: 2048, Seeders: 2, DownloadFactor: 1, UploadFactor: 1}}}, nil
			})
			if (err != nil) != (sample.Error != nil) {
				t.Fatal("source endpoint success/error shape differs", err)
			}
			wantCore := 0
			for _, call := range sample.Observations {
				if call.Kind == "core" {
					wantCore++
				}
			}
			if nativeCalls != wantCore {
				t.Fatal("source scalar/list/null core-call count differs", nativeCalls, wantCore)
			}
			if err == nil {
				if len(result.Resources) != len(sample.Public) {
					t.Fatal("source list accumulation differs", len(result.Resources), len(sample.Public))
				}
				for i, want := range sample.Public {
					if result.Resources[i].Title != want["title"] {
						t.Fatal("source plugin/core list order differs", i)
					}
				}
				if sample.Name == "plugin_list" {
					first := result.Resources[0]
					if first.ID != "11" || first.SiteID != site.ID || first.SourceSubtitle != nil || !first.Free || !strings.Contains(string(first.ModuleSourceFields), "owned-plugin-secret") {
						t.Fatal("private complete source/native safe projection missing")
					}
					raw, _ := json.Marshal(first)
					if strings.Contains(string(raw), "owned-plugin-secret") || strings.Contains(string(raw), "owned-private-material") || strings.Contains(string(raw), "enclosure") {
						t.Fatal("private source torrent credentials exposed in public Resource")
					}
				}
			}
			after := ownedModuleRead(t, object)
			if after.Calls["get_state"]-before.Calls["get_state"] != 1 || after.Calls["get_module"]-before.Calls["get_module"] != 1 {
				t.Fatal("source discovery frame was queried twice")
			}
			if after.Calls[handler]-before.Calls[handler] != 1 {
				t.Fatal("no actual asynchronous source SDK override")
			}
			var input struct{ Args, Kwargs json.RawMessage }
			if json.Unmarshal(after.Inputs[handler], &input) != nil || string(input.Args) != "[]" {
				t.Fatal("actual source SDK arguments missing")
			}
			expected, _ := decodeSourceJSON(sample.Observations[0].Kwargs)
			actual, _ := decodeSourceJSON(input.Kwargs)
			if !reflect.DeepEqual(actual, expected) {
				t.Fatal("pinned async_browse kwargs/site/default/page contract differs")
			}
		})
	}
}

func TestOriginalSiteBrowseActualHTTPHostSDKAndPrivacy(t *testing.T) {
	reference := readResourceBrowseOracle(t)
	a, cookie, object := ownedModuleFixture(t, true)
	html, err := os.ReadFile("../sites/testdata/ubits-search.sanitized.html")
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/torrents.php" || r.URL.Query().Get("search") != "" || r.URL.Query().Get("page") != "1" || r.Header.Get("Cookie") != "owned-site-cookie" {
			t.Error("real native source fallback request differs")
		}
		w.Write(html)
	}))
	defer server.Close()
	site := installResourceBrowseSite(t, a, server.URL)
	var plugin json.RawMessage
	for _, sample := range reference.Cases {
		if sample.Name == "plugin_list" {
			plugin = sample.Plugin
		}
	}
	plugin = json.RawMessage(strings.ReplaceAll(string(plugin), reference.Site["url"].(string), server.URL))
	cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_refresh_torrents": "module_execute", "refresh_torrents": "module_fail"}, "result": plugin})
	saveOwnedModule(t, a, cookie, object, string(cfg))
	response := callTest(t, a.Handler(), "POST", "/api/sites/manage/"+site.ID+"/resources", `{"query":"","page":2}`, cookie, "")
	var out struct{ Data SearchOutput }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &out) != nil || len(out.Data.Candidates) != 3 || len(out.Data.Sites) != 1 || out.Data.Sites[0].Status != "ok" || out.Data.Candidates[0].Resource.Title != "Owned plugin resource" || requests.Load() != 1 {
		t.Fatal("actual native HTTP/SDK/core accumulation did not take effect", response.Code, len(out.Data.Candidates), requests.Load())
	}
	if strings.Contains(response.Body.String(), "owned-plugin-secret") || strings.Contains(response.Body.String(), "owned-private-material") || strings.Contains(response.Body.String(), "site_cookie") || strings.Contains(response.Body.String(), "enclosure") {
		t.Fatal("native public response disclosed private source fields")
	}
}
