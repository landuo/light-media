package app

import (
	"context"
	"testing"
)

func TestSiteMessageCoreAuthorityRevokedBetweenRouterStepsAndAtDispatch(t *testing.T) {
	for _, boundary := range []string{"route_step", "queue_intent"} {
		for _, change := range []string{"stop_site", "connection"} {
			t.Run(boundary+"/"+change, func(t *testing.T) {
				a := newTestApp(t)
				siteMessageHTTPFixture(t, a, true)
				ctx := context.Background()
				sent := siteMessageOwnedFIFOSender(t, a)
				changed := false
				a.pluginStore.notices.afterCommit = func(operation string) error {
					if changed || operation != boundary {
						return nil
					}
					changed = true
					config := a.Settings()
					switch change {
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
				job := siteMessageClaimBatch(t, a)
				runErr := a.runSiteRefresh(ctx, job)
				a.pluginStore.notices.afterCommit = nil
				if !changed {
					t.Fatal("real revocation boundary was not reached")
				}
				if len(sent()) != 0 {
					t.Fatal("revoked source reached an external sender", len(sent()))
				}
				var steps int
				if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_notice_route_steps`).Scan(&steps); err != nil {
					t.Fatal(err)
				}
				if boundary == "route_step" && steps != 1 {
					t.Fatal("new Router steps ran after revocation", steps)
				}
				if runErr == nil {
					t.Fatal("revoked core collection reported normal return")
				}
				// Recovery sees the same persisted revocation and must not send either.
				_ = a.resumeSiteMessageCollections(ctx)
				if len(sent()) != 0 {
					t.Fatal("recovery ignored revoked authority", len(sent()))
				}
			})
		}
	}
}
