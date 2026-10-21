package app

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

	"light-media/internal/plugins"
)

func normalizeStatisticNexusRequests(rows []map[string]any) {
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
func normalizeStatisticNexusMessages(data map[string]any) {
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

func TestSiteStatisticNexusOriginalRequestsActualSDKAndColdSnapshot(t *testing.T) {
	path := os.Getenv("LIGHT_MEDIA_OWNED_NEXUS_ORACLE")
	if path == "" {
		path = "../../testdata/plugins/site-nexus-account-original-oracle.json"
	}
	raw, err := os.ReadFile(path)
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
			a, _, _, _ := pluginHTTPFixture(t)
			ctx := context.Background()
			positions := map[string]int{}
			requests := []map[string]any{}
			previous := http.DefaultTransport
			http.DefaultTransport = siteOutcomeRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Scheme != "https" || r.URL.Host != "owned.example" {
					return nil, fmt.Errorf("owned unexpected source origin")
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
					r.ParseForm()
					body = r.PostForm
				}
				requests = append(requests, map[string]any{"method": r.Method, "url": r.URL.String(), "headers": headers, "body_kind": kind, "body": body})
				key := r.URL.RequestURI()
				i := positions[key]
				positions[key] = i + 1
				replies := item.Replies[key]
				if i >= len(replies) {
					return nil, fmt.Errorf("owned unexpected path")
				}
				reply := replies[i]
				return &http.Response{StatusCode: reply.Status, Header: http.Header{"Content-Type": {"text/html; charset=utf-8"}}, Body: io.NopCloser(strings.NewReader(reply.Body)), Request: r}, nil
			})
			defer func() { a.Close(); http.DefaultTransport = previous }()
			c := a.Settings()
			c.Sites[0].BaseURL = "https://owned.example/"
			c.Sites[0].Cookie = "synthetic-cookie=1"
			c.Sites[0].UserAgent = "synthetic-cookie-UA"
			c.Sites[0].Kind = "jiayuan"
			if item.Kind == "NexusHhanclub" {
				c.Sites[0].Kind = "hhanclub"
			}
			c.ReadSiteMessages = &item.Read
			if err := a.saveSettings(ctx, c); err != nil {
				t.Fatal(err)
			}
			before := time.Now().In(shanghai())
			run := enqueueSyntheticPluginHTTP(t, a)
			a.executePluginRun(ctx, run)
			finished, err := a.pluginStore.Run(ctx, run.ID)
			if err != nil || finished.State != "succeeded" {
				t.Fatal("source API not committed", finished.State, finished.ErrorCode, err)
			}
			encoded, _ := json.Marshal(requests)
			var actual []map[string]any
			json.Unmarshal(encoded, &actual)
			for _, r := range item.Requests {
				delete(r, "timeout")
			}
			normalizeStatisticNexusRequests(actual)
			normalizeStatisticNexusRequests(item.Requests)
			if !reflect.DeepEqual(actual, item.Requests) {
				t.Fatal("SDK source request flow differs", actual, item.Requests)
			}
			receipt, err := a.pluginStore.siteStatisticReceipt(ctx, run)
			if err != nil {
				t.Fatal(err)
			}
			var response plugins.HTTPResponse
			if json.Unmarshal(receipt, &response) != nil {
				t.Fatal("invalid SDK receipt")
			}
			var payload struct {
				Success bool
				Data    map[string]any
			}
			if json.Unmarshal(response.Body, &payload) != nil {
				t.Fatal("invalid source API body")
			}
			if payload.Success != (item.Data != nil) {
				t.Fatal("SDK changed source DTO/exception outcome")
			}
			if item.Data != nil {
				payload.Data["domain"] = "owned.example"
				payload.Data["updated_day"] = item.Data["updated_day"]
				if item.Name == "date-relative-clock" {
					at, e := time.ParseInLocation("2006-01-02 15:04:05", payload.Data["join_at"].(string), shanghai())
					if e != nil || at.Before(before.AddDate(0, 0, -2).Add(-time.Second)) || at.After(time.Now().In(shanghai()).AddDate(0, 0, -2).Add(time.Second)) {
						t.Fatal("source relative join date ignored host timezone/clock", e)
					}
					payload.Data["join_at"] = item.Data["join_at"]
				}
				normalizeStatisticNexusMessages(payload.Data)
				normalizeStatisticNexusMessages(item.Data)
				if !reflect.DeepEqual(payload.Data, item.Data) {
					t.Fatal("SDK DTO differs from source", payload.Data, item.Data)
				}
			}
			var facts int
			if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='site.refreshed'`).Scan(&facts); err != nil {
				t.Fatal(err)
			}
			want := 0
			if item.Data != nil {
				want = 1
			}
			if facts != want {
				t.Fatal("source event did not follow DTO outcome", facts, want)
			}
			if item.Name == "message-duplicates-and-null-content" || item.Name == "profile-summary-plus-page-count" {
				calls := len(requests)
				if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET state='running',result=NULL WHERE id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
				dir := a.Dir
				if err = a.Close(); err != nil {
					t.Fatal(err)
				}
				cold, err := New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				defer cold.Close()
				recovered, err := cold.pluginStore.Run(ctx, run.ID)
				if err != nil || recovered.State != "succeeded" || string(recovered.Result) != string(finished.Result) || len(requests) != calls {
					t.Fatal("confirmed source flow replayed after cold recovery", err)
				}
				var snap siteSnapshot
				if err = cold.Store.Get(ctx, "site_connection", "known", &snap); err != nil || snap.SourceData == nil {
					t.Fatal("encrypted source snapshot missing", err)
				}
				if item.Name == "message-duplicates-and-null-content" {
					if len(snap.SourceData.Messages) != 2 || snap.SourceData.Messages[0][1] != nil || snap.SourceData.Messages[0][2] != nil {
						t.Fatal("source duplicate/null messages lost")
					}
				} else if snap.SourceData.Seeding != 12 || snap.SourceData.SeedingSize != 1073744896 {
					t.Fatal("original cumulative fallback changed")
				}
			}
		})
	}
}
