package app

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/catalog"
)

// Source basis: installed-category-helper.py SHA256
// 1c0ec53b78caa3a4949dd970931f38430c67c9d46ea2e16c3cc00950fb24c2b2,
// CategoryHelper.init/get_category, independently replayed with ruamel 0.19.1.
// These unit/integration checks do not claim an original production-host run.
func TestCategoryEditorDeferredConditionLoadsBeforeMatching(t *testing.T) {
	for _, expression := range []string{"16", "1.5", "true", "[16]", "{id: 16}"} {
		t.Run(expression, func(t *testing.T) {
			raw := []byte("movie:\n  Deferred:\n    genre_ids: " + expression + "\n")
			if _, err := parseMediaCategoryPolicy(raw); err == nil {
				t.Fatal("strict validation contract was relaxed")
			}
			policy, kind, err := categoryPluginReload(raw)
			if err != nil || kind != categoryReplaceMaps || len(policy["movie"]) != 1 || policy["movie"][0].name != "Deferred" {
				t.Fatalf("source init must retain the category map: kind=%v policy=%+v err=%v", kind, policy, err)
			}
			if got, err := policy.categoryWithError(catalog.Media{MediaType: "movie"}); err != nil || got != "" {
				t.Fatalf("missing info_value must skip split: %q %v", got, err)
			}
			if got, err := policy.categoryWithError(catalog.Media{MediaType: "movie", GenreIDs: []int64{16}}); err == nil || got != "" {
				t.Fatalf("present info_value must expose split failure: %q %v", got, err)
			}
		})
	}
}

func TestCategoryEditorDeferredConditionUsesSourceEvaluationOrder(t *testing.T) {
	for _, sample := range []struct {
		name, raw, want string
		media           catalog.Media
		fail            bool
	}{
		{
			name:  "earlier mismatch does not hide later split error",
			raw:   "movie:\n  Invalid:\n    original_language: 'en'\n    genre_ids: 16\n  Fallback: {}\n",
			media: catalog.Media{MediaType: "movie", OriginalLanguage: "zh", GenreIDs: []int64{16}}, fail: true,
		},
		{
			name:  "earlier absent field does not hide later split error",
			raw:   "movie:\n  Invalid:\n    genre_ids: '16'\n    original_language: true\n  Fallback: {}\n",
			media: catalog.Media{MediaType: "movie", OriginalLanguage: "zh"}, fail: true,
		},
		{
			name:  "matched earlier rule stops before invalid later rule",
			raw:   "movie:\n  First:\n    original_language: 'zh'\n  Invalid:\n    genre_ids: 16\n",
			media: catalog.Media{MediaType: "movie", OriginalLanguage: "zh", GenreIDs: []int64{16}}, want: "First",
		},
		{
			name:  "unconditional earlier rule stops before invalid later rule",
			raw:   "movie:\n  First: {}\n  Invalid:\n    genre_ids: 16\n",
			media: catalog.Media{MediaType: "movie", GenreIDs: []int64{16}}, want: "First",
		},
		{
			name:  "absent invalid field mismatches without raising",
			raw:   "movie:\n  Invalid:\n    genre_ids: 16\n    original_language: 'zh'\n  Fallback: {}\n",
			media: catalog.Media{MediaType: "movie", OriginalLanguage: "zh"}, want: "Fallback",
		},
	} {
		t.Run(sample.name, func(t *testing.T) {
			policy, _, err := categoryPluginReload([]byte(sample.raw))
			if err != nil {
				t.Fatal(err)
			}
			got, err := policy.categoryWithError(sample.media)
			if got != sample.want || (err != nil) != sample.fail {
				t.Fatalf("category=%q err=%v; want=%q failure=%t", got, err, sample.want, sample.fail)
			}
		})
	}
}

func TestCategoryEditorDeferredConditionPreservesInfoTruthiness(t *testing.T) {
	for _, sample := range []struct {
		name, field string
		media       catalog.Media
	}{
		{"origin list with empty element", "origin_country", catalog.Media{OriginCountry: []string{""}}},
		{"production list with empty element", "production_countries", catalog.Media{ProductionCountries: []catalog.Country{{}}}},
		{"genre list with zero", "genre_ids", catalog.Media{GenreIDs: []int64{0}}},
		{"truthy generic field", "status", catalog.Media{CategoryFields: map[string]catalog.CategoryField{"status": {Present: true}}}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			policy, _, err := categoryPluginReload([]byte("movie:\n  Deferred:\n    " + sample.field + ": true\n"))
			if err != nil {
				t.Fatal(err)
			}
			sample.media.MediaType = "movie"
			if _, err := policy.categoryWithError(sample.media); err == nil {
				t.Fatal("truthy info_value did not reach split")
			}
			absent := catalog.Media{MediaType: "movie", CategoryFields: map[string]catalog.CategoryField{sample.field: {Present: false}}}
			if got, err := policy.categoryWithError(absent); err != nil || got != "" {
				t.Fatalf("falsey info_value reached split: %q %v", got, err)
			}
		})
	}
	for _, expression := range []string{"false", "0", "0.0", "[]", "{}", "null"} {
		t.Run("falsey condition "+expression, func(t *testing.T) {
			policy, _, err := categoryPluginReload([]byte("movie:\n  Skipped:\n    genre_ids: " + expression + "\n"))
			if err != nil {
				t.Fatal(err)
			}
			if got, err := policy.categoryWithError(catalog.Media{ID: 123, MediaType: "movie", GenreIDs: []int64{16}}); err != nil || got != "Skipped" {
				t.Fatalf("source falsey value must be ignored: %q %v", got, err)
			}
		})
	}
}

func TestCategoryEditorDeferredConditionKeepsWarmAndColdMaps(t *testing.T) {
	const numeric = "movie:\n  Numeric:\n    genre_ids: 16\n"
	for _, later := range []string{"", "movie: [unterminated"} {
		t.Run(later, func(t *testing.T) {
			a := &App{Dir: t.TempDir()}
			path := filepath.Join(a.Dir, "category.yaml")
			if err := os.WriteFile(path, []byte(numeric), 0600); err != nil {
				t.Fatal(err)
			}
			for _, instance := range []*App{a, {Dir: a.Dir}} {
				policy, err := instance.currentMediaCategoryPolicy()
				if err != nil || len(policy["movie"]) != 1 || policy["movie"][0].name != "Numeric" {
					t.Fatalf("cold load rejected numeric map: %+v %v", policy, err)
				}
				if _, err := policy.categoryWithError(catalog.Media{MediaType: "movie", GenreIDs: []int64{16}}); err == nil {
					t.Fatal("cold load discarded deferred error")
				}
			}
			if err := os.WriteFile(path, []byte(later), 0600); err != nil {
				t.Fatal(err)
			}
			// This fixture models an explicit CategoryHelper.init after editing;
			// ordinary getters must not observe an external file edit.
			policy, err := a.reloadMediaCategoryPolicy()
			if err != nil || len(policy["movie"]) != 1 || policy["movie"][0].name != "Numeric" {
				t.Fatalf("empty/parser failure replaced warm map: %+v %v", policy, err)
			}
			if _, err := policy.categoryWithError(catalog.Media{MediaType: "movie", GenreIDs: []int64{16}}); err == nil {
				t.Fatal("warm retained map lost deferred error")
			}
			cold := &App{Dir: a.Dir}
			policy, err = cold.currentMediaCategoryPolicy()
			if err != nil || len(policy) != 0 {
				t.Fatalf("new empty/parser-failed object inherited warm map: %+v %v", policy, err)
			}
		})
	}
}

func TestCategoryEditorDeferredConditionDoesNotRelaxSafety(t *testing.T) {
	for _, raw := range []string{
		"movie:\n  '../escape':\n    genre_ids: 16\n",
		"movie:\n  '.hidden':\n    genre_ids: 16\n",
		"movie:\n  Deferred:\n    genre_ids: !custom 16\n",
		"movie:\n  Deferred:\n    genre_ids: [!custom 16]\n",
		"movie:\n  Deferred:\n    genre_ids: &loop [*loop]\n",
		"movie: &loop\n  <<: *loop\n",
		strings.Repeat("#", maxCategoryPolicyBytes+1),
		"movie:\n  Deferred:\n    genre_ids: " + strings.Repeat("[", 34) + "16" + strings.Repeat("]", 34) + "\n",
	} {
		if _, _, err := categoryPluginReload([]byte(raw)); err == nil {
			t.Fatalf("unsafe runtime input accepted: %q", raw[:min(len(raw), 120)])
		}
	}
}

func TestCategoryEditorDeferredConditionErrorsReachImportCaller(t *testing.T) {
	a := newTestApp(t)
	if err := os.WriteFile(filepath.Join(a.Dir, "category.yaml"), []byte("movie:\n  Numeric:\n    genre_ids: 16\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.reloadMediaCategoryPolicy(); err != nil {
		t.Fatal("explicit source helper init", err)
	}
	identity := catalog.Media{ID: 123, MediaType: "movie", GenreIDs: []int64{16}, CategoryMetadataKnown: true}
	got, category, err := a.importMediaCategory(context.Background(), identity)
	if err == nil || category != "" || got.ID != identity.ID {
		t.Fatalf("import caller swallowed deferred error: %+v %q %v", got, category, err)
	}
	identity.GenreIDs = nil
	if _, category, err := a.importMediaCategory(context.Background(), identity); err != nil || category != "" {
		t.Fatalf("import caller raised for absent info_value: %q %v", category, err)
	}
}

func TestCategoryEditorDeferredConditionErrorsReachRecognitionCaller(t *testing.T) {
	a := newTestApp(t)
	path := filepath.Join(a.Dir, "category.yaml")
	if err := os.WriteFile(path, []byte("movie:\n  Numeric:\n    genre_ids: 16\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.reloadMediaCategoryPolicy(); err != nil {
		t.Fatal("explicit source helper init", err)
	}
	settings := a.Settings()
	settings.TMDB = TMDBConfig{APIKey: "synthetic-deferred-condition-key"}
	a.config = settings
	proxy, err := a.proxyConfig()
	if err != nil {
		t.Fatal(err)
	}
	a.catCreds, a.catProxy = settings.TMDB, proxy
	calls := 0
	a.catClient = catalog.NewWithTransport(catalog.Credentials{APIKey: settings.TMDB.APIKey}, imageTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Scheme != "https" || request.URL.Host != "api.themoviedb.org" || request.URL.Path != "/3/movie/123" {
			t.Fatal("recognition left the fixed TMDB origin")
		}
		body := `{"id":123,"title":"owned category test","original_title":"owned category test","release_date":"2024-01-01","genres":[{"id":16,"name":"Animation"}]}`
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	for i, content := range []string{"movie:\n  Numeric:\n    genre_ids: 16\n", ""} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := a.reloadMediaCategoryPolicy(); err != nil {
			t.Fatal("explicit source helper init", err)
		}
		out, err := a.sourceRecognizeTMDBCore(context.Background(), catalog.Media{ID: 123, MediaType: "movie"})
		if err == nil || out.MediaInfo != nil || calls != i+1 {
			t.Fatalf("recognition did not fetch then expose retained matching error: calls=%d output=%+v error=%v", calls, out, err)
		}
	}
}
