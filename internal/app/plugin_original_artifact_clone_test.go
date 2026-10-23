package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func originalArtifactCloneFixture(t *testing.T) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	binary := os.Getenv("OWNED_MODULE_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("OWNED_MODULE_FIXTURE_BINARY must name the actual owned compiled SDK artifact")
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	manifestRaw, err := exec.CommandContext(ctx, binary, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatalf("actual executable manifest: %v", err)
	}
	var manifest plugins.Manifest
	if err = json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.PluginID != "OwnedModuleFixture" || manifest.Version != "1.0" || len(manifest.Capabilities) != 0 {
		t.Fatalf("unexpected owned manifest: %+v", manifest)
	}
	if err = manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: pluginPureSDKContract}}}}}
	var indexHits, artifactHits, unexpected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			indexHits.Add(1)
			_ = json.NewEncoder(w).Encode(index)
		case "/owned-module-fixture":
			artifactHits.Add(1)
			_, _ = w.Write(payload)
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/owned-module-fixture"
	a := newTestApp(t)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]any{"plugin_id": manifest.PluginID, "repo_url": server.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	in, err := a.pluginStore.Installation(context.Background(), manifest.PluginID)
	if err != nil || !in.Installed || in.Version != manifest.Version {
		t.Fatalf("actual installed artifact: %+v %v", in, err)
	}
	loaded, ok := a.pluginRunningInstances.Load(manifest.PluginID)
	if !ok {
		t.Fatal("artifact installed but not loaded")
	}
	object := loaded.(*originalPluginInstance)
	if !object.external {
		t.Fatal("fixture borrowed builtin implementation")
	}

	return a, cookie, object
}
func originalArtifactSnapshot(t *testing.T, p *originalPluginInstance) map[string]any {
	t.Helper()
	var value map[string]any
	if err := p.readSession(context.Background(), "snapshot", &value); err != nil {
		t.Fatal(err)
	}
	return value
}
func TestPluginOriginalArtifactCloneIsolationAndDeletedCodeRecreation(t *testing.T) {
	a, cookie, sourceObject := originalArtifactCloneFixture(t)
	ctx := context.Background()
	const source = "OwnedModuleFixture"
	const target = "OwnedModuleFixtureisolated"
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+source, `{"enabled":false,"marker":"source","custom_nested":{"owned":7}}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	sourceState := originalArtifactSnapshot(t, sourceObject)
	before, err := a.pluginStore.Config(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	clone := func() {
		t.Helper()
		resp := callTest(t, a.Handler(), "POST", "/api/plugins/"+source+"/clone", `{"suffix":"Isolated"}`, cookie, "")
		assertOriginalRegistrySuccess(t, resp.Code, resp.Body.Bytes())
		a.wg.Wait()
	}
	clone()
	value, ok := a.pluginRunningInstances.Load(target)
	if !ok {
		t.Fatal("clone missing actual process")
	}
	cloneObject := value.(*originalPluginInstance)
	if cloneObject == sourceObject || cloneObject.installation.PluginID != sourceObject.installation.PluginID || cloneObject.installation.InstanceID == sourceObject.installation.InstanceID {
		t.Fatal("clone object or underlying business identity incorrect")
	}
	cloneState := originalArtifactSnapshot(t, cloneObject)
	if cloneState["version"] != "1.0" || cloneState["config"].(map[string]any)["marker"] != "source" {
		t.Fatalf("clone actual state: %+v", cloneState)
	}
	originalCode, err := artifacts.ReadInstalled(a.pluginPackageRoot(), source, validatePluginArtifactContract)
	if err != nil {
		t.Fatal(err)
	}
	copiedCode, err := artifacts.ReadInstalled(a.pluginPackageRoot(), target, validatePluginArtifactContract)
	if err != nil {
		t.Fatal(err)
	}
	sourceFile, err := os.Stat(originalCode.ExecutablePath)
	if err != nil {
		t.Fatal(err)
	}
	targetFile, err := os.Stat(copiedCode.ExecutablePath)
	if err != nil {
		t.Fatal(err)
	}
	if originalCode.SHA256 != copiedCode.SHA256 || os.SameFile(sourceFile, targetFile) {
		t.Fatal("clone code is not physically separate equal bytes")
	}
	r = callTest(t, a.Handler(), "PUT", "/api/plugins/"+target, `{"enabled":false,"marker":"clone-only"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	next := originalArtifactSnapshot(t, cloneObject)
	if next["init_count"].(float64) != cloneState["init_count"].(float64)+1 || next["config"].(map[string]any)["marker"] != "clone-only" {
		t.Fatalf("clone save did not affect same actual object: %+v", next)
	}
	sourceAfter := originalArtifactSnapshot(t, sourceObject)
	oldJSON, _ := json.Marshal(sourceState)
	newJSON, _ := json.Marshal(sourceAfter)
	if !bytes.Equal(oldJSON, newJSON) {
		t.Fatal("clone save mutated source process")
	}
	after, err := a.pluginStore.Config(ctx, source)
	if err != nil || after.Revision != before.Revision || !bytes.Equal(after.Value, before.Value) {
		t.Fatalf("clone save mutated source DB: %v", err)
	}
	first, err := a.pluginStore.Installation(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.SaveData(ctx, target, "history", 0, "owned old state"); err != nil {
		t.Fatal(err)
	}
	r = callTest(t, a.Handler(), "DELETE", "/api/plugins/"+target, `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	if _, err = os.Stat(filepath.Join(a.pluginPackageRoot(), target)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("clone code not actually removed: %v", err)
	}
	if _, ok = a.pluginRunningInstances.Load(target); ok {
		t.Fatal("uninstalled clone process retained")
	}
	clone()
	second, err := a.pluginStore.Installation(ctx, target)
	if err != nil || second.Generation <= first.Generation {
		t.Fatalf("same ID new generation: %v", err)
	}
	if _, err = a.pluginStore.Data(ctx, target, "history"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old clone state revived: %v", err)
	}
	rebuilt, ok := a.pluginRunningInstances.Load(target)
	if !ok || rebuilt == cloneObject {
		t.Fatal("rebuilt clone reused dead object")
	}
	fresh := originalArtifactSnapshot(t, rebuilt.(*originalPluginInstance))
	if fresh["config"].(map[string]any)["marker"] != "source" || fresh["failure_count"] != float64(0) {
		t.Fatalf("rebuilt clone retained old state: %+v", fresh)
	}
	finalSource := originalArtifactSnapshot(t, sourceObject)
	newJSON, _ = json.Marshal(finalSource)
	if !bytes.Equal(oldJSON, newJSON) {
		t.Fatal("delete/recreate changed source object")
	}
}
