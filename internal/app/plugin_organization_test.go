package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/uninstall"
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

func TestPluginOrganizationUninstallAndBulkUninstallPreserveOtherContents(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	for _, instance := range []string{"one", "two", "keep"} {
		uninstallInstall(t, a, instance, builtin.SystemProcessManifest(), map[string]any{"enable": false}, false)
	}
	saved, err := a.pluginStore.SaveOrganization(ctx, organizationFixture())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.pluginStore.Uninstall(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	current, _ := a.pluginStore.Organization(ctx)
	if current.Revision != saved.Revision+1 || string(current.Folders["旧格式"]) != `[]` || !bytes.Equal(current.Folders["新格式"], saved.Folders["新格式"]) {
		t.Fatal("regular uninstall lost unrelated appearance/data", current)
	}
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"two"}}, true)
	result := uninstallApply(t, a, &run)
	if !reflect.DeepEqual(result.Removed, []string{"two"}) {
		t.Fatal(result)
	}
	current, _ = a.pluginStore.Organization(ctx)
	members, err := pluginFolderMembers(current.Folders["新格式"])
	if err != nil || !reflect.DeepEqual(members, []string{"keep"}) || !bytes.Contains(current.Folders["新格式"], []byte(`"showIcon":false`)) || !bytes.Contains(current.Folders["新格式"], []byte(`"future":{"x":0,"flag":false}`)) || !bytes.Equal(current.Order, saved.Order) {
		t.Fatal("bulk uninstall failed source folder cleanup", current, members, err)
	}
}

func TestPluginOrganizationUninstallRollbackAndInvalidMembership(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	installStoredPlugin(t, p, "one")
	saved, err := p.SaveOrganization(ctx, organizationFixture())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`CREATE TRIGGER synthetic_org_failure BEFORE UPDATE ON plugin_organization BEGIN SELECT RAISE(ABORT,'synthetic transaction failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := p.Uninstall(ctx, "one"); err == nil {
		t.Fatal("folder write failure did not abort uninstall")
	}
	installation, _ := p.Installation(ctx, "one")
	current, _ := p.Organization(ctx)
	if !installation.Installed || !reflect.DeepEqual(saved, current) {
		t.Fatal("partial uninstall committed", installation, current)
	}
	for _, folders := range []string{`{"x":["one","one"]}`, `{"x":["one"],"y":{"plugins":["one"]}}`, `{"one":["one"]}`, `{"x":["../bad"]}`, `{"x":{"plugins":null}}`, `{" ":[]}`} {
		var value map[string]json.RawMessage
		if err := json.Unmarshal([]byte(folders), &value); err != nil {
			t.Fatal(err)
		}
		if _, err := p.SaveOrganization(ctx, PluginOrganization{Revision: saved.Revision, Folders: value, Order: json.RawMessage(`[]`)}); pluginErrorCode(err) != "INVALID_PLUGIN_FOLDER" {
			t.Fatal("invalid folder accepted", folders, err)
		}
	}
	if _, err := pluginFolderMembers(json.RawMessage(" \n")); err == nil {
		t.Fatal("blank raw folder accepted")
	}
}

func TestPluginOrganizationHTTPAuthOriginAndRevision(t *testing.T) {
	a := newTestApp(t)
	request := func(method string, cookie *http.Cookie, origin string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "http://example.test/api/plugins/organization", bytes.NewReader(raw))
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
	if got := request("GET", nil, "", nil); got.Code != 401 {
		t.Fatal(got.Code, got.Body.String())
	}
	cookie := setupTestAdmin(t, a)
	if got := request("PUT", cookie, "https://other.invalid", organizationFixture()); got.Code != 403 {
		t.Fatal(got.Code, got.Body.String())
	}
	if got := request("PUT", cookie, "http://example.test", organizationFixture()); got.Code != 200 || !bytes.Contains(got.Body.Bytes(), []byte(`"revision":1`)) {
		t.Fatal(got.Code, got.Body.String())
	}
	if got := request("PUT", cookie, "http://example.test", organizationFixture()); got.Code == 200 || !bytes.Contains(got.Body.Bytes(), []byte(`PLUGIN_REVISION_CONFLICT`)) {
		t.Fatal(got.Code, got.Body.String())
	}
	if got := request("GET", cookie, "", nil); got.Code != 200 || !bytes.Contains(got.Body.Bytes(), []byte(`future-instance`)) {
		t.Fatal(got.Code, got.Body.String())
	}
}
