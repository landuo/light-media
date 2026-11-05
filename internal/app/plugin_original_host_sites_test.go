package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/sites"
)

func TestOriginalHostBusinessSiteAccessFixedSourceReplay(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-business-20261031/source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	type state struct {
		Success, Fail int64
		Seconds       *int64
		LastState     int    `json:"lst_state"`
		LastModified  string `json:"lst_mod_date"`
		Note          map[string]int64
	}
	var oracle struct {
		Commit string `json:"source_commit"`
		Sites  struct {
			Cases []struct {
				Name     string
				Initial  *state
				Success  bool
				Seconds  *int64
				Clock    string
				Expected state
			}
		}
	}
	if err = json.Unmarshal(raw, &oracle); err != nil || oracle.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Sites.Cases) != 7 {
		t.Fatal("fixed source cases missing", err)
	}
	for _, sample := range oracle.Sites.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			start := plugins.SiteHostAccessV1{SchemaVersion: 1}
			if sample.Initial != nil {
				v := sample.Initial
				start.Success, start.Fail, start.Seconds, start.LastState, start.LastModified, start.Note = v.Success, v.Fail, v.Seconds, v.LastState, v.LastModified, v.Note
			}
			got := originalHostSiteAccessUpdate(start, sample.Initial != nil, plugins.SiteHostAccessResultV1{Success: sample.Success, Seconds: sample.Seconds}, sample.Clock)
			want := sample.Expected
			if got.Success != want.Success || got.Fail != want.Fail || got.LastState != want.LastState || got.LastModified != want.LastModified || !reflect.DeepEqual(got.Seconds, want.Seconds) || !reflect.DeepEqual(got.Note, want.Note) {
				t.Fatalf("source outcome changed: got=%+v want=%+v", got, want)
			}
		})
	}
}

func TestOriginalHostBusinessSitesActualSDKHistoryCloneAndAccess(t *testing.T) {
	a := newTestApp(t)
	cookie, worker := hostBusinessFixture(t, a)
	config := a.Settings()
	site := SiteConfig{Config: sites.Config{ID: "owned-site", Name: "Owned", Kind: "ubits", BaseURL: "http://127.0.0.1:9", Cookie: "OWNED_COOKIE_SECRET", UserAgent: "Owned-UA"}, SiteOptions: SiteOptions{Priority: 4}, Enabled: true}
	config.Sites = []SiteConfig{site}
	ownedDownloaderSave(t, a, config)
	var list plugins.SiteHostResultV1
	if err := hostBusinessCall(t, worker, plugins.SitesReadCapability, plugins.SiteHostRequestV1{}, &list); err != nil || len(list.Items) != 1 || list.Items[0].ID != site.ID {
		t.Fatal("site read", err)
	}
	encoded, _ := json.Marshal(list)
	if bytes.Contains(encoded, []byte("OWNED_COOKIE_SECRET")) || bytes.Contains(encoded, []byte("Owned-UA")) {
		t.Fatal("site credentials escaped")
	}
	zero, seven := int64(0), int64(7)
	ownedRecordAccount(t, a, site, ownedAccountObservation(t, a, site, "2026-10-19", "10:00:00", "", &zero))
	ownedRecordAccount(t, a, site, ownedAccountObservation(t, a, site, "2026-10-20", "11:00:00", "failed day", nil))
	var accounts plugins.SiteAccountHostResultV1
	if err := hostBusinessCall(t, worker, plugins.SitesAccountReadCapability, plugins.SiteAccountHostRequestV1{ID: site.ID, Latest: true}, &accounts); err != nil || len(accounts.Items) != 1 || !strings.Contains(string(accounts.Items[0].Fields), "2026-10-19") {
		t.Fatal("latest selected failed day", err)
	}
	if err := hostBusinessCall(t, worker, plugins.SitesAccountReadCapability, plugins.SiteAccountHostRequestV1{Date: "2026-10-20"}, &accounts); err != nil || len(accounts.Items) != 1 || !strings.Contains(string(accounts.Items[0].Fields), "failed day") {
		t.Fatal("date omitted error row", err)
	}
	ownedRecordAccount(t, a, site, ownedAccountObservation(t, a, site, "2026-10-20", "12:00:00", "", &seven))
	if err := hostBusinessCall(t, worker, plugins.SitesAccountReadCapability, plugins.SiteAccountHostRequestV1{ID: site.ID, Latest: true}, &accounts); err != nil || len(accounts.Items) != 1 || !strings.Contains(string(accounts.Items[0].Fields), `"upload":7`) {
		t.Fatal("daily recovery not visible", err, string(accounts.Items[0].Fields))
	}
	var access plugins.SiteHostAccessV1
	seconds := int64(0)
	if err := hostBusinessCall(t, worker, plugins.SitesResultWriteCapability, plugins.SiteHostAccessResultV1{ID: site.ID, Success: true, Seconds: &seconds}, &access); err != nil || access.Success != 1 || access.Seconds == nil || *access.Seconds != 1 {
		t.Fatal("source success result mismatch", err)
	}
	if err := hostBusinessCall(t, worker, plugins.SitesResultWriteCapability, plugins.SiteHostAccessResultV1{ID: site.ID}, &access); err != nil || access.Fail != 1 || access.Success != 1 || access.LastState != 1 {
		t.Fatal("source fail result mismatch", err)
	}
	response := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedHostBusiness/clone", `{"suffix":"Twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	v, ok := a.pluginRunningInstances.Load("OwnedHostBusinesstwin")
	if !ok {
		t.Fatal("clone missing")
	}
	if err := hostBusinessCall(t, v.(*originalPluginInstance), plugins.SitesReadCapability, plugins.SiteHostRequestV1{}, &list); err != nil || len(list.Items) != 1 {
		t.Fatal("clone resource call", err)
	}
	// Both instances see the same host state, while their private last-result
	// KV is isolated. Resource sharing is not a clone of host configuration.
	var sourceData, cloneData json.RawMessage
	key := "last"
	var err error
	if sourceData, err = a.pluginStore.originalBaseGetData(context.Background(), "OwnedHostBusiness", &key); err != nil {
		t.Fatal(err)
	}
	if cloneData, err = a.pluginStore.originalBaseGetData(context.Background(), "OwnedHostBusinesstwin", &key); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(sourceData, cloneData) {
		t.Fatal("clone private results aliased")
	}
}

func TestOriginalHostBusinessSitesActualSDKBrowseRSSMaterialAndFailures(t *testing.T) {
	a := newTestApp(t)
	_, worker := hostBusinessFixture(t, a)
	var searches, rssReads, materials atomic.Int32
	torrent := append([]byte("d4:infod6:lengthi1e4:name4:test12:piece lengthi16384e6:pieces20:"), bytes.Repeat([]byte{'x'}, 20)...)
	torrent = append(torrent, 'e', 'e')
	var fail atomic.Bool
	var encodedXML atomic.Bool
	var recoverXML atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			searches.Add(1)
			if r.Header.Get("Cookie") != "OWNED_COOKIE_SECRET" || r.URL.Query().Get("page") != "0" {
				t.Error("browse host credentials/page changed")
			}
			fmt.Fprint(w, `<a href="logout.php">Logout</a><a href="userdetails.php?id=1">Owned</a><table class="torrents"><tr><th>Title</th></tr><tr><td>category</td><td><table class="torrentname"><tr><td class="embedded"><a title="Owned.S01E01.1080p.WEB-DL" href="details.php?id=101">Owned</a></td></tr></table></td><td>1</td><td><span title="2026-10-15 18:30:00">date</span></td><td>1.5<br>GiB</td><td><a href="details.php?id=101#seeders">12</a></td><td>2</td><td>99</td></tr></table>`)
		case "/rss.php":
			rssReads.Add(1)
			if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.URL.Query().Get("passkey") != "OWNED_RSS_SECRET" {
				t.Error("RSS wrongly inherited login credentials")
			}
			if fail.Load() {
				http.Error(w, "owned unavailable", 503)
				return
			}
			if encodedXML.Load() {
				w.Header().Set("Content-Type", "text/xml; charset=UTF-8")
				_, _ = w.Write([]byte("<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><rss><channel><item><title>Caf\xe9</title><link>details.php?id=101</link></item></channel></rss>"))
				return
			}
			if recoverXML.Load() {
				_, _ = w.Write([]byte(`<rss><item><title>A & B</title><enclosure url="download.php?id=101&amp;passkey=OWNED_MATERIAL_SECRET" length="1"/></item></rss>`))
				return
			}
			fmt.Fprint(w, `<rss><channel><item><title>Owned RSS</title><description>owned</description><link>details.php?id=101</link><enclosure url="download.php?id=101&amp;passkey=OWNED_MATERIAL_SECRET" length="1"/><pubDate>Sat, 31 Oct 2026 00:00:00 GMT</pubDate></item><item><title></title><description>source skips empty title</description><enclosure url="download.php?id=102" length="2"/></item></channel></rss>`)
		case "/download.php":
			materials.Add(1)
			if r.Header.Get("Cookie") != "" || r.URL.Query().Get("passkey") != "OWNED_MATERIAL_SECRET" {
				t.Error("RSS material did not retain bound private enclosure")
			}
			_, _ = w.Write(torrent)
		default:
			http.Error(w, "unexpected endpoint", 500)
		}
	}))
	defer server.Close()
	config := a.Settings()
	site := SiteConfig{Config: sites.Config{ID: "owned-site", Name: "Owned", Kind: "ubits", BaseURL: server.URL, Cookie: "OWNED_COOKIE_SECRET"}, SiteOptions: SiteOptions{RSS: server.URL + "/rss.php?passkey=OWNED_RSS_SECRET"}, Enabled: true}
	config.Sites = []SiteConfig{site}
	ownedDownloaderSave(t, a, config)
	var browse plugins.SiteBrowseHostResultV1
	if err := hostBusinessCall(t, worker, plugins.SitesBrowseCapability, plugins.SiteBrowseHostRequestV1{ID: site.ID}, &browse); err != nil || len(browse.Items) != 1 || browse.Items[0].ID != "101" || searches.Load() != 1 {
		t.Fatal("browse", err, browse)
	}
	if err := hostBusinessCall(t, worker, plugins.SitesRSSCapability, plugins.SiteHostRefreshV1{ID: site.ID}, &browse); err != nil || len(browse.Items) != 1 || browse.Items[0].Title != "Owned RSS" || browse.Items[0].Subtitle != "" || rssReads.Load() != 1 {
		t.Fatal("RSS", err, browse)
	}
	encoded, _ := json.Marshal(browse)
	for _, secret := range []string{"OWNED_COOKIE_SECRET", "OWNED_RSS_SECRET", "OWNED_MATERIAL_SECRET", "download.php"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatal("RSS secret escaped", secret)
		}
	}
	var material plugins.SiteTorrentHostResultV1
	if err := hostBusinessCall(t, worker, plugins.SitesTorrentDownloadCapability, plugins.SiteTorrentHostRequestV1{ID: site.ID, ResourceID: browse.Items[0].ID}, &material); err != nil || !bytes.Equal(material.Torrent, torrent) || materials.Load() != 1 {
		t.Fatal("RSS material host chain", err)
	}
	encodedXML.Store(true)
	if err := hostBusinessCall(t, worker, plugins.SitesRSSCapability, plugins.SiteHostRefreshV1{ID: site.ID}, &browse); err != nil || len(browse.Items) != 1 || browse.Items[0].Title != "Café" {
		t.Fatal("source declared XML encoding priority changed", err, browse)
	}
	encodedXML.Store(false)
	recoverXML.Store(true)
	if err := hostBusinessCall(t, worker, plugins.SitesRSSCapability, plugins.SiteHostRefreshV1{ID: site.ID}, &browse); err != nil || len(browse.Items) != 1 || browse.Items[0].Title != "A  B" {
		t.Fatal("actual SDK did not reach source XML recovery", err, browse)
	}
	fail.Store(true)
	if err := hostBusinessCall(t, worker, plugins.SitesRSSCapability, plugins.SiteHostRefreshV1{ID: site.ID}, &browse); err == nil {
		t.Fatal("RSS HTTP failure became empty success")
	}
	if err := hostBusinessCall(t, worker, plugins.SitesTorrentDownloadCapability, plugins.SiteTorrentHostRequestV1{ID: site.ID, ResourceID: "https://unrelated.invalid/private"}, &material); err == nil {
		t.Fatal("arbitrary material URL accepted")
	}
}

func TestOriginalHostBusinessSitesActualSDKRefreshAtomicDailyAndSourceFailure(t *testing.T) {
	a := newTestApp(t)
	_, worker := hostBusinessFixture(t, a)
	var requests atomic.Int32
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Cookie") != "OWNED_COOKIE_SECRET" {
			t.Error("refresh lost host credential")
		}
		if fail.Load() {
			http.Error(w, "owned unavailable", 503)
			return
		}
		fmt.Fprint(w, `<a href="logout.php">退出</a><a href="userdetails.php?id=1">owned</a><div>上传：1 GB 下载：0 B 总做种数：0 总做种体积：0 B</div>`)
	}))
	defer server.Close()
	config := a.Settings()
	no := false
	config.ReadSiteMessages = &no
	site := SiteConfig{Config: sites.Config{ID: "owned-refresh", Name: "Owned refresh", Kind: "jiayuan", BaseURL: server.URL, Cookie: "OWNED_COOKIE_SECRET"}, Enabled: true}
	config.Sites = []SiteConfig{site}
	ownedDownloaderSave(t, a, config)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_host_refresh_rollback BEFORE INSERT ON domain_events BEGIN SELECT RAISE(ABORT,'owned failure'); END`); err != nil {
		t.Fatal(err)
	}
	var result plugins.SiteAccountHostResultV1
	if err := hostBusinessCall(t, worker, plugins.SitesRefreshCapability, plugins.SiteHostRefreshV1{ID: site.ID}, &result); err == nil {
		t.Fatal("refresh SQL failure swallowed")
	}
	var count int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind IN ('site_connection','site_stats_history','site_stats_latest')`).Scan(&count); err != nil || count != 0 {
		t.Fatal("refresh partially committed", count, err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_host_refresh_rollback`); err != nil {
		t.Fatal(err)
	}
	if err := hostBusinessCall(t, worker, plugins.SitesRefreshCapability, plugins.SiteHostRefreshV1{ID: site.ID}, &result); err != nil || len(result.Items) != 1 {
		t.Fatal("source refresh", err, result)
	}
	var fields struct {
		Upload *int64  `json:"upload"`
		Error  *string `json:"err_msg"`
	}
	if err := json.Unmarshal(result.Items[0].Fields, &fields); err != nil || fields.Upload == nil || *fields.Upload != 1<<30 {
		t.Fatal("source account bytes not observed", err, string(result.Items[0].Fields))
	}
	fail.Store(true)
	if err := hostBusinessCall(t, worker, plugins.SitesRefreshCapability, plugins.SiteHostRefreshV1{ID: site.ID}, &result); err != nil || len(result.Items) != 1 {
		t.Fatal("source error DTO refresh must keep existing daily success", err, result)
	}
	if err := json.Unmarshal(result.Items[0].Fields, &fields); err != nil || fields.Upload == nil || *fields.Upload != 1<<30 {
		t.Fatal("failed refresh replaced daily good value", err)
	}
	var connection siteSnapshot
	if err := a.Store.Get(context.Background(), "site_connection", site.ID, &connection); err != nil || connection.Connection.Status != "failed" {
		t.Fatal("failed connection hidden", err, connection.Connection)
	}
	if requests.Load() < 3 {
		t.Fatal("refresh did not use actual owned protocol")
	}
}

func TestOriginalHostBusinessSitesActualSDKListAndPriorityAreDistinct(t *testing.T) {
	a := newTestApp(t)
	_, worker := hostBusinessFixture(t, a)
	config := a.Settings()
	config.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-created-first", Name: "First", Kind: "ubits", BaseURL: "http://127.0.0.1:9001", Cookie: "owned"}, SiteOptions: SiteOptions{Priority: 9}, Enabled: true}, {Config: sites.Config{ID: "owned-created-second", Name: "Second", Kind: "ubits", BaseURL: "http://127.0.0.1:9002", Cookie: "owned"}, SiteOptions: SiteOptions{Priority: 1}, Enabled: false}}
	ownedDownloaderSave(t, a, config)
	var list plugins.SiteHostResultV1
	if err := hostBusinessCall(t, worker, plugins.SitesReadCapability, plugins.SiteHostRequestV1{}, &list); err != nil || len(list.Items) != 2 || list.Items[0].ID != "owned-created-first" || list.Items[0].SourceID >= list.Items[1].SourceID {
		t.Fatal("SiteOper.list changed insertion/identity order", err, list)
	}
	if err := hostBusinessCall(t, worker, plugins.SitesReadCapability, plugins.SiteHostRequestV1{OrderByPriority: true}, &list); err != nil || len(list.Items) != 2 || list.Items[0].ID != "owned-created-second" {
		t.Fatal("SiteOper.list_order_by_pri not selected", err, list)
	}
	if err := hostBusinessCall(t, worker, plugins.SitesReadCapability, plugins.SiteHostRequestV1{ActiveOnly: true}, &list); err != nil || len(list.Items) != 1 || list.Items[0].ID != "owned-created-first" {
		t.Fatal("SiteOper.list_active changed predicate", err, list)
	}
}
