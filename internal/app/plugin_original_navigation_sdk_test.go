package app

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestOriginalNavigationActualSDKSourceContract(t *testing.T) {
	a, cookie, p := ownedStandardSDKFixture(t)
	save := func(value string) {
		t.Helper()
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+ownedStandardFixtureID, value, cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	}
	read := func() []map[string]any {
		t.Helper()
		value, e := a.pluginNavigation(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		raw, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		var result []map[string]any
		if json.Unmarshal(raw, &result) != nil {
			t.Fatal("invalid nav", string(raw))
		}
		return result
	}
	save(`{"enabled":true}`)
	if got := read(); len(got) != 0 {
		t.Fatal("vuetify included", got)
	}
	save(`{"enabled":false,"render_mode":["vue",""],"fail_sidebar":true}`)
	if got := read(); len(got) != 0 {
		t.Fatal("disabled included", got)
	}
	save(`{"enabled":true,"render_mode":["vue",""],"sidebar_nav":[{"nav_key":"z","section":"START","order":"2","permission":"admin"},{"key":" a ","title":"","icon":false,"section":"bad","permission":"bogus","order":"bad"},{"nav_key":"bad/path"},{"nav_key":"bad key"},null,{},"not-a-dict",{"title":"Fallback","order":true}]}`)
	before := ownedStandardSnapshotRead(t, p)
	got := read()
	if len(got) != 3 {
		t.Fatalf("source skips individual malformed entries, not entire plugin: %+v", got)
	}
	want := []map[string]any{
		{"plugin_id": ownedStandardFixtureID, "nav_key": "z", "title": "Owned standard fixture", "icon": "mdi-puzzle", "section": "start", "permission": "admin", "order": float64(2)},
		{"plugin_id": ownedStandardFixtureID, "nav_key": "a", "title": "Owned standard fixture", "icon": "mdi-puzzle", "section": "system", "permission": nil, "order": float64(0)},
		{"plugin_id": ownedStandardFixtureID, "nav_key": "main", "title": "Fallback", "icon": "mdi-puzzle", "section": "system", "permission": nil, "order": float64(1)},
	}
	for i, row := range got {
		delete(row, "instance_id")
		if !reflect.DeepEqual(row, want[i]) {
			t.Fatalf("source normalized nav[%d] got=%+v want=%+v", i, row, want[i])
		}
	}
	after := ownedStandardSnapshotRead(t, p)
	if before.Nonce != after.Nonce || before.InitCount != after.InitCount {
		t.Fatal("navigation reinitialized/replaced actual object")
	}
}

func TestOriginalNavigationActualSDKExceptionBoundary(t *testing.T) {
	for _, method := range []string{"state", "render", "sidebar"} {
		t.Run(method, func(t *testing.T) {
			a, cookie, p := ownedStandardSDKFixture(t)
			body := `{"enabled":true,"render_mode":["vue",""],"fail_` + method + `":true}`
			r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+ownedStandardFixtureID, body, cookie, "")
			// Original init_plugin calls get_state after actual Init. Its state error
			// propagates after the new config/member values were already retained.
			if method == "state" {
				if r.Code != 500 {
					t.Fatalf("state setup error was swallowed: %d %s", r.Code, r.Body.String())
				}
			} else {
				assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			}
			before := ownedStandardSnapshotRead(t, p)
			value, err := a.pluginNavigation(context.Background())
			if method == "sidebar" {
				if err != nil {
					t.Fatal("sidebar error escaped source try", err)
				}
				raw, _ := json.Marshal(value)
				if string(raw) != "[]" {
					t.Fatal("failed sidebar produced nav", string(raw))
				}
			} else if err == nil {
				t.Fatal("source pre-try error swallowed", method)
			}
			after := ownedStandardSnapshotRead(t, p)
			if before.Nonce != after.Nonce || before.InitCount != after.InitCount {
				t.Fatal("error changed actual instance")
			}
		})
	}
}
