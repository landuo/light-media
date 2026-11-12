package main

import (
	"reflect"
	"slices"
	"testing"
)

func TestSourcePassFixtureKeepsLifecycleAndCapabilities(t *testing.T) {
	old := fixtureSourceStub
	defer func() { fixtureSourceStub = old }()
	fixtureSourceStub = ""
	original := manifest()
	fixtureSourceStub = "true"
	stub := manifest()
	if err := stub.Validate(); err != nil {
		t.Fatal(err)
	}
	if stub.PluginID != original.PluginID || !reflect.DeepEqual(stub.Capabilities, original.Capabilities) {
		t.Fatal("source-pass mode changed identity or authority")
	}
	omitted := []string{"get_module", "get_service", "get_command", "get_dashboard", "get_auth_providers"}
	for _, method := range omitted {
		if slices.Contains(stub.Methods, method) {
			t.Fatal("declared source-pass getter", method)
		}
	}
	for _, method := range original.Methods {
		if !slices.Contains(omitted, method) && !slices.Contains(stub.Methods, method) {
			t.Fatal("unrelated method removed", method)
		}
	}
	for _, method := range []string{"init", "get_state", "stop", "snapshot", "get_render_mode"} {
		if !slices.Contains(stub.Methods, method) {
			t.Fatal("lifecycle/counter missing", method)
		}
	}
}
