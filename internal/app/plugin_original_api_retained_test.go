package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"light-media/internal/plugins"
)

// Pure registration-state coverage, not process or source execution evidence.
func TestOriginalAPIRegistrationImmutableSnapshotAndPointer(t *testing.T) {
	a := &App{}
	in := PluginInstallation{PluginID: "OwnedAPI", InstanceID: "OwnedAPI", Version: "1", Generation: 7}
	object := &originalPluginInstance{app: a, installation: in}
	life := newOriginalRetainedLifetime(object)
	input := []plugins.HTTPAPI{{Key: "state", Path: "/state", Methods: []string{"GET"}, Auth: "apikey", Query: []plugins.HTTPParameter{{Name: "value", Kind: "string"}}, Headers: []string{"X-Owned"}}}
	routes := cloneOriginalAPIRoutes(input)
	registration := &originalAPIRegistration{object: object, installation: in, routes: routes, owner: retainedTestLease(t, life, originalRetainedAPI, nil)}
	a.replaceOriginalAPIRegistration(in.InstanceID, registration)
	defer a.unregisterOriginalPluginAPIs(in.InstanceID)
	input[0].Methods[0] = "POST"
	input[0].Query[0].Name = "changed"
	input[0].Headers[0] = "X-Changed"
	input[0].Auth = "anonymous"
	if routes[0].Methods[0] != "GET" || routes[0].Query[0].Name != "value" || routes[0].Headers[0] != "X-Owned" || routes[0].Auth != "apikey" {
		t.Fatal("published routes alias mutable getter output")
	}
	selected, operation, err := a.selectOriginalAPIRegistration(in.InstanceID)
	if err != nil || selected != registration {
		t.Fatal("selection lost exact registration", err)
	}
	defer object.releaseOriginalRetainedOwner(operation)
	if operation.callPhaseAllowed(object) {
		t.Fatal("API occupancy became callback authority")
	}
	if err := a.currentOriginalAPIRegistration(selected, in, routes[0]); err != nil {
		t.Fatal(err)
	}
	changed := routes[0]
	changed.Auth = "anonymous"
	if err := a.currentOriginalAPIRegistration(selected, in, changed); err == nil {
		t.Fatal("same path/key admitted changed route authority")
	}
	stale := in
	stale.Generation++
	if err := a.currentOriginalAPIRegistration(selected, stale, routes[0]); err == nil {
		t.Fatal("stale generation admitted")
	}
	// Even an identical route/object snapshot is a distinct registration epoch.
	replacement := &originalAPIRegistration{object: object, installation: in, routes: cloneOriginalAPIRoutes(routes), owner: retainedTestLease(t, life, originalRetainedAPI, nil)}
	a.replaceOriginalAPIRegistration(in.InstanceID, replacement)
	if err := a.currentOriginalAPIRegistration(selected, in, routes[0]); err == nil {
		t.Fatal("replaced registration pointer retained route authority")
	}
	if err := a.currentOriginalAPIRegistration(replacement, in, routes[0]); err != nil {
		t.Fatal(err)
	}
}

func TestOriginalAPIRegistrationActualRemoveRetainsExactWorker(t *testing.T) {
	a, object := retainedRuntimeFixture(t, false)
	client := ownedSubmittedClient(object)
	if client == nil || !client.SupportsConcurrent() || !client.SupportsTransportClose() {
		t.Fatal("actual concurrent/transport-close worker required")
	}
	settings := a.Settings()
	settings.APIToken = "owned-concurrent-api-key"
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	a.registerOriginalPluginAPIs(ownedConcurrentAppID)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	handler := a.Handler()
	request := func() *httptest.ResponseRecorder {
		return ownedConcurrentAppHTTP(ctx, handler, ownedConcurrentAppID, "state", "retained-api", "")
	}
	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest("GET", "/api/v1/plugin/"+ownedConcurrentAppID+"/state", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("registered ownership bypassed API authentication: %d", unauthenticated.Code)
	}
	before := ownedConcurrentAppResult(t, request())
	registered, ok := a.pluginRegisteredAPIs.Load(ownedConcurrentAppID)
	if !ok || registered.(*originalAPIRegistration).object != object {
		t.Fatal("initial API not bound to actual installed object")
	}
	// This call is explicitly the private host/component layer: resource wire
	// dispatch is still closed. HTTP below uses the real App router and SDK.
	scope := originalBaseCallScope{validate: func(ctx context.Context) error { return ctx.Err() }}
	result, err := a.originalSharedManagerRemove(ctx, scope, json.RawMessage(`{"plugin_id":"OwnedAppConcurrent"}`))
	if err != nil || string(result) != "null" {
		t.Fatal("actual Manager.remove component failed", string(result), err)
	}
	if _, ok := a.pluginRunningInstances.Load(ownedConcurrentAppID); ok {
		t.Fatal("removed object remains in manager running map")
	}
	if _, ok := a.pluginOriginalClasses.Load(ownedConcurrentAppID); ok {
		t.Fatal("removed class remains in manager map")
	}
	if current, ok := a.pluginRegisteredAPIs.Load(ownedConcurrentAppID); !ok || current != registered {
		t.Fatal("Manager.remove unregistered or rebound the API")
	}
	response := request()
	after := ownedConcurrentAppResult(t, response)
	var actual struct {
		Order []string `json:"order"`
	}
	if json.Unmarshal(response.Body.Bytes(), &actual) != nil || after.PID != before.PID || after.InitCount != before.InitCount || !reflect.DeepEqual(actual.Order, []string{"close", "stop_service"}) {
		t.Fatalf("bound endpoint restarted or lost ordinary cleanup state: before=%+v after=%+v body=%s", before, after, response.Body.String())
	}
	// Capture an admitted request pin before unregister. Removal revokes route
	// authority immediately but cannot reap this exact object until the pin is
	// released; no map-length polling or fabricated completion is involved.
	registration, operation, err := a.selectOriginalAPIRegistration(ownedConcurrentAppID)
	if err != nil {
		t.Fatal(err)
	}
	defer object.releaseOriginalRetainedOwner(operation)
	life := operation.lifetime
	removed := make(chan struct{})
	go func() { a.unregisterOriginalPluginAPIs(ownedConcurrentAppID); close(removed) }()
	retainedRuntimeAwait(t, removed, "API registry removal outside retirement")
	select {
	case <-life.retired():
		t.Fatal("unregister reaped a pinned invocation")
	default:
	}
	if gone := request(); gone.Code != http.StatusNotFound {
		t.Fatalf("unregistered API still exposed: %d %s", gone.Code, gone.Body.String())
	}
	api := registration.routes[0]
	_, err = a.callOriginalExternalHTTP(ctx, object.installation, registration, operation, api, plugins.HTTPRequest{Key: api.Key, Method: "GET", Path: api.Path}, pluginAPIAuthority{Kind: "key", Proof: a.pluginAPIKeyProof(settings.APIToken)})
	var failure *APIError
	if !errors.As(err, &failure) || failure.Code != "PLUGIN_API_SCOPE_DENIED" {
		t.Fatal("captured request retained removed route authority", err)
	}
	object.releaseOriginalRetainedOwner(operation)
	retainedRuntimeAwait(t, life.retired(), "actual last API owner transport reap")
	a.wg.Wait()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	if err := client.Stop(stopCtx); err != nil {
		t.Fatal("actual bound worker did not reap cleanly", err)
	}
	if _, ok := a.pluginRetainedObjects.Load(object); ok {
		t.Fatal("reaped API object leaked from cleanup enumeration")
	}
	if _, err := a.pluginStore.Data(context.Background(), ownedConcurrentAppID, "cleanup_stop"); err == nil {
		t.Fatal("transport retirement added a business stop hook")
	}
	t.Logf("private Manager.remove preserved actual router-bound PID %d; explicit unregister revoked route and last pin joined worker", before.PID)
}
