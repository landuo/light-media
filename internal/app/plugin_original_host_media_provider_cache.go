package app

import (
	"context"
	"encoding/json"

	"light-media/internal/catalog"
)

type originalHostMediaFreshContextKey struct{}

func cloneOriginalHostMediaProviderValue(value any) (any, error) {
	switch value := value.(type) {
	case json.RawMessage:
		return append(json.RawMessage(nil), value...), nil
	case catalog.SourceRecognitionResponse:
		return catalog.SourceRecognitionResponse{JSON: append(json.RawMessage(nil), value.JSON...), Headers: value.Headers.Clone()}, nil
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return decodeSourceJSON(raw)
	}
}

// Matches core/cache.cached: read and write lock independently, with no
// in-flight coalescing; fresh bypasses reads and still replaces cached results.
// This is a finite internal host helper, not an arbitrary worker cache/RPC.
func (a *App) originalHostMediaProviderCached(ctx context.Context, source, region, key string, skipEmpty bool, load func() (any, error)) (any, error) {
	limit := 256
	switch source {
	case "themoviedb":
		if a.bigMemoryMode {
			limit = 1024
		}
	case "douban", "bangumi", "anilist":
		if a.bigMemoryMode {
			limit = 512
		}
	default:
		return nil, bad("INVALID_RECOGNIZE_SOURCE", "媒体数据源无效")
	}
	switch region {
	case "request", "get", "post", "detail", "search", "credits":
	default:
		return nil, bad("INVALID_RECOGNIZE_CACHE_REGION", "媒体缓存区无效")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, _ := a.originalHostMediaProviderCaches.LoadOrStore(source+":"+region, &originalHostMediaRecognitionCache{items: map[string]*originalHostMediaCacheEntry{}})
	cache := value.(*originalHostMediaRecognitionCache)
	if ctx.Value(originalHostMediaFreshContextKey{}) != true {
		cache.mu.Lock()
		entry := cache.items[key]
		if entry != nil && a.originalHostMediaMonotonicTime() < entry.order.expires {
			cache.clock++
			entry.used = cache.clock
			result := entry.Value["value"]
			cache.mu.Unlock()
			return cloneOriginalHostMediaProviderValue(result)
		}
		cache.mu.Unlock()
	}
	result, err := load()
	if err != nil || result == nil {
		return result, err
	}
	check := result
	if raw, ok := result.(json.RawMessage); ok {
		if len(raw) == 0 {
			return result, nil
		}
		decoded, err := decodeSourceJSON(raw)
		if err != nil {
			return result, nil
		}
		check = decoded
	}
	if check == nil || skipEmpty && !sourceSubscriptionTruth(check) {
		return result, nil
	}
	stored, err := cloneOriginalHostMediaProviderValue(result)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return result, nil
	}
	ttl, err := a.originalHostMediaCacheTTL()
	if err != nil {
		return nil, err
	}
	cache.mu.Lock()
	now := a.originalHostMediaMonotonicTime()
	cache.set(key, &originalHostMediaCacheEntry{Value: map[string]any{"value": stored}}, now+ttl, now, limit)
	cache.mu.Unlock()
	return result, nil
}

func (a *App) originalHostMediaEffectiveLocale() string {
	if locale := a.originalHostMediaRecognition.Locale; locale != "" {
		return locale
	}
	return "en-US"
}
