package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins/artifacts"
)

func originalCatalogFixture(t *testing.T, env string) (artifacts.Index, []byte) {
	t.Helper()
	index, payload := originalMarketOwnedIndex(t, env)
	index.Releases[0].Artifacts[0].HostContract = pluginPureSDKContract
	return index, payload
}

func originalCatalogInstall(t *testing.T, a *App, cookie *http.Cookie, id, repository string, force bool) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"plugin_id": id, "repo_url": repository, "force": force})
	r := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(raw), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	value, ok := a.pluginRunningInstances.Load(id)
	if !ok || !value.(*originalPluginInstance).external {
		t.Fatal("actual catalog worker did not load", id)
	}
}

func TestOriginalMarketCatalogActualMultipleInstallUpdateAndIsolation(t *testing.T) {
	first, payload1 := originalCatalogFixture(t, "OWNED_MODULE_FIXTURE_BINARY")
	next, payload2 := originalCatalogFixture(t, "OWNED_MODULE_FIXTURE_BINARY_V2")
	second, payloadSecond := originalCatalogFixture(t, "OWNED_CATALOG_SECOND_BINARY")
	if first.PluginID == second.PluginID || first.PluginID != next.PluginID || first.CurrentVersion != "1.0" || next.CurrentVersion != "2.0" {
		t.Fatal("compile three distinct required catalog fixtures")
	}
	var catalog atomic.Value
	var indexCalls, payloadCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/catalog":
			indexCalls.Add(1)
			w.Write(catalog.Load().([]byte))
		case "/first":
			payloadCalls.Add(1)
			w.Write(payload1)
		case "/next":
			payloadCalls.Add(1)
			w.Write(payload2)
		case "/second":
			payloadCalls.Add(1)
			w.Write(payloadSecond)
		default:
			t.Error("unexpected repository request", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	for index, path := range map[*artifacts.Index]string{&first: "first", &next: "next", &second: "second"} {
		index.Releases[0].Artifacts[0].URL = server.URL + "/" + path
	}
	publish := func(indexes ...artifacts.Index) {
		raw, _ := json.Marshal(artifacts.RepositoryIndex{SchemaVersion: 1, Plugins: indexes})
		catalog.Store(raw)
	}
	publish(first, second)
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	t.Cleanup(a.closeOriginalSessions)
	settings := a.Settings()
	settings.PluginMarkets = []string{server.URL + "/catalog"}
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	readMarket := func() []pluginCard {
		t.Helper()
		r := callTest(t, a.Handler(), "GET", "/api/plugins/market?details=true&force=true", "", cookie, "")
		var body struct {
			Data struct {
				Items   []pluginCard                 `json:"items"`
				Sources []originalMarketSourceStatus `json:"sources"`
			} `json:"data"`
		}
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &body) != nil || len(body.Data.Sources) != 1 || !body.Data.Sources[0].Success {
			t.Fatal(r.Body.String())
		}
		return body.Data.Items
	}
	items := readMarket()
	found := 0
	for _, card := range items {
		if card.InstanceID == first.PluginID || card.InstanceID == second.PluginID {
			if !card.Available || card.RepositoryURL != server.URL+"/catalog" {
				t.Fatal("wrong catalog source", card)
			}
			found++
		}
	}
	if found != 2 || indexCalls.Load() != 1 || payloadCalls.Load() != 0 {
		t.Fatal("one source did not list two inert candidates", found, indexCalls.Load(), payloadCalls.Load())
	}
	for _, id := range []string{first.PluginID, second.PluginID} {
		originalCatalogInstall(t, a, cookie, id, server.URL+"/catalog", false)
		raw, _ := json.Marshal(map[string]any{"enabled": false, "marker": id})
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, string(raw), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	}
	a.wg.Wait()
	beforeSecond, _ := a.pluginRunningInstances.Load(second.PluginID)
	secondState := originalArtifactSnapshot(t, beforeSecond.(*originalPluginInstance))
	firstBefore, _ := a.pluginRunningInstances.Load(first.PluginID)
	next.History = map[string]string{"2.0": "catalog update"}
	publish(next, second)
	items = readMarket()
	for _, card := range items {
		if card.InstanceID == first.PluginID && (!card.HasUpdate || card.RepositoryVersion != "2.0") {
			t.Fatal("catalog update not discovered", card)
		}
	}
	originalCatalogInstall(t, a, cookie, first.PluginID, server.URL+"/catalog", true)
	current, _ := a.pluginRunningInstances.Load(first.PluginID)
	if current == firstBefore || originalArtifactSnapshot(t, current.(*originalPluginInstance))["version"] != "2.0" {
		t.Fatal("first plugin was not actually replaced")
	}
	sibling, _ := a.pluginRunningInstances.Load(second.PluginID)
	if sibling != beforeSecond || originalArtifactSnapshot(t, sibling.(*originalPluginInstance))["init_count"] != secondState["init_count"] {
		t.Fatal("updating first restarted sibling")
	}
	for id, version := range map[string]string{first.PluginID: "2.0", second.PluginID: "1.0"} {
		code, err := artifacts.ReadInstalled(a.pluginPackageRoot(), id, validatePluginArtifactContract)
		if err != nil || code.Manifest.PluginID != id || code.Manifest.Version != version || code.RepositoryURL != server.URL+"/catalog" {
			t.Fatal("catalog provenance or version mismatch", id, err)
		}
		r := callTest(t, a.Handler(), "GET", "/api/plugins/history/"+id+"?repo_url="+url.QueryEscape(server.URL+"/catalog"), "", cookie, "")
		var history struct {
			Data originalPluginHistory `json:"data"`
		}
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &history) != nil || history.Data.PluginID != id || history.Data.LatestVersion != version {
			t.Fatal("history selected sibling", r.Body.String())
		}
	}
	// An incompatible latest entry remains a disabled update while the actual
	// previously installed process and settings stay available.
	broken := next
	broken.CurrentVersion = "3.0"
	broken.Releases = append([]artifacts.Release(nil), next.Releases...)
	broken.Releases[0].Version = "3.0"
	broken.Releases[0].Manifest.Version = "3.0"
	broken.Releases[0].Artifacts = []artifacts.Artifact{next.Releases[0].Artifacts[0]}
	broken.Releases[0].Artifacts[0].Platform = "unsupported-arch"
	publish(broken, second)
	items = readMarket()
	blocked := false
	for _, card := range items {
		if card.InstanceID == first.PluginID {
			blocked = !card.Available && card.UpdateBlockedReason != ""
		}
	}
	if !blocked {
		t.Fatal("incompatible update not shown as blocked")
	}
	r := callTest(t, a.Handler(), "GET", "/api/plugins?state=installed", "", cookie, "")
	var installed struct {
		Data []pluginCard `json:"data"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &installed) != nil {
		t.Fatal(r.Body.String())
	}
	for _, card := range installed.Data {
		if card.InstanceID == first.PluginID && (!card.Available || card.UpdateBlockedReason != "") {
			t.Fatal("existing compatible process disabled", card)
		}
	}
}

func TestOriginalMarketCatalogLocalWatcherOnlyReloadsChangedPlugin(t *testing.T) {
	first, payload1 := originalCatalogFixture(t, "OWNED_MODULE_FIXTURE_BINARY")
	next, payload2 := originalCatalogFixture(t, "OWNED_MODULE_FIXTURE_BINARY_V2")
	second, payloadSecond := originalCatalogFixture(t, "OWNED_CATALOG_SECOND_BINARY")
	root := t.TempDir()
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	t.Cleanup(a.closeOriginalSessions)
	for index, path := range map[*artifacts.Index]string{&first: "first", &next: "next", &second: "second"} {
		index.Releases[0].Artifacts[0].URL = path
	}
	for path, payload := range map[string][]byte{"first": payload1, "next": payload2, "second": payloadSecond} {
		if err := os.WriteFile(filepath.Join(root, path), payload, 0600); err != nil {
			t.Fatal(err)
		}
	}
	publish := func(indexes ...artifacts.Index) {
		t.Helper()
		raw, _ := json.Marshal(artifacts.RepositoryIndex{SchemaVersion: 1, Plugins: indexes})
		if err := os.WriteFile(filepath.Join(root, artifacts.IndexName), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	publish(first, second)
	settings := a.Settings()
	settings.PluginMarkets = nil
	settings.PluginLocalRepoPaths = []string{root}
	settings.PluginAutoReload = true
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first.PluginID, second.PluginID} {
		repository, _ := artifacts.LocalRepositoryURL(id, root)
		originalCatalogInstall(t, a, cookie, id, repository, false)
	}
	state := &originalLocalWatchState{}
	if failures := a.scanOriginalLocalRepositories(context.Background(), state); len(failures) != 0 {
		t.Fatal(failures)
	}
	beforeFirst, _ := a.pluginRunningInstances.Load(first.PluginID)
	beforeSecond, _ := a.pluginRunningInstances.Load(second.PluginID)
	// Change the first plugin and also reorder the catalog, leaving the second
	// declaration and artifact bytes untouched.
	publish(second, next)
	if failures := a.scanOriginalLocalRepositories(context.Background(), state); len(failures) != 0 {
		t.Fatal(failures)
	}
	a.wg.Wait()
	afterFirst, _ := a.pluginRunningInstances.Load(first.PluginID)
	afterSecond, _ := a.pluginRunningInstances.Load(second.PluginID)
	if beforeFirst == afterFirst || beforeSecond != afterSecond || originalArtifactSnapshot(t, afterFirst.(*originalPluginInstance))["version"] != "2.0" {
		t.Fatal("local catalog change crossed plugin identity")
	}
}

func TestOriginalMarketCatalogAcrossSourcesKeepsVersionAndConfiguredOrder(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/first" && r.URL.Path != "/second" {
			t.Error("metadata discovery fetched executable")
			http.NotFound(w, r)
			return
		}
		a := guideMarketIndex("CatalogShared", server.URL+"/never-execute")
		b := guideMarketIndex("CatalogEqual", server.URL+"/never-execute")
		if r.URL.Path == "/second" {
			a.CurrentVersion = "3.0"
			a.Releases[0].Version = "3.0"
			a.Releases[0].Manifest.Version = "3.0"
		}
		json.NewEncoder(w).Encode(artifacts.RepositoryIndex{SchemaVersion: 1, Plugins: []artifacts.Index{a, b}})
	}))
	defer server.Close()
	app := newTestApp(t)
	for _, paths := range [][]string{{"/first", "/second"}, {"/second", "/first"}} {
		settings := app.Settings()
		settings.PluginMarkets = []string{server.URL + paths[0], server.URL + paths[1]}
		if err := app.saveSettings(context.Background(), settings); err != nil {
			t.Fatal(err)
		}
		candidates, statuses := app.originalMarketDeclarations(context.Background(), false)
		if len(candidates) != 2 || len(statuses) != 2 {
			t.Fatal(candidates, statuses)
		}
		for _, candidate := range candidates {
			want := server.URL + paths[0]
			if candidate.PluginID == "CatalogShared" {
				want = server.URL + "/second"
			}
			if candidate.RepositoryURL != want {
				t.Fatalf("wrong winner after source ordering %v: %+v", paths, candidate)
			}
		}
	}
}
