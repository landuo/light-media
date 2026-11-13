package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func TestOriginalAuthTicketSourceLifetimeAndCapacity(t *testing.T) {
	var store originalAuthRuntime
	at := time.Unix(100, 0)
	create := func(metadata string) string {
		t.Helper()
		ticket, err := store.create(1, "owned", json.RawMessage(metadata), at)
		if err != nil {
			t.Fatal(err)
		}
		return ticket
	}
	first := create(`false`)
	if len(first) != 43 {
		t.Fatal("token does not contain 32 urlsafe random bytes")
	}
	if value, ok := store.consume(first, at.Add(120*time.Second)); !ok || string(value.metadata) != `{}` {
		t.Fatal("exact TTL or falsey metadata changed", ok, string(value.metadata))
	}
	if _, ok := store.consume(first, at); ok {
		t.Fatal("ticket reused")
	}
	expired := create(`{"owned":true}`)
	if _, ok := store.consume(expired, at.Add(120*time.Second+time.Nanosecond)); ok {
		t.Fatal("expired ticket accepted")
	}
	keys := []string{}
	for i := 0; i < 1026; i++ {
		keys = append(keys, create(`{}`))
	}
	if len(store.tickets) != 1025 {
		t.Fatal("source cleanup-before-insert capacity lost", len(store.tickets))
	}
	if _, ok := store.tickets[keys[0]]; ok {
		t.Fatal("oldest insertion with same timestamp not evicted")
	}
	if _, ok := store.tickets[keys[1]]; !ok {
		t.Fatal("evicted more than source")
	}
	// Even an unknown consume triggers cleanup after removing its candidate.
	store.consume("unknown", at)
	if len(store.tickets) != 1024 {
		t.Fatal("consume cleanup order changed")
	}
}

func TestOriginalAuthTicketConcurrentConsume(t *testing.T) {
	var store originalAuthRuntime
	at := time.Now()
	ticket, err := store.create(1, "owned", nil, at)
	if err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int32
	var wait sync.WaitGroup
	for i := 0; i < 32; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, ok := store.consume(ticket, at); ok {
				successes.Add(1)
			}
		}()
	}
	wait.Wait()
	if successes.Load() != 1 {
		t.Fatal("ticket not consumed exactly once", successes.Load())
	}
}

func TestOriginalAuthExchangeRequestValidation(t *testing.T) {
	a := newTestApp(t)
	for _, body := range []string{`{}`, `null`, `[]`, `{"ticket":null}`, `{"ticket":3}`, `true`, `{`} {
		r := callTest(t, a.Handler(), "POST", "/api/auth/exchange", body, nil, "")
		if r.Code != 422 {
			t.Fatalf("%s returned %d", body, r.Code)
		}
	}
	r := callTest(t, a.Handler(), "POST", "/api/auth/exchange", `{"ticket":"","ignored":true}`, nil, "")
	if r.Code != 401 {
		t.Fatal("empty but valid ticket should reach source consume", r.Code)
	}
}

func installOwnedAuthFixture(t *testing.T, a *App) *http.Cookie {
	t.Helper()
	return installOwnedAuthFixtureBinary(t, a, os.Getenv("OWNED_AUTH_FIXTURE_BINARY"))
}

func installOwnedAuthFixtureBinary(t *testing.T, a *App, binaryPath string) *http.Cookie {
	t.Helper()
	if binaryPath == "" {
		t.Fatal("OWNED_AUTH_FIXTURE_BINARY must name an actual compiled owned-auth-fixture")
	}
	binary, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	raw, err := exec.CommandContext(ctx, binaryPath, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedAuth" || manifest.Frontend != nil {
		t.Fatal("wrong standalone auth fixture manifest")
	}
	sum := sha256.Sum256(binary)
	digest := hex.EncodeToString(sum[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: digest, ExecutableSHA256: digest, Size: int64(len(binary)), HostContract: pluginResourcesSDKContract}}}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(binary)
		default:
			t.Error("unexpected fixture request", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/worker"
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]string{"plugin_id": "OwnedAuth", "repo_url": server.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	t.Cleanup(a.closeOriginalSessions)
	return cookie
}

func ownedAuthConfig(t *testing.T, a *App, cookie *http.Cookie, instance, body string) *httptest.ResponseRecorder {
	t.Helper()
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+instance, body, cookie, "")
	a.wg.Wait()
	return response
}

func ownedAuthProofBody(t *testing.T, userID int64, provider string) string {
	t.Helper()
	nonce := "owned-local-assertion"
	mac := hmac.New(sha256.New, []byte("owned-auth-proof-key"))
	_, _ = mac.Write([]byte(nonce))
	raw, err := json.Marshal(map[string]any{"nonce": nonce, "proof": hex.EncodeToString(mac.Sum(nil)), "user_id": userID, "provider_id": provider})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func ownedAuthMint(t *testing.T, a *App, instance string) string {
	t.Helper()
	response := callTest(t, a.Handler(), "POST", "/api/v1/plugin/"+instance+"/authenticate", ownedAuthProofBody(t, 1, "plugin:"+instance), nil, "")
	var result struct {
		Ticket string `json:"ticket"`
		PID    int    `json:"pid"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Ticket == "" || result.PID == 0 {
		t.Fatal("real proof/mint failed", response.Code, response.Body.String())
	}
	return result.Ticket
}

func TestOriginalAuthProvidersActualSDKSourceValues(t *testing.T) {
	a := newTestApp(t)
	cookie := installOwnedAuthFixture(t, a)
	response := ownedAuthConfig(t, a, cookie, "OwnedAuth", `{"enabled":true,"providers":[null,false,{},42,"skip",{"extra":9007199254740993},{"id":null,"name":"","enabled":false},{"id":"duplicate","enabled":[]},{"id":"duplicate","enabled":1},{"id":"duplicate","enabled":"yes","remote":{"x":2}}]}`)
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	raw, err := a.originalPluginAuthProviders(context.Background())
	if err != nil || len(raw) != 5 {
		t.Fatal("raw source declarations", len(raw), err)
	}
	if string(raw[0]["extra"]) != "9007199254740993" || string(raw[0]["id"]) != `"plugin:OwnedAuth"` || string(raw[0]["enabled"]) != "true" || string(raw[1]["id"]) != "null" || string(raw[1]["name"]) != `""` {
		t.Fatal("setdefault/raw value semantics lost", raw)
	}
	response = callTest(t, a.Handler(), "GET", "/api/auth/providers", "", nil, "")
	var envelope struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || len(envelope.Data) != 3 || string(envelope.Data[1]["id"]) != `"duplicate"` || string(envelope.Data[2]["remote"]) != `{"x":2}` {
		t.Fatal("source enabled filter/order/duplicates", response.Code, response.Body.String())
	}
	for _, test := range []struct {
		value string
		want  int
	}{{`null`, 0}, {`false`, 0}, {`0`, 0}, {`{}`, 0}, {`{"key":1}`, 0}, {`"text"`, 0}, {`[{}, {"id":"kept"}]`, 1}} {
		response = ownedAuthConfig(t, a, cookie, "OwnedAuth", `{"enabled":true,"providers":`+test.value+`}`)
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
		providers, err := a.originalPluginAuthProviders(context.Background())
		if err != nil || len(providers) != test.want {
			t.Fatalf("%s: %d %v", test.value, len(providers), err)
		}
	}
	response = ownedAuthConfig(t, a, cookie, "OwnedAuth", `{"enabled":true,"fail_getter":true}`)
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	providers, err := a.originalPluginAuthProviders(context.Background())
	if err != nil || len(providers) != 0 {
		t.Fatal("getter failure not isolated", err)
	}
	ownedAuthConfig(t, a, cookie, "OwnedAuth", `{"enabled":true,"fail_state":true}`)
	if _, err = a.originalPluginAuthProviders(context.Background()); err == nil {
		t.Fatal("state exception incorrectly swallowed")
	}
	ownedAuthConfig(t, a, cookie, "OwnedAuth", `{"enabled":true,"fail_render":true}`)
	if _, err = a.originalPluginAuthProviders(context.Background()); err == nil {
		t.Fatal("render exception incorrectly swallowed")
	}
	response = ownedAuthConfig(t, a, cookie, "OwnedAuth", `{"enabled":true,"providers":1}`)
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	if _, err = a.originalPluginAuthProviders(context.Background()); err == nil {
		t.Fatal("noniterable truthy result must fail")
	}
}

func TestOriginalAuthActualSDKProofExchangeAndAuthority(t *testing.T) {
	a := newTestApp(t)
	cookie := installOwnedAuthFixture(t, a)
	response := ownedAuthConfig(t, a, cookie, "OwnedAuth", `{"enabled":true}`)
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	response = callTest(t, a.Handler(), "POST", "/api/v1/plugin/OwnedAuth/authenticate", `{"nonce":"owned-local-assertion","proof":"bad"}`, nil, "")
	if response.Code != 401 {
		t.Fatal("invalid proof accepted", response.Code)
	}
	for _, body := range []string{ownedAuthProofBody(t, 2, "plugin:OwnedAuth"), ownedAuthProofBody(t, 1, "plugin:OtherPlugin")} {
		response = callTest(t, a.Handler(), "POST", "/api/v1/plugin/OwnedAuth/authenticate", body, nil, "")
		if response.Code != 403 {
			t.Fatal("forged local user/provider accepted", response.Code, response.Body.String())
		}
	}
	ticket := ownedAuthMint(t, a, "OwnedAuth")
	response = callTest(t, a.Handler(), "POST", "/api/v1/plugin/OwnedAuth/stale", ownedAuthProofBody(t, 1, "plugin:OwnedAuth"), nil, "")
	if response.Code != 403 {
		t.Fatal("retained closed host callback minted", response.Code, response.Body.String())
	}
	response = ownedAuthConfig(t, a, cookie, "OwnedAuth", `{"enabled":true,"providers":[{"id":"plugin:OwnedAuth","enabled":false}]}`)
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	response = callTest(t, a.Handler(), "POST", "/api/v1/plugin/OwnedAuth/authenticate", ownedAuthProofBody(t, 1, "plugin:OwnedAuth"), nil, "")
	if response.Code != 403 {
		t.Fatal("disabled provider minted", response.Code, response.Body.String())
	}
	response = ownedAuthConfig(t, a, cookie, "OwnedAuth", `{"enabled":false}`)
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	response = callTest(t, a.Handler(), "POST", "/api/v1/plugin/OwnedAuth/authenticate", ownedAuthProofBody(t, 1, "plugin:OwnedAuth"), nil, "")
	if response.Code != 403 {
		t.Fatal("disabled plugin minted", response.Code, response.Body.String())
	}
	// Source exchange does not revoke an already issued ticket on disable.
	body, _ := json.Marshal(map[string]string{"ticket": ticket})
	response = callTest(t, a.Handler(), "POST", "/api/auth/exchange", string(body), nil, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"authenticated":true`) {
		t.Fatal("actual exchange after disable", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Value == "" || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("real session cookie missing", cookies)
	}
	status := callTest(t, a.Handler(), "GET", "/api/auth/status", "", cookies[0], "")
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"authenticated":true`) {
		t.Fatal("exchange did not establish actual core session", status.Body.String())
	}
	protected := callTest(t, a.Handler(), "GET", "/api/plugins", "", cookies[0], "")
	if protected.Code != 200 {
		t.Fatal("minted session cannot access protected manager", protected.Code)
	}
	response = callTest(t, a.Handler(), "POST", "/api/auth/exchange", string(body), nil, "")
	if response.Code != 401 {
		t.Fatal("ticket replay accepted", response.Code)
	}
}

func TestOriginalAuthActualSDKCloneAndConcurrentExchange(t *testing.T) {
	a := newTestApp(t)
	cookie := installOwnedAuthFixture(t, a)
	response := ownedAuthConfig(t, a, cookie, "OwnedAuth", `{"enabled":true}`)
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	response = callTest(t, a.Handler(), "POST", "/api/plugins/OwnedAuth/clone", `{"suffix":"Twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	response = ownedAuthConfig(t, a, cookie, "OwnedAuthtwin", `{"enabled":true}`)
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	providers, err := a.originalAuthProviders(context.Background())
	if err != nil || len(providers) != 2 || string(providers[0]["plugin_id"]) != `"OwnedAuth"` || string(providers[1]["id"]) != `"plugin:OwnedAuthtwin"` {
		t.Fatal("clone order/identity", providers, err)
	}
	ticket := ownedAuthMint(t, a, "OwnedAuthtwin")
	body, _ := json.Marshal(map[string]string{"ticket": ticket})
	var good, bad atomic.Int32
	var wait sync.WaitGroup
	for i := 0; i < 12; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			request := httptest.NewRequest("POST", "/api/auth/exchange", strings.NewReader(string(body)))
			request.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, request)
			if w.Code == 200 {
				good.Add(1)
			} else if w.Code == 401 {
				bad.Add(1)
			} else {
				t.Errorf("unexpected exchange status %d", w.Code)
			}
		}()
	}
	wait.Wait()
	if good.Load() != 1 || bad.Load() != 11 {
		t.Fatal("HTTP exchange not atomic", good.Load(), bad.Load())
	}
}

func TestOriginalAuthActualSDKExpiryMissingUserAndOrigin(t *testing.T) {
	a := newTestApp(t)
	cookie := installOwnedAuthFixture(t, a)
	response := ownedAuthConfig(t, a, cookie, "OwnedAuth", `{"enabled":true}`)
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	ticket := ownedAuthMint(t, a, "OwnedAuth")
	a.pluginOriginalAuth.mu.Lock()
	entry := a.pluginOriginalAuth.tickets[ticket]
	entry.createdAt = time.Now().Add(-121 * time.Second)
	a.pluginOriginalAuth.tickets[ticket] = entry
	a.pluginOriginalAuth.mu.Unlock()
	body, _ := json.Marshal(map[string]string{"ticket": ticket})
	response = callTest(t, a.Handler(), "POST", "/api/auth/exchange", string(body), nil, "")
	if response.Code != 401 {
		t.Fatal("expired HTTP ticket accepted", response.Code)
	}
	ticket = ownedAuthMint(t, a, "OwnedAuth")
	body, _ = json.Marshal(map[string]string{"ticket": ticket})
	request := httptest.NewRequest("POST", "/api/auth/exchange", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://unrelated.invalid")
	blocked := httptest.NewRecorder()
	a.Handler().ServeHTTP(blocked, request)
	if blocked.Code != 403 {
		t.Fatal("cross-origin exchange accepted", blocked.Code)
	}
	if _, err := a.Store.DB.Exec(`DELETE FROM admin`); err != nil {
		t.Fatal(err)
	}
	response = callTest(t, a.Handler(), "POST", "/api/auth/exchange", string(body), nil, "")
	if response.Code != 403 {
		t.Fatal("deleted user not rejected", response.Code, response.Body.String())
	}
	response = callTest(t, a.Handler(), "POST", "/api/auth/exchange", string(body), nil, "")
	if response.Code != 401 {
		t.Fatal("invalid-user exchange must consume first", response.Code)
	}
}
