package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/networkproxy"
	"light-media/internal/plugins/artifacts"
)

// One real A consumer chain. It does not test B, a normal server process,
// public GitHub, a distinct-version update, or any UI. The inherited old center
// isolation is explicit; proxy/env/TLS policy is never rewritten by this root.
func TestOriginalRequestUtilsOwnedTLSInstallRecoveryAndCold(t *testing.T) {
	ctx := context.Background()
	index, binary := originalMarketOwnedIndex(t, "OWNED_STANDARD_FIXTURE_BINARY")
	if index.PluginID != ownedStandardFixtureID || index.CurrentVersion != "1.0" {
		t.Fatal("requires the bound, already compiled ordinary owned standard worker")
	}
	index.History = map[string]string{"1.0": "owned actual release"}
	var indexes, payloads, unexpected atomic.Int32
	var failPayload atomic.Bool
	failPayload.Store(true)
	peer := originalRequestUtilsOwnedTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.IsAbs() || r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" {
			unexpected.Add(1)
			http.Error(w, "fixed owned request required", 403)
			return
		}
		for key := range r.URL.Query() {
			if key != "_refresh" || r.URL.Path != "/index" {
				unexpected.Add(1)
				http.Error(w, "unknown owned query", 403)
				return
			}
		}
		switch r.URL.Path {
		case "/index":
			indexes.Add(1)
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			payloads.Add(1)
			if failPayload.Load() {
				http.Error(w, "owned-artifact-failure-private-marker", 503)
				return
			}
			_, _ = w.Write(binary)
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
		}
	}))
	index.Releases[0].Artifacts[0].URL = peer.URL + "/worker"
	// The production ordinary environment route must already select these
	// owned destinations. No request is sent if that condition does not hold.
	for _, path := range []string{"/index", "/worker"} {
		r, _ := http.NewRequest(http.MethodGet, peer.URL+path, nil)
		proxy, err := networkproxy.RequestsProxy(r)
		if err != nil || proxy != nil {
			t.Fatal("owned A endpoint unavailable on inherited route; no alternate route")
		}
	}
	dir := t.TempDir()
	a, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if a != nil {
			_ = a.Close()
		}
	})
	disableResetFixtureStatistics(t, a) // old PluginStatisticShare/PluginServerURL only; not a B result
	cookie := setupTestAdmin(t, a)
	settings := a.Settings()
	if settings.ProxyHost != "" {
		t.Fatal("fresh app unexpectedly supplies explicit proxy")
	}
	settings.PluginMarkets = []string{peer.URL + "/index"}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	checkCounts := func(label string, wantIndex, wantPayload int32) {
		t.Helper()
		a.wg.Wait()
		if indexes.Load() != wantIndex || payloads.Load() != wantPayload || unexpected.Load() != 0 {
			t.Fatalf("%s owned counts index=%d payload=%d unexpected=%d", label, indexes.Load(), payloads.Load(), unexpected.Load())
		}
		t.Logf("owned_phase=%s index=%d payload=%d", label, wantIndex, wantPayload)
	}
	market := callTest(t, a.Handler(), "GET", "/api/plugins/market?details=true&force=true", "", cookie, "")
	if market.Code != 200 || !strings.Contains(market.Body.String(), `"plugin_id":"`+index.PluginID+`"`) {
		t.Fatal("actual HTTPS market metadata missing", market.Code)
	}
	checkCounts("market", 1, 0)
	install := func(force bool, success bool) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"plugin_id": index.PluginID, "repo_url": peer.URL + "/index", "force": force})
		response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(raw), cookie, "")
		var envelope struct {
			Data struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			} `json:"data"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Data.Success != success {
			t.Fatal("actual install result differs", response.Code, success)
		}
		if strings.Contains(response.Body.String(), "owned-artifact-failure-private-marker") {
			t.Fatal("remote failure body leaked")
		}
	}
	install(false, false)
	checkCounts("failed_acquisition_no_replay", 2, 1)
	if _, ok := a.pluginRunningInstances.Load(index.PluginID); ok {
		t.Fatal("failed artifact started a worker")
	}
	if _, err := a.pluginStore.Installation(ctx, index.PluginID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("failed artifact published installation", err)
	}
	failPayload.Store(false)
	install(false, true)
	checkCounts("explicit_recovery", 3, 2)
	read := func() ownedStandardVersionSnapshot {
		t.Helper()
		value, ok := a.pluginRunningInstances.Load(index.PluginID)
		if !ok {
			t.Fatal("actual installed worker missing")
		}
		out := ownedStandardVersionRead(t, value.(*originalPluginInstance))
		if out.Version != "1.0" || out.Nonce <= 0 {
			t.Fatal("actual worker identity missing")
		}
		return out
	}
	first := read()
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+index.PluginID, `{"enabled":false,"key":"owned-a-persisted"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	before, err := a.pluginStore.Config(ctx, index.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	// Already-present class takes the second A call site: InspectCompatible,
	// not payload acquisition. Reload still creates an actual new object.
	install(false, true)
	checkCounts("existing_class_compatibility", 4, 2)
	second := read()
	if second.Nonce == first.Nonce {
		t.Fatal("existing class reinstall did not reload actual worker")
	}
	// Exercise the real force/update publication branch with identical bound
	// executable bytes; do not relabel this as a distinct-version upgrade.
	install(true, true)
	checkCounts("same_version_force_reinstall", 5, 3)
	third := read()
	if third.Nonce == second.Nonce {
		t.Fatal("force reinstall retained old worker")
	}
	after, err := a.pluginStore.Config(ctx, index.PluginID)
	if err != nil || !reflect.DeepEqual(before.Value, after.Value) {
		t.Fatal("reinstall lost actual stored config", err)
	}
	installed, err := artifacts.ReadInstalled(a.pluginPackageRoot(), index.PluginID, validatePluginArtifactContract)
	if err != nil || installed.SHA256 != index.Releases[0].Artifacts[0].ExecutableSHA256 {
		t.Fatal("installed bytes differ from bound worker", err)
	}
	for i, path := range []string{
		"/api/plugins/history/" + index.PluginID + "?force=true",
		"/api/plugins/releases/" + index.PluginID + "?repo_url=" + url.QueryEscape(peer.URL+"/index"),
	} {
		response := callTest(t, a.Handler(), "GET", path, "", cookie, "")
		var got struct {
			Data originalPluginHistory `json:"data"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &got) != nil || got.Data.CurrentVersion != "1.0" || got.Data.LatestVersion != "1.0" || got.Data.Source == nil || !got.Data.Source.Success {
			t.Fatal("actual HTTPS history/release missing", response.Code)
		}
		if i == 0 && got.Data.History["1.0"] != "owned actual release" {
			t.Fatal("history lost owned authored field")
		}
		if i == 1 && len(got.Data.Items) != 1 {
			t.Fatal("release did not expose exact bound artifact")
		}
		checkCounts("history_release", int32(6+i), 3)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = nil
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	a = reopened
	if a.Settings().PluginServerURL == nil || *a.Settings().PluginServerURL != "" || originalPluginStatisticShared(a.Settings()) {
		t.Fatal("old center isolation did not persist")
	}
	if err := a.initializeOriginalPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	cold := read()
	if cold.Nonce == third.Nonce || !reflect.DeepEqual(cold.Config, third.Config) {
		t.Fatal("cold actual process/config differs")
	}
	checkCounts("cold_load_without_artifact_fetch", 7, 3)
	// The real startup synchronizer reads HTTPS market material, then finds
	// the already-current installed implementation; no fake missing package.
	a.synchronizeOriginalPluginsAtStartup(ctx)
	checkCounts("cold_startup_sync_current", 8, 3)
	if read().Nonce != cold.Nonce {
		t.Fatal("current-package startup sync replaced worker")
	}
	t.Logf("actual_worker_pids initial=%d compatible=%d force=%d cold=%d; bound_version=1.0", first.Nonce, second.Nonce, third.Nonce, cold.Nonce)
}
