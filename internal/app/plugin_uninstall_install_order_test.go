package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"light-media/internal/plugins/builtin"
)

func TestPluginUninstallSourceHTTPReinstallAppendsAndSelfFirstStillCompletes(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	install := func(id string) {
		w := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"`+id+`"}`, cookie, "")
		if w.Code != http.StatusOK {
			t.Fatal(w.Code, w.Body.String())
		}
		run, err := a.pluginStore.claimRunForInstance(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		a.executePluginRun(ctx, run)
	}
	install("CategoryEditor")
	install("PluginUnInstall")
	w := callTest(t, a.Handler(), "POST", "/api/plugins/CategoryEditor/uninstall", `{}`, cookie, "")
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	install("CategoryEditor")
	inventory, err := a.pluginStore.uninstallInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, in := range inventory {
		ids = append(ids, in.InstanceID)
	}
	if !reflect.DeepEqual(ids, []string{"PluginUnInstall", "CategoryEditor"}) {
		t.Fatal("source install appends a removed/reinstalled ID; old rowid is not source installation order", ids)
	}
	w = callTest(t, a.Handler(), "PUT", "/api/plugins/PluginUnInstall/config", `{"revision":1,"config":{"plugin_ids":["CategoryEditor","PluginUnInstall"],"clear_config":true,"clear_data":true}}`, cookie, "")
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	run, err := a.pluginStore.claimRunForInstance(ctx, "PluginUnInstall")
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(ctx, run)
	saved, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || saved.State != "succeeded" || !bytes.Contains(saved.Result, []byte(`"removed":["PluginUnInstall","CategoryEditor"]`)) {
		t.Fatal("self-first source sequence did not complete", saved.State, saved.ErrorCode, err)
	}
}

func TestPluginUninstallSourceLocalSequenceSurvivesResetUpgradeAndColdReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	open := true
	t.Cleanup(func() {
		if open {
			_ = a.Close()
		}
	})
	for _, id := range []string{"Alpha", "Beta"} {
		uninstallInstall(t, a, id, builtin.CategoryEditorManifest(), map[string]any{"enabled": false}, false)
	}
	check := func(want []string) {
		t.Helper()
		rows, e := a.pluginStore.uninstallInventory(ctx)
		if e != nil {
			t.Fatal(e)
		}
		ids := []string{}
		for _, in := range rows {
			ids = append(ids, in.InstanceID)
		}
		if !reflect.DeepEqual(ids, want) {
			t.Fatal("local install sequence", ids, want)
		}
	}
	check([]string{"Alpha", "Beta"})
	if err = a.pluginStore.Reset(ctx, "Alpha"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.pluginStore.SaveConfigAndQueue(ctx, "Alpha", 0, map[string]any{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	check([]string{"Alpha", "Beta"})
	manifest, _ := json.Marshal(builtin.CategoryEditorManifest())
	installation := PluginInstallation{InstanceID: "Alpha", PluginID: "CategoryEditor", Version: builtin.CategoryEditorManifest().Version, ManifestSchemaVersion: 1, Manifest: manifest, DesiredEnabled: true}
	if err = a.pluginStore.Install(ctx, installation); err != nil {
		t.Fatal(err)
	}
	check([]string{"Alpha", "Beta"}) // installed upgrade does not append
	if err = a.pluginStore.Uninstall(ctx, "Alpha"); err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.Install(ctx, installation); err != nil {
		t.Fatal(err)
	}
	check([]string{"Beta", "Alpha"})
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	open = false
	a, err = New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	check([]string{"Beta", "Alpha"})
}

func TestPluginUninstallSourceSequenceLegacyBackfillOnceAndMissingRowsAreNotRebased(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	for _, id := range []string{"Alpha", "Beta", "Gamma"} {
		uninstallInstall(t, a, id, builtin.CategoryEditorManifest(), map[string]any{"enabled": false}, false)
	}
	// Model a pre-feature local DB: it never recorded installation sequence.
	if _, err := a.Store.DB.Exec(`DROP TABLE plugin_install_sequence`); err != nil {
		t.Fatal(err)
	}
	p, err := NewPluginStore(ctx, a.Store)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := p.uninstallInventory(ctx)
	if err != nil || len(rows) != 3 || rows[0].InstanceID != "Alpha" || rows[2].InstanceID != "Gamma" {
		t.Fatal("legacy local row-order backfill", err)
	}
	if _, err = a.Store.DB.Exec(`DELETE FROM plugin_install_sequence WHERE instance_id='Beta'`); err != nil {
		t.Fatal(err)
	}
	p, err = NewPluginStore(ctx, a.Store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.uninstallInventory(ctx); pluginErrorCode(err) != "PLUGIN_INVENTORY_ORDER_INVALID" {
		t.Fatal("missing local order row was silently re-created", err)
	}
	if response := callTest(t, a.Handler(), "GET", "/healthz", "", nil, ""); response.Code != http.StatusOK {
		t.Fatal("local order damage stopped unrelated HTTP", response.Code)
	}
}
