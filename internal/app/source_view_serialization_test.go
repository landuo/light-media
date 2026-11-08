package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSourceViewSerializationActualSDKHTTP(t *testing.T) {
	dir := os.Getenv("SOURCE_VIEW_EVIDENCE_DIR")
	if dir == "" {
		t.Fatal("actual original source evidence dir required")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []map[string]json.RawMessage
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(dir, "source.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Rows []struct {
			ID    string `json:"id"`
			Paths map[string]struct {
				Status int    `json:"status"`
				Body   string `json:"body_b64"`
			} `json:"paths"`
		} `json:"rows"`
	}
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Rows) != len(cases) {
		t.Fatal("source/case count mismatch")
	}
	a, cookie, object := originalExternalHTTPBoundaryFixture(t, "SOURCE_VIEW_FIXTURE_BINARY")
	var initial struct {
		PID int `json:"pid"`
	}
	if err = object.readSession(context.Background(), "snapshot", &initial); err != nil || initial.PID <= 0 {
		t.Fatal(initial, err)
	}
	for i, c := range cases {
		var id string
		_ = json.Unmarshal(c["id"], &id)
		if id != source.Rows[i].ID {
			t.Fatal("source ordering mismatch")
		}
		t.Run(id, func(t *testing.T) {
			// Seed through the actual encrypted production store, including raw
			// non-dictionary values that public dict-only save cannot create.
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(context.Background(), object.installation, c["stored"]); err != nil {
				t.Fatal(err)
			}
			a.invalidateOriginalBaseConfig(object.installation.InstanceID)
			payload, _ := json.Marshal(c)
			if _, err := object.callOriginalMethod(context.Background(), "set_case", payload); err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"page", "form"} {
				t.Run(kind, func(t *testing.T) {
					response := callTest(t, a.Handler(), "GET", "/api/plugins/"+object.installation.InstanceID+"/"+kind, "", cookie, "")
					want := source.Rows[i].Paths[kind]
					if response.Code != want.Status {
						t.Fatalf("source status %d, actual SDK+App status %d body %s", want.Status, response.Code, response.Body.String())
					}
					if want.Status == 200 {
						var envelope struct {
							Data json.RawMessage `json:"data"`
						}
						if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
							t.Fatal(err)
						}
						bytes, err := base64.StdEncoding.DecodeString(want.Body)
						if err != nil {
							t.Fatal(err)
						}
						var got, expected any
						if json.Unmarshal(envelope.Data, &got) != nil || json.Unmarshal(bytes, &expected) != nil || !reflect.DeepEqual(got, expected) {
							t.Fatalf("source=%s actual=%s", bytes, envelope.Data)
						}
					}
				})
			}
		})
	}
	var final struct {
		PID int `json:"pid"`
	}
	if err = object.readSession(context.Background(), "snapshot", &final); err != nil || final.PID != initial.PID {
		t.Fatalf("same worker must survive HTTP serializer failure: %v %v", final, err)
	}
}
