package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestOriginalInstalledLocalClassesActualSDKNoRepositoryRequest(t *testing.T) {
	a, cookie, source := ownedStandardSDKFixture(t)
	ctx := context.Background()
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedStandardFixtureID+"/clone", `{"suffix":"Local","version":"9.7"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	id := ownedStandardFixtureID + "local"
	clone := originalBaseSDKLoaded(t, a, id)
	if err := a.setOriginalPluginInstalled(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	// A real stored installed ID without an imported class must not become a
	// fabricated local card. The installed-ID endpoint still retains that ID.
	in := source.installation
	in.InstanceID = "OwnedNoImportedClass"
	if err := a.pluginStore.Install(ctx, in); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); http.Error(w, "must not request", 503) }))
	defer repository.Close()
	settings := a.Settings()
	settings.PluginMarkets = []string{repository.URL}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	beforeSource, beforeClone := ownedStandardSnapshotRead(t, source), ownedStandardSnapshotRead(t, clone)
	response = callTest(t, a.Handler(), "GET", "/api/plugins?state=installed&force=invalid", "", cookie, "")
	if response.Code != 400 {
		t.Fatal("invalid bool query was accepted before local class processing")
	}
	response = callTest(t, a.Handler(), "GET", "/api/plugins?state=installed", "", cookie, "")
	var body struct{ Data []pluginCard }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Data) != 1 || body.Data[0].InstanceID != ownedStandardFixtureID || body.Data[0].RepositoryURL != "" || body.Data[0].HasUpdate || !body.Data[0].Available {
		t.Fatal("local installed list differed from fixed class registry", response.Body.String())
	}
	afterSource, afterClone := ownedStandardSnapshotRead(t, source), ownedStandardSnapshotRead(t, clone)
	if hits.Load() != 0 || afterSource.Calls["get_state"] != beforeSource.Calls["get_state"]+1 || afterClone.Calls["get_state"] != beforeClone.Calls["get_state"]+1 {
		t.Fatal("installed-only read requested a repository or skipped uninstalled imported class state", hits.Load())
	}
	// Source get_state failure becomes false while the imported card remains.
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/"+ownedStandardFixtureID, `{"enabled":false,"fail_state":true}`, cookie, "")
	if response.Code != 500 {
		t.Fatal("state failure fixture did not fail actual initialization tail")
	}
	response = callTest(t, a.Handler(), "GET", "/api/plugins?state=installed", "", cookie, "")
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Data) != 1 || body.Data[0].State || hits.Load() != 0 {
		t.Fatal("state exception removed a class or triggered remote requests", response.Body.String())
	}
}
