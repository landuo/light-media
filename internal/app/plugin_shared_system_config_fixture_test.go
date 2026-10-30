package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func sharedSystemConfigFixture(t *testing.T, caps []string) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	return a, cookie, installSharedSystemConfigFixture(t, a, cookie, caps)
}
func installSharedSystemConfigFixture(t *testing.T, a *App, cookie *http.Cookie, caps []string) *originalPluginInstance {
	t.Helper()
	return installSharedSystemConfigFixtureVersion(t, a, cookie, caps, "1.0")
}
func installSharedSystemConfigFixtureVersion(t *testing.T, a *App, cookie *http.Cookie, caps []string, version string) *originalPluginInstance {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "owned-system-config-fixture")
	args := []string{"build", "-mod=readonly", "-buildvcs=false"}
	if overlay := os.Getenv("OWNED_SYSTEM_CONFIG_OVERLAY"); overlay != "" {
		args = append(args, "-overlay="+overlay)
	}
	if os.Getenv("OWNED_SYSTEM_CONFIG_RACE") == "1" {
		args = append(args, "-race")
	}
	args = append(args, "-ldflags=-X main.fixtureCapabilities="+strings.Join(caps, ",")+" -X main.fixtureVersion="+version, "-o", binary, "./cmd/owned-system-config-fixture")
	goBin := os.Getenv("OWNED_SYSTEM_CONFIG_GO")
	if goBin == "" {
		goBin = "go"
	}
	cmd := exec.Command(goBin, args...)
	cmd.Dir = "../.."
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("independent worker build %v: %v %s", args, e, out)
	}
	t.Logf("actual independent worker build: %s %q", goBin, args)
	payload, e := os.ReadFile(binary)
	if e != nil {
		t.Fatal(e)
	}
	manifestRaw, e := exec.Command(binary, "--manifest").Output()
	if e != nil {
		t.Fatal(e)
	}
	var manifest plugins.Manifest
	if e = json.Unmarshal(manifestRaw, &manifest); e != nil {
		t.Fatal(e)
	}
	if e = manifest.Validate(); e != nil {
		t.Fatal(e)
	}
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	header := &zip.FileHeader{Name: manifest.Entry, Method: zip.Deflate}
	header.SetMode(0700)
	w, e := archive.CreateHeader(header)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = w.Write(payload); e != nil {
		t.Fatal(e)
	}
	if e = archive.Close(); e != nil {
		t.Fatal(e)
	}
	compressed := buf.Bytes()
	programSHA, zipSHA := sha256.Sum256(payload), sha256.Sum256(compressed)
	t.Logf("actual worker SHA256=%x", programSHA)
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "zip", SHA256: hex.EncodeToString(zipSHA[:]), ExecutableSHA256: hex.EncodeToString(programSHA[:]), Size: int64(len(compressed)), HostContract: pluginResourcesSDKContract}}}}}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/artifact":
			_, _ = w.Write(compressed)
		default:
			t.Error("unexpected owned artifact request")
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(endpoint.Close)
	index.Releases[0].Artifacts[0].URL = endpoint.URL + "/artifact"
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(mustCleanSeedJSON(map[string]any{"plugin_id": manifest.PluginID, "repo_url": endpoint.URL + "/index", "release_version": version, "force": version != "1.0"})), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	v, ok := a.pluginRunningInstances.Load(manifest.PluginID)
	if !ok {
		t.Fatal("actual worker absent")
	}
	p := v.(*originalPluginInstance)
	if !p.external || p.hostContract != pluginResourcesSDKContract {
		t.Fatal("not an external resource SDK worker")
	}
	return p
}
func sharedSystemConfigCall(t *testing.T, p *originalPluginInstance, method string, input any) (json.RawMessage, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	raw, e := json.Marshal(map[string]any{"method": method, "payload": input})
	if e != nil {
		return nil, e
	}
	out, e := p.callSession(ctx, plugins.Runner{Timeout: 20 * time.Second}, "resource", raw, nil)
	return out.Result, e
}
func sharedSystemConfigWorkerSnapshot(t *testing.T, p *originalPluginInstance) map[string]json.RawMessage {
	t.Helper()
	var out map[string]json.RawMessage
	if e := p.readSession(context.Background(), "snapshot", &out); e != nil {
		t.Fatal(e)
	}
	if string(out["stale_denied"]) != "true" {
		t.Fatal("prior callback remained active")
	}
	return out
}
