package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins/artifacts"
)

// Original endpoint returns immediately on acquisition failure, before reload.
// Force skips backup, so prepare failure removes code but not the loaded object.
func TestOriginalStandardForceFailureThenMissingSourceReload(t *testing.T) {
	a, cookie, object := ownedStandardSDKFixture(t)
	ctx := context.Background()
	const id = ownedStandardFixtureID
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, `{"enabled":false,"key":"before-force-failure"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	before, err := a.pluginStore.Installation(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := a.pluginStore.Config(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := ownedStandardFailureSnapshot(t, object)

	installed, err := artifacts.ReadInstalled(a.pluginPackageRoot(), id, validatePluginArtifactContract)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(installed.ExecutablePath)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(payload)
	digest := hex.EncodeToString(hash[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: id, CurrentVersion: installed.Manifest.Version, Releases: []artifacts.Release{{Version: installed.Manifest.Version, Manifest: installed.Manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", Size: int64(len(payload)), SHA256: digest, ExecutableSHA256: digest, HostContract: "light-media.sdk-base/v1"}}}}}
	var downloadHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/broken":
			downloadHits.Add(1)
			http.Error(w, "owned prepare failure", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	index.Releases[0].Artifacts[0].URL = server.URL + "/broken"
	request, _ := json.Marshal(map[string]any{"plugin_id": id, "repo_url": server.URL + "/index", "force": true})
	r = callTest(t, a.Handler(), "POST", "/api/plugins/install", string(request), cookie, "")
	var response struct {
		Data struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		} `json:"data"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &response) != nil || response.Data.Success || response.Data.Message == "" {
		t.Fatalf("force prepare failure falsely succeeded: %d %s", r.Code, r.Body.String())
	}
	if downloadHits.Load() != 1 {
		t.Fatalf("did not reach actual download failure: %d", downloadHits.Load())
	}
	if _, err = artifacts.ReadInstalled(a.pluginPackageRoot(), id, validatePluginArtifactContract); err == nil {
		t.Fatal("failed force retained a valid code artifact")
	}
	if _, err = os.Stat(filepath.Join(a.pluginPackageRoot(), id)); !os.IsNotExist(err) {
		t.Fatalf("force cleanup did not remove owned package: %v", err)
	}
	after, err := a.pluginStore.Installation(ctx, id)
	if err != nil || after.Installed != before.Installed || after.Version != before.Version || after.Generation != before.Generation {
		t.Fatalf("failed install changed installed identity: %+v %v", after, err)
	}
	afterCfg, err := a.pluginStore.Config(ctx, id)
	if err != nil || afterCfg.Revision != cfg.Revision || !bytes.Equal(afterCfg.Value, cfg.Value) {
		t.Fatalf("failure changed config: %v", err)
	}
	current, ok := a.pluginRunningInstances.Load(id)
	if !ok || current != object {
		t.Fatal("force failure reloaded/stopped original object")
	}
	retained := ownedStandardFailureSnapshot(t, object)
	if retained["nonce"] != snapshot["nonce"] || retained["init_count"] != snapshot["init_count"] {
		t.Fatal("force failure altered live object state")
	}
	// No disk-based fallback is needed: use the already-bound live session only.
	r = callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, `{"enabled":false,"key":"saved-on-retained-object"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	current, ok = a.pluginRunningInstances.Load(id)
	if !ok || current != object {
		t.Fatal("save silently replaced retained object")
	}
	saved := ownedStandardFailureSnapshot(t, object)
	if saved["init_count"].(float64) != snapshot["init_count"].(float64)+1 || saved["config"].(map[string]any)["key"] != "saved-on-retained-object" {
		t.Fatalf("retained object failed actual init: %+v", saved)
	}
	// Explicit reload is different: stop the old object, then no valid directory
	// exists to load. Original endpoint still returns success with no object.
	r = callTest(t, a.Handler(), "POST", "/api/plugins/"+id+"/reload", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	if _, ok = a.pluginRunningInstances.Load(id); ok {
		t.Fatal("reload synthesized object without valid code")
	}
	if _, exists := a.pluginOriginalClasses.Load(id); exists {
		t.Fatal("missing source class survived reload")
	}
	r = callTest(t, a.Handler(), "GET", "/api/plugins/"+id+"/page", "", cookie, "")
	if r.Code != 404 {
		t.Fatalf("missing source page unexpectedly available: %d %s", r.Code, r.Body.String())
	}
	if _, exists := a.pluginRunningInstances.Load(id); exists {
		t.Fatal("read revived missing source")
	}
	var stale map[string]any
	if err = object.readSession(ctx, "snapshot", &stale); err == nil {
		t.Fatal("old session survived explicit reload")
	}
}

func ownedStandardFailureSnapshot(t *testing.T, p *originalPluginInstance) map[string]any {
	t.Helper()
	var result map[string]any
	if err := p.readSession(context.Background(), "snapshot", &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestOriginalStandardSaveInitFailureRetainsPartial(t *testing.T) {
	a, cookie, p := ownedStandardSDKFixture(t)
	before := ownedStandardFailureSnapshot(t, p)
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+ownedStandardFixtureID, `{"enabled":false,"key":"partial","fail_init":true}`, cookie, "")
	if r.Code != 500 {
		t.Fatalf("real Init exception: %d %s", r.Code, r.Body.String())
	}
	loaded, ok := a.pluginRunningInstances.Load(ownedStandardFixtureID)
	if !ok || loaded != p {
		t.Fatal("failed same-object Init replaced or removed object")
	}
	after := ownedStandardFailureSnapshot(t, p)
	if after["nonce"] != before["nonce"] || after["init_count"].(float64) != before["init_count"].(float64)+1 {
		t.Fatal("actual partial members lost", after)
	}
	cfg, err := a.pluginStore.Config(context.Background(), ownedStandardFixtureID)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if json.Unmarshal(cfg.Value, &config) != nil || config["fail_init"] != true || config["key"] != "partial" {
		t.Fatal("save rolled back", string(cfg.Value))
	}
	if after["init_input"].(map[string]any)["fail_init"] != true || after["config"].(map[string]any)["key"] != "partial" || after["value"] != "partial" {
		t.Fatal("real init/Base partial effects lost", after)
	}
	var runs int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatal("legacy runs", runs, err)
	}
}

func TestOriginalStandardCloseFailureSkipsServiceButReaps(t *testing.T) {
	a, cookie, p := ownedStandardSDKFixture(t)
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+ownedStandardFixtureID, `{"enabled":false,"fail_close":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	before := ownedStandardFailureSnapshot(t, p)
	p.sessionMu.Lock()
	transport := p.session
	p.sessionMu.Unlock()
	if transport == nil {
		t.Fatal("actual session absent")
	}
	r = callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedStandardFixtureID+"/reload", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	raw, err := a.pluginStore.Data(context.Background(), ownedStandardFixtureID, "owned_stop")
	if err != nil {
		t.Fatal(err)
	}
	var stopped struct {
		Nonce int      `json:"nonce"`
		Order []string `json:"order"`
	}
	if json.Unmarshal(raw.Value, &stopped) != nil || float64(stopped.Nonce) != before["nonce"] || len(stopped.Order) != 2 || stopped.Order[0] != "close" || stopped.Order[1] != "stop" {
		t.Fatal("close failure ordering", string(raw.Value))
	}
	if _, err = a.pluginStore.Data(context.Background(), ownedStandardFixtureID, "owned_stop_service"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("stop_service ran after close failure", err)
	}
	p.sessionMu.Lock()
	released := p.session == nil
	p.sessionMu.Unlock()
	if !released {
		t.Fatal("old transport retained")
	}
	if err = transport.Stop(context.Background()); err != nil {
		t.Fatal("old transport terminal", err)
	}
	loaded, ok := a.pluginRunningInstances.Load(ownedStandardFixtureID)
	if !ok || loaded == p {
		t.Fatal("reload did not create fresh object")
	}
	after := ownedStandardFailureSnapshot(t, loaded.(*originalPluginInstance))
	if after["nonce"] == before["nonce"] || after["init_count"] != float64(1) {
		t.Fatal("old process reused", after)
	}
}
