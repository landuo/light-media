package app

import (
	"encoding/json"
	"fmt"
	"light-media/internal/catalog"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCatalogRecommendCachesEachListAndPageAndPreservesIdentity(t *testing.T) {
	a, _, _ := importTestApp(t)
	if err := a.Store.Put(t.Context(), "subscription", "sub-123", map[string]any{
		"id": "sub-123", "state": "active", "media": catalog.Media{ID: 123, MediaType: "movie"},
	}); err != nil {
		t.Fatal(err)
	}
	type request struct {
		id     string
		page   int
		source string
		tmdbID int64
	}
	requests := []request{
		{"douban_movie_hot", 1, "douban", 0},
		{"douban_movie_hot", 2, "douban", 0},
		{"douban_movies", 1, "douban", 0},
		{"douban_movies", 2, "douban", 0},
		{"tmdb_movies", 1, "themoviedb", 123},
	}
	for _, request := range requests {
		p := catalog.ExplorePage{
			Results: []catalog.ExploreMedia{{
				Media:  catalog.Media{ID: request.tmdbID, Title: fmt.Sprintf("%s/%d", request.id, request.page), MediaType: "movie"},
				Source: request.source, SourceID: "123",
			}},
			Page: request.page, TotalPages: 4, TotalResults: 80, HasMore: true,
		}
		importCache(t, a, recommendCacheKey(request.id, request.page, time.Now()), p)
	}
	// Every response is seeded locally; provider credentials and network calls
	// are unnecessary, while collisions would return another row or page.
	for _, request := range requests {
		target := fmt.Sprintf("/api/catalog/recommend?id=%s&page=%d", request.id, request.page)
		value, err := a.catalogRecommendAPI(httptest.NewRequest("GET", target, nil))
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			catalog.ExplorePage
			CachedAt time.Time `json:"cached_at"`
		}
		if err = json.Unmarshal(data, &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Results) != 1 || response.Page != request.page || response.TotalPages != 4 || response.TotalResults != 80 || !response.HasMore || response.CachedAt.IsZero() {
			t.Fatalf("invalid pagination/cache metadata: %s", data)
		}
		item := response.Results[0]
		if item.Title != fmt.Sprintf("%s/%d", request.id, request.page) || item.Source != request.source || item.SourceID != "123" || item.ID != request.tmdbID {
			t.Fatalf("cache or source identity collision: %s", data)
		}
		wantSubscription := ""
		if request.source == "themoviedb" {
			wantSubscription = "sub-123"
		}
		if item.SubscriptionID != wantSubscription {
			t.Fatalf("native source ID decorated as TMDB identity: %s", data)
		}
	}
}

func TestRecommendQueryValidation(t *testing.T) {
	for _, query := range []string{
		"", "id=unknown", "id=tmdb_movies&id=tmdb_tvs", "id=tmdb_movies&page=1&page=2",
		"id=tmdb_movies&page=0", "id=tmdb_movies&page=-1", "id=tmdb_movies&page=501",
		"id=tmdb_movies&page=", "id=tmdb_movies&page=1.5", "id=tmdb_movies&page=99999999999999999999999999",
		"id=tmdb_movies&source=themoviedb", "id=tmdb_movies&unknown=1", "id=tmdb_movies&page=%zz",
		"id=tmdb_movies;page=2", "id=tmdb_movies%00", "id=tmdb_movies&page=%0A1",
	} {
		t.Run(query, func(t *testing.T) {
			if _, _, err := parseRecommendQuery(query); err == nil {
				t.Fatal("invalid recommendation query accepted")
			}
		})
	}
	for _, source := range catalog.RecommendSources() {
		for _, page := range []int{1, 500} {
			query := fmt.Sprintf("id=%s&page=%d", source.ID, page)
			gotID, gotPage, err := parseRecommendQuery(query)
			if err != nil || gotID != source.ID || gotPage != page {
				t.Fatalf("valid query rejected: %s, %s/%d, %v", query, gotID, gotPage, err)
			}
		}
	}
	if id, page, err := parseRecommendQuery("id=tmdb_movies"); err != nil || id != "tmdb_movies" || page != 1 {
		t.Fatalf("default page: %s/%d, %v", id, page, err)
	}
}

func TestRecommendCacheRollsOverAtShanghaiMidnight(t *testing.T) {
	// This boundary changes both Bangumi's weekday and AniList's quarter.
	before := time.Date(2026, 3, 31, 15, 59, 59, 0, time.UTC)
	after := before.Add(time.Second)
	for _, source := range catalog.RecommendSources() {
		oldKey := recommendCacheKey(source.ID, 1, before)
		newKey := recommendCacheKey(source.ID, 1, after)
		if oldKey == newKey || !strings.HasSuffix(oldKey, ":2026-03-31") || !strings.HasSuffix(newKey, ":2026-04-01") {
			t.Fatalf("stale calendar/season cache namespace: %q, %q", oldKey, newKey)
		}
		if newKey != recommendCacheKey(source.ID, 1, after.Add(12*time.Hour)) {
			t.Fatal("same Shanghai date did not reuse its cache")
		}
		if newKey == recommendCacheKey(source.ID, 2, after) {
			t.Fatal("recommend pages share a cache key")
		}
	}
}

func TestRecommendPreferencesPersistAcrossStoreReopenIndependently(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if store != nil {
			_ = store.Close()
		}
	})
	a := &App{Store: store}
	ctx := t.Context()
	defaults, err := a.catalogRecommendPreferences(ctx)
	if err != nil || len(defaults.Enabled) != 15 {
		t.Fatalf("defaults: %+v, %v", defaults, err)
	}
	for id, enabled := range defaults.Enabled {
		if !enabled {
			t.Fatalf("source %s is disabled by default", id)
		}
	}
	explore, err := a.saveCatalogExplorePreferences(ctx, ExplorePreferences{TabOrder: []string{"anilist", "bangumi"}})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := a.saveCatalogRecommendPreferences(ctx, RecommendPreferences{Enabled: map[string]bool{"tmdb_movies": false, "douban_movie_hot": false}})
	if err != nil || len(saved.Enabled) != len(defaults.Enabled) || saved.Enabled["tmdb_movies"] || saved.Enabled["douban_movie_hot"] || !saved.Enabled["tmdb_tvs"] {
		t.Fatalf("partial preferences were not completed: %+v, %v", saved, err)
	}
	// Fixed user/config/Recommend stores ordinary extra source path keys even
	// while a plugin is stopped; normalizing builtins must not discard them.
	saved.Enabled["plugin/Owned/feed?list=ordinary"] = false
	saved, err = a.saveCatalogRecommendPreferences(ctx, saved)
	if err != nil || saved.Enabled["plugin/Owned/feed?list=ordinary"] {
		t.Fatal("extra source preference not retained", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store = nil
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a = &App{Store: store}
	got, err := a.catalogRecommendPreferences(ctx)
	if err != nil || !reflect.DeepEqual(got, saved) {
		t.Fatalf("preferences lost after restart or invalid update: %+v, %v", got, err)
	}
	gotExplore, err := a.catalogExplorePreferences(ctx)
	if err != nil || !reflect.DeepEqual(gotExplore, explore) {
		t.Fatalf("recommend preferences changed explore preferences: %+v, %v", gotExplore, err)
	}
	reset, err := a.saveCatalogRecommendPreferences(ctx, RecommendPreferences{})
	if err != nil || !reflect.DeepEqual(reset, defaults) {
		t.Fatalf("empty settings did not restore defaults: %+v, %v", reset, err)
	}
}

func TestRecommendPreferencesRejectNonBooleanJSON(t *testing.T) {
	for _, body := range []string{
		`null`, `[]`, `{"enabled":null}`, `{"enabled":[]}`, `{"enabled":true}`,
		`{"enabled":{"tmdb_movies":null}}`, `{"enabled":{"tmdb_movies":"false"}}`,
		`{"enabled":{"tmdb_movies":0}}`, `{"enabled":{"tmdb_movies":[]}}`,
		`{"enabled":{"tmdb_movies":{}}}`, `{"enable":{"tmdb_movies":false}}`,
	} {
		t.Run(body, func(t *testing.T) {
			var in RecommendPreferences
			r := httptest.NewRequest("PUT", "/api/catalog/recommend/preferences", strings.NewReader(body))
			if err := decodeBody(httptest.NewRecorder(), r, &in); err == nil {
				t.Fatal("invalid recommendation preferences accepted")
			}
		})
	}
	for _, body := range []string{`{}`, `{"enabled":{}}`, `{"enabled":{"tmdb_movies":false,"tmdb_tvs":true}}`} {
		var in RecommendPreferences
		if err := json.Unmarshal([]byte(body), &in); err != nil {
			t.Fatalf("valid preferences rejected: %s: %v", body, err)
		}
		if _, err := normalizeRecommendPreferences(in); err != nil {
			t.Fatalf("valid preferences rejected: %s: %v", body, err)
		}
	}
}
