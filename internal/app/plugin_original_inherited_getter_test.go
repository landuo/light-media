package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"testing"

	"light-media/internal/plugins"
)

// Fixed Manager: command/service/module/dashboard discovery checks whether
// the source getter is implemented before reading state. Auth intentionally
// reads state first. Direct dashboard reads still call the Base pass default.
func TestOriginalInheritedGetterGatesActualSDK(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-inherited-getter-gates-20261106/source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Inventory []struct {
			Class      string `json:"class"`
			Interfaces map[string]struct {
				Implemented bool `json:"check_method"`
			} `json:"interfaces"`
		} `json:"inventory"`
	}
	if json.Unmarshal(raw, &oracle) != nil {
		t.Fatal("invalid source inventory")
	}
	index := slices.IndexFunc(oracle.Inventory, func(row struct {
		Class      string `json:"class"`
		Interfaces map[string]struct {
			Implemented bool `json:"check_method"`
		} `json:"interfaces"`
	}) bool {
		return row.Class == "RemoveLink"
	})
	if index < 0 {
		t.Fatal("source RemoveLink inventory absent")
	}
	for _, name := range []string{"get_module", "get_service", "get_command", "get_dashboard", "get_auth_providers"} {
		value, ok := oracle.Inventory[index].Interfaces[name]
		if !ok || value.Implemented {
			t.Fatal("source getter is not pass", name)
		}
	}
	binary := os.Getenv("OWNED_MODULE_SOURCE_STUB_BINARY")
	if binary == "" {
		t.Fatal("OWNED_MODULE_SOURCE_STUB_BINARY must name the real counter SDK fixture with source pass declarations omitted")
	}
	t.Setenv("OWNED_MODULE_NO_DECLARATION_BINARY", binary)
	a, cookie, object := ownedModuleFixture(t, false)
	saveOwnedModule(t, a, cookie, object, `{"enabled":true}`)
	for _, method := range []string{"get_module", "get_service", "get_command", "get_dashboard", "get_auth_providers"} {
		if slices.Contains(object.manifest.Methods, method) {
			t.Fatal("wrong fixture declaration", method)
		}
	}
	ctx := context.Background()
	for _, name := range []string{"command", "service", "module", "dashboard_meta", "dashboard_direct", "auth"} {
		t.Run(name, func(t *testing.T) {
			before := ownedModuleRead(t, object)
			switch name {
			case "command":
				a.initOriginalPluginCommands()
				a.wg.Wait()
			case "service":
				a.updateOriginalPluginJobs(ctx, ownedModuleID)
			case "module":
				if got, err := a.originalPluginModules(ctx, ""); err != nil || len(got) != 0 {
					t.Fatal("source pass module was discovered", got, err)
				}
			case "dashboard_meta":
				if got := a.originalPluginDashboardMetadata(ctx); len(got) != 0 {
					t.Fatal("source pass dashboard was discovered", got)
				}
			case "dashboard_direct":
				response := callTest(t, a.Handler(), http.MethodGet, "/api/plugins/"+ownedModuleID+"/dashboard?key=owned", "", cookie, "")
				var body struct {
					Data  json.RawMessage `json:"data"`
					Error json.RawMessage `json:"error"`
				}
				if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Error) != 0 || string(body.Data) != "null" {
					t.Fatal("source Base dashboard pass is not null", response.Code, response.Body.String())
				}
			case "auth":
				if got, err := a.originalObjectAuthProviders(ctx, object); err != nil || len(got) != 0 {
					t.Fatal("source pass auth getter returned providers", got, err)
				}
			}
			after := ownedModuleRead(t, object)
			wantState := 0
			if name == "auth" {
				wantState = 1
			}
			if got := after.Calls["get_state"] - before.Calls["get_state"]; got != wantState {
				t.Fatalf("source state call order/count: got %d, want %d", got, wantState)
			}
			for _, getter := range []string{"get_module", "get_service", "get_command", "get_dashboard", "get_dashboard_meta", "get_auth_providers"} {
				if after.Calls[getter] != before.Calls[getter] {
					t.Fatal("source pass getter was called", getter)
				}
			}
			if name == "dashboard_direct" && after.Calls["get_render_mode"] != before.Calls["get_render_mode"]+1 {
				t.Fatal("direct dashboard skipped original render-mode call")
			}
		})
	}
}

func TestOriginalOptionalDeclarationsRetainLifecycleAndCapabilityGuards(t *testing.T) {
	binary := os.Getenv("OWNED_MODULE_SOURCE_STUB_BINARY")
	if binary == "" {
		t.Fatal("real source-pass declaration fixture required")
	}
	raw, err := exec.Command(binary, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil {
		t.Fatal("invalid actual manifest")
	}
	if err := validatePluginArtifactContract(manifest, pluginBaseSDKContract); err != nil {
		t.Fatal("optional declarations rejected", err)
	}
	for _, method := range []string{"init", "get_state", "stop"} {
		t.Run(method, func(t *testing.T) {
			candidate := manifest
			candidate.Methods = slices.DeleteFunc(slices.Clone(manifest.Methods), func(name string) bool { return name == method })
			if err := validatePluginArtifactContract(candidate, pluginBaseSDKContract); err == nil {
				t.Fatal("missing required lifecycle method accepted", method)
			}
		})
	}
	manifest.Capabilities = append(slices.Clone(manifest.Capabilities), "owned.forbidden")
	if err := validatePluginArtifactContract(manifest, pluginBaseSDKContract); err == nil {
		t.Fatal("optional getter change granted an unknown capability")
	}
}
