package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	source "light-media/internal/plugins/autosignin"
	signinjournal "light-media/internal/plugins/autosignin/durable"
	"light-media/internal/sites"
)

// Publish an isolated fixture through the same authenticated identity
// reconciliation as site management; never invent the numeric source ID.
func signinSitesPublish(t *testing.T, a *App, config Settings) {
	t.Helper()
	if err := signinSitesPublishConfig(a, config); err != nil {
		t.Fatal(err)
	}
}

// Return publication errors so HTTP handlers can report them to the test's
// main goroutine without calling FailNow from the server goroutine.
func signinSitesPublishConfig(a *App, config Settings) error {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	tx, e := a.Store.DB.BeginTx(context.Background(), nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = a.reconcileNativeSiteIdentitiesTx(context.Background(), tx, a.Settings(), config); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	a.cfgMu.Lock()
	a.config = config
	a.cfgMu.Unlock()
	return nil
}
func signinSitesNative(address string) SiteConfig {
	return SiteConfig{Config: sites.Config{ID: "opaque-not-a-source-number", Kind: "ubits", Name: "Synthetic native", BaseURL: address, Cookie: "sid=synthetic-native", UserAgent: "Owned-signin-UA"}, Enabled: true}
}
func signinSitesDependencies(t *testing.T, f *signinSQLFixture) signinjournal.Dependencies {
	t.Helper()
	d, e := f.a.autoSignInSiteDependencies(context.Background(), f.o, f.run)
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func TestAutoSignInTrustedSitesNumericAndCustomStringsUseOnePrivateSnapshot(t *testing.T) {
	ctx := context.Background()
	f := signinSQLSetup(t)
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.Header.Get("Cookie") != "sid=synthetic-native" || r.Header.Get("User-Agent") != "Owned-signin-UA" {
			t.Error("host credentials changed")
		}
		fmt.Fprint(w, `<a href="logout.php">退出</a><p>这是您的首次签到，本次签到获得10个魔力值。</p>`)
	}))
	defer server.Close()
	settings := f.a.Settings()
	settings.Sites = []SiteConfig{signinSitesNative(server.URL)}
	signinSitesPublish(t, f.a, settings)
	identity, e := f.a.Store.nativeSiteByAppID(ctx, settings.Sites[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	customID := fmt.Sprint(identity.SourceID)
	installSiteControlPeerFixture(t, f.a, "CustomSites", map[string]any{"enabled": true, "sites": []any{map[string]any{"id": customID, "name": "Synthetic custom", "url": "https://custom.example.test/", "cookie": "synthetic-custom-secret"}}})
	deps := signinSitesDependencies(t, f)
	inventory, e := deps.Inventory(ctx)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(inventory)
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "cookie") || strings.Contains(string(raw), server.URL) {
		t.Fatal("private capability escaped in inventory")
	}
	nativeID := inventory.Configured[0]["id"]
	if _, ok := nativeID.(json.Number); !ok {
		t.Fatal("native source ID not numeric", nativeID)
	}
	if inventory.Custom[0]["id"] != customID {
		t.Fatal("custom ID lost string identity")
	}
	bound, e := deps.Factory(ctx, source.Object{"id": nativeID, "url": "http://attacker.invalid/", "cookie": "injected"})
	if e != nil {
		t.Fatal(e)
	}
	if bound.Site["url"] != server.URL || bound.Site["cookie"] != "sid=synthetic-native" {
		t.Fatal("worker fields became a grant")
	}
	// The production Controller binds this sink. Exercise the real durable
	// lane here rather than making an absent statistics host appear successful.
	f.start(t)
	execution := &signinjournal.Execution{Store: f.j, Binding: f.o.binding, RunID: f.run.ID, Epoch: f.o.binding.Epoch, DigestKey: [32]byte{1, 2, 3}}
	lane := execution.Lane("owned-sites-statistics", "")
	bound.Hooks.Stats = func(ctx context.Context, domain string, success bool, seconds int) error {
		return lane.StatisticClock(ctx, domain, success, seconds, func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) })
	}
	result, e := (source.SiteVisitor{Adapters: source.InstalledAdapterRegistry(), Hooks: bound.Hooks}).Visit(ctx, bound.Site, "签到", false)
	if e != nil || !result.Success || count.Load() != 1 {
		t.Fatal("owned HTTP did not establish business success", result, e, count.Load())
	}
	state, e := f.j.State(ctx, f.o.binding)
	if e != nil || state.Stats[source.DomainKey(server.URL)].Success != 1 {
		t.Fatal("successful visit was not durably counted", e, state.Stats)
	}
	custom, e := deps.Factory(ctx, source.Object{"id": customID})
	if e != nil || custom.Site["name"] != "Synthetic custom" {
		t.Fatal("numeric/string collision", e)
	}
	if _, e = deps.Factory(ctx, source.Object{"id": settings.Sites[0].ID}); !errors.Is(e, source.ErrSiteTargetDenied) {
		t.Fatal("opaque App ID accepted", e)
	}
	inventory.Configured[0]["name"] = "mutated"
	bound.Site["cookie"] = "mutated"
	again, _ := deps.Inventory(ctx)
	if again.Configured[0]["name"] != "Synthetic native" {
		t.Fatal("public mutable alias")
	}
	againBound, e := deps.Factory(ctx, source.Object{"id": nativeID})
	if e != nil || againBound.Site["cookie"] != "sid=synthetic-native" {
		t.Fatal("private mutable alias", e)
	}
}
func TestAutoSignInTrustedSitesEveryIORechecksCredentialsAndOwner(t *testing.T) {
	for _, mutation := range []string{"cookie", "origin", "proxy", "custom_revision", "run"} {
		t.Run(mutation, func(t *testing.T) {
			ctx := context.Background()
			f := signinSQLSetup(t)
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); fmt.Fprint(w, "should not be reached") }))
			defer server.Close()
			settings := f.a.Settings()
			settings.Sites = []SiteConfig{signinSitesNative(server.URL)}
			signinSitesPublish(t, f.a, settings)
			installSiteControlPeerFixture(t, f.a, "CustomSites", map[string]any{"enabled": false, "sites": []any{}})
			deps := signinSitesDependencies(t, f)
			inventory, _ := deps.Inventory(ctx)
			bound, e := deps.Factory(ctx, inventory.Configured[0])
			if e != nil {
				t.Fatal(e)
			}
			switch mutation {
			case "cookie":
				settings.Sites[0].Cookie = "replacement"
				signinSitesPublish(t, f.a, settings)
			case "origin":
				settings.Sites[0].BaseURL = "http://127.0.0.1:1"
				signinSitesPublish(t, f.a, settings)
			case "proxy":
				settings.ProxyHost = "http://127.0.0.1:2"
				signinSitesPublish(t, f.a, settings)
			case "custom_revision":
				if _, e = f.a.pluginStore.SaveConfig(ctx, "CustomSites", 1, map[string]any{"enabled": false, "sites": []any{}}); e != nil {
					t.Fatal(e)
				}
			case "run":
				if _, e = f.a.Store.DB.Exec(`UPDATE plugin_runs SET state='failed' WHERE id=?`, f.run.ID); e != nil {
					t.Fatal(e)
				}
			}
			_, e = bound.Hooks.HTTP.Do(ctx, source.PageRequest{Method: "GET", URL: server.URL + "/attendance.php"})
			if !errors.Is(e, signinjournal.ErrStale) || count.Load() != 0 {
				t.Fatal("changed binding performed network", e, count.Load())
			}
		})
	}
}
func TestAutoSignInTrustedSitesCrossOriginAndUnsupportedCapabilities(t *testing.T) {
	ctx := context.Background()
	f := signinSQLSetup(t)
	var leaked atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer other.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, http.StatusFound) }))
	defer origin.Close()
	settings := f.a.Settings()
	settings.Sites = []SiteConfig{signinSitesNative(origin.URL)}
	signinSitesPublish(t, f.a, settings)
	deps := signinSitesDependencies(t, f)
	inventory, _ := deps.Inventory(ctx)
	bound, e := deps.Factory(ctx, inventory.Configured[0])
	if e != nil {
		t.Fatal(e)
	}
	if _, e = bound.Hooks.HTTP.Do(ctx, source.PageRequest{Method: "GET", URL: other.URL}); e == nil {
		t.Fatal("arbitrary cross-origin capability")
	}
	_, _ = bound.Hooks.HTTP.Do(ctx, source.PageRequest{Method: "GET", URL: origin.URL})
	if leaked.Load() != 0 {
		t.Fatal("redirect leaked credential-bearing request")
	}
	for _, kind := range []string{"browser", "ocr", "tjupt"} {
		t.Run(kind, func(t *testing.T) {
			settings.Sites[0] = signinSitesNative(origin.URL)
			settings.ProxyHost = ""
			switch kind {
			case "browser":
				settings.Sites[0].Render = true
			case "ocr":
				settings.Sites[0].BaseURL = "https://hdsky.me"
			case "tjupt":
				settings.Sites[0].BaseURL = "https://www.tjupt.org"
			}
			signinSitesPublish(t, f.a, settings)
			deps := signinSitesDependencies(t, f)
			inventory, _ := deps.Inventory(ctx)
			if _, e := deps.Factory(ctx, inventory.Configured[0]); e == nil {
				t.Fatal("unsupported capability silently enabled")
			}
		})
	}
}
func TestAutoSignInTrustedSitesRejectsIdentityCorruptionAndReservedCustomID(t *testing.T) {
	t.Run("mapping", func(t *testing.T) {
		f := signinSQLSetup(t)
		settings := f.a.Settings()
		settings.Sites = []SiteConfig{signinSitesNative("http://127.0.0.1:1")}
		signinSitesPublish(t, f.a, settings)
		if _, e := f.a.Store.DB.Exec(`UPDATE native_site_identities SET proof='tampered'`); e != nil {
			t.Fatal(e)
		}
		if _, e := f.a.autoSignInSiteDependencies(context.Background(), f.o, f.run); e == nil {
			t.Fatal("tampered ID mapping accepted")
		}
	})
	t.Run("reserved custom", func(t *testing.T) {
		f := signinSQLSetup(t)
		installSiteControlPeerFixture(t, f.a, "CustomSites", map[string]any{"enabled": true, "sites": []any{map[string]any{"id": "all", "url": "https://custom.example.test"}}})
		if _, e := f.a.autoSignInSiteDependencies(context.Background(), f.o, f.run); e == nil {
			t.Fatal("reserved ID accepted")
		}
	})
}

func TestAutoSignInFormInventoryBeforeInstallationDoesNotCreateOwner(t *testing.T) {
	store, plugins := pluginStoreFixture(t)
	a := &App{Store: store, pluginStore: plugins, autoSignInEnvironment: func() []string { return nil }}
	settings := a.Settings()
	settings.Sites = []SiteConfig{signinSitesNative("http://127.0.0.1:1")}
	signinSitesPublish(t, a, settings)
	inventory, err := a.autoSignInFormInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Configured) != 1 {
		t.Fatal("missing trusted pre-install option")
	}
	raw, _ := json.Marshal(inventory)
	if strings.Contains(string(raw), "sid=") || strings.Contains(string(raw), "127.0.0.1") {
		t.Fatal("private form fields")
	}
	var n int
	if err = store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_signin_owners`).Scan(&n); err != nil && !strings.Contains(err.Error(), "no such table") {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("form created durable owner")
	}
}

func TestAutoSignInTrustedSitesEveryAPIIORechecksOriginalAdmission(t *testing.T) {
	for _, mutation := range []string{"sealed_scope", "rotated_key"} {
		t.Run(mutation, func(t *testing.T) {
			ctx := context.Background()
			f := signinSQLSetup(t)
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1) }))
			defer server.Close()
			settings := f.a.Settings()
			settings.APIToken = "synthetic-signin-original-api-key"
			settings.Sites = []SiteConfig{signinSitesNative(server.URL)}
			signinSitesPublish(t, f.a, settings)
			if err := f.a.pluginStore.FinishRun(ctx, f.run.ID, "succeeded", "", json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			in, err := f.a.pluginStore.Installation(ctx, f.run.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			request := plugins.HTTPRequest{Key: "signin_by_domain", Method: "GET", Path: "/signin_by_domain", Params: map[string]string{}, Query: map[string][]string{"url": {server.URL}}, CredentialMatches: map[string]bool{"apikey": true}, Headers: map[string]string{}}
			// Match the real ingress: the original matched key's HMAC belongs only to
			// the sealed host scope, never the worker request or its CredentialMatches.
			proof := f.a.pluginAPIKeyProof(settings.APIToken)
			run, err := f.a.enqueuePluginHTTP(ctx, in, request, pluginAPIScope{Key: request.Key, Method: request.Method, Authority: pluginAPIAuthority{Kind: "anonymous"}, CredentialProofs: map[string]string{"apikey": proof}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.a.Store.DB.Exec(`UPDATE plugin_runs SET state='running' WHERE id=?`, run.ID); err != nil {
				t.Fatal(err)
			}
			run, err = f.a.pluginStore.Run(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(run.Input), settings.APIToken) || strings.Contains(string(run.Input), proof) {
				t.Fatal("API secret/proof escaped host scope")
			}
			deps, err := f.a.autoSignInSiteDependencies(ctx, f.o, run)
			if err != nil {
				t.Fatal(err)
			}
			inventory, _ := deps.Inventory(ctx)
			bound, err := deps.Factory(ctx, inventory.Configured[0])
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "sealed_scope":
				if _, err = f.a.Store.DB.Exec(`UPDATE plugin_api_calls SET scope=x'00' WHERE run_id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
			case "rotated_key":
				// Only the API key changes. The site snapshot, run and owner remain valid;
				// this must be denied by the original API authority, not a stale site.
				settings.APIToken = "synthetic-signin-rotated-api-key"
				signinSitesPublish(t, f.a, settings)
			}
			_, err = bound.Hooks.HTTP.Do(ctx, source.PageRequest{Method: "GET", URL: server.URL})
			if err == nil || count.Load() != 0 {
				t.Fatal("revoked API admission reached network", err, count.Load())
			}
			if mutation == "rotated_key" && pluginErrorCode(err) != "PLUGIN_API_AUTH_REVOKED" {
				t.Fatal("rotation did not revoke original key proof", err)
			}
		})
	}
}

func TestAutoSignInTrustedSitesRedirectRechecksOriginalAPIAdmission(t *testing.T) {
	signinSitesRedirectRevocation(t, true)
}

func TestAutoSignInTrustedSitesRedirectRechecksServiceSnapshot(t *testing.T) {
	signinSitesRedirectRevocation(t, false)
}

// Real same-origin 302 hops exercise net/http's redirect loop. Revocation is
// committed inside /first before its Location header is sent, with no sleeps
// or edits to the already admitted run or its bound site snapshot.
func signinSitesRedirectRevocation(t *testing.T, api bool) {
	t.Helper()
	ctx := context.Background()
	f := signinSQLSetup(t)
	var first, second atomic.Int32
	saved := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/first":
			first.Add(1)
			settings := f.a.Settings()
			if api {
				// Rotate only the API key; the trusted site snapshot is unchanged.
				settings.APIToken = "synthetic-signin-redirect-rotated-key"
			} else {
				settings.Sites = append([]SiteConfig(nil), settings.Sites...)
				settings.Sites[0].Cookie = "sid=synthetic-redirect-replacement"
			}
			err := signinSitesPublishConfig(f.a, settings)
			saved <- err
			if err != nil {
				http.Error(w, "fixture publication failed", http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, "/second", http.StatusFound)
		case "/second":
			second.Add(1)
			fmt.Fprint(w, "must not be reached after revocation")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	settings := f.a.Settings()
	settings.APIToken = "synthetic-signin-redirect-original-key"
	settings.Sites = []SiteConfig{signinSitesNative(server.URL)}
	signinSitesPublish(t, f.a, settings)
	if err := f.a.pluginStore.FinishRun(ctx, f.run.ID, "succeeded", "", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	var run PluginRun
	var err error
	if api {
		in, readErr := f.a.pluginStore.Installation(ctx, f.run.InstanceID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		request := plugins.HTTPRequest{Key: "signin_by_domain", Method: "GET", Path: "/signin_by_domain", Params: map[string]string{}, Query: map[string][]string{"url": {server.URL}}, CredentialMatches: map[string]bool{"apikey": true}, Headers: map[string]string{}}
		proof := f.a.pluginAPIKeyProof(settings.APIToken)
		run, err = f.a.enqueuePluginHTTP(ctx, in, request, pluginAPIScope{Key: request.Key, Method: request.Method, Authority: pluginAPIAuthority{Kind: "anonymous"}, CredentialProofs: map[string]string{"apikey": proof}})
	} else {
		run, err = f.a.pluginStore.EnqueueRun(ctx, f.run.InstanceID, "service", json.RawMessage(`{}`))
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.a.Store.DB.Exec(`UPDATE plugin_runs SET state='running' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	run, err = f.a.pluginStore.Run(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	deps, err := f.a.autoSignInSiteDependencies(ctx, f.o, run)
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := deps.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := deps.Factory(ctx, inventory.Configured[0])
	if err != nil {
		t.Fatal(err)
	}
	_, err = bound.Hooks.HTTP.Do(ctx, source.PageRequest{Method: "GET", URL: server.URL + "/first"})
	if first.Load() != 1 || second.Load() != 0 {
		t.Fatal("redirect revocation did not stop the second request", err, first.Load(), second.Load())
	}
	if saveErr := <-saved; saveErr != nil {
		t.Fatal("redirect fixture publication failed", saveErr)
	}
	if api {
		if pluginErrorCode(err) != "PLUGIN_API_AUTH_REVOKED" {
			t.Fatal("redirect lost the original API admission revocation", err)
		}
	} else if !errors.Is(err, signinjournal.ErrStale) {
		t.Fatal("redirect lost the stale site snapshot error", err)
	}
	if bound.Site["cookie"] != "sid=synthetic-native" {
		t.Fatal("revocation rewrote the originally bound credentials")
	}
}

// The provider never reads process environment, and changes are synchronized
// with redirect handlers to keep the revocation tests race-clean.
type signinSyntheticEnvironment struct {
	mu      sync.Mutex
	entries []string
	calls   int
}

func (e *signinSyntheticEnvironment) read() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	return append([]string(nil), e.entries...)
}
func (e *signinSyntheticEnvironment) set(entries ...string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.entries = append([]string(nil), entries...)
}

func TestAutoSignInProxySnapshotFiltersAndFreezesAllRoutingInputs(t *testing.T) {
	f := signinSQLSetup(t)
	environment := &signinSyntheticEnvironment{}
	environment.set("HTTP_PROXY=http://127.0.0.1:1101", "http_proxy=", "HTTPS_PROXY=socks5h://127.0.0.1:1102", "http://host.test_proxy=http://127.0.0.1:1103", "No_PrOxY=", "REQUEST_METHOD=POST", "request_method=ignored", "UNRELATED=synthetic-not-routing")
	f.a.autoSignInEnvironment = environment.read
	first, err := f.a.readAutoSignInSites(context.Background(), f.o)
	if err != nil {
		t.Fatal(err)
	}
	if environment.calls != 1 {
		t.Fatal("snapshot reread provider")
	}
	want := []string{"HTTP_PROXY=http://127.0.0.1:1101", "http_proxy=", "HTTPS_PROXY=socks5h://127.0.0.1:1102", "http://host.test_proxy=http://127.0.0.1:1103", "No_PrOxY=", "REQUEST_METHOD=POST"}
	if !reflect.DeepEqual(first.Environment, want) || first.Proxy.HTTP != "http://127.0.0.1:1101" || first.Proxy.HTTPS != "socks5h://127.0.0.1:1102" {
		t.Fatal("frozen routing inputs differ")
	}
	environment.set(append(want, "UNRELATED=changed")...)
	second, err := f.a.readAutoSignInSites(context.Background(), f.o)
	if err != nil || first.Revision != second.Revision {
		t.Fatal("unrelated environment changed authority", err)
	}
	environment.set(append(want, "all_proxy=socks5h://127.0.0.1:1104")...)
	third, err := f.a.readAutoSignInSites(context.Background(), f.o)
	if err != nil || first.Revision == third.Revision {
		t.Fatal("environment omitted from authority", err)
	}
	if !reflect.DeepEqual(first.Environment, want) {
		t.Fatal("snapshot mutated with provider")
	}
	environment.set("HTTP_PROXY=http://127.0.0.1:1101", "HTTP_PROXY=http://127.0.0.1:1105")
	duplicate, err := f.a.readAutoSignInSites(context.Background(), f.o)
	if err != nil || duplicate.Proxy.HTTP != "http://127.0.0.1:1105" || duplicate.Proxy.HTTPS != "http://127.0.0.1:1105" {
		t.Fatal("duplicate synthetic environment did not use last value", err)
	}
}

func TestAutoSignInFactoryRequestsProxyUsesEnvironmentEvenWhenFlagFalse(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			f := signinSQLSetup(t)
			var direct, proxied atomic.Int32
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { direct.Add(1); fmt.Fprint(w, "direct") }))
			defer origin.Close()
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				proxied.Add(1)
				if !r.URL.IsAbs() || r.Header.Get("Cookie") != "sid=synthetic-native" {
					t.Error("proxy lost absolute target or trusted credentials")
				}
				fmt.Fprint(w, "synthetic proxy response")
			}))
			defer proxy.Close()
			environment := &signinSyntheticEnvironment{}
			environment.set("http_proxy=" + proxy.URL)
			f.a.autoSignInEnvironment = environment.read
			settings := f.a.Settings()
			settings.Sites = []SiteConfig{signinSitesNative(origin.URL)}
			settings.Sites[0].Proxy = enabled
			signinSitesPublish(t, f.a, settings)
			deps := signinSitesDependencies(t, f)
			inventory, _ := deps.Inventory(context.Background())
			bound, err := deps.Factory(context.Background(), inventory.Configured[0])
			if err != nil {
				t.Fatal(err)
			}
			_, err = bound.Hooks.HTTP.Do(context.Background(), source.PageRequest{Method: "GET", URL: origin.URL})
			if err != nil || proxied.Load() != 1 || direct.Load() != 0 {
				t.Fatal("environment routing failed", err, proxied.Load(), direct.Load())
			}
			environment.set("http_proxy="+proxy.URL, "no_proxy=127.0.0.1")
			_, err = bound.Hooks.HTTP.Do(context.Background(), source.PageRequest{Method: "GET", URL: origin.URL})
			if !errors.Is(err, signinjournal.ErrStale) || proxied.Load() != 1 || direct.Load() != 0 {
				t.Fatal("revoked environment reached network", err)
			}
		})
	}
}

// A minimal SOCKS5 fixture forwards only to the synthetic local origin. It is
// not a production proxy and never resolves arbitrary external destinations.
func signinSyntheticSOCKS(t *testing.T, target string) (string, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	count := &atomic.Int32{}
	var workers sync.WaitGroup
	acceptDone := make(chan struct{})
	t.Cleanup(func() { listener.Close(); <-acceptDone; workers.Wait() })
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				header := make([]byte, 2)
				if _, err := io.ReadFull(conn, header); err != nil {
					return
				}
				methods := make([]byte, int(header[1]))
				if _, err := io.ReadFull(conn, methods); err != nil {
					return
				}
				if _, err := conn.Write([]byte{5, 0}); err != nil {
					return
				}
				request := make([]byte, 4)
				if _, err := io.ReadFull(conn, request); err != nil {
					return
				}
				size := 0
				switch request[3] {
				case 1:
					size = 4
				case 4:
					size = 16
				case 3:
					b := make([]byte, 1)
					if _, err := io.ReadFull(conn, b); err != nil {
						return
					}
					size = int(b[0])
				default:
					return
				}
				if _, err := io.ReadFull(conn, make([]byte, size+2)); err != nil {
					return
				}
				upstream, err := net.DialTimeout("tcp", target, time.Second)
				if err != nil {
					return
				}
				defer upstream.Close()
				count.Add(1)
				if _, err = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
					return
				}
				done := make(chan struct{})
				go func() {
					_, _ = io.Copy(upstream, conn)
					if c, ok := upstream.(*net.TCPConn); ok {
						_ = c.CloseWrite()
					}
					close(done)
				}()
				_, _ = io.Copy(conn, upstream)
				conn.Close()
				<-done
			}()
		}
	}()
	return listener.Addr().String(), count
}

func TestAutoSignInFactorySOCKSSupportedAndFailureNeverFallsBack(t *testing.T) {
	for _, scheme := range []string{"socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			f := signinSQLSetup(t)
			var requests atomic.Int32
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Connection", "close")
				fmt.Fprint(w, "synthetic socks success")
			}))
			defer origin.Close()
			address, count := signinSyntheticSOCKS(t, strings.TrimPrefix(origin.URL, "http://"))
			settings := f.a.Settings()
			settings.Sites = []SiteConfig{signinSitesNative(origin.URL)}
			settings.Sites[0].Proxy = true
			settings.ProxyHost = scheme + "://" + address
			signinSitesPublish(t, f.a, settings)
			deps := signinSitesDependencies(t, f)
			inv, _ := deps.Inventory(context.Background())
			bound, err := deps.Factory(context.Background(), inv.Configured[0])
			if err != nil {
				t.Fatal("SOCKS binding rejected", err)
			}
			response, err := bound.Hooks.HTTP.Do(context.Background(), source.PageRequest{Method: "GET", URL: origin.URL})
			if err != nil || response == nil || response.Status != http.StatusOK || response.Text != "synthetic socks success" || count.Load() != 1 || requests.Load() != 1 {
				t.Fatal("SOCKS did not return the origin response", err, count.Load(), requests.Load())
			}
			// Reject a real SOCKS handshake, proving the configured proxy was
			// attempted while the origin remains untouched by fallback traffic.
			failedProxy, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			var failedAttempts atomic.Int32
			stopped := make(chan struct{})
			go func() {
				defer close(stopped)
				conn, acceptErr := failedProxy.Accept()
				if acceptErr != nil {
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				greeting := make([]byte, 2)
				if _, readErr := io.ReadFull(conn, greeting); readErr != nil {
					return
				}
				methods := make([]byte, int(greeting[1]))
				if _, readErr := io.ReadFull(conn, methods); readErr != nil {
					return
				}
				failedAttempts.Add(1)
				_, _ = conn.Write([]byte{5, 255})
			}()
			t.Cleanup(func() { failedProxy.Close(); <-stopped })
			settings.ProxyHost = scheme + "://" + failedProxy.Addr().String()
			signinSitesPublish(t, f.a, settings)
			deps = signinSitesDependencies(t, f)
			bound, err = deps.Factory(context.Background(), inv.Configured[0])
			if err != nil {
				t.Fatal("supported binding unexpectedly rejected", err)
			}
			response, err = bound.Hooks.HTTP.Do(context.Background(), source.PageRequest{Method: "GET", URL: origin.URL})
			// Frozen RequestUtils suppresses ordinary RequestException and
			// returns None; nil response is not a successful HTTP response.
			if response != nil || err != nil || failedAttempts.Load() != 1 || requests.Load() != 1 {
				t.Fatal("failed SOCKS did not preserve source failure without direct fallback", err, failedAttempts.Load(), requests.Load())
			}
		})
	}
}

func TestAutoSignInProxyRevocationStopsSameOriginRedirect(t *testing.T) {
	for _, mutation := range []string{"environment", "setting"} {
		t.Run(mutation, func(t *testing.T) {
			f := signinSQLSetup(t)
			environment := &signinSyntheticEnvironment{}
			f.a.autoSignInEnvironment = environment.read
			var first, next atomic.Int32
			publication := make(chan error, 1)
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/first" {
					first.Add(1)
					if mutation == "environment" {
						environment.set("NO_PROXY=synthetic-new-bypass.test")
						publication <- nil
					} else {
						settings := f.a.Settings()
						settings.ProxyHost = "http://127.0.0.1:9"
						publication <- signinSitesPublishConfig(f.a, settings)
					}
					http.Redirect(w, r, "/next", http.StatusFound)
					return
				}
				next.Add(1)
			}))
			defer origin.Close()
			settings := f.a.Settings()
			settings.Sites = []SiteConfig{signinSitesNative(origin.URL)}
			signinSitesPublish(t, f.a, settings)
			deps := signinSitesDependencies(t, f)
			inventory, _ := deps.Inventory(context.Background())
			bound, err := deps.Factory(context.Background(), inventory.Configured[0])
			if err != nil {
				t.Fatal(err)
			}
			_, err = bound.Hooks.HTTP.Do(context.Background(), source.PageRequest{Method: "GET", URL: origin.URL + "/first"})
			if !errors.Is(err, signinjournal.ErrStale) || first.Load() != 1 || next.Load() != 0 {
				t.Fatal("routing revocation allowed redirect", err, first.Load(), next.Load())
			}
			if err := <-publication; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAutoSignInSDKProxyPolicyClonesAndBusinessSuccess(t *testing.T) {
	for _, route := range []string{"direct", "environment-forward", "explicit-socks"} {
		t.Run(route, func(t *testing.T) {
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			var direct, forward atomic.Int32
			success := `<a href="logout.php">退出</a><p>这是您的首次签到，本次签到获得10个魔力值。</p>`
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				direct.Add(1)
				w.Header().Set("Connection", "close")
				fmt.Fprint(w, success)
			}))
			defer origin.Close()
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				forward.Add(1)
				if !r.URL.IsAbs() || r.Header.Get("Cookie") != "sid=synthetic-native" {
					t.Error("untrusted SDK forward request")
				}
				fmt.Fprint(w, success)
			}))
			defer proxy.Close()
			environment := &signinSyntheticEnvironment{}
			a.autoSignInEnvironment = environment.read
			settings := a.Settings()
			settings.Sites = []SiteConfig{signinSitesNative(origin.URL)}
			var socks *atomic.Int32
			switch route {
			case "environment-forward":
				environment.set("http_proxy=" + proxy.URL) // proxy=false must still honor trust_env.
			case "explicit-socks":
				address, count := signinSyntheticSOCKS(t, strings.TrimPrefix(origin.URL, "http://"))
				socks = count
				settings.ProxyHost = "socks5h://" + address
				settings.Sites[0].Proxy = true
			}
			signinSitesPublish(t, a, settings)
			config := map[string]any{"enabled": true, "onlyonce": false, "notify": false, "cron": "0 0 * * *", "sign_sites": []any{"all"}, "login_sites": []any{}, "queue_cnt": 1}
			for _, instance := range []string{"AutoSignIn", "AutoSignIn_proxy_clone"} {
				autoSignInSDKInstall(t, a, instance, config)
			}
			for index, instance := range []string{"AutoSignIn", "AutoSignIn_proxy_clone"} {
				if _, err := a.queueAutoSignInCommand(context.Background(), instance, pluginAPIAuthority{Kind: "command"}, "飞书", "synthetic-user"); err != nil {
					t.Fatal(err)
				}
				autoSignInSDKExecute(t, a, instance)
				state := autoSignInSDKState(t, a, instance)
				if len(state.History) == 0 || state.Stats[source.DomainKey(origin.URL)].Success == 0 {
					t.Fatal("SDK lacked business success and durable statistics", route)
				}
				if index == 0 && len(autoSignInSDKState(t, a, "AutoSignIn_proxy_clone").History) != 0 {
					t.Fatal("proxy execution crossed clone history")
				}
			}
			switch route {
			case "direct":
				if direct.Load() < 2 || forward.Load() != 0 {
					t.Fatal("direct route mismatch")
				}
			case "environment-forward":
				if forward.Load() < 2 || direct.Load() != 0 {
					t.Fatal("SDK proxy=false bypassed environment proxy")
				}
			case "explicit-socks":
				if socks.Load() < 2 || direct.Load() < 2 || forward.Load() != 0 {
					t.Fatal("SDK SOCKS routing mismatch")
				}
			}
		})
	}
}
