package app

import (
	"context"
	"errors"
	iyuu "light-media/internal/plugins/iyuuautoseed"
	"testing"
)

func TestPluginSiteDependencyReadIsNarrowAndAggregationStaysStrict(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	for _, id := range []string{"owned-sibling-a", "owned-sibling-b"} {
		uninstallInstall(t, a, id, iyuu.Manifest(), map[string]any{"enabled": false, "sites": []any{}}, false)
	}
	installSiteControlPeerFixture(t, a, "CustomSites", map[string]any{"enabled": true, "sites": []any{}})
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	peer, ok, err := a.pluginStore.siteControlDependencyPeerTx(ctx, tx, "CustomSites")
	if err != nil || !ok || peer.ID != "CustomSites" || peer.PluginID != "CustomSites" || !peer.Desired || string(peer.Config["enabled"]) != "true" || peer.Revision != 1 {
		t.Fatal("dependency identity/config lost", peer, ok, err)
	}
	if _, ok, err = a.pluginStore.siteControlDependencyPeerTx(ctx, tx, "MissingDependency"); err != nil || ok {
		t.Fatal("absent dependency", ok, err)
	}
	_, _, err = a.pluginStore.siteControlDependencyPeerTx(ctx, tx, "")
	var api *APIError
	if !errors.As(err, &api) || api.Code != "INVALID_PLUGIN_DEPENDENCY" {
		t.Fatal("empty dependency widened scope", err)
	}
	_, _, err = a.pluginStore.siteControlPeersTx(ctx, tx)
	api = nil
	if !errors.As(err, &api) || api.Code != "PLUGIN_REFERENCE_AMBIGUOUS" {
		t.Fatal("original aggregation no longer fails closed", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE plugin_values SET sealed=? WHERE instance_id=? AND kind='config' AND item_key='main'`, []byte("owned-invalid-ciphertext"), "CustomSites")
	if err != nil {
		t.Fatal(err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		t.Fatal("ciphertext fixture did not update selected config", affected, err)
	}
	if _, _, err = a.pluginStore.siteControlDependencyPeerTx(ctx, tx, "CustomSites"); err == nil {
		t.Fatal("selected corrupted ciphertext bypassed decryption guard")
	}
}
