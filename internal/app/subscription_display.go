package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type subscriptionDisplayCoverage struct {
	library, reserved, pending, excluded map[int]bool
	dates                                map[int]string
	known                                []int
}
type subscriptionDisplay struct {
	version       int64
	subscriptions []Subscription
	coverage      map[string]*subscriptionDisplayCoverage
	words         map[string]string
}

// A heart button only needs membership and season, not every card's episode coverage.
func (a *App) matchingSubscriptions(ctx context.Context, kind string, id int64) ([]map[string]any, error) {
	if !validMedia(kind, id) {
		return nil, bad("INVALID_MEDIA", "请选择明确的电影或剧集身份")
	}
	rows, err := a.Store.reader().QueryContext(ctx, `SELECT uid,COALESCE(json_extract(payload,'$.season'),0),json_extract(payload,'$.state')
		FROM subscribe WHERE (json_extract(payload,'$.media.media_type')||':'||json_extract(payload,'$.media.id'))=?
		AND json_extract(payload,'$.state') NOT IN ('cancelled','canceled') ORDER BY updated_at DESC,uid`, kind+":"+strconv.FormatInt(id, 10))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var uid, state string
		var season int
		if err := rows.Scan(&uid, &season, &state); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": uid, "season": season, "state": state, "media": map[string]any{"id": id, "media_type": kind}})
	}
	return items, rows.Err()
}

// The version and all display facts are read from the same WAL snapshot.
func subscriptionDisplayVersionSchema() string {
	var sql strings.Builder
	sql.WriteString("CREATE TABLE IF NOT EXISTS subscription_display_version(id INTEGER PRIMARY KEY CHECK(id=1),version INTEGER NOT NULL); INSERT OR IGNORE INTO subscription_display_version VALUES(1,0);")
	for _, table := range []string{"subscribe", "asset_slots", "reservations", "downloadhistory", "media_exclusion", "cache_entry", "subscription_words"} {
		for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
			condition := ""
			if table == "downloadhistory" {
				// Only intents claimed by a reservation affect the displayed coverage.
				ref := "NEW.uid"
				if event == "DELETE" {
					ref = "OLD.uid"
				}
				condition = " WHEN EXISTS(SELECT 1 FROM reservations WHERE intent_id=" + ref + ")"
				if event == "UPDATE" {
					condition = " WHEN (NEW.uid IS NOT OLD.uid OR json_extract(NEW.payload,'$.state') IS NOT json_extract(OLD.payload,'$.state')) AND EXISTS(SELECT 1 FROM reservations WHERE intent_id IN (OLD.uid,NEW.uid))"
				}
				// Replace the original unconditional triggers on existing installations.
				fmt.Fprintf(&sql, "DROP TRIGGER IF EXISTS display_downloadhistory_%s;", event)
			}
			if table == "cache_entry" {
				switch event {
				case "INSERT":
					condition = " WHEN NEW.id GLOB 'season:*'"
				case "DELETE":
					condition = " WHEN OLD.id GLOB 'season:*'"
				default:
					condition = " WHEN NEW.id GLOB 'season:*' OR OLD.id GLOB 'season:*'"
				}
			}
			name := table
			if table == "downloadhistory" {
				name = "reserved_intent"
			}
			fmt.Fprintf(&sql, "CREATE TRIGGER IF NOT EXISTS display_%s_%s AFTER %s ON %s%s BEGIN UPDATE subscription_display_version SET version=version+1 WHERE id=1; END;", name, event, event, table, condition)
		}
	}
	return sql.String()
}
func subscriptionDisplayKey(s Subscription) string {
	return fmt.Sprintf("%s:%d", mediaKey(s.Media), s.Season)
}

// Batch only the visible subscriptions. All facts come from one WAL snapshot,
// including the reservation-to-asset transition and encrypted editor words.
func (a *App) loadSubscriptionDisplay(ctx context.Context) (*subscriptionDisplay, error) {
	tx, err := a.Store.reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var version int64
	if err = tx.QueryRowContext(ctx, "SELECT version FROM subscription_display_version WHERE id=1").Scan(&version); err != nil {
		return nil, err
	}
	if snapshot := a.Store.subscriptionDisplayCache.Load(); snapshot != nil && snapshot.version == version {
		return snapshot, nil
	}
	estimatedBytes := 0
	out := &subscriptionDisplay{version: version, coverage: map[string]*subscriptionDisplayCoverage{}, words: map[string]string{}}
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM subscribe WHERE json_extract(payload,'$.state') IS NOT 'cancelled' ORDER BY updated_at DESC,uid LIMIT 500`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			rows.Close()
			return nil, err
		}
		estimatedBytes += len(b)
		var sub Subscription
		if err = json.Unmarshal(b, &sub); err != nil {
			rows.Close()
			return nil, err
		}
		out.subscriptions = append(out.subscriptions, sub)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(out.subscriptions) == 0 {
		return out, nil
	}
	pairs := []map[string]any{}
	ids := []string{}
	cacheKeys := map[string][]string{}
	for _, sub := range out.subscriptions {
		ids = append(ids, sub.ID)
		key := subscriptionDisplayKey(sub)
		if out.coverage[key] != nil {
			continue
		}
		out.coverage[key] = &subscriptionDisplayCoverage{library: map[int]bool{}, reserved: map[int]bool{}, pending: map[int]bool{}, excluded: map[int]bool{}, dates: map[int]string{}, known: []int{}}
		pairs = append(pairs, map[string]any{"media": mediaKey(sub.Media), "season": sub.Season, "key": key})
		if sub.Media.MediaType == "tv" && !providerNativeMedia(sub.Media) {
			cacheKey := fmt.Sprintf("season:%d:%d", sub.Media.ID, sub.Season)
			cacheKeys[cacheKey] = append(cacheKeys[cacheKey], key)
		}
	}
	pairJSON, _ := json.Marshal(pairs)
	const selected = `WITH selected AS (SELECT json_extract(value,'$.media') AS media,json_extract(value,'$.season') AS season,json_extract(value,'$.key') AS key FROM json_each(?)) `
	rows, err = tx.QueryContext(ctx, selected+`
 SELECT s.key,a.episode,'library','' FROM selected s JOIN asset_slots a ON a.media_key=s.media AND a.season=s.season
 UNION ALL SELECT s.key,r.episode,'reservation',COALESCE(json_extract(i.payload,'$.state'),'') FROM selected s JOIN reservations r ON r.media_key=s.media AND r.season=s.season LEFT JOIN downloadhistory i ON i.uid=r.intent_id`, string(pairJSON))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key, kind, state string
		var ep int
		if err = rows.Scan(&key, &ep, &kind, &state); err != nil {
			rows.Close()
			return nil, err
		}
		estimatedBytes += 64
		data := out.coverage[key]
		if kind == "library" {
			data.library[ep] = true
		} else {
			// A reservation whose intent is gone or terminal no longer means an
			// in-flight acquisition; counting it kept the subscribe at "目标已满足"
			// while nothing was downloading.
			// MoviePilot decides a wanted episode from the library, never from an
			// in-flight download: a finished-but-unorganized download (downloaded)
			// still leaves the episode wanted. Only an actually moving transfer
			// (planned/accepted/downloading) holds the slot.
			if state != "planned" && state != "accepted" && state != "downloading" && state != "reconcile_pending" {
				continue
			}
			data.reserved[ep] = true
			data.pending[ep] = state == "downloaded"
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Range bounds use the records primary key instead of one LIKE scan per subscription.
	rows, err = tx.QueryContext(ctx, selected+`SELECT s.key,e.id,e.payload FROM selected s JOIN media_exclusion e ON e.id>=s.key||':' AND e.id<s.key||';'`, string(pairJSON))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key, id string
		var b []byte
		if err = rows.Scan(&key, &id, &b); err != nil {
			rows.Close()
			return nil, err
		}
		estimatedBytes += len(b) + len(id) + 64
		var rule struct {
			Blocked bool `json:"blocked"`
		}
		if err = json.Unmarshal(b, &rule); err != nil {
			rows.Close()
			return nil, err
		}
		ep, e := strconv.Atoi(strings.TrimPrefix(id, key+":"))
		if e == nil && rule.Blocked {
			out.coverage[key].excluded[ep] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	keys := []string{}
	for key := range cacheKeys {
		keys = append(keys, key)
	}
	rawKeys, _ := json.Marshal(keys)
	rows, err = tx.QueryContext(ctx, `SELECT id,payload FROM cache_entry WHERE id IN (SELECT value FROM json_each(?))`, string(rawKeys))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key string
		var b []byte
		if err = rows.Scan(&key, &b); err != nil {
			rows.Close()
			return nil, err
		}
		var cache struct {
			Data struct {
				Episodes []struct {
					EpisodeNumber int    `json:"episode_number"`
					AirDate       string `json:"air_date"`
				} `json:"episodes"`
			} `json:"data"`
		}
		if json.Unmarshal(b, &cache) != nil {
			continue
		} // Same best-effort cache semantics as the single-subscription path.
		for _, target := range cacheKeys[key] {
			data := out.coverage[target]
			// Only episode numbers and air dates survive decoding the provider payload.
			estimatedBytes += len(cache.Data.Episodes) * 96
			for _, ep := range cache.Data.Episodes {
				estimatedBytes += len(ep.AirDate)
				data.known = append(data.known, ep.EpisodeNumber)
				data.dates[ep.EpisodeNumber] = ep.AirDate
			}
			sort.Ints(data.known)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rawIDs, _ := json.Marshal(ids)
	rows, err = tx.QueryContext(ctx, `SELECT subscription_id,sealed FROM subscription_words WHERE subscription_id IN (SELECT value FROM json_each(?))`, string(rawIDs))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key string
		var sealed []byte
		if err = rows.Scan(&key, &sealed); err != nil {
			rows.Close()
			return nil, err
		}
		raw, e := a.pluginStore.unseal("subscription_words", "system", key, sealed)
		if e != nil {
			rows.Close()
			return nil, e
		}
		estimatedBytes += len(raw)
		var text string
		if json.Unmarshal(raw, &text) != nil {
			rows.Close()
			return nil, bad("INVALID_SUBSCRIPTION_WORDS", "订阅识别词存储无法读取")
		}
		out.words[key] = text
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Keep only one modest working set on a memory-constrained NAS.
	if estimatedBytes <= 1<<20 {
		a.Store.subscriptionDisplayCache.Store(out)
	}
	return out, nil
}
