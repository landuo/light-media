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

func TestBuiltinModuleDiscoveryPreservesDynamicLarkBindings(t *testing.T) {
	a := newTestApp(t)
	a.pluginBuiltinDisabled = false
	ctx := context.Background()
	catalog, err := builtinCatalogLoad()
	if err != nil {
		t.Fatal(err)
	}
	m := catalog["LarkMessager"].manifest
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.Install(ctx, PluginInstallation{PluginID: m.PluginID, InstanceID: m.PluginID, Version: m.Version, ManifestSchemaVersion: m.SchemaVersion, Manifest: raw, DesiredEnabled: true}); err != nil {
		t.Fatal(err)
	}
	retainedStoreSaveConfig(t, a, m.PluginID, map[string]any{"enabled": true, "app_id": "test", "app_secret": "test"})
	object, err := a.reloadOriginalPlugin(ctx, m.PluginID)
	if err != nil || object == nil || !object.builtin.Load() {
		t.Fatal("load compiled Lark", err)
	}
	a.wg.Wait()
	used := object.lastUsed.Load()
	for _, method := range []string{"get_file_item", "recognize_media", "obtain_images", "tmdb_episodes", "transfer"} {
		groups, err := a.originalPluginModulesForMethod(ctx, "", method)
		if err != nil || len(groups) != 0 || object.lastUsed.Load() != used {
			t.Fatalf("%s touched the message worker: %v, %v", method, groups, err)
		}
	}
	for _, method := range []string{"", "post_message"} {
		groups, err := a.originalPluginModulesForMethod(ctx, "", method)
		if err != nil || len(groups) != 1 || !originalModuleGroupsHaveMethod(groups, "post_message") {
			t.Fatalf("lost live message binding for %q: %v, %v", method, groups, err)
		}
	}
	retainedStoreSaveConfig(t, a, m.PluginID, map[string]any{"enabled": false})
	if _, err = a.reloadOriginalPlugin(ctx, m.PluginID); err != nil {
		t.Fatal(err)
	}
	groups, err := a.originalPluginModulesForMethod(ctx, "", "post_message")
	if err != nil || len(groups) != 0 {
		t.Fatal("stale enabled message binding", groups, err)
	}
	// A rollback worker or clone may implement a different get_module, even
	// with the same plugin ID and manifest. Its declarations are always live.
	external := &originalPluginInstance{manifest: m, external: true}
	if !external.mayDeclareModule("recognize_media") {
		t.Fatal("external worker was mistaken for the compiled class")
	}
}
