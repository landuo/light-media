package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// The plugin page awaits these three reads in order before it can render, and
// the toolbar/command menu comes from the same startup. Each must answer fast
// with builtin objects present: a slow step is what an empty list looks like.
func TestBuiltinUIPluginChainIsFast(t *testing.T) {
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
	if err := a.initializeOriginalPlugins(ctx); err != nil {
		t.Fatalf("startup: %v", err)
	}
	a.pluginOriginalRunningMu.Lock()
	loaded := len(a.pluginOriginalRunningOrder)
	a.pluginOriginalRunningMu.Unlock()
	t.Logf("loaded=%d", loaded)
	if loaded == 0 {
		t.Fatal("no object loaded: the page would render an empty list")
	}

	cookie := setupTestAdmin(t, a)

	// 1. installed list
	start := time.Now()
	w := callTest(t, a.Handler(), "GET", "/api/plugins?state=installed", "", cookie, "")
	if w.Code != 200 {
		t.Fatalf("installed %d %s", w.Code, w.Body)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("installed read took %v", elapsed)
	}
	var installedEnvelope struct {
		Data []pluginCard `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &installedEnvelope); err != nil {
		t.Fatalf("installed decode: %v", err)
	}
	installed := installedEnvelope.Data
	if len(installed) == 0 {
		t.Fatal("installed list is empty: the page would show nothing")
	}
	t.Logf("installed=%d in %v", len(installed), time.Since(start))

	// 2. market details (must be local)
	start = time.Now()
	w = callTest(t, a.Handler(), "GET", "/api/plugins/market?details=true", "", cookie, "")
	if w.Code != 200 {
		t.Fatalf("market %d %s", w.Code, w.Body)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("market read took %v: it is on the page critical path", elapsed)
	}
	var marketEnvelope struct {
		Data struct {
			Items   []pluginCard `json:"items"`
			Sources []any        `json:"sources"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &marketEnvelope); err != nil {
		t.Fatalf("market decode: %v", err)
	}
	market := marketEnvelope.Data
	if len(market.Items) == 0 {
		t.Fatal("market items empty: the page joins on this")
	}
	t.Logf("market=%d in %v", len(market.Items), time.Since(start))

	// 3. command menu (the sidebar entry the user clicks)
	start = time.Now()
	w = callTest(t, a.Handler(), "GET", "/api/plugins?state=installed&commands=true", "", cookie, "")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("command read took %v", elapsed)
	}
	t.Logf("aux read=%d in %v", w.Code, time.Since(start))
}
