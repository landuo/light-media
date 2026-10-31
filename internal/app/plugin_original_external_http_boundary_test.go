package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func originalExternalHTTPBoundaryFixture(t *testing.T, env string) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	binary := os.Getenv(env)
	if binary == "" {
		t.Fatalf("%s must name the actual owned compiled SDK artifact", env)
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
	if manifest.Version != "1.0" || len(manifest.Capabilities) != 0 {
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
		case "/original-api-registration-fixture":
			artifactHits.Add(1)
			_, _ = w.Write(payload)
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/original-api-registration-fixture"
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
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

	settings := a.Settings()
	settings.APIToken = "synthetic-external-boundary-key"
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	a.registerOriginalPluginAPIs(manifest.PluginID)
	return a, cookie, object
}

func originalExternalBoundaryRequest(t *testing.T, a *App, instance, key string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest("GET", "/api/v1/plugin/"+instance+"/actual", nil)
	if key != "" {
		request.Header.Set("X-API-KEY", key)
	}
	response := httptest.NewRecorder()
	a.Handler().ServeHTTP(response, request)
	return response
}

func TestPluginOriginalExternalHTTPRejectsActualNullResponse(t *testing.T) {
	// Real standard http method returns JSON null, not an HTTPResponse.
	a, _, object := originalExternalHTTPBoundaryFixture(t, "ORIGINAL_API_FIXTURE_BINARY")
	var before, after int
	if err := object.readSession(context.Background(), "get_http_calls", &before); err != nil {
		t.Fatal("actual http counter unavailable", err)
	}
	response := originalExternalBoundaryRequest(t, a, object.installation.InstanceID, a.Settings().APIToken)
	if response.Code != 502 {
		t.Fatalf("invalid actual SDK response: got %d %s", response.Code, response.Body.String())
	}
	if err := object.readSession(context.Background(), "get_http_calls", &after); err != nil || after != before+1 {
		t.Fatalf("502 must come from actual http returning null, calls %d -> %d: %v", before, after, err)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["detail"] != "Plugin execution failed" {
		t.Fatalf("invalid response leaked or accepted: %s %v", response.Body.String(), err)
	}
}

func TestPluginOriginalExternalHTTPRevokedKeyAndUninstalledRoute(t *testing.T) {
	a, cookie, object := originalExternalHTTPBoundaryFixture(t, "ORIGINAL_API_REGISTRATION_FIXTURE_BINARY")
	instance := object.installation.InstanceID
	oldKey := a.Settings().APIToken
	var enabled bool
	if err := object.readSession(context.Background(), "get_state", &enabled); err != nil || enabled {
		t.Fatalf("fixture must be business-disabled: %v %v", enabled, err)
	}
	runCounts := func() (int, int) {
		t.Helper()
		var initRuns, apiRuns int
		if err := a.Store.DB.QueryRowContext(context.Background(), `SELECT COALESCE(SUM(action='init'),0),COALESCE(SUM(action='api'),0) FROM plugin_runs WHERE instance_id=?`, instance).Scan(&initRuns, &apiRuns); err != nil {
			t.Fatal(err)
		}
		return initRuns, apiRuns
	}
	initBefore, apiBefore := runCounts()
	good := originalExternalBoundaryRequest(t, a, instance, oldKey)
	if good.Code != 200 {
		t.Fatalf("actual success prerequisite: %d %s", good.Code, good.Body.String())
	}
	patch := httptest.NewRequest("PATCH", "/api/v1/plugin/"+instance+"/actual", nil)
	patch.Header.Set("X-API-KEY", oldKey)
	patchResponse := httptest.NewRecorder()
	a.Handler().ServeHTTP(patchResponse, patch)
	if patchResponse.Code != 200 {
		t.Fatalf("business-disabled PATCH: %d %s", patchResponse.Code, patchResponse.Body.String())
	}
	initAfter, apiAfter := runCounts()
	if initAfter != initBefore || apiAfter != apiBefore {
		t.Fatalf("direct APIs queued/reinitialized: init %d -> %d; api %d -> %d", initBefore, initAfter, apiBefore, apiAfter)
	}
	registered, operation, err := a.selectOriginalAPIRegistration(instance)
	if err != nil {
		t.Fatal("missing actual route", err)
	}
	defer object.releaseOriginalRetainedOwner(operation)
	api := registered.routes[0]
	authority := pluginAPIAuthority{Kind: "key", Proof: a.pluginAPIKeyProof(oldKey)}
	settings := a.Settings()
	settings.APIToken = "synthetic-external-boundary-rotated"
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	denied := originalExternalBoundaryRequest(t, a, instance, oldKey)
	if denied.Code != 401 {
		t.Fatalf("revoked key: %d %s", denied.Code, denied.Body.String())
	}
	_, err = a.callOriginalExternalHTTP(context.Background(), object.installation, registered, operation, api, plugins.HTTPRequest{Key: api.Key, Method: "GET", Path: "/actual"}, authority)
	var failure *APIError
	if !errors.As(err, &failure) || failure.Code != "PLUGIN_API_AUTH_REVOKED" {
		t.Fatalf("stale already-authenticated authority must reject before SDK: %v", err)
	}
	good = originalExternalBoundaryRequest(t, a, instance, settings.APIToken)
	if good.Code != 200 {
		t.Fatalf("replacement key: %d %s", good.Code, good.Body.String())
	}
	response := callTest(t, a.Handler(), "DELETE", "/api/plugins/"+instance, `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	gone := originalExternalBoundaryRequest(t, a, instance, settings.APIToken)
	if gone.Code != 404 {
		t.Fatalf("uninstalled route still exposed: %d %s", gone.Code, gone.Body.String())
	}
	_, err = a.callOriginalExternalHTTP(context.Background(), object.installation, registered, operation, api, plugins.HTTPRequest{Key: api.Key, Method: "GET", Path: "/actual"}, pluginAPIAuthority{Kind: "key", Proof: a.pluginAPIKeyProof(settings.APIToken)})
	failure = nil
	if !errors.As(err, &failure) || failure.Code != "PLUGIN_NOT_LOADED" {
		t.Fatalf("stale direct object call survived actual uninstall: %v", err)
	}
}

func TestPluginOriginalExternalHTTPRejectsRemovedRegisteredRoute(t *testing.T) {
	a, _, object := originalExternalHTTPBoundaryFixture(t, "ORIGINAL_API_REGISTRATION_FIXTURE_BINARY")
	registered, operation, err := a.selectOriginalAPIRegistration(object.installation.InstanceID)
	if err != nil {
		t.Fatal("missing route", err)
	}
	defer object.releaseOriginalRetainedOwner(operation)
	api := registered.routes[0]
	raw, _ := json.Marshal("null")
	if _, err := object.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, "set_mode", raw, nil); err != nil {
		t.Fatal(err)
	}
	a.registerOriginalPluginAPIs(object.installation.InstanceID)
	_, err = a.callOriginalExternalHTTP(context.Background(), object.installation, registered, operation, api, plugins.HTTPRequest{Key: api.Key, Method: "GET", Path: "/actual"}, pluginAPIAuthority{Kind: "key", Proof: a.pluginAPIKeyProof(a.Settings().APIToken)})
	var failure *APIError
	if !errors.As(err, &failure) || failure.Code != "PLUGIN_API_SCOPE_DENIED" {
		t.Fatalf("old route reused after actual getter removal: %v", err)
	}
}
