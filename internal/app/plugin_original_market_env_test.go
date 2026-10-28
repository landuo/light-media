package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestOriginalMarketEnvironmentOverrideDoesNotOverwriteSavedSources(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	settings := a.Settings()
	settings.PluginMarkets = []string{"https://saved.invalid/index"}
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	environment := []string{"https://environment.invalid/index"}
	b, err := New(Options{DataDir: a.Dir, PluginMarketsOverride: &environment})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	environment[0] = "https://mutated.invalid/index"
	if got := b.Settings().PluginMarkets; !slices.Equal(got, []string{"https://environment.invalid/index"}) {
		t.Fatal("override aliases caller storage", got)
	}
	response := callTest(t, b.Handler(), "GET", "/api/plugins/markets", "", cookie, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"plugin_markets_locked":true`) {
		t.Fatal(response.Body.String())
	}
	response = callTest(t, b.Handler(), "PUT", "/api/plugins/markets", `{"plugin_markets":["https://changed.invalid/index"]}`, cookie, "")
	if response.Code < 400 || !strings.Contains(response.Body.String(), "PLUGIN_MARKET_ENV_OVERRIDE") {
		t.Fatal("UI replaced environment source", response.Code, response.Body.String())
	}
	if _, err = b.saveOriginalWikiRepositories(context.Background(), []string{"https://github.com/owned/plugins/"}); pluginErrorCode(err) != "PLUGIN_MARKET_ENV_OVERRIDE" {
		t.Fatal("wiki replaced environment source", err)
	}
	// An unrelated save must preserve the original persisted sources, even
	// though the caller obtained its settings snapshot under the env override.
	settings = b.Settings()
	settings.AppDomain = "https://owned.invalid"
	if err = b.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	if err = b.Store.DB.QueryRow("SELECT payload FROM records WHERE kind='config' AND id='main'").Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	var saved Settings
	if err = b.Store.Unseal(sealed, &saved); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(saved.PluginMarkets, []string{"https://saved.invalid/index"}) {
		raw, _ := json.Marshal(saved.PluginMarkets)
		t.Fatal("override leaked into persisted settings", string(raw))
	}
	if err = b.Close(); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{DataDir: a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !slices.Equal(c.Settings().PluginMarkets, saved.PluginMarkets) {
		t.Fatal("removing env did not restore saved sources")
	}
}
