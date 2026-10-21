package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins"
)

func TestSiteStatisticSourceOutcomesActualSDKAndColdRecovery(t *testing.T) {
	oraclePath := os.Getenv("LIGHT_MEDIA_OWNED_OUTCOME_ORACLE")
	if oraclePath == "" {
		oraclePath = "../../testdata/plugins/site-account-outcomes-original-oracle.json"
	}
	raw, err := os.ReadFile(oraclePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name, Kind, Body string
			Data             map[string]any
			Success          bool
			APIEvents        []string `json:"api_events"`
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, item := range fixture.Cases {
		if item.Kind == "throws" || item.Kind == "unsupported" {
			continue
		}
		t.Run(item.Name, func(t *testing.T) {
			a, _, _, _ := pluginHTTPFixture(t)
			ctx := context.Background()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, item.Body) }))
			defer server.Close()
			c := a.Settings()
			c.Sites[0].BaseURL = server.URL
			c.Sites[0].Kind = item.Kind
			c.Sites[0].APIKey = "owned-synthetic-key"
			if item.Kind == "mteam" {
				c.Sites[0].BaseURL = "https://api.m-team.cc"
				previous := http.DefaultTransport
				http.DefaultTransport = siteOutcomeRoundTrip(func(r *http.Request) (*http.Response, error) {
					if r.URL.Host != "api.m-team.cc" {
						return nil, fmt.Errorf("owned transport denied unexpected origin")
					}
					calls.Add(1)
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(item.Body)), Request: r}, nil
				})
				defer func() { a.Close(); http.DefaultTransport = previous }()
			}
			if item.Kind == "nexus" {
				c.Sites[0].Kind = "jiayuan"
			}
			if err := a.saveSettings(ctx, c); err != nil {
				t.Fatal(err)
			}
			run := enqueueSyntheticPluginHTTP(t, a)
			a.executePluginRun(ctx, run)
			finished, err := a.pluginStore.Run(ctx, run.ID)
			if err != nil || finished.State != "succeeded" {
				t.Fatal("SDK did not publish source response", finished.State, finished.ErrorCode, err)
			}
			receipt, err := a.pluginStore.siteStatisticReceipt(ctx, run)
			if err != nil {
				t.Fatal(err)
			}
			var response plugins.HTTPResponse
			if json.Unmarshal(receipt, &response) != nil {
				t.Fatal("invalid receipt")
			}
			var payload struct {
				Success bool
				Data    map[string]any
			}
			if json.Unmarshal(response.Body, &payload) != nil {
				t.Fatal("invalid body")
			}
			if payload.Success != item.Success {
				t.Fatal("native connection health replaced source truthiness")
			}
			if item.Success {
				if _, ok := payload.Data["updated_day"].(string); !ok {
					t.Fatal("missing DTO day")
				}
				payload.Data["updated_day"] = "<day>"
				payload.Data["domain"] = "owned.example"
				if !reflect.DeepEqual(payload.Data, item.Data) {
					t.Fatal("SDK response differs from installed source oracle", payload.Data, item.Data)
				}
			} else if len(payload.Data) != 0 {
				t.Fatal("None response invented source fields")
			}
			var facts, histories int
			if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='site.refreshed'`).Scan(&facts); err != nil {
				t.Fatal(err)
			}
			if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='site_stats_history'`).Scan(&histories); err != nil {
				t.Fatal(err)
			}
			want := 0
			if item.Success {
				want = 1
			}
			if facts != want || histories != want {
				t.Fatal("DTO/None facts or history differ", facts, histories, want)
			}
			var snapshot siteSnapshot
			if err = a.Store.Get(ctx, "site_connection", "known", &snapshot); err != nil {
				t.Fatal(err)
			}
			if item.Name != "mt-numeric-userid" && snapshot.Connection.Status != "failed" {
				t.Fatal("unhealthy native connection became healthy")
			}
			if item.Success && (snapshot.Stats.Uploaded != nil || snapshot.Stats.LeechingBytes != nil) {
				t.Fatal("source defaults became observed native zero")
			}
			if item.Name == "logged-out" {
				day, err := statisticData(snapshot, c.Sites[0])
				if err != nil || day.Error == "" || day.UpdatedTime == "" || snapshot.SourceData.Time != nil {
					t.Fatal("DTO and database timestamps coupled", err)
				}
				before := calls.Load()
				if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET state='running',result=NULL WHERE id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
				dir := a.Dir
				if err = a.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				recovered, err := reopened.pluginStore.Run(ctx, run.ID)
				if err != nil || recovered.State != "succeeded" || string(recovered.Result) != string(finished.Result) || calls.Load() != before {
					t.Fatal("lost ACK refetched or lost source error DTO", err)
				}
				var cold siteSnapshot
				if err = reopened.Store.Get(ctx, "site_connection", "known", &cold); err != nil || cold.SourceData == nil || cold.SourceData.Error == nil || cold.SourceData.Time != nil {
					t.Fatal("cold encrypted snapshot lost error DTO", err)
				}
			}
		})
	}
}

type siteOutcomeRoundTrip func(*http.Request) (*http.Response, error)

func (f siteOutcomeRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSiteStatisticErrorDTOCannotOverwriteEarlierSameDay(t *testing.T) {
	a, _, _, _ := pluginHTTPFixture(t)
	ctx := context.Background()
	var failed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failed.Load() {
			fmt.Fprint(w, `<input type="password">`)
			return
		}
		fmt.Fprint(w, `<a href="logout.php">out</a><a href="userdetails.php?id=7">owned</a><div>上传：2 GB 下载：1 GB 总做种数：0 总做种体积：0 B</div>`)
	}))
	defer server.Close()
	c := a.Settings()
	c.Sites[0].BaseURL = server.URL
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		failed.Store(i == 1)
		run := enqueueSyntheticPluginHTTP(t, a)
		a.executePluginRun(ctx, run)
		finished, err := a.pluginStore.Run(ctx, run.ID)
		if err != nil || finished.State != "succeeded" {
			t.Fatal("source refresh not completed", err)
		}
	}
	var latest, connection, history siteSnapshot
	if err := a.Store.Get(ctx, "site_stats_latest", "known", &latest); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Get(ctx, "site_connection", "known", &connection); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Get(ctx, "site_stats_history", "known:"+latest.Day, &history); err != nil {
		t.Fatal(err)
	}
	if latest.SourceData == nil || latest.SourceData.Upload != 2147483648 || history.SourceData.Upload != 2147483648 || connection.SourceData.Error == nil || connection.Connection.Status != "failed" {
		t.Fatal("failed DTO replaced prior day or diagnostic lost")
	}
}
