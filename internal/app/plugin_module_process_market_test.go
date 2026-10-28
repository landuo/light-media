package app

import (
	"encoding/json"
	"reflect"
	"sync/atomic"
	"testing"
)

// Source expectations: core/cache.py:1294-1315, core/plugin.py:1566,1820,
// endpoints/plugin.py:295-340,428 and endpoints/system.py:892. These calls
// cross the production listener and inspect the real SDK PID and persistent
// settings. Only repository HTTP responses are controlled by this test.
func verifyOwnedProductionMarketMetadata(t *testing.T, repository string, unavailable *atomic.Bool, artifactHits, metadataHits *atomic.Int64,
	request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	sources := []string{repository + "/version-one/index", repository + "/version-two/index", repository + "/same-version/index"}
	// Keep the installed worker at the offered latest version. Fixed startup
	// sync intentionally upgrades older installed code; introducing a downgrade
	// here would race that independent startup operation rather than test refresh.
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": ownedStandardFixtureID, "repo_url": repository + "/index", "release_version": "2.0", "force": true})
	var before struct {
		Version   string          `json:"version"`
		Nonce     int             `json:"nonce"`
		InitCount int             `json:"init_count"`
		Config    json.RawMessage `json:"config"`
	}
	if json.Unmarshal(request("GET", "/api/v1/plugin/"+ownedStandardFixtureID+"/echo", nil, 200), &before) != nil || before.Version != "2.0" {
		t.Fatal("actual worker did not install requested 2.0", before)
	}
	hits := artifactHits.Load()
	success("PUT", "/api/plugins/markets", map[string]any{"plugin_markets": sources})
	type market struct {
		Data struct {
			Items   []pluginCard                 `json:"items"`
			Sources []originalMarketSourceStatus `json:"sources"`
		} `json:"data"`
	}
	read := func(force bool) market {
		t.Helper()
		path := "/api/plugins/market?details=true"
		if force {
			path += "&force=true"
		}
		var got market
		if json.Unmarshal(request("GET", path, nil, 200), &got) != nil || len(got.Data.Sources) != 3 {
			t.Fatal("market did not retain configured sources")
		}
		return got
	}
	selected := func(got market, source string) {
		t.Helper()
		var history struct{ Data originalPluginHistory }
		if json.Unmarshal(request("GET", "/api/plugins/history/"+ownedStandardFixtureID+"?force=false", nil, 200), &history) != nil || history.Data.RepositoryURL != source || history.Data.CurrentVersion != "2.0" || history.Data.LatestVersion != "2.0" {
			t.Fatal("source version/order differs from fixed process_plugins_list and exact-ID history", history)
		}
	}
	initial := read(true)
	selected(initial, sources[1])
	unavailable.Store(true)
	defer unavailable.Store(false)
	metadataBefore := metadataHits.Load()
	var local struct{ Data []pluginCard }
	if json.Unmarshal(request("GET", "/api/plugins?state=installed", nil, 200), &local) != nil || len(local.Data) != 1 || local.Data[0].InstanceID != ownedStandardFixtureID || local.Data[0].RepositoryURL != "" || local.Data[0].HasUpdate || !local.Data[0].Available || metadataHits.Load() != metadataBefore {
		t.Fatal("installed-only class listing depended on repository HTTP or fabricated update metadata")
	}
	failed := read(true)
	for _, source := range failed.Data.Sources {
		if source.Success || source.Cached || source.Message == "" {
			t.Fatal("forced failure claimed success or substituted cache", source)
		}
	}
	for _, card := range failed.Data.Items {
		if card.InstanceID == ownedStandardFixtureID || card.InstanceID == "OwnedBaseFixture" {
			t.Fatal("failed fresh read advertised the old remote candidate", card)
		}
	}
	var history struct{ Data originalPluginHistory }
	if json.Unmarshal(request("GET", "/api/plugins/history/"+ownedStandardFixtureID, nil, 200), &history) != nil || history.Data.CurrentVersion != "2.0" || len(history.Data.History) != 0 || len(history.Data.Items) != 0 {
		t.Fatal("failed history did not fall back to installed metadata", history)
	}
	if json.Unmarshal(request("GET", "/api/plugins/releases/"+ownedStandardFixtureID+"?force=true&repo_url="+sources[1], nil, 200), &history) != nil || history.Data.CurrentVersion != "2.0" || history.Data.ReleaseSupported || history.Data.LatestVersion != "" {
		t.Fatal("failed release read fabricated remote versions", history)
	}
	ordinary := read(false)
	selected(ordinary, sources[1])
	for i, source := range ordinary.Data.Sources {
		if !source.Success || !source.Cached || source.CheckedAt != initial.Data.Sources[i].CheckedAt {
			t.Fatal("fresh failure mutated successful cache entry or TTL", source)
		}
	}
	unavailable.Store(false)
	sources[0], sources[2] = sources[2], sources[0]
	success("PUT", "/api/plugins/markets", map[string]any{"plugin_markets": sources})
	selected(read(false), sources[0])
	var after struct {
		Version   string          `json:"version"`
		Nonce     int             `json:"nonce"`
		InitCount int             `json:"init_count"`
		Config    json.RawMessage `json:"config"`
	}
	if json.Unmarshal(request("GET", "/api/v1/plugin/"+ownedStandardFixtureID+"/echo", nil, 200), &after) != nil || !reflect.DeepEqual(before, after) || artifactHits.Load() != hits {
		t.Fatal("metadata save/refresh initialized, downloaded or replaced the worker", before, after, hits, artifactHits.Load())
	}
	stop()
	start()
	var persisted struct {
		Data struct {
			Markets []string `json:"plugin_markets"`
		}
	}
	if json.Unmarshal(request("GET", "/api/plugins/markets", nil, 200), &persisted) != nil || !reflect.DeepEqual(persisted.Data.Markets, sources) {
		t.Fatal("repository order did not survive the production restart", persisted)
	}
	selected(read(true), sources[0])
	if json.Unmarshal(request("GET", "/api/plugins/history/"+ownedStandardFixtureID, nil, 200), &history) != nil || history.Data.CurrentVersion != "2.0" || history.Data.LatestVersion != "2.0" || history.Data.History["2.0"] != "Owned executable update" {
		t.Fatal("history recovery lost installed version or author notes", history)
	}
	t.Log("production market verified: source version/tie order, settings restart persistence, fresh failure/unchanged ordinary cache, history local fallback/recovery; no artifact download or SDK reinitialization during metadata operations")
}
