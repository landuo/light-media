package app

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"light-media/internal/plugins/artifacts"
)

type originalMarketPackageKey struct {
	repository string
	version    string
}

// The source helper caches async_get_plugins(repo, package_version), not a
// combined repository result. Each completed successful package updates only
// its own entry. A failed force read is absent now but leaves its old entry for
// a later ordinary read. Empty dictionaries (including 404) are valid values.
func (a *App) originalMarketSourcePackages(ctx context.Context, repository, sourceURL string, force bool, client *http.Client) ([]artifacts.Discovery, bool, bool, error) {
	type packageResult struct {
		values []artifacts.Discovery
		cached bool
		err    error
	}
	versions := [...]string{"v2", ""}
	var results [2]packageResult
	var workers sync.WaitGroup
	for i, version := range versions {
		workers.Add(1)
		go func() {
			defer workers.Done()
			results[i].values, results[i].cached, results[i].err = a.originalMarketSourcePackage(ctx, repository, sourceURL, version, force, client)
		}()
	}
	workers.Wait()
	values := []artifacts.Discovery{}
	allCached, anySuccess := true, false
	var failures []error
	for _, result := range results {
		allCached = allCached && result.cached
		if result.err != nil {
			failures = append(failures, result.err)
			continue
		}
		anySuccess = true
		values = append(values, result.values...)
	}
	return values, allCached, anySuccess, errors.Join(failures...)
}

func (a *App) originalMarketSourcePackage(ctx context.Context, repository, sourceURL, version string, force bool, client *http.Client) ([]artifacts.Discovery, bool, error) {
	cache := &a.pluginOriginalMarkets
	key := originalMarketPackageKey{repository: repository, version: version}
	cache.mu.Lock()
	if !force {
		if entry, ok := cache.packages[key]; ok && time.Since(entry.status.CheckedAt) < originalMarketTTL {
			entry.accessedAt = time.Now()
			cache.packages[key] = entry
			cache.mu.Unlock()
			return append([]artifacts.Discovery{}, entry.discoveries...), true, nil
		}
	}
	cache.mu.Unlock()
	values, err := artifacts.DiscoverGitHubSourcePackage(ctx, sourceURL, version, client)
	if err != nil {
		return nil, false, err
	}
	if err = ctx.Err(); err != nil {
		return nil, false, err
	}
	for i := range values {
		values[i].RepositoryURL = repository
	}
	entry := originalMarketResult{discoveries: append([]artifacts.Discovery{}, values...), accessedAt: time.Now(), status: originalMarketSourceStatus{Success: true, CheckedAt: time.Now().UTC()}}
	cache.mu.Lock()
	if err = ctx.Err(); err != nil {
		cache.mu.Unlock()
		return nil, false, err
	}
	if cache.packages == nil {
		cache.packages = make(map[originalMarketPackageKey]originalMarketResult)
	}
	// cachetools.TLRUCache expires entries before capacity eviction. A recently
	// touched but expired band must not evict an older, still-valid sibling.
	for candidate, value := range cache.packages {
		if time.Since(value.status.CheckedAt) >= originalMarketTTL {
			delete(cache.packages, candidate)
		}
	}
	if _, exists := cache.packages[key]; !exists && len(cache.packages) >= 128 {
		var oldest originalMarketPackageKey
		var at time.Time
		for candidate, value := range cache.packages {
			if at.IsZero() || value.accessedAt.Before(at) {
				oldest, at = candidate, value.accessedAt
			}
		}
		delete(cache.packages, oldest)
	}
	cache.packages[key] = entry
	cache.mu.Unlock()
	return values, false, nil
}
