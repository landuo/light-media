package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/sites"
)

func ownedSiteHTTP(t *testing.T) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(key, "")
	}
	binary := os.Getenv("OWNED_HOST_SITE_HTTP_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("OWNED_HOST_SITE_HTTP_FIXTURE_BINARY requires separately compiled standard worker")
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	m := installOwnedResourceProcessWorker(t, a, cookie, binary)
	value, ok := a.pluginRunningInstances.Load(m.PluginID)
	if !ok {
		t.Fatal("site HTTP fixture not loaded")
	}
	return a, cookie, value.(*originalPluginInstance)
}
func ownedSiteHTTPCall(t *testing.T, p *originalPluginInstance, input plugins.SiteHTTPRequestV1) (plugins.SiteHTTPResultV1, error) {
	t.Helper()
	raw, _ := json.Marshal(input)
	result, err := p.callOriginalMethod(context.Background(), "request", raw)
	var value plugins.SiteHTTPResultV1
	if err == nil {
		err = json.Unmarshal(result.Result, &value)
	}
	return value, err
}
func ownedSiteHTTPConfig(t *testing.T, a *App, peer, cookie, ua string) {
	t.Helper()
	settings := a.Settings()
	settings.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-site", Kind: "nexusphp", BaseURL: peer, Cookie: cookie, UserAgent: ua}, Enabled: false, SiteOptions: SiteOptions{Token: "owned-token"}}}
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
}

// Expected bytes and header defaults come from the independently executed fixed
// RequestUtils + paired requests/urllib3 oracle, not the new host serializer.
func TestOriginalHostSiteHTTPActualSDKFixedSourceWire(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-platform-final-20261104/settings/site-http-independent-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name     string
			Response struct {
				Status int
				Body   string
			}
			Trace []struct {
				Method, Path, Body string
				Headers            map[string]string
			}
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 6 {
		t.Fatal("fixed source HTTP oracle")
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, _, worker := ownedSiteHTTP(t)
			var mu sync.Mutex
			trace := []struct {
				Method, Path, Body string
				Headers            map[string]string
			}{}
			var peer *httptest.Server
			peer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				headers := map[string]string{}
				for k, values := range r.Header {
					if !strings.EqualFold(k, "content-length") {
						headers[strings.ToLower(k)] = strings.Join(values, ", ")
					}
				}
				if ref := headers["referer"]; ref != "" {
					headers["referer"] = strings.ReplaceAll(ref, peer.URL, "<owned-origin>")
				}
				mu.Lock()
				trace = append(trace, struct {
					Method, Path, Body string
					Headers            map[string]string
				}{r.Method, r.URL.RequestURI(), string(body), headers})
				mu.Unlock()
				if r.URL.Path == "/redirect" {
					status, _ := strconv.Atoi(r.URL.Query().Get("status"))
					w.Header().Set("Location", "/final")
					w.Header().Set("Set-Cookie", "owned=redirect; Path=/")
					w.WriteHeader(status)
					return
				}
				if r.URL.Query().Get("status") == "503" {
					w.WriteHeader(503)
				}
				fmt.Fprint(w, "owned-response")
			}))
			defer peer.Close()
			input := plugins.SiteHTTPRequestV1{ID: "owned-site", Path: sample.Trace[0].Path, Method: "POST"}
			cookie, ua := "", ""
			switch sample.Name {
			case "ordered-repeated-form":
				input.Form = []plugins.SiteHTTPPairV1{{Name: "second", Value: "x y"}, {Name: "choice[]", Value: "1"}, {Name: "choice[]", Value: "2"}, {Name: "first", Value: "中文"}}
				input.Referer = "/from"
				cookie = "owned=initial; other=a=b"
				ua = "OwnedPlugin/1"
			case "json-default-header", "json-explicit-headers":
				input.JSON = json.RawMessage(`{"z":1,"a":"中"}`)
				if sample.Name == "json-explicit-headers" {
					value := "yes"
					input.Headers = map[string]*string{"X-Owned": &value}
				}
			case "raw-body-non2xx":
				input.Body = []byte("owned=%2B&x=raw")
			case "post-302", "post-307":
				input.Form = []plugins.SiteHTTPPairV1{{Name: "key", Value: "one"}}
				cookie = "owned=initial"
			}
			ownedSiteHTTPConfig(t, a, peer.URL, cookie, ua)
			result, err := ownedSiteHTTPCall(t, worker, input)
			if err != nil || result.Response == nil || result.Response.StatusCode != sample.Response.Status || string(result.Response.Content) != sample.Response.Body {
				t.Fatal("fixed response differs", result, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(trace, sample.Trace) {
				t.Fatalf("fixed source wire differs\nactual=%s\nsource=%s", mustJSON(trace), mustJSON(sample.Trace))
			}
		})
	}
}

func TestOriginalHostSiteHTTPActualSDKCredentialsCookiesAndGuards(t *testing.T) {
	a, cookie, worker := ownedSiteHTTP(t)
	var foreign atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreign.Add(1) }))
	defer other.Close()
	var observedMu sync.Mutex
	observed := http.Header{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observedMu.Lock()
		observed = r.Header.Clone()
		observedMu.Unlock()
		switch r.URL.Path {
		case "/cookie":
			w.Header().Add("Set-Cookie", "next=opaque-private; Path=/")
		case "/cross":
			http.Redirect(w, r, other.URL+"/must-not-receive", 302)
			return
		case "/drop":
			c, _, _ := w.(http.Hijacker).Hijack()
			_ = c.Close()
			return
		}
		fmt.Fprint(w, "owned-response")
	}))
	defer peer.Close()
	ownedSiteHTTPConfig(t, a, peer.URL, "original=host-private", "OwnedSite/1")
	settings := a.Settings()
	settings.Sites[0].APIKey = "owned-api-private"
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	request := plugins.SiteHTTPRequestV1{ID: "owned-site", Path: "/cookie", Credentials: []plugins.SiteHTTPCredentialV1{{Credential: "api_key", Header: "api-token"}, {Credential: "token", Header: "Authorization", Prefix: "Bearer "}}}
	value, err := ownedSiteHTTPCall(t, worker, request)
	if err != nil || value.Response == nil || len(value.Response.Cookies) != 1 {
		t.Fatal("credentialed request", value, err)
	}
	observedMu.Lock()
	headers := observed.Clone()
	observedMu.Unlock()
	if headers.Get("Api-Token") != "owned-api-private" || headers.Get("Authorization") != "Bearer owned-token" || headers.Get("Cookie") != "original=host-private" {
		t.Fatal("protected binding changed")
	}
	raw, _ := json.Marshal(value)
	for _, private := range []string{"owned-api-private", "owned-token", "host-private"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("saved credential projected into result")
		}
	}
	replacement := value.Response.Cookies
	request = plugins.SiteHTTPRequestV1{ID: "owned-site", Path: "/echo", Method: "POST", CookieOverride: &replacement}
	if _, err := ownedSiteHTTPCall(t, worker, request); err != nil {
		t.Fatal(err)
	}
	observedMu.Lock()
	gotCookie := observed.Get("Cookie")
	observedMu.Unlock()
	if gotCookie != "next=opaque-private" {
		t.Fatal("response cookies did not replace configured cookies", gotCookie)
	}
	clone := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedHostSiteHTTP/clone", `{"suffix":"copy"}`, cookie, "")
	assertOriginalRegistrySuccess(t, clone.Code, clone.Body.Bytes())
	object, _ := a.pluginRunningInstances.Load("OwnedHostSiteHTTPcopy")
	if _, err := ownedSiteHTTPCall(t, object.(*originalPluginInstance), request); err != nil {
		t.Fatal("clone cannot use ordinary explicitly supplied response cookies", err)
	}
	request.CookieOverride = nil
	request.Path = "/cross"
	value, err = ownedSiteHTTPCall(t, worker, request)
	if err != nil || value.Response != nil || foreign.Load() != 0 {
		t.Fatal("redirect escaped canonical site", value, err, foreign.Load())
	}
	request.Path = other.URL
	if _, err := ownedSiteHTTPCall(t, worker, request); err == nil {
		t.Fatal("absolute foreign target admitted")
	}
	request.Path = "/drop"
	value, err = ownedSiteHTTPCall(t, worker, request)
	if err != nil || value.Response != nil || value.RequestState != "unknown" {
		t.Fatal("lost POST response must remain unknown", value, err)
	}
	for _, header := range []string{"Idempotency-Key", "X-Idempotency-Key", "TE", "Trailer", "Upgrade", "Proxy-Authenticate", "Origin", "Referer"} {
		request.Path = "/must-not-execute"
		request.Credentials = []plugins.SiteHTTPCredentialV1{{Credential: "token", Header: header}}
		if _, err := ownedSiteHTTPCall(t, worker, request); err == nil {
			t.Fatal("credential binding bypassed reserved-header policy", header)
		}
	}
	request.Path = "/echo"
}

func TestOriginalHostSiteHTTPActualSDKPostGuardKeepsConfirmedOutcome(t *testing.T) {
	a, _, worker := ownedSiteHTTP(t)
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		settings := a.Settings()
		settings.Sites[0].Proxy = true
		if err := a.saveSettings(context.Background(), settings); err != nil {
			t.Error(err)
		}
		fmt.Fprint(w, "committed-owned-write")
	}))
	defer peer.Close()
	ownedSiteHTTPConfig(t, a, peer.URL, "owned=initial", "")
	value, err := ownedSiteHTTPCall(t, worker, plugins.SiteHTTPRequestV1{ID: "owned-site", Path: "/write", Method: "POST", Body: []byte("owned-write")})
	if err != nil || value.Response == nil || value.Response.StatusCode != 200 || string(value.Response.Content) != "committed-owned-write" || !value.ScopeChanged || value.RequestState != "completed" || calls.Load() != 1 {
		t.Fatal("post-write guard erased real outcome", value, err, calls.Load())
	}
}

func TestOriginalHostSiteHTTPActualSDKFixedSourceResponseCookies(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-platform-final-20261104/settings/site-http-cookie-independent-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name       string
			SetCookie  []string `json:"set_cookie"`
			NextCookie *string  `json:"next_request_cookie"`
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 8 {
		t.Fatal("fixed Response.cookies oracle")
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, _, worker := ownedSiteHTTP(t)
			var mu sync.Mutex
			got := ""
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/cookie" {
					for _, value := range sample.SetCookie {
						w.Header().Add("Set-Cookie", value)
					}
				} else {
					mu.Lock()
					got = r.Header.Get("Cookie")
					mu.Unlock()
				}
				fmt.Fprint(w, "owned-response")
			}))
			defer peer.Close()
			ownedSiteHTTPConfig(t, a, peer.URL, "original=must-not-return", "")
			out, err := ownedSiteHTTPCall(t, worker, plugins.SiteHTTPRequestV1{ID: "owned-site", Path: "/cookie"})
			if err != nil || out.Response == nil || out.Response.Cookies == nil {
				t.Fatal("response cookie dictionary missing", out, err)
			}
			_, err = ownedSiteHTTPCall(t, worker, plugins.SiteHTTPRequestV1{ID: "owned-site", Path: "/next", Method: "POST", CookieOverride: &out.Response.Cookies})
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if sample.NextCookie != nil {
				want = *sample.NextCookie
			}
			mu.Lock()
			defer mu.Unlock()
			if got != want {
				t.Fatal("source fresh cookies=dict wire differs", got, want)
			}
		})
	}
}

func TestOriginalHostSiteHTTPActualSDKPrivateCredentialsAndCleanupGrant(t *testing.T) {
	a, cookie, worker := ownedSiteHTTP(t)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "owned-response") }))
	defer peer.Close()
	ownedSiteHTTPConfig(t, a, peer.URL, "owned-cookie-private", "owned-UA-private")
	settings := a.Settings()
	settings.Sites[0].APIKey = "owned-key-private"
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	check := func(object *originalPluginInstance, want string) {
		t.Helper()
		out, err := object.callOriginalMethod(context.Background(), "credential_check", json.RawMessage(`{"id":"owned-site"}`))
		if err != nil {
			t.Fatal(err)
		}
		var value struct{ Digest string }
		if json.Unmarshal(out.Result, &value) != nil || value.Digest != want {
			t.Fatal("private worker received wrong credential values")
		}
	}
	digest := sha256.Sum256([]byte("owned-cookie-private\x00owned-UA-private\x00owned-key-private\x00owned-token"))
	check(worker, hex.EncodeToString(digest[:]))
	clone := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedHostSiteHTTP/clone", `{"suffix":"copy"}`, cookie, "")
	assertOriginalRegistrySuccess(t, clone.Code, clone.Body.Bytes())
	loaded, _ := a.pluginRunningInstances.Load("OwnedHostSiteHTTPcopy")
	check(loaded.(*originalPluginInstance), hex.EncodeToString(digest[:]))
	for _, path := range []string{"/api/settings", "/api/plugins", "/api/v1/plugin/OwnedHostSiteHTTP/snapshot"} {
		response := callTest(t, a.Handler(), "GET", path, "", cookie, "")
		for _, secret := range []string{"owned-cookie-private", "owned-key-private", "owned-token"} {
			if strings.Contains(response.Body.String(), secret) {
				t.Fatal("private credential appeared in public projection", path)
			}
		}
	}
	response := callTest(t, a.Handler(), "GET", "/api/v1/plugin/OwnedHostSiteHTTP/credential_check", "", cookie, "")
	if response.Code != 404 {
		t.Fatal("private method accidentally registered as public API", response.Code)
	}
	old, revoke := worker.originalBaseSessionHost(context.Background(), "request", nil)
	revoke()
	identity := plugins.Identity{PluginID: worker.installation.PluginID, InstanceID: worker.installation.InstanceID, Version: worker.installation.Version}
	if _, err := old(context.Background(), identity, plugins.SitesCredentialsCapability, json.RawMessage(`{"id":"owned-site"}`)); err == nil {
		t.Fatal("revoked old ordinary callback obtained credentials")
	}
	configured := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedHostSiteHTTP", `{"check_on_stop":true,"site_id":"owned-site"}`, cookie, "")
	assertOriginalRegistrySuccess(t, configured.Code, configured.Body.Bytes())
	if err := a.stopOriginalPlugin(context.Background(), "OwnedHostSiteHTTP"); err != nil {
		t.Fatal("fresh actual stop_service credential cleanup failed", err)
	}
	stored, err := a.pluginStore.Data(context.Background(), "OwnedHostSiteHTTP", "cleanup_credentials_accessed")
	if err != nil || string(stored.Value) != "true" {
		t.Fatal("fresh detached cleanup grant did not execute actual host credential access", err)
	}
	settings = a.Settings()
	settings.Sites[0].Cookie = ""
	settings.Sites[0].UserAgent = ""
	settings.Sites[0].APIKey = ""
	settings.Sites[0].Token = ""
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	out, err := loaded.(*originalPluginInstance).callOriginalMethod(context.Background(), "credential_check", json.RawMessage(`{"id":"owned-site"}`))
	if err != nil {
		t.Fatal(err)
	}
	var blank struct{ Empty bool }
	if json.Unmarshal(out.Result, &blank) != nil || !blank.Empty {
		t.Fatal("empty private fields were fabricated")
	}
}

func TestOriginalHostSiteHTTPActualSDKFixedSourceUserAgent(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-platform-final-20261104/settings/site-http-useragent-independent-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Module, Mode string
			Agent        string `json:"wire_user_agent"`
		}
		Global string `json:"controlled_global_user_agent"`
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 4 {
		t.Fatal("fixed plugin caller USER_AGENT oracle")
	}
	a, cookie, worker := ownedSiteHTTP(t)
	var mu sync.Mutex
	observed := ""
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		observed = r.UserAgent()
		mu.Unlock()
		fmt.Fprint(w, r.UserAgent())
	}))
	defer peer.Close()
	ownedSiteHTTPConfig(t, a, peer.URL, "", "")
	cloneResponse := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedHostSiteHTTP/clone", `{"suffix":"copy"}`, cookie, "")
	assertOriginalRegistrySuccess(t, cloneResponse.Code, cloneResponse.Body.Bytes())
	loaded, _ := a.pluginRunningInstances.Load("OwnedHostSiteHTTPcopy")
	for n, sample := range oracle.Cases {
		object := worker
		if n == 1 {
			object = loaded.(*originalPluginInstance)
		}
		input := plugins.SiteHTTPRequestV1{ID: "owned-site", Path: "/agent", UseHostUserAgent: sample.Mode != "browser"}
		if sample.Mode == "browser" {
			settings := a.Settings()
			settings.Sites[0].UserAgent = "OwnedBrowser/1"
			if err := a.saveSettings(context.Background(), settings); err != nil {
				t.Fatal(err)
			}
		}
		if sample.Mode == "explicit" {
			value := "yes"
			input.Headers = map[string]*string{"X-Owned": &value}
		}
		if _, err := ownedSiteHTTPCall(t, object, input); err != nil {
			t.Fatal(err)
		}
		expected := strings.ReplaceAll(sample.Agent, oracle.Global, originalPluginServerUserAgent())
		expected = strings.ReplaceAll(expected, "Plugin/"+sample.Module, "Plugin/"+strings.ToLower(object.installation.InstanceID))
		mu.Lock()
		actual := observed
		mu.Unlock()
		if actual != expected {
			t.Fatal("fixed USER_AGENT source/caller differs", actual, expected)
		}
	}
	value, err := worker.callOriginalMethod(context.Background(), "verify", json.RawMessage(`{"id":"owned-site","operation":"user_agent"}`))
	if err != nil {
		t.Fatal(err)
	}
	var checked struct {
		Read        string
		WriteDenied bool `json:"write_denied"`
	}
	if json.Unmarshal(value.Result, &checked) != nil || checked.Read != originalPluginServerUserAgent() || !checked.WriteDenied {
		t.Fatal("actual SDK USER_AGENT read-only contract failed")
	}
	falseValue := false
	if _, err := ownedSiteHTTPCall(t, worker, plugins.SiteHTTPRequestV1{ID: "owned-site", Path: "/agent", UseHostUserAgent: true, UseSiteUserAgent: &falseValue}); err == nil {
		t.Fatal("ambiguous USER_AGENT selection accepted")
	}
}
