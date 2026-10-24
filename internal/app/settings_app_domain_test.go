package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestSettingsAppDomainHTTPPersistenceAndClear(t *testing.T) {
	// No server listener, App.Start, scheduler, real settings or account is used.
	// Settings GET/PUT are dispatched through the real authenticated handler.
	// Reject any default-client dial rather than relying on .invalid DNS failure.
	// This test is intentionally nonparallel because DefaultTransport is global.
	t.Setenv("LIGHT_MEDIA_MEDIA_ROOTS", "")
	var dials atomic.Int64
	rejectDial := func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("unexpected network dial in app-domain settings test")
	}
	oldTransport := http.DefaultTransport
	transport := &http.Transport{DialContext: rejectDial, DialTLSContext: rejectDial}
	http.DefaultTransport = transport
	t.Cleanup(func() {
		http.DefaultTransport = oldTransport
		transport.CloseIdleConnections()
		if n := dials.Load(); n != 0 {
			t.Errorf("settings attempted %d unexpected network dials", n)
		}
	})

	for _, mode := range []string{"inherited", "explicit-false", "explicit-true"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			var a *App
			open := func() {
				t.Helper()
				var err error
				a, err = New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				if a != nil {
					if err := a.Close(); err != nil {
						t.Error(err)
					}
				}
			})
			open()
			if w := callTest(t, a.Handler(), "GET", "/api/settings", "", nil, ""); w.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated settings GET status %d", w.Code)
			}
			cookie := setupTestAdmin(t, a)
			request := func(method string, body map[string]any) map[string]any {
				t.Helper()
				var raw []byte
				var err error
				if body != nil {
					raw, err = json.Marshal(body)
					if err != nil {
						t.Fatal(err)
					}
				}
				w := callTest(t, a.Handler(), method, "/api/settings", string(raw), cookie, "")
				if w.Code != http.StatusOK {
					t.Fatalf("settings %s status %d", method, w.Code)
				}
				var result struct {
					Data map[string]any `json:"data"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Data == nil {
					t.Fatal("settings response missing non-null data object")
				}
				return result.Data
			}
			seed := request("GET", nil)
			if _, exists := seed["app_domain"]; exists {
				t.Fatal("new config unexpectedly has app_domain")
			}
			delete(seed, "secret_state")
			seed["site_data_refresh_hours"] = 12
			seed["material_max_mib"] = 96
			seed["log_level"] = "WARN"
			switch mode {
			case "inherited":
				seed["read_site_messages"] = nil
			case "explicit-false":
				seed["read_site_messages"] = false
			case "explicit-true":
				seed["read_site_messages"] = true
			}
			request("PUT", seed)
			baseline := a.Settings()
			assertSettings := func(domain string, projection map[string]any) {
				t.Helper()
				if domain == "" {
					if _, exists := projection["app_domain"]; exists {
						t.Fatal("empty optional app_domain should be omitted in public settings")
					}
				} else if projection["app_domain"] != domain {
					t.Fatal("public app_domain not preserved verbatim")
				}
				if value, exists := projection["read_site_messages"]; !exists || !reflect.DeepEqual(value, seed["read_site_messages"]) {
					t.Fatal("nullable read-site-messages preference changed")
				}
				if projection["effective_read_site_messages"] != (mode != "explicit-false") {
					t.Fatal("effective read-site-messages projection changed")
				}
				expected := baseline
				expected.AppDomain = domain
				if !reflect.DeepEqual(a.Settings(), expected) {
					t.Fatal("domain edit changed unrelated in-memory settings")
				}
				var sealed []byte
				if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='config' AND id='main'`).Scan(&sealed); err != nil {
					t.Fatal(err)
				}
				var stored Settings
				if err := a.Store.Unseal(sealed, &stored); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(stored, expected) {
					t.Fatal("domain edit not persisted or changed unrelated stored settings")
				}
			}
			// A scheme-less domain remains allowed; the URL helper, not settings
			// storage, owns default-link formatting. Empty explicitly clears it.
			for _, domain := range []string{"https://media.example.invalid/base/", "media.example.invalid/base", ""} {
				payload := request("GET", nil)
				delete(payload, "secret_state")
				payload["app_domain"] = domain
				assertSettings(domain, request("PUT", payload))
				assertSettings(domain, request("GET", nil))
				if err := a.Close(); err != nil {
					t.Fatal(err)
				}
				a = nil
				open()
				login := callTest(t, a.Handler(), "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
				if login.Code != http.StatusOK {
					t.Fatalf("cold login status %d", login.Code)
				}
				cookie = nil
				for _, candidate := range login.Result().Cookies() {
					if candidate.Name == "lm_session" {
						cookie = candidate
						break
					}
				}
				if cookie == nil {
					t.Fatal("cold login did not issue session")
				}
				assertSettings(domain, request("GET", nil))
			}
		})
	}
}
