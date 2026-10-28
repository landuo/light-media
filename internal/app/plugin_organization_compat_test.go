package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func organizationCompatRequest(a *App, cookie *http.Cookie, method, endpoint string, raw []byte, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://example.test"+endpoint, bytes.NewReader(raw))
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

func TestPluginOrganizationCompatInstalledASGIReference(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugins/plugin-organization-api-original-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Source map[string]string `json:"source_sha256"`
		Cases  []struct {
			Name, Method, Endpoint, Version string
			Body, Result, Folders           json.RawMessage
			Status                          int
			Allow                           *string
		}
	}
	if err = json.Unmarshal(raw, &reference); err != nil || len(reference.Cases) != 32 || reference.Source["plugin"] != "16e96285764587b266c07af21b6df140836c00f916d6626b87584a492a4031ce" || reference.Source["response"] != "0afd53be12ee3a6f513e74e10c98344b0d9f66e01c250e66a74ccee604bc1970" {
		t.Fatal("installed ASGI reference changed", err)
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	for _, c := range reference.Cases {
		t.Run(c.Name, func(t *testing.T) {
			w := organizationCompatRequest(a, cookie, c.Method, "/api/"+c.Version+"/"+c.Endpoint, c.Body, "http://example.test")
			if w.Code != c.Status {
				t.Fatalf("status %d, want %d: %s", w.Code, c.Status, w.Body.String())
			}
			var got, want any
			if json.Unmarshal(w.Body.Bytes(), &got) != nil || json.Unmarshal(c.Result, &want) != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("original response mismatch\ngot: %s\nwant: %s", w.Body.String(), c.Result)
			}
			allow := ""
			if c.Allow != nil {
				allow = *c.Allow
			}
			if w.Header().Get("Allow") != allow {
				t.Fatal("original Allow changed", w.Header().Get("Allow"), allow)
			}
			current, err := a.pluginStore.Organization(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			folders, _ := json.Marshal(current.Folders)
			json.Unmarshal(folders, &got)
			json.Unmarshal(c.Folders, &want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("source folder state differs\ngot: %s\nwant: %s", folders, c.Folders)
			}
			if strings.Contains(w.Body.String(), `"request_id"`) || strings.Contains(w.Body.String(), `"revision"`) || strings.Contains(w.Body.String(), `"order_present"`) {
				t.Fatal("native envelope/private metadata leaked into original response")
			}
		})
	}
}

func TestPluginOrganizationCompatAuthorityRollbackAndConcurrentOwnership(t *testing.T) {
	a := newTestApp(t)
	path := "/api/v1/plugin/folders"
	if w := organizationCompatRequest(a, nil, "GET", path, nil, ""); w.Code != 401 {
		t.Fatal("management read without admin session", w.Code)
	}
	cookie := setupTestAdmin(t, a)
	if w := organizationCompatRequest(a, cookie, "POST", path, []byte(`{}`), "https://cross.invalid"); w.Code != 403 {
		t.Fatal("cross origin mutation", w.Code)
	}
	ctx := context.Background()
	if _, err := a.pluginStore.SaveOrganization(ctx, organizationFixture()); err != nil {
		t.Fatal(err)
	}
	before, _ := a.pluginStore.Organization(ctx)
	if w := organizationCompatRequest(a, cookie, "POST", path, []byte(`[]`), ""); w.Code != 422 {
		t.Fatal("invalid top-level dictionary accepted", w.Code, w.Body.String())
	}
	after, _ := a.pluginStore.Organization(ctx)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rejected operation changed stored layout")
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER synthetic_compat_failure BEFORE UPDATE ON plugin_organization BEGIN SELECT RAISE(ABORT,'must never expose private database diagnostics'); END`); err != nil {
		t.Fatal(err)
	}
	w := organizationCompatRequest(a, cookie, "POST", path+"/事务失败", nil, "")
	if w.Code != 503 || bytes.Contains(w.Body.Bytes(), []byte("private database")) {
		t.Fatal("SQL failure was successful or leaked diagnostics", w.Code, w.Body.String())
	}
	after, _ = a.pluginStore.Organization(ctx)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("SQL failure partially saved folders")
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER synthetic_compat_failure`); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		group.Add(1)
		go func(name string) {
			defer group.Done()
			if w := organizationCompatRequest(a, cookie, "POST", path+"/"+name, nil, ""); w.Code != 200 {
				t.Errorf("concurrent folder %s: %d %s", name, w.Code, w.Body.String())
			}
		}(name)
	}
	group.Wait()
	after, _ = a.pluginStore.Organization(ctx)
	if len(after.Folders) != len(before.Folders)+8 || !bytes.Equal(after.Order, before.Order) {
		t.Fatal("concurrent create lost another folder or overwrote PluginOrder", len(after.Folders))
	}
}

func TestPluginOrganizationOrderPresenceAndLegacyEncryptedRow(t *testing.T) {
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
	if err := p.mutateOrganization(ctx, func(in *PluginOrganization) (bool, error) {
		in.Folders["folder-only"] = json.RawMessage(`[]`)
		return true, nil
	}); err != nil {
		t.Fatal(err)
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
	current, err := p.Organization(ctx)
	if err != nil || current.orderPresent || string(current.Order) != `[]` || len(current.Folders) != 1 {
		t.Fatal("restart converted unset order into explicit empty order", current, err)
	}
	// Rows written before order_present always came from the complete native
	// endpoint; continue to interpret their existing order as explicitly saved.
	raw, _ := json.Marshal(current)
	sealed, err := p.seal("organization", "admin", "main", raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE plugin_organization SET sealed=?`, sealed); err != nil {
		t.Fatal(err)
	}
	current, err = p.Organization(ctx)
	if err != nil || !current.orderPresent {
		t.Fatal("legacy native row lost explicit order", err)
	}
}
