package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestOriginalSystemVersionMarketAndInstallationKeepActualWorker(t *testing.T) {
	a, cookie, object := ownedStandardSDKFixture(t)
	index, payload := originalMarketOwnedIndex(t, "OWNED_STANDARD_FIXTURE_V2_BINARY")
	var rangeJSON atomic.Value
	rangeJSON.Store(json.RawMessage(`">=3"`))
	var hits atomic.Int32
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/worker" {
			hits.Add(1)
			w.Write(payload)
			return
		}
		copy := index
		copy.SystemVersion = rangeJSON.Load().(json.RawMessage)
		json.NewEncoder(w).Encode(copy)
	}))
	defer repository.Close()
	index.Releases[0].Artifacts[0].URL = repository.URL + "/worker"
	raw, _ := json.Marshal(map[string]any{"plugin_markets": []string{repository.URL + "/index"}})
	result := callTest(t, a.Handler(), "PUT", "/api/plugins/markets", string(raw), cookie, "")
	assertOriginalRegistrySuccess(t, result.Code, result.Body.Bytes())
	readCard := func() pluginCard {
		t.Helper()
		response := callTest(t, a.Handler(), "GET", "/api/plugins/market?details=true&force=true", "", cookie, "")
		var result struct{ Data struct{ Items []pluginCard } }
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Data.Items) != 1 {
			t.Fatal("system-version market declaration missing")
		}
		return result.Data.Items[0]
	}
	blocked := readCard()
	if blocked.Available || blocked.SystemVersionCompatible == nil || *blocked.SystemVersionCompatible || blocked.SystemVersionMessage != "插件要求 MoviePilot 版本 >=3，当前版本 v2.15.7 不满足，已拒绝安装" {
		t.Fatal("incompatible source card did not expose actual original gate")
	}
	for _, force := range []bool{false, true} {
		body, _ := json.Marshal(map[string]any{"plugin_id": ownedStandardFixtureID, "repo_url": repository.URL + "/index", "force": force})
		response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
		var result struct{ Data struct{ Success bool } }
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Data.Success {
			t.Fatal("current incompatible install accepted", force)
		}
		current, ok := a.pluginRunningInstances.Load(ownedStandardFixtureID)
		if !ok || current != object || hits.Load() != 0 {
			t.Fatal("compatibility failure changed actual worker or downloaded code")
		}
	}
	rangeJSON.Store(json.RawMessage(`">=2.12.0,<3"`))
	card := readCard()
	if !card.Available || card.SystemVersionCompatible == nil || !*card.SystemVersionCompatible || card.SystemVersionMessage != "" {
		t.Fatal("compatible range did not recover actual candidate")
	}
}
