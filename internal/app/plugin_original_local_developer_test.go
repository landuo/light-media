package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"light-media/internal/plugins/artifacts"
)

func TestOriginalLocalDeveloperMonitorSourceAndRangeIsolation(t *testing.T) {
	a, cookie, initial := ownedStandardSDKFixture(t)
	ctx := context.Background()
	var oracle struct {
		Cases []struct {
			Developer  bool
			AutoReload bool `json:"auto_reload"`
			Actions    []string
			Candidate  struct {
				Strict bool `json:"strict_system_version"`
			}
		}
	}
	raw, err := os.ReadFile("../../testdata/standard-market/local-watch-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 4 {
		t.Fatal("fixed monitor AST oracle missing", err)
	}
	v1, bytes1 := originalMarketOwnedIndex(t, "OWNED_STANDARD_FIXTURE_BINARY")
	v2, bytes2 := originalMarketOwnedIndex(t, "OWNED_STANDARD_FIXTURE_V2_BINARY")
	root := t.TempDir()
	repository, _ := artifacts.LocalRepositoryURL(ownedStandardFixtureID, root)
	publish := func(index artifacts.Index, payload []byte) {
		t.Helper()
		index.Releases[0].Artifacts[0].URL = "worker"
		raw, _ := json.Marshal(index)
		if os.WriteFile(filepath.Join(root, "worker"), payload, 0600) != nil || os.WriteFile(filepath.Join(root, artifacts.IndexName), raw, 0600) != nil {
			t.Fatal("cannot publish owned local artifact")
		}
	}
	publish(v1, bytes1)
	settings := a.Settings()
	settings.PluginLocalRepoPaths = []string{root}
	settings.PluginMarkets = []string{}
	for _, row := range oracle.Cases {
		a.pluginDeveloperMode = row.Developer
		settings.PluginAutoReload = row.AutoReload
		if err := a.saveSettings(ctx, settings); err != nil {
			t.Fatal(err)
		}
		state := &originalLocalWatchState{}
		if errors := a.scanOriginalLocalRepositories(ctx, state); len(errors) > 0 {
			t.Fatal(errors)
		}
		observed := []string{"stop"}
		if len(state.fingerprints) > 0 {
			observed = append(observed, "start")
		}
		if !reflect.DeepEqual(observed, row.Actions) {
			t.Fatal("DEV/auto monitor differs from actual source AST", row.Developer, row.AutoReload, observed, row.Actions)
		}
		if row.Candidate.Strict == row.Developer {
			t.Fatal("fixed source DEV candidate range expectation missing")
		}
	}
	// core/plugin.py:1462 processes the helper's merged candidate map once;
	// preserving duplicate configured roots must not repeat get_state effects.
	settings.PluginLocalRepoPaths = []string{root, root}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	prior := ownedStandardSnapshotRead(t, initial)
	candidates, statuses := a.originalLocalMarketDeclarations(ctx)
	after := ownedStandardSnapshotRead(t, initial)
	if len(candidates) != 1 || len(statuses) != 2 || after.Calls["get_state"] != prior.Calls["get_state"]+1 || after.Nonce != prior.Nonce || after.InitCount != prior.InitCount {
		t.Fatal("local root merge repeated live state getter or initialization")
	}
	settings.PluginLocalRepoPaths = []string{root}
	settings.PluginAutoReload = false
	a.pluginDeveloperMode = true
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	state := &originalLocalWatchState{}
	if failures := a.scanOriginalLocalRepositories(ctx, state); len(failures) > 0 {
		t.Fatal(failures)
	}
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedStandardFixtureID+"/clone", `{"suffix":"DevScope"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	cloneID := ownedStandardFixtureID + "devscope"
	cloneBefore, _ := a.pluginRunningInstances.Load(cloneID)
	v2.SystemVersion = json.RawMessage(`">=3"`)
	publish(v2, bytes2)
	if failures := a.scanOriginalLocalRepositories(ctx, state); len(failures) > 0 {
		t.Fatal("source DEV watcher must allow range mismatch", failures)
	}
	primary, ok := a.pluginRunningInstances.Load(ownedStandardFixtureID)
	if !ok || primary.(*originalPluginInstance).manifest.Version != "2.0" {
		t.Fatal("DEV flag failed to synchronize actual v2 SDK with auto switch off")
	}
	if clone, _ := a.pluginRunningInstances.Load(cloneID); clone != cloneBefore {
		t.Fatal("DEV reload replaced clone")
	}
	// DEV is a trusted process watcher mode, not manual installation authority.
	body, _ := json.Marshal(map[string]any{"plugin_id": ownedStandardFixtureID, "repo_url": repository, "force": true})
	response = callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	var result struct{ Data struct{ Success bool } }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Data.Success {
		t.Fatal("DEV bypass leaked to manual system-version install gate")
	}
	body, _ = json.Marshal(map[string]any{"plugin_id": ownedStandardFixtureID, "repo_url": repository, "force": true, "developer_local_watch": true})
	response = callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	if response.Code != 400 {
		t.Fatal("HTTP accepted trusted DEV monitor authority field")
	}
	v1.SystemVersion = json.RawMessage(`">=3"`)
	v1.Releases[0].Artifacts[0].HostContract = "owned-invalid-contract"
	publish(v1, bytes1)
	if failures := a.scanOriginalLocalRepositories(ctx, state); len(failures) != 1 {
		t.Fatal("DEV watcher bypassed executable SDK contract", failures)
	}
	if current, _ := a.pluginRunningInstances.Load(ownedStandardFixtureID); current != primary {
		t.Fatal("invalid SDK replaced actual DEV worker")
	}
}
