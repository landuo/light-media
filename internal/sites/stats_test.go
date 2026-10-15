package sites

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestUserStatsNexusDoesNotConfuseZeroUnknownOrOtherUsers(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Cookie") != "test-secret-cookie" {
			t.Error("missing authentication")
		}
		switch r.URL.Path {
		case "/index.php":
			fmt.Fprint(w, `<a href="logout.php">退出</a><a href="https://unrelated.invalid/userdetails.php?id=999">不能提交认证</a><a href="userdetails.php?id=17">本人</a><div>上传量：1.50 TB 下载量：0 B 分享率：∞</div><a href="mybonus.php">魔力值 12,345.50</a>`)
		case "/userdetails.php":
			if r.URL.Query().Get("id") != "17" {
				t.Error("wrong user")
			}
			fmt.Fprint(w, `<a href="logout.php">退出</a><table><tr><td>等级</td><td><img title="Elite User"></td></tr><tr><td>加入日期</td><td>2023-01-02</td></tr></table>`)
		case "/getusertorrentlistajax.php":
			fmt.Fprint(w, `<table><tr><th>名称</th><th>大小</th></tr><tr><td><a href="details.php?id=9">synthetic</a></td><td>1.5 GiB</td></tr></table>`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c, e := New(Config{ID: "test", Kind: "jiayuan", BaseURL: server.URL, Cookie: "test-secret-cookie"})
	if e != nil {
		t.Fatal(e)
	}
	s, e := c.UserStats(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if s.UserID != "17" || s.Username != "本人" || s.Level != "Elite User" || s.JoinedAt != "2023-01-02" {
		t.Fatalf("identity %+v", s)
	}
	if s.Uploaded == nil || *s.Uploaded != 1649267441664 || s.Downloaded == nil || *s.Downloaded != 0 || !s.RatioInfinite || s.Ratio != nil {
		t.Fatalf("traffic %+v", s)
	}
	if s.Bonus == nil || *s.Bonus != 12345.5 || s.Seeding == nil || *s.Seeding != 1 || s.SeedingBytes == nil || *s.SeedingBytes != 1610612736 || s.Leeching != nil {
		t.Fatalf("observed/unknown %+v", s)
	}
	if len(paths) != 3 || s.Sources["seeding_bytes"] == "" {
		t.Fatal(paths, s.Sources)
	}
}

func TestUserStatsPartialSeedingAndLoginNeverBecomeZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.php":
			fmt.Fprint(w, `<a href="logout.php">退出</a><a href="userdetails.php?id=5">本人</a><div>上传：1 GB 下载：2 GB 做种中：200</div>`)
		case "/userdetails.php":
			http.Error(w, "upstream failure", 503)
		case "/getusertorrentlistajax.php":
			fmt.Fprint(w, `<form><input type="password"></form>`)
		default:
			t.Fatal(r.URL.Path)
		}
	}))
	defer server.Close()
	c, _ := New(Config{ID: "test", Kind: "ubits", BaseURL: server.URL, Cookie: "fake-cookie"})
	s, e := c.UserStats(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if s.SeedingBytes != nil || s.Seeding == nil || *s.Seeding != 200 || len(s.Warnings) != 2 || s.Ratio == nil || *s.Ratio != 0.5 {
		t.Fatalf("partial was falsely complete %+v", s)
	}
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `<input type="password">`) })
	if _, e = c.UserStats(context.Background()); e == nil || e.Error() != "site_authentication_failed" {
		t.Fatal("login accepted", e)
	}
}

func TestUserStatsMTeamUsesNativeFieldsAndAllSeedingPages(t *testing.T) {
	pages := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("x-api-key") != "test-key" {
			t.Error("wrong mteam request")
		}
		switch r.URL.Path {
		case "/api/member/profile":
			fmt.Fprint(w, `{"code":"0","data":{"id":"7","username":"tester","role":"3","memberStatus":{"createdDate":"2021-01-01"},"memberCount":{"uploaded":"9000000","downloaded":"0","bonus":"0","shareRate":"∞"}}}`)
		case "/api/tracker/myPeerStatus":
			fmt.Fprint(w, `{"code":"0","data":{"seeder":"2","leecher":"0"}}`)
		case "/api/member/getUserTorrentList":
			pages++
			if pages == 1 {
				fmt.Fprint(w, `{"code":"0","data":{"total":"2","data":[{"torrent":{"id":"13","size":"1000"}}]}}`)
			} else {
				fmt.Fprint(w, `{"code":"0","data":{"total":"2","data":[{"torrent":{"id":"14","size":"2000"}}]}}`)
			}
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	c := &Client{config: Config{ID: "mteam", Kind: "mteam", APIKey: "test-key"}, base: base, http: server.Client()}
	s, e := c.UserStats(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if pages != 2 || s.SeedingBytes == nil || *s.SeedingBytes != 3000 || s.Bonus == nil || *s.Bonus != 0 || s.Leeching == nil || *s.Leeching != 0 || !s.RatioInfinite || s.Level != "Elite User" {
		t.Fatalf("native stats %+v pages=%d", s, pages)
	}
}

func TestStatsHTMLIgnoresTotalsAndNestedTableLabels(t *testing.T) {
	doc, _ := html.Parse(strings.NewReader(`<div>总上传：999 TB 上传：2.5 GB 总下载：888 TB 下载：1.25 GB 分享率：2.0</div>`))
	s := UserStats{Sources: map[string]string{}}
	parseUserHTML(&s, doc, "fixture")
	if s.Uploaded == nil || *s.Uploaded != 2684354560 || s.Downloaded == nil || *s.Downloaded != 1342177280 || s.Ratio == nil || *s.Ratio != 2 {
		t.Fatalf("wrong account totals %+v", s)
	}
}

func TestUserStatsNexusCSRFEndpointAndIconTraffic(t *testing.T) {
	posted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.php":
			fmt.Fprint(w, `<a href="logout.php">退出</a><a href="userdetails.php?id=8">test</a><img alt="上传量">1 GB<img alt="下载量">2 GB`)
		case "/userdetails.php":
			fmt.Fprint(w, `<a href="logout.php">退出</a><meta name="x-csrf" content="test-csrf-not-live"><a href="javascript: getusertorrentlistajax('8','seeding')">做种</a>`)
		case "/ajax_getusertorrentlist.php":
			if r.Method != "POST" {
				t.Error("CSRF endpoint requires form POST")
			}
			_ = r.ParseForm()
			if r.Form.Get("csrf") != "test-csrf-not-live" || r.Form.Get("userid") != "8" || r.Form.Get("type") != "seeding" {
				t.Error("wrong CSRF parameters")
			}
			posted = true
			fmt.Fprint(w, `<table><tr><td>名称</td><td>大小</td></tr><tr><td><a href="details.php?id=5">own</a></td><td>2 GB</td></tr></table>`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c, _ := New(Config{ID: "csrf-site", Kind: "1ptba", BaseURL: server.URL, Cookie: "fake-cookie"})
	s, e := c.UserStats(context.Background())
	if e != nil || !posted || s.SeedingBytes == nil || *s.SeedingBytes != 2147483648 || s.Uploaded == nil || *s.Uploaded != 1073741824 || s.Ratio == nil || *s.Ratio != 0.5 {
		t.Fatalf("CSRF/icons not recognized %+v err=%v posted=%v", s, e, posted)
	}
}

func TestStatsHHanNativePanelAndNexusCoins(t *testing.T) {
	doc, _ := html.Parse(strings.NewReader(`<div id="user-info-panel"><div></div><div><div></div><div><div></div><div></div><div></div><div>1.5 TiB</div><div>256 GiB</div></div></div></div><span class="ucoin-symbol ucoin-gold">1</span><span class="ucoin-symbol ucoin-silver">2</span><span class="ucoin-symbol ucoin-copper">0</span>`))
	s := UserStats{Sources: map[string]string{}}
	parseUserHTML(&s, doc, "index.php")
	parseHhanPanel(&s, doc, "index.php")
	if s.Uploaded == nil || *s.Uploaded != 1649267441664 || s.Downloaded == nil || *s.Downloaded != 274877906944 || s.Ratio == nil || *s.Ratio != 6 || s.Bonus == nil || *s.Bonus != 10200 {
		t.Fatalf("native panel/coins %+v", s)
	}
}
