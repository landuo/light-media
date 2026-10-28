package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestStandardPluginSettingsEventAfterCommitNotOnFailureOrNoChange(t *testing.T) {
	a := newTestApp(t)
	cookie := installOwnedEventFixture(t, a)
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+ownedEventFixtureID, `{"enabled":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	ctx := context.Background()
	settings := a.Settings()
	settings.PluginMarkets = []string{"http://127.0.0.1/owned-index"}
	settings.PluginAutoReload = true
	share := false
	settings.PluginStatisticShare = &share
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	events := requireBusinessEventCount(t, a, ownedEventFixtureID, "config.updated", 1)
	var payload struct {
		Key        []string `json:"key"`
		Value      any      `json:"value"`
		ChangeType string   `json:"change_type"`
	}
	if err := json.Unmarshal(events[0].Data, &payload); err != nil || !reflect.DeepEqual(payload.Key, []string{"PLUGIN_MARKET", "PLUGIN_AUTO_RELOAD", "PLUGIN_STATISTIC_SHARE"}) || payload.Value != nil || payload.ChangeType != "update" {
		t.Fatal("changed-key payload", string(events[0].Data), err)
	}
	if !reflect.DeepEqual(a.Settings().PluginMarkets, settings.PluginMarkets) {
		t.Fatal("event preceded published settings")
	}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	requireBusinessEventCount(t, a, ownedEventFixtureID, "config.updated", 1)
	settings.PluginAutoReload = false
	injected := errors.New("owned rollback")
	a.pluginMu.Lock()
	err := a.saveSettingsWithEffect(ctx, settings, func(context.Context, *sql.Tx, *Settings) error { return injected })
	a.pluginMu.Unlock()
	if !errors.Is(err, injected) {
		t.Fatal("failure not propagated", err)
	}
	a.wg.Wait()
	requireBusinessEventCount(t, a, ownedEventFixtureID, "config.updated", 1)
	if !a.Settings().PluginAutoReload {
		t.Fatal("failed save changed live settings")
	}
}

func TestStandardPluginSettingValuesMatchOriginalSingleKeyEndpoints(t *testing.T) {
	// Fixed system.py:892-922 publishes the saved value and one-key set;
	// system.py:847-876 does the same after Wiki merge. /env remains the
	// separate bulk-key/no-value path exercised above.
	a := newTestApp(t)
	cookie := installOwnedEventFixture(t, a)
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+ownedEventFixtureID, `{"enabled":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	var fetches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		http.Error(w, "repository must not be fetched by settings save", 500)
	}))
	defer server.Close()
	input, _ := json.Marshal(map[string]any{"plugin_markets": []string{server.URL + "/index"}, "plugin_auto_reload": true})
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/markets", string(input), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	events := requireBusinessEventCount(t, a, ownedEventFixtureID, "config.updated", 2)
	values := map[string]json.RawMessage{}
	for _, event := range events {
		var payload struct {
			Key        []string        `json:"key"`
			Value      json.RawMessage `json:"value"`
			ChangeType string          `json:"change_type"`
		}
		if json.Unmarshal(event.Data, &payload) != nil || len(payload.Key) != 1 || payload.ChangeType != "update" {
			t.Fatal("single-key event did not match source", string(event.Data))
		}
		values[payload.Key[0]] = payload.Value
	}
	urlValue, _ := json.Marshal(server.URL + "/index")
	assertOriginalBaseJSON(t, values["PLUGIN_MARKET"], string(urlValue))
	assertOriginalBaseJSON(t, values["PLUGIN_AUTO_RELOAD"], "true")
	if fetches.Load() != 0 {
		t.Fatal("saving sources fetched or installed repository code")
	}
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/markets", string(input), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/markets", `{"plugin_markets":["invalid"]}`, cookie, "")
	if response.Code == 200 {
		t.Fatal("invalid source was saved")
	}
	a.wg.Wait()
	requireBusinessEventCount(t, a, ownedEventFixtureID, "config.updated", 2)
	if _, err := a.saveOriginalWikiRepositories(context.Background(), []string{"https://github.com/Owner/New"}); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	events = requireBusinessEventCount(t, a, ownedEventFixtureID, "config.updated", 3)
	var wiki struct {
		Key   []string `json:"key"`
		Value string   `json:"value"`
	}
	if json.Unmarshal(events[2].Data, &wiki) != nil || !reflect.DeepEqual(wiki.Key, []string{"PLUGIN_MARKET"}) || wiki.Value != server.URL+"/index,https://github.com/Owner/New" {
		t.Fatal("Wiki did not publish the committed merged value", string(events[2].Data))
	}
	before := a.Settings().PluginMarkets
	response = callTest(t, a.Handler(), "PUT", "/api/plugins/markets", `{"plugin_auto_reload":false,"plugin_statistic_share":false}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	events = requireBusinessEventCount(t, a, ownedEventFixtureID, "config.updated", 4)
	var bulk struct {
		Key   []string        `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	if !reflect.DeepEqual(a.Settings().PluginMarkets, before) || json.Unmarshal(events[3].Data, &bulk) != nil ||
		!reflect.DeepEqual(bulk.Key, []string{"PLUGIN_AUTO_RELOAD", "PLUGIN_STATISTIC_SHARE"}) || string(bulk.Value) != "null" {
		t.Fatal("system settings overwrote newer repositories or lost the original bulk event", string(events[3].Data))
	}
}
