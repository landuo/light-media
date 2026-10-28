package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const syntheticPluginAPIKey = "synthetic-api-reference-key-2026"

func TestPluginHTTPAuthorityPriorityAndSameOriginSessionBoundary(t *testing.T) {
	a, cookie := retainedPluginAuthorityFixture(t)
	validKey := syntheticPluginAPIKey
	for _, item := range []struct {
		name, auth, query, authorization, key string
		native, cookie                        bool
		status                                int
	}{
		{name: "anonymous", auth: "anonymous"},
		{name: "bear-key", auth: "bear", key: validKey},
		{name: "bear-token-query", auth: "bear", query: "token=" + validKey},
		{name: "source-bearer-cookie-adaptation", auth: "bear", cookie: true},
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
	a, cookie := retainedPluginAuthorityFixture(t)
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

func retainedPluginAuthorityFixture(t *testing.T) (*App, *http.Cookie) {
	t.Helper()
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	c := a.Settings()
	c.APIToken = syntheticPluginAPIKey
	if err := a.saveSettings(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return a, cookie
}

func TestPluginHTTPAPIKeyCannotGrantManagementAuthority(t *testing.T) {
	a, _ := retainedPluginAuthorityFixture(t)
	c := a.Settings()
	c.APIToken = "synthetic-rotated-api-key"
	if err := a.saveSettings(context.Background(), c); err != nil {
		t.Fatal(err)
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
