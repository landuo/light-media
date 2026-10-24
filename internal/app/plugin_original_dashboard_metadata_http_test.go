package app

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
)

const ownedDashboardMetadataPath = "/api/plugins/dashboard/meta"

// This reuses the real artifact HTTP acquisition, validation, persistent SDK
// process, encrypted configuration and existing snapshot helpers. No callback,
// running-object insertion or in-memory manifest replacement is used.
func ownedDashboardMetadataFixture(t *testing.T, mode string) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	if mode != "default" {
		env := "OWNED_STANDARD_DASHBOARD_META_FIXTURE_BINARY"
		if mode == "none" {
			env = "OWNED_STANDARD_NO_DASHBOARD_FIXTURE_BINARY"
		}
		path := os.Getenv(env)
		if path == "" {
			t.Fatalf("%s must name the actual owned standard %s executable", env, mode)
		}
		t.Setenv("OWNED_STANDARD_FIXTURE_BINARY", path)
	}
	a, cookie, object := ownedStandardSDKFixture(t)
	if got := slices.Contains(object.manifest.Methods, "get_dashboard"); got != (mode != "none") {
		t.Fatalf("actual %s worker declared get_dashboard=%v", mode, got)
	}
	if got := slices.Contains(object.manifest.Methods, "get_dashboard_meta"); got != (mode != "default") {
		t.Fatalf("actual %s worker declared get_dashboard_meta=%v", mode, got)
	}
	return a, cookie, object
}

// Save and clone registrations launch a finite command rebuild. Observe its
// completion before call-count baselines, without starting the App scheduler.
func ownedDashboardMetadataSettled(t *testing.T, a *App) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("owned metadata setup registration did not settle")
	}
}

func ownedDashboardMetadataSave(t *testing.T, a *App, cookie *http.Cookie, id, body string, wantStatus int) {
	t.Helper()
	r := callTest(t, a.Handler(), http.MethodPut, "/api/plugins/"+id, body, cookie, "")
	if wantStatus == http.StatusOK {
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	} else if r.Code != wantStatus {
		t.Fatalf("actual metadata setup status=%d want=%d body=%s", r.Code, wantStatus, r.Body.String())
	}
	ownedDashboardMetadataSettled(t, a)
}

func ownedDashboardMetadataClone(t *testing.T, a *App, cookie *http.Cookie, suffix string) *originalPluginInstance {
	t.Helper()
	body, err := json.Marshal(map[string]string{"suffix": suffix})
	if err != nil {
		t.Fatal(err)
	}
	r := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/"+ownedStandardFixtureID+"/clone", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	ownedDashboardMetadataSettled(t, a)
	id := ownedStandardFixtureID + strings.ToLower(suffix)
	value, ok := a.pluginRunningInstances.Load(id)
	if !ok {
		t.Fatalf("actual metadata clone %s is not loaded", id)
	}
	return value.(*originalPluginInstance)
}

type ownedDashboardMetadataObservation struct {
	object                *originalPluginInstance
	stateCalls, metaCalls int
}

func ownedDashboardMetadataRead(t *testing.T, a *App, cookie *http.Cookie, want []map[string]any, observations ...ownedDashboardMetadataObservation) {
	t.Helper()
	before := make([]ownedStandardSnapshot, len(observations))
	clients := make([]*plugins.Client, len(observations))
	for i, observation := range observations {
		object := observation.object
		before[i] = ownedStandardSnapshotRead(t, object)
		object.sessionMu.Lock()
		clients[i] = object.session
		object.sessionMu.Unlock()
		if clients[i] == nil {
			t.Fatal("actual SDK process missing before metadata read")
		}
	}
	r := callTest(t, a.Handler(), http.MethodGet, ownedDashboardMetadataPath, "", cookie, "")
	var body struct {
		Data  json.RawMessage `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &body) != nil || len(body.Error) != 0 {
		t.Fatalf("actual metadata HTTP request failed: %d %s", r.Code, r.Body.String())
	}
	var got []map[string]any
	if json.Unmarshal(body.Data, &got) != nil || got == nil {
		t.Fatalf("metadata must be a JSON array, not absent/null: %s", body.Data)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("metadata rows/order differ: got=%#v want=%#v", got, want)
	}
	for i, observation := range observations {
		object := observation.object
		after := ownedStandardSnapshotRead(t, object)
		loaded, ok := a.pluginRunningInstances.Load(object.installation.InstanceID)
		if !ok || loaded != object || after.Nonce != before[i].Nonce || after.InitCount != before[i].InitCount || string(after.Config) != string(before[i].Config) {
			t.Fatal("metadata replaced, reinitialized or changed a retained actual object")
		}
		object.sessionMu.Lock()
		sameClient := object.session == clients[i]
		object.sessionMu.Unlock()
		if !sameClient {
			t.Fatal("metadata replaced the persistent SDK transport")
		}
		for method, count := range after.Calls {
			delta := count - before[i].Calls[method]
			wantDelta := 0
			switch method {
			case "snapshot":
				wantDelta = 1
			case "get_state":
				wantDelta = observation.stateCalls
			case "get_dashboard_meta":
				wantDelta = observation.metaCalls
			}
			if delta != wantDelta {
				t.Fatalf("metadata %s method %s calls=%d want=%d", object.installation.InstanceID, method, delta, wantDelta)
			}
		}
		// Explicit checks also catch a required method that was never called.
		if after.Calls["get_state"]-before[i].Calls["get_state"] != observation.stateCalls || after.Calls["get_dashboard_meta"]-before[i].Calls["get_dashboard_meta"] != observation.metaCalls {
			t.Fatal("metadata skipped a required state or metadata method")
		}
	}
	var runs int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("metadata entered the retired business queue: runs=%d err=%v", runs, err)
	}
}

func TestOriginalDashboardMetadataHTTPEmptyAuthAndMethod(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	if r := callTest(t, a.Handler(), http.MethodGet, ownedDashboardMetadataPath, "", nil, ""); r.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous metadata access: %d %s", r.Code, r.Body.String())
	}
	ownedDashboardMetadataRead(t, a, cookie, []map[string]any{})
	if r := callTest(t, a.Handler(), http.MethodPost, ownedDashboardMetadataPath, `{}`, cookie, ""); r.Code != http.StatusNotFound && r.Code != http.StatusMethodNotAllowed {
		t.Fatalf("metadata route accepted a non-GET method: %d %s", r.Code, r.Body.String())
	}
}

func TestOriginalDashboardMetadataActualSDKDefaultAndDisabled(t *testing.T) {
	a, cookie, object := ownedDashboardMetadataFixture(t, "default")
	if object.manifest.Name != "Owned standard fixture" {
		t.Fatal("unexpected default fixture name")
	}
	ownedDashboardMetadataSave(t, a, cookie, ownedStandardFixtureID, `{"enabled":false,"fail_render":true}`, http.StatusOK)
	ownedDashboardMetadataRead(t, a, cookie, []map[string]any{}, ownedDashboardMetadataObservation{object, 1, 0})
	ownedDashboardMetadataSave(t, a, cookie, ownedStandardFixtureID, `{"enabled":true,"fail_render":true}`, http.StatusOK)
	want := []map[string]any{{"id": ownedStandardFixtureID, "name": "Owned standard fixture", "key": ""}}
	for i := 0; i < 3; i++ {
		ownedDashboardMetadataRead(t, a, cookie, want, ownedDashboardMetadataObservation{object, 1, 0})
	}
	clone := ownedDashboardMetadataClone(t, a, cookie, "Twin")
	if clone == object || ownedStandardSnapshotRead(t, clone).Nonce == ownedStandardSnapshotRead(t, object).Nonce {
		t.Fatal("metadata clone borrowed the source's actual object or process")
	}
	// The original clone lifecycle always disables copied configuration.
	ownedDashboardMetadataRead(t, a, cookie, want, ownedDashboardMetadataObservation{object, 1, 0}, ownedDashboardMetadataObservation{clone, 1, 0})
	ownedDashboardMetadataSave(t, a, cookie, clone.installation.InstanceID, `{"enabled":true,"fail_render":true}`, http.StatusOK)
	want = append(want, map[string]any{"id": ownedStandardFixtureID + "twin", "name": clone.manifest.Name, "key": ""})
	ownedDashboardMetadataRead(t, a, cookie, want, ownedDashboardMetadataObservation{object, 1, 0}, ownedDashboardMetadataObservation{clone, 1, 0})
	ownedDashboardMetadataSave(t, a, cookie, ownedStandardFixtureID, `{"enabled":false}`, http.StatusOK)
	ownedDashboardMetadataRead(t, a, cookie, want[1:], ownedDashboardMetadataObservation{object, 1, 0}, ownedDashboardMetadataObservation{clone, 1, 0})
}

func TestOriginalDashboardMetadataActualSDKMissingDashboardBeforeState(t *testing.T) {
	a, cookie, object := ownedDashboardMetadataFixture(t, "none")
	// The real object's init succeeds, then the original save endpoint fails at
	// get_state. It remains loaded. Metadata must skip it BEFORE calling state.
	ownedDashboardMetadataSave(t, a, cookie, ownedStandardFixtureID, `{"enabled":true,"fail_state":true,"fail_dashboard_meta":true}`, http.StatusInternalServerError)
	ownedDashboardMetadataRead(t, a, cookie, []map[string]any{}, ownedDashboardMetadataObservation{object, 0, 0})
}

func TestOriginalDashboardMetadataActualSDKMultiAndFalseyContract(t *testing.T) {
	a, cookie, object := ownedDashboardMetadataFixture(t, "multi")
	ownedDashboardMetadataSave(t, a, cookie, ownedStandardFixtureID, `{"enabled":false,"fail_dashboard_meta":true}`, http.StatusOK)
	ownedDashboardMetadataRead(t, a, cookie, []map[string]any{}, ownedDashboardMetadataObservation{object, 1, 0})
	for _, raw := range []string{`null`, `false`, `0`, `""`, `[]`, `{}`} {
		t.Run("falsey_"+raw, func(t *testing.T) {
			ownedDashboardMetadataSave(t, a, cookie, ownedStandardFixtureID, `{"enabled":true,"fail_render":true,"dashboard_meta":`+raw+`}`, http.StatusOK)
			ownedDashboardMetadataRead(t, a, cookie, []map[string]any{}, ownedDashboardMetadataObservation{object, 1, 1})
		})
	}
	// No metadata result never falls back to the single-dashboard declaration.
	ownedDashboardMetadataSave(t, a, cookie, ownedStandardFixtureID, `{"enabled":true}`, http.StatusOK)
	ownedDashboardMetadataRead(t, a, cookie, []map[string]any{}, ownedDashboardMetadataObservation{object, 1, 1})
	ownedDashboardMetadataSave(t, a, cookie, ownedStandardFixtureID, `{"enabled":true,"fail_render":true,"dashboard_meta":[null,false,0,"",[],{},{"name":"First","key":"one"},{"name":"","key":""},{"name":null},{"key":"key-only"},{"other":1},{"name":7,"key":false},{"name":"Repeat","key":"one"}]}`, http.StatusOK)
	want := []map[string]any{
		{"id": ownedStandardFixtureID, "name": "First", "key": "one"},
		{"id": ownedStandardFixtureID, "name": "", "key": ""},
		{"id": ownedStandardFixtureID, "name": nil, "key": nil},
		{"id": ownedStandardFixtureID, "name": nil, "key": "key-only"},
		{"id": ownedStandardFixtureID, "name": nil, "key": nil},
		{"id": ownedStandardFixtureID, "name": float64(7), "key": false},
		{"id": ownedStandardFixtureID, "name": "Repeat", "key": "one"},
	}
	ownedDashboardMetadataRead(t, a, cookie, want, ownedDashboardMetadataObservation{object, 1, 1})
}

func TestOriginalDashboardMetadataActualSDKExceptionsAndInsertionOrder(t *testing.T) {
	a, cookie, object := ownedDashboardMetadataFixture(t, "multi")
	ownedDashboardMetadataSave(t, a, cookie, ownedStandardFixtureID, `{"enabled":true,"dashboard_meta":[{"name":"Source","key":"source"}]}`, http.StatusOK)
	// Deliberately reverse alphabetical clone order. Source dict snapshot order
	// must survive; metadata entries must also preserve each plugin's list order.
	zeta := ownedDashboardMetadataClone(t, a, cookie, "Zeta")
	alpha := ownedDashboardMetadataClone(t, a, cookie, "Alpha")
	ownedDashboardMetadataSave(t, a, cookie, zeta.installation.InstanceID, `{"enabled":true,"fail_render":true,"dashboard_meta":[{"name":"Z second","key":"z2"},{"name":"Z first","key":"z1"}]}`, http.StatusOK)
	ownedDashboardMetadataSave(t, a, cookie, alpha.installation.InstanceID, `{"enabled":true,"fail_render":true,"dashboard_meta":[{"name":"A","key":"a"}]}`, http.StatusOK)
	healthy := []map[string]any{
		{"id": ownedStandardFixtureID + "zeta", "name": "Z second", "key": "z2"},
		{"id": ownedStandardFixtureID + "zeta", "name": "Z first", "key": "z1"},
		{"id": ownedStandardFixtureID + "alpha", "name": "A", "key": "a"},
	}
	all := append([]map[string]any{{"id": ownedStandardFixtureID, "name": "Source", "key": "source"}}, healthy...)
	for i := 0; i < 5; i++ {
		ownedDashboardMetadataRead(t, a, cookie, all, ownedDashboardMetadataObservation{object, 1, 1}, ownedDashboardMetadataObservation{zeta, 1, 1}, ownedDashboardMetadataObservation{alpha, 1, 1})
	}
	for _, test := range []struct {
		name, body        string
		status, metaCalls int
	}{
		{"state_error", `{"enabled":true,"fail_state":true,"dashboard_meta":[{"name":"Must not leak","key":"bad"}]}`, http.StatusInternalServerError, 0},
		{"metadata_error", `{"enabled":true,"fail_dashboard_meta":true}`, http.StatusOK, 1},
		{"partial_list_error", `{"enabled":true,"dashboard_meta":[{"name":"Must not leak","key":"bad"},true,{"name":"Also must not leak","key":"bad2"}]}`, http.StatusOK, 1},
		{"object_result_error", `{"enabled":true,"dashboard_meta":{"name":"Not a list","key":"bad"}}`, http.StatusOK, 1},
		{"number_result_error", `{"enabled":true,"dashboard_meta":1}`, http.StatusOK, 1},
		{"string_result_error", `{"enabled":true,"dashboard_meta":"bad"}`, http.StatusOK, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ownedDashboardMetadataSave(t, a, cookie, ownedStandardFixtureID, test.body, test.status)
			ownedDashboardMetadataRead(t, a, cookie, healthy, ownedDashboardMetadataObservation{object, 1, test.metaCalls}, ownedDashboardMetadataObservation{zeta, 1, 1}, ownedDashboardMetadataObservation{alpha, 1, 1})
		})
	}
}
