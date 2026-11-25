package app

import (
	"context"
	"database/sql"
	"light-media/internal/catalog"
	"strconv"
	"strings"
)

type cardState struct{ Library, Subscription, Download string }

func (a *App) decorateMedia(ctx context.Context, items []catalog.Media) error {
	keys := []string{}
	seen := map[string]bool{}
	var collect func([]catalog.Media)
	collect = func(ms []catalog.Media) {
		for _, m := range ms {
			key := mediaKey(m)
			if validMedia(m.MediaType, m.ID) && !seen[key] && len(keys) < 1000 {
				keys = append(keys, key)
				seen[key] = true
			}
			collect(m.Recommendations)
		}
	}
	collect(items)
	if len(keys) == 0 {
		return nil
	}
	// Keep all three badges on the same committed snapshot, without taking the writer.
	tx, err := a.Store.reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state := map[string]cardState{}
	params := make([]any, len(keys)+1)
	for i, k := range keys {
		params[i+1] = k
	}
	marks := strings.TrimRight(strings.Repeat("?,", len(keys)), ",")
	for _, kind := range []string{"asset", "subscription", "intent"} {
		params[0] = kind
		table, _ := recordTableFor(kind)
		rows, e := tx.QueryContext(ctx, `SELECT (json_extract(payload,'$.media.media_type')||':'||json_extract(payload,'$.media.id')),json_extract(payload,'$.id'),json_extract(payload,'$.state') FROM `+table.name+` WHERE record_kind=? AND (json_extract(payload,'$.media.media_type')||':'||json_extract(payload,'$.media.id')) IN (`+marks+`)`, params...)
		if e != nil {
			return e
		}
		for rows.Next() {
			var key string
			var id, status sql.NullString
			if e = rows.Scan(&key, &id, &status); e != nil {
				rows.Close()
				return e
			}
			value := state[key]
			switch kind {
			case "asset":
				if status.String == "present" || status.String == "external" {
					value.Library = "present"
				} else if value.Library != "present" {
					value.Library = status.String
				}
			case "subscription":
				if status.String == "active" || status.String == "paused" {
					value.Subscription = id.String
				}
			case "intent":
				if status.String == "planned" || status.String == "submitting" || status.String == "accepted" || status.String == "downloading" || status.String == "downloaded" || status.String == "reconcile_pending" {
					value.Download = status.String
				}
			}
			state[key] = value
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	var apply func([]catalog.Media)
	apply = func(ms []catalog.Media) {
		for i := range ms {
			v := state[ms[i].MediaType+":"+strconv.FormatInt(ms[i].ID, 10)]
			ms[i].LibraryState = v.Library
			ms[i].SubscriptionID = v.Subscription
			ms[i].DownloadState = v.Download
			apply(ms[i].Recommendations)
		}
	}
	apply(items)
	return nil
}
