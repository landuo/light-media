package sites

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// The original mailbox set has two allowed group orders. Preserve every
// request and duplicate while canonicalizing only those unspecified orders.
func normalizeNexusRequests(rows []map[string]any) {
	category := func(r map[string]any) string {
		u, _ := url.Parse(r["url"].(string))
		switch u.Query().Get("action") {
		case "viewmailbox":
			return "box"
		case "viewmessage":
			return "content"
		}
		return "other"
	}
	for start := 0; start < len(rows); {
		kind := category(rows[start])
		end := start + 1
		for end < len(rows) && category(rows[end]) == kind {
			end++
		}
		if kind != "other" {
			sort.SliceStable(rows[start:end], func(i, j int) bool {
				a, b := rows[start+i]["url"].(string), rows[start+j]["url"].(string)
				if kind == "box" {
					x, _ := url.Parse(a)
					y, _ := url.Parse(b)
					return x.Query().Get("box") < y.Query().Get("box")
				}
				return a < b
			})
		}
		start = end
	}
}

func TestSourceNexusGBKAndControlBoundaries(t *testing.T) {
	for _, mode := range []string{"gbk", "cross-origin", "cancel", "pagination-limit"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			home := `<a href="logout.php">退出</a><a href="userdetails.php?id=7"><b>影视用户</b></a><div>上传：1 GB 下载：2 GB</div>`
			transport := sourceAccountTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "owned.example" {
					t.Error("cookie crossed origin")
				}
				body := ""
				contentType := "text/html; charset=utf-8"
				switch r.URL.Path {
				case "/":
					body = home
					if mode == "gbk" {
						encoded, e := simplifiedchinese.GBK.NewEncoder().String(body)
						if e != nil {
							t.Fatal(e)
						}
						body = encoded
						contentType = "text/html; charset=gbk"
					}
				case "/userdetails.php":
					if mode == "cross-origin" {
						body = `<a href="https://unrelated.invalid/getusertorrentlist.php?type=seeding">bad</a>`
					}
					if mode == "cancel" {
						cancel()
					}
				default:
					if mode == "pagination-limit" {
						body = `<a href="?page=1">下一页</a>`
					}
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			base, _ := url.Parse("https://owned.example/")
			c := &Client{config: Config{ID: "owned", Kind: "jiayuan", Cookie: "synthetic-cookie=1"}, base: base, http: &http.Client{Transport: transport}}
			s, d, e := c.SourceUserStats(ctx, UserStatsOptions{})
			if mode == "gbk" {
				if e != nil || d == nil || d.Username == nil || *d.Username != "影视用户" || d.Upload != 1073741824 || s.Uploaded == nil {
					t.Fatal("source legacy encoding not decoded", e, d)
				}
				return
			}
			if d != nil || e == nil {
				t.Fatal("control failure published a source DTO", e)
			}
			switch mode {
			case "cross-origin":
				if e.Error() != "site_credential_origin_mismatch" || calls != 2 {
					t.Fatal(e, calls)
				}
			case "cancel":
				if e != context.Canceled || calls != 2 {
					t.Fatal(e, calls)
				}
			case "pagination-limit":
				if e.Error() != "site_account_pagination_limit" || calls != 52 {
					t.Fatal(e, calls)
				}
			}
		})
	}
}

func normalizeNexusMessages(data map[string]any) {
	if data == nil {
		return
	}
	rows, ok := data["message_unread_contents"].([]any)
	if !ok {
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, _ := json.Marshal(rows[i])
		b, _ := json.Marshal(rows[j])
		return string(a) < string(b)
	})
}

func TestSourceNexusInstalledFullParserWireOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugins/site-nexus-account-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name, Kind string
			Read       bool `json:"read_messages"`
			Replies    map[string][]struct {
				Status int
				Body   string
			}
			Requests []map[string]any
			Data     map[string]any
		}
	}
	if json.Unmarshal(raw, &fixture) != nil || len(fixture.Cases) != 27 {
		t.Fatal("invalid installed Nexus fixture")
	}
	for _, item := range fixture.Cases {
		t.Run(item.Name, func(t *testing.T) {
			positions := map[string]int{}
			requests := []map[string]any{}
			transport := sourceAccountTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Scheme != "https" || r.URL.Host != "owned.example" {
					return nil, fmt.Errorf("owned unexpected origin")
				}
				headers := map[string]any{}
				for _, key := range []string{"Cookie", "User-Agent", "Content-Type"} {
					if value := r.Header.Get(key); value != "" {
						headers[key] = value
					}
				}
				kind := "none"
				var body any
				if r.Method == http.MethodPost {
					kind = "form"
					if r.ParseForm() != nil {
						t.Error("invalid form")
					}
					body = r.PostForm
				}
				deadline, exists := r.Context().Deadline()
				if !exists {
					t.Error("source timeout missing")
				}
				requests = append(requests, map[string]any{"method": r.Method, "url": r.URL.String(), "headers": headers, "body_kind": kind, "body": body, "timeout": int64(time.Until(deadline).Round(time.Second) / time.Second)})
				key := r.URL.RequestURI()
				index := positions[key]
				positions[key] = index + 1
				replies := item.Replies[key]
				if index >= len(replies) {
					return nil, fmt.Errorf("owned unexpected path %s", key)
				}
				reply := replies[index]
				return &http.Response{StatusCode: reply.Status, Header: http.Header{"Content-Type": {"text/html; charset=utf-8"}}, Body: io.NopCloser(strings.NewReader(reply.Body)), Request: r}, nil
			})
			base, _ := url.Parse("https://owned.example/")
			kind := "jiayuan"
			if item.Kind == "NexusHhanclub" {
				kind = "hhanclub"
			}
			c := &Client{config: Config{ID: "owned", Kind: kind, Cookie: "synthetic-cookie=1", UserAgent: "synthetic-cookie-UA"}, base: base, http: &http.Client{Transport: transport}}
			ctx := context.WithValue(context.Background(), sourceUserStatsKey{}, &sourceUserStatsTrace{})
			s, data, e := c.sourceNexusUserStats(ctx, UserStatsOptions{ReadMessages: item.Read}, time.Date(2026, 10, 21, 12, 34, 56, 0, time.FixedZone("CST", 8*3600)))
			encoded, _ := json.Marshal(requests)
			var actualRequests []map[string]any
			json.Unmarshal(encoded, &actualRequests)
			normalizeNexusRequests(actualRequests)
			normalizeNexusRequests(item.Requests)
			if !reflect.DeepEqual(actualRequests, item.Requests) {
				want, _ := json.Marshal(item.Requests)
				t.Fatalf("source request flow differs\nactual %s\nexpected %s", encoded, want)
			}
			if (data == nil) != (item.Data == nil) {
				t.Fatal("source module DTO/exception differs", e)
			}
			if data != nil {
				data.Domain = "owned.example"
				data.Day = item.Data["updated_day"].(string)
				encoded, err = json.Marshal(data)
				if err != nil {
					t.Fatal(err)
				}
				var actual map[string]any
				json.Unmarshal(encoded, &actual)
				normalizeNexusMessages(actual)
				normalizeNexusMessages(item.Data)
				if !reflect.DeepEqual(actual, item.Data) {
					t.Fatalf("source DTO differs\nactual %s\nexpected %v", encoded, item.Data)
				}
			}
			if item.Name == "all-native-values-unknown" && (s.Uploaded != nil || s.Downloaded != nil || s.Seeding != nil || s.SeedingInfo != nil || s.Bonus != nil) {
				t.Fatal("source constructor defaults became native observations")
			}
			if item.Name == "invalid-size-and-seeders-default" && (s.SeedingBytes != nil || s.SeedingInfo != nil) {
				t.Fatal("unparseable source cells became observed zero")
			}
		})
	}
}
