package app

import (
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
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func ownedSyncRepository(t *testing.T, mutate func(*artifacts.Index)) (string, *atomic.Int32, plugins.Manifest) {
	t.Helper()
	binary := os.Getenv("OWNED_MODULE_OVERRIDE_V2_BINARY")
	if binary == "" {
		t.Fatal("OWNED_MODULE_OVERRIDE_V2_BINARY must name the real owned-module-override built with -X main.version=2.0")
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(binary, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.PluginID != ownedModuleID || manifest.Version != "2.0" {
		t.Fatal("wrong v2 worker", string(raw))
	}
	hash := sha256.Sum256(payload)
	digest := hex.EncodeToString(hash[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: "light-media.sdk-base/v1"}}}}}
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			hits.Add(1)
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/worker"
	if mutate != nil {
		mutate(&index)
	}
	return server.URL + "/index", &hits, index.Releases[0].Manifest
}

func setOwnedSyncMarkets(t *testing.T, a *App, sources ...string) {
	t.Helper()
	settings := a.Settings()
	settings.PluginMarkets = sources
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
}

func TestOriginalPluginSyncUpgradeRepairAndNoop(t *testing.T) {
	a, cookie, source := ownedModuleFixture(t, true)
	v1, err := artifacts.ReadInstalled(a.pluginPackageRoot(), ownedModuleID, validatePluginArtifactContract)
	if err != nil {
		t.Fatal(err)
	}
	saveOwnedModule(t, a, cookie, source, `{"enabled":true,"modules":{"fetch":"module_execute"},"result":[1]}`)
	clone := ownedModuleClone(t, a, cookie, "copy")
	saveOwnedModule(t, a, cookie, clone, `{"enabled":false,"result":[99]}`)
	cloneBefore, err := artifacts.ReadInstalled(a.pluginPackageRoot(), clone.installation.InstanceID, validatePluginArtifactContract)
	if err != nil {
		t.Fatal(err)
	}
	sourceConfig, _ := a.pluginStore.Config(context.Background(), ownedModuleID)
	cloneConfig, _ := a.pluginStore.Config(context.Background(), clone.installation.InstanceID)
	repository, hits, _ := ownedSyncRepository(t, nil)
	setOwnedSyncMarkets(t, a, repository)
	a.synchronizeOriginalPluginsAtStartup(context.Background())
	value, ok := a.pluginRunningInstances.Load(ownedModuleID)
	if !ok || value.(*originalPluginInstance).manifest.Version != "2.0" || value == source || hits.Load() != 1 {
		t.Fatal("actual startup sync did not load v2", ok, hits.Load())
	}
	cloneAfter, err := artifacts.ReadInstalled(a.pluginPackageRoot(), clone.installation.InstanceID, validatePluginArtifactContract)
	if err != nil || cloneAfter.SHA256 != cloneBefore.SHA256 || cloneAfter.Manifest.Version != "1.0" {
		t.Fatal("primary sync replaced copied clone", cloneAfter, err)
	}
	for id, prior := range map[string]PluginStoredValue{ownedModuleID: sourceConfig, clone.installation.InstanceID: cloneConfig} {
		current, err := a.pluginStore.Config(context.Background(), id)
		if err != nil || !reflect.DeepEqual(current, prior) {
			t.Fatal("sync changed configuration", id, current, err)
		}
	}
	readOriginalInstalledIDs(t, a, cookie, []string{ownedModuleID, clone.installation.InstanceID})
	stable := value
	a.synchronizeOriginalPluginsAtStartup(context.Background())
	if current, _ := a.pluginRunningInstances.Load(ownedModuleID); current != stable || hits.Load() != 1 {
		t.Fatal("current code was reinstalled/reinitialized")
	}
	setOwnedSyncMarkets(t, a, v1.RepositoryURL)
	a.synchronizeOriginalPluginsAtStartup(context.Background())
	if current, _ := a.pluginRunningInstances.Load(ownedModuleID); current != stable {
		t.Fatal("lower market version downgraded live plugin")
	}
	setOwnedSyncMarkets(t, a, repository)
	if err = a.stopOriginalPlugin(context.Background(), ownedModuleID); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(a.pluginPackageRoot(), ownedModuleID, "owned-module-override")); err != nil {
		t.Fatal(err)
	}
	a.synchronizeOriginalPluginsAtStartup(context.Background())
	if current, ok := a.pluginRunningInstances.Load(ownedModuleID); !ok || current.(*originalPluginInstance).manifest.Version != "2.0" || hits.Load() != 2 {
		t.Fatal("missing executable was not repaired", ok, hits.Load())
	}
}

func TestOriginalPluginSyncFailureIsolationAndInstalledSelection(t *testing.T) {
	a, cookie, source := ownedModuleFixture(t, true)
	good, hits, _ := ownedSyncRepository(t, nil)
	bad, badHits, badManifest := ownedSyncRepository(t, func(index *artifacts.Index) {
		index.PluginID = "OwnedMissingRepair"
		index.Releases[0].Manifest.PluginID = index.PluginID
		index.Releases[0].Artifacts[0].SHA256 = strings.Repeat("0", 64)
	})
	// A persisted installation whose prior code is gone is deliberately seeded;
	// the attempted repair uses a real HTTP artifact and must fail its hash gate.
	raw, _ := json.Marshal(badManifest)
	if err := a.pluginStore.Install(context.Background(), PluginInstallation{InstanceID: badManifest.PluginID, PluginID: badManifest.PluginID, Version: "2.0", ManifestSchemaVersion: 1, Manifest: raw}); err != nil {
		t.Fatal(err)
	}
	unreachable := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(unreachable.Close)
	setOwnedSyncMarkets(t, a, unreachable.URL+"/missing", bad, good)
	a.pluginOriginalOperationMu.Lock()
	synced, err := a.syncOriginalPlugins(context.Background())
	a.pluginOriginalOperationMu.Unlock()
	if err != nil || !reflect.DeepEqual(synced, []string{ownedModuleID}) || hits.Load() != 1 || badHits.Load() != 1 {
		t.Fatal("failed source hid success or counted success", synced, err, hits.Load(), badHits.Load())
	}
	if current, _ := a.pluginRunningInstances.Load(ownedModuleID); current != source {
		t.Fatal("sync helper performed manager init itself")
	}
	if _, ok := a.pluginRunningInstances.Load(badManifest.PluginID); ok {
		t.Fatal("invalid repair started a worker")
	}
	in, err := a.pluginStore.Installation(context.Background(), badManifest.PluginID)
	if err != nil || !in.Installed {
		t.Fatal("failed repair uninstalled entry", in, err)
	}
	if _, err = artifacts.ReadInstalled(a.pluginPackageRoot(), badManifest.PluginID, validatePluginArtifactContract); err == nil {
		t.Fatal("failed repair published code")
	}
	// Once uninstalled, even a newer compatible candidate is never acquired.
	r := callTest(t, a.Handler(), "DELETE", "/api/plugins/"+ownedModuleID, "", cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	setOwnedSyncMarkets(t, a, good)
	a.synchronizeOriginalPluginsAtStartup(context.Background())
	if _, ok := a.pluginRunningInstances.Load(ownedModuleID); ok || hits.Load() != 1 {
		t.Fatal("startup installed unrequested marketplace item", hits.Load())
	}
	// A repository whose current release cannot run here is not a repair target.
	incompatible, incompatibleHits, _ := ownedSyncRepository(t, func(index *artifacts.Index) {
		index.PluginID = badManifest.PluginID
		index.Releases[0].Manifest.PluginID = badManifest.PluginID
		otherArch := "arm64"
		if runtime.GOARCH == otherArch {
			otherArch = "amd64"
		}
		index.Releases[0].Artifacts[0].Platform = runtime.GOOS + "-" + otherArch
	})
	setOwnedSyncMarkets(t, a, incompatible)
	a.synchronizeOriginalPluginsAtStartup(context.Background())
	if incompatibleHits.Load() != 0 {
		t.Fatal("incompatible artifact downloaded")
	}
}

func TestOriginalPluginSyncActualStartAndCloseCancellation(t *testing.T) {
	a, _, _ := ownedModuleFixture(t, true)
	repository, _, _ := ownedSyncRepository(t, nil)
	setOwnedSyncMarkets(t, a, repository)
	a.Start(context.Background())
	deadline := time.Now().Add(15 * time.Second)
	for {
		value, ok := a.pluginRunningInstances.Load(ownedModuleID)
		if ok && value.(*originalPluginInstance).manifest.Version == "2.0" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("real App.Start did not synchronize")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.pluginRunningInstances.Load(ownedModuleID); ok {
		t.Fatal("Close retained startup worker")
	}
	// This second app blocks repository lookup, proving Close cancels the actual
	// startup request before joining the shared mutation lock and worker drain.
	b, _, _ := ownedModuleFixture(t, true)
	entered := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { once.Do(func() { close(entered) }); <-r.Context().Done() }))
	t.Cleanup(server.Close)
	setOwnedSyncMarkets(t, b, server.URL+"/index")
	b.Start(context.Background())
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("startup repository call did not begin")
	}
	done := make(chan error, 1)
	go func() { done <- b.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Close did not cancel/join startup synchronization")
	}
	if _, ok := b.pluginRunningInstances.Load(ownedModuleID); ok {
		t.Fatal("worker restarted after Close")
	}
}

func TestOriginalPluginMutationsRejectSealedShutdown(t *testing.T) {
	a, cookie, source := ownedModuleFixture(t, true)
	before := ownedModuleRead(t, source)
	a.closeOriginalBackgroundAdmission()
	for _, request := range []struct{ method, path, body string }{
		{"POST", "/api/plugins/install", `{"plugin_id":"OwnedModuleOverride"}`},
		{"DELETE", "/api/plugins/OwnedModuleOverride", ``},
		{"PUT", "/api/plugins/OwnedModuleOverride", `{"enabled":true}`},
		{"POST", "/api/plugins/OwnedModuleOverride/reload", ``},
		{"POST", "/api/plugins/OwnedModuleOverride/reset", ``},
		{"POST", "/api/plugins/OwnedModuleOverride/clone", `{"suffix":"closed"}`},
	} {
		r := callTest(t, a.Handler(), request.method, request.path, request.body, cookie, "")
		if r.Code != http.StatusServiceUnavailable {
			t.Fatal("sealed mutation admitted", request.path, r.Code, r.Body.String())
		}
	}
	after := ownedModuleRead(t, source)
	if before.PID != after.PID || before.Calls["init"] != after.Calls["init"] {
		t.Fatal("sealed mutation restarted/reinitialized worker")
	}
	readOriginalInstalledIDs(t, a, cookie, []string{ownedModuleID})
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a.synchronizeOriginalPluginsAtStartup(context.Background())
	if _, ok := a.pluginRunningInstances.Load(ownedModuleID); ok {
		t.Fatal("startup wrapper restarted after actual Close")
	}
}
