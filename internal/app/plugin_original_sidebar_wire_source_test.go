package app

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

func TestOriginalSidebarWireFixedPydantic(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-navigation/dashboard-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name     string
			Input    json.RawMessage
			Expected struct {
				Status int
				Value  json.RawMessage
			}
		} `json:"sidebar_wire_cases"`
	}
	if json.Unmarshal(raw, &oracle) != nil {
		t.Fatal("sidebar DTO oracle")
	}
	for _, c := range oracle.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var rows []map[string]any
			d := json.NewDecoder(bytes.NewReader(c.Input))
			d.UseNumber()
			if d.Decode(&rows) != nil {
				t.Fatal("source rows")
			}
			got, err := originalPluginSidebarProjection(rows, false)
			if c.Expected.Status == 500 {
				if e, ok := err.(*APIError); !ok || e.Status != 500 {
					t.Fatal("source response validation must fail500", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(got)
			var a, b any
			decode := func(raw []byte, out *any) {
				d := json.NewDecoder(bytes.NewReader(raw))
				d.UseNumber()
				if d.Decode(out) != nil {
					t.Fatal("invalid DTO")
				}
			}
			decode(raw, &a)
			decode(c.Expected.Value, &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatal(string(raw), string(c.Expected.Value))
			}
		})
	}
}
func TestOriginalSidebarWireActualSDKFailureAndAliasProjection(t *testing.T) {
	a, cookie, _ := ownedStandardSDKFixture(t)
	for _, title := range []string{`[1]`, `"Owned strict title"`} {
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+ownedStandardFixtureID, `{"enabled":true,"render_mode":["vue","assets"],"sidebar_nav":[{"nav_key":"main","title":`+title+`}]}`, cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		want := 200
		if title == `[1]` {
			want = 500
		}
		r = callTest(t, a.Handler(), "GET", "/api/plugins/navigation", "", cookie, "")
		if r.Code != want {
			t.Fatal("native DTO", r.Code, r.Body.String())
		}
		for _, version := range []string{"v1", "v2"} {
			req := httptest.NewRequest("GET", "/api/"+version+"/plugin/sidebar_nav", nil)
			req.Header.Set("Authorization", "Bearer "+cookie.Value)
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, req)
			if w.Code != want {
				t.Fatal("fixed alias", version, w.Code, w.Body.String())
			}
			if want == 200 {
				var rows []map[string]any
				if json.Unmarshal(w.Body.Bytes(), &rows) != nil || len(rows) != 1 || len(rows[0]) != 7 || rows[0]["instance_id"] != nil {
					t.Fatal("source DTO must exclude native identity extension", w.Body.String())
				}
			}
		}
	}
}
