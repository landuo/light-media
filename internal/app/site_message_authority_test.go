package app

import (
	"context"
	"sync/atomic"
	"testing"
)

// Mutate persisted settings after a real Router commit without publishing the
// in-memory cache. This models the config commit/cache publication window and
// proves each next effect checks its own SQL snapshot, not only an outer guard.
func TestSiteMessageAuthorityRevokedBetweenRouterStepsAndAtDispatch(t *testing.T) {
	for _, boundary := range []string{"route_step", "queue_intent"} {
		for _, change := range []string{"api_key", "stop_site", "connection"} {
			t.Run(boundary+"/"+change, func(t *testing.T) {
				a, _, _, _ := pluginHTTPFixture(t)
				siteMessageHTTPFixture(t, a, true)
				ctx := context.Background()
				var sends atomic.Int32
				a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
				changed := false
				a.pluginStore.notices.afterCommit = func(operation string) error {
					if changed || operation != boundary {
						return nil
					}
					changed = true
					config := a.Settings()
					switch change {
					case "api_key":
						config.APIToken = "owned-replacement-api-key"
					case "stop_site":
						config.Sites[0].Enabled = false
					case "connection":
						config.Sites[0].BaseURL = "https://changed.owned.invalid"
					}
					sealed, err := a.Store.Seal(config)
					if err != nil {
						return err
					}
					_, err = a.Store.DB.ExecContext(ctx, `UPDATE records SET payload=?,updated_at=? WHERE kind='config' AND id='main'`, sealed, now())
					return err
				}
				run := enqueueSyntheticPluginHTTP(t, a)
				a.executePluginRun(ctx, run)
				a.pluginStore.notices.afterCommit = nil
				if !changed {
					t.Fatal("real revocation boundary was not reached")
				}
				if sends.Load() != 0 {
					t.Fatal("revoked source reached an external sender", sends.Load())
				}
				var steps int
				if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_notice_route_steps`).Scan(&steps); err != nil {
					t.Fatal(err)
				}
				if boundary == "route_step" && steps != 1 {
					t.Fatal("new Router steps ran after revocation", steps)
				}
				finished, err := a.pluginStore.Run(ctx, run.ID)
				if err != nil || finished.State == "succeeded" {
					t.Fatal("revoked API reported normal return", finished.State, err)
				}
				// Recovery sees the same persisted revocation and must not send either.
				_ = a.resumeSiteMessageCollections(ctx)
				if sends.Load() != 0 {
					t.Fatal("recovery ignored revoked authority", sends.Load())
				}
			})
		}
	}
}
