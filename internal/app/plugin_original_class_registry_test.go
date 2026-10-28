package app

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"reflect"
	"testing"

	"light-media/internal/plugins/artifacts"
)

func TestOriginalLocalClassRegistrySourceOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-market/class-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		ClassOrder []string           `json:"class_insertion_order"`
		Versions   map[string]*string `json:"installed_class_versions"`
	}
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t)
	ctx := context.Background()
	// Synthetic imported classes, with deliberately different installed and
	// running memberships; no synthetic object is presented as a real worker.
	for _, id := range append(append([]string{}, oracle.ClassOrder...), "Ghost") {
		in := PluginInstallation{InstanceID: id, PluginID: id, Version: map[string]string{"A": "9.7", "B": "2.0", "C": "3.0", "Ghost": "4.0"}[id], ManifestSchemaVersion: 1, Manifest: json.RawMessage(`{}`)}
		if err = a.pluginStore.Install(ctx, in); err != nil {
			t.Fatal(err)
		}
		if id != "Ghost" {
			a.storeOriginalPluginClass(id, originalPluginClass{installation: in})
		}
		if id == "B" {
			if err = a.setOriginalPluginInstalled(ctx, id, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !reflect.DeepEqual(a.pluginOriginalClassOrder, oracle.ClassOrder) {
		t.Fatal("class insertion order changed")
	}
	for id, expected := range oracle.Versions {
		version, present, err := a.originalLocalPluginVersion(ctx, id)
		if err != nil || present != (expected != nil) || expected != nil && version != *expected {
			t.Fatal("version differs from fixed class getter", id, version, present, err)
		}
	}
	a.forgetOriginalPluginClass("B")
	a.storeOriginalPluginClass("B", originalPluginClass{})
	if !reflect.DeepEqual(a.pluginOriginalClassOrder, []string{"A", "C", "B"}) {
		t.Fatal("reimport failed to append class")
	}
}

func TestOriginalHistoryActualStatesAndImportedClassVersion(t *testing.T) {
	a, cookie, source := ownedStandardSDKFixture(t)
	ctx := context.Background()
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedStandardFixtureID+"/clone", `{"suffix":"History","version":"9.7"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	id := ownedStandardFixtureID + "history"
	clone := originalBaseSDKLoaded(t, a, id)
	code, err := artifacts.ReadInstalled(a.pluginPackageRoot(), ownedStandardFixtureID, validatePluginArtifactContract)
	if err != nil {
		t.Fatal(err)
	}
	settings := a.Settings()
	settings.PluginMarkets = []string{code.RepositoryURL}
	if err = a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	priorSource, priorClone := ownedStandardSnapshotRead(t, source), ownedStandardSnapshotRead(t, clone)
	response = callTest(t, a.Handler(), "GET", "/api/plugins/history/"+id, "", cookie, "")
	var history struct{ Data originalPluginHistory }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &history) != nil || history.Data.CurrentVersion != "9.7" {
		t.Fatal("history lost imported clone class version", response.Body.String())
	}
	afterSource, afterClone := ownedStandardSnapshotRead(t, source), ownedStandardSnapshotRead(t, clone)
	if afterSource.Calls["get_state"] != priorSource.Calls["get_state"]+2 || afterClone.Calls["get_state"] != priorClone.Calls["get_state"]+1 || afterSource.Nonce != priorSource.Nonce || afterClone.Nonce != priorClone.Nonce {
		t.Fatal("history failed to ask every actual running object without reinitializing")
	}
	history.Data = originalPluginHistory{}
	response = callTest(t, a.Handler(), "GET", "/api/plugins/releases/"+id+"?repo_url="+url.QueryEscape("local://"+id+"?path=/unused"), "", cookie, "")
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &history) != nil || history.Data.CurrentVersion != "9.7" {
		t.Fatal("release lost imported class version")
	}
	if got := ownedStandardSnapshotRead(t, source); got.Calls["get_state"] != afterSource.Calls["get_state"] {
		t.Fatal("local version lookup triggered state calls")
	}
	if err := a.stopOriginalPlugin(ctx, id); err != nil {
		t.Fatal(err)
	}
	response = callTest(t, a.Handler(), "GET", "/api/plugins/history/"+id, "", cookie, "")
	if response.Code != 404 {
		t.Fatal("installed row without imported class invented history", response.Body.String())
	}
	history.Data = originalPluginHistory{}
	response = callTest(t, a.Handler(), "GET", "/api/plugins/releases/"+id+"?repo_url="+url.QueryEscape("local://"+id+"?path=/unused"), "", cookie, "")
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &history) != nil || history.Data.CurrentVersion != "" {
		t.Fatal("installed row without class invented current version")
	}
	// A failed initializer leaves an imported class and no running object.
	installed, err := a.pluginStore.Installation(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.saveOriginalPluginConfiguration(ctx, installed, json.RawMessage(`{"fail_init":true}`)); err != nil {
		t.Fatal(err)
	}
	a.invalidateOriginalBaseConfig(id)
	if object := a.startOriginalPlugin(ctx, id); object != nil {
		t.Fatal("initializer unexpectedly succeeded")
	}
	if version, present, err := a.originalLocalPluginVersion(ctx, id); err != nil || !present || version != "9.7" {
		t.Fatal("failed initialization lost imported class metadata", version, present, err)
	}
}
