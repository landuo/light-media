package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"

	"light-media/internal/catalog"
)

// An ordinary season card shows total minus lack. Confirmed local slots
// prove a minimum downloaded count; other library providers
// may already have proved more. Multiple copies of an episode count once.
func inventorySubscriptionLack(s Subscription, library map[int]bool) (int, bool) {
	if s.Media.MediaType != "tv" || s.BestVersion || s.TargetScope == "episodes" || len(s.Episodes) != 0 || s.TotalEpisodes < subscriptionStartEpisode(s) {
		return 0, false
	}
	start := subscriptionStartEpisode(s)
	inScope := func(ep int) bool { return ep >= start && ep <= s.TotalEpisodes && !slices.Contains(s.Excluded, ep) }
	owned := 0
	for ep, present := range library {
		if present && inScope(ep) {
			owned++
		}
	}
	if owned == 0 {
		return 0, false
	}
	lack := s.TotalEpisodes - start + 1 - owned
	return lack, s.LackEpisode == nil || lack < *s.LackEpisode
}

// A nil identity reconciles pre-existing subscriptions during startup. During
// import this runs inside the transaction that publishes a video's slots.
func reconcileInventorySubscriptions(ctx context.Context, tx *sql.Tx, identity *catalog.Media) error {
	query := "SELECT payload FROM subscribe WHERE json_extract(payload,'$.state') IS NOT 'cancelled'"
	var args []any
	if identity != nil {
		if identity.MediaType != "tv" {
			return nil
		}
		if providerNativeMedia(*identity) {
			query += " AND media_source=? AND media_id=?"
			args = []any{identity.ProviderSource, identity.ProviderID}
		} else {
			query += " AND tmdbid=?"
			args = []any{identity.ID}
		}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	changes := make(map[string]int)
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		var sub Subscription
		if err = json.Unmarshal(raw, &sub); err != nil {
			return err
		}
		if sub.Media.MediaType != "tv" || sub.BestVersion || identity != nil && mediaKey(sub.Media) != mediaKey(*identity) {
			continue
		}
		library, err := inventorySubscriptionEpisodes(ctx, tx, sub)
		if err != nil {
			return err
		}
		if lack, changed := inventorySubscriptionLack(sub, library); changed {
			changes[sub.ID] = lack
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for id, lack := range changes {
		if _, err = tx.ExecContext(ctx, "UPDATE subscribe SET payload=json_set(payload,'$.lack_episode',?) WHERE uid=?", lack, id); err != nil {
			return err
		}
	}
	return nil
}

func inventorySubscriptionEpisodes(ctx context.Context, tx *sql.Tx, sub Subscription) (map[int]bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT DISTINCT episode FROM asset_slots WHERE media_key=? AND season=? AND episode BETWEEN ? AND ?", mediaKey(sub.Media), sub.Season, subscriptionStartEpisode(sub), sub.TotalEpisodes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	owned := make(map[int]bool)
	for rows.Next() {
		var ep int
		if err := rows.Scan(&ep); err != nil {
			return nil, err
		}
		owned[ep] = true
	}
	return owned, rows.Err()
}
