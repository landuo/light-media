package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"light-media/internal/plugins/artifacts"
	"light-media/internal/plugins/builtin"
)

// This is an owned, no-business SDK fixture declaring a legacy catalog ID.
// It does not implement or validate AutoSignIn business behavior.
func TestOriginalStandardArtifactLegacyCatalogIDActualSDK(t *testing.T) {
	index, payload := originalCatalogFixture(t, "OWNED_LEGACY_ID_FIXTURE_BINARY")
	if index.PluginID != "AutoSignIn" {
		t.Fatal("wrong owned alias fixture")
	}
	if _, _, known := builtin.Lookup(index.PluginID); !known {
		t.Fatal("fixture did not exercise a catalog collision")
	}
	manifest := index.Releases[0].Manifest
	manifest.Capabilities = []string{"site.signin"}
	if validatePluginArtifactContract(manifest, pluginPureSDKContract) == nil {
		t.Fatal("legacy ID granted a native capability")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			json.NewEncoder(w).Encode(index)
		case "/worker":
			w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	index.Releases[0].Artifacts[0].URL = server.URL + "/worker"
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	t.Cleanup(a.closeOriginalSessions)
	settings := a.Settings()
	settings.PluginMarkets = []string{server.URL + "/index"}
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), "GET", "/api/plugins/market?force=true", "", cookie, "")
	var market struct{ Data []pluginCard }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &market) != nil {
		t.Fatal(response.Body.String())
	}
	found := false
	for _, card := range market.Data {
		if card.InstanceID == index.PluginID {
			found = card.Available
		}
	}
	if !found {
		t.Fatal("valid standard package was hidden just because of its ID")
	}
	originalCatalogInstall(t, a, cookie, index.PluginID, server.URL+"/index", false)
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/"+index.PluginID, `{"enabled":false,"owned_marker":"no-business"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	loaded := originalBaseSDKLoaded(t, a, index.PluginID)
	snapshot := originalArtifactSnapshot(t, loaded)
	if snapshot["init_count"] != float64(2) || snapshot["config"].(map[string]any)["owned_marker"] != "no-business" {
		t.Fatal("catalog ID blocked actual configuration/init", snapshot)
	}
	apiSettings := a.Settings()
	apiSettings.APIToken = "owned-legacy-id-test-key"
	if err := a.saveSettings(context.Background(), apiSettings); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/api/v1/plugin/"+index.PluginID+"/status", nil)
	request.Header.Set("X-API-KEY", apiSettings.APIToken)
	response = httptest.NewRecorder()
	a.Handler().ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal("actual standard API not callable", response.Body.String())
	}
	response = callTest(t, a.Handler(), "POST", "/api/plugins/"+index.PluginID+"/clone", `{"suffix":"Owned"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	if clone := originalBaseSDKLoaded(t, a, index.PluginID+"owned"); clone == loaded {
		t.Fatal("catalog ID clone reused object")
	}
	for _, id := range []string{index.PluginID + "owned", index.PluginID} {
		response = callTest(t, a.Handler(), "DELETE", "/api/plugins/"+id, "", cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	}
	if _, ok := a.pluginRunningInstances.Load(index.PluginID); ok {
		t.Fatal("catalog ID uninstall retained worker")
	}
	if _, err := artifacts.ReadInstalled(a.pluginPackageRoot(), index.PluginID, validatePluginArtifactContract); err != nil {
		t.Fatal("primary uninstall did not retain actual code", err)
	}
}
