package app

import (
	"encoding/json"
	"testing"
)

// Source clone_plugin rewrites class attributes. Installing an exact-ID new
// class then replaces those attributes; configuration/data remain at that ID.
// A catalog name grants no native host capability and cannot ban a valid SDK.
func verifyOwnedProductionCatalogIdentities(t *testing.T, repository string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	const clone = "OwnedStandardFixtureexact"
	type snapshot struct {
		Version   string         `json:"version"`
		PID       int            `json:"nonce"`
		InitCount int            `json:"init_count"`
		Config    map[string]any `json:"config"`
		Value     string         `json:"value"`
		Calls     map[string]int `json:"calls"`
	}
	read := func(id string) snapshot {
		t.Helper()
		var got snapshot
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/echo", nil, 200), &got) != nil || got.PID <= 0 {
			t.Fatal("actual SDK snapshot unavailable")
		}
		return got
	}
	success("POST", "/api/plugins/"+ownedStandardFixtureID+"/clone", map[string]any{"suffix": "Exact", "version": "9.7", "name": "Owned custom clone", "description": "custom old class", "icon": "https://example.invalid/custom.svg"})
	success("PUT", "/api/plugins/"+clone, map[string]any{"enabled": false, "key": "exact-kept"})
	beforeSource, beforeClone := read(ownedStandardFixtureID), read(clone)
	var history struct{ Data originalPluginHistory }
	if json.Unmarshal(request("GET", "/api/plugins/history/"+clone, nil, 200), &history) != nil || history.Data.CurrentVersion != "9.7" || history.Data.LatestVersion != "2.0" {
		t.Fatal("clone logical version/history did not match authored class")
	}
	afterSource, afterClone := read(ownedStandardFixtureID), read(clone)
	// Current market has three sources, each with both source and exact-ID
	// declarations. get_local_plugins additionally asks each object once.
	if afterSource.Calls["get_state"] != beforeSource.Calls["get_state"]+4 || afterClone.Calls["get_state"] != beforeClone.Calls["get_state"]+4 {
		t.Fatal("history did not read local and per-source states", beforeSource.Calls, afterSource.Calls, beforeClone.Calls, afterClone.Calls)
	}
	assertCard := func(version, name string) {
		t.Helper()
		var cards struct{ Data []pluginCard }
		if json.Unmarshal(request("GET", "/api/plugins?state=installed", nil, 200), &cards) != nil {
			t.Fatal("cards unavailable")
		}
		for _, card := range cards.Data {
			if card.InstanceID == clone {
				if card.DisplayVersion != version || card.Name != name || card.HasUpdate {
					t.Fatal("clone class attributes/update comparison incorrect", card)
				}
				return
			}
		}
		t.Fatal("clone card missing")
	}
	assertCard("9.7", "Owned custom clone")
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": clone, "repo_url": repository + "/index", "force": true})
	updated := read(clone)
	if updated.PID == beforeClone.PID || updated.InitCount != 1 || updated.Version != "2.0" || updated.Value != "exact-kept" || updated.Config["key"] != "exact-kept" {
		t.Fatal("exact-ID update failed to replace code while retaining config/data", updated)
	}
	if read(ownedStandardFixtureID).PID != beforeSource.PID {
		t.Fatal("clone update replaced source")
	}
	assertCard("2.0", "Owned exact class")
	stop()
	start()
	restarted := read(clone)
	if restarted.PID == updated.PID || restarted.Value != "exact-kept" || restarted.Config["key"] != "exact-kept" {
		t.Fatal("exact clone failed restart recovery")
	}
	assertCard("2.0", "Owned exact class")
	success("DELETE", "/api/plugins/"+clone, nil)
	request("GET", "/api/plugins/history/"+clone, nil, 404)
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": "AutoSignIn", "repo_url": repository + "/legacy-index"})
	success("PUT", "/api/plugins/AutoSignIn", map[string]any{"enabled": false, "owned_marker": "synthetic-only"})
	var legacy struct {
		Version   string         `json:"version"`
		InitCount int            `json:"init_count"`
		Config    map[string]any `json:"config"`
	}
	if json.Unmarshal(request("GET", "/api/v1/plugin/AutoSignIn/status", nil, 200), &legacy) != nil || legacy.Version != "1.0" || legacy.InitCount != 2 || legacy.Config["owned_marker"] != "synthetic-only" {
		t.Fatal("legacy name blocked real standard SDK config/API", legacy)
	}
	success("POST", "/api/plugins/AutoSignIn/clone", map[string]any{"suffix": "Owned"})
	request("GET", "/api/v1/plugin/AutoSignInowned/status", nil, 200)
	success("DELETE", "/api/plugins/AutoSignInowned", nil)
	success("DELETE", "/api/plugins/AutoSignIn", nil)
	request("GET", "/api/v1/plugin/AutoSignIn/status", nil, 404)
	t.Log("production catalog identities verified: clone 9.7 suppresses lower 2.0 update; exact-ID replacement resets authored metadata and preserves independent config/KV through restart; history asks all actual objects and each source declaration; legacy-name standard fixture install/config/API/clone/uninstall succeed without business implementation")
}
