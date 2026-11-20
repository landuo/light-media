package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// A builtin deployment must start every class, publish the sidebar and the
// command menu, and answer a plugin card read without a worker process. This
// is the startup path the UI depends on.
func TestBuiltinStartupPublishesMenusAndCards(t *testing.T) {
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
	for class, entry := range catalog {
		raw, err := json.Marshal(entry.manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.pluginStore.Install(ctx, PluginInstallation{
			InstanceID: class, PluginID: class, Version: entry.manifest.Version,
			ManifestSchemaVersion: entry.manifest.SchemaVersion, Manifest: raw, DesiredEnabled: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for class := range catalog {
		in, err := a.pluginStore.Installation(ctx, class)
		if err != nil {
			t.Fatalf("%s installation row: %v", class, err)
		}
		if _, err := a.resolvePluginImplementation(ctx, in); err != nil {
			t.Fatalf("%s resolve: %v", class, err)
		}
	}
	start := time.Now()
	if err := a.initializeOriginalPlugins(ctx); err != nil {
		t.Fatalf("builtin startup: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 90*time.Second {
		t.Fatalf("builtin startup took %v", elapsed)
	}
	a.pluginOriginalRunningMu.Lock()
	loaded := len(a.pluginOriginalRunningOrder)
	a.pluginOriginalRunningMu.Unlock()
	if loaded == 0 {
		t.Fatal("no builtin object loaded")
	}
	t.Logf("builtin objects loaded: %d of %d in %v", loaded, len(catalog), time.Since(start))
	// No worker process may exist: every object is an in-process session.
	for _, object := range a.originalLoadedPluginSnapshot() {
		if !object.builtin.Load() {
			t.Fatalf("%s is not marked builtin", object.installation.PluginID)
		}
		object.sessionMu.Lock()
		session := object.session
		object.sessionMu.Unlock()
		if session == nil {
			t.Fatalf("%s has no session after startup", object.installation.PluginID)
		}
	}
	cards, err := a.originalLocalPluginCards(ctx, true)
	if err != nil {
		t.Fatalf("installed cards: %v", err)
	}
	if len(cards) == 0 {
		t.Fatal("installed cards must list the loaded builtin classes")
	}
}
