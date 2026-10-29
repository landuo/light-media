package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/catalog"
)

func TestDefaultMediaCategoryRulesMatchMoviePilotOrder(t *testing.T) {
	policy, err := parseMediaCategoryPolicy([]byte(defaultMediaCategoryPolicy))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, kind, language, country, want string
		genres                              []int64
	}{
		{"animation before Chinese", "movie", "zh", "CN", "动画电影", []int64{16}},
		{"Chinese", "movie", "zh", "CN", "华语电影", []int64{18}},
		{"Cantonese", "movie", "cn", "HK", "华语电影", []int64{18}},
		{"Tibetan", "movie", "bo", "CN", "华语电影", nil},
		{"Zhuang", "movie", "za", "CN", "华语电影", nil},
		{"foreign despite Chinese display title", "movie", "en", "US", "外语电影", []int64{18}},
		{"Chinese animation", "tv", "zh", "CN", "国漫", []int64{16, 99}},
		{"Taiwan animation", "tv", "zh", "TW", "国漫", []int64{16}},
		{"Japanese animation", "tv", "ja", "JP", "日番", []int64{16, 10762}},
		{"documentary before drama", "tv", "zh", "CN", "纪录片", []int64{99}},
		{"kids before variety", "tv", "en", "US", "儿童", []int64{10762, 10764}},
		{"reality", "tv", "zh", "CN", "综艺", []int64{10764}},
		{"talk", "tv", "en", "US", "综艺", []int64{10767}},
		{"domestic", "tv", "zh", "HK", "国产剧", []int64{18}},
		{"western", "tv", "en", "GB", "欧美剧", []int64{18}},
		{"western UK compatibility", "tv", "en", "UK", "欧美剧", nil},
		{"Korean", "tv", "ko", "KR", "日韩剧", []int64{18}},
		{"Thai follows original policy", "tv", "th", "TH", "日韩剧", []int64{18}},
		{"unmatched", "tv", "es", "MX", "未分类", []int64{18}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identity := catalog.Media{MediaType: tc.kind, Title: "显示为中文的标题", OriginalLanguage: tc.language, OriginCountry: []string{tc.country}, GenreIDs: tc.genres}
			if got := policy.category(identity); got != tc.want {
				t.Fatalf("category=%q want=%q", got, tc.want)
			}
		})
	}
}

const customJapaneseMovieCategory = `movie:
  日番动漫电影:
    genre_ids: '16'
    origin_country: 'JP'
  动画电影:
    genre_ids: '16'
  华语电影:
    original_language: 'zh,cn,bo,za'
  外语电影:
`

func TestCustomMediaCategoryPreservesFileOrderAndConditionSemantics(t *testing.T) {
	policy, err := parseMediaCategoryPolicy([]byte(customJapaneseMovieCategory))
	if err != nil {
		t.Fatal(err)
	}
	identity := catalog.Media{MediaType: "movie", OriginalLanguage: "ja", OriginCountry: []string{"JP"}, Genres: []catalog.Genre{{ID: 16}}}
	if category := policy.category(identity); category != "日番动漫电影" {
		t.Fatal("user's first matching category lost precedence", category)
	}
	identity.OriginCountry = []string{"CN"}
	if category := policy.category(identity); category != "动画电影" {
		t.Fatal("AND condition treated a non-Japanese animation as Japanese", category)
	}
	policy, err = parseMediaCategoryPolicy([]byte(`movie:
  近年华语:
    original_language: 'zh,cn,!en'
    production_countries: 'CN,TW,!US'
    release_year: '2020-2026,!2025'
  其余:
`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		language  string
		countries []catalog.Country
		year      int
		want      string
	}{
		{"zh", []catalog.Country{{ISO3166_1: "CN"}}, 2026, "近年华语"},
		{"cn", []catalog.Country{{ISO3166_1: "TW"}}, 2020, "近年华语"},
		{"zh", []catalog.Country{{ISO3166_1: "CN"}, {ISO3166_1: "US"}}, 2026, "其余"},
		{"en", []catalog.Country{{ISO3166_1: "CN"}}, 2026, "其余"},
		{"zh", []catalog.Country{{ISO3166_1: "CN"}}, 2025, "其余"},
		{"zh", []catalog.Country{{ISO3166_1: "CN"}}, 2019, "其余"},
		{"zh", nil, 2026, "其余"},
	} {
		if got := policy.category(catalog.Media{MediaType: "movie", OriginalLanguage: tc.language, ProductionCountries: tc.countries, Year: tc.year}); got != tc.want {
			t.Fatalf("conditions=%+v category=%q", tc, got)
		}
	}
}

func TestMediaCategoryPolicyRejectsUnsafeOrUnsupportedConfiguration(t *testing.T) {
	for _, raw := range []string{
		"movie: [bad]",
		"movie: {}\nmovie: {}",
		"movie:\n  ../逃逸: {}",
		"movie:\n  '电影/子目录': {}",
		"movie:\n  '.隐藏目录': {}",
		"movie:\n  重复: {}\n  重复: {}",
		"movie:\n  条件:\n    genre_ids: '16'\n    genre_ids: '99'",
		"movie:\n  条件:\n    genre_ids: 16",
		"movie:\n  条件:\n    original_language: ['zh']",
		"movie: &loop\n  <<: *loop",
		"movie: {}\n---\ntv: {}",
		strings.Repeat("#", maxCategoryPolicyBytes+1),
	} {
		if _, err := parseMediaCategoryPolicy([]byte(raw)); err == nil {
			t.Fatalf("unsafe category configuration was accepted: %q", raw[:min(len(raw), 100)])
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "category.yaml"), []byte(customJapaneseMovieCategory), 0600); err != nil {
		t.Fatal(err)
	}
	policy, err := loadMediaCategoryPolicy(dir)
	if err != nil || policy["movie"][0].name != "日番动漫电影" {
		t.Fatal("custom policy was not loaded", err)
	}
}

func TestFullCustomMediaCategoryKeepsJapaneseMoviePriorityAndTVDefaults(t *testing.T) {
	// Same customization and order as the user's MoviePilot category.yaml:
	// Japanese animated movies precede the remaining bundled rules.
	raw := strings.Replace(defaultMediaCategoryPolicy, "movie:\n", "movie:\n  日番动漫电影:\n    genre_ids: '16'\n    origin_country: 'JP'\n", 1)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "category.yaml"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	policy, err := loadMediaCategoryPolicy(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind, language, country, want string
		genres                        []int64
	}{
		{"movie", "ja", "JP", "日番动漫电影", []int64{16}},
		{"movie", "zh", "CN", "动画电影", []int64{16}},
		{"movie", "zh", "CN", "华语电影", []int64{18}},
		{"tv", "zh", "CN", "国漫", []int64{16}},
		{"tv", "ja", "JP", "日番", []int64{16}},
		{"tv", "zh", "CN", "纪录片", []int64{99}},
	} {
		if got := policy.category(catalog.Media{MediaType: tc.kind, OriginalLanguage: tc.language, OriginCountry: []string{tc.country}, GenreIDs: tc.genres}); got != tc.want {
			t.Fatalf("full custom policy changed original order: got=%q want=%q", got, tc.want)
		}
	}
}

func useCategoryDetailResponse(t *testing.T, a *App, status int, body map[string]any) *int {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	installMetadataFixture(t, a, "synthetic-category-key", []string{"/3/movie/123"}, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Scheme != "https" || request.URL.Host != "api.themoviedb.org" || request.URL.Path != "/3/movie/123" {
			t.Fatal("category refresh left the official detail origin")
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	return &calls
}

func categoryMovieDetail() map[string]any {
	return map[string]any{
		"id": 123, "title": "测试影片", "original_title": "Test Film", "overview": "分类测试",
		"release_date": "2024-01-01", "genres": []map[string]any{{"id": 18, "name": "剧情"}},
		"original_language": "zh", "origin_country": []string{"CN"}, "production_countries": []catalog.Country{{ISO3166_1: "CN", Name: "China"}},
		"credits": map[string]any{"cast": []any{}}, "recommendations": map[string]any{"results": []any{}},
	}
}

func TestMediaCategoryRefreshesLegacyCacheBeforePlanning(t *testing.T) {
	a, source, target := importTestApp(t)
	identity := importFixture(t, a, "movie")
	identity.CategoryMetadataKnown, identity.OriginalLanguage = false, ""
	identity.OriginCountry, identity.ProductionCountries = nil, nil
	importCache(t, a, "media:movie:123", identity)
	calls := useCategoryDetailResponse(t, a, 200, categoryMovieDetail())
	path := filepath.Join(source, "Test Film (2024).mkv")
	importWrite(t, path, []byte("classification refresh fixture"))
	request := ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: path}}, LibraryTypeFolder: importFlag(true)}
	for i := 0; i < 2; i++ {
		plan, err := a.previewImport(context.Background(), request)
		if err != nil || len(plan.Files) != 1 || !strings.HasPrefix(plan.Files[0].Target, filepath.Join(target, "华语电影")+string(filepath.Separator)) || !plan.Media.CategoryMetadataKnown {
			t.Fatalf("legacy metadata classified without refreshing reliable fields: %+v %v", plan, err)
		}
	}
	if *calls != 1 {
		t.Fatal("refreshed category metadata was not cached", *calls)
	}
	cached, err := a.media(context.Background(), "movie", 123)
	if err != nil || !cached.CategoryMetadataKnown || cached.OriginalLanguage != "zh" {
		t.Fatal("cache was not upgraded", err)
	}
}

func TestMediaCategoryRefreshFailureDoesNotClassifyLegacyUnknownLanguage(t *testing.T) {
	t.Run("upstream failure", func(t *testing.T) {
		a, source, _ := importTestApp(t)
		identity := importFixture(t, a, "movie")
		identity.CategoryMetadataKnown, identity.OriginalLanguage = false, ""
		importCache(t, a, "media:movie:123", identity)
		body := categoryMovieDetail()
		delete(body, "original_language")
		useCategoryDetailResponse(t, a, 503, body)
		path := filepath.Join(source, "Test Film (2024).mkv")
		importWrite(t, path, []byte("incomplete metadata fixture"))
		_, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: path}}, LibraryTypeFolder: importFlag(true)})
		if err == nil {
			t.Fatal("missing language was silently classified as a foreign film")
		}
		var plans int
		if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='import_plan'`).Scan(&plans); err != nil || plans != 0 {
			t.Fatal("incomplete classification created an execution plan", err)
		}
	})
}

func TestMediaCategoryFreshPartialMetadataFollowsMoviePilotFallback(t *testing.T) {
	for _, tc := range []struct {
		genre int64
		want  string
	}{
		{16, "动画电影"},
		{18, "外语电影"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			a, source, target := importTestApp(t)
			identity := importFixture(t, a, "movie")
			identity.CategoryMetadataKnown, identity.OriginalLanguage = false, ""
			importCache(t, a, "media:movie:123", identity)
			body := categoryMovieDetail()
			body["genres"] = []map[string]any{{"id": tc.genre}}
			delete(body, "original_language")
			delete(body, "origin_country")
			delete(body, "production_countries")
			calls := useCategoryDetailResponse(t, a, 200, body)
			path := filepath.Join(source, "Test Film (2024).mkv")
			importWrite(t, path, []byte("partial category detail fixture"))
			plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: path}}, LibraryTypeFolder: importFlag(true)})
			if err != nil || len(plan.Files) != 1 || !strings.HasPrefix(plan.Files[0].Target, filepath.Join(target, tc.want)+string(filepath.Separator)) || !plan.Media.CategoryMetadataKnown || *calls != 1 {
				t.Fatalf("fresh detail did not follow original absent-field/fallback semantics: %+v %v", plan, err)
			}
		})
	}
}

func TestDisabledMediaCategoryRetainsTargetAndDoesNotRequireNewMetadata(t *testing.T) {
	a, source, target := importTestApp(t)
	identity := importFixture(t, a, "movie")
	identity.CategoryMetadataKnown, identity.OriginalLanguage = false, ""
	importCache(t, a, "media:movie:123", identity)
	if err := os.WriteFile(filepath.Join(a.Dir, "category.yaml"), []byte("invalid policy"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, "Test Film (2024).mkv")
	importWrite(t, path, []byte("unclassified fixture"))
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: path}}, LibraryTypeFolder: importFlag(false)})
	if err != nil || plan.Files[0].Target != filepath.Join(target, "测试影片 (2024)", "测试影片 (2024).mkv") {
		t.Fatalf("disabled category changed the original target or fetched metadata: %+v %v", plan, err)
	}
}

func TestCustomMediaCategoryPlanPublishesFrozenHardlink(t *testing.T) {
	a, source, target := importTestApp(t)
	identity := importFixture(t, a, "movie")
	identity.OriginalLanguage, identity.OriginCountry = "ja", []string{"JP"}
	identity.ProductionCountries = []catalog.Country{{ISO3166_1: "JP", Name: "Japan"}}
	identity.Genres = []catalog.Genre{{ID: 16, Name: "动画"}}
	importCache(t, a, "media:movie:123", identity)
	policyPath := filepath.Join(a.Dir, "category.yaml")
	if err := os.WriteFile(policyPath, []byte(customJapaneseMovieCategory), 0600); err != nil {
		t.Fatal(err)
	}
	// Source CategoryHelper observes file edits only through explicit init.
	if _, err := a.reloadMediaCategoryPolicy(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, "Test Film (2024).mkv")
	importWrite(t, path, []byte("frozen Japanese category fixture"))
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: path}}, LibraryTypeFolder: importFlag(true)})
	want := filepath.Join(target, "日番动漫电影", "测试影片 (2024)", "测试影片 (2024).mkv")
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Target != want {
		t.Fatalf("custom category not reflected in preview: %+v %v", plan, err)
	}
	if err := os.WriteFile(policyPath, []byte("movie:\n  后续新分类:\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.reloadMediaCategoryPolicy(); err != nil {
		t.Fatal(err)
	}
	job, err := a.executeImport(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, job)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	linked, err := os.Stat(want)
	if err != nil || !os.SameFile(info, linked) {
		t.Fatal("execution did not publish the frozen classified hardlink", err)
	}
	for _, forbidden := range []string{"电影", "电视剧", "后续新分类"} {
		if _, err := os.Stat(filepath.Join(target, forbidden)); !os.IsNotExist(err) {
			t.Fatal("execution created a type wrapper or reclassified an existing plan", forbidden, err)
		}
	}
}
