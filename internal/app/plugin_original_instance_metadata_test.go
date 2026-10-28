package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"testing"
	"time"
)

type originalInstanceMetadataSnapshot struct {
	Metadata    plugins.InstanceMetadata `json:"metadata"`
	Name        string                   `json:"name"`
	PID         int                      `json:"pid"`
	StaleDenied bool                     `json:"stale_denied"`
}

func originalReadInstanceMetadata(t *testing.T, p *originalPluginInstance) originalInstanceMetadataSnapshot {
	t.Helper()
	var result originalInstanceMetadataSnapshot
	if err := p.readSession(context.Background(), "instance_metadata", &result); err != nil {
		t.Fatal(err)
	}
	if result.PID <= 0 || !result.StaleDenied || result.Name != result.Metadata.Name {
		t.Fatalf("SDK did not read current authenticated metadata: %+v", result)
	}
	return result
}

// Real installation, clone and reload HTTP operations launch the actual SDK
// executable. No host handler, metadata response or identity is substituted.
func TestOriginalBaseSDKActualCloneInstanceMetadata(t *testing.T) {
	a, cookie, source := originalBaseSDKFixture(t)
	ctx := context.Background()
	before := originalReadInstanceMetadata(t, source)
	expectedSource := plugins.InstanceMetadata{
		InstanceID: ownedBaseFixtureID, PluginID: ownedBaseFixtureID, Version: "1.0",
		Name: source.manifest.Name, Description: source.manifest.Description,
		DisplayVersion: "1.0", Icon: source.manifest.Icon,
	}
	if before.Metadata != expectedSource {
		t.Fatalf("source metadata: %+v want %+v", before.Metadata, expectedSource)
	}
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedBaseFixtureID+"/clone",
		`{"suffix":"Named","name":"独立分身","description":"只由宿主保存的说明","version":"9.7","icon":"https://example.invalid/clone.svg"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	cloneID := ownedBaseFixtureID + "named"
	clone := originalBaseSDKLoaded(t, a, cloneID)
	got := originalReadInstanceMetadata(t, clone)
	expectedClone := plugins.InstanceMetadata{
		InstanceID: cloneID, SourceInstanceID: ownedBaseFixtureID, PluginID: ownedBaseFixtureID,
		Version: "1.0", Name: "独立分身", Description: "只由宿主保存的说明",
		DisplayVersion: "9.7", Icon: "https://example.invalid/clone.svg",
	}
	if got.Metadata != expectedClone || got.PID == before.PID {
		t.Fatalf("independent worker clone metadata: %+v", got)
	}
	installation, err := a.pluginStore.Installation(ctx, cloneID)
	if err != nil || installation.PluginID != ownedBaseFixtureID || installation.Version != "1.0" {
		t.Fatalf("display metadata replaced trusted executable identity: %+v %v", installation, err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(installation.Manifest, &manifest) != nil || manifest.PluginID != ownedBaseFixtureID || manifest.Version != "1.0" {
		t.Fatal("clone display version changed executable manifest")
	}
	// These fields belong to ordinary plugin configuration, not host metadata.
	// A same-object initialization must not let them rename the instance.
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/"+cloneID,
		`{"enabled":false,"name":"forged","display_version":"88","source_instance_id":"forged","plugin_id":"forged"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	if updated := originalReadInstanceMetadata(t, clone); updated.Metadata != expectedClone || updated.PID != got.PID {
		t.Fatalf("config overwrote metadata or replaced the object: %+v", updated)
	}
	// The new method is current-instance-only and read-only even when called
	// directly through the real worker's HostCaller rather than the SDK helper.
	for _, raw := range []string{`null`, `[]`, `{"plugin_id":"OwnedBaseFixture"}`, `{"name":"forged"}`, `{"config":{"name":"forged"}}`} {
		result, err := clone.callSession(ctx, plugins.Runner{Timeout: 10 * time.Second}, "metadata_raw", json.RawMessage(raw), nil)
		var rejection struct {
			Denied bool `json:"denied"`
		}
		if err != nil || json.Unmarshal(result.Result, &rejection) != nil || !rejection.Denied {
			t.Fatalf("metadata caller override accepted: %s result=%s err=%v", raw, result.Result, err)
		}
	}
	after := originalReadInstanceMetadata(t, source)
	if after.Metadata != before.Metadata || after.PID != before.PID {
		t.Fatalf("clone changed source: before=%+v after=%+v", before, after)
	}
	response = callTest(t, a.Handler(), "POST", "/api/plugins/"+cloneID+"/reload", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	reloaded := originalReadInstanceMetadata(t, originalBaseSDKLoaded(t, a, cloneID))
	if reloaded.Metadata != expectedClone || reloaded.PID == got.PID {
		t.Fatalf("reload lost persisted metadata or retained process: %+v", reloaded)
	}
	if err := clone.readSession(ctx, "instance_metadata", &originalInstanceMetadataSnapshot{}); err == nil {
		t.Fatal("old worker retained metadata access after replacement")
	}
}
