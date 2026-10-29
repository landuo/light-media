package app

// These tests run the actual CategoryEditor executable through the standard
// resources SDK, the real App/SQLite store, and an owned loopback artifact
// server. All policy/config/data inputs are synthetic. They are not evidence
// of an old MoviePilot live run, a NAS deployment, or production migration.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

const categoryEditorStandardID = "CategoryEditor"
const categoryEditorStandardMessage = "二级分类策略已更新，请注意同步调整目录设置，插件将恢复关闭状态！"
const categoryEditorStandardWarm = "movie:\n  缓存电影:\n    original_language: 'zh'\n  其它电影:\ntv:\n  缓存剧集:\n"

type categoryEditorStandardFixture struct {
	app                 *App
	cookie              *http.Cookie
	indexURL            string
	indexHits, fileHits atomic.Int32
	unexpected          atomic.Int32
	sessions            map[*plugins.Client]struct{}
}

func newCategoryEditorStandardFixture(t *testing.T, initialPolicy *string) *categoryEditorStandardFixture {
	t.Helper()
	binary := os.Getenv("CATEGORYEDITOR_WORKER_BINARY")
	if binary == "" {
		t.Fatal("CATEGORYEDITOR_WORKER_BINARY must identify the actual compiled CategoryEditor standard SDK executable; this integration test must not skip")
	}
	var err error
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	raw, err := exec.CommandContext(ctx, binary, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatalf("actual CategoryEditor --manifest: %v", err)
	}
	var manifest plugins.Manifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if err = manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if manifest.PluginID != categoryEditorStandardID || manifest.Version != "1.3" || manifest.PluginOrder != 5 || manifest.Name != "二级分类策略" || manifest.HasPage || len(manifest.Events) != 0 || len(manifest.APIs) != 0 || slices.Contains(manifest.Methods, "invoke") {
		t.Fatalf("not the source CategoryEditor 1.3 standard executable: %+v", manifest)
	}
	wantCapabilities := []string{"plugin.category.get_default", "plugin.category.apply", "plugin.systemmessage.put", plugins.BaseUpdateConfig}
	if !slices.Equal(manifest.Capabilities, wantCapabilities) {
		t.Fatalf("unexpected CategoryEditor host authority: %v", manifest.Capabilities)
	}
	hash := sha256.Sum256(payload)
	digest := hex.EncodeToString(hash[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: "light-media.sdk-resources/v1"}}}}}
	f := &categoryEditorStandardFixture{sessions: map[*plugins.Client]struct{}{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			f.indexHits.Add(1)
			_ = json.NewEncoder(w).Encode(index)
		case "/category-editor":
			f.fileHits.Add(1)
			_, _ = w.Write(payload)
		default:
			f.unexpected.Add(1)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/category-editor"
	f.indexURL = server.URL + "/index"
	ownedDir := t.TempDir()
	if initialPolicy != nil {
		if err = os.WriteFile(filepath.Join(ownedDir, "category.yaml"), []byte(*initialPolicy), 0600); err != nil {
			t.Fatal(err)
		}
	}
	markets := []string{}
	f.app, err = New(Options{DataDir: ownedDir, PluginMarketsOverride: &markets})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if f.app != nil {
			if err := f.app.Close(); err != nil {
				t.Error("close owned CategoryEditor App", err)
			}
		}
		// App shutdown logs stop errors; also fail the integration test on an
		// actual worker's nonzero exit, including the race detector's exit code.
		for client := range f.sessions {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := client.Stop(ctx)
			cancel()
			if err != nil {
				t.Error("actual CategoryEditor process exit/join status", err)
			}
		}
	})
	// No telemetry/market traffic is authorized by this local fixture.
	settings := f.app.Settings()
	noShare, noServer := false, ""
	settings.PluginStatisticShare, settings.PluginServerURL = &noShare, &noServer
	if err = f.app.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	f.cookie = setupTestAdmin(t, f.app)
	f.install(t, categoryEditorStandardID, false, f.indexURL)
	object := f.object(t, categoryEditorStandardID)
	if object.hostContract != "light-media.sdk-resources/v1" || !object.external {
		t.Fatal("installation substituted a native executor")
	}
	object.sessionMu.Lock()
	actualSession := object.session != nil && object.sessionStarted
	object.sessionMu.Unlock()
	if !actualSession {
		t.Fatal("real executable session was not started")
	}
	if f.indexHits.Load() != 1 || f.fileHits.Load() != 1 || f.unexpected.Load() != 0 {
		t.Fatalf("actual artifact acquisition: index=%d binary=%d unexpected=%d", f.indexHits.Load(), f.fileHits.Load(), f.unexpected.Load())
	}
	t.Logf("actual %s/%s standard worker, version=%s sha256=%s; owned SQLite + loopback artifact HTTP", runtime.GOOS, runtime.GOARCH, manifest.Version, digest)
	return f
}

func (f *categoryEditorStandardFixture) success(t *testing.T, method, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	r := callTest(t, f.app.Handler(), method, path, string(raw), f.cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	f.app.wg.Wait()
}

func (f *categoryEditorStandardFixture) install(t *testing.T, id string, force bool, repo string) {
	t.Helper()
	f.success(t, "POST", "/api/plugins/install", map[string]any{"plugin_id": id, "repo_url": repo, "force": force})
}

func (f *categoryEditorStandardFixture) save(t *testing.T, id string, value any) {
	t.Helper()
	f.success(t, "PUT", "/api/plugins/"+id, value)
}

func (f *categoryEditorStandardFixture) object(t *testing.T, id string) *originalPluginInstance {
	t.Helper()
	value, ok := f.app.pluginRunningInstances.Load(id)
	if !ok {
		t.Fatalf("actual CategoryEditor instance %s is not loaded", id)
	}
	object := value.(*originalPluginInstance)
	object.sessionMu.Lock()
	client := object.session
	object.sessionMu.Unlock()
	if client != nil {
		f.sessions[client] = struct{}{}
	}
	return object
}

func (f *categoryEditorStandardFixture) config(t *testing.T, id string, want any) PluginStoredValue {
	t.Helper()
	got, err := f.app.pluginStore.Config(context.Background(), id)
	raw, marshalErr := json.Marshal(want)
	if err != nil || marshalErr != nil || !originalBaseJSONEqual(got.Value, raw) {
		t.Fatalf("persisted config %s: got=%s want=%s err=%v", id, got.Value, raw, err)
	}
	return got
}

func (f *categoryEditorStandardFixture) disabled(t *testing.T, id, content string) PluginStoredValue {
	t.Helper()
	got := f.config(t, id, map[string]any{"enabled": false, "content": content})
	var state json.RawMessage
	if err := f.object(t, id).readSession(context.Background(), "get_state", &state); err != nil || string(state) != "false" {
		t.Fatalf("actual worker not auto-disabled: state=%s err=%v", state, err)
	}
	return got
}

func (f *categoryEditorStandardFixture) bytes(t *testing.T, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(f.app.Dir, "category.yaml"))
	if err != nil || !bytes.Equal(got, []byte(want)) {
		t.Fatalf("actual global category.yaml bytes differ: got=%q want=%q err=%v", got, want, err)
	}
}

func (f *categoryEditorStandardFixture) categories(t *testing.T, movie, tv string) {
	t.Helper()
	for _, sample := range []struct {
		media catalog.Media
		want  string
	}{
		{catalog.Media{ID: 101, MediaType: "movie", OriginalLanguage: "zh", CategoryMetadataKnown: true}, movie},
		{catalog.Media{ID: 202, MediaType: "tv", OriginCountry: []string{"CN"}, CategoryMetadataKnown: true}, tv},
	} {
		_, got, err := f.app.importMediaCategory(context.Background(), sample.media)
		if err != nil || got != sample.want {
			t.Fatalf("real core classification %s: got=%q want=%q err=%v", sample.media.MediaType, got, sample.want, err)
		}
	}
}

func (f *categoryEditorStandardFixture) notification(t *testing.T) {
	t.Helper()
	var message systemMessage
	raw := f.app.systemMessages.get("plugin")
	if err := json.Unmarshal(raw, &message); err != nil || message.Type != "plugin" || message.Title == nil || *message.Title != "二级分类策略" || message.Text != categoryEditorStandardMessage {
		t.Fatalf("actual system-message helper: %s err=%v", raw, err)
	}
	if next := f.app.systemMessages.get(""); len(next) != 0 {
		t.Fatalf("unexpected extra notification: %s", next)
	}
}

func (f *categoryEditorStandardFixture) noNotification(t *testing.T) {
	t.Helper()
	if raw := f.app.systemMessages.get(""); len(raw) != 0 {
		t.Fatalf("unexpected notification: %s", raw)
	}
}

func (f *categoryEditorStandardFixture) coldReopen(t *testing.T) {
	t.Helper()
	dir := f.app.Dir
	if err := f.app.Close(); err != nil {
		t.Fatal(err)
	}
	f.app = nil
	markets := []string{}
	a, err := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if err != nil {
		t.Fatal(err)
	}
	f.app = a
	// Run the actual installed-list startup manager, without unrelated scheduler
	// threads/network services. The full server/browser startup is separate QA.
	if err = a.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	f.object(t, categoryEditorStandardID)
}

func categoryEditorStandardSourceReferences(t *testing.T) {
	t.Helper()
	for _, item := range []struct{ name, sum string }{
		{"installed-categoryeditor.py", "24a8109b5c15bb73b845e42653933d09eb83b608a53303fbab9ebff39b801c27"},
		{"installed-category-helper.py", "1c0ec53b78caa3a4949dd970931f38430c67c9d46ea2e16c3cc00950fb24c2b2"},
	} {
		raw, err := os.ReadFile(filepath.Join("../../testdata/plugins/category-editor-1.3", item.name))
		hash := sha256.Sum256(raw)
		if err != nil || hex.EncodeToString(hash[:]) != item.sum {
			t.Fatalf("fixed original source reference differs: %s %v", item.name, err)
		}
	}
}

func TestCategoryEditorStandardInstallSaveAndSourceSurface(t *testing.T) {
	categoryEditorStandardSourceReferences(t)
	f := newCategoryEditorStandardFixture(t, nil)
	a := f.app
	object := f.object(t, categoryEditorStandardID)
	if _, err := a.pluginStore.Config(context.Background(), categoryEditorStandardID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("install manufactured default config", err)
	}
	f.noNotification(t)
	for _, method := range []string{"get_service", "get_command", "get_api", "get_page", "get_dashboard"} {
		var raw json.RawMessage
		if err := object.readSession(context.Background(), method, &raw); err != nil || string(raw) != "null" {
			t.Fatalf("source empty method %s: %s %v", method, raw, err)
		}
	}
	var reference struct {
		Form json.RawMessage `json:"form"`
	}
	raw, err := os.ReadFile("../../testdata/plugins/category-editor-1.3/installed-reference.json")
	if err != nil || json.Unmarshal(raw, &reference) != nil {
		t.Fatal("source form reference", err)
	}
	formResponse := callTest(t, a.Handler(), "GET", "/api/plugins/CategoryEditor/form", "", f.cookie, "")
	var form struct {
		Data struct {
			Conf  json.RawMessage `json:"conf"`
			Model map[string]any  `json:"model"`
		} `json:"data"`
	}
	if formResponse.Code != 200 || json.Unmarshal(formResponse.Body.Bytes(), &form) != nil || !originalBaseJSONEqual(form.Data.Conf, reference.Form) || form.Data.Model["enabled"] != false || form.Data.Model["content"] != defaultMediaCategoryPolicy {
		t.Fatal("actual default form differs from original", formResponse.Body.String())
	}
	pageResponse := callTest(t, a.Handler(), "GET", "/api/plugins/CategoryEditor/page", "", f.cookie, "")
	var page struct {
		Data struct {
			Page []any `json:"page"`
		} `json:"data"`
	}
	if pageResponse.Code != 200 || json.Unmarshal(pageResponse.Body.Bytes(), &page) != nil || len(page.Data.Page) != 0 {
		t.Fatal("source has no business page", pageResponse.Body.String())
	}
	content := "# synthetic, retain exact Unicode/CRLF/trailing bytes\r\nmovie:\n  云端电影:\n    original_language: 'zh'\n  外语后备: {}\ntv:\n  云端剧集: {}\n\n"
	f.save(t, categoryEditorStandardID, map[string]any{"enabled": true, "content": content, "discard_after_success": "owned synthetic field"})
	if f.object(t, categoryEditorStandardID) != object {
		t.Fatal("config save replaced the persistent SDK object")
	}
	f.bytes(t, content)
	f.categories(t, "云端电影", "云端剧集")
	f.notification(t)
	f.disabled(t, categoryEditorStandardID, content)
	formResponse = callTest(t, a.Handler(), "GET", "/api/plugins/CategoryEditor/form", "", f.cookie, "")
	if formResponse.Code != 200 || json.Unmarshal(formResponse.Body.Bytes(), &form) != nil || form.Data.Model["content"] != content || form.Data.Model["enabled"] != false {
		t.Fatal("saved config overlay was not exposed by standard form", formResponse.Body.String())
	}
	a.pluginOriginalDirectJobs.mu.Lock()
	jobs := len(a.pluginOriginalDirectJobs.jobs)
	a.pluginOriginalDirectJobs.mu.Unlock()
	a.pluginOriginalCommandsMu.Lock()
	commands := len(a.pluginOriginalCommands)
	a.pluginOriginalCommandsMu.Unlock()
	apis, err := object.getAPIs(context.Background())
	var runs int
	queryErr := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&runs)
	if jobs != 0 || commands != 0 || len(apis) != 0 || err != nil || queryErr != nil || runs != 0 {
		t.Fatalf("source gained execution surfaces: jobs=%d commands=%d apis=%d runs=%d err=%v/%v", jobs, commands, len(apis), runs, err, queryErr)
	}
}

func TestCategoryEditorStandardCloneLifecycleAndColdNoReplay(t *testing.T) {
	f := newCategoryEditorStandardFixture(t, nil)
	ctx := context.Background()
	const source = categoryEditorStandardID
	const clone = source + "owned"
	sourceContent := "movie:\n  源电影: {}\ntv:\n  源剧集: {}\n"
	cloneContent := "movie:\n  分身电影: {}\ntv:\n  分身剧集: {}\n"
	f.save(t, source, map[string]any{"enabled": true, "content": sourceContent})
	f.notification(t)
	f.success(t, "POST", "/api/plugins/"+source+"/clone", map[string]any{"suffix": "Owned"})
	sourceObject, cloneObject := f.object(t, source), f.object(t, clone)
	if sourceObject == cloneObject {
		t.Fatal("clone reused source process object")
	}
	// CategoryEditor itself has no KV business methods. Seed ordinary platform
	// namespaces with synthetic values to check that its lifecycle preserves them.
	for id, value := range map[string]string{source: `{"owned":"source"}`, clone: `{"owned":"clone"}`} {
		if err := f.app.pluginStore.originalBaseSaveData(ctx, id, "synthetic-isolation", json.RawMessage(value)); err != nil {
			t.Fatal(err)
		}
	}
	f.save(t, clone, map[string]any{"enabled": true, "content": cloneContent, "drop": true})
	sourceConfig := f.disabled(t, source, sourceContent)
	cloneConfig := f.disabled(t, clone, cloneContent)
	f.bytes(t, cloneContent)
	f.categories(t, "分身电影", "分身剧集")
	// The single global helper may deduplicate the identical success message.
	// Do not interpret that as proof no callback ran, or invent per-clone files.
	for len(f.app.systemMessages.get("")) != 0 {
	}
	path := filepath.Join(f.app.Dir, "category.yaml")
	frozen := time.Unix(981173106, 0)
	if err := os.Chtimes(path, frozen, frozen); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	noReplayWrite := func() {
		t.Helper()
		f.bytes(t, cloneContent)
		after, err := os.Stat(path)
		if err != nil || !os.SameFile(before, after) || !after.ModTime().Equal(frozen) {
			t.Fatal("disabled lifecycle replayed a category.yaml write", err)
		}
	}
	unchanged := func() {
		t.Helper()
		noReplayWrite()
		for id, want := range map[string]string{source: `{"owned":"source"}`, clone: `{"owned":"clone"}`} {
			data, err := f.app.pluginStore.Data(ctx, id, "synthetic-isolation")
			if err != nil || string(data.Value) != want {
				t.Fatalf("lifecycle crossed namespace %s: %s %v", id, data.Value, err)
			}
		}
		if got := f.disabled(t, source, sourceContent); got.Revision != sourceConfig.Revision {
			t.Fatal("lifecycle rewrote source config")
		}
		if got := f.disabled(t, clone, cloneContent); got.Revision != cloneConfig.Revision {
			t.Fatal("lifecycle rewrote clone config")
		}
		f.noNotification(t)
	}
	f.success(t, "POST", "/api/plugins/"+source+"/reload", map[string]any{})
	if f.object(t, source) == sourceObject || f.object(t, clone) != cloneObject {
		t.Fatal("source reload failed to replace only source worker")
	}
	unchanged()
	sourceObject = f.object(t, source)
	fileHits := f.fileHits.Load()
	f.install(t, source, true, f.indexURL)
	if f.object(t, source) == sourceObject || f.object(t, clone) != cloneObject || f.fileHits.Load() <= fileHits {
		t.Fatal("same-version force did not reacquire actual artifact and isolate clone")
	}
	unchanged()
	sourceObject = f.object(t, source)
	// Existing-code clone reinstall is not a fabricated new-version artifact.
	f.install(t, clone, false, "")
	if f.object(t, clone) == cloneObject || f.object(t, source) != sourceObject {
		t.Fatal("clone existing-code reinstall changed source")
	}
	cloneObject = f.object(t, clone)
	unchanged()
	request, _ := json.Marshal(map[string]any{"plugin_id": clone, "force": true, "repo_url": f.indexURL})
	rejected := callTest(t, f.app.Handler(), "POST", "/api/plugins/install", string(request), f.cookie, "")
	var result struct {
		Data struct {
			Success bool `json:"success"`
		} `json:"data"`
	}
	if rejected.Code != 200 || json.Unmarshal(rejected.Body.Bytes(), &result) != nil || result.Data.Success || f.object(t, clone) != cloneObject || f.object(t, source) != sourceObject {
		t.Fatal("wrong-ID source artifact was accepted for clone", rejected.Code, rejected.Body.String())
	}
	unchanged()
	f.coldReopen(t)
	unchanged()
	f.categories(t, "分身电影", "分身剧集")
	// Cold App owns a fresh notification FIFO/dedup cache; no replay is masked.
	f.noNotification(t)
	if f.object(t, source) == sourceObject || f.object(t, clone) == cloneObject {
		t.Fatal("cold App retained prior process objects")
	}
	sourceObject = f.object(t, source)
	f.success(t, "POST", "/api/plugins/"+clone+"/reset", map[string]any{})
	if f.object(t, source) != sourceObject {
		t.Fatal("clone reset restarted source")
	}
	for _, key := range []string{"config", "data"} {
		var err error
		if key == "config" {
			_, err = f.app.pluginStore.Config(ctx, clone)
		} else {
			_, err = f.app.pluginStore.Data(ctx, clone, "synthetic-isolation")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("clone reset retained own", key, err)
		}
	}
	noReplayWrite()
	f.disabled(t, source, sourceContent)
	// Re-populate the reset clone with disabled synthetic config/data so the
	// uninstall assertion proves deletion, not merely an already-empty state.
	f.save(t, clone, map[string]any{"enabled": false, "content": "owned disabled draft after reset"})
	if err := f.app.pluginStore.originalBaseSaveData(ctx, clone, "synthetic-isolation", json.RawMessage(`{"after_reset":true}`)); err != nil {
		t.Fatal(err)
	}
	f.success(t, "POST", "/api/plugins/"+clone+"/uninstall", map[string]any{})
	if _, loaded := f.app.pluginRunningInstances.Load(clone); loaded || f.object(t, source) != sourceObject {
		t.Fatal("clone uninstall retained clone or changed source")
	}
	if _, err := f.app.pluginStore.Config(ctx, clone); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("clone uninstall retained own configuration", err)
	}
	if _, err := f.app.pluginStore.Data(ctx, clone, "synthetic-isolation"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("clone uninstall retained own data", err)
	}
	if _, err := os.Stat(filepath.Join(f.app.pluginPackageRoot(), clone)); !os.IsNotExist(err) {
		t.Fatal("clone uninstall retained executable package", err)
	}
	noReplayWrite()
	f.categories(t, "分身电影", "分身剧集")
	f.noNotification(t)
	if data, err := f.app.pluginStore.Data(ctx, source, "synthetic-isolation"); err != nil || string(data.Value) != `{"owned":"source"}` {
		t.Fatal("clone reset/uninstall changed source KV", err)
	}
}

func TestCategoryEditorStandardWarmAndColdSourceReloadOracle(t *testing.T) {
	categoryEditorStandardSourceReferences(t)
	var oracle struct {
		Cases []struct {
			Case string   `json:"case"`
			Warm []string `json:"warm"`
			Cold []string `json:"cold"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile("../../testdata/plugins/category-editor-1.3/reload-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 11 {
		t.Fatal("fixed original helper reload oracle", err)
	}
	inputs := map[string]string{
		"blank": "", "comments": "# cleared policy\n", "null": "null\n",
		"empty_map": "{}\n", "empty_sequence": "[]\n", "false": "false\n", "zero": "0\n", "empty_string": "''\n",
		"parser_failure": "movie:\n  [ordinary syntax error\n", "duplicate_key": "movie: {}\nmovie: {}\n", "multiple_documents": "movie: {}\n---\ntv: {}\n",
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Case, func(t *testing.T) {
			content, exists := inputs[sample.Case]
			if !exists || len(sample.Warm) != 2 || len(sample.Cold) != 2 {
				t.Fatal("unmapped source oracle case")
			}
			warm := categoryEditorStandardWarm
			f := newCategoryEditorStandardFixture(t, &warm)
			f.categories(t, "缓存电影", "缓存剧集")
			f.save(t, categoryEditorStandardID, map[string]any{"enabled": true, "content": content, "discard": true})
			f.bytes(t, content)
			before := f.disabled(t, categoryEditorStandardID, content)
			f.notification(t)
			f.categories(t, sample.Warm[0], sample.Warm[1])
			f.coldReopen(t)
			f.bytes(t, content)
			if after := f.disabled(t, categoryEditorStandardID, content); after.Revision != before.Revision {
				t.Fatal("cold disabled init rewrote config")
			}
			f.categories(t, sample.Cold[0], sample.Cold[1])
			f.noNotification(t)
		})
	}
}

func TestCategoryEditorStandardFailureOrderAndRetainedConfig(t *testing.T) {
	for _, kind := range []string{"non_string_content", "unwritable_regular_file", "non_regular_path", "unpaired_surrogate"} {
		t.Run(kind, func(t *testing.T) {
			warm := categoryEditorStandardWarm
			f := newCategoryEditorStandardFixture(t, &warm)
			object := f.object(t, categoryEditorStandardID)
			content := "movie:\n  写入候选电影: {}\ntv:\n  写入候选剧集: {}\n"
			config := map[string]any{"enabled": true, "content": content, "retained_on_failure": kind}
			path := filepath.Join(f.app.Dir, "category.yaml")
			switch kind {
			case "non_string_content":
				config["content"] = map[string]any{"truthy": true}
			case "unwritable_regular_file":
				if os.Geteuid() == 0 {
					t.Fatal("unwritable-file source test requires a non-root Linux user, not a skip")
				}
				if err := os.Chmod(path, 0400); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(path, 0600) })
			case "non_regular_path":
				if err := os.Rename(path, path+".owned-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "unpaired_surrogate":
				// Preserve the actual HTTP JSON escape. Converting through a Go
				// string would replace the source's invalid Unicode before testing it.
				config["content"] = json.RawMessage(`"\ud800"`)
			}
			raw, _ := json.Marshal(config)
			response := callTest(t, f.app.Handler(), "PUT", "/api/plugins/CategoryEditor", string(raw), f.cookie, "")
			f.app.wg.Wait()
			if response.Code < 400 {
				t.Fatal("source initializer failure reported success", response.Code, response.Body.String())
			}
			stored := f.config(t, categoryEditorStandardID, config)
			if kind == "unpaired_surrogate" && !bytes.Contains(stored.Value, []byte(`\ud800`)) {
				t.Fatal("config storage silently replaced the invalid source character")
			}
			if f.object(t, categoryEditorStandardID) != object {
				t.Fatal("failed same-object init removed/replaced the loaded worker")
			}
			var state json.RawMessage
			if err := object.readSession(context.Background(), "get_state", &state); err != nil || string(state) != "true" {
				t.Fatal("failure incorrectly reached enabled=false assignment", string(state), err)
			}
			f.noNotification(t)
			switch kind {
			case "unpaired_surrogate":
				// pathlib.write_text opens/truncates before UTF-8 encoding fails.
				// No helper reload runs, so this process keeps its earlier maps.
				f.bytes(t, "")
				f.categories(t, "缓存电影", "缓存剧集")
			case "non_regular_path":
				info, err := os.Stat(path)
				if err != nil || !info.IsDir() {
					t.Fatal("non-regular owned path was replaced", err)
				}
			default:
				f.bytes(t, warm)
				f.categories(t, "缓存电影", "缓存剧集")
			}
		})
	}
}

func TestCategoryEditorStandardPythonConfigTruthiness(t *testing.T) {
	for _, value := range []any{nil, false, 0, "", []any{}, map[string]any{}} {
		raw, _ := json.Marshal(value)
		t.Run(string(raw), func(t *testing.T) {
			warm := categoryEditorStandardWarm
			f := newCategoryEditorStandardFixture(t, &warm)
			// Nonempty "false" is true in the original Python condition.
			f.save(t, categoryEditorStandardID, map[string]any{"enabled": "false", "content": value})
			f.bytes(t, "")
			f.disabled(t, categoryEditorStandardID, "")
			f.categories(t, "缓存电影", "缓存剧集")
			f.notification(t)
		})
	}
}

func TestCategoryEditorStandardSourceAcceptsUnvalidatedConditionOnSave(t *testing.T) {
	// The complete fixed CategoryHelper.init only loads YAML and assigns maps.
	// It does not call condition.split. Numeric conditions fail later during
	// classification, not during CategoryEditor.init's write/notice/disable path.
	// Keep this source-derived assertion even if the host's stricter parser
	// exposes a migration gap; never make the new implementation its own oracle.
	f := newCategoryEditorStandardFixture(t, nil)
	content := "movie:\n  Numeric:\n    genre_ids: 16\n"
	raw, _ := json.Marshal(map[string]any{"enabled": true, "content": content, "must_be_replaced": true})
	response := callTest(t, f.app.Handler(), "PUT", "/api/plugins/CategoryEditor", string(raw), f.cookie, "")
	f.app.wg.Wait()
	stored, storeErr := f.app.pluginStore.Config(context.Background(), categoryEditorStandardID)
	var state json.RawMessage
	stateErr := f.object(t, categoryEditorStandardID).readSession(context.Background(), "get_state", &state)
	t.Logf("source-unvalidated-condition observation: HTTP=%d config=%s state=%s storageErr=%v stateErr=%v", response.Code, stored.Value, state, storeErr, stateErr)
	f.bytes(t, content)
	if response.Code != http.StatusOK {
		t.Errorf("original CategoryHelper.init accepts numeric condition maps; save failed early: %d %s", response.Code, response.Body.String())
	}
	f.notification(t)
	f.disabled(t, categoryEditorStandardID, content)
	_, _, err := f.app.importMediaCategory(context.Background(), catalog.Media{ID: 303, MediaType: "movie", GenreIDs: []int64{16}, CategoryMetadataKnown: true})
	if err == nil {
		t.Fatal("numeric condition must fail when original get_category reaches value.split, not become a successful match")
	}
}

func TestCategoryEditorStandardActualOrganizePreviewConsumesPolicy(t *testing.T) {
	f := newCategoryEditorStandardFixture(t, nil)
	ctx := context.Background()
	ownedBase, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source, target := filepath.Join(ownedBase, "owned-source"), filepath.Join(ownedBase, "owned-library")
	for _, dir := range []string{source, target} {
		if err = os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	settings := f.app.Settings()
	settings.AllowedRoots = []string{source}
	settings.Roots = []Root{{ID: "owned-category-preview", Path: target, Kind: "mixed"}}
	if settings.TMDB.APIKey != "" || settings.TMDB.AccessToken != "" || len(settings.Sites) != 0 || len(settings.Downloaders) != 0 {
		t.Fatal("owned preview fixture must have no external provider credentials or downloader/site configuration")
	}
	if err = f.app.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	// Ordinary cached known metadata and a full synthetic provider response are
	// inputs, not classifier substitutes. A standard get_module declaration
	// makes the real planner use SourceInformation, not just the compact cache.
	// The existing bounded fixture transport handles that request entirely in
	// memory, rejects unknown requests, and cannot fall back to the public API.
	identity := importFixture(t, f.app, "movie")
	if !identity.CategoryMetadataKnown {
		t.Fatal("synthetic metadata lacks known category fields")
	}
	provider, err := json.Marshal(categoryMovieDetail())
	if err != nil {
		t.Fatal(err)
	}
	var metadataCalls atomic.Int32
	metadataClient := installMetadataFixture(t, f.app, "owned-category-preview-key", []string{"/3/movie/123"}, func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("append_to_response") != "images,credits,alternative_titles,translations,release_dates,external_ids" {
			return nil, errors.New("unexpected metadata projection in owned CategoryEditor preview")
		}
		metadataCalls.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(provider)), Request: r}, nil
	})
	input := filepath.Join(source, "Owned Category Preview (2024).mkv")
	inputBytes := []byte("owned synthetic bytes, not a user's media or a downloaded file")
	if err = os.WriteFile(input, inputBytes, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(input)
	if err != nil {
		t.Fatal(err)
	}
	request := ImportRequest{MediaType: "movie", TMDBID: identity.ID, RootID: "owned-category-preview", Mode: "hardlink", Scraping: importFlag(false), LibraryTypeFolder: importFlag(true), Files: []ImportFileRequest{{Path: input}}}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	preview := func(category string) ImportPlan {
		t.Helper()
		response := callTest(t, f.app.Handler(), "POST", "/api/import/preview", string(raw), f.cookie, "")
		var result struct {
			Data ImportPlan `json:"data"`
		}
		want := filepath.Join(target, category, "测试影片 (2024)", "测试影片 (2024).mkv")
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Data.ID == "" || result.Data.State != "preview" || len(result.Data.Files) != 1 || result.Data.Files[0].Status != "ready" || result.Data.Files[0].Target != want {
			t.Fatalf("actual organize HTTP preview did not consume worker policy %q: %d %s", category, response.Code, response.Body.String())
		}
		var persisted ImportPlan
		if err := f.app.Store.Get(ctx, "import_plan", result.Data.ID, &persisted); err != nil || len(persisted.Files) != 1 || persisted.Files[0].Target != want {
			t.Fatal("classified preview was not persisted as the actual SQLite plan", err)
		}
		return result.Data
	}
	firstPolicy := "movie:\n  插件保存的第一分类:\n    original_language: 'zh'\n  其它: {}\ntv: {}\n"
	f.save(t, categoryEditorStandardID, map[string]any{"enabled": true, "content": firstPolicy})
	f.disabled(t, categoryEditorStandardID, firstPolicy)
	first := preview("插件保存的第一分类")
	secondPolicy := "movie:\n  插件保存的第二分类:\n    original_language: 'zh'\n  其它: {}\ntv: {}\n"
	f.save(t, categoryEditorStandardID, map[string]any{"enabled": true, "content": secondPolicy})
	f.disabled(t, categoryEditorStandardID, secondPolicy)
	second := preview("插件保存的第二分类")
	var retained ImportPlan
	if first.ID == second.ID || f.app.Store.Get(ctx, "import_plan", first.ID, &retained) != nil || len(retained.Files) != 1 || retained.Files[0].Target != first.Files[0].Target {
		t.Fatal("a later policy save mutated the earlier frozen preview plan")
	}
	after, err := os.Stat(input)
	readBack, readErr := os.ReadFile(input)
	if err != nil || readErr != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || !bytes.Equal(inputBytes, readBack) {
		t.Fatal("preview changed the synthetic source", err, readErr)
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatal("preview published library files or directories", err)
	}
	var jobs int
	if err = f.app.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("preview launched an execution/download job", jobs, err)
	}
	if metadataCalls.Load() != 2 || f.app.catalog() != metadataClient {
		t.Fatal("preview did not retain the bounded in-memory metadata transport", metadataCalls.Load())
	}
	t.Log("actual HTTP organize preview + persisted SQLite plans consumed both worker-saved policies through real source-metadata projection; exactly two in-memory provider responses, no public network/execute/download, no published library file, source unchanged")
}
