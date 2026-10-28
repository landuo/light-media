package app

import (
	"bytes"
	"context"
	"encoding/json"
	"light-media/internal/plugins/artifacts"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func organizationUIRequest(t *testing.T, a *App, cookie *http.Cookie, method string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	return organizationCompatRequest(a, cookie, method, "/api/plugins/organization", raw, "http://example.test")
}

func readOrganizationUIResponse(t *testing.T, response *httptest.ResponseRecorder) PluginOrganization {
	t.Helper()
	var result struct {
		Data PluginOrganization `json:"data"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Data.Folders == nil || result.Data.Order == nil {
		t.Fatalf("actual plugin-page organization response: %d %s", response.Code, response.Body.String())
	}
	return result.Data
}

// The real Plugins component reads local classes, market, statistics and
// organization in that order. The folders transport alone is not the entry.
func TestPluginOrganizationUIInitialLoadAndIndependentSourceWrites(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	installed := callTest(t, a.Handler(), "GET", "/api/plugins?state=installed", "", cookie, "")
	if installed.Code != http.StatusOK {
		t.Fatal("plugin inventory", installed.Code, installed.Body.String())
	}
	readOriginalInstalledIDs(t, a, cookie, []string{})
	initial := readOrganizationUIResponse(t, organizationUIRequest(t, a, cookie, "GET", nil))
	if len(initial.Folders) != 0 || !bytes.Equal(initial.Order, []byte("[]")) {
		t.Fatal("fresh page invented organization", initial)
	}

	// Save through the existing original transport first. A facade folder write
	// must not overwrite a separately saved user order or require its revision.
	order := json.RawMessage(`[{"id":"future-instance","type":"plugin","order":0.125}]`)
	response := organizationCompatRequest(a, cookie, "POST", "/api/v1/user/config/PluginOrder", order, "http://example.test")
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	folders := map[string]json.RawMessage{"分组": json.RawMessage(`{"plugins":[],"showIcon":false,"future":{"zero":0}}`)}
	updated := readOrganizationUIResponse(t, organizationUIRequest(t, a, cookie, "PUT", map[string]any{"revision": -100, "folders": folders}))
	if !reflect.DeepEqual(updated.Folders, folders) || !originalBaseJSONEqual(updated.Order, order) {
		t.Fatal("folder save changed user order or source fields", updated)
	}
	otherOrder := json.RawMessage(`[{"id":"分组","type":"folder","order":0},"future-instance"]`)
	updated = readOrganizationUIResponse(t, organizationUIRequest(t, a, cookie, "PUT", map[string]any{"order": otherOrder}))
	if !reflect.DeepEqual(updated.Folders, folders) || !originalBaseJSONEqual(updated.Order, otherOrder) {
		t.Fatal("order save changed folders", updated)
	}
	oldSource := organizationCompatRequest(a, cookie, "GET", "/api/v1/plugin/folders", nil, "")
	var sourceFolders map[string]json.RawMessage
	if oldSource.Code != 200 || json.Unmarshal(oldSource.Body.Bytes(), &sourceFolders) != nil || !reflect.DeepEqual(sourceFolders, folders) {
		t.Fatal("facade used a separate organization store", oldSource.Code, oldSource.Body.String())
	}

	for _, input := range []any{map[string]any{"folders": nil}, map[string]any{"order": nil}, map[string]any{"order": map[string]any{}}, map[string]any{"folders": []any{}}} {
		response = organizationUIRequest(t, a, cookie, "PUT", input)
		if response.Code < 400 {
			t.Fatal("invalid source shape accepted", input, response.Body.String())
		}
	}
	after := readOrganizationUIResponse(t, organizationUIRequest(t, a, cookie, "GET", nil))
	if !reflect.DeepEqual(after, updated) {
		t.Fatal("rejected body changed organization", after, updated)
	}
}

func TestPluginOrganizationUIAuthenticationAndRollback(t *testing.T) {
	a := newTestApp(t)
	if response := organizationUIRequest(t, a, nil, "GET", nil); response.Code != 401 {
		t.Fatal("anonymous organization read", response.Code)
	}
	cookie := setupTestAdmin(t, a)
	initial := readOrganizationUIResponse(t, organizationUIRequest(t, a, cookie, "PUT", map[string]any{"folders": map[string]any{"组": []string{}}, "order": []string{}}))
	response := organizationCompatRequest(a, cookie, "PUT", "/api/plugins/organization", []byte(`{"folders":{}}`), "https://other.invalid")
	if response.Code != 403 {
		t.Fatal("cross-origin organization write", response.Code)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_organization_ui_failure BEFORE UPDATE ON plugin_organization BEGIN SELECT RAISE(ABORT,'owned organization write failure'); END`); err != nil {
		t.Fatal(err)
	}
	response = organizationUIRequest(t, a, cookie, "PUT", map[string]any{"folders": map[string]any{"new": []string{}}, "order": []string{"new"}})
	if response.Code < 400 {
		t.Fatal("failed commit reported success", response.Code, response.Body.String())
	}
	after := readOrganizationUIResponse(t, organizationUIRequest(t, a, cookie, "GET", nil))
	if !reflect.DeepEqual(after, initial) {
		t.Fatal("failed organization write changed state", after, initial)
	}
}

func TestPluginOrganizationUIRealSourceAndClonesRemainIndependent(t *testing.T) {
	a, cookie, organization := standardOrganizationFixture(t)
	readOriginalInstalledIDs(t, a, cookie, []string{ownedStandardFixtureID, ownedStandardFixtureID + "two", ownedStandardFixtureID + "keep"})
	before := map[string]organizationStandardState{}
	for _, instance := range []string{ownedStandardFixtureID, ownedStandardFixtureID + "two", ownedStandardFixtureID + "keep"} {
		before[instance] = readOrganizationStandardState(t, a, instance)
	}
	organization.Revision = -999 // A browser does not need a source CAS token.
	saved := readOrganizationUIResponse(t, organizationUIRequest(t, a, cookie, "PUT", organization))
	if !reflect.DeepEqual(saved.Folders, organization.Folders) || !originalBaseJSONEqual(saved.Order, organization.Order) {
		t.Fatal("source/clone grouping changed", saved)
	}
	for instance, prior := range before {
		after := readOrganizationStandardState(t, a, instance)
		if !reflect.DeepEqual(after, prior) {
			t.Fatal("grouping mutated actual plugin instance", instance, after, prior)
		}
	}
	readback, err := a.pluginStore.Organization(context.Background())
	if err != nil || !reflect.DeepEqual(readback.Folders, saved.Folders) || !originalBaseJSONEqual(readback.Order, saved.Order) {
		t.Fatal("organization was not persisted", readback, err)
	}
	if err := a.stopOriginalPlugin(context.Background(), ownedStandardFixtureID+"two"); err != nil {
		t.Fatal(err)
	}
	readOriginalInstalledIDs(t, a, cookie, []string{ownedStandardFixtureID, ownedStandardFixtureID + "two", ownedStandardFixtureID + "keep"})
	response := callTest(t, a.Handler(), "DELETE", "/api/plugins/"+ownedStandardFixtureID+"keep", "", cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	readOriginalInstalledIDs(t, a, cookie, []string{ownedStandardFixtureID, ownedStandardFixtureID + "two"})
}

func readOriginalInstalledIDs(t *testing.T, a *App, cookie *http.Cookie, want []string) {
	t.Helper()
	response := callTest(t, a.Handler(), "GET", "/api/plugins/installed", "", cookie, "")
	var result struct {
		Data []string `json:"data"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || !reflect.DeepEqual(result.Data, want) {
		t.Fatal("installed-list HTTP projection", response.Code, response.Body.String(), want)
	}
}

func TestPluginOrganizationUIInstalledAppendOrderAndRollback(t *testing.T) {
	a, cookie, _ := standardOrganizationFixture(t)
	ctx := context.Background()
	const source = ownedStandardFixtureID
	const two = source + "two"
	const keep = source + "keep"
	installed, err := artifacts.ReadInstalled(a.pluginPackageRoot(), source, validatePluginArtifactContract)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise a pre-order-record DB with equal timestamps. The migration tie
	// is physical insertion order, not alphabetical clone ID or browser order.
	if _, err = a.Store.DB.Exec(`DELETE FROM records WHERE kind=?`, originalInstalledOrderKind); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_installations SET created_at='2026-10-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	readOriginalInstalledIDs(t, a, cookie, []string{source, two, keep})
	install := func(force bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"plugin_id": source, "repo_url": installed.RepositoryURL, "force": force})
		r := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
	}
	remove := func(id string) {
		t.Helper()
		r := callTest(t, a.Handler(), "DELETE", "/api/plugins/"+id, "", cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	}
	install(false) // Updating existing code must not move its installed position.
	readOriginalInstalledIDs(t, a, cookie, []string{source, two, keep})
	remove(source)
	readOriginalInstalledIDs(t, a, cookie, []string{two, keep})
	install(false)
	readOriginalInstalledIDs(t, a, cookie, []string{two, keep, source})
	install(true) // Artifact replacement is also an update, not another append.
	readOriginalInstalledIDs(t, a, cookie, []string{two, keep, source})
	remove(two)
	r := callTest(t, a.Handler(), "POST", "/api/plugins/"+source+"/clone", `{"suffix":"two"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	readOriginalInstalledIDs(t, a, cookie, []string{keep, source, two})
	readOrganizationUIResponse(t, organizationUIRequest(t, a, cookie, "PUT", map[string]any{"order": []string{two, keep, source}}))
	readOriginalInstalledIDs(t, a, cookie, []string{keep, source, two})
	loaded, _ := a.pluginRunningInstances.Load(source)
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_installed_order_failure BEFORE UPDATE ON records WHEN NEW.kind='plugin-original-installed-order-v1' BEGIN SELECT RAISE(ABORT,'owned installed order failure'); END`); err != nil {
		t.Fatal(err)
	}
	r = callTest(t, a.Handler(), "DELETE", "/api/plugins/"+source, "", cookie, "")
	if r.Code < 400 {
		t.Fatal("failed membership commit reported success", r.Code, r.Body.String())
	}
	readOriginalInstalledIDs(t, a, cookie, []string{keep, source, two})
	in, err := a.pluginStore.Installation(ctx, source)
	if current, _ := a.pluginRunningInstances.Load(source); err != nil || !in.Installed || current != loaded {
		t.Fatal("failed list write changed membership or stopped instance", in, err)
	}
}
