package app

// Archived CategoryHelper 2026-10-20 + installed CategoryEditor 1.3 source
// regressions. Real owned file IO and the actual Go worker Handle/resource
// implementations are exercised; update_config is a recorder, not SQLite or
// stdio/browser acceptance. Current NAS helper identity is not asserted.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
	"light-media/internal/plugins/categoryeditor"
)

func categoryRuntimeArchivedSource(t *testing.T) {
	t.Helper()
	for path, want := range map[string]string{
		"../../testdata/plugins/category-editor-1.3/installed-category-helper.py":                     "1c0ec53b78caa3a4949dd970931f38430c67c9d46ea2e16c3cc00950fb24c2b2",
		"../../testdata/plugins/category-editor-1.3/installed-categoryeditor.py":                      "24a8109b5c15bb73b845e42653933d09eb83b608a53303fbab9ebff39b801c27",
		"../../migration/evidence/categoryeditor/source/categoryeditor-source-helper-oracle.json":     "9fa95d762b18da0ee90973f4b494bf508a4ce68546834d11d214795df66d78a6",
		"../../migration/evidence/categoryeditor/review/categoryeditor-review-filesystem-source.json": "9691bc9e4251f7b4a545f350275bacb962cadc2ce60762e42ee30d948a4e5da9",
	} {
		raw, err := os.ReadFile(path)
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != want {
			t.Fatalf("archived source/oracle binding changed: %s: %v", path, err)
		}
	}
}

func categoryRuntimeWarmApp(t *testing.T) *App {
	t.Helper()
	a := &App{Dir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(a.Dir, "category.yaml"), []byte(categoryEditorStandardWarm), 0600); err != nil {
		t.Fatal(err)
	}
	categoryRuntimeMatch(t, a, "缓存电影")
	return a
}

func categoryRuntimeMatch(t *testing.T, a *App, want string) {
	t.Helper()
	policy, err := a.currentMediaCategoryPolicy()
	if err != nil {
		t.Fatal("cached getter unexpectedly reloaded or failed", err)
	}
	got, err := policy.categoryWithError(catalog.Media{ID: 1, MediaType: "movie", OriginalLanguage: "zh", CategoryMetadataKnown: true})
	if err != nil || got != want {
		t.Fatalf("cached match=%q error=%v; want %q", got, err, want)
	}
}

func categoryRuntimeApply(t *testing.T, a *App, content string, fail bool) {
	t.Helper()
	instance := categoryeditor.New()
	calls := []string{}
	var saved, noticePayload json.RawMessage
	a.systemMessages.mu.Lock()
	mayDeduplicate := len(a.systemMessages.recent) != 0
	a.systemMessages.mu.Unlock()
	scope := originalBaseCallScope{validate: func(context.Context) error { return nil }}
	host := func(ctx context.Context, method string, payload json.RawMessage) (json.RawMessage, error) {
		calls = append(calls, method)
		if method == plugins.BaseUpdateConfig {
			saved = append(json.RawMessage(nil), payload...)
			return json.RawMessage("true"), nil
		}
		if method == plugins.SystemMessagePutCapability {
			noticePayload = append(json.RawMessage(nil), payload...)
		}
		return a.categoryEditorResource(ctx, scope, method, payload)
	}
	config, err := json.Marshal(map[string]any{"enabled": true, "content": content})
	if err != nil {
		t.Fatal(err)
	}
	_, err = instance.Handle(context.Background(), "init", config, host)
	if (err != nil) != fail {
		t.Fatalf("apply error=%v; source failure=%t", err, fail)
	}
	state, stateErr := instance.Handle(context.Background(), "get_state", nil, host)
	if stateErr != nil || string(state) != fmt.Sprint(fail) {
		t.Fatalf("worker auto-disable mismatch: %s %v", state, stateErr)
	}
	wantCalls := []string{plugins.CategoryApplyCapability}
	if !fail {
		wantCalls = append(wantCalls, plugins.SystemMessagePutCapability, plugins.BaseUpdateConfig)
	}
	if !slices.Equal(calls, wantCalls) {
		t.Fatalf("source write/helper/notice/save order differs: %v", calls)
	}
	message := a.systemMessages.get("plugin")
	if fail {
		if len(saved) != 0 || len(message) != 0 {
			t.Fatalf("failed helper reached notice/config: saved=%s message=%s", saved, message)
		}
		return
	}
	var input struct {
		Config json.RawMessage `json:"config"`
	}
	wantConfig, _ := json.Marshal(map[string]any{"enabled": false, "content": content})
	if json.Unmarshal(saved, &input) != nil || !originalBaseJSONEqual(input.Config, wantConfig) {
		t.Fatalf("source config replacement differs: %s", saved)
	}
	wantNotice, _ := json.Marshal(map[string]any{"message": categoryEditorStandardMessage, "title": "二级分类策略"})
	if !originalBaseJSONEqual(noticePayload, wantNotice) {
		t.Fatalf("source local notification call differs: %s", noticePayload)
	}
	// MessageHelper may suppress the repeated identical notice. The source
	// call and its exact arguments must still occur before update_config.
	if mayDeduplicate && len(message) == 0 {
		return
	}
	var notice systemMessage
	if json.Unmarshal(message, &notice) != nil || notice.Text != categoryEditorStandardMessage || notice.Title == nil || *notice.Title != "二级分类策略" {
		t.Fatalf("source local notification differs: %s", message)
	}
}

func TestCategoryEditorRuntimeArchivedTruthyRootRetainsGetterMaps(t *testing.T) {
	categoryRuntimeArchivedSource(t)
	// Expected timing/state comes from the hash-bound source helper oracle's
	// scalar_true/scalar_integer/scalar_string/nonempty_sequence warm cases.
	for name, content := range map[string]string{"scalar_true": "true\n", "scalar_integer": "42\n", "scalar_string": "'text'\n", "nonempty_sequence": "[a, b]\n"} {
		t.Run(name, func(t *testing.T) {
			a := categoryRuntimeWarmApp(t)
			categoryRuntimeApply(t, a, content, true)
			stored, err := os.ReadFile(filepath.Join(a.Dir, "category.yaml"))
			if err != nil || string(stored) != content {
				t.Fatal("failed helper must not roll back written source bytes", err)
			}
			categoryRuntimeMatch(t, a, "缓存电影")
			policy, err := a.currentMediaCategoryPolicy()
			if err != nil || len(policy["movie"]) != 2 || policy["movie"][0].name != "缓存电影" || policy["movie"][1].name != "其它电影" {
				t.Fatalf("list getter must retain maps after .get fails: %+v %v", policy, err)
			}
			// Explicit recovery, not a different digest observed by a getter.
			categoryRuntimeApply(t, a, "movie:\n  Recovered:\n", false)
			categoryRuntimeMatch(t, a, "Recovered")
		})
	}
}

func TestCategoryEditorRuntimeArchivedWriteOnlyStillNotifiesAndDisables(t *testing.T) {
	categoryRuntimeArchivedSource(t)
	a := categoryRuntimeWarmApp(t)
	path := filepath.Join(a.Dir, "category.yaml")
	if err := os.Chmod(path, 0200); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })
	if _, err := os.ReadFile(path); !errors.Is(err, os.ErrPermission) {
		t.Fatal("0200 regression requires a real non-root permission denial, not a skip", err)
	}
	const content = "movie:\n  New:\n"
	categoryRuntimeApply(t, a, content, false)
	categoryRuntimeMatch(t, a, "缓存电影")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0200 {
		t.Fatal("apply changed the source file's permission bits", err)
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(path)
	if err != nil || string(stored) != content {
		t.Fatal("write did not finish before swallowed helper read error", err)
	}
	categoryRuntimeMatch(t, a, "缓存电影") // Restoring readability is not init.
	if _, err = a.reloadMediaCategoryPolicy(); err != nil {
		t.Fatal(err)
	}
	categoryRuntimeMatch(t, a, "New")
}

func TestCategoryEditorRuntimeArchivedGetterDoesNotReloadExternalChanges(t *testing.T) {
	categoryRuntimeArchivedSource(t)
	a := categoryRuntimeWarmApp(t)
	path := filepath.Join(a.Dir, "category.yaml")
	if err := os.WriteFile(path, []byte("movie:\n  New:\n"), 0600); err != nil {
		t.Fatal(err)
	}
	categoryRuntimeMatch(t, a, "缓存电影")
	if _, err := a.reloadMediaCategoryPolicy(); err != nil {
		t.Fatal(err)
	}
	categoryRuntimeMatch(t, a, "New")
	if err := os.Rename(path, path+".owned-previous"); err != nil {
		t.Fatal(err)
	}
	categoryRuntimeMatch(t, a, "New")
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("getter recreated missing file without explicit init", err)
	}
}

func TestCategoryEditorRuntimeArchivedMissingFileCopiesExactDefault(t *testing.T) {
	categoryRuntimeArchivedSource(t)
	a := &App{Dir: t.TempDir()}
	policy, err := a.currentMediaCategoryPolicy()
	if err != nil || len(policy["movie"]) == 0 {
		t.Fatal("cold helper did not initialize default", err)
	}
	path := filepath.Join(a.Dir, "category.yaml")
	stored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(stored, []byte(defaultMediaCategoryPolicy)) {
		t.Fatal("helper did not copy exact embedded default to owned App path", err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("default copy is not an owned regular file", err)
	}
	if _, err = a.reloadMediaCategoryPolicy(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, after) {
		t.Fatal("explicit init unnecessarily replaced existing default", err)
	}
}

func TestCategoryEditorRuntimeRootFailureSurvivesReadIOUntilParsedAgain(t *testing.T) {
	categoryRuntimeArchivedSource(t)
	// Source init assigns _categorys before .get. IO failure leaves _categorys
	// untouched; parse failure explicitly assigns {}, clearing that .get error.
	for _, reset := range []string{"", "movie: [unterminated"} {
		t.Run(fmt.Sprintf("reset_%q", reset), func(t *testing.T) {
			a := categoryRuntimeWarmApp(t)
			path := filepath.Join(a.Dir, "category.yaml")
			categoryRuntimeApply(t, a, "true\n", true)
			if err := os.Chmod(path, 0200); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(path, 0600) })
			if _, err := os.ReadFile(path); !errors.Is(err, os.ErrPermission) {
				t.Fatal("test requires an actual read permission failure", err)
			}
			categoryRuntimeApply(t, a, "movie:\n  Unreadable:\n", true)
			categoryRuntimeMatch(t, a, "缓存电影")
			if err := os.Chmod(path, 0600); err != nil {
				t.Fatal(err)
			}
			categoryRuntimeApply(t, a, reset, false)
			if err := os.Chmod(path, 0200); err != nil {
				t.Fatal(err)
			}
			categoryRuntimeApply(t, a, "movie:\n  StillUnreadable:\n", false)
			categoryRuntimeMatch(t, a, "缓存电影")
		})
	}
}

func TestCategoryEditorRuntimeReadSafetyErrorsAreNotSwallowed(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "oversized", "non_utf8", "cyclic_yaml"} {
		t.Run(kind, func(t *testing.T) {
			a := categoryRuntimeWarmApp(t)
			path := filepath.Join(a.Dir, "category.yaml")
			switch kind {
			case "symlink", "directory":
				owned := path + ".owned-original"
				if err := os.Rename(path, owned); err != nil {
					t.Fatal(err)
				}
				var err error
				if kind == "symlink" {
					err = os.Symlink(owned, path)
				} else {
					err = os.Mkdir(path, 0700)
				}
				if err != nil {
					t.Fatal(err)
				}
			default:
				content := map[string]string{"oversized": strings.Repeat("#", maxCategoryPolicyBytes+1), "non_utf8": "movie:\n  Invalid\xff:\n", "cyclic_yaml": "movie: &loop\n  <<: *loop\n"}[kind]
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := a.reloadMediaCategoryPolicy(); err == nil {
				t.Fatal("explicit helper init swallowed a host safety boundary")
			}
			// Source cache getter behavior does not itself authorize reloading or
			// reading a path that failed a host boundary check.
			categoryRuntimeMatch(t, a, "缓存电影")
			cold := &App{Dir: a.Dir}
			if _, err := cold.currentMediaCategoryPolicy(); err == nil {
				t.Fatal("cold getter bypassed a host safety boundary")
			}
		})
	}
}
