package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func fnosSettingsFixture(t *testing.T) (*App, *http.Cookie, Settings) {
	t.Helper()
	a := newTestApp(t)
	c := a.Settings()
	c.FNOS = FNConfig{
		Host: "https://192.168.8.129:5667/v", Username: "synthetic-user", Password: "synthetic-fnos-password",
		Enabled: true, LibraryGUID: "legacy-library", Mappings: []PathMapping{{From: "/media", To: "/media"}},
	}
	if err := a.saveSettings(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return a, setupTestAdmin(t, a), a.Settings()
}

func redactedFNOSSettings(t *testing.T, a *App) Settings {
	t.Helper()
	raw, err := json.Marshal(a.publicSettings())
	if err != nil {
		t.Fatal(err)
	}
	var incoming Settings
	if err := json.Unmarshal(raw, &incoming); err != nil {
		t.Fatal(err)
	}
	if incoming.FNOS.Password != "" {
		t.Fatal("public settings returned a password")
	}
	return incoming
}

func storedFNOSSettings(t *testing.T, a *App) []byte {
	t.Helper()
	var raw []byte
	if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='config'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestFNOSSettingsRejectChangedDestinationWithoutPassword(t *testing.T) {
	for _, tc := range []struct {
		name, host, username string
	}{
		{"host", "https://other.invalid/v", "synthetic-user"},
		{"other-private-host", "http://192.168.8.130:5666", "synthetic-user"},
		{"port", "https://192.168.8.129:5668/v", "synthetic-user"},
		{"arbitrary-port", "http://192.168.8.129:9999/v", "synthetic-user"},
		{"scheme", "http://192.168.8.129:5667/v", "synthetic-user"},
		{"account", "https://192.168.8.129:5667/v", "different-user"},
		{"account-and-default-entry", "http://192.168.8.129:5666", "different-user"},
		{"query", "https://192.168.8.129:5667/v?target=other", "synthetic-user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, cookie, previous := fnosSettingsFixture(t)
			stored := storedFNOSSettings(t, a)
			incoming := redactedFNOSSettings(t, a)
			incoming.FNOS.Host, incoming.FNOS.Username = tc.host, tc.username
			incoming.LogLevel = "WARN"
			raw, err := json.Marshal(incoming)
			if err != nil {
				t.Fatal(err)
			}
			w := callTest(t, a.Handler(), http.MethodPut, "/api/settings", string(raw), cookie, "")
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":"FNOS_CREDENTIALS_REQUIRED"`) || !strings.Contains(w.Body.String(), "请重新输入密码") {
				t.Fatalf("changed destination must require an explicitly supplied password: status=%d body=%s", w.Code, w.Body)
			}
			if strings.Contains(w.Body.String(), previous.FNOS.Password) {
				t.Fatal("credential leaked in the rejection")
			}
			if !reflect.DeepEqual(a.Settings(), previous) || !bytes.Equal(storedFNOSSettings(t, a), stored) {
				t.Fatal("rejected settings changed the in-memory or durable configuration")
			}
		})
	}
}

func TestFNOSSettingsEquivalentAddressPreservesCredentialsAndLegacyFields(t *testing.T) {
	for _, suffix := range []string{"", "/", "/v", "/v/"} {
		t.Run("path="+suffix, func(t *testing.T) {
			a, cookie, previous := fnosSettingsFixture(t)
			incoming := redactedFNOSSettings(t, a)
			incoming.FNOS.Host = "https://192.168.8.129:5667" + suffix
			incoming.LogLevel = "WARN"
			raw, err := json.Marshal(incoming)
			if err != nil {
				t.Fatal(err)
			}
			w := callTest(t, a.Handler(), http.MethodPut, "/api/settings", string(raw), cookie, "")
			if w.Code != http.StatusOK {
				t.Fatalf("equivalent address rejected: status=%d body=%s", w.Code, w.Body)
			}
			saved := a.Settings()
			if saved.FNOS.Password != previous.FNOS.Password || saved.FNOS.LibraryGUID != previous.FNOS.LibraryGUID || !reflect.DeepEqual(saved.FNOS.Mappings, previous.FNOS.Mappings) || saved.LogLevel != "WARN" {
				t.Fatal("equivalent endpoint or unrelated settings update lost stored configuration")
			}
			if strings.Contains(w.Body.String(), previous.FNOS.Password) {
				t.Fatal("saved credential leaked in the public response")
			}
		})
	}
}

func TestFNOSSettingsDefaultPrivateEntrySwitchPreservesCredentials(t *testing.T) {
	for _, tc := range []struct{ from, to string }{
		{"https://192.168.8.129:5667/v", "http://192.168.8.129:8005"},
		{"https://192.168.8.129:5667/v", "http://192.168.8.129:5666"},
		{"http://192.168.8.129:5666/v", "https://192.168.8.129:5667"},
	} {
		t.Run(tc.from+"->"+tc.to, func(t *testing.T) {
			a, cookie, previous := fnosSettingsFixture(t)
			previous.FNOS.Host = tc.from
			if err := a.saveSettings(context.Background(), previous); err != nil {
				t.Fatal(err)
			}
			incoming := redactedFNOSSettings(t, a)
			incoming.FNOS.Host = tc.to
			raw, err := json.Marshal(incoming)
			if err != nil {
				t.Fatal(err)
			}
			w := callTest(t, a.Handler(), http.MethodPut, "/api/settings", string(raw), cookie, "")
			if w.Code != http.StatusOK {
				t.Fatalf("same NAS default entry rejected: status=%d body=%s", w.Code, w.Body)
			}
			saved := a.Settings()
			if saved.FNOS.Host != tc.to || saved.FNOS.Password != previous.FNOS.Password || saved.FNOS.LibraryGUID != previous.FNOS.LibraryGUID || !reflect.DeepEqual(saved.FNOS.Mappings, previous.FNOS.Mappings) {
				t.Fatal("default entry switch lost stored credentials or legacy configuration")
			}
			if strings.Contains(w.Body.String(), previous.FNOS.Password) {
				t.Fatal("retained credential leaked in the public response")
			}
		})
	}
}

func TestFNOSCredentialEndpointDefaultPrivateEntries(t *testing.T) {
	entries := []string{"http://192.168.8.129:8005", "http://192.168.8.129:5666/v", "https://192.168.8.129:5667/v/"}
	for _, incoming := range entries {
		for _, previous := range entries {
			if !sameFNOSCredentialEndpoint(incoming, previous) {
				t.Fatalf("same private NAS default entries were not equivalent: %s -> %s", previous, incoming)
			}
		}
	}
	for _, tc := range []struct {
		incoming, previous string
		want               bool
	}{
		{"http://10.2.3.4:5666", "https://10.2.3.4:5667", true},
		{"http://172.31.1.2:8005", "https://172.31.1.2:5667", true},
		{"http://[fd00::1]:5666", "https://[fd00::1]:5667", true},
		{"http://[::ffff:192.168.8.129]:5666", "https://192.168.8.129:5667", true},
		{"http://192.168.8.130:5666", "https://192.168.8.129:5667", false},
		{"http://192.168.8.129:9999", "https://192.168.8.129:5667", false},
		{"http://192.168.8.129:5667", "https://192.168.8.129:5667", false},
		{"https://192.168.8.129:5666", "https://192.168.8.129:5667", false},
		{"https://198.51.100.1:5666", "https://198.51.100.1:5667", false},
		{"https://nas.example:5666", "https://nas.example:5667", false},
		{"http://localhost:5666", "https://localhost:5667", false},
		{"http://127.0.0.1:5666", "https://127.0.0.1:5667", false},
		{"http://192.168.8.129:5666/v?target=other", "https://192.168.8.129:5667", false},
		{"http://192.168.8.129:5666/other", "https://192.168.8.129:5667", false},
	} {
		if got := sameFNOSCredentialEndpoint(tc.incoming, tc.previous); got != tc.want {
			t.Errorf("sameFNOSCredentialEndpoint(%q, %q)=%t want %t", tc.incoming, tc.previous, got, tc.want)
		}
	}
}

func TestFNOSSettingsExplicitCredentialsAllowDestinationChange(t *testing.T) {
	a, cookie, _ := fnosSettingsFixture(t)
	incoming := redactedFNOSSettings(t, a)
	incoming.FNOS.Host = "http://192.168.8.129:8005/v"
	incoming.FNOS.Username = "different-user"
	incoming.FNOS.Password = "synthetic-replacement-password"
	raw, err := json.Marshal(incoming)
	if err != nil {
		t.Fatal(err)
	}
	w := callTest(t, a.Handler(), http.MethodPut, "/api/settings", string(raw), cookie, "")
	if w.Code != http.StatusOK {
		t.Fatalf("explicit credentials rejected: status=%d body=%s", w.Code, w.Body)
	}
	saved := a.Settings()
	if saved.FNOS.Host != incoming.FNOS.Host || saved.FNOS.Username != incoming.FNOS.Username || saved.FNOS.Password != incoming.FNOS.Password {
		t.Fatal("explicit replacement configuration was not saved")
	}
	if strings.Contains(w.Body.String(), incoming.FNOS.Password) {
		t.Fatal("replacement credential leaked in the public response")
	}
}

func TestFNOSCredentialEndpointDoesNotEquateInvalidAddresses(t *testing.T) {
	if sameFNOSCredentialEndpoint("not-a-url", "also-not-a-url") {
		t.Fatal("different invalid addresses were treated as the same destination")
	}
	if !sameFNOSCredentialEndpoint("legacy-invalid-address", "legacy-invalid-address") {
		t.Fatal("an unrelated settings save changed the identity of an unchanged legacy address")
	}
}
