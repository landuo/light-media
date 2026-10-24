package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"
)

// This is the real HTTP acquisition/start/save path and real persistent SDK.
// The old queue tables are observed only to prove they do not participate.
func TestOriginalDirectManagerStartsWithEmptyConfigWithoutLegacyRun(t *testing.T) {
	a, cookie, object := originalBaseSDKFixture(t)
	ctx := context.Background()
	if _, err := a.pluginStore.Config(ctx, ownedBaseFixtureID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("start manufactured config", err)
	}
	snapshot := readOriginalBaseSDKSnapshot(t, object)
	if string(snapshot.InitInput) != "{}" || string(snapshot.Config) != "null" || snapshot.InitCount != 1 {
		t.Fatalf("manager/Base separation: input=%s base=%s calls=%d", snapshot.InitInput, snapshot.Config, snapshot.InitCount)
	}
	var count int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?`, ownedBaseFixtureID).Scan(&count); err != nil || count != 0 {
		t.Fatal("direct start entered retired queue", count, err)
	}
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+ownedBaseFixtureID, "{}", cookie, "")
	if response.Code != http.StatusOK {
		t.Fatal("direct save/init failed", response.Code, response.Body.String())
	}
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	loaded, ok := a.pluginRunningInstances.Load(ownedBaseFixtureID)
	if !ok || loaded != object {
		t.Fatal("save replaced original object")
	}
	snapshot = readOriginalBaseSDKSnapshot(t, object)
	if snapshot.InitCount != 2 || string(snapshot.InitInput) != "{}" {
		t.Fatal("save did not call exact same actual SDK object", snapshot.InitCount, string(snapshot.InitInput))
	}
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?`, ownedBaseFixtureID).Scan(&count); err != nil || count != 0 {
		t.Fatal("direct save entered retired queue", count, err)
	}
}
