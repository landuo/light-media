package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

const ownedBaseFixtureID = "OwnedBaseFixture"

// A distinct actual executable and sdk-base metadata, never relabelled pure
// fixture bytes. Installs via authenticated App HTTP and its artifact reader.
func originalBaseSDKFixture(t *testing.T) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	binary := os.Getenv("OWNED_BASE_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("OWNED_BASE_FIXTURE_BINARY must name the actual owned compiled Base SDK worker")
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	manifestRaw, err := exec.CommandContext(ctx, binary, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatalf("actual Base manifest %v", err)
	}
	var manifest plugins.Manifest
	if err = json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.PluginID != ownedBaseFixtureID || manifest.Version != "1.0" || !slices.Equal(manifest.Capabilities, plugins.BaseMethods()) {
		t.Fatalf("wrong executable/capabilities %+v", manifest)
	}
	if err = manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(payload)
	digest := hex.EncodeToString(hash[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: "light-media.sdk-base/v1"}}}}}
	var indexHits, artifactHits, unexpected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			indexHits.Add(1)
			_ = json.NewEncoder(w).Encode(index)
		case "/owned-base-fixture":
			artifactHits.Add(1)
			_, _ = w.Write(payload)
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/owned-base-fixture"
	a := newTestApp(t)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]any{"plugin_id": manifest.PluginID, "repo_url": server.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	in, err := a.pluginStore.Installation(context.Background(), ownedBaseFixtureID)
	if err != nil || !in.Installed || in.Version != "1.0" {
		t.Fatalf("actual Base installation %+v %v", in, err)
	}
	value, ok := a.pluginRunningInstances.Load(ownedBaseFixtureID)
	if !ok {
		t.Fatal("real Base worker not loaded")
	}
	object := value.(*originalPluginInstance)
	if !object.external {
		t.Fatal("Base fixture substituted builtin")
	}
	if indexHits.Load() != 1 || artifactHits.Load() != 1 || unexpected.Load() != 0 {
		t.Fatalf("fixture acquisition %d %d %d", indexHits.Load(), artifactHits.Load(), unexpected.Load())
	}
	return a, cookie, object
}

type originalBaseSDKSnapshot struct {
	InitCount    int                   `json:"init_count"`
	InitInput    json.RawMessage       `json:"init_input"`
	InitAfter    json.RawMessage       `json:"init_after"`
	UpdateResult *bool                 `json:"update_result"`
	Config       json.RawMessage       `json:"config"`
	Value        json.RawMessage       `json:"value"`
	Rows         []plugins.BaseDataRow `json:"rows"`
	Path         string                `json:"path"`
	StaleDenied  bool                  `json:"stale_denied"`
}

func readOriginalBaseSDKSnapshot(t *testing.T, p *originalPluginInstance) originalBaseSDKSnapshot {
	t.Helper()
	var result originalBaseSDKSnapshot
	// This is the actual manager session path with its freshly bound production
	// HostHandler. No custom handler, scope validator, run or receipt is injected.
	if err := p.readSession(context.Background(), "snapshot", &result); err != nil {
		t.Fatalf("actual Base snapshot %v", err)
	}
	if !result.StaleDenied {
		t.Fatal("previous SDK callback remained active")
	}
	for _, row := range result.Rows {
		if row.Key == "owned_stale_must_not_exist" {
			t.Fatal("expired callback wrote actual data")
		}
	}
	return result
}
func applyOriginalBaseSDKPlan(t *testing.T, a *App, cookie *http.Cookie, instance string, plan map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"enabled": false, "owned_plan": plan})
	if err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+instance, string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
}
func assertOriginalBaseJSON(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	if !originalBaseJSONEqual(got, json.RawMessage(want)) {
		t.Fatalf("JSON got %s want %s", got, want)
	}
}
func originalBaseSDKLoaded(t *testing.T, a *App, id string) *originalPluginInstance {
	t.Helper()
	value, ok := a.pluginRunningInstances.Load(id)
	if !ok {
		t.Fatalf("missing loaded instance %s", id)
	}
	return value.(*originalPluginInstance)
}

func TestOriginalBaseSDKConfigUpdateThenSameCallRead(t *testing.T) {
	a, cookie, object := originalBaseSDKFixture(t)
	applyOriginalBaseSDKPlan(t, a, cookie, ownedBaseFixtureID, map[string]any{"config": map[string]any{"enabled": false, "marker": "replacement"}, "key": "shared", "value": map[string]any{"nested": []any{false, 0, nil}}})
	got := readOriginalBaseSDKSnapshot(t, object)
	assertOriginalBaseJSON(t, got.InitAfter, `{"enabled":false,"marker":"replacement"}`)
	assertOriginalBaseJSON(t, got.Config, `{"enabled":false,"marker":"replacement"}`)
	assertOriginalBaseJSON(t, got.Value, `{"nested":[false,0,null]}`)
	if got.UpdateResult == nil || !*got.UpdateResult {
		t.Fatal("source set result not true")
	}
	if got.InitCount != 2 {
		t.Fatalf("base.update_config reinitialized itself: %d", got.InitCount)
	}
	if info, err := os.Stat(got.Path); err != nil || !info.IsDir() {
		t.Fatalf("actual data path %s %v", got.Path, err)
	}
}

func TestOriginalBaseSDKCloneDefaultsAndExplicitNamespaces(t *testing.T) {
	a, cookie, source := originalBaseSDKFixture(t)
	applyOriginalBaseSDKPlan(t, a, cookie, ownedBaseFixtureID, map[string]any{"config": map[string]any{"marker": "source", "enabled": false}, "key": "shared", "value": "source"})
	sourceBefore := readOriginalBaseSDKSnapshot(t, source)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedBaseFixtureID+"/clone", `{"suffix":"Twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	cloneID := ownedBaseFixtureID + "twin"
	clone := originalBaseSDKLoaded(t, a, cloneID)
	applyOriginalBaseSDKPlan(t, a, cookie, cloneID, map[string]any{"config": map[string]any{"marker": "clone", "enabled": false}, "key": "shared", "value": "clone"})
	sourceAfter := readOriginalBaseSDKSnapshot(t, source)
	cloneAfter := readOriginalBaseSDKSnapshot(t, clone)
	assertOriginalBaseJSON(t, sourceAfter.Value, `"source"`)
	assertOriginalBaseJSON(t, cloneAfter.Value, `"clone"`)
	if sourceAfter.Path == cloneAfter.Path || sourceAfter.Path != sourceBefore.Path {
		t.Fatal("default clone directory is not isolated")
	}
	if sourceAfter.InitCount != sourceBefore.InitCount {
		t.Fatal("clone init affected source object")
	}
	// Source-compatible explicit override deliberately targets another namespace;
	// it must NOT be silently rewritten back to the caller clone.
	applyOriginalBaseSDKPlan(t, a, cookie, cloneID, map[string]any{"target": ownedBaseFixtureID, "config": map[string]any{"marker": "explicit-source", "enabled": false}, "key": "shared", "value": "explicit-source"})
	sourceExplicit := readOriginalBaseSDKSnapshot(t, source)
	cloneExplicit := readOriginalBaseSDKSnapshot(t, clone)
	assertOriginalBaseJSON(t, sourceExplicit.Value, `"explicit-source"`)
	assertOriginalBaseJSON(t, cloneExplicit.Value, `"clone"`)
	assertOriginalBaseJSON(t, sourceExplicit.Config, `{"marker":"explicit-source","enabled":false}`)
	if sourceExplicit.InitCount != sourceBefore.InitCount {
		t.Fatal("explicit base update unexpectedly initialized target")
	}
}

func TestOriginalBaseSDKUnknownNamespaceWithoutInstallation(t *testing.T) {
	a, cookie, object := originalBaseSDKFixture(t)
	ctx := context.Background()
	target := "OwnedUninstalledBaseNamespace"
	if _, err := a.pluginStore.Installation(ctx, target); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("target unexpectedly installed: %v", err)
	}
	applyOriginalBaseSDKPlan(t, a, cookie, ownedBaseFixtureID, map[string]any{"target": target, "config": map[string]any{"marker": "unknown"}, "key": "shared", "value": 0})
	got := readOriginalBaseSDKSnapshot(t, object)
	assertOriginalBaseJSON(t, got.InitAfter, `{"marker":"unknown"}`)
	assertOriginalBaseJSON(t, got.Value, `null`)
	config, err := a.pluginStore.Config(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	assertOriginalBaseJSON(t, config.Value, `{"marker":"unknown"}`)
	value, err := a.pluginStore.Data(ctx, target, "shared")
	if err != nil {
		t.Fatal(err)
	}
	assertOriginalBaseJSON(t, value.Value, `0`)
	if _, err = a.pluginStore.Installation(ctx, target); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("SDK write fabricated installation: %v", err)
	}
}

func TestOriginalBaseSDKEmptyCacheReloadResetAndFreshStop(t *testing.T) {
	a, cookie, object := originalBaseSDKFixture(t)
	ctx := context.Background()
	applyOriginalBaseSDKPlan(t, a, cookie, ownedBaseFixtureID, map[string]any{"config": map[string]any{}, "key": "shared", "value": "before-reset"})
	empty := readOriginalBaseSDKSnapshot(t, object)
	assertOriginalBaseJSON(t, empty.Config, `{}`)
	if _, err := a.pluginStore.Config(ctx, ownedBaseFixtureID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("source falsy set did not delete DB row: %v", err)
	}
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedBaseFixtureID+"/reload", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	reloaded := originalBaseSDKLoaded(t, a, ownedBaseFixtureID)
	if reloaded == object {
		t.Fatal("reload reused stopped object")
	}
	afterReload := readOriginalBaseSDKSnapshot(t, reloaded)
	assertOriginalBaseJSON(t, afterReload.InitInput, `{}`)
	assertOriginalBaseJSON(t, afterReload.Config, `{}`)
	stopped, err := a.pluginStore.Data(ctx, ownedBaseFixtureID, "owned_stop")
	if err != nil {
		t.Fatalf("fresh stop callback failed: %v", err)
	}
	var stop map[string]any
	if err = json.Unmarshal(stopped.Value, &stop); err != nil || stop["stale_denied"] != true {
		t.Fatalf("old callback not denied during fresh stop: %s %v", stopped.Value, err)
	}
	var old originalBaseSDKSnapshot
	if err = object.readSession(ctx, "snapshot", &old); err == nil {
		t.Fatal("old object callback survived reload")
	}
	response = callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedBaseFixtureID+"/reset", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	reset := originalBaseSDKLoaded(t, a, ownedBaseFixtureID)
	afterReset := readOriginalBaseSDKSnapshot(t, reset)
	assertOriginalBaseJSON(t, afterReset.InitInput, `null`)
	assertOriginalBaseJSON(t, afterReset.Config, `null`)
	assertOriginalBaseJSON(t, afterReset.Value, `null`)
	if len(afterReset.Rows) != 0 {
		t.Fatalf("reset retained data or fresh stop marker %+v", afterReset.Rows)
	}
	if afterReset.Path != empty.Path {
		t.Fatal("reset changed/deleted data directory namespace")
	}
	if err = reloaded.readSession(ctx, "snapshot", &old); err == nil {
		t.Fatal("old callback object survived reset")
	}
}

func TestOriginalBaseSDKKeyedAndAllDataDeletion(t *testing.T) {
	a, cookie, object := originalBaseSDKFixture(t)
	applyOriginalBaseSDKPlan(t, a, cookie, ownedBaseFixtureID, map[string]any{"key": "shared", "value": false})
	assertOriginalBaseJSON(t, readOriginalBaseSDKSnapshot(t, object).Value, `false`)
	applyOriginalBaseSDKPlan(t, a, cookie, ownedBaseFixtureID, map[string]any{"delete": "shared"})
	assertOriginalBaseJSON(t, readOriginalBaseSDKSnapshot(t, object).Value, `null`)
	for _, key := range []string{"one", "two"} {
		applyOriginalBaseSDKPlan(t, a, cookie, ownedBaseFixtureID, map[string]any{"key": key, "value": map[string]any{"owned": true}})
	}
	before := readOriginalBaseSDKSnapshot(t, object)
	if len(before.Rows) != 2 {
		t.Fatalf("actual all-data rows %+v", before.Rows)
	}
	applyOriginalBaseSDKPlan(t, a, cookie, ownedBaseFixtureID, map[string]any{"delete": ""})
	if got := readOriginalBaseSDKSnapshot(t, object); len(got.Rows) != 0 {
		t.Fatalf("empty key did not delete all own data %+v", got.Rows)
	}
}

func TestOriginalBaseSDKResetSecondFailureStillInvalidatesConfigCache(t *testing.T) {
	a, cookie, object := originalBaseSDKFixture(t)
	ctx := context.Background()
	applyOriginalBaseSDKPlan(t, a, cookie, ownedBaseFixtureID, map[string]any{"config": map[string]any{"marker": "cached-before-reset"}, "key": "shared", "value": "must-remain"})
	readOriginalBaseSDKSnapshot(t, object)
	// Real temporary owned App DB fault at the second original reset operation.
	// No installation, admission, proof or success result is manufactured.
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_base_reset_data_failure BEFORE DELETE ON plugin_values WHEN OLD.instance_id='OwnedBaseFixture' AND OLD.kind='data' BEGIN SELECT RAISE(ABORT,'owned base data delete failure'); END`); err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedBaseFixtureID+"/reset", `{}`, cookie, "")
	if response.Code < 400 {
		t.Fatalf("real second-phase reset failure hidden: %d %s", response.Code, response.Body.String())
	}
	if _, err := a.pluginStore.Config(ctx, ownedBaseFixtureID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("config deletion rolled back/skipped: %v", err)
	}
	value, err := a.pluginStore.Data(ctx, ownedBaseFixtureID, "shared")
	if err != nil {
		t.Fatal(err)
	}
	assertOriginalBaseJSON(t, value.Value, `"must-remain"`)
	// A later actual reload must read missing config, not the old cache, despite
	// the previous reset returning failure before its final data phase committed.
	response = callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedBaseFixtureID+"/reload", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	fresh := readOriginalBaseSDKSnapshot(t, originalBaseSDKLoaded(t, a, ownedBaseFixtureID))
	assertOriginalBaseJSON(t, fresh.InitInput, `null`)
	assertOriginalBaseJSON(t, fresh.Config, `null`)
}
