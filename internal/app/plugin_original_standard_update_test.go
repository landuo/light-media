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
	"os/exec"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

type ownedStandardVersionSnapshot struct {
	Version   string          `json:"version"`
	Nonce     int             `json:"nonce"`
	InitCount int             `json:"init_count"`
	Config    json.RawMessage `json:"config"`
}

func ownedStandardVersionRead(t *testing.T, p *originalPluginInstance) ownedStandardVersionSnapshot {
	t.Helper()
	var result ownedStandardVersionSnapshot
	if err := p.readSession(context.Background(), "snapshot", &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// Real two-version executable publication. No manifest is relabelled and no
// config/revision/run record is used to manufacture a successful transition.
func TestOriginalStandardActualArtifactUpgradePreservesClone(t *testing.T) {
	ctx := context.Background()
	binary := os.Getenv("OWNED_STANDARD_FIXTURE_V2_BINARY")
	if binary == "" {
		t.Fatal("OWNED_STANDARD_FIXTURE_V2_BINARY must identify the separately compiled 2.0 executable")
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	sourceBytes, err := os.ReadFile(os.Getenv("OWNED_STANDARD_FIXTURE_BINARY"))
	if err != nil || bytes.Equal(sourceBytes, payload) {
		t.Fatal("upgrade requires genuinely distinct executable bytes", err)
	}
	inspect, cancel := context.WithTimeout(ctx, 20*time.Second)
	raw, err := exec.CommandContext(inspect, binary, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.PluginID != ownedStandardFixtureID || manifest.Version != "2.0" || !slices.Equal(manifest.Capabilities, plugins.BaseMethods()) {
		t.Fatal("actual 2.0 executable manifest is wrong")
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	sha := hex.EncodeToString(digest[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: sha, ExecutableSHA256: sha, Size: int64(len(payload)), HostContract: "light-media.sdk-base/v1"}}}}}
	var indexHits, artifactHits, unexpected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			indexHits.Add(1)
			_ = json.NewEncoder(w).Encode(index)
		case "/standard-v2":
			artifactHits.Add(1)
			_, _ = w.Write(payload)
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	index.Releases[0].Artifacts[0].URL = server.URL + "/standard-v2"
	a, cookie, old := ownedStandardSDKFixture(t)
	put := callTest(t, a.Handler(), "PUT", "/api/plugins/"+ownedStandardFixtureID, `{"enabled":false,"key":"source"}`, cookie, "")
	assertOriginalRegistrySuccess(t, put.Code, put.Body.Bytes())
	old.sessionMu.Lock()
	oldTransport := old.session
	old.sessionMu.Unlock()
	if oldTransport == nil {
		t.Fatal("actual old worker transport missing")
	}
	before := ownedStandardVersionRead(t, old)
	if before.Version != "1.0" || before.InitCount != 2 {
		t.Fatal("actual original version missing", before)
	}
	preserved, err := a.pluginStore.Data(ctx, ownedStandardFixtureID, "owned_v1_preserved")
	if err != nil {
		t.Fatal("real v1 Base marker missing", err)
	}
	oldConfig, err := a.pluginStore.Config(ctx, ownedStandardFixtureID)
	if err != nil {
		t.Fatal(err)
	}
	cloned := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedStandardFixtureID+"/clone", `{"suffix":"VersionTwin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, cloned.Code, cloned.Body.Bytes())
	cloneID := ownedStandardFixtureID + "versiontwin"
	cloneValue, ok := a.pluginRunningInstances.Load(cloneID)
	if !ok {
		t.Fatal("real clone not loaded")
	}
	clone := cloneValue.(*originalPluginInstance)
	sibling := callTest(t, a.Handler(), "PUT", "/api/plugins/"+cloneID, `{"enabled":false,"key":"sibling"}`, cookie, "")
	assertOriginalRegistrySuccess(t, sibling.Code, sibling.Body.Bytes())
	cloneBefore := ownedStandardVersionRead(t, clone)
	if cloneBefore.Version != "1.0" || cloneBefore.Nonce == before.Nonce {
		t.Fatal("clone did not use independent 1.0 process")
	}
	request, _ := json.Marshal(map[string]any{"plugin_id": ownedStandardFixtureID, "repo_url": server.URL + "/index", "release_version": "2.0", "force": true})
	updated := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(request), cookie, "")
	assertOriginalRegistrySuccess(t, updated.Code, updated.Body.Bytes())
	a.wg.Wait()
	currentValue, ok := a.pluginRunningInstances.Load(ownedStandardFixtureID)
	if !ok || currentValue == old {
		t.Fatal("upgrade did not replace old actual object")
	}
	current := currentValue.(*originalPluginInstance)
	after := ownedStandardVersionRead(t, current)
	if after.Version != "2.0" || after.Nonce == before.Nonce || after.InitCount != 1 || current.installation.Version != "2.0" {
		t.Fatal("actual 2.0 session not started", after)
	}
	installation, err := a.pluginStore.Installation(ctx, ownedStandardFixtureID)
	if err != nil || installation.Version != "2.0" {
		t.Fatal("published installation version mismatch", err)
	}
	config, err := a.pluginStore.Config(ctx, ownedStandardFixtureID)
	if err != nil || !bytes.Equal(config.Value, oldConfig.Value) || !bytes.Equal(after.Config, oldConfig.Value) {
		t.Fatal("upgrade replaced explicit Base configuration", err)
	}
	sourceData, err := a.pluginStore.Data(ctx, ownedStandardFixtureID, "shared")
	if err != nil || string(sourceData.Value) != `"source"` {
		t.Fatal("upgrade lost original Base data", err)
	}
	kept, err := a.pluginStore.Data(ctx, ownedStandardFixtureID, "owned_v1_preserved")
	if err != nil || !bytes.Equal(kept.Value, preserved.Value) {
		t.Fatal("new version lost old data it cannot recreate", err)
	}
	oldStop, err := a.pluginStore.Data(ctx, ownedStandardFixtureID, "owned_stop")
	var stop struct {
		Version string   `json:"version"`
		Nonce   int      `json:"nonce"`
		Order   []string `json:"order"`
	}
	if err != nil || json.Unmarshal(oldStop.Value, &stop) != nil || stop.Version != "1.0" || stop.Nonce != before.Nonce || !slices.Equal(stop.Order, []string{"close", "stop_service", "stop"}) {
		t.Fatal("old executable did not complete source and transport cleanup", err, stop)
	}
	old.sessionMu.Lock()
	sessionReleased := old.session == nil && old.sessionStarted
	old.sessionMu.Unlock()
	if !sessionReleased {
		t.Fatal("old worker transport was not released")
	}
	// The old worker recorded its actual stop callback above. The SDK's
	// idempotent Stop now exposes that already joined transport's terminal result.
	if err := oldTransport.Stop(ctx); err != nil {
		t.Fatal("old worker transport did not join successfully", err)
	}
	if originalObjectStillLoaded(old) {
		t.Fatal("old version remains loaded")
	}
	if _, err := old.callOriginalMethod(ctx, "snapshot", nil); err == nil {
		t.Fatal("replaced object remained callable")
	}
	cloneCurrent, ok := a.pluginRunningInstances.Load(cloneID)
	if !ok || cloneCurrent != clone {
		t.Fatal("source upgrade replaced old clone")
	}
	cloneAfter := ownedStandardVersionRead(t, clone)
	if cloneAfter.Version != "1.0" || cloneAfter.Nonce != cloneBefore.Nonce || cloneAfter.InitCount != cloneBefore.InitCount {
		t.Fatal("clone code/session was upgraded with source", cloneAfter)
	}
	cloneData, err := a.pluginStore.Data(ctx, cloneID, "shared")
	if err != nil || string(cloneData.Value) != `"sibling"` {
		t.Fatal("source upgrade changed sibling namespace", err)
	}
	if indexHits.Load() != 1 || artifactHits.Load() != 1 || unexpected.Load() != 0 {
		t.Fatal("upgrade did not consume the actual new owned artifact", indexHits.Load(), artifactHits.Load(), unexpected.Load())
	}
	var runs int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatal("upgrade entered retired queue", runs, err)
	}
}
