package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func organizationFixture() PluginOrganization {
	return PluginOrganization{Folders: map[string]json.RawMessage{
		"旧格式": json.RawMessage(`["one"]`),
		"新格式": json.RawMessage(`{"plugins":["two","keep"],"order":0,"icon":"mdi-folder-music","showIcon":false,"background":"https://synthetic.invalid/private-test-image","future":{"x":0,"flag":false}}`),
	}, Order: json.RawMessage(`[{"type":"folder","id":"新格式","order":0},{"type":"plugin","id":"future-instance","order":0.11},{"type":"folder","id":"旧格式","order":2}]`)}
}

func TestPluginOrganizationOriginalShapesCASAndEncryptedRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewPluginStore(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := p.Organization(ctx)
	if err != nil || initial.Revision != 0 || len(initial.Folders) != 0 || string(initial.Order) != `[]` {
		t.Fatal(initial, err)
	}
	in := organizationFixture()
	out, err := p.SaveOrganization(ctx, in)
	if err != nil || out.Revision != 1 {
		t.Fatal(out, err)
	}
	if _, err := p.SaveOrganization(ctx, in); pluginErrorCode(err) != "PLUGIN_REVISION_CONFLICT" {
		t.Fatal("stale page overwrote folders", err)
	}
	var sealed []byte
	if err := s.DB.QueryRow(`SELECT sealed FROM plugin_organization`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("private-test-image")) || bytes.Contains(sealed, []byte("future-instance")) {
		t.Fatal("organization data not encrypted")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err = NewPluginStore(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := p.Organization(ctx)
	if err != nil || !reflect.DeepEqual(restored, out) {
		t.Fatal("restart changed old/new format or unknown fields", restored, out, err)
	}
	restored.Order = json.RawMessage(`[{"id":"two"},{"id":"one"},{"id":"future-instance"}]`)
	if _, err := p.SaveOrganization(ctx, restored); err != nil {
		t.Fatal("legacy order rejected", err)
	}
}

// These fixtures install the actual owned Base worker and clone its real code
// through the public manager. No native lifecycle, synthetic running object or
// pre-populated installation row substitutes for those operations.
func standardOrganizationFixture(t *testing.T) (*App, *http.Cookie, PluginOrganization) {
	t.Helper()
	a, cookie, source := ownedStandardSDKFixture(t)
	for _, suffix := range []string{"two", "keep"} {
		body, err := json.Marshal(map[string]string{"suffix": suffix})
		if err != nil {
			t.Fatal(err)
		}
		response := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedStandardFixtureID+"/clone", string(body), cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
		a.wg.Wait()
		value, ok := a.pluginRunningInstances.Load(ownedStandardFixtureID + suffix)
		if !ok || value == source || !value.(*originalPluginInstance).external {
			t.Fatal("organization fixture did not load an independent real Base clone", suffix)
		}
		ownedStandardSnapshotRead(t, value.(*originalPluginInstance))
	}
	for _, instance := range []string{ownedStandardFixtureID, ownedStandardFixtureID + "two", ownedStandardFixtureID + "keep"} {
		body, err := json.Marshal(map[string]any{"enabled": false, "key": instance})
		if err != nil {
			t.Fatal(err)
		}
		response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+instance, string(body), cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
		a.wg.Wait()
	}
	organization := organizationFixture()
	legacy, err := json.Marshal([]string{ownedStandardFixtureID})
	if err != nil {
		t.Fatal(err)
	}
	organization.Folders["旧格式"] = legacy
	var modern map[string]json.RawMessage
	if err := json.Unmarshal(organization.Folders["新格式"], &modern); err != nil {
		t.Fatal(err)
	}
	modern["plugins"], err = json.Marshal([]string{ownedStandardFixtureID + "two", ownedStandardFixtureID + "keep"})
	if err != nil {
		t.Fatal(err)
	}
	organization.Folders["新格式"], err = json.Marshal(modern)
	if err != nil {
		t.Fatal(err)
	}
	return a, cookie, organization
}

type organizationStandardState struct {
	Installation PluginInstallation
	Config       PluginStoredValue
	Data         PluginStoredValue
	Object       *originalPluginInstance
}

func readOrganizationStandardState(t *testing.T, a *App, instance string) organizationStandardState {
	t.Helper()
	ctx := context.Background()
	installation, err := a.pluginStore.Installation(ctx, instance)
	if err != nil || !installation.Installed {
		t.Fatal("expected retained standard installation", instance, installation, err)
	}
	config, err := a.pluginStore.Config(ctx, instance)
	if err != nil {
		t.Fatal(instance, err)
	}
	data, err := a.pluginStore.Data(ctx, instance, "shared")
	if err != nil {
		t.Fatal(instance, err)
	}
	value, ok := a.pluginRunningInstances.Load(instance)
	if !ok || !value.(*originalPluginInstance).external {
		t.Fatal("expected retained real Base object", instance)
	}
	return organizationStandardState{Installation: installation, Config: config, Data: data, Object: value.(*originalPluginInstance)}
}

// The native PluginUnInstall bulk adapter is retired. Both old- and new-shape
// cleanup assertions below now use ordinary manager uninstalls of real workers.
func TestPluginOrganizationStandardUninstallPreservesOtherContents(t *testing.T) {
	a, cookie, organization := standardOrganizationFixture(t)
	ctx := context.Background()
	saved, err := a.pluginStore.SaveOrganization(ctx, organization)
	if err != nil {
		t.Fatal(err)
	}
	before := map[string]organizationStandardState{}
	for _, instance := range []string{ownedStandardFixtureID, ownedStandardFixtureID + "two", ownedStandardFixtureID + "keep"} {
		before[instance] = readOrganizationStandardState(t, a, instance)
	}
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedStandardFixtureID+"/uninstall", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	installation, err := a.pluginStore.Installation(ctx, ownedStandardFixtureID)
	if err != nil || installation.Installed {
		t.Fatal("ordinary uninstall retained installed membership", installation, err)
	}
	if _, ok := a.pluginRunningInstances.Load(ownedStandardFixtureID); ok {
		t.Fatal("ordinary uninstall retained the loaded Base object")
	}
	config, err := a.pluginStore.Config(ctx, ownedStandardFixtureID)
	if err != nil || !reflect.DeepEqual(config, before[ownedStandardFixtureID].Config) {
		t.Fatal("ordinary uninstall changed retained source configuration", config, err)
	}
	data, err := a.pluginStore.Data(ctx, ownedStandardFixtureID, "shared")
	if err != nil || !reflect.DeepEqual(data, before[ownedStandardFixtureID].Data) {
		t.Fatal("ordinary uninstall changed retained source Base data", data, err)
	}
	current, err := a.pluginStore.Organization(ctx)
	if err != nil || current.Revision != saved.Revision+1 || string(current.Folders["旧格式"]) != `[]` || !bytes.Equal(current.Folders["新格式"], saved.Folders["新格式"]) || !bytes.Equal(current.Order, saved.Order) {
		t.Fatal("ordinary uninstall lost unrelated appearance/data/order", current, err)
	}
	for _, instance := range []string{ownedStandardFixtureID + "two", ownedStandardFixtureID + "keep"} {
		if after := readOrganizationStandardState(t, a, instance); !reflect.DeepEqual(after, before[instance]) {
			t.Fatal("ordinary uninstall changed another real instance", instance, after)
		}
	}
	response = callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedStandardFixtureID+"two/uninstall", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	installation, err = a.pluginStore.Installation(ctx, ownedStandardFixtureID+"two")
	if err != nil || installation.Installed {
		t.Fatal("second ordinary uninstall retained installed membership", installation, err)
	}
	if _, ok := a.pluginRunningInstances.Load(ownedStandardFixtureID + "two"); ok {
		t.Fatal("second ordinary uninstall retained the loaded Base object")
	}
	current, err = a.pluginStore.Organization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	members, err := pluginFolderMembers(current.Folders["新格式"])
	if err != nil || current.Revision != saved.Revision+2 || !reflect.DeepEqual(members, []string{ownedStandardFixtureID + "keep"}) || string(current.Folders["旧格式"]) != `[]` || !bytes.Contains(current.Folders["新格式"], []byte(`"showIcon":false`)) || !bytes.Contains(current.Folders["新格式"], []byte(`"future":{"x":0,"flag":false}`)) || !bytes.Equal(current.Order, saved.Order) {
		t.Fatal("standard manager failed source folder cleanup", current, members, err)
	}
	var previousAppearance, currentAppearance map[string]json.RawMessage
	if err := json.Unmarshal(saved.Folders["新格式"], &previousAppearance); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(current.Folders["新格式"], &currentAppearance); err != nil {
		t.Fatal(err)
	}
	delete(previousAppearance, "plugins")
	delete(currentAppearance, "plugins")
	if !reflect.DeepEqual(previousAppearance, currentAppearance) {
		t.Fatal("standard manager changed appearance or unknown fields", currentAppearance)
	}
	if after := readOrganizationStandardState(t, a, ownedStandardFixtureID+"keep"); !reflect.DeepEqual(after, before[ownedStandardFixtureID+"keep"]) {
		t.Fatal("second ordinary uninstall changed the retained instance", after)
	}
}

// Rollback belongs to the organization storage write, not the whole uninstall.
// Pinned original api-plugin.py:993-1030 commits installed membership first;
// :1088-1124 catches a folder-save failure and continues stopping the plugin.
func TestPluginOrganizationUninstallRollbackAndInvalidMembership(t *testing.T) {
	a, cookie, organization := standardOrganizationFixture(t)
	ctx := context.Background()
	saved, err := a.pluginStore.SaveOrganization(ctx, organization)
	if err != nil {
		t.Fatal(err)
	}
	before := map[string]organizationStandardState{}
	for _, instance := range []string{ownedStandardFixtureID, ownedStandardFixtureID + "two", ownedStandardFixtureID + "keep"} {
		before[instance] = readOrganizationStandardState(t, a, instance)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER synthetic_org_failure BEFORE UPDATE ON plugin_organization BEGIN SELECT RAISE(ABORT,'synthetic transaction failure'); END`); err != nil {
		t.Fatal(err)
	}
	// A direct valid storage mutation must report the real injected failure and
	// retain every stored field/revision, without changing installed workers.
	next := PluginOrganization{Revision: saved.Revision, Folders: map[string]json.RawMessage{"changed": json.RawMessage(`[]`)}, Order: json.RawMessage(`[]`)}
	if _, err := a.pluginStore.SaveOrganization(ctx, next); err == nil || !bytes.Contains([]byte(err.Error()), []byte("synthetic transaction failure")) {
		t.Fatal("organization write did not reach the injected failure", err)
	}
	current, err := a.pluginStore.Organization(ctx)
	if err != nil || !reflect.DeepEqual(saved, current) {
		t.Fatal("failed organization storage write committed a partial value", current, err)
	}
	for instance, original := range before {
		if after := readOrganizationStandardState(t, a, instance); !reflect.DeepEqual(after, original) {
			t.Fatal("organization storage failure changed a real instance", instance, after)
		}
	}
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedStandardFixtureID+"/uninstall", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	installation, err := a.pluginStore.Installation(ctx, ownedStandardFixtureID)
	if err != nil || installation.Installed {
		t.Fatal("folder failure incorrectly rolled back original installed-list removal", installation, err)
	}
	current, err = a.pluginStore.Organization(ctx)
	if err != nil || !reflect.DeepEqual(saved, current) {
		t.Fatal("folder write failure committed partial organization data", current, err)
	}
	if _, ok := a.pluginRunningInstances.Load(ownedStandardFixtureID); ok {
		t.Fatal("folder failure prevented the original stop step")
	}
	if _, ok := a.pluginRegisteredAPIs.Load(ownedStandardFixtureID); ok {
		t.Fatal("folder failure restored removed API registration")
	}
	config, err := a.pluginStore.Config(ctx, ownedStandardFixtureID)
	if err != nil || !reflect.DeepEqual(config, before[ownedStandardFixtureID].Config) {
		t.Fatal("folder failure changed retained source configuration", config, err)
	}
	data, err := a.pluginStore.Data(ctx, ownedStandardFixtureID, "shared")
	if err != nil || !reflect.DeepEqual(data, before[ownedStandardFixtureID].Data) {
		t.Fatal("folder failure changed retained source Base data", data, err)
	}
	flushTestLogs(t, a)
	entries, err := a.readLogs(map[string]string{"module": "plugins", "limit": "500"})
	if err != nil {
		t.Fatal(err)
	}
	folderFailures := 0
	for _, entry := range entries {
		if entry["event"] == "remove_folder_failed" {
			folderFailures++
		}
	}
	if folderFailures != 1 {
		t.Fatal("manager did not observe and report the actual folder write failure", folderFailures)
	}
	if _, err := pluginFolderMembers(json.RawMessage(" \n")); err == nil {
		t.Fatal("blank raw folder accepted")
	}
	current, err = a.pluginStore.Organization(ctx)
	if err != nil || !reflect.DeepEqual(saved, current) {
		t.Fatal("invalid membership changed persisted organization", current, err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER synthetic_org_failure`); err != nil {
		t.Fatal(err)
	}
	// Retrying the actual endpoint repairs the pending folder cleanup; it must
	// neither reinstall the source nor alter the surviving real instances.
	response = callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedStandardFixtureID+"/uninstall", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	current, err = a.pluginStore.Organization(ctx)
	if err != nil || current.Revision != saved.Revision+1 || string(current.Folders["旧格式"]) != `[]` || !bytes.Equal(current.Folders["新格式"], saved.Folders["新格式"]) || !bytes.Equal(current.Order, saved.Order) {
		t.Fatal("retry failed to repair only pending folder membership", current, err)
	}
	installation, err = a.pluginStore.Installation(ctx, ownedStandardFixtureID)
	if err != nil || installation.Installed {
		t.Fatal("folder cleanup retry reinstalled source", installation, err)
	}
	for _, instance := range []string{ownedStandardFixtureID + "two", ownedStandardFixtureID + "keep"} {
		if after := readOrganizationStandardState(t, a, instance); !reflect.DeepEqual(after, before[instance]) {
			t.Fatal("folder failure or retry changed another real instance", instance, after)
		}
	}
}

func TestPluginOrganizationHTTPAuthOriginAndRevision(t *testing.T) {
	a := newTestApp(t)
	request := func(method, path string, cookie *http.Cookie, origin string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "http://example.test"+path, bytes.NewReader(raw))
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	const foldersPath = "/api/v1/plugin/folders"
	const orderPath = "/api/v1/user/config/PluginOrder"
	if got := request("GET", foldersPath, nil, "", nil); got.Code != 401 {
		t.Fatal(got.Code, got.Body.String())
	}
	cookie := setupTestAdmin(t, a)
	value := organizationFixture()
	if got := request("POST", foldersPath, cookie, "https://other.invalid", value.Folders); got.Code != 403 {
		t.Fatal(got.Code, got.Body.String())
	}
	assertSourceSuccess := func(got *httptest.ResponseRecorder) {
		t.Helper()
		var response struct {
			Success bool `json:"success"`
		}
		if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &response) != nil || !response.Success || bytes.Contains(got.Body.Bytes(), []byte(`"revision"`)) {
			t.Fatal("original folder response", got.Code, got.Body.String())
		}
	}
	assertSourceSuccess(request("POST", foldersPath, cookie, "http://example.test", value.Folders))
	stored, err := a.pluginStore.Organization(context.Background())
	if err != nil || stored.Revision != 1 {
		t.Fatal("folder write did not commit the real storage revision", stored, err)
	}
	// CAS is a private storage protection, not an original HTTP parameter.
	// Keep its rejection assertion at the real storage entry instead of sending
	// the removed /api/plugins/organization route as a plugin configuration PUT.
	if _, err := a.pluginStore.SaveOrganization(context.Background(), value); pluginErrorCode(err) != "PLUGIN_REVISION_CONFLICT" {
		t.Fatal("stale organization storage write accepted", err)
	}
	assertSourceSuccess(request("POST", foldersPath, cookie, "http://example.test", value.Folders))
	assertSourceSuccess(request("POST", orderPath, cookie, "http://example.test", value.Order))
	got := request("GET", foldersPath, cookie, "", nil)
	var folders map[string]json.RawMessage
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &folders) != nil || !reflect.DeepEqual(folders, value.Folders) {
		t.Fatal("folder read lost original shapes/fields", got.Code, got.Body.String())
	}
	got = request("GET", orderPath, cookie, "", nil)
	var order struct {
		Data struct {
			Value json.RawMessage `json:"value"`
		} `json:"data"`
	}
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &order) != nil || !originalBaseJSONEqual(order.Data.Value, value.Order) || !bytes.Contains(got.Body.Bytes(), []byte(`future-instance`)) {
		t.Fatal("source user order was lost", got.Code, got.Body.String())
	}
}
