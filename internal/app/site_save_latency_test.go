package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func cookieSaveFixture(t *testing.T) (*App, SiteConfig) {
	t.Helper()
	a := newTestApp(t)
	in := identityTestSite("Synthetic", "site.example.com")
	if _, err := a.saveManagedSiteWithResponse(context.Background(), in, true); err != nil {
		t.Fatal(err)
	}
	return a, a.Settings().Sites[0]
}
func TestSiteCookieSaveDoesNotWaitForImportDownload(t *testing.T) {
	a, in := cookieSaveFixture(t)
	in.Cookie = "synthetic-not-live"
	a.importMu.RLock()
	defer a.importMu.RUnlock()
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := a.saveManagedSiteWithResponse(ctx, in, true)
	if err != nil {
		t.Fatal(err)
	}
	if a.Settings().Sites[0].Cookie != in.Cookie {
		t.Fatal("credential not committed")
	}
	raw, _ := json.Marshal(result)
	if !strings.Contains(string(raw), in.Cookie) {
		t.Fatal("credential not echoed")
	}
	if !strings.Contains(string(raw), `"configuration_only":true`) {
		t.Fatal("missing save acknowledgement")
	}
}
func TestSiteSaveLockCancellation(t *testing.T) {
	for _, which := range []string{"settings", "plugin", "import", "download"} {
		t.Run(which, func(t *testing.T) {
			a, in := cookieSaveFixture(t)
			in.Name = "Structural edit retains safety locks"
			var unlock func()
			switch which {
			case "settings":
				a.settingsMu.Lock()
				unlock = a.settingsMu.Unlock
			case "plugin":
				a.pluginMu.Lock()
				unlock = a.pluginMu.Unlock
			case "download":
				a.downloadMu.Lock()
				unlock = a.downloadMu.Unlock
			case "import":
				a.importMu.RLock()
				unlock = a.importMu.RUnlock
			}
			defer unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			started := time.Now()
			_, err := a.saveManagedSiteWithResponse(ctx, in, true)
			if err != context.DeadlineExceeded || time.Since(started) > time.Second {
				t.Fatalf("lock cancellation: %v", err)
			}
			if a.Settings().Sites[0].Name == in.Name {
				t.Fatal("canceled save committed")
			}
		})
	}
}
func TestSiteSaveCredentialScopeAndCommittedAcknowledgement(t *testing.T) {
	a, in := cookieSaveFixture(t)
	old := a.Settings()
	next := a.Settings()
	next.Sites[0].Cookie = "new"
	if !siteCredentialsOnly(old, next) {
		t.Fatal("cookie rejected")
	}
	next.Sites[0].Enabled = !old.Sites[0].Enabled
	if siteCredentialsOnly(old, next) {
		t.Fatal("enable change bypassed locks")
	}
	next = a.Settings()
	next.Sites[0].BaseURL = "https://other.example.com"
	if siteCredentialsOnly(old, next) {
		t.Fatal("origin change bypassed locks")
	}
	// Corrupt historical statistics must not make a committed configuration save
	// report failure. GET remains responsible for reporting its own read errors.
	if err := a.Store.Put(context.Background(), "site_connection", in.ID, map[string]string{"bad": "history"}); err != nil {
		t.Fatal(err)
	}
	in.Cookie = "synthetic-updated"
	if _, err := a.saveManagedSiteWithResponse(context.Background(), in, true); err != nil {
		t.Fatal(err)
	}
	if a.Settings().Sites[0].Cookie != in.Cookie {
		t.Fatal("not saved")
	}
}

func TestSiteCredentialConcurrentSavesKeepBothUpdates(t *testing.T) {
	a, first := cookieSaveFixture(t)
	if _, err := a.saveManagedSite(context.Background(), identityTestSite("Second", "second.example.com")); err != nil {
		t.Fatal(err)
	}
	second := a.Settings().Sites[1]
	first.Cookie = "synthetic-first"
	second.APIKey = "synthetic-second"
	done := make(chan error, 2)
	for _, site := range []SiteConfig{first, second} {
		go func(in SiteConfig) {
			_, err := a.saveManagedSiteWithResponse(context.Background(), in, true)
			done <- err
		}(site)
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	settings := a.Settings()
	if settings.Sites[0].Cookie != first.Cookie || settings.Sites[1].APIKey != second.APIKey {
		t.Fatal("lost concurrent site update")
	}
	var persisted Settings
	if err := a.Store.Get(context.Background(), "config", "main", &persisted); err == nil {
		// Config is encrypted, so ordinary Get must not expose plaintext.
		t.Fatal("encrypted config accepted as ordinary JSON")
	}
}

func TestSiteCredentialSaveCanceledWriterWaitDoesNotPublish(t *testing.T) {
	a, in := cookieSaveFixture(t)
	in.Cookie = "synthetic-never-committed"
	tx, err := a.Store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := a.saveManagedSiteWithResponse(ctx, in, true); err != context.DeadlineExceeded {
		t.Fatal(err)
	}
	if a.Settings().Sites[0].Cookie == in.Cookie {
		t.Fatal("uncommitted settings published")
	}
}

func TestSiteCredentialNormalizationCannotWidenBypass(t *testing.T) {
	a, in := cookieSaveFixture(t)
	a.cfgMu.Lock()
	a.config.AutoImportMode = "copy"
	a.cfgMu.Unlock()
	in.Cookie = "synthetic-new"
	a.importMu.RLock()
	defer a.importMu.RUnlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := a.saveManagedSiteWithResponse(ctx, in, true); err != context.DeadlineExceeded {
		t.Fatal("normalized non-site settings bypassed import lock", err)
	}
}

func TestSiteCredentialLegacyOtherEnabledSiteCannotBypassLocks(t *testing.T) {
	a, in := cookieSaveFixture(t)
	if _, err := a.saveManagedSite(context.Background(), identityTestSite("Second", "second.example.com")); err != nil {
		t.Fatal(err)
	}
	a.cfgMu.Lock()
	a.config.Sites[1].Kind = "jiayuan"
	a.config.Sites[1].BaseURL = "https://second.example.com/"
	a.config.Sites[1].Domain = "legacy.invalid"
	a.cfgMu.Unlock()
	in.Cookie = "synthetic-new"
	a.importMu.RLock()
	defer a.importMu.RUnlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := a.saveManagedSiteWithResponse(ctx, in, true); err != context.DeadlineExceeded {
		t.Fatal("legacy other site normalization bypassed import lock", err)
	}
	if a.Settings().Sites[0].Cookie == in.Cookie {
		t.Fatal("canceled change committed")
	}
}

func TestSiteSaveHTTPResponseOptInCompatibility(t *testing.T) {
	a, in := cookieSaveFixture(t)
	cookie := setupTestAdmin(t, a)
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, variant := range []struct {
		query string
		fast  bool
	}{{"", false}, {"?response=configuration", true}, {"?response=unknown", false}} {
		w := callTest(t, a.Handler(), "POST", "/api/sites/manage"+variant.query, string(raw), cookie, "")
		if w.Code != 200 {
			t.Fatalf("response %s: %d", variant.query, w.Code)
		}
		var out struct {
			Data struct {
				ConfigurationOnly bool              `json:"configuration_only"`
				Sites             []json.RawMessage `json:"sites"`
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.Data.ConfigurationOnly != variant.fast || len(out.Data.Sites) != 1 {
			t.Fatalf("wrong response mode %s", variant.query)
		}
	}
}
