package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins/artifacts"
)

func TestOriginalMarketReleaseForceReturnsCacheThenRefreshesSourceOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-market/cache-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Releases struct {
			Forced   originalPluginHistory `json:"forced_with_existing_cache"`
			NoSource map[string]any        `json:"no_source"`
		} `json:"releases"`
	}
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	var hits atomic.Int32
	var promoted atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	var latest, old artifacts.Index
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index" {
			t.Error("release metadata query downloaded executable bytes")
			http.NotFound(w, r)
			return
		}
		n := hits.Add(1)
		if n == 4 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		index := old
		if promoted.Load() {
			index = latest
		}
		json.NewEncoder(w).Encode(index)
	}))
	defer server.Close()
	latest = guideMarketIndex("Owned", server.URL+"/worker")
	latest.Releases = latest.Releases[:2]
	old = latest
	old.CurrentVersion, old.Releases = "1.0", latest.Releases[1:2]
	manifest := old.Releases[0].Manifest
	manifestRaw, _ := json.Marshal(manifest)
	if err = a.pluginStore.Install(context.Background(), PluginInstallation{InstanceID: "Owned", PluginID: "Owned", Version: "1.0", ManifestSchemaVersion: 1, Manifest: manifestRaw}); err != nil {
		t.Fatal(err)
	}
	// Synthetic imported-class boundary; this test does not start a worker.
	installed, loadErr := a.pluginStore.Installation(context.Background(), "Owned")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	a.storeOriginalPluginClass("Owned", originalPluginClass{installation: installed})
	var empty struct{ Data map[string]any }
	response := callTest(t, a.Handler(), "GET", "/api/plugins/releases/Owned", "", cookie, "")
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &empty) != nil {
		t.Fatal("empty-source release response failed")
	}
	for key, expected := range oracle.Releases.NoSource {
		if !reflect.DeepEqual(empty.Data[key], expected) {
			t.Fatal("no-source wire result differs from pinned endpoint, including null versions", key, empty.Data[key], expected)
		}
	}
	response = callTest(t, a.Handler(), "GET", "/api/plugins/releases/Owned?repo_url="+url.QueryEscape("local://Owned?path=%2Fnot-configured"), "", cookie, "")
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &empty) != nil || empty.Data["release_supported"] != false || empty.Data["current_version"] != "1.0" || empty.Data["latest_version"] != nil || len(empty.Data["items"].([]any)) != 0 {
		t.Fatal("local repository invented remote Release support")
	}
	read := func(force bool) originalPluginHistory {
		t.Helper()
		query := url.Values{"repo_url": []string{server.URL + "/index"}}
		if force {
			query.Set("force", "true")
		}
		response := callTest(t, a.Handler(), "GET", "/api/plugins/releases/Owned?"+query.Encode(), "", cookie, "")
		var got struct{ Data originalPluginHistory }
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &got) != nil {
			t.Fatal("release endpoint failed")
		}
		return got.Data
	}
	first := read(false)
	if len(first.Items) != 1 || first.Items[0].Version != "1.0" || hits.Load() != 2 {
		t.Fatal("initial metadata and Release fetches were not independent", first, hits.Load())
	}
	promoted.Store(true)
	forced := read(true)
	expected := oracle.Releases.Forced
	if forced.ReleaseSupported != expected.ReleaseSupported || forced.LatestVersion != expected.LatestVersion || forced.CurrentVersion != expected.CurrentVersion || !reflect.DeepEqual(forced.Items, expected.Items) {
		t.Fatal("forced Release response differs from actual pinned endpoint oracle", forced, expected)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("forced request failed to schedule an actual background repository fetch")
	}
	close(release)
	a.wg.Wait()
	refreshed := read(false)
	if len(refreshed.Items) != 2 || refreshed.Items[0].Version != "2.0" || !refreshed.Items[0].IsLatest || !refreshed.Items[1].IsCurrent || hits.Load() != 4 {
		t.Fatal("background refresh did not replace the Release list independently", refreshed, hits.Load())
	}
}
