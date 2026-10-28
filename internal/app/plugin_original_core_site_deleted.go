package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// The two fixed SiteDeleted consumers change search/RSS/subscription selection,
// not site configuration. A plugin-issued ordinary event therefore reuses the
// existing settings/reference transaction without deleting a configured site,
// inventing a CookieCloud sync, or broadcasting a second SiteDeleted event.
func (a *App) consumeOriginalCoreSiteDeleted(ctx context.Context, raw json.RawMessage, subscription bool) error {
	value, err := decodeSourceJSON(raw)
	if err != nil || sourceMap(value) == nil {
		return bad("PLUGIN_EVENT_INVALID", "站点删除事件数据不是对象")
	}
	fields := sourceMap(value)
	reset, _ := fields["site_id"].(string)
	deleted := []string{}
	if reset != "*" {
		number, infinity, numeric := originalBaseNumber(fields["site_id"])
		// Python list membership distinguishes a string "1" from site ID 1,
		// while True and 1.0 compare equal to 1. Unknown/false IDs are no-ops.
		if !numeric || infinity != 0 || number == nil || !number.IsInt() || !number.Num().IsInt64() || number.Sign() <= 0 {
			return nil
		}
		identity, err := a.Store.nativeSiteByID(ctx, number.Num().Int64())
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		deleted = append(deleted, identity.SiteID)
	}
	// Source Search and Subscribe handlers are separate asynchronous consumers.
	// Each setting commit precedes Subscribe's per-record commits. Preserve
	// earlier effects if a later record update fails, rather than replacing that
	// source behavior with one stronger all-or-nothing transaction.
	if err := a.pruneOriginalCoreSiteSelection(ctx, deleted, reset == "*", subscription); err != nil || !subscription {
		return err
	}
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT id FROM records WHERE kind='subscription' ORDER BY rowid`)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		var raw []byte
		err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='subscription' AND id=?`, id).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			tx.Rollback()
			continue
		}
		var sub Subscription
		if err != nil || json.Unmarshal(raw, &sub) != nil {
			tx.Rollback()
			if err != nil {
				return err
			}
			return bad("INVALID_SUBSCRIPTION", "订阅站点引用无法读取")
		}
		changed := false
		if reset == "*" && len(sub.SiteIDs) > 0 {
			sub.SiteIDs, changed = []string{}, true
		}
		for _, site := range deleted {
			var removed bool
			sub.SiteIDs, removed = removeCoreSiteReference(sub.SiteIDs, site)
			changed = changed || removed
		}
		if changed {
			sub.Revision++
			if err = writePrunedCoreSubscriptionSiteTx(ctx, tx, sub); err != nil {
				tx.Rollback()
				return err
			}
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) pruneOriginalCoreSiteSelection(ctx context.Context, deleted []string, reset, subscription bool) error {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	c := a.Settings()
	values := &c.IndexerSites
	if subscription {
		values = &c.RSSSites
	}
	changed := reset
	if reset {
		*values = []string{}
	}
	for _, site := range deleted {
		var removed bool
		*values, removed = removeCoreSiteReference(*values, site)
		changed = changed || removed
	}
	if !changed {
		return nil
	}
	return a.saveSettingsWithEffect(ctx, c, nil)
}
