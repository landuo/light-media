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

func TestSourceRousiInstalledFullParserWireOracle(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/plugins/site-rousi-account-original-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Cases []struct {
			Name, Token, APIKey string
			Read                bool `json:"read_messages"`
			Replies             map[string][]struct {
				Status int
				Body   string
			}
			Requests []map[string]any
			Data     map[string]any
		}
	}
	if json.Unmarshal(raw, &f) != nil || len(f.Cases) != 31 {
		t.Fatal("invalid source fixture")
	}
	for _, item := range f.Cases {
		t.Run(item.Name, func(t *testing.T) {
			positions := map[string]int{}
			requests := []map[string]any{}
			transport := sourceAccountTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "owned.example" || r.URL.Scheme != "https" {
					return nil, fmt.Errorf("unexpected owned origin")
				}
				key := r.URL.Path
				if q := r.URL.Query().Encode(); q != "" {
					key += "?" + q
				}
				n := positions[key]
				positions[key]++
				replies := item.Replies[key]
				if n >= len(replies) {
					t.Errorf("unexpected call %s", key)
					return nil, fmt.Errorf("owned reply exhausted")
				}
				headers := map[string]any{}
				for _, key := range []string{"Authorization", "Accept", "Content-Type", "Cookie"} {
					if v := r.Header.Get(key); v != "" {
						headers[key] = v
					}
				}
				if r.URL.Path == "/api/messages" || r.URL.Path == "/api/messages/read-all" {
					headers["User-Agent"] = r.Header.Get("User-Agent")
				}
				params := map[string]any{}
				requestURL := "https://owned.example" + r.URL.Path
				if r.URL.Path == "/api/v1/profile" {
					requestURL += "?include_fields[user]=seeding_leeching_data"
				} else {
					for k, vs := range r.URL.Query() {
						params[k] = vs[0]
					}
				}
				requests = append(requests, map[string]any{"method": r.Method, "url": requestURL, "params": params, "headers": headers, "timeout": float64(60)})
				response := replies[n]
				return &http.Response{StatusCode: response.Status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(response.Body)), Request: r}, nil
			})
			base, _ := url.Parse("https://owned.example/")
			c := &Client{config: Config{ID: "owned", Kind: "rousi", APIKey: item.APIKey, MessageToken: item.Token, Cookie: "must-not-send=1", UserAgent: "synthetic-UA"}, base: base, http: &http.Client{Transport: transport}}
			ctx := context.WithValue(context.Background(), sourceUserStatsKey{}, &sourceUserStatsTrace{})
			_, d, e := c.sourceRousiUserStats(ctx, UserStatsOptions{ReadMessages: item.Read}, time.Date(2026, 10, 21, 12, 34, 56, 0, time.FixedZone("CST", 8*3600)))
			if !reflect.DeepEqual(requests, item.Requests) {
				a, _ := json.Marshal(requests)
				b, _ := json.Marshal(item.Requests)
				t.Fatalf("wire mismatch\n%s\n%s", a, b)
			}
			if item.Data == nil {
				if d != nil || e == nil {
					t.Fatalf("expected original module failure: %v", e)
				}
				return
			}
			if d == nil {
				t.Fatalf("source DTO missing: %v", e)
			}
			b, _ := json.Marshal(d)
			var got map[string]any
			json.Unmarshal(b, &got)
			delete(got, "domain")
			delete(got, "updated_day")
			delete(got, "updated_time")
			delete(item.Data, "domain")
			delete(item.Data, "updated_day")
			delete(item.Data, "updated_time")
			if !reflect.DeepEqual(got, item.Data) {
				a, _ := json.Marshal(got)
				b, _ := json.Marshal(item.Data)
				t.Fatalf("DTO mismatch\n%s\n%s", a, b)
			}
		})
	}
}
