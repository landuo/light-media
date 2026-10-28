package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestPluginOriginalMarketSourcesPersistenceDiscoveryAndActualCards(t *testing.T) {
	binary := os.Getenv("OWNED_MODULE_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("requires actual OWNED_MODULE_FIXTURE_BINARY")
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	raw, err := exec.CommandContext(ctx, binary, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.PluginID != "OwnedModuleFixture" || manifest.Version != "1.0" {
		t.Fatal("wrong owned manifest")
	}
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	makeIndex := func(m plugins.Manifest, contract string) artifacts.Index {
		return artifacts.Index{SchemaVersion: 1, PluginID: m.PluginID, CurrentVersion: m.Version, Releases: []artifacts.Release{{Version: m.Version, Manifest: m, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: contract}}}}}
	}
	good := makeIndex(manifest, pluginPureSDKContract)
	badManifest := manifest
	badManifest.PluginID = "OwnedUnsupportedMarket"
	badManifest.Name = "Owned unsupported contract"
	unsupported := makeIndex(badManifest, "owned-unsupported/v1")
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/good":
			_ = json.NewEncoder(w).Encode(good)
		case "/unsupported":
			_ = json.NewEncoder(w).Encode(unsupported)
		case "/binary":
			downloads.Add(1)
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	good.Releases[0].Artifacts[0].URL = server.URL + "/binary"
	unsupported.Releases[0].Artifacts[0].URL = server.URL + "/binary"
	dir := t.TempDir()
	a, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if a != nil {
			a.closeOriginalSessions()
			_ = a.Close()
		}
	})
	cookie := setupTestAdmin(t, a)
	settings := a.Settings()
	settings.AppDomain = "https://owned.invalid"
	settings.TMDB.APIKey = "owned-synthetic-secret"
	settings.APIToken = "owned-synthetic-api-token"
	if err = a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	staleResponse := callTest(t, a.Handler(), "GET", "/api/settings", "", cookie, "")
	var stale struct {
		Data map[string]any `json:"data"`
	}
	if staleResponse.Code != 200 || json.Unmarshal(staleResponse.Body.Bytes(), &stale) != nil {
		t.Fatal("settings snapshot failed")
	}
	if r := callTest(t, a.Handler(), "GET", "/api/plugins/markets", "", nil, ""); r.Code != 401 {
		t.Fatalf("anonymous markets: %d", r.Code)
	}
	putMarkets := func(markets []string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"plugin_markets": markets})
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/markets", string(body), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	}
	checkMarkets := func(want []string) {
		t.Helper()
		r := callTest(t, a.Handler(), "GET", "/api/plugins/markets", "", cookie, "")
		var body struct {
			Data struct {
				Markets []string `json:"plugin_markets"`
			} `json:"data"`
		}
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &body) != nil || !reflect.DeepEqual(body.Data.Markets, want) {
			t.Fatalf("market source order not preserved: %s", r.Body.String())
		}
	}
	first := []string{server.URL + "/unsupported", server.URL + "/good"}
	putMarkets(first)
	checkMarkets(first)
	second := []string{server.URL + "/good", server.URL + "/unsupported"}
	putMarkets(second)
	checkMarkets(second)
	current := a.Settings()
	if current.TMDB.APIKey != settings.TMDB.APIKey || current.APIToken != settings.APIToken || current.AppDomain != settings.AppDomain {
		t.Fatal("market section save changed unrelated settings/secrets")
	}
	// Old full form snapshot is not the source section's current authority.
	stale.Data["plugin_markets"] = []string{}
	staleRaw, _ := json.Marshal(stale.Data)
	r := callTest(t, a.Handler(), "PUT", "/api/settings", string(staleRaw), cookie, "")
	if r.Code != 200 {
		t.Fatalf("stale full settings save: %d %s", r.Code, r.Body.String())
	}
	checkMarkets(second)
	if a.Settings().TMDB.APIKey != settings.TMDB.APIKey || a.Settings().APIToken != settings.APIToken {
		t.Fatal("stale redacted snapshot lost secrets")
	}
	readCards := func(path string) []pluginCard {
		t.Helper()
		r := callTest(t, a.Handler(), "GET", path, "", cookie, "")
		var body struct {
			Data []pluginCard `json:"data"`
		}
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &body) != nil {
			t.Fatalf("cards %s: %d %s", path, r.Code, r.Body.String())
		}
		return body.Data
	}
	find := func(cards []pluginCard, id string) (pluginCard, bool) {
		for _, card := range cards {
			if card.InstanceID == id {
				return card, true
			}
		}
		return pluginCard{}, false
	}
	market := readCards("/api/plugins/market")
	card, ok := find(market, manifest.PluginID)
	if !ok || card.Name != manifest.Name || card.Version != "1.0" || card.RepositoryURL != server.URL+"/good" || card.Installed {
		t.Fatalf("actual external discovery metadata incorrect: %+v", card)
	}
	if blocked, exists := find(market, badManifest.PluginID); !exists || blocked.Available || blocked.UnavailableReason == "" || blocked.Name != badManifest.Name {
		t.Fatal("incompatible metadata must remain visible and explicitly not installable", blocked)
	}
	unsupportedBody, _ := json.Marshal(map[string]any{"plugin_id": badManifest.PluginID, "repo_url": server.URL + "/unsupported"})
	rejected := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(unsupportedBody), cookie, "")
	var rejectedResult struct {
		Data struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		} `json:"data"`
	}
	if rejected.Code != 200 || json.Unmarshal(rejected.Body.Bytes(), &rejectedResult) != nil || rejectedResult.Data.Success || rejectedResult.Data.Message == "" {
		t.Fatal("unsupported contract installation was not rejected", rejected.Body.String())
	}
	if downloads.Load() != 0 {
		t.Fatal("market discovery downloaded executable")
	}
	var installedCount int
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_installations`).Scan(&installedCount); err != nil || installedCount != 0 {
		t.Fatalf("discovery mutated installations %d %v", installedCount, err)
	}
	body, _ := json.Marshal(map[string]any{"plugin_id": manifest.PluginID, "repo_url": server.URL + "/good"})
	r = callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	r = callTest(t, a.Handler(), "PUT", "/api/plugins/"+manifest.PluginID, `{"enabled":true,"marker":"loaded-state"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	card, ok = find(readCards("/api/plugins?state=installed"), manifest.PluginID)
	if !ok || !card.Installed || !card.State || card.RepositoryURL != "" || card.Version != "1.0" {
		t.Fatalf("actual installed card wrong: %+v", card)
	}
	// Changing stored config alone must not change already-loaded get_state.
	_, err = a.pluginStore.Config(context.Background(), manifest.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	retainedStoreSaveConfig(t, a, manifest.PluginID, map[string]any{"enabled": false})
	card, ok = find(readCards("/api/plugins?state=installed"), manifest.PluginID)
	if !ok || !card.State {
		t.Fatal("card guessed state from current DB instead of actual object")
	}
	if downloads.Load() != 1 {
		t.Fatalf("unexpected payload downloads: %d", downloads.Load())
	}
	a.closeOriginalSessions()
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a = nil
	a, err = New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	// No App.Start: reopen validates durable source/receipt data, not business startup.
	if !reflect.DeepEqual(a.Settings().PluginMarkets, second) {
		t.Fatal("sources lost on real reopen")
	}
	receipt, err := artifacts.ReadInstalled(a.pluginPackageRoot(), manifest.PluginID, validatePluginArtifactContract)
	if err != nil || receipt.RepositoryURL != server.URL+"/good" || receipt.Manifest.Version != "1.0" {
		t.Fatalf("artifact origin lost on reopen: %v", err)
	}
	in, err := a.pluginStore.Installation(context.Background(), manifest.PluginID)
	if err != nil || !in.Installed || in.Version != "1.0" {
		t.Fatalf("unsupported discovery altered installation: %+v %v", in, err)
	}
	if downloads.Load() != 1 {
		t.Fatal("reopen downloaded payload")
	}
}
