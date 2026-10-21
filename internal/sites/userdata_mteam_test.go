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
	"strings"
	"testing"
	"time"
)

type sourceMTeamFixture struct {
	Cases []struct {
		Name    string
		Read    bool `json:"read_messages"`
		Replies map[string][]struct {
			Status int
			Body   string
		}
		Requests []map[string]any
		Data     map[string]any
		Error    *string `json:"error_type"`
	}
}

type sourceAccountTransport func(*http.Request) (*http.Response, error)

func (f sourceAccountTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSourceMTeamInstalledFullParserWireOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugins/site-mteam-account-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture sourceMTeamFixture
	if err = json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) != 23 {
		t.Fatal("invalid full parser fixture", err)
	}
	for _, item := range fixture.Cases {
		t.Run(item.Name, func(t *testing.T) {
			positions := map[string]int{}
			requests := []map[string]any{}
			transport := sourceAccountTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Scheme != "https" || r.URL.Host != "api.m-team.cc" || r.Header.Get("Cookie") != "" {
					t.Error("source credential origin/header changed")
				}
				if r.Header.Get("User-Agent") == "synthetic-configured-UA" {
					t.Error("API-key requests forwarded the original unused cookie UA")
				}
				headers := map[string]any{}
				for _, k := range []string{"Content-Type", "Accept", "x-api-key"} {
					if v := r.Header.Get(k); v != "" {
						headers[k] = v
					}
				}
				kind := "none"
				var body any
				if r.Method == http.MethodPost {
					if r.Header.Get("Content-Type") == "application/json" {
						kind = "json"
						if json.NewDecoder(r.Body).Decode(&body) != nil {
							t.Error("invalid JSON request")
						}
					} else {
						kind = "form"
						if r.ParseForm() != nil {
							t.Error("invalid form")
						}
						body = r.PostForm
					}
				}
				deadline, ok := r.Context().Deadline()
				if !ok {
					t.Error("account request missing timeout")
				}
				requests = append(requests, map[string]any{"method": r.Method, "url": r.URL.String(), "headers": headers, "body_kind": kind, "body": body, "timeout": int64(time.Until(deadline).Round(time.Second) / time.Second)})
				index := positions[r.URL.Path]
				positions[r.URL.Path] = index + 1
				replies := item.Replies[r.URL.Path]
				if index >= len(replies) {
					return nil, fmt.Errorf("owned unexpected request")
				}
				v := replies[index]
				return &http.Response{StatusCode: v.Status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(v.Body)), Request: r}, nil
			})
			base, _ := url.Parse("https://api.m-team.cc")
			c := &Client{config: Config{ID: "owned", Kind: "mteam", APIKey: "synthetic-owned-api-key", UserAgent: "synthetic-configured-UA", Timeout: 1}, base: base, http: &http.Client{Transport: transport, Timeout: time.Second}}
			s, data, e := c.SourceUserStats(context.Background(), UserStatsOptions{ReadMessages: item.Read})
			actualRaw, _ := json.Marshal(requests)
			var actualRequests []map[string]any
			json.Unmarshal(actualRaw, &actualRequests)
			if !reflect.DeepEqual(actualRequests, item.Requests) {
				want, _ := json.Marshal(item.Requests)
				t.Fatalf("request flow differs\nactual %s\nexpected %s", actualRaw, want)
			}
			if (data == nil) != (item.Data == nil) {
				t.Fatal("source DTO/exception differs", e, item.Error)
			}
			if data != nil {
				if item.Name == "large-integer-ID-preserved" {
					if id, ok := data.UserID.(json.Number); !ok || id.String() != "1208925819614629174706176" {
						t.Fatal("source integer ID rounded")
					}
				}
				data.Domain = "m-team.cc"
				data.Day = item.Data["updated_day"].(string)
				raw, err := json.Marshal(data)
				if err != nil {
					t.Fatal(err)
				}
				var actual map[string]any
				json.Unmarshal(raw, &actual)
				if !reflect.DeepEqual(actual, item.Data) {
					t.Fatalf("DTO differs\nactual %s\nexpected %v", raw, item.Data)
				}
			}
			if (strings.Contains(item.Name, "invalid-JSON") || item.Name == "invalid-peer-is-caught") && s.SeedingInfo != nil {
				t.Fatal("source partial/default count became complete native observation")
			}
			if s.MessageUnread != nil || s.Leeching != nil || s.LeechingBytes != nil {
				t.Fatal("source constructor values or extra peer fields became observations")
			}
		})
	}
}

func TestSourceMTeamPaginationLimitAndCancellationDoNotPublishDTO(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			marks := 0
			transport := sourceAccountTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				body := ""
				switch r.URL.Path {
				case "/api/member/profile":
					body = `{"code":"0","data":{"id":7,"username":"owned"}}`
				case "/api/msg/search":
					cancel()
					body = `{"code":"0","data":{"data":[{"id":1,"unread":true,"title":"owned","createdDate":"date","context":"body"}]}}`
				case "/api/msg/markRead":
					marks++
					t.Error("cancelled reader marked a message")
				case "/api/member/getUserTorrentList":
					body = `{"code":"0","data":{"data":[]}}`
				case "/api/tracker/myPeerStatus":
					body = `{"code":"0","data":{"seeder":1}}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			base, _ := url.Parse("https://api.m-team.cc")
			c := &Client{config: Config{ID: "owned", Kind: "mteam", APIKey: "synthetic-owned-api-key"}, base: base, http: &http.Client{Transport: transport}}
			_, d, e := c.SourceUserStats(ctx, UserStatsOptions{ReadMessages: cancelled})
			if d != nil || e == nil || marks != 0 {
				t.Fatal("incomplete reader published source data", e)
			}
			if cancelled {
				if e != context.Canceled || calls != 2 {
					t.Fatal("cancelled source reader continued requests", e, calls)
				}
			} else if e.Error() != "site_account_pagination_limit" || calls != 101 {
				t.Fatal("unbounded/partial source pagination", e, calls)
			}
		})
	}
}
