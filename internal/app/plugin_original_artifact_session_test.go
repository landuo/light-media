package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func TestPluginOriginalArtifactInstallSaveErrorAndReloadPersistentObject(t *testing.T) {
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
	defer server.Close()
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
	snapshot := func(p *originalPluginInstance, count, failures int, marker string) {
		t.Helper()
		var got struct {
			Version      string         `json:"version"`
			InitCount    int            `json:"init_count"`
			FailureCount int            `json:"failure_count"`
			Config       map[string]any `json:"config"`
		}
		if err := p.readSession(context.Background(), "snapshot", &got); err != nil {
			t.Fatalf("independent actual IPC snapshot: %v", err)
		}
		if got.Version != "1.0" || got.InitCount != count || got.FailureCount != failures {
			t.Fatalf("persistent object snapshot: %+v", got)
		}
		if marker != "" && got.Config["marker"] != marker {
			t.Fatalf("actual object config: %+v", got)
		}
	}
	snapshot(object, 1, 0, "")
	for i, marker := range []string{"first", "second"} {
		raw, _ := json.Marshal(map[string]any{"enabled": false, "marker": marker})
		response = callTest(t, a.Handler(), "PUT", "/api/plugins/"+manifest.PluginID, string(raw), cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
		a.wg.Wait()
		current, ok := a.pluginRunningInstances.Load(manifest.PluginID)
		if !ok || current != object {
			t.Fatal("save replaced original running object")
		}
		snapshot(object, i+2, 0, marker)
	}
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/"+manifest.PluginID, `{"enabled":false,"marker":"mutated-before-error","fail_after_mutation":true}`, cookie, "")
	if response.Code < 400 {
		t.Fatalf("real init business failure reported success: %d %s", response.Code, response.Body.String())
	}
	current, ok := a.pluginRunningInstances.Load(manifest.PluginID)
	if !ok || current != object {
		t.Fatal("business failure removed/replaced loaded object")
	}
	snapshot(object, 4, 1, "mutated-before-error")
	// Repair via the same explicit save path; reload must then initialize saved
	// config on a genuinely new process, not reconstruct the old counter.
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/"+manifest.PluginID, `{"enabled":false,"marker":"repaired","fail_after_mutation":false}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	snapshot(object, 5, 1, "repaired")
	response = callTest(t, a.Handler(), "POST", "/api/plugins/"+manifest.PluginID+"/reload", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	current, ok = a.pluginRunningInstances.Load(manifest.PluginID)
	if !ok || current == object {
		t.Fatal("reload did not replace loaded process object")
	}
	snapshot(current.(*originalPluginInstance), 1, 0, "repaired")
	var old map[string]any
	if err = object.readSession(context.Background(), "snapshot", &old); err == nil {
		t.Fatal("stopped old object still callable")
	}
	if indexHits.Load() != 1 || artifactHits.Load() != 1 || unexpected.Load() != 0 {
		t.Fatalf("owned acquisition requests index=%d artifact=%d unexpected=%d", indexHits.Load(), artifactHits.Load(), unexpected.Load())
	}
}
