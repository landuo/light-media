package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"light-media/internal/plugins/artifacts"
)

func TestMergeSiteSwitchInstalledAMD64FormAndEvents(t *testing.T) {
	bundle := os.Getenv("SOURCE_MERGE_SITE_SWITCH_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the owned Linux amd64 plugin packages")
	}
	t.Setenv("MP_SERVER_HOST", "")
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	a.Start(t.Context())
	handler := a.Handler()
	for _, id := range []string{"AutoSignIn", "SiteStatistic", "IYUUAutoSeed", "MergeSiteSwitch"} {
		installMergeSiteSwitchPackage(t, a, cookie, filepath.Join(bundle, id), id)
	}
	const path = "/api/plugins/MergeSiteSwitch"
	setSite := func(id, name, address string, priority int, enabled bool) int64 {
		t.Helper()
		payload := map[string]any{"id": id, "name": name, "base_url": address, "pri": priority, "enabled": enabled}
		response := callTest(t, handler, http.MethodPost, "/api/sites/manage", string(mustCleanSeedJSON(payload)), cookie, "")
		if response.Code != http.StatusOK {
			t.Fatalf("site %s save failed: %d %s", id, response.Code, response.Body.String())
		}
		identity, err := a.Store.nativeSiteByAppID(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		value, err := a.Store.nativeSourcePublicSiteID(t.Context(), a.Store.DB, identity)
		if err != nil || value <= 0 {
			t.Fatalf("site %s missing source identity: %d %v", id, value, err)
		}
		return value
	}
	alpha := setSite("owned-alpha", "聚合甲", "https://owned-alpha.example.org", 20, true)
	beta := setSite("owned-beta", "聚合乙", "https://owned-beta.example.net", 10, true)
	response := callTest(t, handler, http.MethodGet, path+"/form", "", cookie, "")
	var form struct {
		Data struct {
			Mode  string `json:"render_mode"`
			Conf  json.RawMessage
			Model map[string]json.RawMessage
		}
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &form) != nil || form.Data.Mode != "vuetify" {
		t.Fatalf("source-native form did not open: %d %s", response.Code, response.Body.String())
	}
	for _, field := range []string{"enable_sites", "search_sites", "rss_sites", "signin_sites", "login_sites", "statistic_sites", "iyuu_seed_sites"} {
		if _, ok := form.Data.Model[field]; !ok {
			t.Fatal("opening the form did not project", field)
		}
	}
	for _, absent := range []string{"brush_flow_sites", "cross_seed_sites"} {
		if _, ok := form.Data.Model[absent]; ok {
			t.Fatal("form included an uninstalled exact peer", absent)
		}
	}
	var enabled []int64
	if json.Unmarshal(form.Data.Model["enable_sites"], &enabled) != nil || !reflect.DeepEqual(enabled, []int64{beta, alpha}) {
		t.Fatalf("form did not read live active sites in source priority order: %v", enabled)
	}
	saved := callTest(t, handler, http.MethodPut, path, string(mustCleanSeedJSON(map[string]any{
		"enable_sites": []int64{alpha}, "search_follow_enable_sites": true, "rss_sites": []int64{beta},
		"signin_follow_enable_sites": true, "login_follow_enable_sites": true,
		"statistic_follow_enable_sites": true, "iyuu_seed_follow_enable_sites": true,
	})), cookie, "")
	assertOriginalRegistrySuccess(t, saved.Code, saved.Body.Bytes())
	checkLists := func(eventOrder, priorityOrder []int64, search []string) error {
		s := a.Settings()
		if !reflect.DeepEqual(s.IndexerSites, search) || !reflect.DeepEqual(s.RSSSites, []string{"owned-beta"}) {
			return fmt.Errorf("search=%v RSS=%v", s.IndexerSites, s.RSSSites)
		}
		fields := map[string][]string{"AutoSignIn": {"sign_sites", "login_sites"}, "SiteStatistic": {"statistic_sites"}, "IYUUAutoSeed": {"sites"}}
		for instance, keys := range fields {
			raw, present, err := a.readOriginalBaseConfigForInit(t.Context(), instance)
			var values map[string]json.RawMessage
			if err != nil || !present || json.Unmarshal(raw, &values) != nil {
				return fmt.Errorf("could not read %s configuration: %v", instance, err)
			}
			// AutoSignIn and IYUUAutoSeed each reinitialize in site-priority
			// order and persist that normalization. SiteStatistic keeps the
			// field exactly as MergeSiteSwitch wrote it (append order).
			want := priorityOrder
			if instance == "SiteStatistic" {
				want = eventOrder
			}
			for _, key := range keys {
				var ids []int64
				if json.Unmarshal(values[key], &ids) != nil || !reflect.DeepEqual(ids, want) {
					return fmt.Errorf("%s %s=%v, want %v", instance, key, ids, want)
				}
			}
		}
		return nil
	}
	active := []string{}
	for _, site := range a.Settings().Sites {
		if site.Enabled {
			active = append(active, site.ID)
		}
	}
	if err := checkLists([]int64{alpha}, []int64{alpha}, []string{"owned-alpha"}); err != nil || !reflect.DeepEqual(active, []string{"owned-alpha"}) {
		t.Fatalf("explicit config save did not update native enabled/search/RSS and the three installed peer configurations: active=%v, %v", active, err)
	}
	var last error
	t.Cleanup(func() {
		if t.Failed() && last != nil {
			t.Log("last observed source selection:", last)
		}
	})
	setSite("owned-beta", "聚合乙", "https://owned-beta.example.net", 10, true)
	privateAwait(t, "actual SiteUpdated adds the enabled site to source follow lists", func() bool {
		last = checkLists([]int64{alpha, beta}, []int64{beta, alpha}, []string{"owned-alpha", "owned-beta"})
		return last == nil
	})
	deleted := callTest(t, handler, http.MethodDelete, "/api/sites/manage/owned-alpha", "", cookie, "")
	if deleted.Code != http.StatusOK {
		t.Fatalf("owned site deletion failed: %d %s", deleted.Code, deleted.Body.String())
	}
	privateAwait(t, "actual SiteDeleted removes the numeric site from following lists only", func() bool {
		last = checkLists([]int64{beta}, []int64{beta}, []string{"owned-beta"})
		return last == nil
	})
	t.Log("indexed Linux plugin rendered the native form, saved actual site and peer settings, and followed normal site update and deletion; independent RSS selection persisted")
}

func TestMergeSiteSwitchSiteUpdatedWaitsForLifecycle(t *testing.T) {
	bundle := os.Getenv("SOURCE_MERGE_SITE_SWITCH_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the owned Linux amd64 plugin packages")
	}
	t.Setenv("MP_SERVER_HOST", "")
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	a.Start(t.Context())
	for _, id := range []string{"SiteStatistic", "MergeSiteSwitch"} {
		installMergeSiteSwitchPackage(t, a, cookie, filepath.Join(bundle, id), id)
	}
	handler := a.Handler()
	const site = `"id":"owned-gate","name":"并发验收","base_url":"https://owned-gate.example.org"`
	setSite := func(enabled bool) {
		t.Helper()
		response := callTest(t, handler, http.MethodPost, "/api/sites/manage", fmt.Sprintf(`{%s,"enabled":%t}`, site, enabled), cookie, "")
		if response.Code != http.StatusOK {
			t.Fatalf("site save failed: %d %s", response.Code, response.Body.String())
		}
	}
	setSite(false)
	response := callTest(t, handler, http.MethodPut, "/api/plugins/MergeSiteSwitch", `{"search_follow_enable_sites":true,"statistic_follow_enable_sites":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	_, release, err := a.enterOriginalManagerOperation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer release()
		setSite(true)
		// Allow the actual asynchronous dispatcher to reach the occupied manager.
		time.Sleep(200 * time.Millisecond)
		if got := a.Settings().IndexerSites; len(got) != 0 {
			t.Fatalf("event wrote search before admitting the peer reinitialization: %v", got)
		}
	}()
	identity, err := a.Store.nativeSiteByAppID(t.Context(), "owned-gate")
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := a.Store.nativeSourcePublicSiteID(t.Context(), a.Store.DB, identity)
	if err != nil || sourceID <= 0 {
		t.Fatal("missing numeric source identity", err)
	}
	privateAwait(t, "queued SiteUpdated writes both selections after lifecycle releases", func() bool {
		raw, _, err := a.readOriginalBaseConfigForInit(t.Context(), "SiteStatistic")
		var peer struct {
			IDs []int64 `json:"statistic_sites"`
		}
		return err == nil && json.Unmarshal(raw, &peer) == nil && reflect.DeepEqual(peer.IDs, []int64{sourceID}) &&
			reflect.DeepEqual(a.Settings().IndexerSites, []string{"owned-gate"})
	})
	t.Log("the ordinary site update made no partial search write while the manager was occupied and committed both selections after release")
}

func installMergeSiteSwitchPackage(t *testing.T, a *App, cookie *http.Cookie, bundle, id string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(bundle, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index artifacts.RepositoryIndex
	if json.Unmarshal(raw, &index) != nil || len(index.Plugins) != 1 || index.Plugins[0].PluginID != id || len(index.Plugins[0].Releases) != 1 || len(index.Plugins[0].Releases[0].Artifacts) != 1 {
		t.Fatal("unexpected fixture index", id)
	}
	artifact := &index.Plugins[0].Releases[0].Artifacts[0]
	archive, err := os.ReadFile(filepath.Join(bundle, "worker.zip"))
	if err != nil || artifact.Platform != "linux-amd64" || artifact.Size != int64(len(archive)) || artifact.SHA256 != fmt.Sprintf("%x", sha256.Sum256(archive)) {
		t.Fatalf("unexpected exact fixture package %s: %v", id, err)
	}
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker.zip":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(repository.Close)
	artifact.URL = repository.URL + "/worker.zip"
	payload := mustCleanSeedJSON(map[string]string{"plugin_id": id, "repo_url": repository.URL + "/index"})
	response := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/install", string(payload), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	originalBaseSDKLoaded(t, a, id)
	t.Logf("installed %s exact ZIP %s", id, artifact.SHA256)
}
