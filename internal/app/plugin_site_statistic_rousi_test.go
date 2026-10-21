package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"light-media/internal/plugins"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestSiteStatisticRousiOriginalRequestsActualSDKAndColdSnapshot(t *testing.T) {
	path := os.Getenv("LIGHT_MEDIA_OWNED_ROUSI_ORACLE")
	if path == "" {
		path = "../../testdata/plugins/site-rousi-account-original-oracle.json"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
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
	if json.Unmarshal(raw, &fixture) != nil || len(fixture.Cases) != 31 {
		t.Fatal("invalid installed Rousi fixture")
	}
	for _, item := range fixture.Cases {
		t.Run(item.Name, func(t *testing.T) {
			a, _, _, _ := pluginHTTPFixture(t)
			ctx := context.Background()
			positions := map[string]int{}
			requests := []map[string]any{}
			previous := http.DefaultTransport
			http.DefaultTransport = siteOutcomeRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Scheme != "https" || r.URL.Host != "rousi.pro" {
					return nil, fmt.Errorf("owned unexpected source origin")
				}
				headers := map[string]any{}
				for _, key := range []string{"Authorization", "Accept", "Content-Type", "Cookie"} {
					if value := r.Header.Get(key); value != "" {
						headers[key] = value
					}
				}
				if strings.HasPrefix(r.URL.Path, "/api/messages") {
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
				requests = append(requests, map[string]any{"method": r.Method, "url": requestURL, "params": params, "headers": headers})
				key := r.URL.Path
				if q := r.URL.Query().Encode(); q != "" {
					key += "?" + q
				}
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
			c.Sites[0].BaseURL = "https://rousi.pro/"
			c.Sites[0].Cookie = "must-not-send=1"
			c.Sites[0].UserAgent = "synthetic-UA"
			c.Sites[0].Kind = "rousi"
			c.Sites[0].APIKey = item.APIKey
			c.Sites[0].Token = item.Token
			c.ReadSiteMessages = &item.Read
			if err := a.saveSettings(ctx, c); err != nil {
				t.Fatal(err)
			}
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
			if item.Name == "paginated-duplicates-and-null-fields" || item.Name == "zero-messages-still-read-all" {
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
				if item.Name == "paginated-duplicates-and-null-fields" {
					if len(snap.SourceData.Messages) != 3 || snap.SourceData.Messages[1][0] != nil || snap.SourceData.MessageUnread != 3 {
						t.Fatal("source duplicates/nulls lost after encrypted recovery")
					}
				} else if snap.SourceData.MessageUnread != 0 || len(snap.SourceData.Messages) != 0 {
					t.Fatal("zero unread source snapshot changed")
				}

			}
		})
	}
}
