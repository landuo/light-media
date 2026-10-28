package app

import (
	"bytes"
	"encoding/json"
	"sync/atomic"
	"testing"
)

func verifyOwnedProductionSystemVersion(t *testing.T, repository string, artifactHits *atomic.Int64, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	id := ownedStandardFixtureID
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": id, "repo_url": repository + "/version-one/index", "force": true})
	success("PUT", "/api/plugins/"+id, map[string]any{"enabled": true, "key": "owned-system-version"})
	before := request("GET", "/api/v1/plugin/"+id+"/echo", nil, 200)
	hits := artifactHits.Load()
	success("PUT", "/api/plugins/markets", map[string]any{"plugin_markets": []string{repository + "/incompatible/index"}})
	var market struct{ Data struct{ Items []pluginCard } }
	readMarket := func() {
		t.Helper()
		if json.Unmarshal(request("GET", "/api/plugins/market?details=true&force=true", nil, 200), &market) != nil || len(market.Data.Items) != 1 {
			t.Fatal("production system_version card missing")
		}
	}
	readMarket()
	card := market.Data.Items[0]
	if card.Available || card.SystemVersionCompatible == nil || *card.SystemVersionCompatible || card.SystemVersionMessage != "插件要求 MoviePilot 版本 >=3，当前版本 v2.15.7 不满足，已拒绝安装" {
		t.Fatal("actual Linux market ignored pinned system range")
	}
	for _, force := range []bool{false, true} {
		var result struct{ Data struct{ Success bool } }
		if json.Unmarshal(request("POST", "/api/plugins/install", map[string]any{"plugin_id": id, "repo_url": repository + "/incompatible/index", "force": force}, 200), &result) != nil || result.Data.Success {
			t.Fatal("actual Linux accepted incompatible installation", force)
		}
	}
	after := request("GET", "/api/v1/plugin/"+id+"/echo", nil, 200)
	var old, next map[string]json.RawMessage
	if json.Unmarshal(before, &old) != nil || json.Unmarshal(after, &next) != nil {
		t.Fatal("actual system-version SDK snapshot invalid")
	}
	for _, key := range []string{"nonce", "init_count", "config", "value"} {
		if !bytes.Equal(old[key], next[key]) {
			t.Fatal("failed system-version gate changed SDK", key)
		}
	}
	if artifactHits.Load() != hits {
		t.Fatal("incompatible metadata fetched an actual executable")
	}
	stop()
	start()
	// Await the real market read joining startup synchronization; incompatible
	// candidates must not replace installed code while restoring the actual SDK.
	readMarket()
	var cold map[string]json.RawMessage
	if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/echo", nil, 200), &cold) != nil || bytes.Equal(cold["nonce"], old["nonce"]) || !bytes.Equal(cold["config"], old["config"]) || !bytes.Equal(cold["value"], old["value"]) || string(cold["version"]) != `"1.0"` || artifactHits.Load() != hits {
		t.Fatal("cold recovery consumed incompatible candidate or lost saved state")
	}
	success("DELETE", "/api/plugins/"+id, nil)
	success("PUT", "/api/plugins/markets", map[string]any{"plugin_markets": []string{repository + "/index"}})
	t.Log("production Linux system_version verified: source-derived >=3 gate visible, existing-class and forced install both fail before artifact fetch and preserve actual PID/init/config/KV; cold startup skips incompatible candidate and restores v1 SDK; owned source/settings restored")
}
