package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
func TestPluginOriginalArtifactForceDownloadFailureKeepsLoadedObjectUntilReload(t *testing.T) {
	a, cookie, object := originalArtifactCloneFixture(t)
	ctx := context.Background()
	const id = "OwnedModuleFixture"
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, `{"enabled":false,"marker":"before-force-failure"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	before, err := a.pluginStore.Installation(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := a.pluginStore.Config(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := originalArtifactSnapshot(t, object)
	snapshotRaw, _ := json.Marshal(snapshot)
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
	index := artifacts.Index{SchemaVersion: 1, PluginID: id, CurrentVersion: installed.Manifest.Version, Releases: []artifacts.Release{{Version: installed.Manifest.Version, Manifest: installed.Manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", Size: int64(len(payload)), SHA256: digest, ExecutableSHA256: digest, HostContract: pluginPureSDKContract}}}}}
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
	retained := originalArtifactSnapshot(t, object)
	retainedRaw, _ := json.Marshal(retained)
	if !bytes.Equal(snapshotRaw, retainedRaw) {
		t.Fatal("force failure altered live object state")
	}
	// No disk-based fallback is needed: use the already-bound live session only.
	r = callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, `{"enabled":false,"marker":"saved-on-retained-object"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	current, ok = a.pluginRunningInstances.Load(id)
	if !ok || current != object {
		t.Fatal("save silently replaced retained object")
	}
	saved := originalArtifactSnapshot(t, object)
	if saved["init_count"].(float64) != snapshot["init_count"].(float64)+1 || saved["config"].(map[string]any)["marker"] != "saved-on-retained-object" {
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
	var stale map[string]any
	if err = object.readSession(ctx, "snapshot", &stale); err == nil {
		t.Fatal("old session survived explicit reload")
	}
}
