package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/sites"
)

func TestOriginalHostBusinessCategoriesCompleteSupportedCatalogBinding(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-integration-20261031/catalog-supported-complete-r2/projection.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Rows       []originalHostCategoryTemplate
		Projection string `json:"allowed_projection_sha256"`
	}
	if json.Unmarshal(raw, &source) != nil || len(source.Rows) != 165 || source.Projection != "474cb360650e642866072498747b51821548992c8c4c8a68f26362fa069078f5" {
		t.Fatal("complete source projection binding changed")
	}
	var embedded []originalHostCategoryTemplate
	if json.Unmarshal([]byte(originalHostCategoryData), &embedded) != nil || len(embedded) != len(source.Rows) {
		t.Fatal("complete catalog was truncated")
	}
	for _, row := range source.Rows {
		got, ok := originalHostCategoryForSite(SiteConfig{SiteOptions: SiteOptions{Domain: row.Domain}})
		if !ok || !originalBaseJSONEqual(mustCleanSeedJSON(got.Category), mustCleanSeedJSON(row.Category)) || got.Schema != row.Schema {
			t.Fatal("source category/domain lost or aliased to another schema", row.Domain)
		}
	}
	t.Logf("source projection %s; embedded limited projection SHA %x; source165, category114", source.Projection, sha256.Sum256([]byte(originalHostCategoryData)))
}

func TestOriginalHostBusinessCategoriesFixedSourceBlockAndSpecials(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-business-20261031/category-supported-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Commit string `json:"source_commit"`
		Cases  []struct {
			Name       string
			Category   map[string]any
			Selection  *string
			Initial    map[string]any
			After      json.RawMessage `json:"params_after"`
			ErrorClass *string         `json:"error_class"`
		}
		Specials map[string][]struct {
			Selection *string
			Expected  map[string]json.RawMessage
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&oracle); err != nil || oracle.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Cases) != 855 {
		t.Fatal("fixed source category cases missing", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			params := sample.Initial
			err := originalHostApplyCategory(params, sample.Category, sample.Selection)
			if (err != nil) != (sample.ErrorClass != nil) {
				t.Fatal("source error branch differs", err, sample.ErrorClass)
			}
			if !originalBaseJSONEqual(mustCleanSeedJSON(params), sample.After) {
				t.Fatal("source category parameter order/selection/value changed", string(mustCleanSeedJSON(params)), string(sample.After))
			}
		})
	}
	for name, rows := range oracle.Specials {
		for i, sample := range rows {
			t.Run(fmt.Sprintf("%s/%d", name, i), func(t *testing.T) {
				kind := "mteam"
				if name == "RousiSpider" {
					kind = "rousi"
				}
				params, err := originalHostSiteCategoryParams(SiteConfig{Config: sites.Config{Kind: kind}}, sample.Selection)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "mteam" {
					if len(params) != 0 || string(sample.Expected["categories"]) != "[]" {
						t.Fatal("fixed MTorrent must ignore cat", params)
					}
				} else {
					var want string
					if raw := sample.Expected["category"]; len(raw) > 0 {
						if err := json.Unmarshal(raw, &want); err != nil {
							t.Fatal(err)
						}
					}
					if params.Get("category") != want {
						t.Fatal("fixed Rousi first mapped ID/unknown selection differs", params, want)
					}
				}
			})
		}
	}
}

func TestOriginalHostBusinessCategoriesActualSDKNativeQuery(t *testing.T) {
	a := newTestApp(t)
	_, worker := hostBusinessFixture(t, a)
	selected := "404"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/torrents.php" || r.Header.Get("Cookie") != "owned-cookie" {
			t.Error("category bypassed ordinary site host")
		}
		query := r.URL.Query()
		if selected == "404" {
			if query.Get("cat404") != "1" || query.Get("cat401") != "" || query.Get("cat405") != "" {
				t.Error("source whitelist category changed", query)
			}
		} else if selected == "" {
			for _, id := range []string{"401", "402", "403", "404", "405"} {
				if query.Get("cat"+id) != "1" {
					t.Error("source default movie+TV missing", id, query)
				}
			}
		} else {
			for key := range query {
				if len(key) >= 3 && key[:3] == "cat" {
					t.Error("unknown category manufactured a selector", query)
				}
			}
		}
		fmt.Fprint(w, `<a href="logout.php">Logout</a><a href="userdetails.php?id=1">Owned</a><table class="torrents"><tr><td>No torrents found</td></tr></table>`)
	}))
	defer server.Close()
	config := a.Settings()
	site := SiteConfig{Config: sites.Config{ID: "owned-category", Name: "Owned", Kind: "ubits", BaseURL: server.URL, Cookie: "owned-cookie"}, SiteOptions: SiteOptions{Domain: "ubits.club"}, Enabled: true}
	config.Sites = []SiteConfig{site}
	ownedDownloaderSave(t, a, config)
	for _, value := range []string{"404", "", "unknown"} {
		selected = value
		var result plugins.SiteBrowseHostResultV1
		category := value
		if err := hostBusinessCall(t, worker, plugins.SitesBrowseCapability, plugins.SiteBrowseHostRequestV1{ID: site.ID, Category: &category}, &result); err != nil || len(result.Items) != 0 {
			t.Fatal("actual category host query failed", err)
		}
	}
}

func TestOriginalHostBusinessCategoriesActualSDKSpecialNativeQueries(t *testing.T) {
	for _, kind := range []string{"mteam", "rousi"} {
		t.Run(kind, func(t *testing.T) {
			a := newTestApp(t)
			_, worker := hostBusinessFixture(t, a)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if kind == "mteam" {
					if r.Method != "POST" || r.URL.Path != "/api/torrent/search" || r.Header.Get("x-api-key") != "owned-api" {
						t.Error("mteam source host request changed")
					}
					var payload struct {
						Keyword    string   `json:"keyword"`
						Categories []string `json:"categories"`
						Page       int      `json:"pageNumber"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Keyword != "Owned" || len(payload.Categories) != 0 || payload.Categories == nil || payload.Page != 1 {
						t.Error("mteam must ignore cat", payload, err)
					}
					fmt.Fprint(w, `{"code":0,"data":{"data":[],"total":0}}`)
				} else {
					if r.Method != "GET" || r.URL.Path != "/api/v1/torrents" || r.Header.Get("Authorization") != "Bearer owned-api" || r.URL.Query().Get("category") != "tv" || r.URL.Query().Get("page") != "1" {
						t.Error("rousi first mapped category request changed", r.URL.String())
					}
					fmt.Fprint(w, `{"code":0,"data":{"torrents":[]}}`)
				}
			}))
			defer server.Close()
			origin := server.URL
			if kind == "mteam" {
				// Keep the production allowlist; protocol I/O is sent only to
				// this owned server by the test transport.
				origin = "https://api.m-team.cc"
				local, err := url.Parse(server.URL)
				if err != nil {
					t.Fatal(err)
				}
				previous := http.DefaultTransport
				transport := &http.Transport{Proxy: nil}
				http.DefaultTransport = siteOutcomeRoundTrip(func(request *http.Request) (*http.Response, error) {
					if request.URL.Scheme != "https" || request.URL.Host != "api.m-team.cc" {
						return nil, fmt.Errorf("owned transport denied unexpected origin")
					}
					owned := request.Clone(request.Context())
					target := *request.URL
					target.Scheme, target.Host = local.Scheme, local.Host
					owned.URL, owned.Host, owned.RequestURI = &target, local.Host, ""
					return transport.RoundTrip(owned)
				})
				t.Cleanup(func() { transport.CloseIdleConnections(); http.DefaultTransport = previous })
			}
			config := a.Settings()
			config.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-special", Name: "Owned", Kind: kind, BaseURL: origin, APIKey: "owned-api"}, Enabled: true}}
			ownedDownloaderSave(t, a, config)
			var result plugins.SiteBrowseHostResultV1
			category := "2,1"
			if err := hostBusinessCall(t, worker, plugins.SitesBrowseCapability, plugins.SiteBrowseHostRequestV1{ID: "owned-special", Keyword: "Owned", Category: &category}, &result); err != nil || len(result.Items) != 0 || calls != 1 {
				t.Fatal("actual special category API failed", err, result, calls)
			}
		})
	}
}
