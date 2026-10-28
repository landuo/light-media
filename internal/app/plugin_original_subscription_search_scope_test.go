package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/download"
)

func TestOriginalSubscriptionSearchSharedScopeActualSDKSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-resource-modules/subscription-search-scope-source-oracle.json")
	var source struct {
		Commit string
		Cases  []struct {
			Selected []int `json:"selected_sites"`
			Missing  []int
			Calls    []struct {
				Sites   []int
				Missing map[string]map[string]struct{ Episodes []int }
			} `json:"process_calls"`
			Recognition int `json:"recognition_count"`
		}
	}
	if err != nil || json.Unmarshal(raw, &source) != nil || source.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(source.Cases) != 3 {
		t.Fatal("actual fixed subscription process scope required", err)
	}
	t.Setenv("SEARCH_RESOURCE_PAGES", "1")
	t.Setenv("SEARCH_KEYWORD_MIN_INTERVAL", "0")
	for i, sample := range source.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			first := installResourceBrowseSite(t, a, "http://127.0.0.1:43100")
			second, other := first, first
			second.ID, second.Name, second.BaseURL = "owned-second-site", "Owned second site", "http://127.0.0.1:43101"
			other.ID, other.Name, other.BaseURL = "owned-unselected-site", "Owned unselected site", "http://127.0.0.1:43102"
			a.cfgMu.Lock()
			a.config.Sites = append(a.config.Sites, second, other)
			a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-qb", Kind: "qb", BaseURL: "http://127.0.0.1:1"}}}
			a.cfgMu.Unlock()
			ctx := context.Background()
			if err := a.bootstrapNativeSiteIdentities(ctx); err != nil {
				t.Fatal(err)
			}
			provider := json.RawMessage(`{"id":900043,"name":"Owned scope","original_name":"Owned scope","first_air_date":"2020-01-01","number_of_seasons":1,"number_of_episodes":3,"seasons":[{"season_number":1,"episode_count":3}],"alternative_titles":{"results":[{"title":"Owned scope"}]},"genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`)
			full, err := sourceTMDBMediaInfo(provider, "tv", "image.tmdb.org", "")
			if err != nil {
				t.Fatal(err)
			}
			info, err := a.originalRecognitionProjection(catalog.Media{ID: 900043, MediaType: "tv"}, full)
			if err != nil {
				t.Fatal(err)
			}
			episodes := []catalog.Episode{}
			for n := 1; n <= 3; n++ {
				episodes = append(episodes, catalog.Episode{ID: int64(n), SeasonNumber: 1, EpisodeNumber: n, AirDate: "2020-01-01"})
			}
			for key, v := range map[string]any{"media:tv:900043": info.Media, "season:900043:1": catalog.Season{SeriesID: 900043, SeasonNumber: 1, Episodes: episodes}} {
				if err := a.Store.Put(ctx, "cache", key, map[string]any{"at": time.Now(), "data": v}); err != nil {
					t.Fatal(err)
				}
			}
			siteIDs := []string{}
			for _, id := range sample.Selected {
				siteIDs = append(siteIDs, map[int]string{1: first.ID, 2: second.ID}[id])
			}
			s := Subscription{ID: "owned-scope", Media: info.Media, Season: 1, Keyword: "Owned scope", TargetScope: "episodes", Episodes: sample.Missing, SiteIDs: siteIDs, DownloaderID: "owned-qb", State: "active", SourceState: "R", Revision: 1, TotalEpisodes: 3, EpisodeDates: map[int]string{}}
			if err := a.Store.Put(ctx, "subscription", s.ID, s); err != nil {
				t.Fatal(err)
			}
			cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_page_size", "batch_search_torrents": "module_execute", "async_batch_search_torrents": "module_fail"}, "page_size": full, "result": map[string]any{"1": []any{}, "2": []any{}}})
			saveOwnedModule(t, a, cookie, object, string(cfg))
			object = originalBaseSDKLoaded(t, a, ownedModuleID)
			before := ownedModuleRead(t, object)
			if err := a.checkSubscription(ctx, importClaim(t, a, "subscription", s.ID), s.ID, s.Revision); err != nil {
				t.Fatal(err)
			}
			after := ownedModuleRead(t, object)
			if after.Calls["module_execute"]-before.Calls["module_execute"] != len(sample.Calls) || after.Calls["module_page_size"]-before.Calls["module_page_size"] != sample.Recognition || after.Calls["module_fail"] != before.Calls["module_fail"] {
				t.Fatal("subscription repeated per-episode/site dispatch or selected wrong sync hook", after.Calls, before.Calls)
			}
			var call struct {
				Kwargs struct {
					Sites   []struct{ ID int }
					Keyword string
					Mtype   string
					Page    int
				}
			}
			if json.Unmarshal(after.Inputs["module_execute"], &call) != nil {
				t.Fatal("actual SDK call missing")
			}
			ids := []int{}
			for _, site := range call.Kwargs.Sites {
				ids = append(ids, site.ID)
			}
			if !reflect.DeepEqual(ids, sample.Calls[0].Sites) || call.Kwargs.Keyword != "Owned scope" || call.Kwargs.Mtype != "电视剧" || call.Kwargs.Page != 0 {
				t.Fatal("source selected-site scope/default page/type differs", ids, call.Kwargs)
			}
			coverage, err := a.subscriptionCoverage(ctx, s, []int{1, 2, 3})
			if err != nil || !reflect.DeepEqual(coverage.Missing, sample.Missing) {
				t.Fatal("shared search changed missing scope", coverage.Missing, err)
			}
			var usage int
			if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM site_usage`).Scan(&usage); err != nil || usage != 0 {
				t.Fatal("terminal empty batch contacted native/unselected site", usage, err)
			}
		})
	}
}

// The existing native admission/download fixture supplies actual guarded site
// HTTP and qBittorrent protocol responses. The module only replaces discovery;
// it cannot fake preview material, site attribution, reservations or Add.
func TestOriginalSubscriptionSharedSearchKeepsActualDownloadAttribution(t *testing.T) {
	t.Setenv("SEARCH_RESOURCE_PAGES", "1")
	t.Setenv("SEARCH_KEYWORD_MIN_INTERVAL", "0")
	var object *originalPluginInstance
	var selected []string
	f := newRecognitionSelectionFixture(t, func(f *recognitionSelectionFixture) {
		a := f.app
		ctx := context.Background()
		settings := a.Settings()
		first := settings.Sites[0]
		origin, err := url.Parse(first.BaseURL)
		if err != nil {
			t.Fatal(err)
		}
		proxy := httptest.NewServer(httputil.NewSingleHostReverseProxy(origin))
		t.Cleanup(proxy.Close)
		second := first
		second.ID, second.Name, second.BaseURL = "owned-second-selected", "Owned second selected", proxy.URL
		settings.Sites = append(settings.Sites, second)
		selected = []string{first.ID, second.ID}
		settings.RSSSites = selected
		if err := a.saveSettings(ctx, settings); err != nil {
			t.Fatal(err)
		}
		f.request.SiteIDs = selected
		cookie := setupTestAdmin(t, a)
		installOrderWorker(t, a, cookie, os.Getenv("OWNED_MODULE_OVERRIDE_BINARY"))
		t.Cleanup(a.closeOriginalSessions)
		object = originalBaseSDKLoaded(t, a, ownedModuleID)
		provider := json.RawMessage(`{"id":206484,"name":"诛仙","original_name":"Jade Dynasty","first_air_date":"2022-01-01","seasons":[{"season_number":4,"episode_count":5}],"alternative_titles":{"results":[{"title":"Jade Dynasty"}]},"genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`)
		full, err := sourceTMDBMediaInfo(provider, "tv", "image.tmdb.org", "")
		if err != nil {
			t.Fatal(err)
		}
		rows := map[string][]map[string]any{}
		for i, site := range []SiteConfig{first, second} {
			identity, err := a.Store.nativeSiteByAppID(ctx, site.ID)
			if err != nil {
				t.Fatal(err)
			}
			// Deliberately reverse episode order across the selected sites. A stale
			// outer loop's site/episode must not substitute for the resource's owner.
			episode := 2 - i
			resource := 101 + episode - 1
			rows[fmt.Sprint(identity.SourceID)] = []map[string]any{{"site": identity.SourceID, "site_name": site.Name, "title": fmt.Sprintf("Jade.Dynasty.S04E%02d.2160p.WEB-DL.HDR10-FixtureTeam", episode), "description": nil, "page_url": fmt.Sprintf("%s/details.php?id=%d", site.BaseURL, resource), "enclosure": fmt.Sprintf("%s/download.php?id=%d", site.BaseURL, resource), "size": int64(1610612736), "seeders": 12, "peers": 2, "grabs": 99, "uploadvolumefactor": 2, "downloadvolumefactor": 0, "pri_order": 0}}
		}
		cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_page_size", "batch_search_torrents": "module_execute"}, "page_size": full, "result": rows})
		saveOwnedModule(t, a, cookie, object, string(cfg))
		object = originalBaseSDKLoaded(t, a, ownedModuleID)
	})
	after := ownedModuleRead(t, object)
	if after.Calls["module_execute"] != 1 || after.Calls["module_page_size"] != 1 {
		t.Fatal("actual multi-episode/two-site job repeated recognition/search", after.Calls)
	}
	raw, err := f.app.Store.List(context.Background(), "intent", 10, 0)
	if err != nil || len(raw) != 2 {
		t.Fatal("missing actual selected intents", len(raw), err)
	}
	seen := map[int]string{}
	for _, value := range raw {
		var intent Intent
		if json.Unmarshal(value, &intent) != nil || len(intent.Episodes) != 1 {
			t.Fatal("bad actual intent")
		}
		seen[intent.Episodes[0]] = intent.SiteID
	}
	if !reflect.DeepEqual(seen, map[int]string{1: selected[1], 2: selected[0]}) {
		t.Fatal("shared candidates rebound to current site/episode", seen)
	}
	for i := 0; i < 2; i++ {
		job, _ := recognitionSelectionClaim(t, f)
		recognitionSelectionRun(t, f, job)
	}
	if f.adds.Load() != 2 {
		t.Fatal("actual guarded materials did not reach two owned qBittorrent adds", f.adds.Load())
	}
	var reservations int
	if err := f.app.Store.DB.QueryRow(`SELECT COUNT(*) FROM reservations WHERE media_key=? AND season=4`, mediaKey(f.sub.Media)).Scan(&reservations); err != nil || reservations != 2 {
		t.Fatal("shared download lost distinct episode reservations", reservations, err)
	}
}
