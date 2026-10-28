package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"light-media/internal/plugins/artifacts"
)

func TestOriginalLocalRepositoryActualInstallWatchCloneAndFailure(t *testing.T) {
	a := newTestApp(t)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	v1, bytes1 := originalMarketOwnedIndex(t, "OWNED_STANDARD_FIXTURE_BINARY")
	v2, bytes2 := originalMarketOwnedIndex(t, "OWNED_STANDARD_FIXTURE_V2_BINARY")
	root := t.TempDir()
	repository, err := artifacts.LocalRepositoryURL(v1.PluginID, root)
	if err != nil {
		t.Fatal(err)
	}
	publish := func(index artifacts.Index, payload []byte) {
		t.Helper()
		index.Releases[0].Artifacts[0].URL = "worker"
		raw, err := json.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(root, "worker"), payload, 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(root, artifacts.IndexName), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	publish(v1, bytes1)
	install := func(wantSuccess bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"plugin_id": v1.PluginID, "repo_url": repository, "release_version": "999.0", "force": true})
		response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
		var result struct {
			Data struct {
				Success bool `json:"success"`
			} `json:"data"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Data.Success != wantSuccess {
			t.Fatalf("install outcome %d %s", response.Code, response.Body.String())
		}
	}
	install(false)
	configure := func(roots []string, reload bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"plugin_markets": []string{}, "plugin_local_repo_paths": roots, "plugin_auto_reload": reload})
		response := callTest(t, a.Handler(), "PUT", "/api/plugins/markets", string(body), cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	}
	configure([]string{root, root}, false)
	if !reflect.DeepEqual(a.Settings().PluginLocalRepoPaths, []string{root, root}) || a.Settings().PluginAutoReload {
		t.Fatal("local source controls did not retain ordered duplicates/default off")
	}
	response := callTest(t, a.Handler(), "GET", "/api/plugins/market?details=true", "", cookie, "")
	var market struct {
		Data struct {
			Items   []pluginCard                 `json:"items"`
			Sources []originalMarketSourceStatus `json:"sources"`
		} `json:"data"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &market) != nil {
		t.Fatal(response.Body.String())
	}
	var found bool
	for _, item := range market.Data.Items {
		if item.InstanceID == v1.PluginID && item.RepositoryURL == repository && item.Available && !item.Installed {
			found = true
		}
	}
	if !found || len(market.Data.Sources) != 2 || !market.Data.Sources[0].Success || market.Data.Sources[0].Cached {
		t.Fatal("local discovery or feedback missing", market)
	}
	install(true)
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/"+v1.PluginID, `{"enabled":true,"key":"local-owned-value"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	response = callTest(t, a.Handler(), "POST", "/api/plugins/"+v1.PluginID+"/clone", `{"suffix":"local"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	cloneID := v1.PluginID + "local"
	cloneObject, ok := a.pluginRunningInstances.Load(cloneID)
	if !ok {
		t.Fatal("actual local clone missing")
	}
	checkVersion := func(id, version string) *originalPluginInstance {
		t.Helper()
		code, err := artifacts.ReadInstalled(a.pluginPackageRoot(), id, validatePluginArtifactContract)
		if err != nil || code.Manifest.Version != version || code.RepositoryURL != repository {
			t.Fatalf("code/provenance %s %+v %v", id, code, err)
		}
		loaded, ok := a.pluginRunningInstances.Load(id)
		if !ok {
			t.Fatal("actual local worker not loaded", id)
		}
		object := loaded.(*originalPluginInstance)
		var snapshot struct {
			Version string         `json:"version"`
			Config  map[string]any `json:"config"`
		}
		if err := object.readSession(ctx, "snapshot", &snapshot); err != nil || snapshot.Version != version || snapshot.Config["key"] != "local-owned-value" {
			t.Fatalf("actual worker/config %s %+v %v", id, snapshot, err)
		}
		return object
	}
	primary := checkVersion(v1.PluginID, "1.0")
	checkVersion(cloneID, "1.0")
	state := &originalLocalWatchState{}
	publish(v2, bytes2)
	if failures := a.scanOriginalLocalRepositories(ctx, state); len(failures) != 0 {
		t.Fatal(failures)
	}
	if checkVersion(v1.PluginID, "1.0") != primary {
		t.Fatal("disabled watcher replaced running worker")
	}
	publish(v1, bytes1)
	configure([]string{root}, true)
	if failures := a.scanOriginalLocalRepositories(ctx, state); len(failures) != 0 {
		t.Fatal(failures)
	}
	publish(v2, bytes2)
	if failures := a.scanOriginalLocalRepositories(ctx, state); len(failures) != 0 {
		t.Fatal(failures)
	}
	updated := checkVersion(v1.PluginID, "2.0")
	if updated == primary {
		t.Fatal("watch publication did not replace actual worker")
	}
	if clone := checkVersion(cloneID, "1.0"); clone != cloneObject {
		t.Fatal("primary source update replaced clone")
	}
	// A mismatched publication fails acquisition and restores the prior runtime
	// code. Its next valid publication can be retried without toggling the watcher.
	publish(v1, []byte("broken-local-publication"))
	if failures := a.scanOriginalLocalRepositories(ctx, state); len(failures) != 1 {
		t.Fatal("broken publication not reported", failures)
	}
	if got := checkVersion(v1.PluginID, "2.0"); got != updated {
		t.Fatal("failed publication replaced live worker")
	}
	publish(v1, bytes1)
	if failures := a.scanOriginalLocalRepositories(ctx, state); len(failures) != 0 {
		t.Fatal(failures)
	}
	checkVersion(v1.PluginID, "1.0")
	// Auto sync follows source edits, even when the local developer chooses a
	// lower version; market upgrade suggestions still only suggest increases.
	response = callTest(t, a.Handler(), "DELETE", "/api/plugins/"+v1.PluginID, "", cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	publish(v2, bytes2)
	if failures := a.scanOriginalLocalRepositories(ctx, state); len(failures) != 0 {
		t.Fatal(failures)
	}
	in, err := a.pluginStore.Installation(ctx, v1.PluginID)
	if err != nil || in.Installed {
		t.Fatal("watcher reinstalled uninstalled primary", in, err)
	}
	if clone := checkVersion(cloneID, "1.0"); clone != cloneObject {
		t.Fatal("uninstalled primary update changed clone")
	}
	configure([]string{}, false)
	install(false)
	checkVersion(cloneID, "1.0")
	var runs int
	if err = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatal("legacy executor used", runs, err)
	}
}

func TestOriginalLocalRepositoryClosedAdmissionDoesNotReload(t *testing.T) {
	a, _, object := ownedStandardSDKFixture(t)
	settings := a.Settings()
	settings.PluginAutoReload = true
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	a.closeOriginalBackgroundAdmission()
	if err := a.reloadOriginalLocalCandidate(context.Background(), artifacts.Discovery{PluginID: ownedStandardFixtureID, RepositoryURL: "local://untrusted?path=/unconfigured"}); err != nil {
		t.Fatal("closed watcher admitted source inspection", err)
	}
	current, ok := a.pluginRunningInstances.Load(ownedStandardFixtureID)
	if !ok || current != object {
		t.Fatal("shutdown admitted a replacement worker")
	}
	in, err := a.pluginStore.Installation(context.Background(), ownedStandardFixtureID)
	if err != nil || in.Version != "1.0" {
		t.Fatal("shutdown changed the installation", in, err)
	}
}

func TestOriginalLocalRepositorySettingsFailureAndStaleFullSave(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	root := t.TempDir()
	stale := callTest(t, a.Handler(), "GET", "/api/settings", "", cookie, "")
	var snapshot struct {
		Data map[string]any `json:"data"`
	}
	if stale.Code != 200 || json.Unmarshal(stale.Body.Bytes(), &snapshot) != nil {
		t.Fatal("settings read failed")
	}
	body, _ := json.Marshal(map[string]any{"plugin_markets": []string{}, "plugin_local_repo_paths": []string{root}, "plugin_auto_reload": true})
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/markets", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	body, _ = json.Marshal(snapshot.Data)
	response = callTest(t, a.Handler(), "PUT", "/api/settings", string(body), cookie, "")
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if !reflect.DeepEqual(a.Settings().PluginLocalRepoPaths, []string{root}) || !a.Settings().PluginAutoReload {
		t.Fatal("stale full settings overwrote local controls")
	}
	// Older market clients that omit new fields preserve their current values.
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/markets", `{"plugin_markets":[]}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	body, _ = json.Marshal(map[string]any{"plugin_markets": []string{}, "plugin_local_repo_paths": []string{filepath.Join(root, "missing")}, "plugin_auto_reload": false})
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/markets", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	if !reflect.DeepEqual(a.Settings().PluginLocalRepoPaths, []string{filepath.Join(root, "missing")}) || a.Settings().PluginAutoReload {
		t.Fatal("fixed source must persist future directory and auto flag independently")
	}
	// Invalid OS path is rejected atomically; a future directory is valid config.
	body, _ = json.Marshal(map[string]any{"plugin_local_repo_paths": []string{"bad\x00path"}, "plugin_auto_reload": true})
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/markets", string(body), cookie, "")
	if response.Code == 200 || a.Settings().PluginAutoReload || !reflect.DeepEqual(a.Settings().PluginLocalRepoPaths, []string{filepath.Join(root, "missing")}) {
		t.Fatal("invalid NUL path changed committed future source")
	}
	response = callTest(t, a.Handler(), "GET", "/api/plugins/market?details=true", "", cookie, "")
	var market struct {
		Data struct {
			Sources []originalMarketSourceStatus `json:"sources"`
		} `json:"data"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &market) != nil || len(market.Data.Sources) != 1 || market.Data.Sources[0].Success || market.Data.Sources[0].Message == "" {
		t.Fatal("invalid index silently omitted", response.Body.String())
	}
}

// Fixed PLUGIN_LOCAL_REPO_PATHS has no 32-directory cap; future directories
// and ordered repeats are configuration, not discovery/install success.
func TestOriginalLocalFutureSourcesPreserveOrderedRepeatsWithoutCap(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	root := t.TempDir()
	paths := []string{}
	for i := 0; i < 34; i++ {
		paths = append(paths, filepath.Join(root, fmt.Sprintf("future-%02d", i)))
	}
	paths = append(paths, paths[0])
	raw, _ := json.Marshal(map[string]any{"plugin_local_repo_paths": paths, "plugin_auto_reload": false, "plugin_markets": []string{}})
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/markets", string(raw), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	if !reflect.DeepEqual(a.Settings().PluginLocalRepoPaths, paths) {
		t.Fatal("source order, repeats or future directory settings changed")
	}
	var result struct {
		Data struct {
			Items   []pluginCard
			Sources []originalMarketSourceStatus
		}
	}
	response = callTest(t, a.Handler(), "GET", "/api/plugins/market?details=true", "", cookie, "")
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Data.Items) != 0 || len(result.Data.Sources) != len(paths) {
		t.Fatal("saved future sources fabricated candidates or disappeared from feedback")
	}
	for _, source := range result.Data.Sources {
		if source.Success || source.Message == "" {
			t.Fatal("missing directory was claimed discovered")
		}
	}
}
