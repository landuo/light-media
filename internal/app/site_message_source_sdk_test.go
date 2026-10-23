package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

func TestSiteMessageImportedSettingActualSDKAndRevocation(t *testing.T) {
	for _, mode := range []string{"source-false", "source-true", "explicit-false", "explicit-true", "revoke-before-mark", "corrupt-proof"} {
		t.Run(mode, func(t *testing.T) {
			a, _, _, _ := pluginHTTPFixture(t)
			ctx := context.Background()
			enabled := mode != "source-false" && mode != "explicit-true"
			c := a.Settings()
			c.Sites[0].BaseURL = "https://rousi.pro/"
			c.Sites[0].Kind = "rousi"
			c.Sites[0].Cookie = ""
			c.Sites[0].APIKey = "owned-profile"
			c.Sites[0].Token = "owned-mail-token"
			c.ReadSiteMessages = nil
			if mode == "explicit-false" {
				v := false
				c.ReadSiteMessages = &v
			}
			if mode == "explicit-true" {
				v := true
				c.ReadSiteMessages = &v
			}
			if e := a.saveSettings(ctx, c); e != nil {
				t.Fatal(e)
			}
			dir := a.Dir
			if e := a.Close(); e != nil {
				t.Fatal(e)
			}
			if _, e := ImportReviewedSourceSettings(ctx, dir, ownedSourceSettingsInput(t), true); e != nil {
				t.Fatal(e)
			}
			if _, e := ImportReviewedSiteMessageSettings(ctx, dir, ownedSiteMessageInput(t, enabled), true); e != nil {
				t.Fatal(e)
			}
			cold, e := New(Options{DataDir: dir})
			if e != nil {
				t.Fatal(e)
			}
			defer cold.Close()
			useBuiltInPluginProcess(t, cold)
			if mode == "corrupt-proof" {
				if _, e = cold.Store.DB.Exec(`UPDATE records SET payload=zeroblob(length(payload)) WHERE kind='source_site_message_settings'`); e != nil {
					t.Fatal(e)
				}
			}
			previous := http.DefaultTransport
			defer func() { http.DefaultTransport = previous }()
			paths := []string{}
			http.DefaultTransport = siteOutcomeRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Scheme != "https" || r.URL.Host != "rousi.pro" {
					t.Error("unexpected owned origin")
					return nil, fmt.Errorf("owned origin mismatch")
				}
				paths = append(paths, r.URL.Path)
				body := `{"code":0,"data":{"id":7,"username":"owned","uploaded":1024}}`
				switch r.URL.Path {
				case "/api/v1/profile":
				case "/api/messages":
					body = `{"code":0,"data":{"messages":[],"total_pages":1}}`
					if mode == "revoke-before-mark" {
						sealed, e := cold.pluginStore.seal(sourceSiteMessageKind, "system", "main", ownedSiteMessageInput(t, false))
						if e != nil {
							t.Error(e)
						}
						if _, e = cold.Store.DB.Exec(`UPDATE records SET payload=? WHERE kind=?`, sealed, sourceSiteMessageKind); e != nil {
							t.Error(e)
						}
					}
				case "/api/messages/read-all":
				default:
					t.Error("unexpected owned request")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			run := enqueueSyntheticPluginHTTP(t, cold)
			cold.executePluginRun(ctx, run)
			finished, e := cold.pluginStore.Run(ctx, run.ID)
			wantSuccess := mode != "revoke-before-mark" && mode != "corrupt-proof"
			if e != nil {
				t.Fatal(e)
			}
			if wantSuccess {
				if finished.State != "succeeded" {
					t.Fatal("source API did not settle", finished.State, finished.ErrorCode)
				}
				receipt, e := cold.pluginStore.siteStatisticReceipt(ctx, run)
				if e != nil {
					t.Fatal(e)
				}
				var response plugins.HTTPResponse
				if json.Unmarshal(receipt, &response) != nil {
					t.Fatal("invalid SDK receipt")
				}
				var result struct{ Success bool }
				if json.Unmarshal(response.Body, &result) != nil || !result.Success {
					t.Fatal("source outcome mismatch")
				}
			} else {
				code := "SITE_CHANGED"
				if mode == "corrupt-proof" {
					code = "PLUGIN_HOST_FAILED"
				}
				if finished.State != "failed" || finished.ErrorCode != code {
					t.Fatal("revoked/unconfirmed setting did not fail", finished.State, finished.ErrorCode)
				}
				var published int
				if e = cold.Store.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM records WHERE kind IN ('site_connection','site_stats_latest','site_stats_history'))+(SELECT COUNT(*) FROM domain_events)+(SELECT COUNT(*) FROM plugin_site_statistic_operations)`).Scan(&published); e != nil || published != 0 {
					t.Fatal("revoked/unconfirmed setting published account data/event/receipt", published, e)
				}
			}
			want := []string{"/api/v1/profile"}
			if mode == "source-true" || mode == "explicit-true" {
				want = append(want, "/api/messages", "/api/messages/read-all")
			}
			if mode == "revoke-before-mark" {
				want = append(want, "/api/messages")
			}
			if mode == "corrupt-proof" {
				want = nil
			}
			if strings.Join(paths, ",") != strings.Join(want, ",") {
				t.Fatal("imported flag/user override/revocation changed IO", paths, want)
			}
			if mode == "revoke-before-mark" {
				// A synchronous guard denial is terminal, unlike interrupted
				// admission. Reopening cannot recollect, mark read, or post.
				collection, found, err := cold.pluginStore.siteMessageCollectionForRun(ctx, cold.Store.DB, run)
				if err != nil || !found || collection.Stage != "aborted" || collection.Failure != "SITE_CHANGED" {
					t.Fatal("missing terminal local denial", collection.Stage, err)
				}
				before := len(paths)
				if err = cold.Close(); err != nil {
					t.Fatal(err)
				}
				restarted, err := New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				defer restarted.Close()
				if err = restarted.resumePluginNoticeRoutes(ctx, 200); err != nil {
					t.Fatal(err)
				}
				terminal, err := restarted.pluginStore.Run(ctx, run.ID)
				if err != nil || terminal.State != "failed" || terminal.ErrorCode != "SITE_CHANGED" || len(paths) != before {
					t.Fatal("cold restart retried terminal denied read", terminal.State, paths, err)
				}
				var visible int
				if err = restarted.Store.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM records WHERE kind IN ('site_connection','site_stats_latest','site_stats_history','notification'))+(SELECT COUNT(*) FROM domain_events)+(SELECT COUNT(*) FROM plugin_site_statistic_operations)+(SELECT COUNT(*) FROM plugin_message_queue)`).Scan(&visible); err != nil || visible != 0 {
					t.Fatal("denied admission published effects", visible, err)
				}
			}
			if mode == "corrupt-proof" {
				projection := cold.publicSettings()
				if projection["effective_read_site_messages"] != nil || projection["read_site_messages_error"] == nil || projection["read_site_messages"] != nil {
					t.Fatal("corrupt proof was displayed as a known message preference")
				}
				// Plain native connection tests do not depend on the source-only receipt.
				if _, e = cold.refreshManagedSite(ctx, "known"); e != nil {
					t.Fatal(e)
				}
				if len(paths) != 1 || paths[0] != "/api/v1/profile" {
					t.Fatal("native connection test read messages or used source proof")
				}
			}
		})
	}
}
