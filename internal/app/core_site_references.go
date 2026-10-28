package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
)

// Ordinary settings and subscription references are independent of plugin watchers.
func (a *App) pruneDeletedCoreSiteReferencesTx(ctx context.Context, tx *sql.Tx, previous Settings, next *Settings) error {
	deleted := []string{}
	for _, site := range previous.Sites {
		if !slices.ContainsFunc(next.Sites, func(current SiteConfig) bool { return current.ID == site.ID }) {
			deleted = append(deleted, site.ID)
		}
	}
	if len(deleted) == 0 {
		return nil
	}
	for _, id := range deleted {
		next.IndexerSites, _ = removeCoreSiteReference(next.IndexerSites, id)
		next.RSSSites, _ = removeCoreSiteReference(next.RSSSites, id)
	}
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM records WHERE kind='subscription'`)
	if err != nil {
		return err
	}
	changed := []Subscription{}
	for rows.Next() {
		var raw []byte
		var sub Subscription
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if json.Unmarshal(raw, &sub) != nil {
			rows.Close()
			return bad("INVALID_SUBSCRIPTION", "订阅引用无法读取，站点删除未提交")
		}
		effect := false
		for _, id := range deleted {
			var removed bool
			sub.SiteIDs, removed = removeCoreSiteReference(sub.SiteIDs, id)
			effect = effect || removed
		}
		if effect {
			sub.Revision++
			changed = append(changed, sub)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, sub := range changed {
		if err := writePrunedCoreSubscriptionSiteTx(ctx, tx, sub); err != nil {
			return err
		}
	}
	return nil
}

func writePrunedCoreSubscriptionSiteTx(ctx context.Context, tx *sql.Tx, sub Subscription) error {
	if err := txPut(ctx, tx, "subscription", sub.ID, sub); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,lease_until=0,updated_at=? WHERE kind='subscription' AND json_extract(payload,'$.id')=? AND state IN ('queued','running','retry_wait','waiting_paused')`, now(), sub.ID)
	return err
}

func removeCoreSiteReference(values []string, id string) ([]string, bool) {
	out := slices.Clone(values)
	index := slices.Index(out, id)
	if index >= 0 {
		return append(out[:index], out[index+1:]...), true
	}
	return out, false
}
