package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

const ownedInstanceLogEntry = "owned-instance-log-sdk-fixture"
const ownedInstanceLogDeniedEntry = "owned-instance-log-sdk-fixture-no-cap"

func ownedInstanceLogManifest(allowed bool) plugins.Manifest {
	id, entry := "OwnedInstanceLog", ownedInstanceLogEntry
	capabilities := []string{}
	if allowed {
		capabilities = []string{plugins.LogWriteCapability}
	} else {
		id, entry = "OwnedInstanceLogDenied", ownedInstanceLogDeniedEntry
	}
	return plugins.Manifest{
		SchemaVersion: 1, PluginID: id, Name: "Owned instance log SDK fixture", Version: "1.0", Runtime: "go-executable", Entry: entry,
		HostAPIMin: 1, HostAPIMax: 1,
		Methods:      []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "write", "resource", "probe", "fail", "close", "stop_service", "stop"},
		Capabilities: capabilities,
	}
}

// Install the independently built small owned SDK command as an acquired,
// hashed ZIP artifact. The actual command manifest is inspected before use.
// Only owned loopback index/artifact requests are allowed. All resource reads
// thereafter use the real App manager and its production callback scope.
// Race validation must supply the race-built versions of BOTH command variants.
func ownedInstanceLogSDKFixture(t *testing.T, allowed bool) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	variable := "OWNED_INSTANCE_LOG_FIXTURE_BINARY"
	if !allowed {
		variable = "OWNED_INSTANCE_LOG_DENIED_FIXTURE_BINARY"
	}
	executable := os.Getenv(variable)
	if executable == "" {
		t.Fatalf("%s must name the actual separately built owned SDK worker", variable)
	}
	executable, err := filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(payload)) > artifacts.MaxExpandedBytes {
		t.Fatal("owned standalone SDK executable exceeds the real artifact expanded-byte limit")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	manifestRaw, err := exec.CommandContext(ctx, executable, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatalf("actual owned SDK manifest failed: %v", err)
	}
	var manifest plugins.Manifest
	decoder := json.NewDecoder(bytes.NewReader(manifestRaw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatal("actual owned SDK manifest contains trailing output")
	}
	if !reflect.DeepEqual(manifest, ownedInstanceLogManifest(allowed)) {
		t.Fatal("actual standalone worker manifest does not match its required capability variant")
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if allowed && !slices.Equal(manifest.Capabilities, []string{plugins.LogWriteCapability}) || !allowed && len(manifest.Capabilities) != 0 {
		t.Fatal("owned capability fixture is inconsistent")
	}
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: manifest.Entry, Method: zip.Deflate}
	header.SetMode(0700)
	writer, err := archive.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}
	compressed := buffer.Bytes()
	if int64(len(compressed)) > artifacts.MaxArtifactBytes {
		t.Fatal("owned test ZIP exceeds the real artifact download-byte limit")
	}
	executableHash, archiveHash := sha256.Sum256(payload), sha256.Sum256(compressed)
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "zip", SHA256: hex.EncodeToString(archiveHash[:]), ExecutableSHA256: hex.EncodeToString(executableHash[:]), Size: int64(len(compressed)), HostContract: pluginResourcesSDKContract}}}}}
	var indexHits, artifactHits, unexpected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			indexHits.Add(1)
			_ = json.NewEncoder(w).Encode(index)
		case "/worker.zip":
			artifactHits.Add(1)
			_, _ = w.Write(compressed)
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/worker.zip"
	a := newTestApp(t)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]string{"plugin_id": manifest.PluginID, "repo_url": server.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	value, ok := a.pluginRunningInstances.Load(manifest.PluginID)
	if !ok {
		t.Fatal("actual owned instance log worker was not loaded")
	}
	object := value.(*originalPluginInstance)
	if !object.external || object.hostContract != pluginResourcesSDKContract || object.manifest.PluginID != manifest.PluginID {
		t.Fatal("owned instance log worker was substituted by another implementation")
	}
	object.sessionMu.Lock()
	started := object.session != nil
	object.sessionMu.Unlock()
	if !started {
		t.Fatal("actual persistent instance log SDK process was not started")
	}
	if indexHits.Load() != 1 || artifactHits.Load() != 1 || unexpected.Load() != 0 {
		t.Fatal("actual instance log artifact acquisition did not match the owned endpoints")
	}
	t.Cleanup(func() {
		if indexHits.Load() != 1 || artifactHits.Load() != 1 || unexpected.Load() != 0 {
			t.Error("resource read unexpectedly reacquired or contacted another artifact endpoint")
		}
	})
	return a, cookie, object
}
