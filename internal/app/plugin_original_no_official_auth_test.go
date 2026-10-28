package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"light-media/internal/plugins/artifacts"
)

// User decision 2026-10-26: official MoviePilot authorization is excluded.
// Existing application login and API authority remain required.
func TestOriginalOfficialAuthorizationExcludedActualSDK(t *testing.T) {
	deniedBinary := os.Getenv("OWNED_LICENSE_DENIED_BINARY")
	if deniedBinary == "" {
		t.Fatal("OWNED_LICENSE_DENIED_BINARY must name an actual 2.0 owned-standard-fixture declaring fixtureAuthLevel=2")
	}
	index, payload := originalMarketOwnedIndex(t, "OWNED_LICENSE_DENIED_BINARY")
	level := index.Releases[0].Manifest.AuthLevel
	if index.CurrentVersion != "2.0" || level == nil || *level != 2 {
		t.Fatal("wrong licensed actual worker")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index" {
			_ = json.NewEncoder(w).Encode(index)
		} else if r.URL.Path == "/worker" {
			_, _ = w.Write(payload)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	index.Releases[0].Artifacts[0].URL = server.URL + "/worker"
	a, cookie, old := ownedStandardSDKFixture(t)
	for _, path := range []string{"/api/plugins/market", "/api/plugins/" + ownedStandardFixtureID} {
		if response := callTest(t, a.Handler(), "GET", path, "", nil, ""); response.Code != http.StatusUnauthorized {
			t.Fatal("excluding official authorization changed application login protection", path, response.Code)
		}
	}
	ownedDashboardMetadataSave(t, a, cookie, ownedStandardFixtureID, `{"enabled":false,"key":"license-kept"}`, 200)
	installed, err := artifacts.ReadInstalled(a.pluginPackageRoot(), ownedStandardFixtureID, validatePluginArtifactContract)
	if err != nil {
		t.Fatal(err)
	}
	oldRepository := installed.RepositoryURL
	before, err := a.pluginStore.Config(context.Background(), ownedStandardFixtureID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"plugin_id": ownedStandardFixtureID, "repo_url": server.URL + "/index", "force": true})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	current := originalBaseSDKLoaded(t, a, ownedStandardFixtureID)
	snapshot := ownedStandardVersionRead(t, current)
	if snapshot.Version != "2.0" || !originalBaseJSONEqual(snapshot.Config, before.Value) {
		t.Fatal("official authorization metadata gated actual initialization or lost config")
	}
	if current == old || originalObjectStillLoaded(old) {
		t.Fatal("update did not replace the original worker")
	}
	if _, ok := a.pluginOriginalClasses.Load(ownedStandardFixtureID); !ok {
		t.Fatal("metadata prevented class publication")
	}
	after, err := a.pluginStore.Config(context.Background(), ownedStandardFixtureID)
	if err != nil || !originalBaseJSONEqual(before.Value, after.Value) {
		t.Fatal("metadata changed configuration", err)
	}
	ids, err := a.originalInstalledPluginIDs(context.Background())
	if err != nil || !reflect.DeepEqual(ids, []string{ownedStandardFixtureID}) {
		t.Fatal("metadata changed installed membership", err)
	}
	cards, err := a.originalLocalPluginCards(context.Background(), true)
	if err != nil || len(cards) != 1 {
		t.Fatal("metadata hid installed class", err)
	}
	// Selecting an older release preserves the same independent configuration.
	body, _ = json.Marshal(map[string]any{"plugin_id": ownedStandardFixtureID, "repo_url": oldRepository, "force": true})
	response = callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	current = originalBaseSDKLoaded(t, a, ownedStandardFixtureID)
	snapshot = ownedStandardVersionRead(t, current)
	if snapshot.Version != "1.0" || !originalBaseJSONEqual(snapshot.Config, before.Value) {
		t.Fatal("selected older release failed to preserve configuration")
	}
}

func TestOriginalOfficialAuthorizationMetadataDoesNotFilterMarket(t *testing.T) {
	public, _ := originalMarketOwnedIndex(t, "OWNED_STANDARD_FIXTURE_BINARY")
	protected, _ := originalMarketOwnedIndex(t, "OWNED_LICENSE_DENIED_BINARY")
	publicLevel, protectedLevel := 1, 2
	public.Level, protected.Level = &publicLevel, &protectedLevel
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/public":
			_ = json.NewEncoder(w).Encode(public)
		case "/protected":
			_ = json.NewEncoder(w).Encode(protected)
		default:
			t.Error("metadata-only licensing discovery downloaded an artifact")
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	public.Releases[0].Artifacts[0].URL = server.URL + "/worker"
	protected.Releases[0].Artifacts[0].URL = server.URL + "/worker"
	a := newTestApp(t)
	settings := a.Settings()
	settings.PluginMarkets = []string{server.URL + "/public", server.URL + "/protected"}
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	values, statuses := a.originalMarketDeclarations(context.Background(), true)
	if len(statuses) != 2 || !statuses[0].Success || !statuses[1].Success || len(values) != 1 || values[0].CurrentVersion != "2.0" || values[0].RepositoryURL != server.URL+"/protected" {
		t.Fatal("licensing metadata filtered the highest version", values, statuses)
	}
}
