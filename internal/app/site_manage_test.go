package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"light-media/internal/download"
	"light-media/internal/sites"
)

func TestManagedSiteCRUDProtectsCredentialsAndAdaptation(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	s := SiteConfig{Config: sites.Config{Kind: "jiayuan", Name: "家园", BaseURL: "https://hdhome.org", Cookie: "synthetic-cookie-not-live"}, Enabled: true, DailyBudget: 600}
	result, e := a.saveManagedSite(ctx, s)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), s.Cookie) {
		t.Fatal("secret returned")
	}
	if len(a.Settings().Sites) != 1 || a.Settings().Sites[0].ID == "" {
		t.Fatal("new site not persisted")
	}
	s = a.Settings().Sites[0]
	s.Name = "家园2"
	s.Cookie = ""
	if _, e = a.saveManagedSite(ctx, s); e != nil {
		t.Fatal(e)
	}
	if a.Settings().Sites[0].Cookie != "synthetic-cookie-not-live" {
		t.Fatal("blank editor erased credential")
	}
	s.BaseURL = "https://not-adapted.invalid"
	s.Cookie = "synthetic-other-secret"
	if _, e = a.saveManagedSite(ctx, s); e == nil {
		t.Fatal("unknown host pretended supported")
	}
	if len(a.Settings().Sites) != 1 || a.Settings().Sites[0].BaseURL != "https://hdhome.org" {
		t.Fatal("failed save mutated config")
	}
	if _, e = a.deleteManagedSite(ctx, a.Settings().Sites[0].ID); e != nil || len(a.Settings().Sites) != 0 {
		t.Fatal(e)
	}
	m := SiteConfig{Config: sites.Config{ID: "mteam", Kind: "mteam", Name: "馒头", BaseURL: "https://kp.m-team.cc", APIKey: "fake-key"}, Enabled: true}
	if _, e = a.saveManagedSite(ctx, m); e != nil || a.Settings().Sites[0].BaseURL != "https://api.m-team.cc" {
		t.Fatal("UI origin not canonicalized", e)
	}
}

func TestManagedSiteFailedRefreshPreservesLastSuccessAndUnknown(t *testing.T) {
	failed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failed {
			http.Error(w, "failure", 503)
			return
		}
		switch r.URL.Path {
		case "/index.php":
			fmt.Fprint(w, `<a href="logout.php">退出</a><a href="userdetails.php?id=7">test</a><div>上传：1 GB 下载：0 B 总做种数：0 总做种体积：0 B</div>`)
		case "/userdetails.php":
			fmt.Fprint(w, `<a href="logout.php">退出</a>`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer server.Close()
	a := newTestApp(t)
	ctx := context.Background()
	s := SiteConfig{Config: sites.Config{ID: "home", Kind: "jiayuan", Name: "家园", BaseURL: server.URL, Cookie: "test-cookie"}, Enabled: true}
	if _, e := a.saveManagedSite(ctx, s); e != nil {
		t.Fatal(e)
	}
	if _, e := a.refreshManagedSite(ctx, "home"); e != nil {
		t.Fatal(e)
	}
	var before siteSnapshot
	if e := a.Store.Get(ctx, "site_stats_latest", "home", &before); e != nil {
		t.Fatal(e)
	}
	if before.Stats == nil || before.Stats.Seeding == nil || *before.Stats.Seeding != 0 || before.Stats.Bonus != nil {
		t.Fatalf("missing became zero %+v", before)
	}
	failed = true
	if _, e := a.refreshManagedSite(ctx, "home"); e != nil {
		t.Fatal(e)
	}
	var after, connection siteSnapshot
	_ = a.Store.Get(ctx, "site_stats_latest", "home", &after)
	_ = a.Store.Get(ctx, "site_connection", "home", &connection)
	if after.Stats == nil || after.Stats.CapturedAt != before.Stats.CapturedAt || connection.Connection.Status != "failed" || connection.Stats != nil {
		t.Fatal("failed refresh replaced successful account stats")
	}
	history, e := a.managedSiteHistory(ctx, "home")
	if e != nil {
		t.Fatal(e)
	}
	items := history.(map[string]any)["items"].([]managedSiteAccount)
	if len(items) != 1 {
		t.Fatal("history should retain one successful daily snapshot", len(items))
	}
}

func TestManagedDownloaderOwnConfigurationAndReferenceGuard(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	d := ManagedDownloaderRequest{DownloaderConfig: DownloaderConfig{Config: download.Config{Kind: "transmission", BaseURL: "http://127.0.0.1:9091", Username: "tester", Password: "synthetic-password"}, Name: "TR", DownloadPath: "/downloads"}, Default: true}
	out, e := a.saveManagedDownloader(ctx, d)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), d.Password) {
		t.Fatal("password returned")
	}
	old := a.Settings().Downloaders[0]
	d.DownloaderConfig = old
	d.Password = ""
	d.Name = "TR edited"
	if _, e = a.saveManagedDownloader(ctx, d); e != nil || a.Settings().Downloaders[0].Password != "synthetic-password" {
		t.Fatal("secret preservation failed", e)
	}
	if e = a.Store.Put(ctx, "subscription", "s1", map[string]any{"downloader_id": old.ID, "state": "paused"}); e != nil {
		t.Fatal(e)
	}
	if _, e = a.deleteManagedDownloader(ctx, old.ID); e == nil {
		t.Fatal("deleted downloader referenced by paused subscription")
	}
	_, _ = a.Store.DB.ExecContext(ctx, `DELETE FROM records WHERE kind='subscription'`)
	if _, e = a.deleteManagedDownloader(ctx, old.ID); e != nil || len(a.Settings().Downloaders) != 0 || a.Settings().DefaultDownloader != "" {
		t.Fatal("delete failed", e)
	}
}

func TestManagedDownloaderTestRequiresRealProtocolNotHTTP200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html>login</html>") }))
	defer server.Close()
	a := newTestApp(t)
	ctx := context.Background()
	_, e := a.saveManagedDownloader(ctx, ManagedDownloaderRequest{DownloaderConfig: DownloaderConfig{Config: download.Config{ID: "test-qb", Kind: "qbittorrent", BaseURL: server.URL}, Name: "test", DownloadPath: "/downloads"}})
	if e != nil {
		t.Fatal(e)
	}
	result, e := a.testManagedDownloader(ctx, "test-qb")
	if e != nil {
		t.Fatal(e)
	}
	if result.(map[string]any)["connection"].(managedConnection).Status != "failed" {
		t.Fatal("HTML200 treated as connected")
	}
}

func TestManagedConnectionsHTTPCRUDAndSecretResponses(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	h := a.Handler()
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := callTest(t, h, method, path, body, cookie, "")
		if w.Code != 200 {
			t.Fatalf("%s %s returned %d: %s", method, path, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "synthetic-http-secret") {
			t.Fatal("HTTP leaked saved credential")
		}
		return w
	}
	request("POST", "/api/sites/manage", `{"id":"http-home","kind":"jiayuan","name":"家园","base_url":"https://hdhome.org","cookie":"synthetic-http-secret","enabled":true,"daily_budget":600}`)
	w := request("GET", "/api/sites/manage", "")
	if !strings.Contains(w.Body.String(), `"credential_configured":true`) || !strings.Contains(w.Body.String(), `"stats":null`) {
		t.Fatal("missing observable configuration", w.Body)
	}
	request("POST", "/api/sites/manage", `{"id":"http-home","kind":"jiayuan","name":"家园 编辑","base_url":"https://hdhome.org","enabled":false,"daily_budget":600}`)
	request("GET", "/api/sites/manage/http-home/history", "")
	request("DELETE", "/api/sites/manage/http-home", "")
	request("POST", "/api/downloaders/manage", `{"id":"http-tr","kind":"transmission","name":"TR","base_url":"http://127.0.0.1:19091","username":"test","password":"synthetic-http-secret","download_path":"/downloads","mappings":[{"from":"/downloads","to":"/media/download"}],"default":true}`)
	w = request("GET", "/api/downloaders/manage", "")
	if !strings.Contains(w.Body.String(), `"default":true`) {
		t.Fatal("default downloader not reflected")
	}
	request("DELETE", "/api/downloaders/manage/http-tr", "")
	if len(a.Settings().Sites) != 0 || len(a.Settings().Downloaders) != 0 {
		t.Fatal("HTTP deletes were not persisted")
	}
}

func TestManagedCacheFingerprintsAreKeyedAndDoNotInvalidateIntentDestinations(t *testing.T) {
	a, other := newTestApp(t), newTestApp(t)
	d := DownloaderConfig{Config: download.Config{ID: "fixture", Kind: "qbittorrent", BaseURL: "http://127.0.0.1:8080", Username: "fixture-user", Password: "fixture-weak-password"}, Name: "fixture", DownloadPath: "/downloads"}
	plainVerifier := tokenHash(d.Kind + "\x00" + d.BaseURL + "\x00" + d.Username + "\x00" + d.Password)
	key := a.managedDownloaderKey(d)
	if key == plainVerifier || key == other.managedDownloaderKey(d) || key != a.managedDownloaderKey(d) {
		t.Fatal("connection cache fingerprint is not stable and installation-keyed")
	}
	s := SiteConfig{Config: sites.Config{ID: "fixture", Kind: "ubits", BaseURL: "https://ubits.club", Cookie: "fixture-cookie"}, Enabled: true}
	if a.managedSiteKey(s) == other.managedSiteKey(s) {
		t.Fatal("site fingerprint does not depend on the application key")
	}
	before := downloaderSnapshot(d)
	d.Password = "rotated-fixture-password"
	if key == a.managedDownloaderKey(d) || before != downloaderSnapshot(d) {
		t.Fatal("credential change must invalidate only the connection cache, not confirmed intent destinations")
	}
	d.DownloadPath = "/different-downloads"
	if before == downloaderSnapshot(d) {
		t.Fatal("destination change must still invalidate intent snapshot")
	}
}

func TestLegacyUnkeyedConnectionCacheIsUnverifiedUntilRefreshed(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	d := DownloaderConfig{Config: download.Config{ID: "legacy", Kind: "qbittorrent", BaseURL: "http://127.0.0.1:8080", Username: "fixture", Password: "fixture-password"}, Name: "fixture", DownloadPath: "/downloads"}
	a.config.Downloaders = []DownloaderConfig{d}
	legacyKey := tokenHash(d.Kind + "\x00" + d.BaseURL + "\x00" + d.Username + "\x00" + d.Password)
	if err := a.Store.Put(ctx, "downloader_connection", d.ID, managedDownloaderConnection{managedConnection: managedConnection{Status: "ok"}, ConfigKey: legacyKey}); err != nil {
		t.Fatal(err)
	}
	result, err := a.managedDownloaders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows := result.(map[string]any)["downloaders"].([]map[string]any)
	if rows[0]["connection"].(managedConnection).Status != "" {
		t.Fatal("legacy unkeyed connection result was trusted")
	}
	if err := a.Store.Put(ctx, "downloader_connection", d.ID, managedDownloaderConnection{managedConnection: managedConnection{Status: "ok"}, ConfigKey: a.managedDownloaderKey(d)}); err != nil {
		t.Fatal(err)
	}
	result, err = a.managedDownloaders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows = result.(map[string]any)["downloaders"].([]map[string]any)
	if rows[0]["connection"].(managedConnection).Status != "ok" {
		t.Fatal("current keyed cache was not recognized")
	}
}
