package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"light-media/internal/catalog"
)

type subscriptionDisplayCoverage struct {
	library, reserved, pending, excluded map[int]bool
	dates                                map[int]string
	known                                []int
}
type subscriptionDisplay struct {
	subscriptions []Subscription
	coverage      map[string]*subscriptionDisplayCoverage
	words         map[string]string
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
	out := &subscriptionDisplay{coverage: map[string]*subscriptionDisplayCoverage{}, words: map[string]string{}}
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM records WHERE kind='subscription' AND json_extract(payload,'$.state') IS NOT 'cancelled' ORDER BY updated_at DESC,id LIMIT 500`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			rows.Close()
			return nil, err
		}
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
 UNION ALL SELECT s.key,r.episode,'reservation',COALESCE(json_extract(i.payload,'$.state'),'') FROM selected s JOIN reservations r ON r.media_key=s.media AND r.season=s.season LEFT JOIN records i ON i.kind='intent' AND i.id=r.intent_id`, string(pairJSON))
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
		data := out.coverage[key]
		if kind == "library" {
			data.library[ep] = true
		} else {
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
	rows, err = tx.QueryContext(ctx, selected+`SELECT s.key,e.id,e.payload FROM selected s JOIN records e ON e.kind='exclusion' AND e.id>=s.key||':' AND e.id<s.key||';'`, string(pairJSON))
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
	rows, err = tx.QueryContext(ctx, `SELECT id,payload FROM records WHERE kind='cache' AND id IN (SELECT value FROM json_each(?))`, string(rawKeys))
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
			Data catalog.Season `json:"data"`
		}
		if json.Unmarshal(b, &cache) != nil {
			continue
		} // Same best-effort cache semantics as the single-subscription path.
		for _, target := range cacheKeys[key] {
			data := out.coverage[target]
			for _, ep := range cache.Data.Episodes {
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
	return out, nil
}
