package plugins

import (
	"encoding/json"
	"testing"
)

func TestManifestOriginalOrderAndResetPriority(t *testing.T) {
	var m Manifest
	if err := json.Unmarshal([]byte(`{"plugin_id":"Example"}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.PluginOrder != 0 {
		t.Fatal("missing class attribute must be zero")
	}
	if err := json.Unmarshal([]byte(`{"plugin_order":9999,"reset_handlers":[{"method":"reset"},{"method":"zero","priority":0},{"method":"early","priority":-5}]}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.PluginOrder != 9999 || m.ResetHandlers[0].Order() != 10 || m.ResetHandlers[1].Order() != 0 || m.ResetHandlers[2].Order() != -5 {
		t.Fatalf("original defaults/explicit values: %+v", m)
	}
}
func TestManifestResetDeclarationsValidate(t *testing.T) {
	m := Manifest{SchemaVersion: 1, PluginID: "Example", Name: "Example", Version: "1.0", Runtime: "go-executable", Entry: "example", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"init", "reset", "stop"}, ResetHandlers: []PluginDataResetHandler{{Method: "reset"}}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	m.ResetHandlers = append(m.ResetHandlers, m.ResetHandlers[0])
	if m.Validate() == nil {
		t.Fatal("duplicate handler accepted")
	}
	m.ResetHandlers = []PluginDataResetHandler{{Method: "undeclared"}}
	if m.Validate() == nil {
		t.Fatal("undeclared method accepted")
	}
	m.ResetHandlers = []PluginDataResetHandler{{Method: "../escape"}}
	if m.Validate() == nil {
		t.Fatal("invalid method accepted")
	}
}
