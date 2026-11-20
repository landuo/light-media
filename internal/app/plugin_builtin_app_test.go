package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"light-media/internal/plugins"
)

// The compiled-in path must work through the real App resolver, not only
// through NewInProcess in isolation: a matching installation row resolves to
// a builtin implementation, the session starts without a process, and the
// plugin answers its lifecycle methods.
func TestBuiltinPluginResolvesThroughAppResolver(t *testing.T) {
	a, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	ctx := context.Background()

	catalog, err := builtinCatalogLoad()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := catalog["MergeSiteSwitch"]
	if !ok {
		t.Fatal("MergeSiteSwitch must be compiled in")
	}
	// A row matching the catalog identity is what startup adoption produces.
	if err := a.pluginStore.Install(ctx, PluginInstallation{
		InstanceID: "MergeSiteSwitch", PluginID: "MergeSiteSwitch", Version: entry.manifest.Version,
		ManifestSchemaVersion: entry.manifest.SchemaVersion, Manifest: mustBuiltinJSON(t, entry.manifest),
		DesiredEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	in, err := a.pluginStore.Installation(ctx, "MergeSiteSwitch")
	if err != nil {
		t.Fatal(err)
	}
	implementation, err := a.resolvePluginImplementation(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !implementation.Builtin || implementation.newInProcess == nil {
		t.Fatalf("a catalog class must resolve as builtin: %+v", implementation)
	}
	if implementation.Command.Path != "" {
		t.Fatalf("a builtin carries no worker command: %q", implementation.Command.Path)
	}
	binding := plugins.SessionBinding{Identity: plugins.Identity{PluginID: in.PluginID, InstanceID: in.InstanceID, Version: in.Version}, Generation: in.Generation}
	session, err := plugins.NewInProcess(binding, plugins.InProcessOptions{Manifest: implementation.Manifest, Concurrent: implementation.builtinConcurrent}, implementation.newInProcess())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Call(ctx, "init", json.RawMessage(`{}`), stubHost); err != nil {
		t.Fatalf("builtin init through the resolver: %v", err)
	}
	if _, err := session.Call(ctx, "get_state", json.RawMessage(`{}`), stubHost); err != nil {
		t.Fatalf("builtin get_state: %v", err)
	}
	if err := session.Stop(ctx); err != nil {
		t.Fatalf("builtin stop: %v", err)
	}
}

// An installation row whose version drifted from the catalog must NOT resolve
// as builtin: version adoption at startup is what closes that gap, and a
// half-adopted row must fall back instead of running a mismatched manifest.
func TestBuiltinPluginRejectsVersionDriftBeforeAdoption(t *testing.T) {
	a, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	ctx := context.Background()
	if err := a.pluginStore.Install(ctx, PluginInstallation{
		InstanceID: "MergeSiteSwitch", PluginID: "MergeSiteSwitch", Version: "0.0.1",
		ManifestSchemaVersion: 1, Manifest: json.RawMessage(`{}`), DesiredEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	in, err := a.pluginStore.Installation(ctx, "MergeSiteSwitch")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.resolveBuiltinImplementation(ctx, in, PluginInstanceMetadata{}); ok {
		t.Fatal("a drifted version must not resolve as builtin")
	}
}

func mustBuiltinJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A builtin deployment offers no per-plugin acquisition: install, uninstall
// and market writes are refused with an explicit reason, while reads stay
// available so the UI can still present what ships in the build.
func TestPluginAcquisitionClosedForBuiltinDeployment(t *testing.T) {
	a, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if !a.pluginAcquisitionClosed() {
		t.Fatal("a default (builtin) deployment must close acquisition")
	}
	a.pluginBuiltinDisabled = true
	if a.pluginAcquisitionClosed() {
		t.Fatal("the rollback switch must reopen acquisition")
	}
	t.Setenv("LIGHT_MEDIA_PLUGIN_BUILTIN", "off")
	a.pluginBuiltinDisabled = false
	if a.pluginAcquisitionClosed() {
		t.Fatal("the environment switch must reopen acquisition")
	}
}

// The plugin list and market reads are on the page's critical path: the UI
// awaits both before it can render. In a builtin deployment they must answer
// from the compiled catalog without touching the network or a plugin worker,
// and they must cover every class in the build.
func TestBuiltinPluginListAndMarketAreLocalAndComplete(t *testing.T) {
	a, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	ctx := context.Background()
	catalog, err := builtinCatalogLoad()
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	raw, err := a.builtinMarketResponse(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("a local catalog answer must not take %v", elapsed)
	}
	payload, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("details response shape: %T", raw)
	}
	items, ok := payload["items"].([]pluginCard)
	if !ok {
		t.Fatalf("items shape: %T", payload["items"])
	}
	if len(items) != len(catalog) {
		t.Fatalf("market must list every builtin class: got %d want %d", len(items), len(catalog))
	}
	for _, card := range items {
		if !card.Available {
			t.Fatalf("%s must be available: builtin classes ship in this build", card.InstanceID)
		}
		if card.HasUpdate {
			t.Fatalf("%s must never advertise an update: the build owns its version", card.InstanceID)
		}
	}
	sources, ok := payload["sources"].([]originalMarketSourceStatus)
	if !ok || len(sources) == 0 {
		t.Fatalf("sources shape: %T", payload["sources"])
	}
	// The market read is closed for writes in this deployment.
	if !a.pluginAcquisitionClosed() {
		t.Fatal("acquisition must stay closed for a builtin deployment")
	}
}
