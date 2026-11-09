package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/sites"
)

func TestMoviePilotOptionsRoundTripAndPublicSecrets(t *testing.T) {
	a := newTestApp(t)
	raw := json.RawMessage(`[{"name":"通用站点","domain":"example.com","url":"https://tracker.example.com/","cookie":"synthetic-options-cookie","apikey":"synthetic-options-key","token":"synthetic-options-token","ua":"Synthetic Browser/1.0","rss":"https://tracker.example.com/rss.php?passkey=synthetic-passkey","downloader":"我的下载器","filter":"1080p","proxy":true,"render":"1","public":0,"note":{"value":"synthetic-private-note"},"pri":"7","timeout":"45","is_active":1,"limit_interval":"60","limit_count":"5","limit_seconds":2}]`)
	report, err := a.importManagedSites(context.Background(), raw)
	if err != nil || report.Created != 1 || report.Failed != 0 {
		t.Fatalf("MoviePilot controls were not accepted: %v", err)
	}
	stored := a.Settings().Sites[0]
	if stored.Kind != "nexusphp" || stored.BaseURL != "https://tracker.example.com" || !stored.Enabled || !stored.Proxy || !stored.Render || stored.Public || stored.Priority != 7 || stored.Timeout != 45 || stored.LimitInterval != 60 || stored.LimitCount != 5 || stored.LimitSeconds != 2 || stored.UserAgent != "Synthetic Browser/1.0" || stored.Downloader != "我的下载器" || stored.Filter != "1080p" {
		t.Fatalf("MoviePilot options changed during normalization: kind=%s base=%s enabled=%t proxy=%t render=%t public=%t priority=%d timeout=%d interval=%d count=%d seconds=%d ua=%q downloader=%q filter=%q", stored.Kind, stored.BaseURL, stored.Enabled, stored.Proxy, stored.Render, stored.Public, stored.Priority, stored.Timeout, stored.LimitInterval, stored.LimitCount, stored.LimitSeconds, stored.UserAgent, stored.Downloader, stored.Filter)
	}
	cookie := setupTestAdmin(t, a)
	for _, path := range []string{"/api/sites/manage", "/api/settings", "/api/logs"} {
		response := callTest(t, a.Handler(), "GET", path, "", cookie, "")
		for _, secret := range []string{stored.Cookie, stored.APIKey, stored.Token, "synthetic-passkey", "synthetic-private-note"} {
			if strings.Contains(response.Body.String(), secret) {
				t.Fatal("ordinary site response exposed a protected field")
			}
		}
	}
	public, _ := a.managedSites(context.Background())
	visible := public.(map[string]any)["sites"].([]map[string]any)[0]["config"].(SiteConfig)
	visible.Name = "修改名称"
	if _, err := a.saveManagedSite(context.Background(), visible); err != nil {
		t.Fatal(err)
	}
	stored.Name = "修改名称"
	if a.Settings().Sites[0] != stored {
		t.Fatal("editing redacted config erased saved private fields")
	}
	projection, _ := json.Marshal(a.publicSettings())
	var settings Settings
	if err := json.Unmarshal(projection, &settings); err != nil {
		t.Fatal(err)
	}
	a.preserveSecrets(&settings)
	if settings.Sites[0] != stored {
		t.Fatal("unrelated settings save lost private site fields")
	}
	exported := callTest(t, a.Handler(), "GET", "/api/sites/export", "", cookie, "")
	var entries []map[string]any
	if exported.Code != http.StatusOK || json.Unmarshal(exported.Body.Bytes(), &entries) != nil || len(entries) != 1 || entries[0]["url"] != stored.BaseURL || entries[0]["domain"] != "example.com" || entries[0]["token"] != stored.Token || entries[0]["ua"] != stored.UserAgent {
		t.Fatal("export is not a complete MoviePilot array")
	}
	other := newTestApp(t)
	if imported, err := other.importManagedSites(context.Background(), exported.Body.Bytes()); err != nil || imported.Created != 1 {
		t.Fatal("own MoviePilot array was not accepted")
	}
	restored := other.Settings().Sites[0]
	stored.ID = restored.ID
	if restored != stored {
		t.Fatal("MoviePilot round trip lost credentials, options, or note")
	}
}

func TestSiteConfigCanBeSavedBeforeCredentialValidation(t *testing.T) {
	a := newTestApp(t)
	raw := json.RawMessage(`[{"name":"待配置站点","url":"https://new.example.com","is_active":true},{"name":"馒头","url":"https://kp.m-team.cc","token":"synthetic-token-not-apikey"}]`)
	report, err := a.importManagedSites(context.Background(), raw)
	if err != nil || report.Created != 2 || report.Failed != 0 || len(report.Results[0].Warnings) == 0 || len(report.Results[1].Warnings) == 0 {
		t.Fatal("unconfigured sites must be saved with explicit pending-credential warnings")
	}
	for _, configured := range a.Settings().Sites {
		if configured.APIKey != "" || configured.Cookie != "" {
			t.Fatal("configuration validation placeholder or token became a credential")
		}
		if _, err := sites.New(configured.Config); err == nil {
			t.Fatal("actual requests accepted a site without the required credential")
		}
	}
	managed, err := a.managedSites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range managed.(map[string]any)["sites"].([]map[string]any) {
		if row["connection"].(managedConnection).Status != "" || row["credential_configured"] != false {
			t.Fatal("saving configuration falsely reported a working connection")
		}
	}
}

func TestSiteImportMoviePilotNumericIDsAndSpecificDomain(t *testing.T) {
	a := newTestApp(t)
	raw := json.RawMessage(`[{"id":42,"name":"原版站点","domain":"tracker.example.com","url":"https://tracker.example.com","cookie":"synthetic-cookie","timeout":null,"proxy":null,"is_active":true},{"id":null,"name":"另一站点","domain":"second.example.com","url":"https://second.example.com","cookie":null}]`)
	report, err := a.importManagedSites(context.Background(), raw)
	if err != nil || report.Created != 2 || report.Failed != 0 {
		t.Fatal("MoviePilot numeric or null database ID prevented import")
	}
	stored := a.Settings().Sites[0]
	if stored.ID == "42" || stored.Domain != "tracker.example.com" {
		t.Fatal("foreign ID was trusted or MoviePilot domain key was changed")
	}
	exported := callTest(t, a.Handler(), "GET", "/api/sites/export", "", setupTestAdmin(t, a), "")
	var entries []map[string]any
	if json.Unmarshal(exported.Body.Bytes(), &entries) != nil || entries[0]["domain"] != "tracker.example.com" {
		t.Fatal("MoviePilot domain key was not preserved for export")
	}
}

func TestSiteImportSameOriginMigratesLegacyAdapterWithoutChangingID(t *testing.T) {
	a := newTestApp(t)
	// Legacy configurations could select a classic adapter for another tracker.
	old := SiteConfig{Config: sites.Config{ID: "legacy-site", Kind: "ubits", Name: "旧名称", BaseURL: "https://tracker.example.com", Cookie: "synthetic-legacy-cookie"}, Enabled: true}
	a.cfgMu.Lock()
	a.config.Sites = []SiteConfig{old}
	a.cfgMu.Unlock()
	report, err := a.importManagedSites(context.Background(), json.RawMessage(`[{"id":19,"name":"新名称","url":"https://tracker.example.com","cookie":null}]`))
	if err != nil || report.Updated != 1 || report.Created != 0 {
		t.Fatal("same-origin legacy adapter was duplicated during migration")
	}
	stored := a.Settings().Sites[0]
	if stored.ID != old.ID || stored.Kind != "nexusphp" || stored.Cookie != old.Cookie {
		t.Fatal("same-origin migration lost stable identity or credentials")
	}
}

func TestManagedSiteEditKeepsLegacySameOriginCredentials(t *testing.T) {
	a := newTestApp(t)
	old := SiteConfig{Config: sites.Config{ID: "legacy-site", Kind: "ubits", Name: "旧名称", BaseURL: "https://tracker.example.com/", Cookie: "synthetic-legacy-cookie"}, Enabled: true}
	a.cfgMu.Lock()
	a.config.Sites = []SiteConfig{old}
	a.cfgMu.Unlock()
	edit := old
	hideSiteSecrets(&edit)
	edit.Name = "新名称"
	if _, err := a.saveManagedSite(context.Background(), edit); err != nil {
		t.Fatal(err)
	}
	stored := a.Settings().Sites[0]
	if stored.ID != old.ID || stored.Kind != "nexusphp" || stored.Cookie != old.Cookie {
		t.Fatal("editing legacy same-origin configuration erased its credential")
	}
	edit.BaseURL = "https://another.example.com"
	if _, err := a.saveManagedSite(context.Background(), edit); err != nil {
		t.Fatal(err)
	}
	if a.Settings().Sites[0].Cookie != "" {
		t.Fatal("editing another origin reused a credential")
	}
}

func TestManagedSiteRejectsDuplicateOriginOnAddAndEdit(t *testing.T) {
	for _, pair := range [][2]string{
		{"https://tracker.example.com", "https://TRACKER.EXAMPLE.COM:443/"},
		{"https://api.m-team.cc", "https://kp.m-team.cc/"},
	} {
		t.Run(pair[0], func(t *testing.T) {
			a := newTestApp(t)
			first := SiteConfig{Config: sites.Config{Name: "已有站点", BaseURL: pair[0]}, Enabled: true}
			if _, err := a.saveManagedSite(context.Background(), first); err != nil {
				t.Fatal(err)
			}
			before := a.Settings().Sites
			duplicate := SiteConfig{Config: sites.Config{Name: "重复站点", BaseURL: pair[1]}, Enabled: true}
			if _, err := a.saveManagedSite(context.Background(), duplicate); err == nil || !reflect.DeepEqual(before, a.Settings().Sites) {
				t.Fatal("adding the same normalized origin created a duplicate")
			}
			second := SiteConfig{Config: sites.Config{Name: "另一站点", BaseURL: "https://other.example.com"}, Enabled: true}
			if _, err := a.saveManagedSite(context.Background(), second); err != nil {
				t.Fatal(err)
			}
			before = a.Settings().Sites
			edit := before[1]
			edit.BaseURL = pair[1]
			if _, err := a.saveManagedSite(context.Background(), edit); err == nil || !reflect.DeepEqual(before, a.Settings().Sites) {
				t.Fatal("changing a URL to an existing origin created an ambiguous site")
			}
			edit = before[0]
			edit.Name = "允许修改名称"
			if _, err := a.saveManagedSite(context.Background(), edit); err != nil || len(a.Settings().Sites) != 2 || a.Settings().Sites[0].Name != edit.Name {
				t.Fatal("editing the same site was incorrectly rejected as a duplicate")
			}
		})
	}
}

func TestSiteImportSerialAndBatchKeepDifferentOrigins(t *testing.T) {
	entries := []map[string]any{
		{"name": "通用一", "url": "https://first.example.com", "cookie": "synthetic-first"},
		{"name": "通用二", "url": "https://second.example.com", "cookie": "synthetic-second"},
		{"name": "馒头CC", "url": "https://api.m-team.cc", "apikey": "synthetic-cc"},
		{"name": "馒头IO", "url": "https://api.m-team.io", "apikey": "synthetic-io"},
	}
	for _, serial := range []bool{false, true} {
		t.Run(fmt.Sprint(serial), func(t *testing.T) {
			a := newTestApp(t)
			if serial {
				for _, entry := range entries {
					report, err := a.importManagedSites(context.Background(), siteImportJSON(t, []any{entry}))
					if err != nil || report.Created != 1 || report.Updated != 0 {
						t.Fatal("serial import replaced a different origin")
					}
				}
			} else {
				report, err := a.importManagedSites(context.Background(), siteImportJSON(t, entries))
				if err != nil || report.Created != len(entries) || report.Updated != 0 {
					t.Fatal("batch import merged origins")
				}
			}
			before := a.Settings().Sites
			report, err := a.importManagedSites(context.Background(), siteImportJSON(t, entries))
			if err != nil || report.Updated != len(entries) || !reflect.DeepEqual(before, a.Settings().Sites) {
				t.Fatal("same-origin reimport did not preserve stable records")
			}
		})
	}
}

func TestSiteImportMoreThan32AndCancelledSaveIsAtomic(t *testing.T) {
	a := newTestApp(t)
	entries := make([]map[string]any, 40)
	for i := range entries {
		entries[i] = map[string]any{"name": fmt.Sprintf("站点%d", i), "url": fmt.Sprintf("https://site%d.example.com", i), "cookie": "synthetic-cookie"}
	}
	report, err := a.importManagedSites(context.Background(), siteImportJSON(t, entries))
	if err != nil || report.Created != 40 || len(a.Settings().Sites) != 40 {
		t.Fatal("valid generic import is still limited to five adapters or 32 records")
	}
	before := a.Settings().Sites
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	entries[0]["name"] = "must-not-publish"
	report, err = a.importManagedSites(cancelled, siteImportJSON(t, entries))
	if err == nil || report.Imported != 0 || !reflect.DeepEqual(before, a.Settings().Sites) {
		t.Fatal("failed save published partial settings or successful import counts")
	}
	if strings.Contains(err.Error(), "synthetic") {
		t.Fatal("failed save leaked raw import data")
	}
}

func TestSitePrioritySaveValidatesWholeSelection(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.importManagedSites(context.Background(), siteImportJSON(t, []map[string]any{
		{"name": "一", "url": "https://first.example.com"},
		{"name": "二", "url": "https://second.example.com"},
	})); err != nil {
		t.Fatal(err)
	}
	before := a.Settings().Sites
	if _, err := a.saveSitePriorities(context.Background(), []sitePriority{{ID: before[0].ID, Priority: 2}, {ID: "missing", Priority: 1}}); err == nil || !reflect.DeepEqual(before, a.Settings().Sites) {
		t.Fatal("partial invalid sorting changed saved priorities")
	}
	result, err := a.saveSitePriorities(context.Background(), []sitePriority{{ID: before[0].ID, Priority: 2}, {ID: before[1].ID, Priority: 1}})
	if err != nil {
		t.Fatal(err)
	}
	rows := result.(map[string]any)["sites"].([]map[string]any)
	if rows[0]["config"].(SiteConfig).ID != before[1].ID || rows[1]["config"].(SiteConfig).ID != before[0].ID {
		t.Fatal("managed list does not follow saved priority")
	}
}
