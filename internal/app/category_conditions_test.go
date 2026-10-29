package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/catalog"
)

func TestCategoryConditionsSupportOriginalRangesWithoutExpansion(t *testing.T) {
	for _, tc := range []struct {
		expression string
		values     []string
		want       bool
	}{
		{"0-100000000000000000000000000000000000000", []string{"2026"}, true},
		{"0-100,!40-60", []string{"50"}, false},
		{"1-4", []string{"001"}, false},
		{"001", []string{"001"}, true},
		{"US-GB", []string{"GB"}, true},
		{"US-GB", []string{"CA"}, false},
		{"!US-GB", []string{"GB"}, false},
		{"!US-GB", []string{"CN"}, true},
		{"zh, en", []string{"en"}, false},
		{"1-0", []string{"99"}, true},
		{",,", []string{"99"}, true},
		{"ß", []string{"SS"}, true},
		{"!US", nil, false},
	} {
		condition, err := parseMediaCategoryCondition("generic", tc.expression)
		if err != nil || condition.matches(tc.values, catalog.CategoryField{}) != tc.want {
			t.Fatal(tc, err)
		}
	}
}

func TestCategoryAliasesAndMergesMatchOriginalIterationOrder(t *testing.T) {
	policy, err := parseMediaCategoryPolicy([]byte(`base: &base
  已继承:
    original_language: 'en'
  继承兜底: {}
movie:
  <<: *base
  本地优先:
    original_language: 'en'
  已继承:
    original_language: 'zh'
tv: []
`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ language, want string }{{"en", "本地优先"}, {"zh", "已继承"}, {"ja", "继承兜底"}} {
		if got := policy.category(catalog.Media{MediaType: "movie", OriginalLanguage: tc.language}); got != tc.want {
			t.Fatal("ruamel merge/override order changed", tc, got)
		}
	}
	for _, raw := range []string{"", "null", "movie: []", "movie: null", "movie: 0x0", "movie: 0o0", "movie: 0b0"} {
		if policy, err := parseMediaCategoryPolicy([]byte(raw)); err != nil || policy.category(catalog.Media{MediaType: "movie", OriginalLanguage: "en"}) != "" {
			t.Fatal("original empty policy rejected", raw, err)
		}
	}
}

func TestCategoryGenericFieldRefreshChangesActualOrganizePathAndCachesAbsence(t *testing.T) {
	a, source, target := importTestApp(t)
	raw := `movie:
  已上映大片:
    status: 'Released'
    budget: '20000000-5000000000000000000'
  未知字段:
    nonexistent: '!x'
  其它: {}
`
	if err := os.WriteFile(filepath.Join(a.Dir, "category.yaml"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	// CategoryHelper applies an externally edited file only on explicit init;
	// ordinary consumers retain the process-local maps between reloads.
	if _, err := a.reloadMediaCategoryPolicy(); err != nil {
		t.Fatal(err)
	}
	detail := categoryMovieDetail()
	detail["status"], detail["budget"] = "Released", 30000000
	calls := useCategoryDetailResponse(t, a, 200, detail)
	identity := importFixture(t, a, "movie")
	fresh, category, err := a.importMediaCategory(context.Background(), identity)
	if err != nil || category != "已上映大片" || *calls != 1 || len(fresh.CategoryFields) != 3 {
		t.Fatal("additional TMDB fields were not used", category, len(fresh.CategoryFields), *calls, err)
	}
	_, second, err := a.importMediaCategory(context.Background(), fresh)
	if err != nil || second != category || *calls != 1 {
		t.Fatal("known absent metadata triggered repeat fetch", second, *calls, err)
	}
	// The existing plan path builder must consume the category result too.
	importCache(t, a, "media:movie:123", fresh)
	path := filepath.Join(source, "Test Film (2024).mkv")
	importWrite(t, path, []byte("generic category hardlink fixture"))
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: path}}, LibraryTypeFolder: importFlag(true)})
	if err != nil || len(plan.Files) != 1 || !strings.HasPrefix(plan.Files[0].Target, filepath.Join(target, "已上映大片")+string(filepath.Separator)) {
		t.Fatal("generic field did not affect actual organize preview", err)
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
	linked, err := os.Stat(plan.Files[0].Target)
	if err != nil || !os.SameFile(info, linked) {
		t.Fatal("generic category did not publish a real classified hardlink", err)
	}
	serialized, _ := json.Marshal(fresh)
	if strings.Contains(string(serialized), `"budget":30000000`) {
		t.Fatal("unrelated full provider response was cached")
	}
}
