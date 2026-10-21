package sites

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/net/html"
)

func TestSiteStatsInstalledOriginalMessageOracle(t *testing.T) {
	raw, e := os.ReadFile("testdata/stats-message-original-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		SHA    map[string]string `json:"source_sha256"`
		Counts []struct {
			Name, HTML string
			Observed   *int64
		}
		Contents []struct {
			Name, HTML string
			Tuple      [3]*string
		}
		Links []struct {
			Name, HTML string
			Links      []string
			Next       string `json:"next_page"`
		}
	}
	if json.Unmarshal(raw, &fixture) != nil || fixture.SHA["nexus_php.py"] != "b5c47b4ef97683057d0640f0bc8f4ad3e5354a29728f3acc12a4ed49b20382a4" || fixture.SHA["mtorrent.py"] != "a8fb7d1ea9bc7c588de398a205d1fba7eea17a50e19388c9bf44ffff6cc358ca" {
		t.Fatal("original parser evidence changed")
	}
	for _, c := range fixture.Counts {
		t.Run("count/"+c.Name, func(t *testing.T) {
			doc, _ := html.Parse(strings.NewReader(c.HTML))
			s := UserStats{Sources: map[string]string{}}
			parseNexusUnread(&s, doc)
			if !reflect.DeepEqual(s.MessageUnread, c.Observed) {
				t.Fatal("original count assignment differs")
			}
		})
	}
	for _, c := range fixture.Contents {
		t.Run("content/"+c.Name, func(t *testing.T) {
			doc, _ := html.Parse(strings.NewReader(c.HTML))
			if !reflect.DeepEqual(nexusMessageContent(doc), c.Tuple) {
				t.Fatal("original nullable tuple/text selection differs")
			}
		})
	}
	for _, c := range fixture.Links {
		t.Run("links/"+c.Name, func(t *testing.T) {
			doc, _ := html.Parse(strings.NewReader(c.HTML))
			links, next := nexusUnreadLinks(doc)
			if !reflect.DeepEqual(links, c.Links) || next != c.Next {
				t.Fatal("original unread/read filtering or next page differs")
			}
		})
	}
}

func TestMTeamExtendedAccountFieldsAndOriginalMessageForms(t *testing.T) {
	var marks []string
	var searches, pages int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("x-api-key") != "owned-api-key" {
			t.Error("incorrect authentication or method")
		}
		switch r.URL.Path {
		case "/api/member/profile":
			fmt.Fprint(w, `{"code":"0","data":{"id":"7","username":"owned","memberCount":{"uploaded":"0","downloaded":"0"}}}`)
		case "/api/tracker/myPeerStatus":
			fmt.Fprint(w, `{"code":"0","data":{"seeder":"2","leecher":"0"}}`)
		case "/api/member/getUserTorrentList":
			pages++
			if pages%2 == 1 {
				fmt.Fprint(w, `{"code":"0","data":{"total":"2","data":[{"torrent":{"id":"1","source":"3","size":"1000"}}]}}`)
			} else {
				fmt.Fprint(w, `{"code":"0","data":{"total":"2","data":[{"torrent":{"id":"2","source":"0","size":"2000"}}]}}`)
			}
		case "/api/msg/search":
			searches++
			r.ParseForm()
			if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.Form.Get("box") != "-2" || r.Form.Get("type") != "pageNumber" || r.Form.Get("pageSize") != "100" {
				t.Error("message search changed original form protocol")
			}
			fmt.Fprint(w, `{"code":"0","data":{"data":[{"id":"1","unread":true,"title":"标题","createdDate":"2026-10-21","context":"正文"},{"id":"2","unread":false,"title":"read","createdDate":"date","context":"body"},{"id":"3","unread":true,"title":"incomplete"}]}}`)
		case "/api/msg/markRead":
			r.ParseForm()
			marks = append(marks, r.Form.Get("msgId"))
			fmt.Fprint(w, `{"code":"0","data":null}`)
		default:
			t.Error("unexpected endpoint", r.URL.Path)
		}
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	c := &Client{config: Config{ID: "owned", Kind: "mteam", APIKey: "owned-api-key"}, base: base, http: server.Client()}
	for _, read := range []bool{false, true} {
		s, err := c.UserStatsWithOptions(context.Background(), UserStatsOptions{ReadMessages: read})
		if err != nil || !reflect.DeepEqual(s.SeedingInfo, [][2]int64{{3, 1000}, {0, 2000}}) || s.LeechingBytes != nil || s.MessageUnread != nil || s.Error != nil {
			t.Fatal("extended observations incorrect", err)
		}
		if read {
			if len(s.Messages) != 1 || *s.Messages[0][0] != "标题" || *s.Messages[0][2] != "正文" || len(s.Warnings) != 0 {
				t.Fatal("original unread content or successful mark lost")
			}
		} else if s.Messages != nil {
			t.Fatal("disabled message reading fabricated an empty inbox")
		}
	}
	if searches != 1 || !reflect.DeepEqual(marks, []string{"1"}) {
		t.Fatal("read messages or incomplete records were marked", searches, marks)
	}
}

func TestNexusExtendedFieldsMessagesAndSameOrigin(t *testing.T) {
	var foreign atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreign.Add(1); http.Error(w, "unexpected", 500) }))
	defer other.Close()
	unsafe := false
	var read []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "owned-cookie" {
			t.Error("cookie missing")
		}
		nav := `<a href="logout.php">退出</a>`
		switch r.URL.Path {
		case "/index.php":
			fmt.Fprint(w, nav+`<a href="userdetails.php?id=7">owned</a><span><a href="messages.php">信息箱</a> (2)</span><div>上传：1 GB 下载：0 B 下载中体积：2 GiB 做种中：1</div>`)
		case "/userdetails.php":
			fmt.Fprint(w, nav)
		case "/getusertorrentlistajax.php":
			fmt.Fprint(w, `<table><tr><th>名称</th><th>大小</th><th>在做种</th></tr><tr><td><a href="details.php?id=1">owned</a></td><td>1 KiB</td><td><b><a>2</a></b></td></tr></table>`)
		case "/messages.php":
			switch r.URL.Query().Get("action") {
			case "viewmailbox":
				fmt.Fprint(w, nav)
				if r.URL.Query().Get("box") == "-2" {
					return
				}
				if unsafe {
					fmt.Fprintf(w, `<table><tr><td><a href="%s/messages.php?action=viewmessage&amp;id=3">bad</a></td></tr></table>`, other.URL)
					return
				}
				if r.URL.Query().Get("page") == "2" {
					fmt.Fprint(w, `<table><tr><td><a href="messages.php?action=viewmessage&amp;id=2">second</a></td></tr></table>`)
				} else {
					fmt.Fprint(w, `<table><tr><td><a href="messages.php?action=viewmessage&amp;id=1">first</a></td></tr><tr><td><img alt="Read"><a href="messages.php?action=viewmessage&amp;id=9">already read</a></td></tr></table><a href="messages.php?action=viewmailbox&amp;box=1&amp;unread=yes&amp;page=2">下一页</a>`)
				}
			case "viewmessage":
				id := r.URL.Query().Get("id")
				read = append(read, id)
				fmt.Fprintf(w, `%s<h1>标题%s</h1><table><tr><td class="colhead">头</td></tr><tr><td>date</td><td>2026-10-21</td></tr><tr><td>正文 <b>保留</b>\n空白</td></tr></table>`, nav, id)
			default:
				t.Error("unrecognized message action")
			}
		default:
			t.Error("unexpected endpoint", r.URL.Path)
		}
	}))
	defer server.Close()
	c, err := New(Config{ID: "owned", Kind: "jiayuan", BaseURL: server.URL, Cookie: "owned-cookie"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.UserStatsWithOptions(context.Background(), UserStatsOptions{ReadMessages: true})
	if err != nil || s.LeechingBytes != nil || s.MessageUnread == nil || *s.MessageUnread != 2 || !reflect.DeepEqual(s.SeedingInfo, [][2]int64{{2, 1024}}) || len(s.Messages) != 2 || *s.Messages[0][0] != "标题1" || *s.Messages[1][0] != "标题2" || !reflect.DeepEqual(read, []string{"1", "2"}) {
		t.Fatal("native fields or original message tuples missing", err)
	}
	unsafe = true
	read = nil
	s, err = c.UserStatsWithOptions(context.Background(), UserStatsOptions{ReadMessages: true})
	if err != nil || foreign.Load() != 0 || len(read) != 0 || len(s.Warnings) != 1 || s.Messages != nil {
		t.Fatal("foreign unread link received credentials or became complete", err)
	}
}

func TestMessagesDisabledUnknownZeroAndOwnerGuard(t *testing.T) {
	var messageCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.php" {
			fmt.Fprint(w, `<a href="logout.php">退出</a><a href="userdetails.php?id=7">owned</a><div>上传：0 B 下载：0 B 总做种数：0 总做种体积：0 B</div><span><a href="messages.php">信息箱</a> (0)</span>`)
		} else if r.URL.Path == "/userdetails.php" {
			fmt.Fprint(w, `<a href="logout.php">退出</a>`)
		} else {
			messageCalls.Add(1)
			t.Error("observed empty inbox triggered a request")
		}
	}))
	defer server.Close()
	c, _ := New(Config{ID: "owned", Kind: "ubits", BaseURL: server.URL, Cookie: "owned-cookie"})
	for _, read := range []bool{false, true} {
		s, e := c.UserStatsWithOptions(context.Background(), UserStatsOptions{ReadMessages: read})
		if e != nil || s.MessageUnread == nil || *s.MessageUnread != 0 || (s.Messages != nil) != read || messageCalls.Load() != 0 {
			t.Fatal("unknown or disabled inbox became an observed empty one", e)
		}
	}
	var admitted atomic.Int32
	var allowed atomic.Bool
	allowed.Store(true)
	mt := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/member/profile":
			fmt.Fprint(w, `{"code":"0","data":{"id":"7","username":"owned"}}`)
		case "/api/tracker/myPeerStatus":
			fmt.Fprint(w, `{"code":"0","data":{"seeder":"0"}}`)
		case "/api/member/getUserTorrentList":
			fmt.Fprint(w, `{"code":"0","data":{"total":"0","data":[]}}`)
		case "/api/msg/search":
			allowed.Store(false)
			fmt.Fprint(w, `{"code":"0","data":{"data":[{"id":"1","unread":true,"title":"owned","createdDate":"date","context":"body"}]}}`)
		case "/api/msg/markRead":
			t.Error("revoked owner marked a message")
		default:
			t.Error("unexpected request")
		}
	}))
	defer mt.Close()
	base, _ := url.Parse(mt.URL)
	client := &Client{config: Config{ID: "owned", Kind: "mteam", APIKey: "owned"}, base: base, http: mt.Client()}
	client.SetRequestGuard(func(context.Context) error { admitted.Add(1); return nil })
	client.AddRequestGuard(func(context.Context) error {
		if !allowed.Load() {
			return errors.New("owned-revoked")
		}
		return nil
	})
	s, e := client.UserStatsWithOptions(context.Background(), UserStatsOptions{ReadMessages: true})
	if e != nil || admitted.Load() != 5 || len(s.Messages) != 1 || len(s.Warnings) != 1 || !strings.Contains(s.Warnings[0], "mark_read_failed") {
		t.Fatal("owner guard replaced quota admission or lost observed contents", e)
	}
}

func TestSeedingInfoNeverPublishesAnIncompleteList(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/member/profile":
			fmt.Fprint(w, `{"code":"0","data":{"id":"7","username":"owned"}}`)
		case "/api/tracker/myPeerStatus":
			fmt.Fprint(w, `{"code":"0","data":{"seeder":"2"}}`)
		case "/api/member/getUserTorrentList":
			fmt.Fprint(w, `{"code":"0","data":{"total":"2","data":[{"torrent":{"id":"1","source":"3","size":"1000"}},{"torrent":{"id":"2","size":"2000"}}]}}`)
		}
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	c := &Client{config: Config{ID: "owned", Kind: "mteam", APIKey: "owned"}, base: base, http: server.Client()}
	s, e := c.UserStats(context.Background())
	if e != nil || s.SeedingBytes == nil || *s.SeedingBytes != 3000 || s.SeedingInfo != nil {
		t.Fatal("missing torrent.source became an invented zero or partial seeding list", e)
	}
}

func TestNexusSeedingInfoFetchedEvenWhenProfileTotalsAreKnown(t *testing.T) {
	var details atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.php":
			fmt.Fprint(w, `<a href="logout.php">退出</a><a href="userdetails.php?id=7">owned</a><div>上传：0 B 下载：0 B 总做种数：1 总做种体积：1 KiB</div>`)
		case "/userdetails.php":
			fmt.Fprint(w, `<a href="logout.php">退出</a>`)
		case "/getusertorrentlistajax.php":
			details.Add(1)
			fmt.Fprint(w, `<table><tr><th>名称</th><th>大小</th><th>做种者</th></tr><tr><td><a href="details.php?id=1">owned</a></td><td>1 KiB</td><td>5</td></tr></table>`)
		default:
			t.Error("unexpected endpoint")
		}
	}))
	defer server.Close()
	c, _ := New(Config{ID: "owned", Kind: "jiayuan", BaseURL: server.URL, Cookie: "owned-cookie"})
	s, err := c.UserStats(context.Background())
	if err != nil || details.Load() != 1 || !reflect.DeepEqual(s.SeedingInfo, [][2]int64{{5, 1024}}) {
		t.Fatal("known totals suppressed original detail collection", err)
	}
}
