package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins/artifacts"
)

func TestOriginalMarketCatalogExactCloneUpdatePreservesSourceAndRecreatesAfterUninstall(t *testing.T) {
	a, cookie, sourceObject := originalArtifactCloneFixture(t)
	ctx := context.Background()
	const source = "OwnedModuleFixture"
	const clone = "OwnedModuleFixtureexact"
	for _, step := range []struct{ method, path, body string }{
		{"PUT", "/api/plugins/" + source, `{"enabled":false,"marker":"source"}`},
		{"POST", "/api/plugins/" + source + "/clone", `{"suffix":"Exact","version":"9.7","name":"Owned custom clone","description":"custom description","icon":"https://example.invalid/custom.svg"}`},
		{"PUT", "/api/plugins/" + clone, `{"enabled":false,"marker":"clone"}`},
	} {
		r := callTest(t, a.Handler(), step.method, step.path, step.body, cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	}
	a.wg.Wait()
	sourceCode, err := artifacts.ReadInstalled(a.pluginPackageRoot(), source, validatePluginArtifactContract)
	if err != nil {
		t.Fatal(err)
	}
	configBefore, err := a.pluginStore.Config(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.originalBaseSaveData(ctx, clone, "history", json.RawMessage(`{"kept":true}`)); err != nil {
		t.Fatal(err)
	}
	sourceBefore := originalArtifactSnapshot(t, sourceObject)
	oldClone, _ := a.pluginRunningInstances.Load(clone)
	// A source package must fail in preflight even with force, without removing
	// the clone's independent old code or replacing either running process.
	raw, _ := json.Marshal(map[string]any{"plugin_id": clone, "repo_url": sourceCode.RepositoryURL, "force": true})
	r := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(raw), cookie, "")
	var rejected struct {
		Data struct {
			Success bool `json:"success"`
		} `json:"data"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &rejected) != nil || rejected.Data.Success {
		t.Fatal("source package accepted for clone", r.Body.String())
	}
	copyCode, err := artifacts.ReadInstalled(a.pluginPackageRoot(), clone, validatePluginArtifactContract)
	if err != nil || copyCode.SHA256 != sourceCode.SHA256 {
		t.Fatal("wrong-ID preflight damaged clone", err)
	}
	index, payload := originalCatalogFixture(t, "OWNED_CATALOG_CLONE_BINARY")
	if index.PluginID != clone || index.CurrentVersion != "2.0" {
		t.Fatal("exact-ID clone fixture must be version 2.0")
	}
	var payloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/index" {
			json.NewEncoder(w).Encode(artifacts.RepositoryIndex{SchemaVersion: 1, Plugins: []artifacts.Index{index}})
			return
		}
		if req.URL.Path == "/binary" {
			payloads.Add(1)
			w.Write(payload)
			return
		}
		http.NotFound(w, req)
	}))
	defer server.Close()
	index.Releases[0].Artifacts[0].URL = server.URL + "/binary"
	index.History = map[string]string{"2.0": "exact clone release"}
	settings := a.Settings()
	settings.PluginMarkets = []string{server.URL + "/index"}
	if err = a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	r = callTest(t, a.Handler(), "GET", "/api/plugins/history/"+clone, "", cookie, "")
	var history struct {
		Data originalPluginHistory `json:"data"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &history) != nil || history.Data.LatestVersion != "2.0" || history.Data.CurrentVersion != "9.7" || history.Data.PluginID != clone || history.Data.History["2.0"] != "exact clone release" {
		t.Fatal("exact clone history missing", r.Body.String())
	}
	cards, err := a.originalLocalPluginCards(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, card := range cards {
		if card.InstanceID == clone {
			found = !card.HasUpdate && card.DisplayVersion == "9.7" && card.RepositoryURL == "" && card.SourceInstanceID == source
		}
	}
	if !found || payloads.Load() != 0 {
		t.Fatal("clone authored version was ignored or discovery executed code")
	}
	if !a.originalPluginCodeMeetsVersion(clone, "2.0") || a.originalPluginCodeMeetsVersion(clone, "10.0") {
		t.Fatal("sync compared physical source version instead of the clone class version")
	}
	originalCatalogInstall(t, a, cookie, clone, server.URL+"/index", true)
	value, ok := a.pluginRunningInstances.Load(clone)
	if !ok || value == oldClone {
		t.Fatal("clone process not replaced")
	}
	cloneState := originalArtifactSnapshot(t, value.(*originalPluginInstance))
	if cloneState["version"] != "2.0" || cloneState["config"].(map[string]any)["marker"] != "clone" {
		t.Fatal("clone did not use target artifact with retained config", cloneState)
	}
	in, err := a.pluginStore.Installation(ctx, clone)
	if err != nil || in.PluginID != clone {
		t.Fatal("exact clone identity was not bound", err)
	}
	metadata, err := a.pluginStore.instanceMetadata(ctx, a.Store.DB, clone)
	if err != nil || metadata.SourceInstanceID != source || metadata.DisplayVersion != "2.0" || metadata.Name != index.Releases[0].Manifest.Name || metadata.Description != index.Releases[0].Manifest.Description || metadata.Icon != index.Releases[0].Manifest.Icon {
		t.Fatal("authenticated clone marker lost", err)
	}
	r = callTest(t, a.Handler(), "GET", "/api/plugins/history/"+clone, "", cookie, "")
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &history) != nil || history.Data.CurrentVersion != "2.0" {
		t.Fatal("updated clone still exposes old authored version", r.Body.String())
	}
	cards, err = a.originalLocalPluginCards(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, card := range cards {
		if card.InstanceID == clone && (card.HasUpdate || card.DisplayVersion != "2.0") {
			t.Fatal("updated clone card still exposes old class attributes", card)
		}
	}
	configAfter, err := a.pluginStore.Config(ctx, clone)
	if err != nil || !bytes.Equal(configBefore.Value, configAfter.Value) || configBefore.Revision != configAfter.Revision {
		t.Fatal("exact update rewrote config", err)
	}
	if data, err := a.pluginStore.Data(ctx, clone, "history"); err != nil || string(data.Value) != `{"kept":true}` {
		t.Fatal("exact update cleared data", err)
	}
	sourceCurrent, _ := a.pluginRunningInstances.Load(source)
	if sourceCurrent != sourceObject || originalArtifactSnapshot(t, sourceObject)["init_count"] != sourceBefore["init_count"] {
		t.Fatal("exact clone update restarted source")
	}
	afterCode, err := artifacts.ReadInstalled(a.pluginPackageRoot(), source, validatePluginArtifactContract)
	if err != nil || afterCode.SHA256 != sourceCode.SHA256 {
		t.Fatal("clone update changed source bytes", err)
	}
	if present, err := a.originalCodeAliasPresent(ctx, in); err != nil || !present {
		t.Fatal("updated clone code alias not valid", err)
	}
	r = callTest(t, a.Handler(), "DELETE", "/api/plugins/"+clone, "", cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	if _, err = os.Stat(filepath.Join(a.pluginPackageRoot(), clone)); !os.IsNotExist(err) {
		t.Fatal("exact clone uninstall retained code", err)
	}
	if _, err = a.pluginStore.Config(ctx, clone); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("exact clone uninstall retained config", err)
	}
	if _, err = a.pluginStore.Data(ctx, clone, "history"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("exact clone uninstall retained data", err)
	}
	retained, err := a.pluginStore.Installation(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	if present, err := a.originalCodeAliasPresent(ctx, retained); err != nil || present {
		t.Fatal("deleted exact clone alias treated as primary", err)
	}
	r = callTest(t, a.Handler(), "POST", "/api/plugins/"+source+"/clone", `{"suffix":"Exact"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	rebuilt, ok := a.pluginRunningInstances.Load(clone)
	if !ok {
		t.Fatal("same-name clone did not restart")
	}
	state := originalArtifactSnapshot(t, rebuilt.(*originalPluginInstance))
	if state["version"] != "1.0" || state["config"].(map[string]any)["marker"] != "source" {
		t.Fatal("recreated clone did not copy current source", state)
	}
	if rebound, err := a.pluginStore.Installation(ctx, clone); err != nil || rebound.PluginID != source {
		t.Fatal("same-name clone did not rebind source identity", err)
	}
}
