package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins/artifacts"
	"testing"
)

func TestOriginalMarketPrimaryReinstallWithLiveClone(t *testing.T) {
	a, cookie, _ := ownedStandardSDKFixture(t)
	ctx := context.Background()
	installed, err := artifacts.ReadInstalled(a.pluginPackageRoot(), ownedStandardFixtureID, validatePluginArtifactContract)
	if err != nil {
		t.Fatal(err)
	}
	settings := a.Settings()
	settings.PluginMarkets = []string{installed.RepositoryURL}
	if err = a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedStandardFixtureID+"/clone", `{"suffix":"market"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	cloneID := ownedStandardFixtureID + "market"
	clone, ok := a.pluginRunningInstances.Load(cloneID)
	if !ok {
		t.Fatal("actual clone missing")
	}
	readMarket := func() bool {
		t.Helper()
		r := callTest(t, a.Handler(), "GET", "/api/plugins/market", "", cookie, "")
		if r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
		var envelope struct {
			Data []pluginCard `json:"data"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		for _, card := range envelope.Data {
			if card.InstanceID == ownedStandardFixtureID {
				return card.Available
			}
		}
		return false
	}
	if readMarket() {
		t.Fatal("installed primary incorrectly offered for install")
	}
	response = callTest(t, a.Handler(), "DELETE", "/api/plugins/"+ownedStandardFixtureID, "", cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	if !readMarket() {
		t.Fatal("clone source ID hid the uninstalled primary marketplace entry")
	}
	if current, ok := a.pluginRunningInstances.Load(cloneID); !ok || current != clone {
		t.Fatal("primary uninstall changed clone")
	}
	body, _ := json.Marshal(map[string]any{"plugin_id": ownedStandardFixtureID, "repo_url": installed.RepositoryURL})
	response = callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	if readMarket() {
		t.Fatal("reinstalled primary still offered")
	}
	if current, ok := a.pluginRunningInstances.Load(cloneID); !ok || current != clone {
		t.Fatal("primary reinstall changed clone")
	}
	var runs int
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatal("legacy executor used", runs, err)
	}
}
