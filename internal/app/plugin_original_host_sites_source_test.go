package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/sites"
	"light-media/internal/sourceindexer"
)

func sourceSitesFixture(t *testing.T, a *App, cookie *http.Cookie) *originalPluginInstance {
	t.Helper()
	binary := os.Getenv("PLUGIN_SOURCE_SITES_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("actual separately compiled target SDK worker required")
	}
	m := installOwnedResourceProcessWorker(t, a, cookie, binary)
	return originalBaseSDKLoaded(t, a, m.PluginID)
}
func sourceSitesScope() originalBaseCallScope {
	return originalBaseCallScope{validate: func(context.Context) error { return nil }}
}
func sourceSitesSetModule(t *testing.T, a *App, cookie *http.Cookie, module *originalPluginInstance, value json.RawMessage) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"refresh_userdata": "module_execute"}, "result": value})
	saveOwnedModule(t, a, cookie, module, string(raw))
}
func sourceSitesData(t *testing.T, p *originalPluginInstance, method string, id string) plugins.SiteSourceRefreshResultV1 {
	t.Helper()
	var out plugins.SiteSourceRefreshResultV1
	if err := hostDownloaderCall(t, p, method, plugins.SiteHostRefreshV1{ID: id}, &out); err != nil {
		t.Fatal(method, err)
	}
	return out
}
func TestSourceSitesCurrentDTOActualSDKMatchesSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-source-sites-20261105/source-refresh-observations.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Case    string          `json:"case"`
			Module  json.RawMessage `json:"module_input"`
			Outcome struct {
				Value     json.RawMessage `json:"value"`
				Exception string          `json:"exception"`
			} `json:"outcome"`
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 12 {
		t.Fatal("source fixture")
	}
	a, cookie, module := ownedModuleFixture(t, true)
	worker := sourceSitesFixture(t, a, cookie)
	var network atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/favicon.ico" {
			network.Add(1)
		}
		w.WriteHeader(503)
	}))
	defer peer.Close()
	site := installResourceBrowseSite(t, a, peer.URL)
	for _, row := range oracle.Cases {
		if row.Case == "none-keeps-old" || row.Case == "db-error-stops-event" {
			continue
		}
		t.Run(row.Case, func(t *testing.T) {
			payload := row.Module
			if row.Outcome.Exception == "" {
				payload = row.Outcome.Value
			}
			sourceSitesSetModule(t, a, cookie, module, payload)
			module = originalBaseSDKLoaded(t, a, ownedModuleID)
			var out plugins.SiteSourceRefreshResultV1
			err := hostDownloaderCall(t, worker, "source_refresh", plugins.SiteHostRefreshV1{ID: site.ID}, &out)
			if row.Outcome.Exception != "" || row.Case == "success-preserves-returned-domain-day" {
				if err == nil {
					t.Fatal("malformed row did not fail after committed observation")
				}
				return
			}
			if err != nil || !originalBaseJSONEqual(out.Data, row.Outcome.Value) {
				t.Fatalf("source current DTO changed: %s want %s err %v", out.Data, row.Outcome.Value, err)
			}
		})
	}
	if network.Load() != 0 {
		t.Fatal("module DTO hit native peer", network.Load())
	}
	// A current false/None outcome does not return the previously successful day.
	sourceSitesSetModule(t, a, cookie, module, json.RawMessage(`false`))
	out := sourceSitesData(t, worker, "source_refresh", site.ID)
	if string(out.Data) != "null" {
		t.Fatal("None replaced by daily history", string(out.Data))
	}
	var old plugins.SiteAccountHostResultV1
	if err := hostDownloaderCall(t, worker, "legacy_refresh", plugins.SiteHostRefreshV1{ID: site.ID}, &old); err != nil || len(old.Items) == 0 {
		t.Fatal("legacy daily interface changed", old, err)
	}
}
func TestSourceSitesIndexerActualSDKOrderAliasAndCanonicalIdentity(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	worker := sourceSitesFixture(t, a, cookie)
	config := a.Settings()
	config.Sites = []SiteConfig{
		{Config: sites.Config{ID: "first", Name: "First", Kind: "ubits", BaseURL: "http://127.0.0.1:43101", Cookie: "OWNED_COOKIE_SECRET"}, SiteOptions: SiteOptions{Domain: "canonical.invalid", Priority: 99}, Enabled: true},
		{Config: sites.Config{ID: "last", Name: "Last", Kind: "ubits", BaseURL: "http://127.0.0.1:43102", Cookie: "OWNED_OTHER_SECRET"}, SiteOptions: SiteOptions{Domain: "alias.invalid", Priority: 1}, Enabled: true},
		{Config: sites.Config{ID: "unknown", Name: "Unknown", Kind: "ubits", BaseURL: "http://127.0.0.1:43103"}, SiteOptions: SiteOptions{Domain: "unknown.invalid"}, Enabled: true},
	}
	ownedDownloaderSave(t, a, config)
	template := map[string]any{"name": "Template", "domain": "https://canonical.invalid/", "schema": "NexusPhp", "ext_domains": []string{"https://alias.invalid/"}, "cookie": "NEVER_CACHE_SECRET"}
	if !a.addOriginalSourceSiteIndexer("canonical.invalid", template) || !a.addOriginalSourceSiteIndexer("alias.invalid", template) {
		t.Fatal("custom source hook")
	}
	// The explicit registry Add shares the admitted safe template. Constructor aliases use same map too.
	a.pluginOriginalSourceSites.mu.Lock()
	a.pluginOriginalSourceSites.registry = sourceindexerFixtureRegistry(template)
	a.pluginOriginalSourceSites.mu.Unlock()
	var out plugins.SiteSourceIndexersResultV1
	if err := hostDownloaderCall(t, worker, "source_indexers", struct{}{}, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 2 || out.Items[0].SiteID == nil || *out.Items[0].SiteID != "last" || *out.Items[1].SiteID != "last" {
		t.Fatal("source alias list last overlay", out)
	}
	reordered := a.Settings()
	reordered.Sites[0], reordered.Sites[1] = reordered.Sites[1], reordered.Sites[0]
	ownedDownloaderSave(t, a, reordered)
	var again plugins.SiteSourceIndexersResultV1
	if err := hostDownloaderCall(t, worker, "source_indexers", struct{}{}, &again); err != nil || len(again.Items) != 2 || *again.Items[0].SiteID != "last" {
		t.Fatal("fixed SQLite source scan replaced by settings/priority order", again, err)
	}
	encoded, _ := json.Marshal(out)
	if bytes.Contains(encoded, []byte("SECRET")) {
		t.Fatal("credential escaped")
	}
	for _, domain := range []string{"https://canonical.invalid/", "www.canonical.invalid", "unknown.invalid"} {
		var one plugins.SiteSourceIndexerResultV1
		if err := hostDownloaderCall(t, worker, "source_indexer", plugins.SiteSourceIndexerRequestV1{Domain: domain}, &one); err != nil || one.Indexer != nil {
			t.Fatal("getter normalized raw key or invented template", domain, one, err)
		}
	}
	// Get overlays a detached copy; list's previous last-overlay cache remains.
	var one plugins.SiteSourceIndexerResultV1
	if err := hostDownloaderCall(t, worker, "source_indexer", plugins.SiteSourceIndexerRequestV1{Domain: "canonical.invalid"}, &one); err != nil || one.Indexer.SiteID == nil || *one.Indexer.SiteID != "first" {
		t.Fatal("single projection", one, err)
	}
	// A catalog numeric id alone cannot borrow a configured site's identity.
	a.addOriginalSourceSiteIndexer("catalog-only.invalid", map[string]any{"id": int64(1), "name": "CatalogOnly", "domain": "https://catalog-only.invalid/", "url": "https://not-current.invalid/"})
	if err := hostDownloaderCall(t, worker, "source_indexer", plugins.SiteSourceIndexerRequestV1{Domain: "catalog-only.invalid"}, &one); err != nil || one.Indexer == nil || one.Indexer.SiteID != nil {
		t.Fatal("catalog id collision borrowed canonical identity", one, err)
	}
	config = a.Settings()
	config.Sites = nil
	ownedDownloaderSave(t, a, config)
	if err := hostDownloaderCall(t, worker, "source_indexer", plugins.SiteSourceIndexerRequestV1{Domain: "canonical.invalid"}, &one); err != nil || one.Indexer == nil || one.Indexer.SiteID != nil || !bytes.Contains(one.Indexer.Fields, []byte(`"name":"Last"`)) {
		t.Fatal("source cache lifetime or deleted identity grant", one, err)
	}
	if err := hostDownloaderCall(t, worker, "source_refresh", plugins.SiteHostRefreshV1{ID: "last"}, nil); err == nil {
		t.Fatal("deleted cached metadata authorized refresh")
	}
}
func sourceindexerFixtureRegistry(template map[string]any) *sourceindexer.Registry {
	return sourceindexer.New([]map[string]any{originalSourceTemplateFixture(template)}, "Owned-UA")
}
func originalSourceTemplateFixture(v map[string]any) map[string]any {
	out := originalSourceSiteSafeFields(v)
	out["ext_domains"] = v["ext_domains"]
	return out
}

func TestSourceSitesRefreshAllNativePeerCurrentNoneStopAndSQL(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	worker := sourceSitesFixture(t, a, cookie)
	var mode atomic.Int32
	var hits atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch mode.Load() {
		case 1:
			w.WriteHeader(503)
			io.WriteString(w, "owned logged out")
		case 2:
			io.WriteString(w, `<a href="logout.php">out</a>`)
		default:
			io.WriteString(w, `<a href="logout.php">out</a><div>上传：1 GB 下载：3 GB</div>`)
		}
	}))
	defer peer.Close()
	site := SiteConfig{Config: sites.Config{ID: "native", Name: "OwnedNative", Kind: "jiayuan", BaseURL: peer.URL, Cookie: "owned-cookie"}, SiteOptions: SiteOptions{Domain: "owned.invalid"}, Enabled: true}
	cfg := a.Settings()
	cfg.Sites = []SiteConfig{site}
	ownedDownloaderSave(t, a, cfg)
	a.addOriginalSourceSiteIndexer("owned.invalid", map[string]any{"domain": "https://owned.invalid/", "schema": "NexusPhp"})
	good := sourceSitesData(t, worker, "source_refresh_all", "")
	if !bytes.Contains(good.Data, []byte(`"OwnedNative"`)) || !bytes.Contains(good.Data, []byte(`"upload":1073741824`)) || !bytes.Contains(good.Data, []byte(`"domain":null`)) || !bytes.Contains(good.Data, []byte(`"updated_day":null`)) {
		t.Fatal("native current source DTO", string(good.Data))
	}
	mode.Store(1)
	failed := sourceSitesData(t, worker, "source_refresh", site.ID)
	if !bytes.Contains(failed.Data, []byte(`"err_msg":"未检测到已登陆`)) {
		t.Fatal("current vendor error DTO", string(failed.Data))
	}
	var daily plugins.SiteAccountHostResultV1
	if err := hostDownloaderCall(t, worker, "accounts", plugins.SiteAccountHostRequestV1{ID: site.ID, Latest: true}, &daily); err != nil || len(daily.Items) != 1 || !bytes.Contains(daily.Items[0].Fields, []byte(`"upload":1073741824`)) {
		t.Fatal("error replaced old good day", daily, err)
	}
	// Exact source no-data path: hhanclub logged-in page lacks its required account panel.
	cfg = a.Settings()
	cfg.Sites[0].Kind = "hhanclub"
	ownedDownloaderSave(t, a, cfg)
	mode.Store(2)
	none := sourceSitesData(t, worker, "source_refresh", site.ID)
	if string(none.Data) != "null" {
		t.Fatal("native None fabricated daily row", string(none.Data))
	}
	before := hits.Load()
	a.closeOriginalBackgroundAdmission()
	stopped := sourceSitesData(t, worker, "source_refresh_all", "")
	if string(stopped.Data) != "null" || hits.Load() != before {
		t.Fatal("stopped host performed refresh", stopped, hits.Load(), before)
	}
}
func TestSourceSitesGuardAndSQLiteFailureDoNotBecomeNone(t *testing.T) {
	a, cookie, module := ownedModuleFixture(t, true)
	worker := sourceSitesFixture(t, a, cookie)
	site := installResourceBrowseSite(t, a, "http://127.0.0.1:43100")
	sourceSitesSetModule(t, a, cookie, module, json.RawMessage(`{"upload":17}`))
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_source_site_fail BEFORE INSERT ON records WHEN NEW.kind='site_stats_latest' BEGIN SELECT RAISE(FAIL,'owned-site-write-failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := hostDownloaderCall(t, worker, "source_refresh", plugins.SiteHostRefreshV1{ID: site.ID}, nil); err == nil {
		t.Fatal("real SQLite failure became successful no-data")
	}
	a.Store.DB.Exec(`DROP TRIGGER owned_source_site_fail`)
	scope := sourceSitesScope()
	denied := false
	scope.validate = func(context.Context) error {
		if !denied {
			denied = true
			return bad("PLUGIN_API_AUTH_REVOKED", "owned guard")
		}
		return nil
	}
	if _, err := a.originalSourceSiteRefresh(context.Background(), scope, site.ID); err == nil {
		t.Fatal("transient scope rejection swallowed")
	}
	out := sourceSitesData(t, worker, "source_refresh", site.ID)
	if !strings.Contains(string(out.Data), `"upload":17`) {
		t.Fatal("SQLite same-instance recovery", string(out.Data))
	}
}

func TestSourceSitesAllOwnerOrderNamesStopAndCommittedFailure(t *testing.T) {
	for _, mode := range []string{"normal", "stop-after-first", "sql-failure-after-first"} {
		t.Run(mode, func(t *testing.T) {
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			worker := sourceSitesFixture(t, a, cookie)
			var hits [3]atomic.Int32
			cfg := a.Settings()
			readMessages := false
			cfg.ReadSiteMessages = &readMessages
			names := []string{"SameName", "Middle", "SameName"}
			for i := 0; i < 3; i++ {
				i := i
				peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits[i].Add(1)
					if mode == "stop-after-first" && i == 0 {
						a.closeOriginalBackgroundAdmission()
					}
					io.WriteString(w, fmt.Sprintf(`<a href="logout.php">out</a><div>上传：%d GB 下载：0 GB</div>`, i+1))
				}))
				t.Cleanup(peer.Close)
				id := fmt.Sprintf("site-%d", i)
				domain := fmt.Sprintf("owned-%d.invalid", i)
				cfg.Sites = append(cfg.Sites, SiteConfig{Config: sites.Config{ID: id, Name: names[i], Kind: "jiayuan", BaseURL: peer.URL, Cookie: "owned-cookie"}, SiteOptions: SiteOptions{Domain: domain}, Enabled: true})
				a.addOriginalSourceSiteIndexer(domain, map[string]any{"domain": "https://" + domain + "/", "schema": "NexusPhp"})
			}
			ownedDownloaderSave(t, a, cfg)
			// SQLite write failure at the second site leaves the first real commit/event.
			if mode == "sql-failure-after-first" {
				if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_second_site_fail BEFORE INSERT ON records WHEN NEW.kind='site_stats_latest' AND NEW.id='site-1' BEGIN SELECT RAISE(FAIL,'owned second source write'); END`); err != nil {
					t.Fatal(err)
				}
			}
			var out plugins.SiteSourceRefreshResultV1
			err := hostDownloaderCall(t, worker, "source_refresh_all", struct{}{}, &out)
			var allEvents int
			if e := a.Store.DB.QueryRow(`SELECT count(*) FROM domain_events WHERE json_extract(CAST(payload AS TEXT),'$.event_type')='site.refreshed' AND json_extract(CAST(payload AS TEXT),'$.site_id')='*'`).Scan(&allEvents); e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "normal":
				if err != nil || bytes.Count(out.Data, []byte(`"SameName":`)) != 1 || !bytes.Contains(out.Data, []byte(`"upload":3221225472`)) || bytes.Index(out.Data, []byte(`"SameName"`)) > bytes.Index(out.Data, []byte(`"Middle"`)) || allEvents != 1 {
					t.Fatal("source ordered all map or final event", string(out.Data), err, allEvents)
				}
			case "stop-after-first":
				if err != nil || string(out.Data) != "null" || hits[1].Load() != 0 || hits[2].Load() != 0 || allEvents != 0 {
					t.Fatal("source stop check lost", out, err, allEvents)
				}
			case "sql-failure-after-first":
				if err == nil || hits[2].Load() != 0 || allEvents != 0 {
					t.Fatal("failure continued source batch", err, hits[2].Load(), allEvents)
				}
			}
		})
	}
}

func TestSourceSitesAccountsRenameCloneAndCold(t *testing.T) {
	a, cookie, module := ownedModuleFixture(t, true)
	worker := sourceSitesFixture(t, a, cookie)
	site := installResourceBrowseSite(t, a, "http://127.0.0.1:43100")
	sourceSitesSetModule(t, a, cookie, module, json.RawMessage(`{"upload":17,"username":"owned","userid":"90071992547409931234567890","message_unread_contents":[[null,{"id":1},[2],"source"]]}`))
	sourceSitesData(t, worker, "source_refresh", site.ID)
	oldName := site.Name
	cfg := a.Settings()
	cfg.Sites[0].Name = "CurrentRenamed"
	ownedDownloaderSave(t, a, cfg)
	read := func(object *originalPluginInstance, method string) plugins.SiteAccountHostResultV1 {
		t.Helper()
		var out plugins.SiteAccountHostResultV1
		if err := hostDownloaderCall(t, object, method, plugins.SiteAccountHostRequestV1{ID: site.ID, Latest: true}, &out); err != nil || len(out.Items) != 1 {
			t.Fatal(method, out, err)
		}
		return out
	}
	source := read(worker, "source_accounts")
	legacy := read(worker, "accounts")
	if !bytes.Contains(source.Items[0].Fields, []byte(`"name":"`+oldName+`"`)) || !bytes.Contains(legacy.Items[0].Fields, []byte(`"name":"CurrentRenamed"`)) || !bytes.Contains(source.Items[0].Fields, []byte("90071992547409931234567890")) {
		t.Fatal("stored source name/current legacy name/large ID", source, legacy)
	}
	response := callTest(t, a.Handler(), "POST", "/api/plugins/SourceSitesTest/clone", `{"suffix":"twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	clone := originalBaseSDKLoaded(t, a, "SourceSitesTesttwin")
	if !originalBaseJSONEqual(read(clone, "source_accounts").Items[0].Fields, source.Items[0].Fields) {
		t.Fatal("clone source history differs")
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if err = cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	other := originalBaseSDKLoaded(t, cold, "SourceSitesTest")
	var after plugins.SiteAccountHostResultV1
	if err = hostDownloaderCall(t, other, "source_accounts", plugins.SiteAccountHostRequestV1{ID: site.ID, Latest: true}, &after); err != nil || len(after.Items) != 1 || !originalBaseJSONEqual(after.Items[0].Fields, source.Items[0].Fields) {
		t.Fatal("cold source SQLite history changed", after, err)
	}
}

func TestSourceSitesTransientGuardAfterNativeCannotManufactureNone(t *testing.T) {
	a := newTestApp(t)
	var deny atomic.Bool
	var hits atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		deny.Store(true)
		io.WriteString(w, `<a href="logout.php">out</a><div>上传：1 GB 下载：0 GB</div>`)
	}))
	defer peer.Close()
	site := installResourceBrowseSite(t, a, peer.URL)
	site.Kind = "jiayuan"
	a.cfgMu.Lock()
	a.config.Sites[0] = site
	a.cfgMu.Unlock()
	scope := sourceSitesScope()
	scope.validate = func(context.Context) error {
		if deny.Swap(false) {
			return bad("PLUGIN_API_AUTH_REVOKED", "owned transient guard")
		}
		return nil
	}
	if data, err := a.originalSourceSiteRefresh(context.Background(), scope, site.ID); err == nil {
		t.Fatal("transient guard converted native failure to current no-data", string(data))
	}
	if hits.Load() != 1 {
		t.Fatal("guarded native request count", hits.Load())
	}
	var count int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='site_stats_latest'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected native call published observation", count, err)
	}
	if err := scope.validate(context.Background()); err != nil {
		t.Fatal("test did not actually restore authority", err)
	}
}

func TestSourceSitesWideCurrentIntegersAndHistoricalFloat(t *testing.T) {
	a, cookie, module := ownedModuleFixture(t, true)
	worker := sourceSitesFixture(t, a, cookie)
	site := installResourceBrowseSite(t, a, "http://127.0.0.1:43100")
	integer := "1267650600228229401496703217721"
	input := json.RawMessage(`{"userid":"wide-user","domain":null,"updated_day":null,"upload":` + integer + `,"leeching":` + integer + `,"leeching_size":` + integer + `,"seeding_info":[null,{"nested":[1,"x"]}]}`)
	sourceSitesSetModule(t, a, cookie, module, input)
	out := sourceSitesData(t, worker, "source_refresh", site.ID)
	if !bytes.Contains(out.Data, []byte(`"upload":`+integer)) || !bytes.Contains(out.Data, []byte(`"domain":null`)) || !bytes.Contains(out.Data, []byte(`"seeding_info":[null,{"nested":[1,"x"]}]`)) {
		t.Fatal("exact current source integer/list lost", string(out.Data))
	}
	var historical plugins.SiteAccountHostResultV1
	if err := hostDownloaderCall(t, worker, "source_accounts", plugins.SiteAccountHostRequestV1{ID: site.ID, Latest: true}, &historical); err != nil || len(historical.Items) != 1 {
		t.Fatal(historical, err)
	}
	fields := historical.Items[0].Fields
	if !bytes.Contains(fields, []byte(`"upload":1.2676506002282294e+30`)) || bytes.Index(fields, []byte(`"domain"`)) > bytes.Index(fields, []byte(`"name"`)) || bytes.Index(fields, []byte(`"bonus"`)) > bytes.Index(fields, []byte(`"upload"`)) {
		t.Fatal("source DB Float/schema column order", string(fields))
	}
	// The bounded native observation has no invented zero for this counter.
	var sealed []byte
	var snapshot siteSnapshot
	if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='site_stats_latest' AND id=?`, site.ID).Scan(&sealed); err != nil || a.Store.openSiteAccountSnapshot("site_stats_latest", site.ID, sealed, &snapshot) != nil || snapshot.Stats == nil || snapshot.Stats.Uploaded != nil {
		t.Fatal("wide current value became fabricated native zero", snapshot, err)
	}
	for _, raw := range []json.RawMessage{json.RawMessage(`{"message_unread":` + integer + `}`), json.RawMessage(`{"upload":` + "1" + strings.Repeat("0", 400) + `}`)} {
		sourceSitesSetModule(t, a, cookie, originalBaseSDKLoaded(t, a, ownedModuleID), raw)
		if err := hostDownloaderCall(t, worker, "source_refresh", plugins.SiteHostRefreshV1{ID: site.ID}, nil); err == nil {
			t.Fatal("source DB binding overflow fabricated successful current DTO")
		}
	}
}

func TestSourceSitesBindingErrorSkipRetainsWideCurrentAndUnreadMessage(t *testing.T) {
	a, cookie, module := ownedModuleFixture(t, true)
	worker := sourceSitesFixture(t, a, cookie)
	site := installResourceBrowseSite(t, a, "http://127.0.0.1:43100")
	good := json.RawMessage(`{"userid":"owned","upload":17}`)
	sourceSitesSetModule(t, a, cookie, module, good)
	sourceSitesData(t, worker, "source_refresh", site.ID)
	wide := "1267650600228229401496703217721" // (2**100)+12345, exact independently executed source input.
	for _, sample := range []struct{ key, value string }{{"userid", wide}, {"message_unread", wide}, {"upload", "1" + strings.Repeat("0", 400)}} {
		key, value := sample.key, sample.value
		module = originalBaseSDKLoaded(t, a, ownedModuleID)
		sourceSitesSetModule(t, a, cookie, module, json.RawMessage(`{"err_msg":"synthetic error","`+key+`":`+value+`}`))
		out := sourceSitesData(t, worker, "source_refresh", site.ID)
		if !bytes.Contains(out.Data, []byte(`"`+key+`":`+value)) {
			t.Fatal("err-skip coerced or rejected current source big integer", key, string(out.Data))
		}
		var old plugins.SiteAccountHostResultV1
		if err := hostDownloaderCall(t, worker, "source_accounts", plugins.SiteAccountHostRequestV1{ID: site.ID, Latest: true}, &old); err != nil || len(old.Items) != 1 || !bytes.Contains(old.Items[0].Fields, []byte(`"upload":17.0`)) {
			t.Fatal("err-skip changed old SQL Float row", old, err)
		}
	}
	// Exact huge unread count reaches the existing message owner once.
	var notices int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='notification'`).Scan(&notices); err != nil || notices != 1 {
		t.Fatal("wide unread summary missing", notices, err)
	}
	snapshot := siteSnapshot{SourceData: &sites.SourceUserData{}}
	raw := json.RawMessage(`{"message_unread":` + wide + `}`)
	if err := json.Unmarshal(raw, snapshot.SourceData); err != nil {
		t.Fatal(err)
	}
	step := siteMessageBusinesses(site.Name, site.BaseURL, snapshot)[0]
	message, err := siteMessageBusinessAt(site.Name, site.BaseURL, snapshot, step)
	if err != nil || !bytes.Contains(message, []byte("收到 "+wide+" 条新消息")) {
		t.Fatal("wide unread summary was truncated", string(message), err)
	}
}

func TestSourceSitesCurrentAndHistoricalProjectionTable(t *testing.T) {
	a, cookie, module := ownedModuleFixture(t, true)
	worker := sourceSitesFixture(t, a, cookie)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/favicon.ico" {
			t.Error("terminal module DTO reached native peer", r.URL.Path)
		}
		w.WriteHeader(503)
	}))
	defer peer.Close()
	site := installResourceBrowseSite(t, a, peer.URL)
	raw, err := os.ReadFile("../../testdata/plugin-source-sites-20261105/source-refresh-observations.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Defaults map[string]json.RawMessage `json:"schema_defaults"`
	}
	if json.Unmarshal(raw, &fixture) != nil || len(fixture.Defaults) != 19 {
		t.Fatal("actual schema defaults")
	}
	previous := json.RawMessage(nil)
	for _, row := range []struct {
		name  string
		input string
		skip  bool
	}{
		{"first-null-current-insert-defaults", `{"bonus":null,"upload":null,"download":null,"ratio":null,"seeding":null,"leeching":null,"seeding_size":null,"leeching_size":null,"message_unread":null,"seeding_info":null,"message_unread_contents":null}`, false},
		{"second-null-current-update-nulls", `{"bonus":null,"upload":null,"download":null,"ratio":null,"seeding":null,"leeching":null,"seeding_size":null,"leeching_size":null,"message_unread":null,"seeding_info":null,"message_unread_contents":null}`, false},
		{"default-current-null-storage-canonical", `{}`, false},
		{"explicit-empty-and-text-ID-binding", `{"domain":"","updated_day":"","updated_time":"","userid":7}`, false},
		{"parser-literals-not-storage-namespace", `{"domain":"parser.domain.invalid","updated_day":"1999-01-01","updated_time":"01:02:03","userid":"owned"}`, false},
		{"err-skips-existing-storage", `{"domain":null,"updated_day":null,"err_msg":"owned error"}`, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			current := map[string]json.RawMessage{}
			for k, v := range fixture.Defaults {
				current[k] = v
			}
			var supplied map[string]json.RawMessage
			if json.Unmarshal([]byte(row.input), &supplied) != nil {
				t.Fatal("fixture")
			}
			for k, v := range supplied {
				current[k] = v
			}
			expected, _ := json.Marshal(current)
			sourceSitesSetModule(t, a, cookie, originalBaseSDKLoaded(t, a, ownedModuleID), expected)
			now := sourceSitesData(t, worker, "source_refresh", site.ID)
			if !originalBaseJSONEqual(now.Data, expected) {
				t.Fatal("original current DTO changed", string(now.Data), string(expected))
			}
			var stored plugins.SiteAccountHostResultV1
			if err := hostDownloaderCall(t, worker, "source_accounts", plugins.SiteAccountHostRequestV1{ID: site.ID, Latest: true}, &stored); err != nil || len(stored.Items) != 1 {
				t.Fatal(stored, err)
			}
			fields := stored.Items[0].Fields
			var projected map[string]json.RawMessage
			if json.Unmarshal(fields, &projected) != nil || len(projected["id"]) == 0 {
				t.Fatal("source row lacks actual SQLite identity", string(fields))
			}
			var rowID int64
			if err := a.Store.DB.QueryRow(`SELECT rowid FROM records WHERE kind='site_stats_history' AND id LIKE ?`, site.ID+":%").Scan(&rowID); err != nil || string(projected["id"]) != fmt.Sprint(rowID) {
				t.Fatal("native ID is not real history rowid", string(projected["id"]), rowID, err)
			}
			if row.skip {
				if !bytes.Equal(fields, previous) {
					t.Fatal("error current DTO replaced old stored row", string(fields), string(previous))
				}
				return
			}
			var value map[string]json.RawMessage
			if json.Unmarshal(fields, &value) != nil {
				t.Fatal("stored fields")
			}
			expectedDomain, _ := json.Marshal(sourceindexer.Domain(peer.URL))
			expectedName, _ := json.Marshal(site.Name)
			if !bytes.Equal(value["domain"], expectedDomain) || !bytes.Equal(value["name"], expectedName) || string(value["err_msg"]) != `""` || string(value["updated_day"]) == "null" || string(value["updated_day"]) == `"1999-01-01"` || string(value["updated_time"]) == `"01:02:03"` {
				t.Fatal("source storage-only normalization", string(fields))
			}
			if strings.Contains(row.name, "null-current-") {
				for _, key := range []string{"bonus", "upload", "download", "ratio", "seeding", "leeching", "seeding_size", "leeching_size"} {
					expected := "null"
					if row.name == "first-null-current-insert-defaults" {
						expected = "0.0"
					}
					if string(value[key]) != expected {
						t.Fatal("source INSERT default / UPDATE null", row.name, key, string(fields))
					}
				}
				expected := "null"
				if row.name == "first-null-current-insert-defaults" {
					expected = "0"
				}
				if string(value["message_unread"]) != expected || string(value["seeding_info"]) != "null" || string(value["message_unread_contents"]) != "null" {
					t.Fatal("scalar vs JSON None", string(fields))
				}
			}
			if row.name == "explicit-empty-and-text-ID-binding" && string(value["userid"]) != `"7"` {
				t.Fatal("SQLite TEXT userid was not read as text", string(fields))
			}
			previous = append(json.RawMessage(nil), fields...)
		})
	}
	_ = module
}

func TestSourceSitesProcessLocalStorageClock(t *testing.T) {
	if os.Getenv("SOURCE_SITES_CLOCK_CHILD") != "1" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(executable, "-test.run=^TestSourceSitesProcessLocalStorageClock$", "-test.v")
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "TZ=") && !strings.HasPrefix(value, "SOURCE_SITES_CLOCK_CHILD=") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		cmd.Env = append(cmd.Env, "TZ=UTC", "SOURCE_SITES_CLOCK_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatal("owned independent UTC process", err, string(output))
		}
		return
	}
	fixed := time.Date(2026, 11, 5, 20, 34, 56, 0, time.UTC)
	day, clock := sourceSiteStorageClock(fixed)
	if day != "2026-11-05" || *clock != "20:34:56" {
		t.Fatal("source process-local clock", day, *clock)
	}
	if fixed.In(shanghai()).Format("2006-01-02 15:04:05") != "2026-11-06 04:34:56" {
		t.Fatal("ordinary Shanghai comparison")
	}
	a, cookie, module := ownedModuleFixture(t, true)
	worker := sourceSitesFixture(t, a, cookie)
	site := installResourceBrowseSite(t, a, "http://127.0.0.1:43100")
	sourceSitesSetModule(t, a, cookie, module, json.RawMessage(`{"upload":17,"updated_time":"parser-literal"}`))
	current := sourceSitesData(t, worker, "source_refresh", site.ID)
	if !bytes.Contains(current.Data, []byte(`"updated_time":"parser-literal"`)) {
		t.Fatal("storage clock changed original current DTO", string(current.Data))
	}
	var stored plugins.SiteAccountHostResultV1
	if err := hostDownloaderCall(t, worker, "source_accounts", plugins.SiteAccountHostRequestV1{ID: site.ID, Latest: true}, &stored); err != nil || len(stored.Items) != 1 {
		t.Fatal(stored, err)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(stored.Items[0].Fields, &fields) != nil {
		t.Fatal("fields")
	}
	var at string
	json.Unmarshal(fields["updated_time"], &at)
	if !strings.HasPrefix(at, time.Now().UTC().Format("15:")) {
		t.Fatal("source storage used Shanghai instead of process UTC", at)
	}
	// A cold reader's different location cannot reinterpret persisted clock text.
	savedClock := "20:34:56"
	s := siteSnapshot{Day: "2026-11-05", SourceStoredTime: &savedClock, Connection: managedConnection{CheckedAt: fixed.Format(time.RFC3339Nano)}, SourceData: &sites.SourceUserData{}}
	data, err := statisticData(s, site)
	if err != nil || data.UpdatedTime != savedClock || data.UpdatedDay != "2026-11-05" {
		t.Fatal("stored source local strings reinterpreted", data, err)
	}
	s.SourceStoredTime = nil
	ordinary, err := statisticData(s, site)
	if err != nil || ordinary.UpdatedTime != "04:34:56" {
		t.Fatal("ordinary writer changed", ordinary, err)
	}
}
func TestSourceSitesDateFilterKeepsSourceStringAndLegacyValidation(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	worker := sourceSitesFixture(t, a, cookie)
	var out plugins.SiteAccountHostResultV1
	if err := hostDownloaderCall(t, worker, "source_accounts", plugins.SiteAccountHostRequestV1{Date: "42-01-01"}, &out); err != nil || len(out.Items) != 0 {
		t.Fatal("actual source strftime year rejected", out, err)
	}
	if err := hostDownloaderCall(t, worker, "accounts", plugins.SiteAccountHostRequestV1{Date: "42-01-01"}, nil); err == nil {
		t.Fatal("legacy date validator changed")
	}
}

func TestSourceSitesIntrinsicNativeGuardStaysRejectedAfterRestore(t *testing.T) {
	a := newTestApp(t)
	var afterFirst atomic.Bool
	var denied atomic.Bool
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		afterFirst.Store(true)
		io.WriteString(w, `<a href="logout.php">out</a><a href="userdetails.php?id=7">Me</a><div>上传：1 GB 下载：0 GB</div>`)
	}))
	defer peer.Close()
	site := installResourceBrowseSite(t, a, peer.URL)
	site.Kind = "jiayuan"
	control, err := sites.New(site.Config)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = control.SourceUserStats(context.Background(), sites.UserStatsOptions{ReadMessages: false})
	control.CloseIdleConnections()
	if requests.Load() < 2 {
		t.Fatal("control parser did not reach its real profile request", requests.Load())
	}
	requests.Store(0)
	afterFirst.Store(false)
	a.cfgMu.Lock()
	a.config.Sites[0].Kind = "jiayuan"
	a.cfgMu.Unlock()
	current := json.RawMessage(`null`)
	guard := func(context.Context) error {
		if afterFirst.Swap(false) {
			denied.Store(true)
			return bad("SITE_MESSAGE_SETTINGS_CHANGED", "owned transient intrinsic guard")
		}
		return nil
	}
	_, err = a.collectManagedSiteObserved(context.Background(), site.ID, "", &siteCollectionHooks{OrdinaryEvent: true, SourceResult: &current, RequestGuard: guard})
	if !denied.Load() || !sites.SourceUserRequestRejected(err) || requests.Load() != 1 {
		t.Fatal("real second request guard was not terminal", denied.Load(), requests.Load(), err)
	}
	if err := guard(context.Background()); err != nil {
		t.Fatal("authority was not actually restored", err)
	}
	var count int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind IN ('site_connection','site_stats_latest','site_stats_history')`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected request became successful None observation", count, err)
	}
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM domain_events WHERE json_extract(CAST(payload AS TEXT),'$.event_type')='site.refreshed'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("guard rejection emitted station event", count, err)
	}
}
