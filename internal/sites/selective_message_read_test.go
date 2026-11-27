package sites

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestSelectivelyReadNexusMessages(t *testing.T) {
	var listed, opened []string
	var unsafe bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/pt/messages.php" || r.Header.Get("Cookie") != "test-cookie" {
			t.Error("unexpected mailbox request")
			return
		}
		fmt.Fprint(w, `<a href="#" data-url="logout.php">退出</a>`)
		query := r.URL.Query()
		switch query.Get("action") {
		case "viewmailbox":
			if query.Get("unread") != "yes" {
				t.Error("unread scope was lost")
			}
			listed = append(listed, query.Get("box")+":"+query.Get("page"))
			switch {
			case query.Get("box") == "-2" && unsafe:
				fmt.Fprint(w, `<table><tr><td><img alt="Unread"></td><td><a href="https://another.example/messages.php?action=viewmessage&amp;id=8">需清理 &amp; 通知</a></td></tr></table>`)
			case query.Get("box") == "-2":
				fmt.Fprint(w, `<div class="pm-list"><div class="pm-item-row is-unread"><a class="pm-item__subject" href="/pt/messages.php?action=viewmessage&amp;id=6">需清理 &amp; 通知</a></div></div>`)
			case query.Get("page") == "1":
				fmt.Fprint(w, `<table><tr><td><img title="未读"></td><td><a href="messages.php?action=viewmessage&amp;id=5">需清理 &amp; 通知</a></td></tr></table>`)
			default:
				fmt.Fprint(w, `<table>
				  <tr><td><img alt="Unread"></td><td><a href="messages.php?action=viewmessage&amp;id=1">需清理 &amp; 通知</a></td></tr>
				  <tr><td><img class="unreadpm"></td><td><a href="messages.php?action=viewmessage&amp;id=2">需清理 &amp; 通知：重要</a></td></tr>
				  <tr><td><img title="已读"></td><td><a href="messages.php?action=viewmessage&amp;id=3">需清理 &amp; 通知</a></td></tr>
				  <tr><td></td><td><a href="messages.php?action=viewmessage&amp;id=4">需清理 &amp; 通知</a></td></tr>
				</table><a href="?action=viewmailbox&amp;box=1&amp;page=1"><b>Next&nbsp;&gt;&gt;</b></a>`)
			}
		case "viewmessage":
			id := query.Get("id")
			if len(listed) != 3 || len(query) != 2 {
				t.Error("detail opened before an authenticated mailbox snapshot was collected")
			}
			opened = append(opened, id)
			switch id {
			case "1":
				fmt.Fprint(w, `<h1>需清理 &amp; 通知</h1><table><tr><td class="colhead">发件人</td></tr><tr><td>系统</td><td>2026-11-28</td></tr><tr><td>消息正文</td></tr></table>`)
			case "5":
				fmt.Fprint(w, `<h1>站内短讯暂不可用</h1>`)
			case "6":
				fmt.Fprint(w, `<h1 class="pm-hero__title">需清理 &amp; 通知</h1><article class="pm-view__body">消息正文</article>`)
			default:
				t.Error("unexpected message opened", id)
			}
		default:
			t.Error("unexpected mailbox action")
		}
	}))
	defer server.Close()
	c, err := New(Config{ID: "test", Kind: "jiayuan", BaseURL: server.URL + "/pt/", Cookie: "test-cookie"})
	if err != nil {
		t.Fatal(err)
	}
	match := func(title string) bool { return title == "需清理 & 通知" }
	got, err := c.MarkMatchingMessagesRead(context.Background(), match)
	want := MessageReadResult{Scanned: 4, Matched: 3, Marked: 2, Failed: 1}
	if !errors.Is(err, errMessageMarkUnconfirmed) || got != want || !reflect.DeepEqual(opened, []string{"1", "5", "6"}) {
		t.Fatalf("result %+v, opened %v, error %v", got, opened, err)
	}

	listed, opened, unsafe = nil, nil, true
	got, err = c.MarkMatchingMessagesRead(context.Background(), match)
	if !errors.Is(err, errMessageMailbox) || got != (MessageReadResult{}) || len(opened) != 0 {
		t.Fatalf("unsafe second mailbox should prevent all writes: %+v, %v, %v", got, opened, err)
	}
}

func TestSelectivelyReadMTeamMessages(t *testing.T) {
	var searchPages, marked []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("x-api-key") != "test-api-key" || r.ParseForm() != nil {
			t.Error("unexpected mail API request")
		}
		switch r.URL.Path {
		case "/api/msg/search":
			page := r.Form.Get("pageNumber")
			searchPages = append(searchPages, page)
			if r.Form.Get("box") != "-2" || r.Form.Get("unread") != "true" || r.Form.Get("pageSize") != "100" {
				t.Error("mail API escaped unread message scope")
			}
			switch page {
			case "1":
				fmt.Fprint(w, `{"code":0,"data":{"pageNumber":1,"pageSize":2,"total":6,"data":[{"id":11,"unread":true,"title":"需清理"},{"id":12,"unread":false,"title":"需清理"}]}}`)
			case "2":
				fmt.Fprint(w, `{"code":0,"data":{"pageNumber":2,"pageSize":2,"total":6,"data":[{"id":13,"unread":true,"title":"需清理"},{"id":14,"unread":true,"title":"需清理：重要"}]}}`)
			case "3":
				fmt.Fprint(w, `{"code":0,"data":{"pageNumber":3,"pageSize":2,"total":6,"data":[{"id":15,"unread":true,"title":"需清理"},{"id":16,"unread":true,"title":"重要"}]}}`)
			default:
				t.Error("unexpected API page", page)
			}
		case "/api/msg/markRead":
			id := r.Form.Get("msgId")
			if len(searchPages) != 3 || r.Form.Get("msgIds") != id || len(r.Form["msgIds"]) != 1 || len(r.Form["msgId"]) != 1 {
				t.Error("markRead happened before collecting all pages or did not address exactly one ID")
			}
			marked = append(marked, id)
			if id == "11" {
				fmt.Fprint(w, `{"code":"0","data":null}`)
			} else {
				fmt.Fprint(w, `{"code":"429","data":null}`)
			}
		default:
			t.Error("unexpected API endpoint", r.URL.Path)
		}
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{config: Config{ID: "test", Kind: "mteam", APIKey: "test-api-key"}, base: base, http: server.Client()}
	got, err := c.MarkMatchingMessagesRead(context.Background(), func(title string) bool { return title == "需清理" })
	want := MessageReadResult{Scanned: 5, Matched: 3, Marked: 1, Failed: 2}
	if err == nil || err.Error() != "site_rate_limited" || got != want || !reflect.DeepEqual(marked, []string{"11", "13"}) {
		t.Fatalf("result %+v, marked %v, error %v", got, marked, err)
	}
}

func TestSelectiveNexusRecognizesLocalizedEmptyMailboxes(t *testing.T) {
	for _, label := range []string{"没有短讯", "沒有短訊", "No Messages.", "无效的短讯箱", "無效的短訊箱", "Invalid Mailbox"} {
		t.Run(label, func(t *testing.T) {
			doc, err := nexusMessageDOM([]byte(`<form action="logout.php"></form><p align="center">`+label+`</p>`), "text/html; charset=utf-8")
			if err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse("https://pt.example/messages.php?action=viewmailbox&box=-2&unread=yes")
			rows, next, err := nexusMailboxRows(doc, u, "-2", 0)
			if err != nil || len(rows) != 0 || next != 0 {
				t.Fatalf("recognized empty mailbox: %+v, %d, %v", rows, next, err)
			}
		})
	}
	doc, err := nexusMessageDOM([]byte(`<a data-url="logout.php"></a><form><input type="password"></form>`), "")
	if doc != nil || err == nil {
		t.Fatal("a login form overrode authentication detection")
	}
}

func TestSelectiveNexusDecodesGBKTitle(t *testing.T) {
	raw, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(`<a data-url="logout.php"></a><table><tr><td><img alt="Unread"></td><td><a href="messages.php?action=viewmessage&amp;id=15">每日通知</a></td></tr></table>`))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := nexusMessageDOM(raw, "text/html; charset=gbk")
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse("https://pt.example/messages.php?action=viewmailbox&box=1&unread=yes")
	rows, _, err := nexusMailboxRows(doc, base, "1", 0)
	if err != nil || !reflect.DeepEqual(rows, []unreadMessage{{id: "15", title: "每日通知"}}) {
		t.Fatalf("GBK title not decoded: %+v, %v", rows, err)
	}
}

func TestSelectiveMessageReadRejectsUnsafeTargets(t *testing.T) {
	for _, target := range []string{
		"messages.php?action=viewmessage&id=15&id=16",
		"messages.php?action=viewmessage&action=moveordel&id=15",
		"messages.php?action=viewmessage&id=15&delete=1",
		"messages.php?action=viewmessage&id=9223372036854775808",
		"/different/messages.php?action=viewmessage&id=15",
	} {
		t.Run(target, func(t *testing.T) {
			doc, err := pageDOM([]byte(`<a href="logout.php">退出</a><table><tr><td><img alt="Unread"></td><td><a href="` +
				strings.ReplaceAll(target, "&", "&amp;") + `">需清理</a></td></tr></table>`))
			if err != nil {
				t.Fatal(err)
			}
			base, _ := url.Parse("https://pt.example/pt/messages.php?action=viewmailbox&box=1&unread=yes")
			if rows, _, err := nexusMailboxRows(doc, base, "1", 0); !errors.Is(err, errMessageMailbox) || len(rows) != 0 {
				t.Fatalf("unsafe target accepted: %+v, %v", rows, err)
			}
		})
	}
	client := &Client{config: Config{Kind: "rousi"}}
	if got, err := client.MarkMatchingMessagesRead(context.Background(), func(string) bool { return true }); !errors.Is(err, ErrMessageReadUnsupported) || got != (MessageReadResult{}) {
		t.Fatalf("expected unsupported without a read-all request, got %+v, %v", got, err)
	}
}
