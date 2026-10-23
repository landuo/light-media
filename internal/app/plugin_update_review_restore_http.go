package app

import (
	"context"
	"net/http"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

// Retained review tombstones remain discoverable, including registered clones.
// This is a read-only offer, not permission to install or execute an instance.
func (a *App) pluginReviewRestoreCards(ctx context.Context) ([]pluginCard, error) {
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT instance_id FROM plugin_installations WHERE installed=0 ORDER BY created_at,instance_id`)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	cards := []pluginCard{}
	for _, id := range ids {
		in, err := a.pluginStore.pluginLifecycleInstallation(ctx, a.Store.DB, id)
		if err != nil {
			return nil, err
		}
		// An intervening reinstall belongs to the installed list. The POST also
		// revalidates the full captured generation/revision under its own transaction.
		if in.Installed {
			continue
		}
		review, err := a.pluginStore.pluginUpdateReviewRead(ctx, a.Store.DB, in)
		if err != nil {
			return nil, err
		}
		if review == nil {
			continue
		}
		manifest, _, available := builtin.Lookup(in.PluginID)
		state, stateErr := pluginLifecycleState(in)
		if stateErr != nil {
			return nil, stateErr
		}
		eligible := available && manifest.Version == in.Version && manifest.SchemaVersion == in.ManifestSchemaVersion && manifest.Validate() == nil && state == pluginLifecycleInactive
		if !eligible {
			manifest = plugins.Manifest{PluginID: in.PluginID, Name: in.PluginID, Version: in.Version}
		}
		card, err := a.pluginStore.presentInstance(ctx, in, manifest)
		if err != nil {
			return nil, err
		}
		card.Installed = false
		card.State = false
		card.UpdateReviewPending = true
		if eligible {
			card.RestoreReview = &pluginReviewRestoreRequest{Version: in.Version, Generation: in.Generation, Revision: review.Revision}
		} else {
			card.RestoreReviewBlockedReason = "当前程序未包含此保留版本，未改动配置或执行意图"
		}
		cards = append(cards, card)
	}
	return cards, nil
}

func (a *App) pluginRestoreReviewHTTP(w http.ResponseWriter, r *http.Request, instance string) (any, error) {
	var request pluginReviewRestoreRequest
	if err := decodePluginBody(w, r, &request); err != nil {
		return nil, err
	}
	// Cold restoration deliberately does not wake the execution queue.
	return a.pluginStore.restoreReviewedPlugin(r.Context(), instance, request)
}
