package app

import (
	"context"
	"time"
)

// Fixed plugin.py:428-482 returns a valid Release cache before scheduling a
// force refresh. The Go repository declares its executable releases in the
// same index as current metadata; keep the two caches independent nevertheless.
func (a *App) originalPluginReleaseSource(ctx context.Context, repository string, force bool) originalMarketResult {
	cache := &a.pluginOriginalMarkets
	cache.mu.Lock()
	entry, cached := cache.releases[repository]
	cached = cached && time.Since(entry.status.CheckedAt) < originalMarketTTL
	if cached {
		entry.accessedAt = time.Now()
		cache.releases[repository] = entry
	}
	cache.mu.Unlock()
	if !cached {
		entry = a.originalMarketSource(ctx, repository, true)
		if entry.status.Success {
			a.saveOriginalPluginReleaseSource(repository, entry)
		}
	} else if force {
		a.refreshOriginalPluginReleases(repository)
	}
	return entry
}

func (a *App) saveOriginalPluginReleaseSource(repository string, result originalMarketResult) {
	cache := &a.pluginOriginalMarkets
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.releases == nil {
		cache.releases = make(map[string]originalMarketResult)
	}
	if _, exists := cache.releases[repository]; !exists && len(cache.releases) >= 32 {
		var oldest string
		var at time.Time
		for key, entry := range cache.releases {
			if oldest == "" || entry.accessedAt.Before(at) {
				oldest, at = key, entry.accessedAt
			}
		}
		delete(cache.releases, oldest)
	}
	// Market entries may have been read earlier than the separate Release
	// query. Its TTL starts when this successful Release list was obtained.
	result.status.CheckedAt, result.accessedAt = time.Now().UTC(), time.Now()
	cache.releases[repository] = result
}

func (a *App) refreshOriginalPluginReleases(repository string) {
	cache := &a.pluginOriginalMarkets
	cache.mu.Lock()
	if cache.releaseRefresh[repository] || !a.admitOriginalBackground() {
		cache.mu.Unlock()
		return
	}
	if cache.releaseRefresh == nil {
		cache.releaseRefresh = make(map[string]bool)
	}
	cache.releaseRefresh[repository] = true
	cache.mu.Unlock()
	go func() {
		defer a.wg.Done()
		defer func() {
			cache.mu.Lock()
			delete(cache.releaseRefresh, repository)
			cache.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result := a.originalMarketSource(ctx, repository, true)
		if result.status.Success {
			a.saveOriginalPluginReleaseSource(repository, result)
		}
	}()
}
