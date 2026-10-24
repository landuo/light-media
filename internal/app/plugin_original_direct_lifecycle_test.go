package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestOriginalDirectManagerCloneResetUninstallActualObjects(t *testing.T) {
	a, cookie, source := originalBaseSDKFixture(t)
	ctx := context.Background()
	get := callTest(t, a.Handler(), "GET", "/api/plugins/"+ownedBaseFixtureID, "", cookie, "")
	var initial struct {
		Data json.RawMessage `json:"data"`
	}
	if get.Code != http.StatusOK || json.Unmarshal(get.Body.Bytes(), &initial) != nil || string(initial.Data) != "{}" {
		t.Fatalf("manager GET must be empty dictionary: %d %s", get.Code, get.Body.String())
	}
	save := callTest(t, a.Handler(), "PUT", "/api/plugins/"+ownedBaseFixtureID, `{"marker":"source","enabled":false,"onlyonce":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, save.Code, save.Body.Bytes())
	a.wg.Wait()
	before := readOriginalBaseSDKSnapshot(t, source)
	cloneResponse := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedBaseFixtureID+"/clone", `{"suffix":"Twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, cloneResponse.Code, cloneResponse.Body.Bytes())
	a.wg.Wait()
	cloneID := ownedBaseFixtureID + "twin"
	clone := originalBaseSDKLoaded(t, a, cloneID)
	got := readOriginalBaseSDKSnapshot(t, clone)
	if clone == source || got.InitCount != 1 {
		t.Fatal("clone final reload must create a new actual process", got.InitCount)
	}
	var cfg map[string]any
	if json.Unmarshal(got.InitInput, &cfg) != nil || cfg["marker"] != "source" || cfg["enabled"] != false || cfg["enable"] != false || cfg["onlyonce"] != true {
		t.Fatalf("original clone config transformation: %s", got.InitInput)
	}
	stop, err := a.pluginStore.Data(ctx, cloneID, "owned_stop")
	if err != nil {
		t.Fatal(err)
	}
	var marker struct {
		InitCount int `json:"init_count"`
	}
	if json.Unmarshal(stop.Value, &marker) != nil || marker.InitCount != 2 {
		t.Fatalf("nonempty clone must init twice on first process before endpoint reload: %s", stop.Value)
	}
	sourceAfter := readOriginalBaseSDKSnapshot(t, source)
	if sourceAfter.InitCount != before.InitCount || !originalBaseJSONEqual(sourceAfter.InitInput, before.InitInput) {
		t.Fatal("clone changed source actual object")
	}
	resetResponse := callTest(t, a.Handler(), "POST", "/api/plugins/"+cloneID+"/reset", `{}`, cookie, "")
	assertOriginalRegistrySuccess(t, resetResponse.Code, resetResponse.Body.Bytes())
	a.wg.Wait()
	reset := originalBaseSDKLoaded(t, a, cloneID)
	resetSnapshot := readOriginalBaseSDKSnapshot(t, reset)
	if reset == clone || string(resetSnapshot.InitInput) != "{}" || string(resetSnapshot.Config) != "null" || len(resetSnapshot.Rows) != 0 {
		t.Fatalf("reset original manager/Base separation: %+v", resetSnapshot)
	}
	if _, err = a.pluginStore.Config(ctx, cloneID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("reset manufactured config", err)
	}
	uninstall := callTest(t, a.Handler(), "DELETE", "/api/plugins/"+ownedBaseFixtureID, "", cookie, "")
	assertOriginalRegistrySuccess(t, uninstall.Code, uninstall.Body.Bytes())
	if _, ok := a.pluginRunningInstances.Load(ownedBaseFixtureID); ok {
		t.Fatal("ordinary uninstall left actual object loaded")
	}
	retained, err := a.pluginStore.Config(ctx, ownedBaseFixtureID)
	if err != nil || !originalBaseJSONEqual(retained.Value, before.Config) {
		t.Fatal("ordinary uninstall removed config", err)
	}
	cloneUninstall := callTest(t, a.Handler(), "DELETE", "/api/plugins/"+cloneID, "", cookie, "")
	assertOriginalRegistrySuccess(t, cloneUninstall.Code, cloneUninstall.Body.Bytes())
	var reply struct {
		DataRetained bool `json:"data_retained"`
	}
	if json.Unmarshal(cloneUninstall.Body.Bytes(), &reply) != nil || reply.DataRetained {
		t.Fatal("clone uninstall did not report actual clearing")
	}
	if _, ok := a.pluginRunningInstances.Load(cloneID); ok {
		t.Fatal("clone uninstall left actual object")
	}
	if _, err = a.pluginStore.Config(ctx, cloneID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("clone config survived", err)
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&count); err != nil || count != 0 {
		t.Fatal("manager lifecycle entered old queue", count, err)
	}
}
