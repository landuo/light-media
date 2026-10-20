package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/plugins/builtin"
)

func categoryDeliveryDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func TestCategoryEditorDeliveryOriginalFormContract(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugins/category-editor-1.3/installed-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Form json.RawMessage `json:"form"`
	}
	if json.Unmarshal(raw, &reference) != nil {
		t.Fatal("invalid installed form reference")
	}
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	manifest := builtin.CategoryEditorManifest()
	rawManifest, _ := json.Marshal(manifest)
	installation := PluginInstallation{InstanceID: "CategoryEditor", PluginID: "CategoryEditor", Version: "1.3", ManifestSchemaVersion: 1, Manifest: rawManifest}
	if err := a.pluginStore.Install(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	form, err := a.pluginForm(context.Background(), installation)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(form["conf"])
	var expectedNodes, actualNodes any
	if json.Unmarshal(reference.Form, &expectedNodes) != nil || json.Unmarshal(got, &actualNodes) != nil {
		t.Fatal("form structure unavailable")
	}
	expected, _ := json.Marshal(expectedNodes)
	actual, _ := json.Marshal(actualNodes)
	if !bytes.Equal(expected, actual) {
		t.Fatal("Go SDK form differs from full installed get_form nodes")
	}
	model, _ := json.Marshal(form["model"])
	var defaults struct {
		Enabled bool
		Content string
	}
	if json.Unmarshal(model, &defaults) != nil || defaults.Enabled || categoryDeliveryDigest([]byte(defaults.Content)) != "1095b3aeb315569f541fded31bc77f43fa625cb9ac4844e3a28ba7d7868b2303" {
		t.Fatal("original disabled/default YAML model mismatch")
	}
	if manifest.HasPage || len(manifest.Events) != 0 {
		t.Fatal("original has no detail page or events")
	}
}

// The optional input is read once from stdin and is never logged or written as
// plaintext. This proves the actual original disabled config can cross the real
// encrypted import boundary; ordinary CI uses a synthetic equivalent instead.
func TestCategoryEditorDeliveryDisabledMigrationColdReopen(t *testing.T) {
	ctx := context.Background()
	config, _ := json.Marshal(map[string]any{"enabled": false, "content": customJapaneseMovieCategory})
	if os.Getenv("LIGHT_MEDIA_CATEGORY_ORIGINAL_STDIN") == "1" {
		var source struct {
			Config     json.RawMessage `json:"config"`
			ConfigSHA  string          `json:"config_sha256"`
			ContentSHA string          `json:"content_sha256"`
			KVCount    int             `json:"kv_count"`
		}
		decoder := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&source) != nil || decoder.Decode(new(any)) != io.EOF || source.KVCount != 0 || categoryDeliveryDigest(source.Config) != source.ConfigSHA {
			t.Fatal("original metadata/input mismatch")
		}
		var fields map[string]json.RawMessage
		var content string
		if json.Unmarshal(source.Config, &fields) != nil || len(fields) != 2 || string(fields["enabled"]) != "false" || json.Unmarshal(fields["content"], &content) != nil || categoryDeliveryDigest([]byte(content)) != source.ContentSHA {
			t.Fatal("original config shape/state mismatch")
		}
		config = source.Config
	}
	// JSON whitespace is not configuration. The actual encrypted store compacts
	// RawMessage, while content bytes (including YAML comments/order) survive.
	config, err := pluginJSON(json.RawMessage(config), true)
	if err != nil {
		t.Fatal("configuration encoding rejected", err)
	}
	digest := categoryDeliveryDigest(config)
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	policyPath := filepath.Join(a.Dir, "category.yaml")
	// A different owned global policy detects any accidental plugin activation.
	sentinel := []byte("movie:\n  独立现行分类:\n")
	if err := os.WriteFile(policyPath, sentinel, 0600); err != nil {
		t.Fatal(err)
	}
	manifest := builtin.CategoryEditorManifest()
	rawManifest, _ := json.Marshal(manifest)
	migration, err := a.pluginStore.BeginMigration(ctx, digest, "CategoryEditor", "CategoryEditor")
	if err != nil {
		t.Fatal(err)
	}
	input := PluginImport{RunID: migration.ID, Installation: PluginInstallation{InstanceID: "CategoryEditor", PluginID: "CategoryEditor", Version: manifest.Version, ManifestSchemaVersion: 1, Manifest: rawManifest}, Config: config, Data: map[string]json.RawMessage{}}
	receipt, err := a.pluginStore.Import(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	assertImported := func(current *App) {
		t.Helper()
		stored, err := current.pluginStore.Config(ctx, "CategoryEditor")
		if err != nil || categoryDeliveryDigest(stored.Value) != digest {
			t.Fatal("import changed config bytes/state", err)
		}
		installation, err := current.pluginStore.Installation(ctx, "CategoryEditor")
		if err != nil || !installation.Installed || installation.DesiredEnabled {
			t.Fatal("disabled import was activated", err)
		}
		var sealed []byte
		if err := current.Store.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id='CategoryEditor' AND kind='config'`).Scan(&sealed); err != nil || bytes.Contains(sealed, []byte("content")) || bytes.Contains(sealed, []byte("enabled")) {
			t.Fatal("import is not encrypted", err)
		}
		for _, query := range []string{`SELECT COUNT(*) FROM plugin_runs`, `SELECT COUNT(*) FROM plugin_values WHERE kind='data'`, `SELECT COUNT(*) FROM plugin_event_subscriptions`, `SELECT COUNT(*) FROM records WHERE kind='notification'`} {
			var count int
			if err := current.Store.DB.QueryRow(query).Scan(&count); err != nil || count != 0 {
				t.Fatal("disabled import produced runtime effects", err)
			}
		}
		policy, err := os.ReadFile(policyPath)
		if err != nil || !bytes.Equal(policy, sentinel) {
			t.Fatal("disabled import changed independent global policy", err)
		}
		form, err := current.pluginForm(ctx, installation)
		if err != nil {
			t.Fatal("actual SDK form failed", err)
		}
		model, _ := json.Marshal(form["model"])
		var got, want any
		if json.Unmarshal(model, &got) != nil || json.Unmarshal(config, &want) != nil {
			t.Fatal("form model decode failed")
		}
		canonicalGot, _ := json.Marshal(got)
		canonicalWant, _ := json.Marshal(want)
		if !bytes.Equal(canonicalGot, canonicalWant) {
			t.Fatal("actual SDK form did not preserve imported config")
		}
		if err := current.initializeNativePlugins(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := current.pluginStore.ClaimRun(ctx); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("disabled import queued startup once", err)
		}
	}
	assertImported(a)
	if again, err := a.pluginStore.Import(ctx, input); err != nil || again != receipt {
		t.Fatal("import retry was not idempotent", err)
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	useBuiltInPluginProcess(t, reopened)
	assertImported(reopened)
	if again, err := reopened.pluginStore.Import(ctx, input); err != nil || again != receipt {
		t.Fatal("cold import retry lost receipt", err)
	}
	if err := reopened.pluginStore.Uninstall(ctx, "CategoryEditor"); err != nil {
		t.Fatal(err)
	}
	kept, err := reopened.pluginStore.Config(ctx, "CategoryEditor")
	if err != nil || categoryDeliveryDigest(kept.Value) != digest {
		t.Fatal("ordinary uninstall removed migrated config", err)
	}
	policy, err := os.ReadFile(policyPath)
	if err != nil || !bytes.Equal(policy, sentinel) {
		t.Fatal("ordinary uninstall changed global category policy", err)
	}
	t.Log("disabled migration: encrypted config preserved; zero KV/runs/events/notices; independent policy unchanged; actual SDK form, idempotent receipt, cold reopen and ordinary uninstall verified")
}

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
