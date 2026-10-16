package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

func useBuiltInPluginProcess(t *testing.T, a *App) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a.pluginCommand = func(pluginID string) (plugins.Command, error) {
		return plugins.Command{Path: executable, Args: []string{"-test.run=^TestBuiltInPluginFixture$", "--", pluginID}}, nil
	}
}

func TestBuiltInPluginFixture(t *testing.T) {
	for i, arg := range os.Args {
		if arg == "--" && i+1 < len(os.Args) {
			manifest, handle, ok := builtin.Lookup(os.Args[i+1])
			if !ok || plugins.Serve(context.Background(), os.Stdin, os.Stdout, manifest, handle) != nil {
				os.Exit(2)
			}
			os.Exit(0)
		}
	}
}

func TestPluginCategoryAPIExecutesRealProcessAndAppliesOrderedPolicy(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	h := a.Handler()
	cookie := setupTestAdmin(t, a)
	for _, request := range []struct{ method, path, body string }{
		{"GET", "/api/plugins", ""},
		{"POST", "/api/plugins/install", `{"plugin_id":"CategoryEditor"}`},
	} {
		if result := callTest(t, h, request.method, request.path, request.body, nil, ""); result.Code != 401 {
			t.Fatal("unauthenticated plugin API accepted", result.Code)
		}
	}
	if w := callTest(t, h, "POST", "/api/plugins/install", `{"plugin_id":"CategoryEditor"}`, cookie, "http://evil.example"); w.Code != 403 {
		t.Fatal("cross-origin plugin installation accepted", w.Code)
	}
	if w := callTest(t, h, "POST", "/api/plugins/install", `{"plugin_id":"NotPortedYet"}`, cookie, ""); w.Code != 400 || !strings.Contains(w.Body.String(), "PLUGIN_IMPLEMENTATION_UNAVAILABLE") {
		t.Fatal("unimplemented plugin reported install success", w.Code, w.Body.String())
	}
	if w := callTest(t, h, "POST", "/api/plugins/install", `{"plugin_id":"CategoryEditor"}`, cookie, ""); w.Code != 200 {
		t.Fatal("install failed", w.Code, w.Body.String())
	}
	run, err := a.pluginStore.ClaimRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(context.Background(), run)
	run, _ = a.pluginStore.Run(context.Background(), run.ID)
	if run.State != "succeeded" {
		t.Fatal("default init failed", run.State, run.ErrorCode)
	}
	if _, err := os.Lstat(filepath.Join(a.Dir, "category.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("disabled default configuration changed category file", err)
	}
	form := callTest(t, h, "GET", "/api/plugins/CategoryEditor/form", "", cookie, "")
	var data struct {
		Data struct {
			Revision int64 `json:"revision"`
		} `json:"data"`
	}
	if form.Code != 200 || json.Unmarshal(form.Body.Bytes(), &data) != nil || data.Data.Revision != 1 || !strings.Contains(form.Body.String(), "VAceEditor") || !strings.Contains(form.Body.String(), "启用插件（写入配置文件）") {
		t.Fatal("original config form/field contract missing", form.Code, form.Body.String())
	}
	config := map[string]any{"revision": data.Data.Revision, "config": map[string]any{"enabled": true, "content": customJapaneseMovieCategory}}
	raw, _ := json.Marshal(config)
	w := callTest(t, h, "PUT", "/api/plugins/CategoryEditor/config", string(raw), cookie, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), customJapaneseMovieCategory) {
		t.Fatal("save failed or echoed private invocation", w.Code, w.Body.String())
	}
	run, err = a.pluginStore.ClaimRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(context.Background(), run)
	run, err = a.pluginStore.Run(context.Background(), run.ID)
	if err != nil || run.State != "succeeded" || string(run.Result) != `{"enabled":false,"applied":true}` {
		t.Fatal("real Go plugin did not finish", run.State, run.ErrorCode, string(run.Result), err)
	}
	file, err := os.ReadFile(filepath.Join(a.Dir, "category.yaml"))
	if err != nil || string(file) != customJapaneseMovieCategory {
		t.Fatal("category YAML was not really written", err)
	}
	policy, err := loadMediaCategoryPolicy(a.Dir)
	if err != nil || policy.category(catalog.Media{MediaType: "movie", OriginalLanguage: "ja", OriginCountry: []string{"JP"}, GenreIDs: []int64{16}}) != "日番动漫电影" {
		t.Fatal("plugin file did not affect actual ordered categorization", err)
	}
	stored, err := a.pluginStore.Config(context.Background(), "CategoryEditor")
	if err != nil || stored.Revision != 3 || !bytes.Contains(stored.Value, []byte(`"enabled":false`)) {
		t.Fatal("one-shot config flag was not cleared", stored.Revision, err)
	}
	var notices int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='notification' AND id=?`, "plugin_notice_"+run.ID).Scan(&notices); err != nil || notices != 1 {
		t.Fatal("original system notification missing", notices, err)
	}
	if w := callTest(t, h, "PUT", "/api/plugins/CategoryEditor/config", string(raw), cookie, ""); w.Code != 400 || !strings.Contains(w.Body.String(), "PLUGIN_REVISION_CONFLICT") {
		t.Fatal("stale UI overwrote plugin's one-shot update", w.Code, w.Body.String())
	}
	if w := callTest(t, h, "GET", "/api/plugins/CategoryEditor/runs", "", cookie, ""); w.Code != 200 || strings.Contains(w.Body.String(), "content") || strings.Contains(w.Body.String(), "applied") {
		t.Fatal("run history leaked config or worker output", w.Code, w.Body.String())
	}
	if w := callTest(t, h, "POST", "/api/plugins/CategoryEditor/uninstall", `{}`, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	retained, err := a.pluginStore.Config(context.Background(), "CategoryEditor")
	if err != nil || !bytes.Equal(retained.Value, stored.Value) {
		t.Fatal("ordinary uninstall deleted plugin config", err)
	}
}

func TestPluginHostRejectsOldRunAfterResetOrConfigEdit(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	manifest := builtin.CategoryEditorManifest()
	raw, _ := json.Marshal(manifest)
	installation := PluginInstallation{InstanceID: "CategoryEditor", PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: 1, Manifest: raw}
	if err := a.pluginStore.Install(ctx, installation); err != nil {
		t.Fatal(err)
	}
	_, run, err := a.pluginStore.SaveConfigAndQueue(ctx, installation.InstanceID, 0, map[string]any{"enabled": true, "content": defaultMediaCategoryPolicy})
	if err != nil {
		t.Fatal(err)
	}
	run, err = a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	installation, _ = a.pluginStore.Installation(ctx, installation.InstanceID)
	identity := plugins.Identity{PluginID: manifest.PluginID, InstanceID: installation.InstanceID, Version: manifest.Version}
	request, _ := json.Marshal(map[string]string{"content": customJapaneseMovieCategory})
	wrong := identity
	wrong.InstanceID = "Other"
	if _, err := a.pluginHostCall(ctx, installation, &run, wrong, "category.write", request); pluginErrorCode(err) != "PLUGIN_IDENTITY_MISMATCH" {
		t.Fatal("cross-instance host call accepted", err)
	}
	if _, err := a.pluginHostCall(ctx, installation, &run, identity, "shell.exec", request); pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
		t.Fatal("arbitrary host command accepted", err)
	}
	if err := a.pluginStore.Reset(ctx, installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.pluginStore.SaveConfigAndQueue(ctx, installation.InstanceID, 0, map[string]any{"enabled": true, "content": defaultMediaCategoryPolicy}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginHostCall(ctx, installation, &run, identity, "category.write", request); pluginErrorCode(err) != "PLUGIN_SNAPSHOT_STALE" {
		t.Fatal("old run survived reset ABA", err)
	}
	if _, err := os.Lstat(filepath.Join(a.Dir, "category.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stale run modified category file", err)
	}
}

func TestPluginCategoryRejectsInvalidYAMLAndSymlinkWithoutOverwriting(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	if w := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"CategoryEditor"}`, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, body := range []string{
		`{"revision":1,"config":{"enabled":true,"content":"movie: [bad]"}}`,
		`{"revision":1,"config":{"enabled":false,"content":"","unported_option":true}}`,
	} {
		if w := callTest(t, a.Handler(), "PUT", "/api/plugins/CategoryEditor/config", body, cookie, ""); w.Code != 400 {
			t.Fatal("invalid YAML or unknown field was accepted", w.Code, w.Body.String())
		}
	}
	unchanged, err := a.pluginStore.Config(context.Background(), "CategoryEditor")
	if err != nil || unchanged.Revision != 1 {
		t.Fatal("rejected config was persisted", unchanged.Revision, err)
	}
	original := filepath.Join(t.TempDir(), "original.yaml")
	if err := os.WriteFile(original, []byte("do-not-change"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(original, filepath.Join(a.Dir, "category.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := a.writePluginCategory(defaultMediaCategoryPolicy); pluginErrorCode(err) != "CATEGORY_FILE_UNSAFE" {
		t.Fatal("plugin followed a category symlink", err)
	}
	file, _ := os.ReadFile(original)
	if string(file) != "do-not-change" {
		t.Fatal("plugin modified a file outside its config directory")
	}
}

func TestPluginRuntimeRejectsUnavailablePinnedVersionBeforeStarting(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	manifest := builtin.CategoryEditorManifest()
	manifest.Version = "99.0"
	raw, _ := json.Marshal(manifest)
	installation := PluginInstallation{InstanceID: manifest.PluginID, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: 1, Manifest: raw}
	if err := a.pluginStore.Install(ctx, installation); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.pluginStore.SaveConfigAndQueue(ctx, installation.InstanceID, 0, map[string]bool{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.pluginCommand = func(string) (plugins.Command, error) {
		t.Fatal("a different compiled version was substituted for pinned run")
		return plugins.Command{}, nil
	}
	a.executePluginRun(ctx, run)
	finished, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || finished.State != "failed" || finished.ErrorCode != "PLUGIN_IMPLEMENTATION_UNAVAILABLE" {
		t.Fatal("unavailable version was reported successful", finished.State, finished.ErrorCode, err)
	}
}

func TestPluginLogsRetainAndFilterExecutionIdentity(t *testing.T) {
	a := newTestApp(t)
	for _, instance := range []string{"CategoryEditor", "CategoryEditor.music"} {
		a.pluginExecutionLog(PluginInstallation{PluginID: "CategoryEditor"}, PluginRun{ID: "pr_" + instance, InstanceID: instance, Version: "1.3"}, "INFO", "execution_completed", "插件执行已完成", "")
	}
	flushTestLogs(t, a)
	page, err := a.queryLogs(context.Background(), map[string]string{"plugin_id": "CategoryEditor", "plugin_instance_id": "CategoryEditor.music"})
	if err != nil || len(page.Entries) != 1 || page.Entries[0]["plugin_run_id"] != "pr_CategoryEditor.music" || page.Entries[0]["plugin_version"] != "1.3" {
		t.Fatal("plugin log identity or independent clone filter missing", page.Entries, err)
	}
}
