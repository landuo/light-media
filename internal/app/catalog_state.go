package app

import (
	"context"
	"encoding/json"
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
	state := map[string]cardState{}
	params := make([]any, len(keys)+1)
	for i, k := range keys {
		params[i+1] = k
	}
	marks := strings.TrimRight(strings.Repeat("?,", len(keys)), ",")
	for _, kind := range []string{"asset", "subscription", "intent"} {
		params[0] = kind
		rows, e := a.Store.DB.QueryContext(ctx, `SELECT payload FROM records WHERE kind=? AND (json_extract(payload,'$.media.media_type')||':'||json_extract(payload,'$.media.id')) IN (`+marks+`)`, params...)
		if e != nil {
			return e
		}
		for rows.Next() {
			var b []byte
			if e = rows.Scan(&b); e != nil {
				rows.Close()
				return e
			}
			var v struct {
				ID    string        `json:"id"`
				Media catalog.Media `json:"media"`
				State string        `json:"state"`
			}
			if json.Unmarshal(b, &v) != nil {
				continue
			}
			key := mediaKey(v.Media)
			value := state[key]
			switch kind {
			case "asset":
				if v.State == "present" || v.State == "external" {
					value.Library = "present"
				} else if value.Library != "present" {
					value.Library = v.State
				}
			case "subscription":
				if v.State == "active" || v.State == "paused" {
					value.Subscription = v.ID
				}
			case "intent":
				if v.State == "planned" || v.State == "submitting" || v.State == "accepted" || v.State == "downloading" || v.State == "downloaded" || v.State == "reconcile_pending" {
					value.Download = v.State
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
