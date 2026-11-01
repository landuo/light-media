package app

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/networkproxy"
)

//go:embed testdata/market-display-body-source.json
var ownedLargeDisplaySource []byte

type ownedLargeDisplayCase struct {
	ID          string `json:"id"`
	InputRecipe struct {
		Prefix, Repeat, Suffix string
		Count                  int
	} `json:"input_recipe"`
	InputBytes   int    `json:"input_utf8_bytes"`
	InputSHA     string `json:"input_sha256"`
	OutcomeKind  string `json:"outcome_kind"`
	PythonType   string `json:"python_type"`
	SourceFields map[string]struct {
		Name, Version string
		History       map[string]string
	} `json:"source_fields"`
}

func ownedLargeDisplayInputs(t *testing.T) (ownedLargeDisplayCase, string, string) {
	t.Helper()
	var oracle struct {
		SourceSHA string                  `json:"source_raw_sha256"`
		Cases     []ownedLargeDisplayCase `json:"cases"`
	}
	if json.Unmarshal(ownedLargeDisplaySource, &oracle) != nil || oracle.SourceSHA != "04b5ec00d1faa5af6be4febd702762038d98dd690c0c117581bed07160cfd69f" || len(oracle.Cases) != 2 {
		t.Fatal("missing independently captured source projection")
	}
	var selected ownedLargeDisplayCase
	var valid, invalid string
	for _, sample := range oracle.Cases {
		in := sample.InputRecipe
		if in.Count != 2<<20 || in.Repeat != "x" {
			t.Fatal("changed bounded owned source recipe")
		}
		payload := in.Prefix + strings.Repeat(in.Repeat, in.Count) + in.Suffix
		hash := sha256.Sum256([]byte(payload))
		if len(payload) != sample.InputBytes || hex.EncodeToString(hash[:]) != sample.InputSHA {
			t.Fatal("owned bytes differ from actual source input")
		}
		switch sample.ID {
		case "large_padding":
			if sample.OutcomeKind != "return" || sample.PythonType != "dict" || len(sample.SourceFields) != 1 {
				t.Fatal("unexpected original valid observation")
			}
			selected, valid = sample, payload
		case "large_malformed":
			if sample.OutcomeKind != "return" || sample.PythonType != "NoneType" {
				t.Fatal("unexpected original malformed observation")
			}
			invalid = payload
		default:
			t.Fatal("unselected source observation")
		}
	}
	if valid == "" || invalid == "" {
		t.Fatal("incomplete actual source inputs")
	}
	return selected, valid, invalid
}

// Real isolated App/management Handler and owned TCP4 TLS upstream. The imported
// local class is the same explicit synthetic boundary as the old guide test;
// this root does not install/run a worker or claim ordinary cmd/server evidence.
func TestOriginalLargePythonPackageHTTPAndCacheRecovery(t *testing.T) {
	observed, large, malformed := ownedLargeDisplayInputs(t)
	const id = "OwnedLarge"
	const repository = "https://github.com/Owner/Repo"
	expected, ok := observed.SourceFields[id]
	if !ok || expected.Version == "" || expected.History[expected.Version] == "" {
		t.Fatal("missing original source fields")
	}
	var mode, nativeReads, higherReads, baseReads, unexpected atomic.Int32
	peer := originalRequestUtilsOwnedTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.IsAbs() || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
			unexpected.Add(1)
			http.Error(w, "fixed owned request required", 400)
			return
		}
		for key := range r.URL.Query() {
			if key != "_refresh" {
				unexpected.Add(1)
				http.Error(w, "fixed owned query required", 400)
				return
			}
		}
		const prefix = "/mirror/https://raw.githubusercontent.com/Owner/Repo/main/"
		var payload string
		switch r.URL.Path {
		case prefix + "go-plugins.v1.json":
			nativeReads.Add(1)
			http.NotFound(w, r)
			return
		case prefix + "package.v2.json":
			higherReads.Add(1)
			switch mode.Load() {
			case 0:
				payload = large
			case 1:
				payload = malformed
			case 2:
				http.Error(w, "owned-private-response-marker", 503)
				return
			default:
				unexpected.Add(1)
				http.Error(w, "invalid owned mode", 400)
				return
			}
		case prefix + "package.json":
			baseReads.Add(1)
			payload = `{"OwnedLarge":{"name":"owned lower candidate","version":"1.0","v2":true,"history":{"1.0":"lower"}}}`
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write([]byte(payload))
	}))
	// Fail before dispatch rather than changing inherited proxy/CA routing.
	request, _ := http.NewRequest(http.MethodGet, peer.URL+"/mirror/https://raw.githubusercontent.com/Owner/Repo/main/package.v2.json", nil)
	if proxy, err := networkproxy.RequestsProxy(request); err != nil || proxy != nil {
		t.Fatal("owned mirror unavailable on inherited route", err)
	}
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
	cookie := setupTestAdmin(t, a)
	settings := a.Settings()
	if settings.ProxyHost != "" || settings.GitHubToken != "" || settings.RepoGitHubToken != "" || a.pluginMarketOverride != nil {
		t.Fatal("fresh owned app has unexpected outbound configuration")
	}
	settings.PluginMarkets, settings.GitHubProxy = []string{repository}, peer.URL+"/mirror"
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	// Synthetic imported-class boundary only, never executable acquisition.
	manifest := guideMarketIndex(id, peer.URL+"/must-not-fetch-worker").Releases[1].Manifest
	manifest.Version = "0.5" // Both real source bands remain visible update candidates.
	raw, _ := json.Marshal(manifest)
	if err := a.pluginStore.Install(context.Background(), PluginInstallation{InstanceID: id, PluginID: id, Version: "0.5", ManifestSchemaVersion: 1, Manifest: raw}); err != nil {
		t.Fatal(err)
	}
	installed, err := a.pluginStore.Installation(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	a.storeOriginalPluginClass(id, originalPluginClass{installation: installed})
	checkCounts := func(label string, native, higher, base int32) {
		t.Helper()
		if nativeReads.Load() != native || higherReads.Load() != higher || baseReads.Load() != base || unexpected.Load() != 0 {
			t.Fatalf("%s native=%d higher=%d base=%d unexpected=%d", label, nativeReads.Load(), higherReads.Load(), baseReads.Load(), unexpected.Load())
		}
		t.Logf("owned_phase=%s native=%d higher=%d base=%d", label, native, higher, base)
	}
	checkCards := func(items []pluginCard, version string) {
		t.Helper()
		found := 0
		for _, item := range items {
			if item.PluginID == id {
				found++
				if item.RepositoryVersion != version || item.RepositoryURL != repository || version == expected.Version && item.Name != expected.Name {
					t.Fatalf("source candidate not selected: %+v", item)
				}
			}
		}
		if found != 1 {
			t.Fatal("large metadata card missing or duplicated", found)
		}
	}
	market := func(query, version string, success bool) {
		t.Helper()
		response := callTest(t, a.Handler(), "GET", "/api/plugins/market?details=true"+query, "", cookie, "")
		var envelope struct {
			Data struct {
				Items   []pluginCard                 `json:"items"`
				Sources []originalMarketSourceStatus `json:"sources"`
			} `json:"data"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || len(envelope.Data.Sources) != 1 || envelope.Data.Sources[0].Success != success || strings.Contains(response.Body.String(), "owned-private-response-marker") {
			t.Fatal("market result/status/redaction", response.Code, response.Body.String())
		}
		checkCards(envelope.Data.Items, version)
	}
	market("&force=true", expected.Version, true)
	checkCounts("large-initial", 1, 1, 1)
	list := callTest(t, a.Handler(), "GET", "/api/plugins?state=market", "", cookie, "")
	var listing struct{ Data []pluginCard `json:"data"` }
	if list.Code != 200 || json.Unmarshal(list.Body.Bytes(), &listing) != nil {
		t.Fatal("real list route", list.Code)
	}
	checkCards(listing.Data, expected.Version)
	checkCounts("ordinary-list-cached", 1, 1, 1)
	for _, route := range []string{"/api/plugins/history/" + id + "?force=false", "/api/plugins/releases/" + id + "?repo_url=" + url.QueryEscape(repository)} {
		response := callTest(t, a.Handler(), "GET", route, "", cookie, "")
		var history struct{ Data originalPluginHistory `json:"data"` }
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &history) != nil || history.Data.CurrentVersion != "0.5" || history.Data.LatestVersion != expected.Version || history.Data.History[expected.Version] != expected.History[expected.Version] || history.Data.ReleaseSupported || len(history.Data.Items) != 0 {
			t.Fatal("source metadata history/release projection", response.Code, response.Body.String())
		}
	}
	checkCounts("history-release-cached", 1, 1, 1)
	mode.Store(1)
	market("&force=true", "1.0", false)
	checkCounts("large-malformed-fresh", 2, 2, 2)
	market("", expected.Version, true)
	checkCounts("malformed-did-not-erase-cache", 2, 2, 2)
	mode.Store(2)
	market("&force=true", "1.0", false)
	checkCounts("received-503-no-upstream-replay", 3, 3, 3)
	market("", expected.Version, true)
	checkCounts("http-failure-did-not-erase-cache", 3, 3, 3)
	mode.Store(0)
	market("&force=true", expected.Version, true)
	checkCounts("explicit-large-recovery", 4, 4, 4)
	if _, running := a.pluginRunningInstances.Load(id); running {
		t.Fatal("metadata fixture unexpectedly started a worker")
	}
}
