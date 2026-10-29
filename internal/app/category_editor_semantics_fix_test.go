package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/catalog"
)

type categorySemanticsSourceValue struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

type categorySemanticsSourceCall struct {
	Return    *categorySemanticsSourceValue   `json:"return"`
	Exception *struct{ Type, Message string } `json:"exception"`
}

// These are independently recorded archived-helper observations, not expected
// values calculated with the Go implementation. The helper is dated 2026-10-20;
// no test here asserts that it matches the current NAS helper or actual config.
func TestCategoryEditorSemanticsMatchesArchivedReviewOracle(t *testing.T) {
	readBound := func(path, hash string, out any) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("../..", path))
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != hash {
			t.Fatalf("source evidence changed: %s hash=%s", path, got)
		}
		if out != nil {
			if err := json.Unmarshal(raw, out); err != nil {
				t.Fatal(err)
			}
		}
	}
	readBound("testdata/plugins/category-editor-1.3/installed-category-helper.py", "1c0ec53b78caa3a4949dd970931f38430c67c9d46ea2e16c3cc00950fb24c2b2", nil)
	var inputs struct {
		Cases []struct {
			ID            string          `json:"id"`
			ContentBase64 string          `json:"content_base64"`
			Media         json.RawMessage `json:"media"`
		} `json:"cases"`
	}
	var original struct {
		Cases []struct {
			ID   string `json:"id"`
			Warm struct {
				Call    categorySemanticsSourceCall            `json:"call"`
				Lookups map[string]categorySemanticsSourceCall `json:"lookups"`
			} `json:"warm"`
			Cold struct {
				Call    categorySemanticsSourceCall            `json:"call"`
				Lookups map[string]categorySemanticsSourceCall `json:"lookups"`
			} `json:"cold"`
		} `json:"cases"`
	}
	var base struct {
		InitialText string `json:"initial_text"`
	}
	readBound("migration/evidence/categoryeditor/review/categoryeditor-review-source-extra-inputs.json", "492e88e417dcb0ff3c5754ea030ba4fd5ab9cda594149c42cf498dfc74003454", &inputs)
	readBound("migration/evidence/categoryeditor/review/categoryeditor-review-source-extra-oracle.json", "148b80c44e632626d8379e786fee4603a1adefcc7ade9b9b2d6f07a6ffd6dbb5", &original)
	readBound("migration/evidence/categoryeditor/source/categoryeditor-source-helper-inputs.json", "a9224f02261b09a20d780099463201b74d198a2370e68bec58290f39a874c780", &base)
	selected := map[string]bool{
		"section_scalar": true, "rule_scalar": true,
		"unicode_decimal_range": true, "unicode_superscript_range": true,
		"yaml11_boolean_condition": true, "duplicate_true_and_one": true,
		"truthy_empty_string_list_string_condition": true,
	}
	seen := 0
	for _, input := range inputs.Cases {
		if !selected[input.ID] {
			continue
		}
		seen++
		index := -1
		for i, recorded := range original.Cases {
			if recorded.ID == input.ID {
				index = i
				break
			}
		}
		if index < 0 {
			t.Fatalf("missing original observation %s", input.ID)
		}
		for _, warm := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/warm=%t", input.ID, warm), func(t *testing.T) {
				recorded := original.Cases[index].Cold
				if warm {
					recorded = original.Cases[index].Warm
				}
				a := &App{Dir: t.TempDir()}
				path := filepath.Join(a.Dir, "category.yaml")
				if warm {
					if err := os.WriteFile(path, []byte(base.InitialText), 0600); err != nil {
						t.Fatal(err)
					}
					if _, err := a.currentMediaCategoryPolicy(); err != nil {
						t.Fatal(err)
					}
				}
				raw, err := base64.StdEncoding.DecodeString(input.ContentBase64)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				policy, err := a.reloadMediaCategoryPolicy()
				if (err != nil) != (recorded.Call.Exception != nil) {
					t.Fatalf("init timing differs: target=%v source=%+v", err, recorded.Call)
				}
				if err != nil {
					return
				}
				names, err := policy.categoryNames("movie")
				list := recorded.Lookups["movie_categorys"]
				if (err != nil) != (list.Exception != nil) {
					t.Fatalf("keys timing differs: target=%v source=%+v", err, list)
				}
				if err == nil {
					var sourceNames []categorySemanticsSourceValue
					if list.Return == nil || json.Unmarshal(list.Return.Value, &sourceNames) != nil {
						t.Fatal("invalid source list observation")
					}
					want := []string{}
					for _, value := range sourceNames {
						var name string
						if value.Type != "str" || json.Unmarshal(value.Value, &name) != nil {
							t.Fatal("source category name is not a string")
						}
						want = append(want, name)
					}
					if !reflect.DeepEqual(names, want) {
						t.Fatalf("names=%v source=%v", names, want)
					}
				}
				var media catalog.Media
				if err := json.Unmarshal(input.Media, &media); err != nil {
					t.Fatal(err)
				}
				media.MediaType = "movie"
				got, err := policy.categoryWithError(media)
				match := recorded.Lookups["movie_match"]
				if (err != nil) != (match.Exception != nil) {
					t.Fatalf("match timing differs: target=%q %v source=%+v", got, err, match)
				}
				if err == nil {
					var want string
					if match.Return == nil || json.Unmarshal(match.Return.Value, &want) != nil || got != want {
						t.Fatalf("match=%q source=%+v", got, match)
					}
				}
			})
		}
	}
	if seen != len(selected) {
		t.Fatalf("selected %d source cases, expected %d", seen, len(selected))
	}
}

func TestCategoryEditorSemanticsDeferredShapesPreserveStrictParser(t *testing.T) {
	for _, value := range []string{"5", "true", "'text'", "[5]"} {
		for _, raw := range []string{"movie: " + value + "\n", "movie:\n  Rule: " + value + "\n"} {
			if _, err := parseMediaCategoryPolicy([]byte(raw)); err == nil {
				t.Fatalf("strict parser unexpectedly accepts %q", raw)
			}
			policy, kind, err := categoryPluginReload([]byte(raw))
			if err != nil || kind != categoryReplaceMaps {
				t.Fatalf("runtime init evaluated items: %q %v", raw, err)
			}
			if _, err := policy.categoryWithError(catalog.Media{ID: 1, MediaType: "movie"}); err == nil {
				t.Fatalf("runtime matcher did not evaluate items: %q", raw)
			}
		}
	}
	for _, raw := range []string{
		"movie:\n  First:\n  Bad: 5\n",
		"movie:\n  First:\n  Bad:\n    genre_ids: '²-³'\n",
	} {
		policy, _, err := categoryPluginReload([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := policy.categoryWithError(catalog.Media{ID: 1, MediaType: "movie", GenreIDs: []int64{2}}); got != "First" || err != nil {
			t.Fatalf("earlier rule must shield deferred failure: %s %v", got, err)
		}
	}
}

func TestCategoryEditorSemanticsPublicListAndImportErrorStages(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
		listError       bool
	}{
		{"section scalar", "movie: 5\n", `{}`, true},
		{"rule scalar", "movie:\n  Rule: 5\n", `{"电影":["Rule"],"电视剧":[]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			if err := os.WriteFile(filepath.Join(a.Dir, "category.yaml"), []byte(tc.raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := a.reloadMediaCategoryPolicy(); err != nil {
				t.Fatalf("init raised before public consumers: %v", err)
			}
			a.systemMessages.get("system")
			got, err := a.originalMediaCategories(context.Background())
			if err != nil || !originalBaseJSONEqual(got, json.RawMessage(tc.want)) {
				t.Fatalf("module category list result=%s err=%v", got, err)
			}
			message := a.systemMessages.get("system")
			if (len(message) != 0) != tc.listError {
				t.Fatalf("list keys failure was not reported at module boundary: %s", message)
			}
			identity := catalog.Media{ID: 123, MediaType: "movie", CategoryMetadataKnown: true}
			if _, category, err := a.importMediaCategory(context.Background(), identity); category != "" || err == nil {
				t.Fatalf("import swallowed deferred items failure: %q %v", category, err)
			}
		})
	}
}

func TestCategoryEditorSemanticsUnicodeRangeAndErrorTiming(t *testing.T) {
	for _, tc := range []struct {
		expression string
		genre      int64
		want       string
	}{
		{"١-٣", 2, "Rule"}, {"١-٣", 4, "Fallback"},
		{"!١-٣", 2, "Fallback"}, {"!١-٣", 4, "Rule"},
		{"００１-００３", 2, "Rule"}, {"𝟙-𝟛", 2, "Rule"},
		{"٣-١", 5, "Rule"}, {"٢", 2, "Fallback"},
	} {
		raw := "movie:\n  Rule:\n    genre_ids: '" + tc.expression + "'\n  Fallback:\n"
		policy, _, err := categoryPluginReload([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := policy.categoryWithError(catalog.Media{ID: 1, MediaType: "movie", GenreIDs: []int64{tc.genre}}); got != tc.want || err != nil {
			t.Fatalf("expression=%s genre=%d got=%q %v want=%q", tc.expression, tc.genre, got, err, tc.want)
		}
	}
	raw := "movie:\n  Bad:\n    original_language: 'xx'\n    genre_ids: '²-³'\n  Fallback:\n"
	policy, _, err := categoryPluginReload([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := policy.categoryWithError(catalog.Media{ID: 1, MediaType: "movie", OriginalLanguage: "zh"}); err != nil || got != "Fallback" {
		t.Fatalf("absent info must skip int conversion: %q %v", got, err)
	}
	if _, err := policy.categoryWithError(catalog.Media{ID: 1, MediaType: "movie", OriginalLanguage: "zh", GenreIDs: []int64{2}}); err == nil {
		t.Fatal("prior mismatch incorrectly suppressed int conversion error")
	}
	strict, err := parseMediaCategoryPolicy([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := strict.categoryWithError(catalog.Media{ID: 1, MediaType: "movie", GenreIDs: []int64{2}}); got != "Fallback" || err != nil {
		t.Fatalf("strict parser preexisting behavior changed: %q %v", got, err)
	}
}

func TestCategoryEditorSemanticsYAML11BooleansAndPythonKeyCollision(t *testing.T) {
	for _, word := range []string{"y", "Y", "yes", "Yes", "YES", "on", "On", "ON"} {
		raw := "%YAML 1.1\n---\nmovie:\n  Rule:\n    original_language: " + word + "\n"
		policy, _, err := categoryPluginReload([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := policy.categoryWithError(catalog.Media{ID: 1, MediaType: "movie", OriginalLanguage: "zh"}); err == nil {
			t.Fatalf("YAML 1.1 %s was not a deferred bool", word)
		}
	}
	for _, word := range []string{"n", "N", "no", "No", "NO", "off", "Off", "OFF"} {
		raw := "%YAML 1.1\n---\nmovie:\n  Rule:\n    original_language: " + word + "\n"
		policy, _, err := categoryPluginReload([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := policy.categoryWithError(catalog.Media{ID: 1, MediaType: "movie"}); got != "Rule" || err != nil {
			t.Fatalf("YAML 1.1 %s was not skipped: %s %v", word, got, err)
		}
	}
	for _, spelling := range []string{"'on'", `"on"`, "!!str on"} {
		raw := "%YAML 1.1\n---\nmovie:\n  Rule:\n    original_language: " + spelling + "\n"
		policy, _, err := categoryPluginReload([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := policy.categoryWithError(catalog.Media{ID: 1, MediaType: "movie", OriginalLanguage: "on"}); got != "Rule" || err != nil {
			t.Fatalf("quoted/tagged string incorrectly resolved: %q %v", got, err)
		}
	}
	for _, raw := range []string{
		"movie:\n  true:\n  1:\n", "movie:\n  false:\n  0:\n",
		"movie:\n  true:\n  TRUE:\n", "%YAML 1.1\n---\nmovie:\n  on:\n  1:\n",
	} {
		if _, kind, err := categoryPluginReload([]byte(raw)); kind != categoryKeepParseFailure || err != nil {
			t.Fatalf("Python-equal duplicate keys must be swallowed parser failure: %q kind=%v err=%v", raw, kind, err)
		}
	}
	// Runtime fixes do not remove the already-declared safety boundaries.
	for _, raw := range []string{
		"movie:\n  '../escape': 5\n", "movie: !custom 5\n", "movie: &m [*m]\n",
		"%YAML 1.1\n---\nextra: " + strings.Repeat("[", 34) + "on" + strings.Repeat("]", 34) + "\n",
		strings.Repeat("#", maxCategoryPolicyBytes+1),
	} {
		if _, _, err := categoryPluginReload([]byte(raw)); err == nil {
			t.Fatalf("safety limit relaxed: %q", raw[:min(len(raw), 100)])
		}
	}
}
