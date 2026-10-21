package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
	for _, kind := range []string{"browser", "ocr", "tjupt", "socks"} {
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
			case "socks":
				settings.Sites[0].Proxy = true
				settings.ProxyHost = "socks5://127.0.0.1:9"
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
	a := &App{Store: store, pluginStore: plugins}
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
