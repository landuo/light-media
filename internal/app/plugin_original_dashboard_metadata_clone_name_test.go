package app

import (
	"context"
	"net/http"
	"testing"
)

func TestOriginalDashboardMetadataActualSDKCustomCloneName(t *testing.T) {
	a, cookie, source := ownedDashboardMetadataFixture(t, "default")
	ownedDashboardMetadataSave(t, a, cookie, ownedStandardFixtureID, `{"enabled":true,"fail_render":true}`, http.StatusOK)
	const cloneID = ownedStandardFixtureID + "named"
	const customName = "Owned custom dashboard twin"
	r := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/"+ownedStandardFixtureID+"/clone", `{"suffix":"Named","name":"Owned custom dashboard twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	ownedDashboardMetadataSettled(t, a)
	value, ok := a.pluginRunningInstances.Load(cloneID)
	if !ok {
		t.Fatal("actual custom-named clone is not loaded")
	}
	clone := value.(*originalPluginInstance)
	if clone == source || clone.manifest.Name != source.manifest.Name || clone.manifest.Name == customName {
		t.Fatal("clone must retain an independent actual object and immutable artifact declaration")
	}
	meta, err := a.pluginStore.instanceMetadata(context.Background(), a.Store.DB, cloneID)
	if err != nil || meta.SourceInstanceID != ownedStandardFixtureID || meta.Name != customName {
		t.Fatalf("custom clone declaration was not authenticated and retained: %+v %v", meta, err)
	}
	// The copied configuration is disabled by the original clone lifecycle.
	want := []map[string]any{{"id": ownedStandardFixtureID, "name": "Owned standard fixture", "key": ""}}
	ownedDashboardMetadataRead(t, a, cookie, want, ownedDashboardMetadataObservation{source, 1, 0}, ownedDashboardMetadataObservation{clone, 1, 0})
	ownedDashboardMetadataSave(t, a, cookie, cloneID, `{"enabled":true,"fail_render":true}`, http.StatusOK)
	want = append(want, map[string]any{"id": cloneID, "name": customName, "key": ""})
	for i := 0; i < 3; i++ {
		ownedDashboardMetadataRead(t, a, cookie, want, ownedDashboardMetadataObservation{source, 1, 0}, ownedDashboardMetadataObservation{clone, 1, 0})
	}
}
