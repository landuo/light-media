package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"sort"
	"strconv"
	"sync"
	"time"

	"light-media/internal/releasemeta"
	"light-media/internal/releasemeta/legacyanime"
)

// Ordinary trusted host settings, immutable for one App lifetime. Clock seams
// let source-oracle tests adjust wall and monotonic time independently.
type HostMediaRecognitionConfiguration struct {
	Locale          string
	Source          string
	ScrapeSource    string
	MetaCacheExpire *big.Int // hours; zero selects CONF's memory-mode default
	WallClock       func() float64
	MonotonicClock  func() float64
	Sleep           func(context.Context, float64) error
}

func HostMediaRecognitionEnvironment(lookup func(string) (string, bool)) (*HostMediaRecognitionConfiguration, error) {
	out := &HostMediaRecognitionConfiguration{Locale: "zh", Source: "themoviedb", ScrapeSource: "themoviedb", MetaCacheExpire: new(big.Int)}
	if raw, ok := lookup("TMDB_LOCALE"); ok {
		out.Locale = raw
	}
	if raw, ok := lookup("RECOGNIZE_SOURCE"); ok {
		out.Source = raw
	}
	if raw, ok := lookup("SCRAP_SOURCE"); ok {
		out.ScrapeSource = raw
	}
	if raw, ok := lookup("META_CACHE_EXPIRE"); ok {
		number, err := legacyanime.Integer(legacyanime.Strip(raw))
		if err != nil {
			return nil, errors.New("invalid META_CACHE_EXPIRE")
		}
		out.MetaCacheExpire = number
	}
	return out, nil
}
func cloneHostMediaRecognitionConfiguration(value *HostMediaRecognitionConfiguration) *HostMediaRecognitionConfiguration {
	if value == nil {
		return nil
	}
	out := *value
	if out.ScrapeSource == "" {
		out.ScrapeSource = "themoviedb"
	}
	if value.MetaCacheExpire == nil {
		out.MetaCacheExpire = new(big.Int)
	} else {
		out.MetaCacheExpire = new(big.Int).Set(value.MetaCacheExpire)
	}
	return &out
}

var originalHostMediaMonotonicEpoch = time.Now()

func (a *App) originalHostMediaWallTime() float64 {
	if clock := a.originalHostMediaRecognition.WallClock; clock != nil {
		return clock()
	}
	return float64(time.Now().UnixNano()) / 1e9
}
func (a *App) originalHostMediaMonotonicTime() float64 {
	if clock := a.originalHostMediaRecognition.MonotonicClock; clock != nil {
		return clock()
	}
	return time.Since(originalHostMediaMonotonicEpoch).Seconds()
}
func (a *App) originalHostMediaCacheLimit() int {
	if a.bigMemoryMode {
		return 1024
	}
	return 256
}

type originalHostMediaCacheEntry struct {
	Value     map[string]any `json:"value"`
	ExpiresAt float64        `json:"expires_at"` // wall time persisted by TmdbCache
	used      uint64
	order     *originalHostMediaCacheOrder
}
type originalHostMediaCacheOrder struct {
	key     string
	expires float64
	removed bool
}
type originalHostMediaRecognitionCache struct {
	mu    sync.Mutex
	items map[string]*originalHostMediaCacheEntry
	order []*originalHostMediaCacheOrder // cachetools7.1.4 TLRU expiration heap
	clock uint64
	dirty bool
}

func originalHostMediaMeta(value map[string]any) *releasemeta.MetaObject {
	meta := releasemeta.EmptyMetaObject()
	for key, item := range value {
		meta.Set(key, item)
	}
	if meta.Name() == "" && sourceText(value["name"]) != "" {
		_ = meta.SetName(value["name"])
	}
	return meta
}
func originalHostMediaCacheKey(meta *releasemeta.MetaObject, locale string) string {
	kind := meta.Text("type")
	if kind == "" {
		kind = "未知"
	}
	identity := meta.Get("tmdbid")
	if !sourceSubscriptionTruth(identity) {
		identity = meta.Name()
	}
	return "[" + kind + "][" + locale + "]" + originalCatalogIdentityString(identity) + "-" + originalCatalogIdentityString(meta.Get("year")) + "-" + originalCatalogIdentityString(meta.Get("begin_season"))
}

// Python heapq's equal-expiration choice is significant: TLRU.items iterates
// this heap, and pinned TmdbCache persists that order before bounded restore.
func (c *originalHostMediaRecognitionCache) heapDown(start, pos int) {
	item := c.order[pos]
	for pos > start {
		parent := (pos - 1) >> 1
		if !(item.expires < c.order[parent].expires) {
			break
		}
		c.order[pos] = c.order[parent]
		pos = parent
	}
	c.order[pos] = item
}
func (c *originalHostMediaRecognitionCache) heapUp(pos int) {
	end, start := len(c.order), pos
	item := c.order[pos]
	child := 2*pos + 1
	for child < end {
		right := child + 1
		if right < end && !(c.order[child].expires < c.order[right].expires) {
			child = right
		}
		c.order[pos] = c.order[child]
		pos = child
		child = 2*pos + 1
	}
	c.order[pos] = item
	c.heapDown(start, pos)
}
func (c *originalHostMediaRecognitionCache) heapPop() *originalHostMediaCacheOrder {
	last := c.order[len(c.order)-1]
	c.order = c.order[:len(c.order)-1]
	if len(c.order) == 0 {
		return last
	}
	first := c.order[0]
	c.order[0] = last
	c.heapUp(0)
	return first
}
func (c *originalHostMediaRecognitionCache) expire(now float64) {
	if len(c.order) > len(c.items)*2 {
		order := []*originalHostMediaCacheOrder{}
		for _, item := range c.order {
			if !item.removed {
				order = append(order, item)
			}
		}
		c.order = order
		for i := len(order)/2 - 1; i >= 0; i-- {
			c.heapUp(i)
		}
	}
	for len(c.order) > 0 && (c.order[0].removed || !(now < c.order[0].expires)) {
		item := c.heapPop()
		if !item.removed {
			delete(c.items, item.key)
			c.dirty = true
		}
	}
}
func (c *originalHostMediaRecognitionCache) set(key string, entry *originalHostMediaCacheEntry, deadline, now float64, limit int) {
	if deadline <= now {
		if old := c.items[key]; old != nil {
			old.order.removed = true
			delete(c.items, key)
		}
		return
	}
	c.expire(now)
	if old := c.items[key]; old != nil {
		old.order.removed = true
	} else {
		for len(c.items) >= limit {
			oldest := ""
			var used uint64
			for id, value := range c.items {
				if oldest == "" || value.used < used {
					oldest, used = id, value.used
				}
			}
			c.items[oldest].order.removed = true
			delete(c.items, oldest)
			c.dirty = true
		}
	}
	c.clock++
	entry.used = c.clock
	entry.order = &originalHostMediaCacheOrder{key: key, expires: deadline}
	c.items[key] = entry
	c.order = append(c.order, entry.order)
	c.heapDown(0, len(c.order)-1)
}

func originalHostMediaCacheNumber(value any) (float64, bool) {
	switch value := value.(type) {
	case json.Number:
		n, err := strconv.ParseFloat(string(value), 64)
		return n, err == nil
	case bool:
		if value {
			return 1, true
		}
		return 0, true
	case float64:
		return value, true
	}
	return 0, false
}

func (a *App) originalHostMediaCacheLoadLocked(ctx context.Context) error {
	cache := &a.originalHostMediaCache
	if cache.items != nil {
		return nil
	}
	cache.items = map[string]*originalHostMediaCacheEntry{}
	var raw []byte
	err := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='plugin_host_tmdb_cache' AND id='main'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	value, decodeErr := decodeSourceJSON(raw)
	if err != nil || decodeErr != nil || sourceMap(value) == nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		a.log("ERROR", "metadata", "recognize_cache_restore_failed", "恢复媒体识别缓存失败", "", "")
		return nil
	}
	payload := sourceMap(value)
	items := sourceMap(payload["items"])
	version, _ := originalHostMediaCacheNumber(payload["version"])
	legacy := version != 1 || items == nil
	if legacy {
		items = payload
		cache.dirty = true
	}
	order := []string{}
	seen := map[string]bool{}
	for _, row := range sourceRows(payload["order"]) {
		if key, ok := row.(string); ok && !seen[key] {
			if _, ok := items[key]; ok {
				order = append(order, key)
				seen[key] = true
			}
		}
	}
	// JSON objects are otherwise unordered; old target JSON is restored in its
	// canonical encoding order. All new snapshots carry the source heap order.
	rest := []string{}
	for key := range items {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	order = append(order, rest...)
	wall, mono := a.originalHostMediaWallTime(), a.originalHostMediaMonotonicTime()
	for _, key := range order {
		item := sourceMap(items[key])
		if item == nil {
			cache.dirty = true
			continue
		}
		data := sourceMap(item["value"])
		expires, valid := originalHostMediaCacheNumber(item["expires_at"])
		if legacy {
			data = item
			ttl, err := a.originalHostMediaCacheTTL()
			if err != nil {
				return err
			}
			expires, valid = wall+ttl, true
		}
		if data == nil || !valid || math.IsNaN(expires) || math.IsInf(expires, 0) || expires <= wall {
			cache.dirty = true
			continue
		}
		cache.set(key, &originalHostMediaCacheEntry{Value: data, ExpiresAt: expires}, mono+math.Ceil(expires-wall), mono, a.originalHostMediaCacheLimit())
	}
	return nil
}
func (a *App) originalHostMediaCacheGet(ctx context.Context, key string) (map[string]any, error) {
	cache := &a.originalHostMediaCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if err := a.originalHostMediaCacheLoadLocked(ctx); err != nil {
		return nil, err
	}
	entry := cache.items[key]
	if entry == nil {
		return nil, nil
	}
	if !(a.originalHostMediaMonotonicTime() < entry.order.expires) {
		if entry.ExpiresAt != 0 {
			entry.ExpiresAt = 0
			cache.dirty = true
		}
		return nil, nil
	}
	cache.clock++
	entry.used = cache.clock
	return entry.Value, nil
}
func (a *App) originalHostMediaCacheTTL() (float64, error) {
	hours := new(big.Int).Set(a.originalHostMediaRecognition.MetaCacheExpire)
	if hours.Sign() == 0 {
		hours.SetInt64(24)
		if a.bigMemoryMode {
			hours.SetInt64(72)
		}
	}
	seconds, _ := new(big.Float).SetInt(new(big.Int).Mul(hours, big.NewInt(3600))).Float64()
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return 0, errors.New("TMDB cache expiration exceeds numeric timer")
	}
	return seconds, nil
}
func (a *App) originalHostMediaCachePut(ctx context.Context, key string, info map[string]any) error {
	if info == nil {
		return nil
	}
	value := map[string]any{"id": json.Number("0")}
	if len(info) > 0 {
		titleKey, dateKey := "title", "release_date"
		if info["media_type"] == "电视剧" {
			titleKey, dateKey = "name", "first_air_date"
		}
		date := info[dateKey]
		if text, ok := date.(string); ok && text != "" {
			date = text[:min(4, len(text))]
		}
		value = map[string]any{"id": info["id"], "type": info["media_type"], "year": date, "title": info[titleKey], "poster_path": info["poster_path"], "backdrop_path": info["backdrop_path"]}
	}
	seconds, err := a.originalHostMediaCacheTTL()
	if err != nil {
		return err
	}
	cache := &a.originalHostMediaCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if err := a.originalHostMediaCacheLoadLocked(ctx); err != nil {
		return err
	}
	mono := a.originalHostMediaMonotonicTime()
	cache.set(key, &originalHostMediaCacheEntry{Value: value, ExpiresAt: a.originalHostMediaWallTime() + seconds}, mono+seconds, mono, a.originalHostMediaCacheLimit())
	cache.dirty = true
	return nil
}

// Source saves on destruction. App.Close is the Go lifetime boundary; ordered
// JSON in the existing database replaces Python pickle without Python runtime.
func (a *App) saveOriginalHostMediaCache(ctx context.Context) error {
	cache := &a.originalHostMediaCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.items == nil {
		return nil
	}
	wall, mono := a.originalHostMediaWallTime(), a.originalHostMediaMonotonicTime()
	items := map[string]*originalHostMediaCacheEntry{}
	order := []string{}
	for _, item := range cache.order {
		if item.removed || !(mono < item.expires) {
			continue
		}
		entry := cache.items[item.key]
		if entry == nil {
			continue
		}
		if entry.ExpiresAt == 0 {
			ttl, err := a.originalHostMediaCacheTTL()
			if err != nil {
				return err
			}
			entry.ExpiresAt = wall + ttl
			cache.dirty = true
		}
		if entry.ExpiresAt <= wall || !sourceSubscriptionTruth(entry.Value["id"]) {
			continue
		}
		items[item.key] = entry
		order = append(order, item.key)
	}
	for _, entry := range cache.items {
		if !(mono < entry.order.expires) {
			cache.dirty = true
		}
	}
	if !cache.dirty {
		return nil
	}
	var err error
	if len(items) == 0 {
		_, err = a.Store.DB.ExecContext(ctx, `DELETE FROM records WHERE kind='plugin_host_tmdb_cache' AND id='main'`)
	} else {
		err = a.Store.Put(ctx, "plugin_host_tmdb_cache", "main", struct {
			Version int                                     `json:"version"`
			Items   map[string]*originalHostMediaCacheEntry `json:"items"`
			Order   []string                                `json:"order"`
		}{1, items, order})
	}
	if err == nil {
		cache.dirty = false
	}
	return err
}
