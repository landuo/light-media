package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/plugins/builtin"
)

const categoryReloadInitial = "movie:\n  缓存电影:\n    original_language: 'zh'\n  其它电影:\ntv:\n  缓存剧集:\n"

func categoryReloadRun(t *testing.T, a *App) PluginRun {
	t.Helper()
	run, err := a.pluginStore.ClaimRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(context.Background(), run)
	completed, err := a.pluginStore.Run(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return completed
}

func categoryReloadInstall(t *testing.T, a *App) {
	t.Helper()
	useBuiltInPluginProcess(t, a)
	manifest := builtin.CategoryEditorManifest()
	raw, _ := json.Marshal(manifest)
	_, err := a.pluginStore.InstallAndQueue(context.Background(), PluginInstallation{InstanceID: "CategoryEditor", PluginID: "CategoryEditor", Version: "1.3", ManifestSchemaVersion: 1, Manifest: raw}, map[string]any{"enabled": true, "content": categoryReloadInitial})
	if err != nil {
		t.Fatal(err)
	}
	if run := categoryReloadRun(t, a); run.State != "succeeded" {
		t.Fatal(run.State, run.ErrorCode)
	}
}

func TestCategoryEditorReloadEmptyAndParserFailureRetainsProcessMaps(t *testing.T) {
	for _, tc := range []struct{ name, text string }{
		{"blank", ""}, {"comments", "# cleared policy\n"}, {"null", "null\n"}, {"empty_map", "{}\n"}, {"empty_sequence", "[]\n"}, {"false", "false\n"}, {"zero", "0\n"}, {"empty_string", "''\n"},
		{"parser_failure", "movie:\n  [ordinary syntax error\n"}, {"duplicate_key", "movie: {}\nmovie: {}\n"}, {"multiple_documents", "movie: {}\n---\ntv: {}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, source, target := importTestApp(t)
			categoryReloadInstall(t, a)
			cookie := setupTestAdmin(t, a)
			stored, err := a.pluginStore.Config(context.Background(), "CategoryEditor")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(map[string]any{"revision": stored.Revision, "config": map[string]any{"enabled": true, "content": tc.text}})
			response := callTest(t, a.Handler(), "PUT", "/api/plugins/CategoryEditor/config", string(body), cookie, "")
			if response.Code != 200 {
				t.Fatal("source accepted text was rejected before initialization", response.Code)
			}
			if run := categoryReloadRun(t, a); run.State != "succeeded" || string(run.Result) != `{"enabled":false,"applied":true}` {
				t.Fatal("text write/source init did not complete", run.State, run.ErrorCode)
			}
			written, err := os.ReadFile(filepath.Join(a.Dir, "category.yaml"))
			if err != nil || string(written) != tc.text {
				t.Fatal("source text was not written verbatim", err)
			}
			policy, err := a.currentMediaCategoryPolicy()
			if err != nil || policy.category(catalog.Media{MediaType: "movie", OriginalLanguage: "zh"}) != "缓存电影" || policy.category(catalog.Media{MediaType: "tv"}) != "缓存剧集" {
				t.Fatal("empty/parse failure dropped old process maps", err)
			}
			identity := importFixture(t, a, "movie")
			_ = identity
			file := filepath.Join(source, "Test Film (2024).mkv")
			importWrite(t, file, []byte("owned warm/cold category preview"))
			preview := func(current *App) ImportPlan {
				t.Helper()
				plan, err := current.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: file}}, LibraryTypeFolder: importFlag(true)})
				if err != nil || len(plan.Files) != 1 {
					t.Fatal("actual organize preview failed", err)
				}
				return plan
			}
			warm := preview(a)
			if !strings.HasPrefix(warm.Files[0].Target, filepath.Join(target, "缓存电影")+string(filepath.Separator)) {
				t.Fatal("manual organize ignored retained maps")
			}
			var notices int
			if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='notification'`).Scan(&notices); err != nil || notices != 2 {
				t.Fatal("original post-write notifications missing", notices, err)
			}
			dir, cfg := a.Dir, a.Settings()
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			cold, err := New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer cold.Close()
			useBuiltInPluginProcess(t, cold)
			cold.config = cfg
			policy, err = cold.currentMediaCategoryPolicy()
			if err != nil || policy.category(catalog.Media{MediaType: "movie", OriginalLanguage: "zh"}) != "" || policy.category(catalog.Media{MediaType: "tv"}) != "" {
				t.Fatal("cold process resurrected old maps", err)
			}
			coldPlan := preview(cold)
			if coldPlan.Files[0].Target != filepath.Join(target, "测试影片 (2024)", "测试影片 (2024).mkv") {
				t.Fatal("cold manual organize retained former category")
			}
			if err := cold.initializeNativePlugins(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := cold.pluginStore.ClaimRun(context.Background()); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("cold process replayed closed one-shot", err)
			}
		})
	}
}

func TestCategoryEditorTruthyEmptySectionsReplaceBothMapsImmediately(t *testing.T) {
	a := newTestApp(t)
	categoryReloadInstall(t, a)
	if err := a.writePluginCategory("movie: {}\n"); err != nil {
		t.Fatal(err)
	}
	policy, err := a.currentMediaCategoryPolicy()
	if err != nil || len(policy["movie"]) != 0 || len(policy["tv"]) != 0 {
		t.Fatal("truthy root must clear empty/missing sections", err)
	}
	if err := a.writePluginCategory("tv:\n  仅新剧集:\n"); err != nil {
		t.Fatal(err)
	}
	policy, err = a.currentMediaCategoryPolicy()
	if err != nil || len(policy["movie"]) != 0 || policy.category(catalog.Media{MediaType: "tv"}) != "仅新剧集" {
		t.Fatal("truthy root did not replace both maps", err)
	}
}

func TestCategoryEditorSystemNoticeFailureDoesNotPrematurelyCloseSwitch(t *testing.T) {
	a := newTestApp(t)
	categoryReloadInstall(t, a)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER category_owned_notice_failure BEFORE INSERT ON records WHEN NEW.kind='notification' BEGIN SELECT RAISE(ABORT,'owned category notice failure'); END`); err != nil {
		t.Fatal(err)
	}
	stored, err := a.pluginStore.Config(context.Background(), "CategoryEditor")
	if err != nil {
		t.Fatal(err)
	}
	content := "movie:\n  已写入新分类:\n"
	if _, _, err := a.pluginStore.SaveConfigAndQueue(context.Background(), "CategoryEditor", stored.Revision, map[string]any{"enabled": true, "content": content}); err != nil {
		t.Fatal(err)
	}
	run := categoryReloadRun(t, a)
	if run.State != "failed" {
		t.Fatal("real notification rejection reported success", run.State)
	}
	after, err := a.pluginStore.Config(context.Background(), "CategoryEditor")
	var flags struct{ Enabled bool }
	if err != nil || json.Unmarshal(after.Value, &flags) != nil || !flags.Enabled {
		t.Fatal("switch cleared before source systemmessage step", err)
	}
	file, err := os.ReadFile(filepath.Join(a.Dir, "category.yaml"))
	if err != nil || string(file) != content {
		t.Fatal("notice failure hid actual prior write", err)
	}
	policy, err := a.currentMediaCategoryPolicy()
	if err != nil || policy.category(catalog.Media{MediaType: "movie"}) != "已写入新分类" {
		t.Fatal("notice failure rolled back applied source maps", err)
	}
}

func TestCategoryEditorUnsupportedNativeYAMLIsExplicitlyRejected(t *testing.T) {
	a := newTestApp(t)
	categoryReloadInstall(t, a)
	cookie := setupTestAdmin(t, a)
	stored, err := a.pluginStore.Config(context.Background(), "CategoryEditor")
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"movie:\n  Unsupported: !dangerous {x: y}\n", "movie: [unsupported-shape]\n", "movie: &loop\n  <<: *loop\n", "movie:\n  '../outside': {}\n"} {
		body, _ := json.Marshal(map[string]any{"revision": stored.Revision, "config": map[string]any{"enabled": true, "content": content}})
		if response := callTest(t, a.Handler(), "PUT", "/api/plugins/CategoryEditor/config", string(body), cookie, ""); response.Code != 400 {
			t.Fatal("unsupported native YAML was reported implemented", response.Code)
		}
	}
	content, err := os.ReadFile(filepath.Join(a.Dir, "category.yaml"))
	if err != nil || string(content) != categoryReloadInitial {
		t.Fatal("unsupported YAML changed current global policy", err)
	}
}
