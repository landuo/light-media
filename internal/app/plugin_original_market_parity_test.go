package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func TestOriginalMarketVersionAndSourceOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-market/market-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	type source struct {
		ID, Version, Repository string
	}
	var oracle struct {
		Comparisons []struct {
			Left, Right string
			Comparison  int
		}
		Merges []struct{ Sources, Selected []source }
	}
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	for _, comparison := range oracle.Comparisons {
		if actual := compareOriginalPluginVersion(comparison.Left, comparison.Right); actual != comparison.Comparison {
			t.Errorf("original comparison %q vs %q: got %d, want %d", comparison.Left, comparison.Right, actual, comparison.Comparison)
		}
	}
	for _, merge := range oracle.Merges {
		var candidates []artifacts.Discovery
		for _, row := range merge.Sources {
			candidates = append(candidates, artifacts.Discovery{PluginID: row.ID, CurrentVersion: row.Version, RepositoryURL: row.Repository})
		}
		var actual []source
		for _, row := range selectOriginalMarketDeclarations(candidates) {
			actual = append(actual, source{row.PluginID, row.CurrentVersion, row.RepositoryURL})
		}
		if !reflect.DeepEqual(actual, merge.Selected) {
			t.Errorf("original source selection: got %+v, want %+v", actual, merge.Selected)
		}
	}
}

func originalMarketOwnedIndex(t *testing.T, environment string) (artifacts.Index, []byte) {
	t.Helper()
	binary := os.Getenv(environment)
	if binary == "" {
		t.Fatalf("%s must name an actual compiled fixture", environment)
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, binary, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	return artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(payload)), HostContract: pluginBaseSDKContract}}}}}, payload
}

func TestOriginalMarketHTTPHighestVersionCacheRefreshAndActualUpgrade(t *testing.T) {
	a, cookie, _ := ownedStandardSDKFixture(t)
	v1, bytes1 := originalMarketOwnedIndex(t, "OWNED_STANDARD_FIXTURE_BINARY")
	v2, bytes2 := originalMarketOwnedIndex(t, "OWNED_STANDARD_FIXTURE_V2_BINARY")
	if v1.CurrentVersion != "1.0" || v2.CurrentVersion != "2.0" || v1.PluginID != v2.PluginID {
		t.Fatal("requires genuinely distinct 1.0/2.0 workers")
	}
	var requests, payloads, cacheBusters atomic.Int32
	var failing atomic.Bool
	failing.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("_refresh") != "" {
			cacheBusters.Add(1)
		}
		switch r.URL.Path {
		case "/old":
			requests.Add(1)
			_ = json.NewEncoder(w).Encode(v1)
		case "/new", "/equal":
			requests.Add(1)
			_ = json.NewEncoder(w).Encode(v2)
		case "/failure":
			requests.Add(1)
			if failing.Load() {
				http.Error(w, "private-response-body-must-not-escape", http.StatusServiceUnavailable)
			} else {
				_ = json.NewEncoder(w).Encode(v1)
			}
		case "/worker1":
			payloads.Add(1)
			_, _ = w.Write(bytes1)
		case "/worker2":
			payloads.Add(1)
			_, _ = w.Write(bytes2)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	v1.Releases[0].Artifacts[0].URL = server.URL + "/worker1"
	v2.Releases[0].Artifacts[0].URL = server.URL + "/worker2"
	setSources := func(paths ...string) {
		t.Helper()
		sources := make([]string, len(paths))
		for i, path := range paths {
			sources[i] = server.URL + path
		}
		body, _ := json.Marshal(map[string]any{"plugin_markets": sources})
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/markets", string(body), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	}
	type detailed struct {
		Items   []pluginCard                 `json:"items"`
		Sources []originalMarketSourceStatus `json:"sources"`
	}
	read := func(query string) detailed {
		t.Helper()
		r := callTest(t, a.Handler(), "GET", "/api/plugins/market?details=true"+query, "", cookie, "")
		var envelope struct{ Data detailed }
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &envelope) != nil {
			t.Fatalf("market HTTP: %d %s", r.Code, r.Body.String())
		}
		if strings.Contains(r.Body.String(), "private-response") {
			t.Fatal("remote response leaked in public diagnostics")
		}
		return envelope.Data
	}
	find := func(cards []pluginCard) (pluginCard, bool) {
		for _, card := range cards {
			if card.InstanceID == v1.PluginID {
				return card, true
			}
		}
		return pluginCard{}, false
	}
	setSources("/old", "/new", "/equal", "/failure")
	first := read("")
	card, ok := find(first.Items)
	if !ok || !card.Installed || !card.HasUpdate || card.RepositoryURL != server.URL+"/new" || card.RepositoryVersion != "2.0" {
		t.Fatalf("highest candidate/source was not offered for actual installed worker: %+v", card)
	}
	if len(first.Sources) != 4 || first.Sources[3].Success || first.Sources[3].Message == "" || requests.Load() != 4 || payloads.Load() != 0 {
		t.Fatal("missing partial failure or discovery had side effects", first.Sources, requests.Load(), payloads.Load())
	}
	second := read("")
	if requests.Load() != 5 || !second.Sources[0].Cached || second.Sources[3].Cached {
		t.Fatal("successful TTL or failed-read skip_none behavior diverged")
	}
	// Source reordering takes effect even while the per-URL metadata is cached.
	setSources("/equal", "/new", "/old", "/failure")
	card, ok = find(read("").Items)
	if !ok || card.RepositoryURL != server.URL+"/equal" || requests.Load() != 6 {
		t.Fatal("equal-version configured source order lost", card, requests.Load())
	}
	failing.Store(false)
	refreshed := read("&force=true")
	if requests.Load() != 10 || cacheBusters.Load() != 4 || !refreshed.Sources[3].Success || refreshed.Sources[3].Cached {
		t.Fatal("explicit refresh did not recover failed source", requests.Load(), refreshed.Sources)
	}
	failing.Store(true)
	failed := read("&force=true")
	if requests.Load() != 14 || failed.Sources[3].Success || failed.Sources[3].Cached || failed.Sources[3].Message == "" {
		t.Fatal("fresh failure substituted cached declarations", failed.Sources)
	}
	if polled := read(""); requests.Load() != 14 || !polled.Sources[3].Success || !polled.Sources[3].Cached || polled.Sources[3].CheckedAt != refreshed.Sources[3].CheckedAt {
		t.Fatal("fresh failure changed the prior successful TTL entry")
	}
	// The selected source actually supplies the distinct worker, not just a
	// changed card version. Artifact acquisition remains outside metadata cache.
	body, _ := json.Marshal(map[string]any{"plugin_id": card.PluginID, "repo_url": card.RepositoryURL, "release_version": card.RepositoryVersion, "force": true})
	r := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	installed, err := artifacts.ReadInstalled(a.pluginPackageRoot(), card.PluginID, validatePluginArtifactContract)
	if err != nil || installed.Manifest.Version != "2.0" || payloads.Load() != 1 {
		t.Fatal("actual selected artifact upgrade failed", installed.Manifest.Version, err, payloads.Load())
	}
	setSources("/old")
	if card, exists := find(read("").Items); exists {
		t.Fatal("older market version advertised as update", card)
	}
	installedCards, err := a.originalLocalPluginCards(context.Background(), true)
	card, ok = find(installedCards)
	if err != nil || !ok || card.Version != "2.0" || card.HasUpdate {
		t.Fatal("installed card incorrectly advertises downgrade", card, err)
	}
	// Expiration requests fresh metadata; it must not relabel the prior cache
	// entry as a fresh successful fetch.
	a.pluginOriginalMarkets.mu.Lock()
	entry := a.pluginOriginalMarkets.entries[server.URL+"/old"]
	entry.status.CheckedAt = time.Now().Add(-originalMarketTTL - time.Second)
	a.pluginOriginalMarkets.entries[server.URL+"/old"] = entry
	a.pluginOriginalMarkets.mu.Unlock()
	before := requests.Load()
	expired := read("")
	if requests.Load() != before+1 || expired.Sources[0].Cached {
		t.Fatal("expired metadata was not refreshed")
	}
	r = callTest(t, a.Handler(), "GET", "/api/plugins/market?force=invalid", "", cookie, "")
	if r.Code < 400 {
		t.Fatal("invalid force silently accepted")
	}
}

func TestOriginalMarketConcurrentRefreshJoinsAndWaiterCancellation(t *testing.T) {
	a := newTestApp(t)
	index, _ := originalMarketOwnedIndex(t, "OWNED_STANDARD_FIXTURE_BINARY")
	started, finish := make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(started)
		}
		<-finish
		_ = json.NewEncoder(w).Encode(index)
	}))
	defer server.Close()
	index.Releases[0].Artifacts[0].URL = server.URL + "/unused-worker"
	first := make(chan originalMarketResult, 1)
	go func() { first <- a.originalMarketSource(context.Background(), server.URL, true) }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := a.originalMarketSource(ctx, server.URL, true); result.status.Success {
		t.Fatal("cancelled waiter reported discovery success")
	}
	close(finish)
	if result := <-first; !result.status.Success {
		t.Fatal("waiter cancellation canceled the original fetch", result.status)
	}
	if result := a.originalMarketSource(context.Background(), server.URL, false); !result.status.Success || !result.status.Cached || requests.Load() != 1 {
		t.Fatal("pending read was duplicated or lost", result.status, requests.Load())
	}
}
