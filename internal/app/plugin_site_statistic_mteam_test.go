package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

func TestSiteStatisticMTeamOriginalRequestsActualSDKAndColdSnapshot(t *testing.T) {
	oraclePath := os.Getenv("LIGHT_MEDIA_OWNED_MTEAM_ORACLE")
	if oraclePath == "" {
		oraclePath = "../../testdata/plugins/site-mteam-account-original-oracle.json"
	}
	raw, err := os.ReadFile(oraclePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name    string
			Read    bool `json:"read_messages"`
			Replies map[string][]struct {
				Status int
				Body   string
			}
			Requests []map[string]any
			Data     map[string]any
		}
	}
	if json.Unmarshal(raw, &fixture) != nil || len(fixture.Cases) != 23 {
		t.Fatal("invalid installed MTeam fixture")
	}
	for _, item := range fixture.Cases {
		t.Run(item.Name, func(t *testing.T) {
			a, _, _, _ := pluginHTTPFixture(t)
			ctx := context.Background()
			positions := map[string]int{}
			requests := []map[string]any{}
			previous := http.DefaultTransport
			http.DefaultTransport = siteOutcomeRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Scheme != "https" || r.URL.Host != "api.m-team.cc" || r.Header.Get("Cookie") != "" {
					return nil, fmt.Errorf("owned source origin denied")
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
						r.ParseForm()
						body = r.PostForm
					}
				}
				requests = append(requests, map[string]any{"method": r.Method, "url": r.URL.String(), "headers": headers, "body_kind": kind, "body": body})
				i := positions[r.URL.Path]
				positions[r.URL.Path] = i + 1
				replies := item.Replies[r.URL.Path]
				if i >= len(replies) {
					return nil, fmt.Errorf("owned unexpected source request")
				}
				reply := replies[i]
				return &http.Response{StatusCode: reply.Status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(reply.Body)), Request: r}, nil
			})
			defer func() { a.Close(); http.DefaultTransport = previous }()
			c := a.Settings()
			c.Sites[0].Kind = "mteam"
			c.Sites[0].BaseURL = "https://api.m-team.cc"
			c.Sites[0].APIKey = "synthetic-owned-api-key"
			c.ReadSiteMessages = &item.Read
			if err := a.saveSettings(ctx, c); err != nil {
				t.Fatal(err)
			}
			run := enqueueSyntheticPluginHTTP(t, a)
			a.executePluginRun(ctx, run)
			finished, err := a.pluginStore.Run(ctx, run.ID)
			if err != nil || finished.State != "succeeded" {
				t.Fatal("source API receipt not committed", finished.State, finished.ErrorCode, err)
			}
			observed, _ := json.Marshal(requests)
			var actual []map[string]any
			json.Unmarshal(observed, &actual)
			for _, r := range item.Requests {
				delete(r, "timeout")
			}
			if !reflect.DeepEqual(actual, item.Requests) {
				t.Fatal("SDK host changed original request sequence", actual, item.Requests)
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
				t.Fatal("invalid response body")
			}
			if payload.Success != (item.Data != nil) {
				t.Fatal("SDK changed module DTO/None outcome")
			}
			if item.Data != nil {
				payload.Data["domain"] = "m-team.cc"
				payload.Data["updated_day"] = item.Data["updated_day"]
				if !reflect.DeepEqual(payload.Data, item.Data) {
					t.Fatal("SDK source DTO differs", payload.Data, item.Data)
				}
			}
			if item.Name == "messages-before-seeding" || item.Name == "large-integer-ID-preserved" {
				var sealed []byte
				if a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='site_connection' AND id='known'`).Scan(&sealed) != nil || bytes.Contains(sealed, []byte(" heading ")) {
					t.Fatal("source message not encrypted")
				}
				before := len(requests)
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
				if err != nil || recovered.State != "succeeded" || string(recovered.Result) != string(finished.Result) || len(requests) != before {
					t.Fatal("cold confirmed operation replayed source requests", err)
				}
				var snapshot siteSnapshot
				if err = cold.Store.Get(ctx, "site_connection", "known", &snapshot); err != nil || snapshot.SourceData == nil {
					t.Fatal("source snapshot missing after cold startup", err)
				}
				if item.Name == "large-integer-ID-preserved" {
					if id, ok := snapshot.SourceData.UserID.(json.Number); !ok || id.String() != "1208925819614629174706176" {
						t.Fatal("cold source integer ID rounded")
					}
				} else {
					if len(snapshot.SourceData.Messages) != 2 || snapshot.SourceData.Messages[1][0].(json.Number).String() != "1" || len(snapshot.SourceData.Messages[1][2].([]any)) != 1 || snapshot.Stats.Messages != nil {
						t.Fatal("arbitrary source message tuple lost or partial native contents invented")
					}
				}
			}
		})
	}
}

func TestSiteStatisticMTeamRevocationStopsMarkReadAndSeeding(t *testing.T) {
	a, _, _, _ := pluginHTTPFixture(t)
	ctx := context.Background()
	calls := []string{}
	previous := http.DefaultTransport
	http.DefaultTransport = siteOutcomeRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.m-team.cc" {
			return nil, fmt.Errorf("owned unexpected origin")
		}
		calls = append(calls, r.URL.Path)
		body := ""
		switch r.URL.Path {
		case "/api/member/profile":
			body = `{"code":"0","data":{"id":7,"username":"owned"}}`
		case "/api/msg/search":
			c := a.Settings()
			c.APIToken = "owned-revoked-source-api"
			if err := a.saveSettings(ctx, c); err != nil {
				t.Error(err)
			}
			body = `{"code":"0","data":{"data":[{"id":1,"unread":true,"title":"owned","createdDate":"date","context":"content"}]}}`
		default:
			t.Error("revoked MTeam operation reached markRead/seeding")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	defer func() { a.Close(); http.DefaultTransport = previous }()
	c := a.Settings()
	c.Sites[0].Kind = "mteam"
	c.Sites[0].BaseURL = "https://api.m-team.cc"
	c.Sites[0].APIKey = "synthetic-owned-key"
	read := true
	c.ReadSiteMessages = &read
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	run := enqueueSyntheticPluginHTTP(t, a)
	a.executePluginRun(ctx, run)
	finished, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || finished.State != "failed" || finished.ErrorCode != "PLUGIN_API_AUTH_REVOKED" || len(calls) != 2 {
		t.Fatal("source reader retained revoked authority", finished.State, finished.ErrorCode, calls, err)
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind IN ('site_connection','site_stats_latest','site_stats_history')`).Scan(&count); err != nil || count != 0 {
		t.Fatal("revoked source reader published account data", err)
	}
	siteMessageAssertColdRevocationHasNoEffects(t, a, run, func() int { return len(calls) })
}
