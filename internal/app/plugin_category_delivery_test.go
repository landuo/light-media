package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"light-media/internal/catalog"
	"os"
	"path/filepath"
	"testing"
)

func categoryDeliveryDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// The optional input is read once from stdin and is never logged or written as
// plaintext. This proves the actual original disabled config can cross the real
// encrypted import boundary; ordinary CI uses a synthetic equivalent instead.

func TestCategoryEditorDeliveryAppliedPolicyManualOrganizeAndLifecycle(t *testing.T) {
	a, source, target := importTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	handler := a.Handler()
	if r := callTest(t, handler, "POST", "/api/plugins/install", `{"plugin_id":"CategoryEditor"}`, cookie, ""); r.Code != 200 {
		t.Fatal("actual install failed", r.Code)
	}
	finish := func() PluginRun {
		t.Helper()
		run, err := a.pluginStore.ClaimRun(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		a.executePluginRun(context.Background(), run)
		completed, err := a.pluginStore.Run(context.Background(), run.ID)
		if err != nil || completed.State != "succeeded" {
			t.Fatal("actual SDK run failed", completed.State, completed.ErrorCode, err)
		}
		return completed
	}
	finish()
	body, _ := json.Marshal(map[string]any{"revision": 1, "config": map[string]any{"enabled": true, "content": customJapaneseMovieCategory}})
	if r := callTest(t, handler, "PUT", "/api/plugins/CategoryEditor/config", string(body), cookie, ""); r.Code != 200 {
		t.Fatal("actual config save failed", r.Code)
	}
	applied := finish()
	if string(applied.Result) != `{"enabled":false,"applied":true}` {
		t.Fatal("one-shot did not apply/reset")
	}
	identity := importFixture(t, a, "movie")
	identity.OriginalLanguage = "ja"
	identity.OriginCountry = []string{"JP"}
	identity.Genres = []catalog.Genre{{ID: 16, Name: "动画"}}
	importCache(t, a, "media:movie:123", identity)
	path := filepath.Join(source, "Test Film (2024).mkv")
	importWrite(t, path, []byte("category delivery owned media"))
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: path}}, LibraryTypeFolder: importFlag(true)})
	want := filepath.Join(target, "日番动漫电影", "测试影片 (2024)", "测试影片 (2024).mkv")
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Target != want {
		t.Fatal("plugin write did not reach manual organize preview", err)
	}
	job, err := a.executeImport(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, job)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(want)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("manual organize did not publish classified hardlink", err)
	}
	kept, err := a.pluginStore.Config(context.Background(), "CategoryEditor")
	if err != nil {
		t.Fatal(err)
	}
	var flags struct{ Enabled bool }
	if json.Unmarshal(kept.Value, &flags) != nil || flags.Enabled {
		t.Fatal("one-shot flag remains active")
	}
	for i := 0; i < 2; i++ {
		if err := a.initializeNativePlugins(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.pluginStore.ClaimRun(context.Background()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("completed one-shot replayed", err)
	}
	if r := callTest(t, handler, "POST", "/api/plugins/CategoryEditor/uninstall", `{}`, cookie, ""); r.Code != 200 {
		t.Fatal("actual uninstall failed", r.Code)
	}
	if r := callTest(t, handler, "POST", "/api/plugins/install", `{"plugin_id":"CategoryEditor"}`, cookie, ""); r.Code != 200 {
		t.Fatal("actual reinstall failed", r.Code)
	}
	finish()
	restored, err := a.pluginStore.Config(context.Background(), "CategoryEditor")
	if err != nil || !bytes.Equal(restored.Value, kept.Value) {
		t.Fatal("reinstall did not retain disabled config", err)
	}
	content, err := os.ReadFile(filepath.Join(a.Dir, "category.yaml"))
	if err != nil || string(content) != customJapaneseMovieCategory {
		t.Fatal("disable/uninstall/reinstall changed applied global policy", err)
	}
	t.Log("actual HTTP→Go worker→SQLite→category.yaml→manual preview→hardlink verified; one-shot reset/no startup replay and uninstall/reinstall policy retention verified")
}
