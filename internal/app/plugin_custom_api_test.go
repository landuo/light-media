package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"light-media/internal/sites"
)

func enqueueSyntheticPluginHTTP(t *testing.T, a *App) PluginRun {
	t.Helper()
	ctx := context.Background()
	installation, err := a.pluginStore.Installation(ctx, "SiteStatistic")
	if err != nil {
		t.Fatal(err)
	}
	input := plugins.HTTPRequest{Key: "refresh_by_domain", Method: "GET", Path: "/refresh_by_domain", Params: map[string]string{}, Query: map[string][]string{"domain": {"known.example"}}, CredentialMatches: map[string]bool{"apikey": true}, Headers: map[string]string{}}
	a.pluginMu.Lock()
	_, err = a.enqueuePluginHTTP(ctx, installation, input, pluginAPIScope{Key: input.Key, Method: input.Method, Authority: pluginAPIAuthority{Kind: "key", Proof: a.pluginAPIKeyProof(a.Settings().APIToken)}})
	a.pluginMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestPluginHTTPReceiptRollbackAndActualRestartDoesNotRefetch(t *testing.T) {
	a, _, known, _ := pluginHTTPFixture(t)
	ctx := context.Background()
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER fail_api_receipt BEFORE INSERT ON plugin_site_statistic_operations BEGIN SELECT RAISE(ABORT,'synthetic late receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	run := enqueueSyntheticPluginHTTP(t, a)
	a.executePluginRun(ctx, run)
	failed, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || failed.State != "failed" || known.Load() != 2 {
		t.Fatal("late failure path", failed.State, known.Load(), err)
	}
	for _, kind := range []string{"site_connection", "site_stats_latest", "site_stats_history"} {
		var count int
		a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind=?`, kind).Scan(&count)
		if count != 0 {
			t.Fatal("API published data before its receipt", kind)
		}
	}
	var facts int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM domain_events`).Scan(&facts)
	if facts != 0 {
		t.Fatal("rolled back API published an event")
	}
	a.Store.DB.Exec(`DROP TRIGGER fail_api_receipt`)
	run = enqueueSyntheticPluginHTTP(t, a)
	a.executePluginRun(ctx, run)
	finished, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || finished.State != "succeeded" || known.Load() != 4 {
		t.Fatal("actual API did not collect", finished.State, known.Load(), err)
	}
	a.Store.DB.Exec(`UPDATE plugin_runs SET state='running',result=NULL WHERE id=?`, run.ID)
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, err := reopened.pluginStore.Run(ctx, run.ID)
	if err != nil || recovered.State != "succeeded" || string(recovered.Result) != string(finished.Result) || known.Load() != 4 {
		t.Fatal("confirmed API was replayed on restart", recovered.State, known.Load(), err)
	}
	if err := reopened.validatePluginHTTPRun(ctx, recovered); err != nil {
		t.Fatal("restarted API grant corrupted", err)
	}
}

func TestPluginHTTPRevocationWhileCollectingRollsBackPublication(t *testing.T) {
	a, _, _, _ := pluginHTTPFixture(t)
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.php" {
			close(entered)
			<-release
		}
		fmt.Fprint(w, `<a href="logout.php">退出</a><a href="userdetails.php?id=7">synthetic</a><div>上传：2 GB 下载：1 GB 总做种数：0 总做种体积：0 B</div>`)
	}))
	defer server.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	c := a.Settings()
	c.Sites[0].BaseURL = server.URL
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	run := enqueueSyntheticPluginHTTP(t, a)
	done := make(chan struct{})
	go func() { a.executePluginRun(ctx, run); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("API request did not start")
	}
	c = a.Settings()
	c.APIToken = "synthetic-revoked-during-request"
	if err := a.saveSettings(ctx, c); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("API worker did not finish")
	}
	finished, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || finished.State != "failed" || finished.ErrorCode != "PLUGIN_API_AUTH_REVOKED" {
		t.Fatal("revoked request committed", finished.State, finished.ErrorCode, err)
	}
	var count int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind IN ('site_connection','site_stats_latest','site_stats_history')`).Scan(&count)
	if count != 0 {
		t.Fatal("revoked read published account data")
	}
}

const syntheticPluginAPIKey = "synthetic-api-reference-key-2026"

func pluginHTTPFixture(t *testing.T) (*App, *http.Cookie, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	known, failed := &atomic.Int32{}, &atomic.Int32{}
	goodServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		known.Add(1)
		if r.Header.Get("Cookie") != "synthetic-site-cookie" {
			t.Error("configured site credential missing")
		}
		fmt.Fprint(w, `<a href="logout.php">退出</a><a href="userdetails.php?id=7">synthetic</a><div>上传：2 GB 下载：1 GB 总做种数：0 总做种体积：0 B</div><table><tr><td>等级</td><td>Power User</td></tr></table>`)
	}))
	badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { failed.Add(1); http.Error(w, "synthetic failure", 503) }))
	t.Cleanup(goodServer.Close)
	t.Cleanup(badServer.Close)
	for _, site := range []SiteConfig{
		{Config: sites.Config{ID: "known", Kind: "jiayuan", Name: "合成站点", BaseURL: goodServer.URL, Cookie: "synthetic-site-cookie"}, SiteOptions: SiteOptions{Domain: "known.example"}, Enabled: false},
		{Config: sites.Config{ID: "failed", Kind: "jiayuan", Name: "合成失败站", BaseURL: badServer.URL, Cookie: "synthetic-site-cookie"}, SiteOptions: SiteOptions{Domain: "failed.example"}, Enabled: false},
	} {
		if _, err := a.saveManagedSite(context.Background(), site); err != nil {
			t.Fatal(err)
		}
	}
	c := a.Settings()
	c.APIToken = syntheticPluginAPIKey
	if err := a.saveSettings(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	statisticInstall(t, a, cookie)
	return a, cookie, known, failed
}

func TestPluginHTTPOriginalASGIContractActualSDKAndCollection(t *testing.T) {
	a, _, known, failed := pluginHTTPFixture(t)
	raw, err := os.ReadFile("../../testdata/plugins/site-statistic-http-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SHA   map[string]string `json:"source_sha256"`
		Cases []struct {
			Name        string            `json:"name"`
			Query       string            `json:"query"`
			Headers     map[string]string `json:"headers"`
			Method      string            `json:"method"`
			Endpoint    string            `json:"endpoint"`
			Body        *string           `json:"body"`
			Version     string            `json:"version"`
			Status      int               `json:"status"`
			Allow       *string           `json:"allow"`
			Result      any               `json:"result"`
			Collections int               `json:"collections"`
		} `json:"cases"`
	}
	if json.Unmarshal(raw, &fixture) != nil || len(fixture.Cases) != 24 || fixture.SHA["plugin"] != "763a88be85185a94082a2f131ed1c80d8306abe24b10ebe9ae95447cfe5da17d" || fixture.SHA["security"] != "7c2f1920295d13a40979acc2f74554bdbb94010663909a281c0904d6e3db5040" {
		t.Fatal("invalid original ASGI oracle")
	}
	a.Start(context.Background())
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	client := server.Client()
	client.Timeout = 20 * time.Second
	for _, prefix := range []string{"", "/light-media"} {
		for _, item := range fixture.Cases {
			t.Run(prefix+"/"+item.Name, func(t *testing.T) {
				body := ""
				if item.Body != nil {
					body = *item.Body
				}
				request, err := http.NewRequest(item.Method, server.URL+prefix+"/api/"+item.Version+"/plugin/SiteStatistic"+item.Endpoint+item.Query, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				for key, value := range item.Headers {
					request.Header.Set(key, value)
				}
				beforeGood, beforeBad := known.Load(), failed.Load()
				response, err := client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				bytes, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if response.StatusCode != item.Status {
					t.Fatalf("status %d, expected %d: %s", response.StatusCode, item.Status, bytes)
				}
				if item.Allow != nil && response.Header.Get("Allow") != *item.Allow {
					t.Fatal("method metadata changed", response.Header)
				}
				var actual any
				if len(bytes) > 0 && json.Unmarshal(bytes, &actual) != nil {
					t.Fatal("non-JSON response", string(bytes))
				}
				expectedRaw, _ := json.Marshal(item.Result)
				var expected any
				json.Unmarshal(expectedRaw, &expected)
				if data, ok := actual.(map[string]any); ok {
					if values, ok := data["data"].(map[string]any); ok && data["success"] == true {
						for _, field := range []string{"updated_day", "updated_time"} {
							if _, ok := values[field].(string); !ok {
								t.Fatal("missing observed timestamp")
							}
							values[field] = expected.(map[string]any)["data"].(map[string]any)[field]
						}
					}
				}
				if !reflect.DeepEqual(actual, expected) {
					t.Fatalf("response differs from original\nactual: %s\nexpected: %s", bytes, expectedRaw)
				}
				deltaGood, deltaBad := known.Load()-beforeGood, failed.Load()-beforeBad
				if item.Collections == 0 && (deltaGood != 0 || deltaBad != 0) || item.Collections == 1 && deltaGood+deltaBad == 0 {
					t.Fatal("authentication/business gate performed wrong network work", deltaGood, deltaBad, item.Collections)
				}
				if response.Header.Get("X-Request-ID") == "" {
					t.Fatal("request correlation missing")
				}
			})
		}
	}
	// Public metadata never exposes the actual global/site key, private input,
	// or a public API response by accident.
	runs, err := a.pluginStore.Runs(context.Background(), "SiteStatistic", 100)
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(runs)
	if strings.Contains(string(public), syntheticPluginAPIKey) || strings.Contains(string(public), "synthetic-site-cookie") {
		t.Fatal("public run metadata disclosed credentials")
	}
	for _, run := range runs {
		if run.Action == "api" && strings.Contains(string(run.Input), syntheticPluginAPIKey) {
			t.Fatal("global API key crossed into worker input")
		}
	}
}

func TestPluginHTTPAuthorityPriorityAndNativeSessionBoundary(t *testing.T) {
	a, cookie, _, _ := pluginHTTPFixture(t)
	validKey := syntheticPluginAPIKey
	for _, item := range []struct {
		name, auth, query, authorization, key string
		native, cookie                        bool
		status                                int
	}{
		{name: "anonymous", auth: "anonymous"},
		{name: "bear-key", auth: "bear", key: validKey},
		{name: "bear-token-query", auth: "bear", query: "token=" + validKey},
		{name: "legacy-cookie-rejected", auth: "bear", cookie: true, status: 401},
		{name: "native-cookie", auth: "bear", native: true, cookie: true},
		{name: "bear-session", auth: "bear", authorization: "Bearer " + cookie.Value},
		{name: "invalid-bearer-wins-key", auth: "bear", authorization: "Bearer synthetic-invalid", key: validKey, status: 403},
		{name: "global-key-is-not-bearer", auth: "bear", authorization: "Bearer " + validKey, status: 403},
		{name: "api-key-cookie-rejected", auth: "apikey", native: true, cookie: true, status: 401},
	} {
		t.Run(item.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://example.test/?"+item.query, nil)
			if item.cookie {
				r.AddCookie(cookie)
			}
			r.Header.Set("Authorization", item.authorization)
			r.Header.Set("X-API-KEY", item.key)
			authority, failure := a.pluginHTTPAuthority(r, item.auth, item.native)
			if item.status == 0 {
				if failure != nil || authority.Kind == "" {
					t.Fatal(authority, failure)
				}
			} else if failure == nil || failure.Status != item.status {
				t.Fatal(authority, failure)
			}
		})
	}
	r := httptest.NewRequest("POST", "http://example.test/", nil)
	r.AddCookie(cookie)
	r.Header.Set("Origin", "https://other.invalid")
	if _, failure := a.pluginHTTPAuthority(r, "bear", true); failure == nil || failure.Status != 403 {
		t.Fatal("cross-origin native cookie grant accepted")
	}
}

func TestPluginHTTPSettingsTokenRemainsEncryptedAndRedacted(t *testing.T) {
	a, cookie, _, _ := pluginHTTPFixture(t)
	settings := callTest(t, a.Handler(), "GET", "/api/settings", "", cookie, "")
	if settings.Code != 200 || strings.Contains(settings.Body.String(), syntheticPluginAPIKey) || !strings.Contains(settings.Body.String(), `"api_token":true`) {
		t.Fatal("ordinary settings leaked token", settings.Code)
	}
	c := a.Settings()
	c.APIToken = ""
	a.preserveSecrets(&c)
	if c.APIToken != syntheticPluginAPIKey {
		t.Fatal("blank ordinary settings erased the key")
	}
	if value := cleanWithSettings("unlabelled "+syntheticPluginAPIKey, a.Settings()); strings.Contains(value, syntheticPluginAPIKey) {
		t.Fatal("key leaked through diagnostics")
	}
	reveal := callTest(t, a.Handler(), "POST", "/api/settings/api-token/reveal", `{}`, cookie, "")
	if reveal.Code != 200 || !strings.Contains(reveal.Body.String(), syntheticPluginAPIKey) {
		t.Fatal("explicit owner copy unavailable", reveal.Code)
	}
	if r := callTest(t, a.Handler(), "POST", "/api/settings/api-token/reveal", `{}`, nil, ""); r.Code != 401 {
		t.Fatal("anonymous key disclosure allowed")
	}
	var sealed []byte
	if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='config' AND id='main'`).Scan(&sealed); err != nil || strings.Contains(string(sealed), syntheticPluginAPIKey) {
		t.Fatal("key stored in plaintext", err)
	}
	for _, token := range []string{"too-short", strings.Repeat("x", 513), "synthetic-key-with\nnewline"} {
		c := a.Settings()
		c.APIToken = token
		if validateSettings(c) == nil {
			t.Fatal("invalid key accepted")
		}
	}
}

func TestPluginHTTPForgeCannotChooseAnotherDomainOrAnonymousGrant(t *testing.T) {
	a, _, known, _ := pluginHTTPFixture(t)
	ctx := context.Background()
	installation, _ := a.pluginStore.Installation(ctx, "SiteStatistic")
	input := plugins.HTTPRequest{Key: "refresh_by_domain", Method: "GET", Path: "/refresh_by_domain", Params: map[string]string{}, Query: map[string][]string{"domain": {"known.example"}}, CredentialMatches: map[string]bool{"apikey": true}, Headers: map[string]string{}}
	a.pluginMu.Lock()
	run, err := a.enqueuePluginHTTP(ctx, installation, input, pluginAPIScope{Key: input.Key, Method: input.Method, Authority: pluginAPIAuthority{Kind: "key", Proof: a.pluginAPIKeyProof(syntheticPluginAPIKey)}})
	a.pluginMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	run, err = a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	identity := plugins.Identity{PluginID: "SiteStatistic", InstanceID: installation.InstanceID, Version: installation.Version}
	if _, err := a.pluginHostCall(ctx, installation, &run, identity, "site.statistics.refresh_domain", json.RawMessage(`{"domain":"other.example"}`)); pluginErrorCode(err) != "PLUGIN_API_SCOPE_DENIED" {
		t.Fatal("worker changed target", err)
	}
	if known.Load() != 0 {
		t.Fatal("forged target issued a request")
	}
	// A forged anonymous scope does not override the compiled route policy.
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	forged, err := json.Marshal(pluginAPIScope{Key: input.Key, Method: input.Method, Authority: pluginAPIAuthority{Kind: "anonymous"}})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := a.pluginStore.seal("api_scope", run.InstanceID, run.ID, forged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE plugin_api_calls SET scope=? WHERE run_id=?`, sealed, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.validatePluginHTTPRunTx(ctx, tx, run); pluginErrorCode(err) != "PLUGIN_API_SCOPE_DENIED" {
		t.Fatal("anonymous scope weakened the compiled route", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	c := a.Settings()
	c.APIToken = "synthetic-rotated-api-key"
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginHostCall(ctx, installation, &run, identity, "site.statistics.refresh_domain", json.RawMessage(`{"domain":"known.example"}`)); pluginErrorCode(err) != "PLUGIN_API_AUTH_REVOKED" {
		t.Fatal("revoked API credential remained usable", err)
	}
	if known.Load() != 0 {
		t.Fatal("revoked request performed a site read")
	}
	// API-key identity is never accepted on the regular settings management API.
	r := httptest.NewRequest("GET", "http://test/api/settings", nil)
	r.Header.Set("X-API-KEY", c.APIToken)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("plugin credential expanded management authority")
	}
}

func TestPluginHTTPRegistrationAndQueueTransaction(t *testing.T) {
	a, _, known, _ := pluginHTTPFixture(t)
	ctx := context.Background()
	request := func(path string) int {
		r := httptest.NewRequest("GET", "http://test"+path, nil)
		r.Header.Set("X-API-KEY", syntheticPluginAPIKey)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w.Code
	}
	// Staged imports are not live registrations; disabling the business flag
	// itself is covered by the original ASGI oracle and still permits this API.
	if _, err := a.Store.DB.Exec(`UPDATE plugin_installations SET desired_enabled=0 WHERE instance_id='SiteStatistic'`); err != nil {
		t.Fatal(err)
	}
	if code := request("/api/v1/plugin/SiteStatistic/refresh_by_domain"); code != 404 {
		t.Fatal("staged installation registered a live API", code)
	}
	if _, err := a.Store.DB.Exec(`UPDATE plugin_installations SET desired_enabled=1 WHERE instance_id='SiteStatistic'`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER fail_api_grant BEFORE INSERT ON plugin_api_calls BEGIN SELECT RAISE(ABORT,'synthetic grant failure'); END`); err != nil {
		t.Fatal(err)
	}
	installation, _ := a.pluginStore.Installation(ctx, "SiteStatistic")
	input := plugins.HTTPRequest{Key: "refresh_by_domain", Method: "GET", Path: "/refresh_by_domain"}
	a.pluginMu.Lock()
	_, err := a.enqueuePluginHTTP(ctx, installation, input, pluginAPIScope{Key: input.Key, Method: input.Method, Authority: pluginAPIAuthority{Kind: "key", Proof: a.pluginAPIKeyProof(syntheticPluginAPIKey)}})
	a.pluginMu.Unlock()
	if err == nil {
		t.Fatal("late grant failure was ignored")
	}
	var count int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE action='api'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("queue survived without its grant", count, err)
	}
	if err := a.pluginStore.Uninstall(ctx, "SiteStatistic"); err != nil {
		t.Fatal(err)
	}
	if code := request("/api/plugins/SiteStatistic/api/refresh_by_domain"); code != 404 || known.Load() != 0 {
		t.Fatal("uninstalled API still issued requests", code, known.Load())
	}
}

func TestPluginHTTPInternalFlagPatchAndOldStoredManifest(t *testing.T) {
	a, cookie, known, _ := pluginHTTPFixture(t)
	ctx := context.Background()
	// Reproduce an installation created before this binary gained an API.
	installation, err := a.pluginStore.Installation(ctx, "SiteStatistic")
	if err != nil {
		t.Fatal(err)
	}
	var old plugins.Manifest
	if err = json.Unmarshal(installation.Manifest, &old); err != nil {
		t.Fatal(err)
	}
	old.APIs = nil
	old.Capabilities = []string{"site.statistics.configure", "site.statistics.refresh", "site.statistics.notify"}
	oldRaw, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.Install(ctx, PluginInstallation{InstanceID: installation.InstanceID, PluginID: installation.PluginID, Version: installation.Version, ManifestSchemaVersion: installation.ManifestSchemaVersion, Manifest: oldRaw, DesiredEnabled: true}); err != nil {
		t.Fatal(err)
	}
	statisticSave(t, a, cookie, builtin.SiteStatisticConfiguration{OnlyOnce: true, DashboardType: "today"})
	installation, err = a.pluginStore.Installation(ctx, "SiteStatistic")
	if err != nil {
		t.Fatal(err)
	}
	input := plugins.HTTPRequest{RequestID: "req_synthetic-api-correlation", Key: "refresh_by_domain", Method: "GET", Path: "/refresh_by_domain", Params: map[string]string{}, Query: map[string][]string{"domain": {"known.example"}}, CredentialMatches: map[string]bool{"apikey": true}, Headers: map[string]string{}}
	a.pluginMu.Lock()
	queued, err := a.enqueuePluginHTTP(ctx, installation, input, pluginAPIScope{Key: input.Key, Method: input.Method, RequestID: input.RequestID, Authority: pluginAPIAuthority{Kind: "key", Proof: a.pluginAPIKeyProof(syntheticPluginAPIKey)}})
	a.pluginMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	configured := statisticExecute(t, a)
	queued, err = a.pluginStore.Run(ctx, queued.ID)
	if err != nil || queued.ConfigRevision != configured.ConfigRevision || queued.RequestID != input.RequestID {
		t.Fatal("internal normalization invalidated pending API grant", err)
	}
	if err := a.validatePluginHTTPRun(ctx, queued); err != nil {
		t.Fatal("normalized grant invalid", err)
	}
	finished := statisticExecute(t, a)
	if finished.ID != queued.ID || known.Load() != 2 {
		t.Fatal("old stored manifest hid native API capability", finished.ID, known.Load())
	}
	// The authenticated global key and site cookie never enter SQLite/WAL in
	// cleartext, including the newly added authorization and API result rows.
	for _, name := range []string{"light-media.db", "light-media.db-wal"} {
		raw, err := os.ReadFile(filepath.Join(a.Dir, name))
		if err != nil && (name == "light-media.db" || !os.IsNotExist(err)) {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), syntheticPluginAPIKey) || strings.Contains(string(raw), "synthetic-site-cookie") {
			t.Fatal("plaintext credential in database", name)
		}
	}
}
