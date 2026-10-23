package app

import (
	"context"
	"errors"
	"testing"

	"light-media/internal/plugins/builtin"
)

// The shared dependency query must retain both the narrow namespace introduced
// for IYUU and the authenticated clone filter. A repeated PluginID alone is not
// proof of a clone and must remain ambiguous.
func TestPluginSiteDependencyIntegratedCloneLineage(t *testing.T) {
	for _, mutation := range []string{"formal-clone", "unmarked-duplicate", "missing-clone-proof"} {
		t.Run(mutation, func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			manifest, _, ok := builtin.Lookup("AutoSignIn")
			if !ok {
				t.Fatal("compiled AutoSignIn missing")
			}
			config := map[string]any{"enabled": false, "sign_sites": []string{}, "login_sites": []string{}}
			uninstallInstall(t, a, "AutoSignIn", manifest, config, false)
			clone, err := a.pluginStore.Clone(ctx, "AutoSignIn", PluginCloneRequest{Suffix: "copy"})
			if err != nil {
				t.Fatal(err)
			}
			installSiteControlPeerFixture(t, a, "CustomSites", map[string]any{"enabled": true, "sites": []any{}})
			if mutation == "unmarked-duplicate" {
				uninstallInstall(t, a, "owned-unmarked", manifest, config, false)
			}
			if mutation == "missing-clone-proof" {
				result, e := a.Store.DB.ExecContext(ctx, `DELETE FROM plugin_instance_metadata WHERE instance_id=?`, clone.InstanceID)
				if e != nil {
					t.Fatal(e)
				}
				n, e := result.RowsAffected()
				if e != nil || n != 1 {
					t.Fatal("missing proof fixture", n, e)
				}
			}
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			// A broken or ambiguous unrelated namespace cannot contaminate CustomSites.
			custom, exists, err := a.pluginStore.siteControlDependencyPeerTx(ctx, tx, "CustomSites")
			if err != nil || !exists || custom.ID != "CustomSites" {
				t.Fatal("narrow dependency changed", custom, exists, err)
			}
			peer, exists, err := a.pluginStore.siteControlDependencyPeerTx(ctx, tx, "AutoSignIn")
			if mutation == "formal-clone" {
				if err != nil || !exists || peer.ID != "AutoSignIn" {
					t.Fatal("formal clone became default dependency", peer, exists, err)
				}
				_, peers, e := a.pluginStore.siteControlPeersTx(ctx, tx)
				if e != nil || peers["AutoSignIn"].ID != "AutoSignIn" {
					t.Fatal("aggregation lost authenticated clone filter", e)
				}
			} else {
				var api *APIError
				code := "PLUGIN_REFERENCE_AMBIGUOUS"
				if mutation == "missing-clone-proof" {
					code = "PLUGIN_INSTANCE_METADATA_INVALID"
				}
				if !errors.As(err, &api) || api.Code != code {
					t.Fatal("invalid lineage accepted", mutation, err)
				}
			}
		})
	}
}
