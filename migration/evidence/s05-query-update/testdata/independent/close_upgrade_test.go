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
	"path/filepath"
	"runtime"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func TestAuditS05VersionReplacementCloseKeepsActiveDAO(t *testing.T) {
	workerOverlay := os.Getenv("S05_AUDIT_WORKER_OVERLAY")
	if workerOverlay == "" {
		t.Fatal("worker overlay must be supplied by the independent runner")
	}
	t.Setenv("OWNED_SHARED_HOST_OVERLAY", workerOverlay)
	caps := []string{plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionsUpdate, plugins.BaseSaveData}
	a, cookie, old := sharedSubscriptionsFixture(t, caps)
	auditS05Seed(t, a, "owned-close-upgrade")
	configured := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedSharedHost", `{"close_subscription_id":"owned-close-upgrade"}`, cookie, "")
	assertOriginalRegistrySuccess(t, configured.Code, configured.Body.Bytes())
	if _, e := a.pluginStore.Data(context.Background(), "OwnedSharedHost", "owned-close-dao"); e == nil {
		t.Fatal("close marker existed before upgrade")
	}
	before, e := a.pluginStore.Installation(context.Background(), "OwnedSharedHost")
	if e != nil {
		t.Fatal(e)
	}

	binary := filepath.Join(t.TempDir(), "owned-v2")
	args := []string{"build", "-mod=readonly", "-race", "-overlay=" + workerOverlay,
		"-ldflags=-X main.fixtureCapabilities=subscriptions.get,subscriptions.update,plugin.base.save_data -X main.auditFixtureVersion=2.0",
		"-o", binary, "./cmd/owned-shared-host-fixture"}
	build := exec.Command("go", args...)
	build.Dir = "../.."
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	data, e := os.ReadFile(binary)
	if e != nil {
		t.Fatal(e)
	}
	manifestRaw, e := exec.Command(binary, "--manifest").Output()
	if e != nil {
		t.Fatal(e)
	}
	var manifest plugins.Manifest
	if e = json.Unmarshal(manifestRaw, &manifest); e != nil || manifest.Version != "2.0" {
		t.Fatal("wrong actual v2 manifest", e)
	}
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	t.Logf("actual version replacement worker SHA256=%s", hash)
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version,
		Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{
			Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: hash, ExecutableSHA256: hash, Size: int64(len(data)), HostContract: pluginResourcesSDKContract,
		}}}}}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/artifact":
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	defer endpoint.Close()
	index.Releases[0].Artifacts[0].URL = endpoint.URL + "/artifact"
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(mustCleanSeedJSON(map[string]any{
		"plugin_id": "OwnedSharedHost", "repo_url": endpoint.URL + "/index", "release_version": "2.0", "force": true,
	})), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	after, e := a.pluginStore.Installation(context.Background(), "OwnedSharedHost")
	if e != nil {
		t.Fatal(e)
	}
	if after.Version != "2.0" || after.Generation <= before.Generation {
		t.Fatal("real installation was not replaced", before, after)
	}
	current, ok := a.pluginRunningInstances.Load("OwnedSharedHost")
	if !ok || current == old {
		t.Fatal("actual old object not replaced")
	}
	marker, e := a.pluginStore.Data(context.Background(), "OwnedSharedHost", "owned-close-dao")
	if e != nil {
		t.Fatal("actual old close did not persist Base marker", e)
	}
	t.Logf("actual old close marker=%s; generation %d -> %d", marker.Value, before.Generation, after.Generation)
	var ack struct {
		Version string `json:"version"`
		OK      bool   `json:"dao_ok"`
	}
	if e = json.Unmarshal(marker.Value, &ack); e != nil {
		t.Fatal(e)
	}
	if ack.Version != "1.0" {
		t.Fatal("marker not from old real worker", string(marker.Value))
	}
	row := auditS05Row(t, current.(*originalPluginInstance), plugins.SharedSubscriptionsGet, plugins.SharedSubscriptionGetV1{ID: "owned-close-upgrade"})
	if !ack.OK || string(row["keyword"]) != `"old-version-close"` {
		t.Fatalf("legitimate bound close lost subscription DAO while Base remained active: marker=%s row_keyword=%s", marker.Value, row["keyword"])
	}
}
