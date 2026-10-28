package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func guideMarketIndex(id, address string) artifacts.Index {
	m := plugins.Manifest{SchemaVersion: 1, PluginID: id, Name: "Owned metadata fixture", Version: "2.0", Runtime: "go-executable", Entry: "bin/worker", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"init", "get_state", "get_service", "get_command", "stop"}, Capabilities: []string{}}
	artifact := artifacts.Artifact{Platform: runtime.GOOS + "-" + runtime.GOARCH, URL: address, Format: "executable", Size: 1, SHA256: strings.Repeat("0", 64), ExecutableSHA256: strings.Repeat("0", 64), HostContract: pluginPureSDKContract}
	latest := artifacts.Release{Version: "2.0", Manifest: m, Artifacts: []artifacts.Artifact{artifact}}
	old := latest
	old.Version, old.Manifest.Version = "1.0", "1.0"
	rejected := latest
	rejected.Version, rejected.Manifest.Version = "0.5", "0.5"
	rejected.Artifacts = []artifacts.Artifact{artifact}
	rejected.Artifacts[0].HostContract = "unknown/v1"
	return artifacts.Index{SchemaVersion: 1, PluginID: id, CurrentVersion: "2.0", Releases: []artifacts.Release{latest, old, rejected}, History: map[string]string{"2.0": "真实作者说明", "0.1": "只有说明，不代表可安装"}}
}

// VersionHistory.vue iterates props.history without sorting. The wire order
// must therefore remain the authored descending order, even through Go maps.
func TestOriginalMarketHistoryPreservesAuthorWireOrder(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	index := guideMarketIndex("OwnedHistoryOrder", "http://127.0.0.1/unused-worker")
	index.History = map[string]string{"2.0": "new", "1.0": "old", "0.1": "notes only"}
	index.HistoryJSON = json.RawMessage(`{"2.0":"new","1.0":"old","0.1":"notes only"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(index) }))
	defer server.Close()
	settings := a.Settings()
	settings.PluginMarkets = []string{server.URL}
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(index.Releases[0].Manifest)
	if err := a.pluginStore.Install(context.Background(), PluginInstallation{InstanceID: index.PluginID, PluginID: index.PluginID, Version: "2.0", ManifestSchemaVersion: 1, Manifest: raw}); err != nil {
		t.Fatal(err)
	}
	// Synthetic imported-class boundary; this test does not start a worker.
	installed, loadErr := a.pluginStore.Installation(context.Background(), index.PluginID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	a.storeOriginalPluginClass(index.PluginID, originalPluginClass{installation: installed})
	response := callTest(t, a.Handler(), "GET", "/api/plugins/history/"+index.PluginID, "", cookie, "")
	body := response.Body.String()
	if response.Code != 200 || !strings.Contains(body, `"history":{"2.0":"new","1.0":"old","0.1":"notes only"}`) {
		t.Fatal("HTTP history did not preserve the source widget's author order", body)
	}
}

func TestOriginalMarketWikiExtractionPinnedSourceOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-market/wiki-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Document     string   `json:"document"`
			Repositories []string `json:"repositories"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	for _, test := range oracle.Cases {
		if got := extractOriginalWikiRepositories(test.Document); !reflect.DeepEqual(got, test.Repositories) {
			t.Errorf("fixed Wiki marker/fallback/canonicalization output differs: got %v, want %v", got, test.Repositories)
		}
	}
}

func TestOriginalMarketGuideHistoryReleasesMetadataOnlyAndFailures(t *testing.T) {
	a, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	cookie := setupTestAdmin(t, a)
	var requests, payloads atomic.Int32
	var fail atomic.Bool
	var index artifacts.Index
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index" {
			payloads.Add(1)
			t.Error("history downloaded executable content")
			http.NotFound(w, r)
			return
		}
		requests.Add(1)
		if fail.Load() {
			http.Error(w, "owned failure", 503)
			return
		}
		json.NewEncoder(w).Encode(index)
	}))
	defer server.Close()
	index = guideMarketIndex("OwnedGuideHistory", server.URL+"/binary")
	manifest := index.Releases[1].Manifest
	raw, _ := json.Marshal(manifest)
	if err = a.pluginStore.Install(context.Background(), PluginInstallation{InstanceID: manifest.PluginID, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: 1, Manifest: raw}); err != nil {
		t.Fatal(err)
	}
	// Synthetic imported-class boundary; this test does not start a worker.
	installed, loadErr := a.pluginStore.Installation(context.Background(), manifest.PluginID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	a.storeOriginalPluginClass(manifest.PluginID, originalPluginClass{installation: installed})
	settings := a.Settings()
	settings.PluginMarkets = []string{server.URL + "/index"}
	if err = a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	h := a.Handler()
	// Fixed plugin_history has no repo_url parameter. An unrelated override
	// must not redirect the history read away from configured exact-ID sources.
	endpoint := "/api/plugins/history/" + manifest.PluginID + "?repo_url=" + url.QueryEscape(server.URL+"/untrusted-override")
	read := func(path string) originalPluginHistory {
		t.Helper()
		res := callTest(t, h, "GET", path, "", cookie, "")
		var body struct {
			Data originalPluginHistory `json:"data"`
		}
		if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &body) != nil {
			t.Fatalf("history: %d %s", res.Code, res.Body.String())
		}
		return body.Data
	}
	got := read(endpoint)
	if got.CurrentVersion != "1.0" || got.LatestVersion != "2.0" || got.RepositoryURL != server.URL+"/index" || got.History["2.0"] != index.History["2.0"] || len(got.Items) != 0 || len(got.Rejected) != 0 {
		t.Fatalf("actual metadata lost: %+v", got)
	}
	if requests.Load() != 1 || payloads.Load() != 0 {
		t.Fatal("unexpected requests")
	}
	read(endpoint + "&force=false")
	if requests.Load() != 1 {
		t.Fatal("cache bypassed")
	}
	releases := read("/api/plugins/releases/" + manifest.PluginID + "?repo_url=" + url.QueryEscape(server.URL+"/index"))
	if len(releases.Items) != 2 || len(releases.Rejected) != 1 || !releases.Items[0].IsLatest || !releases.Items[1].IsCurrent || requests.Load() != 2 {
		t.Fatal("downloadable releases were confused with author history", releases)
	}
	fail.Store(true)
	got = read(endpoint)
	if got.Source == nil || got.Source.Success || len(got.History) != 0 || got.CurrentVersion != "1.0" || got.LatestVersion != "" || len(got.Items) != 0 {
		t.Fatal("history failure must return installed information, not stale market notes", got)
	}
	got = read(endpoint + "&force=false")
	if got.History["2.0"] == "" || !got.Source.Success || !got.Source.Cached || requests.Load() != 3 {
		t.Fatal("failed fresh history erased the successful ordinary-read cache", got)
	}
	if r := callTest(t, h, "GET", "/api/plugins/history/Other", "", cookie, ""); r.Code != 404 {
		t.Fatal("uninstalled history accepted")
	}
	if r := callTest(t, h, "GET", endpoint, "", nil, ""); r.Code != 401 {
		t.Fatal("anonymous history accepted")
	}
	if r := callTest(t, h, "GET", endpoint+"&force=bad", "", cookie, ""); r.Code == 200 {
		t.Fatal("invalid force accepted")
	}
	got = read("/api/plugins/releases/" + manifest.PluginID)
	if got.ReleaseSupported || len(got.Items) != 0 || got.CurrentVersion != "" || got.LatestVersion != "" {
		t.Fatal("invented releases without a repository")
	}
	got = read("/api/plugins/releases/" + manifest.PluginID + "?force=true&repo_url=" + url.QueryEscape(server.URL+"/index"))
	if got.ReleaseSupported || len(got.Items) != 0 || got.CurrentVersion != "1.0" || got.LatestVersion != "" || got.Source.Success {
		t.Fatal("failed release lookup fabricated installable versions", got)
	}
	fail.Store(false)
	got = read("/api/plugins/releases/Other?force=true&repo_url=" + url.QueryEscape(server.URL+"/index"))
	if got.PluginID != "Other" || got.LatestVersion != "" || len(got.Items) != 0 || len(got.History) != 0 {
		t.Fatal("source ID mismatch substituted sibling metadata", got)
	}
	if payloads.Load() != 0 {
		t.Fatal("metadata read changed code")
	}
}

type guideMarketTransport func(*http.Request) (*http.Response, error)

func (f guideMarketTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOriginalMarketGuideWikiFixedSourceActualHTTPAndMerge(t *testing.T) {
	markdown := "https://github.com/Outside/Ignore\n<!-- plugin-market-repos:start -->\nhttps://github.com/Owner/A.git\nhttps://github.com/owner/a/\nhttps://github.com/Owner/B\n<!-- plugin-market-repos:end -->"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, markdown)
	}))
	defer server.Close()
	local, _ := url.Parse(server.URL)
	client := &http.Client{Transport: guideMarketTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != originalPluginMarketWikiURL {
			t.Error("unexpected source")
		}
		copy := r.Clone(r.Context())
		value := *r.URL
		value.Scheme, value.Host = local.Scheme, local.Host
		copy.URL, copy.Host = &value, local.Host
		return server.Client().Transport.RoundTrip(copy)
	})}
	repos, err := fetchOriginalMarketWiki(context.Background(), originalPluginMarketWikiURL, client)
	if err != nil || !reflect.DeepEqual(repos, []string{"https://github.com/Owner/A", "https://github.com/Owner/B"}) {
		t.Fatalf("wiki parse: %v %v", repos, err)
	}
	if _, err = fetchOriginalMarketWiki(context.Background(), "https://other.invalid/wiki", client); err == nil || calls.Load() != 1 {
		t.Fatal("arbitrary source fetched")
	}
	markdown = "no repositories"
	if _, err = fetchOriginalMarketWiki(context.Background(), originalPluginMarketWikiURL, client); err == nil {
		t.Fatal("empty response succeeded")
	}
	a, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	settings := a.Settings()
	settings.PluginMarkets = []string{"https://owned.invalid/go/index.json", "https://github.com/owner/a/"}
	settings.PluginAutoReload = true
	if err = a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	result, err := a.saveOriginalWikiRepositories(context.Background(), repos)
	if err != nil || result["added_count"] != 1 || !a.Settings().PluginAutoReload || !reflect.DeepEqual(a.Settings().PluginMarkets, []string{"https://owned.invalid/go/index.json", "https://github.com/owner/a/", "https://github.com/Owner/B"}) {
		t.Fatalf("merge changed existing settings: %+v %v", result, err)
	}
	cookie := setupTestAdmin(t, a)
	res := callTest(t, a.Handler(), "POST", "/api/plugins/markets/sync-wiki", `{"wiki_url":"https://other.invalid/wiki"}`, cookie, "")
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"success":false`) || len(a.Settings().PluginMarkets) != 3 {
		t.Fatal("invalid source changed saved repositories")
	}
}

func TestOriginalMarketGuideConcurrentFetchKeepsConfiguredOrder(t *testing.T) {
	a, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	entered, release := make(chan string, 3), make(chan struct{})
	var index artifacts.Index
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- r.URL.Path
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		json.NewEncoder(w).Encode(index)
	}))
	defer server.Close()
	index = guideMarketIndex("OwnedParallelGuide", server.URL+"/binary")
	settings := a.Settings()
	settings.PluginMarkets = []string{server.URL + "/first", server.URL + "/second", server.URL + "/third"}
	if err = a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	done := make(chan []artifacts.Discovery, 1)
	go func() { values, _ := a.originalMarketDeclarations(context.Background(), true); done <- values }()
	for i := 0; i < 3; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			close(release)
			t.Fatal("repository fetching is sequential")
		}
	}
	close(release)
	values := <-done
	if len(values) != 1 || values[0].RepositoryURL != settings.PluginMarkets[0] {
		t.Fatal("completion order changed source preference")
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { a.runOriginalMarketRefresh(ctx); close(stopped) }()
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("scheduled refresh ignored shutdown")
	}
}
