package app

// This supplements the hash-bound archived-source differential with the actual
// standard executable, authenticated App routes and SQLite. Inputs remain owned
// synthetic fixtures, not current NAS configurations or browser acceptance.
import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/catalog"
)

func TestCategoryEditorStandardWarmRootFailureKeepsConsumerAvailable(t *testing.T) {
	categoryRuntimeArchivedSource(t)
	warm := categoryEditorStandardWarm
	f := newCategoryEditorStandardFixture(t, &warm)
	config := map[string]any{"enabled": true, "content": "42\n", "retained_on_failure": true}
	raw, _ := json.Marshal(config)
	response := callTest(t, f.app.Handler(), "PUT", "/api/plugins/CategoryEditor", string(raw), f.cookie, "")
	f.app.wg.Wait()
	if response.Code < 400 {
		t.Fatal("source truthy-root .get failure disappeared", response.Code)
	}
	f.bytes(t, "42\n")
	f.config(t, categoryEditorStandardID, config)
	var state json.RawMessage
	if err := f.object(t, categoryEditorStandardID).readSession(context.Background(), "get_state", &state); err != nil || string(state) != "true" {
		t.Fatal("failed helper incorrectly disabled the actual worker", string(state), err)
	}
	f.noNotification(t)
	// Archived helper getters keep prior maps despite a failed subsequent init.
	f.categories(t, "缓存电影", "缓存剧集")
	f.save(t, categoryEditorStandardID, map[string]any{"enabled": true, "content": "movie:\n  Recovered:\n"})
	f.disabled(t, categoryEditorStandardID, "movie:\n  Recovered:\n")
	f.notification(t)
	f.categories(t, "Recovered", "")
}

func TestCategoryEditorStandardWriteOnlyReadFailureStillCompletes(t *testing.T) {
	categoryRuntimeArchivedSource(t)
	warm := categoryEditorStandardWarm
	f := newCategoryEditorStandardFixture(t, &warm)
	path := filepath.Join(f.app.Dir, "category.yaml")
	if err := os.Chmod(path, 0200); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })
	if _, err := os.ReadFile(path); !errors.Is(err, os.ErrPermission) {
		t.Fatal("requires a real owned-file read permission failure, not a skip", err)
	}
	const content = "movie:\n  New:\n"
	f.save(t, categoryEditorStandardID, map[string]any{"enabled": true, "content": content, "discard": true})
	f.disabled(t, categoryEditorStandardID, content)
	f.notification(t)
	f.categories(t, "缓存电影", "缓存剧集")
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	f.bytes(t, content)
	// Making the file readable does not implicitly perform CategoryHelper.init.
	f.categories(t, "缓存电影", "缓存剧集")
	f.coldReopen(t)
	f.categories(t, "New", "")
	f.disabled(t, categoryEditorStandardID, content)
	f.noNotification(t)
}

func TestCategoryEditorStandardDeferredShapesUnicodeAndYAML(t *testing.T) {
	categoryRuntimeArchivedSource(t)
	// Expected values and failure phases are independently recorded in
	// categoryeditor-review-source-extra-oracle.json, not target-generated.
	for _, sample := range []struct {
		id, content, want string
		matchError        bool
	}{
		{"section_scalar", "movie: 5\n", "", true},
		{"rule_scalar", "movie:\n  ScalarRule: 5\n", "", true},
		{"unicode_decimal_range", "movie:\n  ArabicRange:\n    genre_ids: '١-٣'\n  Fallback:\n", "ArabicRange", false},
		{"unicode_superscript_range", "movie:\n  SuperscriptRange:\n    genre_ids: '²-³'\n  Fallback:\n", "", true},
		{"yaml11_boolean_condition", "%YAML 1.1\n---\nmovie:\n  LegacyOn:\n    original_language: on\n", "", true},
		{"duplicate_true_and_one", "movie:\n  true:\n  1:\n", "缓存电影", false},
	} {
		t.Run(sample.id, func(t *testing.T) {
			warm := categoryEditorStandardWarm
			f := newCategoryEditorStandardFixture(t, &warm)
			f.save(t, categoryEditorStandardID, map[string]any{"enabled": true, "content": sample.content, "discard": true})
			f.bytes(t, sample.content)
			f.disabled(t, categoryEditorStandardID, sample.content)
			f.notification(t)
			_, got, err := f.app.importMediaCategory(context.Background(), catalog.Media{ID: 303, MediaType: "movie", OriginalLanguage: "zh", GenreIDs: []int64{2}, CategoryMetadataKnown: true})
			if got != sample.want || (err != nil) != sample.matchError {
				t.Fatalf("source match phase differs: got=%q err=%v want=%q error=%v", got, err, sample.want, sample.matchError)
			}
		})
	}
}
