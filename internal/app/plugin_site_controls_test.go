package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

func siteSwitchFixture(t *testing.T) (*App, *http.Cookie, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	first, second := &atomic.Int32{}, &atomic.Int32{}
	server := func(counter *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			counter.Add(1)
			fmt.Fprint(w, workflowHTML("Synthetic.Film.2024.1080p", "", false))
		}))
	}
	one, two := server(first), server(second)
	t.Cleanup(one.Close)
	t.Cleanup(two.Close)
	c := a.Settings()
	c.Sites = []SiteConfig{parallelSearchSite("s1", one.URL), parallelSearchSite("s2", two.URL)}
	c.Sites[1].Enabled = false
	c.Sites[0].Priority = 5
	c.Sites[1].Priority = 1
	if err := a.saveSettings(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	cookie := setupTestAdmin(t, a)
	if response := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"MergeSiteSwitch"}`, cookie, ""); response.Code != 200 {
		t.Fatal("install site switch", response.Code, response.Body.String())
	}
	siteSwitchExecute(t, a, true)
	return a, cookie, first, second
}

func siteSwitchExecute(t *testing.T, a *App, success bool) PluginRun {
	t.Helper()
	ctx := context.Background()
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if run.InstanceID != "MergeSiteSwitch" {
		t.Fatal("expected site-switch worker", run.InstanceID)
	}
	a.executePluginRun(ctx, run)
	run, err = a.pluginStore.Run(ctx, run.ID)
	if err != nil || success != (run.State == "succeeded") {
		t.Fatal("site-switch run outcome", run.State, run.ErrorCode, err)
	}
	return run
}

func siteSwitchSave(t *testing.T, a *App, cookie *http.Cookie, config map[string]any) {
	t.Helper()
	stored, err := a.pluginStore.Config(context.Background(), "MergeSiteSwitch")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"revision": stored.Revision, "config": config})
	if response := callTest(t, a.Handler(), "PUT", "/api/plugins/MergeSiteSwitch/config", string(raw), cookie, ""); response.Code != 200 {
		t.Fatal("save site switch", response.Code, response.Body.String())
	}
}

func TestPluginSiteSwitchRealSDKCoreScopesAndOriginalExplicitOverride(t *testing.T) {
	a, cookie, first, second := siteSwitchFixture(t)
	ctx := context.Background()
	if a.Settings().Sites[1].Enabled {
		t.Fatal("installation changed sites")
	}
	form := callTest(t, a.Handler(), "GET", "/api/plugins/MergeSiteSwitch/form", "", cookie, "")
	for _, field := range []string{"启用的站点", "设定 / 搜索 / 搜索站点", "设定 / 订阅 / 订阅站点", "跟随启用的站点", "点击保存后会立即生效"} {
		if !strings.Contains(form.Body.String(), field) {
			t.Fatal("missing original field", field)
		}
	}
	if strings.Contains(form.Body.String(), "synthetic-search-cookie") {
		t.Fatal("form leaked a site credential")
	}
	siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []string{"s1", "s2"}, "search_sites": []string{"s2"}, "rss_sites": []string{"s1"}})
	run := siteSwitchExecute(t, a, true)
	config := a.Settings()
	if !config.Sites[1].Enabled || !reflect.DeepEqual(config.IndexerSites, []string{"s2"}) || !reflect.DeepEqual(config.RSSSites, []string{"s1"}) {
		t.Fatal("core settings not applied")
	}
	if config.Sites[0].Cookie != "synthetic-search-cookie" {
		t.Fatal("site credential changed")
	}
	if _, err := a.searchResources(ctx, SearchRequest{Query: "Synthetic Film"}); err != nil {
		t.Fatal(err)
	}
	if first.Load() != 0 || second.Load() != 1 {
		t.Fatal("global search selection not used", first.Load(), second.Load())
	}
	if _, err := a.searchResources(ctx, SearchRequest{Query: "Synthetic Film", SiteIDs: []string{"s1"}}); err != nil {
		t.Fatal(err)
	}
	if first.Load() != 1 || second.Load() != 1 {
		t.Fatal("explicit original search override blocked", first.Load(), second.Load())
	}
	config.IndexerSites = []string{}
	a.settingsMu.Lock()
	err := a.saveSettings(ctx, config)
	a.settingsMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.searchResources(ctx, SearchRequest{Query: "Synthetic Film"}); err != nil {
		t.Fatal(err)
	}
	if first.Load() != 2 || second.Load() != 2 {
		t.Fatal("empty list must search all enabled sites", first.Load(), second.Load())
	}
	// Retrying after a committed operation cannot reapply an old enable list.
	config = a.Settings()
	config.Sites[1].Enabled = false
	if err := a.saveSettings(ctx, config); err != nil {
		t.Fatal(err)
	}
	a.Store.DB.Exec(`UPDATE plugin_runs SET state='running',result=NULL WHERE id=?`, run.ID)
	if err := a.pluginStore.recoverPluginRuns(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, _ := a.pluginStore.Run(ctx, run.ID)
	if recovered.State != "succeeded" || a.Settings().Sites[1].Enabled {
		t.Fatal("recovery replayed committed site changes", recovered.State)
	}
	// Reload registers flags, it is not a saved-config API request.
	if _, err := a.pluginStore.EnqueueRun(ctx, "MergeSiteSwitch", "reload", nil); err != nil {
		t.Fatal(err)
	}
	siteSwitchExecute(t, a, true)
	if a.Settings().Sites[1].Enabled {
		t.Fatal("reload replayed stale enable list")
	}
}

func TestPluginSiteSwitchFollowUsesInitializedFlagsAndReflectsExternalEdits(t *testing.T) {
	a, cookie, _, _ := siteSwitchFixture(t)
	ctx := context.Background()
	siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []string{"s1"}, "search_follow_enable_sites": true, "rss_follow_enable_sites": true})
	siteSwitchExecute(t, a, true)
	site := a.Settings().Sites[1]
	site.Enabled = true
	if _, err := a.saveManagedSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Settings().IndexerSites, []string{"s1", "s2"}) || !reflect.DeepEqual(a.Settings().RSSSites, []string{"s1", "s2"}) {
		t.Fatal("follow did not append activated site")
	}
	site = a.Settings().Sites[0]
	site.Enabled = false
	if _, err := a.saveManagedSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Settings().IndexerSites, []string{"s2"}) {
		t.Fatal("follow did not remove disabled site")
	}
	form := callTest(t, a.Handler(), "GET", "/api/plugins/MergeSiteSwitch/form", "", cookie, "")
	var value struct {
		Data struct{ Model builtin.SiteControls }
	}
	if json.Unmarshal(form.Body.Bytes(), &value) != nil || !reflect.DeepEqual(value.Data.Model.Sites("enable_sites"), []string{"s2"}) {
		t.Fatal("form reads stale plugin copy")
	}
	// A config waiting in the queue must not become an active watcher early.
	siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []string{"s2"}, "search_sites": []string{"s2"}, "rss_sites": []string{"s2"}, "search_follow_enable_sites": false, "rss_follow_enable_sites": false})
	site = a.Settings().Sites[0]
	site.Enabled = true
	if _, err := a.saveManagedSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(a.Settings().IndexerSites, "s1") {
		t.Fatal("queued config became active before initialization")
	}
	siteSwitchExecute(t, a, true)
	site = a.Settings().Sites[0]
	site.Enabled = true
	if _, err := a.saveManagedSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Settings().IndexerSites, []string{"s2"}) {
		t.Fatal("disabled follow still modified selection")
	}
	if err := a.pluginStore.Uninstall(ctx, "MergeSiteSwitch"); err != nil {
		t.Fatal(err)
	}
	site = a.Settings().Sites[1]
	site.Enabled = false
	if _, err := a.saveManagedSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Settings().IndexerSites, []string{"s2"}) {
		t.Fatal("uninstall erased independently saved core selection")
	}
}

func installSiteControlPeerFixture(t *testing.T, a *App, pluginID string, fields map[string]any) {
	t.Helper()
	manifest := plugins.Manifest{SchemaVersion: 1, PluginID: pluginID, Name: "合成关联插件", Version: "1.0-fixture", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"invoke"}}
	raw, _ := json.Marshal(manifest)
	in := PluginInstallation{InstanceID: pluginID, PluginID: pluginID, Version: manifest.Version, ManifestSchemaVersion: 1, Manifest: raw, DesiredEnabled: true}
	if err := a.pluginStore.Install(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginStore.SaveConfig(context.Background(), pluginID, 0, fields); err != nil {
		t.Fatal(err)
	}
}

func TestPluginSiteSwitchPeerTransactionProtocolFixtureAndUnavailableRollback(t *testing.T) {
	a, cookie, _, _ := siteSwitchFixture(t)
	ctx := context.Background()
	installSiteControlPeerFixture(t, a, "AutoSignIn", map[string]any{"enabled": false, "sign_sites": []string{"s1"}, "login_sites": []string{}, "cookie": "synthetic-peer-secret"})
	config := map[string]any{"enable_sites": []string{"s2"}, "search_follow_enable_sites": true, "rss_follow_enable_sites": true, "signin_follow_enable_sites": true, "login_follow_enable_sites": true}
	siteSwitchSave(t, a, cookie, config)
	failed := siteSwitchExecute(t, a, false)
	if failed.ErrorCode != "PLUGIN_DEPENDENCY_UNAVAILABLE" || a.Settings().Sites[1].Enabled {
		t.Fatal("unimplemented dependency pretended ready or changed core", failed.ErrorCode)
	}
	peer, _ := a.pluginStore.Config(ctx, "AutoSignIn")
	if peer.Revision != 1 {
		t.Fatal("failed dependency modified target")
	}
	// Only this test permits a synthetic peer. No claim of an AutoSignIn Go
	// business implementation is made; this checks the real store transaction.
	a.pluginSitePeerAvailable = func(id, version string) bool { return id == "AutoSignIn" && version == "1.0-fixture" }
	siteSwitchSave(t, a, cookie, config)
	siteSwitchExecute(t, a, true)
	peer, _ = a.pluginStore.Config(ctx, "AutoSignIn")
	var fields map[string]json.RawMessage
	json.Unmarshal(peer.Value, &fields)
	var signs, logins []string
	json.Unmarshal(fields["sign_sites"], &signs)
	json.Unmarshal(fields["login_sites"], &logins)
	if peer.Revision != 2 || !reflect.DeepEqual(signs, []string{"s2"}) || !reflect.DeepEqual(logins, []string{"s2"}) || string(fields["cookie"]) != `"synthetic-peer-secret"` || string(fields["enabled"]) != "false" {
		t.Fatal("peer projection overwrote unrelated config")
	}
	var queued int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id='AutoSignIn' AND state='pending' AND config_revision=2`).Scan(&queued); err != nil || queued != 1 {
		t.Fatal("peer init not atomically queued", queued, err)
	}
	form := callTest(t, a.Handler(), "GET", "/api/plugins/MergeSiteSwitch/form", "", cookie, "")
	if strings.Contains(form.Body.String(), "synthetic-peer-secret") || !strings.Contains(form.Body.String(), "签到站点") {
		t.Fatal("dynamic peer form missing or secret exposed")
	}
	// Inject the final core write failing after peer edits and self patching.
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER fail_site_control_core BEFORE UPDATE ON records WHEN OLD.kind='config' BEGIN SELECT RAISE(ABORT,'synthetic settings failure'); END`); err != nil {
		t.Fatal(err)
	}
	current := a.Settings()
	site := current.Sites[0]
	site.Enabled = true
	if _, err := a.saveManagedSite(ctx, site); err == nil {
		t.Fatal("core write failure ignored")
	}
	peerAfter, _ := a.pluginStore.Config(ctx, "AutoSignIn")
	if peerAfter.Revision != peer.Revision || !reflect.DeepEqual(a.Settings(), current) {
		t.Fatal("failed follow commit changed target or memory")
	}
	a.Store.DB.Exec(`DROP TRIGGER fail_site_control_core`)
}

func TestPluginSiteSwitchReceiptFailureRollsBackWholeApply(t *testing.T) {
	a, cookie, _, _ := siteSwitchFixture(t)
	ctx := context.Background()
	before := a.Settings()
	siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []string{"s2"}, "search_follow_enable_sites": true})
	stored, _ := a.pluginStore.Config(ctx, "MergeSiteSwitch")
	a.Store.DB.Exec(`CREATE TRIGGER fail_site_control_receipt BEFORE INSERT ON plugin_site_control_operations BEGIN SELECT RAISE(ABORT,'synthetic receipt failure'); END`)
	siteSwitchExecute(t, a, false)
	after, _ := a.pluginStore.Config(ctx, "MergeSiteSwitch")
	if !reflect.DeepEqual(a.Settings(), before) || after.Revision != stored.Revision {
		t.Fatal("receipt failure committed partial state")
	}
	var watchers int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_site_control_watchers WHERE config_revision=?`, stored.Revision).Scan(&watchers)
	if watchers != 0 {
		t.Fatal("failed apply activated watcher")
	}
	a.Store.DB.Exec(`DROP TRIGGER fail_site_control_receipt`)
}

func TestSubscriptionSiteControlsOriginalFallbackAndDeletion(t *testing.T) {
	for _, sample := range []struct{ defaults, selected, want []string }{{nil, nil, nil}, {[]string{"a"}, nil, []string{"a"}}, {nil, []string{"b"}, []string{"b"}}, {[]string{"a", "c"}, []string{"b", "c"}, []string{"c"}}, {[]string{"a"}, []string{"b"}, []string{"a"}}} {
		if got := subscriptionSiteControls(sample.defaults, sample.selected); !reflect.DeepEqual(got, sample.want) {
			t.Fatal("original subscription fallback differs", got, sample.want)
		}
	}
	a, _, _, _ := siteSwitchFixture(t)
	ctx := context.Background()
	c := a.Settings()
	c.IndexerSites = []string{"s1"}
	c.RSSSites = []string{"s1", "s2"}
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "subscription", "owned-sub", Subscription{ID: "owned-sub", SiteIDs: []string{"s1"}, Revision: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.deleteManagedSite(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	var sub Subscription
	a.Store.Get(ctx, "subscription", "owned-sub", &sub)
	if len(sub.SiteIDs) != 0 || sub.Revision != 4 || len(a.Settings().IndexerSites) != 0 || !reflect.DeepEqual(a.Settings().RSSSites, []string{"s2"}) {
		t.Fatal("site deletion retained stale references")
	}
}

func TestPluginSiteSwitchHostRejectsCrossScopeAndOldConfig(t *testing.T) {
	a, cookie, _, _ := siteSwitchFixture(t)
	ctx := context.Background()
	installation, _ := a.pluginStore.Installation(ctx, "MergeSiteSwitch")
	identity := plugins.Identity{PluginID: installation.PluginID, InstanceID: installation.InstanceID, Version: installation.Version}
	if _, err := a.pluginHostCall(ctx, installation, nil, identity, "sites.controls.configure", json.RawMessage(`{}`)); pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
		t.Fatal("form got write capability", err)
	}
	siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []string{"s2"}})
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginHostCall(ctx, installation, &run, identity, "sites.controls.configure", json.RawMessage(`{"instance_id":"another"}`)); pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
		t.Fatal("arbitrary scope accepted", err)
	}
	current, _ := a.pluginStore.Config(ctx, "MergeSiteSwitch")
	if _, err := a.pluginStore.SaveConfig(ctx, "MergeSiteSwitch", current.Revision, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginHostCall(ctx, installation, &run, identity, "sites.controls.configure", json.RawMessage(`{}`)); pluginErrorCode(err) != "PLUGIN_REVISION_CONFLICT" {
		t.Fatal("old config still applied", err)
	}
	if err := a.pluginStore.FinishRun(ctx, run.ID, "failed", "PLUGIN_REVISION_CONFLICT", nil); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPluginSiteSwitchConcurrentSiteEditsAndScopedReads(t *testing.T) {
	a, cookie, _, _ := siteSwitchFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []string{"s1"}, "search_follow_enable_sites": true, "rss_follow_enable_sites": true})
	siteSwitchExecute(t, a, true)
	installation, _ := a.pluginStore.Installation(ctx, "MergeSiteSwitch")
	identity := plugins.Identity{PluginID: installation.PluginID, InstanceID: installation.InstanceID, Version: installation.Version}
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Go(func() {
		for i := 0; i < 20; i++ {
			site := a.Settings().Sites[1]
			site.Enabled = i%2 == 0
			if _, err := a.saveManagedSite(ctx, site); err != nil {
				errors <- err
				return
			}
		}
		errors <- nil
	})
	workers.Go(func() {
		for i := 0; i < 20; i++ {
			if _, err := a.pluginHostCall(ctx, installation, nil, identity, "sites.controls.read", json.RawMessage(`{}`)); err != nil {
				errors <- err
				return
			}
		}
		errors <- nil
	})
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("site edits and plugin reads deadlocked")
	}
	for i := 0; i < 2; i++ {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
}

func TestPluginSiteSwitchWatcherSurvivesRestartWithoutReplayingSelections(t *testing.T) {
	a, cookie, _, _ := siteSwitchFixture(t)
	ctx := context.Background()
	siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []string{"s1"}, "search_follow_enable_sites": true})
	siteSwitchExecute(t, a, true)
	config := a.Settings()
	config.Sites[1].Enabled = true
	if err := a.saveSettings(ctx, config); err != nil {
		t.Fatal(err)
	}
	dir := a.Dir
	a.Close()
	b, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	useBuiltInPluginProcess(t, b)
	if err := b.initializeNativePlugins(ctx); err != nil {
		t.Fatal(err)
	}
	siteSwitchExecute(t, b, true)
	if !b.Settings().Sites[1].Enabled {
		t.Fatal("startup reapplied the old enable list")
	}
	site := b.Settings().Sites[0]
	site.Enabled = false
	if _, err := b.saveManagedSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(b.Settings().IndexerSites, []string{"s2"}) {
		t.Fatal("persisted watcher stopped following after restart")
	}
}

func TestPluginSiteSwitchSelectionsSurviveUnrelatedSettingsSave(t *testing.T) {
	a, cookie, _, _ := siteSwitchFixture(t)
	siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []string{"s1"}, "search_sites": []string{"s2"}, "rss_sites": []string{"s1"}})
	siteSwitchExecute(t, a, true)
	settings := a.publicSettings()
	settings["indexer_sites"] = []string{}
	settings["rss_sites"] = []string{}
	raw, _ := json.Marshal(settings)
	response := callTest(t, a.Handler(), "PUT", "/api/settings", string(raw), cookie, "")
	if response.Code != 200 {
		t.Fatal("unrelated settings save failed", response.Code, response.Body.String())
	}
	if !reflect.DeepEqual(a.Settings().IndexerSites, []string{"s2"}) || !reflect.DeepEqual(a.Settings().RSSSites, []string{"s1"}) {
		t.Fatal("settings page overwrote newer plugin selections")
	}
}
