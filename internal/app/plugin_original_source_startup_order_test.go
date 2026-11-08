package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"light-media/internal/plugins/artifacts"
)

// Fixed manager._load_selective_plugins traverses the directory before stable
// plugin_order sorting. UserInstalledPlugins defines membership, not tie order.
func TestSourceUninstallPackageTraversalIgnoresMembershipOrder(t *testing.T) {
	var outcomes [][]bool
	var directoryOrders [][]string
	for _, ids := range [][]string{{"PluginUnInstall", "PluginUnInstallowned", "MPServerStatus"}, {"PluginUnInstallowned", "PluginUnInstall", "MPServerStatus"}} {
		a, cookie := sourceUninstallFixture(t)
		ctx := context.Background()
		response := callTest(t, a.Handler(), "POST", "/api/plugins/PluginUnInstall/clone", `{"suffix":"owned","version":"9.4"}`, cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
		a.wg.Wait()
		sourceUninstallSave(t, a, "PluginUnInstall", json.RawMessage(`{"plugin_ids":[]}`))
		sourceUninstallSave(t, a, "PluginUnInstallowned", json.RawMessage(`{"plugin_ids":["PluginUnInstall"],"clear_config":true,"clear_data":true}`))
		directory, err := os.Open(a.pluginPackageRoot())
		if err != nil {
			t.Fatal(err)
		}
		names, err := directory.Readdirnames(-1)
		directory.Close()
		if err != nil {
			t.Fatal(err)
		}
		directoryOrders = append(directoryOrders, names)
		raw, _ := json.Marshal(ids)
		if _, err = a.pluginStore.sourceSetOriginalInstalled(ctx, raw); err != nil {
			t.Fatal(err)
		}
		if err = a.initializeOriginalPlugins(ctx); err != nil {
			t.Fatal(err)
		}
		a.wg.Wait()
		_, class := a.pluginOriginalClasses.Load("PluginUnInstall")
		_, running := a.pluginRunningInstances.Load("PluginUnInstall")
		_, codeErr := os.Stat(filepath.Join(a.pluginPackageRoot(), "PluginUnInstall"))
		outcomes = append(outcomes, []bool{class, running, codeErr == nil})
		if err = a.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(directoryOrders[0], directoryOrders[1]) {
		t.Fatal("test physical package order changed", directoryOrders)
	}
	if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
		t.Fatal("source directory plan changed when only installed membership order changed", outcomes, directoryOrders)
	}
}

// Fixed manager.stop(None):183-187 clears all imported classes, including those
// whose initializer failed before insertion into the running-object map.
func TestSourceUninstallStopAllClearsFailedClasses(t *testing.T) {
	a, _ := sourceUninstallFixture(t)
	ctx := context.Background()
	sourceUninstallSave(t, a, "PluginUnInstall", json.RawMessage(`{"plugin_ids":1}`))
	if err := a.initializeOriginalPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	if _, ok := a.pluginOriginalClasses.Load("PluginUnInstall"); !ok {
		t.Fatal("source failed init must keep imported class")
	}
	if _, ok := a.pluginRunningInstances.Load("PluginUnInstall"); ok {
		t.Fatal("fixture did not fail before running insertion")
	}
	if _, err := a.pluginStore.sourceSetOriginalInstalled(ctx, json.RawMessage(`["MPServerStatus"]`)); err != nil {
		t.Fatal(err)
	}
	if err := a.initializeOriginalPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	if _, ok := a.pluginOriginalClasses.Load("PluginUnInstall"); ok {
		t.Fatal("stop-all retained a failed, now unselected class")
	}
	if _, ok := a.pluginRunningInstances.Load("MPServerStatus"); !ok {
		t.Fatal("selected valid class did not start")
	}
}

// Fixed ObjectUtils.check_method ignores a pass/docstring-only method. The
// transport's declared lifecycle stub is not a source data-page implementation.
func TestSourceUninstallPagePresenceUsesSourceDeclaration(t *testing.T) {
	a, _ := sourceUninstallFixture(t)
	locals, err := a.originalLocalPluginCards(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"PluginUnInstall": false, "MPServerStatus": true}
	for _, card := range locals {
		if expected, ok := want[card.InstanceID]; ok && card.HasPage != expected {
			t.Errorf("source local page declaration %s=%v want %v", card.InstanceID, card.HasPage, expected)
		}
	}
	var source []artifacts.Discovery
	for _, card := range locals {
		if _, ok := want[card.InstanceID]; ok {
			source = append(source, artifacts.Discovery{PluginID: card.InstanceID, CurrentVersion: "9.9", RepositoryURL: "https://example.invalid/owned-metadata", Current: artifacts.Compatibility{Manifest: card.Manifest}})
		}
	}
	markets := a.originalMarketCards(locals, source, map[string]bool{})
	if len(markets) != 2 {
		t.Fatal("owned newer declarations missing", len(markets))
	}
	for _, card := range markets {
		if expected, ok := want[card.InstanceID]; ok && card.HasPage != expected {
			t.Errorf("source market page declaration %s=%v want %v", card.InstanceID, card.HasPage, expected)
		}
	}
	if !slices.ContainsFunc(locals, func(c pluginCard) bool { return c.InstanceID == "PluginUnInstall" }) {
		t.Fatal("actual source class missing")
	}
}

// _load_selective_plugins returns before iterdir for an empty membership list.
// Stopping loaded objects must still clear their classes without reading code.
func TestSourceUninstallEmptyMembershipDoesNotReadPackageDirectory(t *testing.T) {
	a, _ := sourceUninstallFixture(t)
	ctx := context.Background()
	if _, err := a.pluginStore.sourceSetOriginalInstalled(ctx, json.RawMessage(`[]`)); err != nil {
		t.Fatal(err)
	}
	folder := a.pluginPackageRoot()
	if err := os.Chmod(folder, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(folder, 0700)
	if err := a.initializeOriginalPlugins(ctx); err != nil {
		t.Fatal("empty source target list scanned unreadable packages", err)
	}
	a.pluginOriginalRunningMu.Lock()
	classes := slices.Clone(a.pluginOriginalClassOrder)
	running := slices.Clone(a.pluginOriginalRunningOrder)
	a.pluginOriginalRunningMu.Unlock()
	if len(classes) != 0 || len(running) != 0 {
		t.Fatal("empty membership retained class/runtime", classes, running)
	}
}

// Source pathlib.exists happens even for [] targets and propagates EACCES
// from an unsearchable ancestor; it does not require reading the package dir.
func TestSourceUninstallEmptyMembershipStillStatsPackagePath(t *testing.T) {
	a, _ := sourceUninstallFixture(t)
	if err := os.Chmod(a.Dir, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(a.Dir, 0700)
	if _, err := a.originalInstalledPackages(context.Background(), []string{}); !os.IsPermission(err) {
		t.Fatal("empty targets hid source Path.exists permission failure", err)
	}
}
