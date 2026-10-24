package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestPluginOriginalArtifactSourceUpgradeKeepsCopiedCloneVersion(t *testing.T) {
	a, cookie, sourceObject := originalArtifactCloneFixture(t)
	const source = "OwnedModuleFixture"
	const clone = "OwnedModuleFixtureversioned"
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+source, `{"enabled":false,"marker":"before-upgrade"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	r = callTest(t, a.Handler(), "POST", "/api/plugins/"+source+"/clone", `{"suffix":"Versioned"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	oldClone, ok := a.pluginRunningInstances.Load(clone)
	if !ok {
		t.Fatal("actual clone not loaded")
	}
	before := originalArtifactSnapshot(t, oldClone.(*originalPluginInstance))
	cloneCode, err := artifacts.ReadInstalled(a.pluginPackageRoot(), clone, validatePluginArtifactContract)
	if err != nil {
		t.Fatal(err)
	}
	cloneCfg, err := a.pluginStore.Config(context.Background(), clone)
	if err != nil {
		t.Fatal(err)
	}
	binary := os.Getenv("OWNED_MODULE_FIXTURE_BINARY_V2")
	if binary == "" {
		t.Fatal("OWNED_MODULE_FIXTURE_BINARY_V2 must name actual version2 artifact")
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	raw, err := exec.CommandContext(ctx, binary, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.PluginID != source || manifest.Version != "2.0" || len(manifest.Capabilities) != 0 {
		t.Fatalf("wrong actual version2 manifest: %s", raw)
	}
	hash := sha256.Sum256(payload)
	digest := hex.EncodeToString(hash[:])
	if digest == cloneCode.SHA256 {
		t.Fatal("version2 did not use different binary bytes")
	}
	index := artifacts.Index{SchemaVersion: 1, PluginID: source, CurrentVersion: "2.0", Releases: []artifacts.Release{{Version: "2.0", Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: pluginPureSDKContract}}}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/binary":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	index.Releases[0].Artifacts[0].URL = server.URL + "/binary"
	request, _ := json.Marshal(map[string]any{"plugin_id": source, "repo_url": server.URL + "/index", "force": true, "release_version": "2.0"})
	r = callTest(t, a.Handler(), "POST", "/api/plugins/install", string(request), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	current, ok := a.pluginRunningInstances.Load(source)
	if !ok || current == sourceObject {
		t.Fatal("upgrade did not replace actual source process")
	}
	upgraded := originalArtifactSnapshot(t, current.(*originalPluginInstance))
	if upgraded["version"] != "2.0" || upgraded["init_count"] != float64(1) {
		t.Fatalf("real upgraded process: %+v", upgraded)
	}
	var old map[string]any
	if err = sourceObject.readSession(context.Background(), "snapshot", &old); err == nil {
		t.Fatal("old source still callable")
	}
	newClone, ok := a.pluginRunningInstances.Load(clone)
	if !ok || newClone != oldClone {
		t.Fatal("source upgrade replaced clone process")
	}
	after := originalArtifactSnapshot(t, newClone.(*originalPluginInstance))
	assertOriginalArtifactOnlyDisabledStateObserved(t, before, after)
	if after["version"] != "1.0" {
		t.Fatal("source upgrade changed clone version", after)
	}
	codeAfter, err := artifacts.ReadInstalled(a.pluginPackageRoot(), clone, validatePluginArtifactContract)
	if err != nil || codeAfter.SHA256 != cloneCode.SHA256 || codeAfter.Manifest.Version != "1.0" {
		t.Fatalf("copied code changed: %v", err)
	}
	actualSource, err := artifacts.ReadInstalled(a.pluginPackageRoot(), source, validatePluginArtifactContract)
	if err != nil || actualSource.SHA256 != digest || actualSource.Manifest.Version != "2.0" {
		t.Fatalf("source artifact not actually replaced: %v", err)
	}
	cfgAfter, err := a.pluginStore.Config(context.Background(), clone)
	if err != nil || cfgAfter.Revision != cloneCfg.Revision || !bytes.Equal(cfgAfter.Value, cloneCfg.Value) {
		t.Fatalf("upgrade changed clone DB: %v", err)
	}
}
