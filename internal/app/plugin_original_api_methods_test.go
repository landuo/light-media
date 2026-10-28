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
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestPluginOriginalAPIMethodActualWorker(t *testing.T) {
	binary := os.Getenv("ORIGINAL_API_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("ORIGINAL_API_FIXTURE_BINARY must name the actual owned compiled SDK artifact")
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
	if manifest.PluginID != "OriginalAPIFixture" || manifest.Version != "1.0" || len(manifest.Capabilities) != 0 {
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
		case "/original-api-fixture":
			artifactHits.Add(1)
			_, _ = w.Write(payload)
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	index.Releases[0].Artifacts[0].URL = server.URL + "/original-api-fixture"
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
	before, _ := json.Marshal(object.manifest)
	first, err := object.getAPIs(context.Background())
	if err != nil || len(first) != 1 || first[0].Path != "/actual" || !reflect.DeepEqual(first[0].Methods, []string{"GET", "PATCH"}) || first[0].Auth != "apikey" {
		t.Fatalf("actual raw get_api differs from declaration: %+v %v", first, err)
	}
	setMode := func(mode string) {
		t.Helper()
		raw, _ := json.Marshal(mode)
		if _, err := object.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, "set_mode", raw, nil); err != nil {
			t.Fatal(err)
		}
	}
	setMode("changed")
	second, err := object.getAPIs(context.Background())
	if err != nil || len(second) != 1 || second[0].Path != "/actual_again" {
		t.Fatalf("same live object: %+v %v", second, err)
	}
	for _, mode := range []string{"error", "invalid"} {
		setMode(mode)
		if apis, err := object.getAPIs(context.Background()); err == nil || len(apis) != 0 {
			t.Fatalf("%s: must surface actual error, not manifest fallback: %+v %v", mode, apis, err)
		}
	}
	setMode("null")
	if apis, err := object.getAPIs(context.Background()); err != nil || len(apis) != 0 {
		t.Fatalf("null: %+v %v", apis, err)
	}
	setMode("host")
	if apis, err := object.getAPIs(context.Background()); err != nil || len(apis) != 1 || apis[0].Key != "host_denied" {
		t.Fatalf("read method must not grant host calls: %+v %v", apis, err)
	}
	after, _ := json.Marshal(object.manifest)
	if string(before) != string(after) || len(object.manifest.Capabilities) != 0 {
		t.Fatal("get_api changed manifest permissions")
	}
}
