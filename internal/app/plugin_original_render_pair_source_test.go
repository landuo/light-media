package app

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestOriginalRenderPairSourceErrorAndGoTypeBoundary(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-navigation/source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		RenderCases []struct {
			Name     string
			Input    json.RawMessage
			Expected struct {
				Rows  json.RawMessage
				Error *string
			}
			StrictGoPair bool `json:"strict_go_pair"`
			Adaptation   *string
		} `json:"render_cases"`
	}
	if json.Unmarshal(raw, &oracle) != nil {
		t.Fatal("source oracle")
	}
	a, cookie, _ := ownedStandardSDKFixture(t)
	for _, c := range oracle.RenderCases {
		t.Run(c.Name, func(t *testing.T) {
			body := `{"enabled":true,"render_mode":` + string(c.Input) + `}`
			r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+ownedStandardFixtureID, body, cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			r = callTest(t, a.Handler(), "GET", "/api/v1/plugin/remotes?token=moviepilot", "", nil, "")
			if c.Expected.Error != nil {
				if r.Code != 500 {
					t.Fatalf("fixed %s must propagate500, got%d %s", *c.Expected.Error, r.Code, r.Body.String())
				}
				return
			}
			if !c.StrictGoPair {
				if c.Adaptation == nil || r.Code != 500 {
					t.Fatal("explicit typed Go SDK boundary missing", r.Code, c.Adaptation)
				}
				return
			}
			if r.Code != 200 {
				t.Fatal(r.Code, r.Body.String())
			}
			var actual, expected []map[string]any
			if json.Unmarshal(r.Body.Bytes(), &actual) != nil || json.Unmarshal(c.Expected.Rows, &expected) != nil {
				t.Fatal("invalid remotes")
			}
			for _, row := range expected {
				row["id"] = ownedStandardFixtureID
				row["name"] = "Owned standard fixture"
				row["url"] = "/plugin/file/ownedstandardfixture/assets/remoteEntry.js"
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatal(actual, expected)
			}
		})
	}
}
