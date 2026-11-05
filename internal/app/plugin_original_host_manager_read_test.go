package app

import (
	"encoding/json"
	"reflect"
	"testing"

	"light-media/internal/plugins"
)

func TestOriginalHostManagerPureEnumerationOrderDoesNotExecuteStateActualSDK(t *testing.T) {
	a, cookie, object := ownedHostServicesFixture(t)
	for _, suffix := range []string{"z", "a"} {
		response := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedHostServices/clone", `{"suffix":"`+suffix+`"}`, cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	}
	if _, err := ownedHostServicesCall(t, object, plugins.PluginManagerControlCapability, plugins.PluginManagerRequest{InstanceID: "OwnedHostServicesz", Action: "init", Config: json.RawMessage(`{"enabled":true,"fail_state":true}`)}); err == nil {
		t.Fatal("getter fault not installed")
	}
	for _, view := range []string{"installed", "running", "classes"} {
		raw, err := ownedHostServicesCall(t, object, plugins.PluginManagerReadCapability, plugins.PluginManagerRequest{View: view})
		if err != nil {
			t.Fatal("source enumeration executed getter", view, err)
		}
		var states []plugins.PluginHostState
		if json.Unmarshal(raw, &states) != nil {
			t.Fatal(string(raw))
		}
		ids := []string{}
		for _, state := range states {
			ids = append(ids, state.InstanceID)
			if state.Enabled != nil {
				t.Fatal("unrequested state was fabricated or read", view, state.InstanceID)
			}
		}
		// Fixed get_plugin_ids/get_running_plugin_ids return dict insertion
		// order. UserInstalledPlugins uses its independent persistent list.
		if !reflect.DeepEqual(ids, []string{"OwnedHostServices", "OwnedHostServicesz", "OwnedHostServicesa"}) {
			t.Fatal("source insertion/installation order changed", view, ids)
		}
	}
	if _, err := ownedHostServicesCall(t, object, plugins.PluginManagerReadCapability, plugins.PluginManagerRequest{InstanceID: "OwnedHostServicesz", IncludeState: true}); err == nil {
		t.Fatal("explicit source get_plugin_state swallowed getter exception")
	}
}
