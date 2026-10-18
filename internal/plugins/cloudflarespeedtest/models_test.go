package cloudflarespeedtest

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestSourceFormCommandsAndAPI(t *testing.T) {
	data, err := os.ReadFile("testdata/models.json")
	if err != nil {
		t.Fatal(err)
	}
	var source map[string]any
	if err = json.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	var formValue any
	if err = json.Unmarshal(Form(), &formValue); err != nil || !reflect.DeepEqual(formValue, source["form"]) {
		t.Fatal("form differs from actual source get_form", err)
	}
	commands, _ := json.Marshal(Commands())
	var commandValue any
	_ = json.Unmarshal(commands, &commandValue)
	if !reflect.DeepEqual(commandValue, source["commands"]) {
		t.Fatal("source command differs", commandValue)
	}
	a := APIs()[0]
	api := map[string]any{"path": a.Path, "endpoint": a.Endpoint, "methods": []any{a.Methods[0]}, "summary": a.Summary, "description": a.Description}
	if !reflect.DeepEqual(api, source["api"]) {
		t.Fatal("source API differs", api, source["api"])
	}
	if a.Auth != "apikey" || a.CredentialQuery != "apikey" || source["page"] != nil || Manifest()["version"] != Version {
		t.Fatal("source declaration mismatch")
	}
	copyForm := Form()
	copyForm[0] = 'x'
	if !json.Valid(Form()) {
		t.Fatal("form exposed mutable embedding")
	}
}
