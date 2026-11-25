package app

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Read just the changed jobs and referenced subjects. The sorting index decides
// membership and order even if an active job was inserted, removed or reprioritized.
func incrementalJobQueue(ctx context.Context, tx *sql.Tx, previous *jobQueueDisplay, settings *Settings, version int64) (map[string]jobView, []string, []string, error) {
	if previous == nil || previous.settings != settings || version <= previous.version || version-previous.version > 128 {
		return nil, nil, nil, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT source,COALESCE(old_id,''),COALESCE(new_id,'') FROM navigation_job_changes WHERE version>?`, previous.version)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	changed := map[string]map[string]bool{}
	count := int64(0)
	for rows.Next() {
		var source, oldID, newID string
		if err = rows.Scan(&source, &oldID, &newID); err != nil {
			return nil, nil, nil, err
		}
		ids := changed[source]
		if ids == nil {
			ids = map[string]bool{}
			changed[source] = ids
		}
		if oldID != "" {
			ids[oldID] = true
		}
		if newID != "" {
			ids[newID] = true
		}
		count++
	}
	if err = rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	if count != version-previous.version {
		return nil, nil, nil, nil
	}
	keep := make(map[string]jobView, len(previous.rows))
	for _, view := range previous.rows {
		if changed["jobs"][view.ID] || changed["events"][view.ID] {
			continue
		}
		subject := ""
		switch view.Kind {
		case "subscription":
			subject = "subscribe"
		case "download", "lark_download", "subscription_download", "subscription_acquisition_receipt", "native_download":
			subject = "downloadhistory"
		case "import":
			subject = "import_plan"
		case "scrape", "fnos_sync", "asset_check":
			subject = "library_asset"
		case "reorganize_cleanup":
			if len(changed["library_asset"]) > 0 {
				continue
			}
			subject = "history_cleanup"
		}
		if len(changed[subject]) > 0 {
			var payload struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(view.Payload, &payload) != nil || changed[subject][payload.ID] {
				continue
			}
		}
		keep[view.ID] = view
	}
	page, err := tx.QueryContext(ctx, `SELECT id FROM jobs ORDER BY `+jobDisplayPriority+`,created_at DESC,id LIMIT 100`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer page.Close()
	order, dirty := make([]string, 0, 100), []string{}
	for page.Next() {
		var id string
		if err = page.Scan(&id); err != nil {
			return nil, nil, nil, err
		}
		order = append(order, id)
		if _, ok := keep[id]; !ok {
			dirty = append(dirty, id)
		}
	}
	return keep, order, dirty, page.Err()
}
