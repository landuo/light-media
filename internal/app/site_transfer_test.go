package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/sites"
)

func siteImportJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSiteImportMoviePilotNativeArrayAndPartialResults(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	old := SiteConfig{Config: sites.Config{ID: "existing-home", Kind: "jiayuan", Name: "old home", BaseURL: "https://hdhome.org", Cookie: "synthetic-original-cookie"}, Enabled: true, DailyBudget: 900}
	if _, err := a.saveManagedSite(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "subscription", "subscription-stable", map[string]any{"site_ids": []string{old.ID}, "state": "active"}); err != nil {
		t.Fatal(err)
	}
	entries := []map[string]any{
		{"name": "馒头", "domain": "m-team.cc", "url": "https://kp.m-team.cc/", "apikey": "synthetic-mteam-key", "cookie": nil, "token": nil, "ua": "fixture-browser", "is_active": true, "pri": 1, "timeout": 15, "note": map[string]any{"unused": "fixture"}},
		{"name": "憨憨", "domain": "hhanclub.net", "url": "https://hhanclub.net", "cookie": "synthetic-hhan-cookie", "is_active": true},
		{"name": "UBits", "domain": "ubits.club", "url": "https://ubits.club", "cookie": "synthetic-ubits-cookie", "is_active": true},
		{"name": "家园新名称", "domain": "hdhome.org", "url": "https://hdhome.org/", "cookie": "synthetic-replaced-cookie", "is_active": false},
		{"name": "1PTBA", "domain": "1ptba.com", "url": "https://1ptba.com", "cookie": "synthetic-1ptba-cookie", "is_active": true},
		{"name": "synthetic-secret-in-name", "domain": "example.com", "url": "https://tracker.example.com", "cookie": "synthetic-generic-cookie"},
		{"name": "wrong domain", "domain": "ubits.club", "url": "https://hdhome.org", "cookie": "synthetic-mismatch-cookie"},
	}
	report, err := a.importManagedSites(ctx, siteImportJSON(t, entries))
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 7 || report.Imported != 6 || report.Created != 5 || report.Updated != 1 || report.Skipped != 0 || report.Failed != 1 {
		t.Fatalf("unexpected import counts: %+v", report)
	}
	if len(a.Settings().Sites) != 6 || a.Settings().Sites[5].Kind != "nexusphp" || report.Results[6].Code != "SITE_ORIGIN_MISMATCH" || len(report.Results[0].Warnings) != 0 || a.Settings().Sites[1].UserAgent != "fixture-browser" {
		t.Fatal("generic site or working MoviePilot options were not imported")
	}
	home := a.Settings().Sites[0]
	if home.ID != old.ID || home.Cookie != "synthetic-replaced-cookie" || home.Enabled || home.DailyBudget != 900 || home.Name != "家园新名称" {
		t.Fatal("existing site identity/settings were not preserved correctly")
	}
	if a.Settings().Sites[1].BaseURL != "https://api.m-team.cc" {
		t.Fatal("MoviePilot M-Team homepage was not canonicalized")
	}
	var subscription struct {
		SiteIDs []string `json:"site_ids"`
	}
	if err := a.Store.Get(ctx, "subscription", "subscription-stable", &subscription); err != nil || !reflect.DeepEqual(subscription.SiteIDs, []string{old.ID}) {
		t.Fatal("import changed subscription references")
	}
	raw := string(siteImportJSON(t, report))
	for _, secret := range []string{"synthetic-mteam-key", "synthetic-replaced-cookie", "synthetic-secret-in-name", "synthetic-generic-cookie", "synthetic-mismatch-cookie"} {
		if strings.Contains(raw, secret) {
			t.Fatal("import report exposed an untrusted or secret value")
		}
	}
	for _, result := range report.Results {
		if result.Warnings == nil {
			t.Fatal("warnings must always be an array")
		}
	}
}

func TestSiteExportImportRoundTripAuthenticationAndPrivateResponses(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	for _, config := range []SiteConfig{
		{Config: sites.Config{ID: "stable-home", Kind: "jiayuan", Name: "家园", BaseURL: "https://hdhome.org", Cookie: "synthetic-roundtrip-cookie"}, Enabled: true, DailyBudget: 721},
		{Config: sites.Config{ID: "stable-mteam", Kind: "mteam", Name: "馒头", BaseURL: "https://api.m-team.io", APIKey: "synthetic-roundtrip-key"}, Enabled: false, DailyBudget: 431},
	} {
		if _, err := a.saveManagedSite(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	cookie := setupTestAdmin(t, a)
	handler := a.Handler()
	if got := callTest(t, handler, "GET", "/api/sites/export", "", nil, ""); got.Code != http.StatusUnauthorized {
		t.Fatal("export did not require authentication")
	}
	if got := callTest(t, handler, "POST", "/api/sites/import", "[]", nil, ""); got.Code != http.StatusUnauthorized {
		t.Fatal("import did not require authentication")
	}
	if got := callTest(t, handler, "POST", "/api/sites/import", "[]", cookie, "https://unrelated.invalid"); got.Code != http.StatusForbidden {
		t.Fatal("cross-origin import accepted")
	}
	exported := callTest(t, handler, "GET", "/api/sites/export", "", cookie, "")
	if exported.Code != 200 || exported.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(exported.Header().Get("Content-Disposition"), `attachment; filename="light-media-sites-`) {
		t.Fatal("export lacks private attachment response")
	}
	var document []json.RawMessage
	if err := json.Unmarshal(exported.Body.Bytes(), &document); err != nil || len(document) != len(a.Settings().Sites) {
		t.Fatal("export is not a complete standalone migration document")
	}
	for i, raw := range document {
		decoded, _, err := decodeSiteImportEntry(raw, false)
		wanted := a.Settings().Sites[i]
		wanted.ID = ""
		if err != nil || decoded != wanted {
			t.Fatal("MoviePilot-compatible export lost connection fields")
		}
	}
	before := a.Settings().Sites
	imported := callTest(t, handler, "POST", "/api/sites/import", exported.Body.String(), cookie, "")
	var response struct {
		Data siteImportReport `json:"data"`
	}
	if json.Unmarshal(imported.Body.Bytes(), &response) != nil || imported.Code != 200 || response.Data.Updated != 2 || response.Data.Created != 0 || !reflect.DeepEqual(before, a.Settings().Sites) {
		t.Fatal("own export did not update the same stable site records")
	}
	other := newTestApp(t)
	report, err := other.importManagedSites(ctx, exported.Body.Bytes())
	if err != nil || report.Created != 2 || other.Settings().Sites[0].Cookie != before[0].Cookie || other.Settings().Sites[1].APIKey != before[1].APIKey || other.Settings().Sites[1].Enabled {
		t.Fatal("cross-installation migration lost connection data")
	}
	if other.Settings().Sites[0].ID == before[0].ID {
		t.Fatal("foreign IDs must not be trusted to select local records")
	}
	for _, path := range []string{"/api/sites/manage", "/api/settings", "/api/logs"} {
		result := callTest(t, handler, "GET", path, "", cookie, "")
		for _, secret := range []string{before[0].Cookie, before[1].APIKey} {
			if strings.Contains(result.Body.String(), secret) || strings.Contains(imported.Body.String(), secret) {
				t.Fatal("ordinary API exposed exported credentials")
			}
		}
	}
}

func TestSiteImportOriginAndCredentialBinding(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	old := SiteConfig{Config: sites.Config{ID: "stable-hhan", Kind: "hhanclub", Name: "憨憨", BaseURL: "https://hhanclub.net", Cookie: "synthetic-bound-cookie"}, Enabled: true}
	if _, err := a.saveManagedSite(ctx, old); err != nil {
		t.Fatal(err)
	}
	old = a.Settings().Sites[0]
	for _, value := range []string{
		"https://not-adapted.invalid", "https://hhanclub.net.attacker.invalid", "https://127.0.0.1", "https://localhost",
		"http://hhanclub.net", "https://hhanclub.net:9443", "https://hhanclub.net/path", "https://hhanclub.net/?passkey=synthetic-url-secret", "https://user:synthetic-password@hhanclub.net",
	} {
		report, err := a.importManagedSites(ctx, siteImportJSON(t, []map[string]any{{"name": "憨憨", "domain": "hhanclub.net", "url": value, "cookie": "synthetic-file-cookie"}}))
		if err != nil || report.Imported != 0 || report.Failed+report.Skipped != 1 || !reflect.DeepEqual(a.Settings().Sites, []SiteConfig{old}) {
			t.Fatal("unsafe imported URL changed configuration")
		}
		if strings.Contains(string(siteImportJSON(t, report)), "synthetic-") {
			t.Fatal("invalid URL/credential echoed in report")
		}
	}
	changed := []map[string]any{{"name": "憨憨", "domain": "hhanclub.net", "url": "https://www.hhanclub.net"}}
	report, err := a.importManagedSites(ctx, siteImportJSON(t, changed))
	if err != nil || report.Created != 1 || len(a.Settings().Sites) != 2 || a.Settings().Sites[0] != old || a.Settings().Sites[1].Cookie != "" || len(report.Results[0].Warnings) == 0 {
		t.Fatal("saved cookie was reused at another origin")
	}
	newID := a.Settings().Sites[1].ID
	changed[0]["cookie"] = "synthetic-new-origin-cookie"
	report, err = a.importManagedSites(ctx, siteImportJSON(t, changed))
	if err != nil || report.Updated != 1 || a.Settings().Sites[0] != old || a.Settings().Sites[1].ID != newID || a.Settings().Sites[1].Cookie != "synthetic-new-origin-cookie" {
		t.Fatal("explicit credential update changed another origin")
	}
	delete(changed[0], "cookie")
	report, err = a.importManagedSites(ctx, siteImportJSON(t, changed))
	if err != nil || report.Updated != 1 || a.Settings().Sites[1].Cookie != "synthetic-new-origin-cookie" {
		t.Fatal("same-origin import erased saved credentials")
	}
	forged := siteExportDocument{Format: siteExportFormat, Version: 1, Sites: []SiteConfig{{Config: sites.Config{ID: old.ID, Kind: "mteam", Name: "馒头", BaseURL: "https://hdhome.org", APIKey: "synthetic-key"}, Enabled: true}}}
	report, err = a.importManagedSites(ctx, siteImportJSON(t, forged))
	if err != nil || report.Failed != 1 || report.Results[0].Code != "SITE_ORIGIN_MISMATCH" || a.Settings().Sites[0].ID != old.ID {
		t.Fatal("adapter or imported ID bypassed URL identity")
	}
}

func TestSiteImportRejectsDuplicatesWithoutOverwritingOtherSites(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	other := SiteConfig{Config: sites.Config{ID: "untouched", Kind: "ubits", Name: "keep", BaseURL: "https://ubits.club", Cookie: "synthetic-untouched-cookie"}, Enabled: true, DailyBudget: 900}
	if _, err := a.saveManagedSite(ctx, other); err != nil {
		t.Fatal(err)
	}
	other = a.Settings().Sites[0]
	entries := []map[string]any{
		{"name": "家园", "url": "https://hdhome.org", "cookie": "synthetic-first-cookie"},
		{"name": "duplicate", "url": "https://hdhome.org/", "cookie": "synthetic-second-cookie"},
	}
	report, err := a.importManagedSites(ctx, siteImportJSON(t, entries))
	if err != nil || report.Created != 1 || report.Failed != 1 || len(a.Settings().Sites) != 2 || a.Settings().Sites[0] != other || a.Settings().Sites[1].Cookie != "synthetic-first-cookie" {
		t.Fatal("duplicate imports replaced records or unrelated site changed")
	}
}

func TestSiteImportNativeMultipleOriginsRoundTrip(t *testing.T) {
	original := []SiteConfig{
		{Config: sites.Config{ID: "from-cc", Kind: "mteam", Name: "馒头 CC", BaseURL: "https://api.m-team.cc", APIKey: "synthetic-cc-key"}, SiteOptions: SiteOptions{Domain: "m-team.cc"}, Enabled: true, DailyBudget: 821},
		{Config: sites.Config{ID: "from-io", Kind: "mteam", Name: "馒头 IO", BaseURL: "https://api.m-team.io", APIKey: "synthetic-io-key"}, SiteOptions: SiteOptions{Domain: "m-team.io"}, Enabled: false, DailyBudget: 532},
	}
	raw := siteImportJSON(t, siteExportDocument{Format: siteExportFormat, Version: 1, Sites: original})
	a := newTestApp(t)
	report, err := a.importManagedSites(context.Background(), raw)
	if err != nil || report.Created != 2 || report.Updated != 0 || report.Failed != 0 || len(a.Settings().Sites) != 2 {
		t.Fatal("native migration merged distinct origins into one site")
	}
	for i, imported := range a.Settings().Sites {
		wanted := original[i]
		wanted.ID = imported.ID
		if imported.ID == original[i].ID || imported != wanted {
			t.Fatal("native migration mixed origin credentials or lost configuration")
		}
	}
	before := a.Settings().Sites
	report, err = a.importManagedSites(context.Background(), raw)
	if err != nil || report.Created != 0 || report.Updated != 2 || report.Failed != 0 || !reflect.DeepEqual(a.Settings().Sites, before) {
		t.Fatal("native reimport failed to preserve both stable local IDs")
	}
	partial := newTestApp(t)
	if _, err := partial.saveManagedSite(context.Background(), original[0]); err != nil {
		t.Fatal(err)
	}
	report, err = partial.importManagedSites(context.Background(), siteImportJSON(t, siteExportDocument{Format: siteExportFormat, Version: 1, Sites: original[1:]}))
	if err != nil || report.Created != 1 || report.Updated != 0 || len(partial.Settings().Sites) != 2 || partial.Settings().Sites[0] != original[0] {
		t.Fatal("native import replaced an existing site's different origin")
	}
}

func TestSiteImportMoviePilotMultipleNewOriginsRemainSeparate(t *testing.T) {
	a := newTestApp(t)
	raw := siteImportJSON(t, []map[string]any{
		{"name": "家园", "domain": "hdhome.org", "url": "https://hdhome.org", "cookie": "synthetic-origin-one"},
		{"name": "家园 www", "domain": "hdhome.org", "url": "https://www.hdhome.org", "cookie": "synthetic-origin-two"},
	})
	report, err := a.importManagedSites(context.Background(), raw)
	if err != nil || report.Created != 2 || report.Updated != 0 || report.Failed != 0 || len(a.Settings().Sites) != 2 {
		t.Fatal("new MoviePilot entries at distinct origins were merged")
	}
	if a.Settings().Sites[0].Cookie != "synthetic-origin-one" || a.Settings().Sites[1].Cookie != "synthetic-origin-two" || a.Settings().Sites[0].ID == a.Settings().Sites[1].ID {
		t.Fatal("new MoviePilot origins do not retain independent identities and credentials")
	}
}

func TestSiteImportMalformedAndLimitedBodiesDoNotMutate(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	handler := a.Handler()
	for _, body := range []string{
		`null`, `{}`, `[]`, `{"format":"light-media-sites","version":2,"sites":[]}`,
		`[{"cookie":"synthetic-parse-secret"`, `[] {}`, `"synthetic-secret"`,
		`[{"name":"` + strings.Repeat("x", 2<<20) + `"}]`,
		string(siteImportJSON(t, make([]map[string]any, 129))),
	} {
		response := callTest(t, handler, "POST", "/api/sites/import", body, cookie, "")
		if response.Code != 400 || len(a.Settings().Sites) != 0 || strings.Contains(response.Body.String(), "synthetic-") {
			t.Fatal("invalid import accepted, mutated settings, or echoed file content")
		}
	}
	for _, entry := range []any{nil, "synthetic-value", map[string]any{"name": "safe", "url": "https://hdhome.org", "cookie": "synthetic-header\r\ninjection"}} {
		report, err := a.importManagedSites(context.Background(), siteImportJSON(t, []any{entry}))
		if err != nil || report.Imported != 0 || report.Failed != 1 || len(a.Settings().Sites) != 0 {
			t.Fatal("invalid entry or wrong credential type imported")
		}
	}
}

func TestSiteImportMoviePilotOptionalNullFields(t *testing.T) {
	a := newTestApp(t)
	raw := json.RawMessage(`[{"name":"家园","domain":"hdhome.org","url":"https://hdhome.org/","cookie":"synthetic-null-fields-cookie","apikey":null,"token":null,"ua":null,"rss":null,"downloader":null,"filter":null,"proxy":null,"render":null,"public":null,"note":null,"timeout":null,"limit_interval":null,"limit_count":null,"limit_seconds":null,"pri":null,"is_active":false}]`)
	report, err := a.importManagedSites(context.Background(), raw)
	if err != nil || report.Created != 1 || a.Settings().Sites[0].Enabled || len(report.Results[0].Warnings) != 0 {
		t.Fatal("optional null fields rejected or inactive site enabled")
	}
}

func TestSiteExportEmptyDocumentHasExplicitImportResult(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	exported := callTest(t, a.Handler(), "GET", "/api/sites/export", "", cookie, "")
	var document []json.RawMessage
	if exported.Code != 200 || json.Unmarshal(exported.Body.Bytes(), &document) != nil || document == nil || len(document) != 0 {
		t.Fatal("empty export must be a valid standalone document with an empty array")
	}
	_, err := a.importManagedSites(context.Background(), exported.Body.Bytes())
	if apiErr, ok := err.(*APIError); !ok || apiErr.Code != "SITE_IMPORT_EMPTY" || apiErr.Message != "文件中没有可导入的站点" {
		t.Fatal("empty export import did not clearly explain the empty file")
	}
}

func TestProductBackupAndRestoreRoutesRemoved(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	backupPath := filepath.Join(a.Dir, "backups", "existing.db")
	if err := os.MkdirAll(filepath.Dir(backupPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupPath, []byte("synthetic-retained-backup"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"/api/", "/light-media/api/"} {
		activeCookie := cookie
		if prefix == "/light-media/api/" {
			activeCookie = proxyLogin(t, a)
		}
		for _, tc := range []struct{ method, path string }{{"GET", "backups"}, {"POST", "backups"}, {"GET", "backups/existing.db"}, {"POST", "restore"}} {
			response := proxyRequest(t, a.Handler(), tc.method, prefix+tc.path, `{"filename":"existing.db","confirm":true}`, "", activeCookie, nil)
			if response.Code != http.StatusNotFound {
				t.Fatalf("removed product route %s %s returned %d", tc.method, prefix+tc.path, response.Code)
			}
		}
	}
	retained, err := os.ReadFile(backupPath)
	if err != nil || string(retained) != "synthetic-retained-backup" {
		t.Fatal("removing routes changed existing backup files")
	}
}
