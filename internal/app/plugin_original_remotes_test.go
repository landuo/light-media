package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOriginalRemotesActualSDKNoStateGate(t *testing.T) {
	a, cookie, p := ownedStandardSDKFixture(t)
	for _, body := range []string{`{"enabled":false,"render_mode":["vue","/frontend/assets/"],"fail_state":true}`, `{"enabled":false,"render_mode":["vue","../dist"]}`} {
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+ownedStandardFixtureID, body, cookie, "")
		if !strings.Contains(body, "fail_state") {
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		}
		before := ownedStandardSnapshotRead(t, p)
		value, err := a.originalPluginRemotes(context.Background(), "")
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(value)
		var rows []map[string]any
		if json.Unmarshal(raw, &rows) != nil || len(rows) != 1 {
			t.Fatal(string(raw))
		}
		want := "/plugin/file/" + strings.ToLower(ownedStandardFixtureID) + "/frontend/assets/remoteEntry.js"
		if strings.Contains(body, "../dist") {
			want = "/plugin/file/" + strings.ToLower(ownedStandardFixtureID) + "/../dist/remoteEntry.js"
		}
		if rows[0]["url"] != want || rows[0]["name"] != "Owned standard fixture" {
			t.Fatal(rows)
		}
		after := ownedStandardSnapshotRead(t, p)
		if before.Nonce != after.Nonce || before.InitCount != after.InitCount {
			t.Fatal("discovery reinitialized object")
		}
	}
	value, err := a.originalPluginRemotes(context.Background(), "missing")
	raw, _ := json.Marshal(value)
	if err != nil || string(raw) != "[]" {
		t.Fatal(string(raw), err)
	}
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+ownedStandardFixtureID, `{"enabled":true,"fail_render":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	if _, err := a.originalPluginRemotes(context.Background(), ""); err == nil {
		t.Fatal("source render exception swallowed")
	}
}

func TestOriginalRemoteDiscoveryHTTPAuthenticationIsNarrow(t *testing.T) {
	a, cookie, _ := ownedStandardSDKFixture(t)
	for _, version := range []string{"v1", "v2"} {
		base := "/api/" + version + "/plugin/"
		for _, c := range []struct {
			path string
			want int
		}{{"remotes", 422}, {"remotes?token=wrong", 403}, {"remotes?token=moviepilot", 200}, {"sidebar_nav?token=moviepilot", 401}, {"file/" + ownedStandardFixtureID + "/anything?token=moviepilot", 403}} {
			r := callTest(t, a.Handler(), "GET", base+c.path, "", nil, "")
			if r.Code != c.want {
				t.Fatalf("%s=%d expected%d: %s", c.path, r.Code, c.want, r.Body.String())
			}
		}
		r := callTest(t, a.Handler(), "GET", base+"sidebar_nav", "", cookie, "")
		if r.Code != 200 {
			t.Fatal("single-user cookie adaptation rejected fixed wire alias", r.Code)
		}
		req := httptest.NewRequest("GET", base+"sidebar_nav", nil)
		req.Header.Set("Authorization", "Bearer "+cookie.Value)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatal("actual bearer rejected", w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"remotes", "navigation", "sidebar_nav"} {
		r := callTest(t, a.Handler(), "GET", "/api/plugins/"+path+"?token=moviepilot", "", nil, "")
		if r.Code != 401 {
			t.Fatal("public discovery token granted native management", path, r.Code)
		}
	}
}

func TestOriginalRemotesNativeHTTPPrecedesSingleInstanceConfig(t *testing.T) {
	a, cookie, _ := ownedStandardSDKFixture(t)
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+ownedStandardFixtureID, `{"enabled":true,"render_mode":["vue","assets"]}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	for _, path := range []string{"remotes", "navigation", "sidebar_nav"} {
		r := callTest(t, a.Handler(), "GET", "/api/plugins/"+path, "", cookie, "")
		var envelope struct{ Data []map[string]any }
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &envelope) != nil || len(envelope.Data) != 1 {
			t.Fatalf("native discovery was captured by config route: %s %d %s", path, r.Code, r.Body.String())
		}
		field := "plugin_id"
		if path == "remotes" {
			field = "id"
		}
		if envelope.Data[0][field] != ownedStandardFixtureID {
			t.Fatal(path, envelope.Data)
		}
	}
	r = callTest(t, a.Handler(), "GET", "/api/plugins/"+ownedStandardFixtureID, "", cookie, "")
	var envelope struct{ Data map[string]any }
	if json.Unmarshal(r.Body.Bytes(), &envelope) != nil || envelope.Data["enabled"] != true {
		t.Fatal("ordinary config read changed", r.Body.String())
	}
}
