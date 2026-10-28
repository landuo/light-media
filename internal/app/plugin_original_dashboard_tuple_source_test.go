package app

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestOriginalDashboardTupleFixedManagerAndSchema(t *testing.T) {
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
				Value  map[string]json.RawMessage
			}
		}
	}
	if json.Unmarshal(raw, &oracle) != nil {
		t.Fatal("dashboard source oracle")
	}
	for _, c := range oracle.Cases {
		t.Run(c.Name, func(t *testing.T) {
			tuple, err := originalDashboardTuple(c.Input)
			if c.Expected.Status == 500 {
				if e, ok := err.(*APIError); !ok || e.Status != 500 {
					t.Fatal("fixed schema failure did not return500", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.Expected.Value == nil {
				if tuple != nil {
					t.Fatal("None created dashboard")
				}
				return
			}
			raw, err := json.Marshal(originalDashboardResponse("Owned_clone", "Owned custom dashboard twin", "summary", "vuetify", tuple))
			if err != nil {
				t.Fatal(err)
			}
			var actual, expected any
			decode := func(raw []byte, out *any) {
				d := json.NewDecoder(bytes.NewReader(raw))
				d.UseNumber()
				if err := d.Decode(out); err != nil {
					t.Fatal(err)
				}
			}
			expectedRaw, _ := json.Marshal(c.Expected.Value)
			decode(raw, &actual)
			decode(expectedRaw, &expected)
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("complete source DTO mismatch got%s want%s", raw, expectedRaw)
			}

		})
	}
}
func TestOriginalDashboardActualSDKMissingUserAgentIsNull(t *testing.T) {
	a, cookie, object := ownedStandardSDKFixture(t)
	response := callTest(t, a.Handler(), "GET", "/api/plugins/"+ownedStandardFixtureID+"/dashboard?key=summary", "", cookie, "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	after := ownedStandardSnapshotRead(t, object)
	var args map[string]json.RawMessage
	if json.Unmarshal(after.LastPayload["get_dashboard"], &args) != nil || string(args["user_agent"]) != "null" {
		t.Fatal("fixed Header default None must reach actual object", string(after.LastPayload["get_dashboard"]))
	}
}
