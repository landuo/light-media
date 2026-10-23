package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/autosignin"
)

const autoSignInFixtureAPIKey = "synthetic-signin-api-key-never-live-0000000001"

func autoSignInFixtureSetAPIKey(a *App, key string) {
	a.cfgMu.Lock()
	a.config.APIToken = key
	a.cfgMu.Unlock()
}
func autoSignInAPIInput() plugins.HTTPRequest {
	return plugins.HTTPRequest{RequestID: id("api_test_"), Key: "signin_by_domain", Method: "GET", Path: "/signin_by_domain", Params: map[string]string{}, Query: map[string][]string{"url": {"https://synthetic.example.invalid/"}}, CredentialMatches: map[string]bool{"apikey": true}, Headers: map[string]string{}}
}
func autoSignInAPIApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	autoSignInFixtureSetAPIKey(a, autoSignInFixtureAPIKey)
	autoSignInSDKInstall(t, a, "AutoSignIn", map[string]any{"enabled": false, "onlyonce": false, "sign_sites": []any{}, "login_sites": []any{}})
	return a
}

// Pass through the actual HTTP parser, which alone observes the raw query key.
// No worker runs until this helper returns; cancelling the response wait does
// not manufacture credentials or change the persisted invocation.
func autoSignInIngressAPI(t *testing.T, a *App, key string, addresses ...string) PluginRun {
	t.Helper()
	address := "https://synthetic.example.invalid/"
	if len(addresses) > 0 {
		address = addresses[0]
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	req := httptest.NewRequest(http.MethodGet, "/api/plugins/AutoSignIn/api/signin_by_domain?url="+url.QueryEscape(address)+"&apikey="+url.QueryEscape(key), nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	go func() { defer close(done); a.Handler().ServeHTTP(recorder, req) }()
	defer func() { cancel(); <-done }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var runID string
		if a.Store.DB.QueryRow(`SELECT id FROM plugin_runs WHERE instance_id='AutoSignIn' AND action='api' AND state='pending' ORDER BY created_at DESC,id DESC LIMIT 1`).Scan(&runID) == nil {
			run, err := a.pluginStore.Run(context.Background(), runID)
			if err != nil {
				t.Fatal(err)
			}
			return run
		}
		select {
		case <-done:
			t.Fatalf("HTTP intake failed: %d %s", recorder.Code, recorder.Body.String())
		case <-deadline.C:
			t.Fatal("HTTP intake did not enqueue")
		case <-ticker.C:
		}
	}
}
func TestPluginAutoSignInAnonymousAPIOriginalKeyProofAndRevocation(t *testing.T) {
	a := autoSignInAPIApp(t)
	ctx := context.Background()
	run := autoSignInIngressAPI(t, a, autoSignInFixtureAPIKey)
	if err := a.validatePluginHTTPRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	if err := a.Store.DB.QueryRow(`SELECT scope FROM plugin_api_calls WHERE run_id=?`, run.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	raw, err := a.pluginStore.unseal("api_scope", run.InstanceID, run.ID, sealed)
	if err != nil {
		t.Fatal(err)
	}
	var scope pluginAPIScope
	if json.Unmarshal(raw, &scope) != nil || scope.Authority.Kind != "anonymous" || scope.CredentialProofs["apikey"] != a.pluginAPIKeyProof(autoSignInFixtureAPIKey) {
		t.Fatal("missing original host proof")
	}
	if strings.Contains(string(raw), autoSignInFixtureAPIKey) || strings.Contains(string(run.Input), autoSignInFixtureAPIKey) || strings.Contains(string(run.Input), scope.CredentialProofs["apikey"]) {
		t.Fatal("query key or private proof crossed worker/storage boundary")
	}
	autoSignInFixtureSetAPIKey(a, "synthetic-rotated-api-key-never-live-0000000002")
	if err = a.validatePluginHTTPRun(ctx, run); pluginErrorCode(err) != "PLUGIN_API_AUTH_REVOKED" {
		t.Fatal("anonymous true remained authorized after key rotation", err)
	}
}
func TestPluginAutoSignInAnonymousAPIAdmissionDoesNotMintFromBoolean(t *testing.T) {
	a := autoSignInAPIApp(t)
	ctx := context.Background()
	in, err := a.pluginStore.Installation(ctx, "AutoSignIn")
	if err != nil {
		t.Fatal(err)
	}
	input := autoSignInAPIInput()
	scope := pluginAPIScope{Key: input.Key, Method: input.Method, RequestID: input.RequestID, Authority: pluginAPIAuthority{Kind: "anonymous"}}
	if _, err = a.enqueuePluginHTTP(ctx, in, input, scope); pluginErrorCode(err) != "PLUGIN_API_SCOPE_DENIED" {
		t.Fatal("unproved true was accepted", err)
	}
	// Proof was captured from the old observed value, then the token rotated
	// before admission. Never derive a replacement from the current token.
	scope.CredentialProofs = map[string]string{"apikey": a.pluginAPIKeyProof(autoSignInFixtureAPIKey)}
	autoSignInFixtureSetAPIKey(a, "synthetic-rotated-api-key-never-live-0000000002")
	if _, err = a.enqueuePluginHTTP(ctx, in, input, scope); pluginErrorCode(err) != "PLUGIN_API_AUTH_REVOKED" {
		t.Fatal("parse-to-enqueue rotation accepted", err)
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_api_calls`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected authority created an API call", count, err)
	}
}
func TestPluginAutoSignInAnonymousAPIWrongKeyKeepsSourceFailureResponse(t *testing.T) {
	a := autoSignInAPIApp(t)
	ctx := context.Background()
	queued := autoSignInIngressAPI(t, a, "synthetic-wrong-api-key-never-live")
	var inv plugins.Invocation
	var input plugins.HTTPRequest
	if json.Unmarshal(queued.Input, &inv) != nil || json.Unmarshal(inv.Input, &input) != nil || input.CredentialMatches["apikey"] {
		t.Fatal("invalid key observation changed")
	}
	if err := a.validatePluginHTTPRun(ctx, queued); err != nil {
		t.Fatal("false observation was treated as revoked authority", err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil || run.ID != queued.ID {
		t.Fatal(err)
	}
	a.executePluginRun(ctx, run)
	final, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || final.State != "succeeded" {
		t.Fatal("wrong-key source response failed", final.State, final.ErrorCode, err)
	}
	var response plugins.HTTPResponse
	var body autosignin.Object
	if json.Unmarshal(final.Result, &response) != nil || autosignin.Decode(response.Body, &body) != nil || response.Status != 200 || body["success"] != false || body["message"] != "API密钥错误" {
		t.Fatal("source wrong-key semantics changed", string(final.Result))
	}
}

func TestPluginAutoSignInAnonymousAPIRealIngressSDKTrustedSiteSuccess(t *testing.T) {
	ctx := context.Background()
	a := autoSignInAPIApp(t)
	var requests, unregisteredRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != "GET" || r.URL.Path != "/attendance.php" || r.Header.Get("Cookie") != "sid=synthetic-native" {
			t.Error("API did not use the native registered transport", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `<a href="logout.php">退出</a><p>这是您的首次签到，本次签到获得10个魔力值。</p>`)
	}))
	defer server.Close()
	unregistered := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unregisteredRequests.Add(1)
		fmt.Fprint(w, "must never be requested")
	}))
	defer unregistered.Close()
	settings := a.Settings()
	settings.Sites = []SiteConfig{signinSitesNative(server.URL)}
	signinSitesPublish(t, a, settings)
	identity, err := a.Store.nativeSiteByAppID(ctx, settings.Sites[0].ID)
	if err != nil || identity.SourceID < 1 {
		t.Fatal("fixture lacks trusted native identity", err)
	}
	before := autoSignInSDKState(t, a, "AutoSignIn")
	beforeHistory, _ := json.Marshal(before.History)
	queued := autoSignInIngressAPI(t, a, autoSignInFixtureAPIKey, settings.Sites[0].BaseURL)
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil || run.ID != queued.ID {
		t.Fatal(err)
	}
	a.executePluginRun(ctx, run)
	final, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || final.State != "succeeded" {
		t.Fatal("real API SDK failed", final.State, final.ErrorCode, err)
	}
	var response plugins.HTTPResponse
	var body autosignin.Object
	if json.Unmarshal(final.Result, &response) != nil || autosignin.Decode(response.Body, &body) != nil || response.Status != 200 || body["success"] != true {
		t.Fatal("HTTP 200 was not verified business success", string(final.Result))
	}
	data, ok := body["data"].(autosignin.Object)
	if !ok || data["signin_success"] != true || data["login_success"] != true || requests.Load() != 1 || unregisteredRequests.Load() != 0 {
		t.Fatal("signin evidence/request count mismatch", body, requests.Load(), unregisteredRequests.Load())
	}
	after := autoSignInSDKState(t, a, "AutoSignIn")
	afterHistory, _ := json.Marshal(after.History)
	// The original direct-domain API calls SiteVisitor, not the bulk SignIn
	// pipeline. It records HTTP/statistic receipts but must not invent history.
	if string(beforeHistory) != string(afterHistory) || after.Runs[run.ID] == nil || after.Runs[run.ID].Phase != "completed" {
		t.Fatal("API mutated bulk history or lost its durable receipt")
	}
	successful := 0
	for _, stat := range after.Stats {
		successful += stat.Success
	}
	if successful != 1 {
		t.Fatal("successful HTTP visit lacks source statistic evidence", successful)
	}
	// A valid API key does not turn an arbitrary URL into a transport grant.
	queued = autoSignInIngressAPI(t, a, autoSignInFixtureAPIKey, unregistered.URL)
	run, err = a.pluginStore.ClaimRun(ctx)
	if err != nil || run.ID != queued.ID {
		t.Fatal(err)
	}
	a.executePluginRun(ctx, run)
	final, err = a.pluginStore.Run(ctx, run.ID)
	if err != nil || final.State != "succeeded" {
		t.Fatal("missing-site source response lost", final.State, final.ErrorCode, err)
	}
	body = nil
	if json.Unmarshal(final.Result, &response) != nil || autosignin.Decode(response.Body, &body) != nil || response.Status != 200 || body["success"] != false || !strings.Contains(fmt.Sprint(body["message"]), "不存在") {
		t.Fatal("unregistered URL was accepted", string(final.Result))
	}
	if requests.Load() != 1 || unregisteredRequests.Load() != 0 {
		t.Fatal("URL parameter selected an unregistered transport")
	}
}

func TestPluginAutoSignInAnonymousAPIPreservesAdmissionAcrossSourceConfigWrite(t *testing.T) {
	for _, change := range []string{"source_only", "external_config", "token_rotation"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			autoSignInFixtureSetAPIKey(a, autoSignInFixtureAPIKey)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/attendance.php" || r.Header.Get("Cookie") != "sid=synthetic-native" {
					t.Error("unexpected native transport", r.URL.Path)
				}
				fmt.Fprint(w, `<a href="logout.php">退出</a><p>这是您的首次签到，本次签到获得10个魔力值。</p>`)
			}))
			defer server.Close()
			settings := a.Settings()
			settings.Sites = []SiteConfig{signinSitesNative(server.URL)}
			signinSitesPublish(t, a, settings)
			autoSignInSDKInstall(t, a, "AutoSignIn", map[string]any{"enabled": true, "onlyonce": false, "cron": "0 0 * * *", "sign_sites": []any{"all"}, "login_sites": []any{}, "queue_cnt": 1})
			command, err := a.queueAutoSignInCommand(ctx, "AutoSignIn", pluginAPIAuthority{Kind: "command"}, "飞书", "owned-user")
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := a.pluginStore.ClaimRun(ctx)
			if err != nil || claimed.ID != command.ID {
				t.Fatal("claim command", err)
			}
			// Admission precedes the other real SDK run's source config write.
			queued := autoSignInIngressAPI(t, a, autoSignInFixtureAPIKey, settings.Sites[0].BaseURL)
			originalInput := string(queued.Input)
			a.executePluginRun(ctx, claimed)
			commandFinal, err := a.pluginStore.Run(ctx, claimed.ID)
			if err != nil || commandFinal.State != "succeeded" {
				t.Fatal("command failed", commandFinal.ErrorCode, err)
			}
			config, err := a.pluginStore.Config(ctx, "AutoSignIn")
			if err != nil || config.Revision <= queued.ConfigRevision || requests.Load() != 1 {
				t.Fatal("command did not produce source revision and HTTP", config.Revision, queued.ConfigRevision, requests.Load(), err)
			}
			if err = a.validatePluginHTTPRun(ctx, queued); err != nil {
				t.Fatal("source config write revoked genuine admission", err)
			}
			switch change {
			case "external_config":
				var external map[string]any
				if err = json.Unmarshal(config.Value, &external); err != nil {
					t.Fatal(err)
				}
				external["notify"] = true
				if _, err = a.pluginStore.SaveConfig(ctx, "AutoSignIn", config.Revision, external); err != nil {
					t.Fatal(err)
				}
			case "token_rotation":
				autoSignInFixtureSetAPIKey(a, "synthetic-rotated-api-key-never-live-0000000002")
			}
			if change != "source_only" {
				if err = a.validatePluginHTTPRun(ctx, queued); err == nil {
					t.Fatal("external change retained API authority")
				}
				if requests.Load() != 1 {
					t.Fatal("revocation performed HTTP")
				}
			} else {
				run, claimErr := a.pluginStore.ClaimRun(ctx)
				if claimErr != nil || run.ID != queued.ID {
					t.Fatal("old admission claim", claimErr)
				}
				a.executePluginRun(ctx, run)
				final, runErr := a.pluginStore.Run(ctx, run.ID)
				var response plugins.HTTPResponse
				var body autosignin.Object
				if runErr != nil || final.State != "succeeded" || json.Unmarshal(final.Result, &response) != nil || autosignin.Decode(response.Body, &body) != nil || response.Status != 200 || body["success"] != true || requests.Load() != 2 {
					t.Fatal("old API admission failed real HTTP", final.State, final.ErrorCode, string(final.Result), requests.Load(), runErr)
				}
			}
			persisted, err := a.pluginStore.Run(ctx, queued.ID)
			if err != nil || persisted.ConfigRevision != queued.ConfigRevision || string(persisted.Input) != originalInput {
				t.Fatal("immutable API admission rewritten", err)
			}
		})
	}
}
