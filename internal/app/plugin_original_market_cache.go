package app

import (
	"context"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"light-media/internal/plugins/artifacts"
)

// MoviePilot PluginHelper.get_plugins caches each repository for 1800 seconds;
// force bypasses that cache. Cache entries contain metadata only, never workers.
const originalMarketTTL = 30 * time.Minute

type originalMarketSourceStatus struct {
	RepositoryURL string    `json:"repo_url"`
	Success       bool      `json:"success"`
	Message       string    `json:"message,omitempty"`
	CheckedAt     time.Time `json:"checked_at"`
	Cached        bool      `json:"cached"`
}

type originalMarketResult struct {
	discoveries []artifacts.Discovery
	status      originalMarketSourceStatus
	accessedAt  time.Time
	// A successful native-index 404 identifies the source-package route. This
	// marker is not a cached aggregate: base/v2 values have independent TTLs.
	sourcePackages bool
	sourceProbeAt  time.Time
}

type originalMarketPending struct {
	done   chan struct{}
	result originalMarketResult
}

type originalMarketCache struct {
	mu       sync.Mutex
	entries  map[string]originalMarketResult
	pending  map[string]*originalMarketPending
	packages map[originalMarketPackageKey]originalMarketResult
	// Release lists have their own TTL and force/background behavior.
	releases       map[string]originalMarketResult
	releaseRefresh map[string]bool
}

func (a *App) originalMarketSource(ctx context.Context, repository string, force bool) originalMarketResult {
	cache := &a.pluginOriginalMarkets
	cache.mu.Lock()
	var sourceProbeAt time.Time
	if !force {
		if entry, ok := cache.entries[repository]; ok {
			checkedAt := entry.status.CheckedAt
			if entry.sourcePackages {
				checkedAt = entry.sourceProbeAt
			}
			if time.Since(checkedAt) < originalMarketTTL {
				entry.accessedAt = time.Now()
				cache.entries[repository] = entry
				if !entry.sourcePackages {
					cache.mu.Unlock()
					entry.status.Cached = true
					return entry
				}
				sourceProbeAt = entry.sourceProbeAt
			}
		}
	}
	if pending := cache.pending[repository]; pending != nil {
		cache.mu.Unlock()
		select {
		case <-pending.done:
			return pending.result
		case <-ctx.Done():
			return originalMarketResult{status: originalMarketSourceStatus{RepositoryURL: repository, Message: "仓库读取已取消"}}
		}
	}
	if cache.pending == nil {
		cache.pending = make(map[string]*originalMarketPending)
	}
	pending := &originalMarketPending{done: make(chan struct{})}
	cache.pending[repository] = pending
	cache.mu.Unlock()

	fetchURL := repository
	if force {
		if parsed, parseErr := url.Parse(repository); parseErr == nil {
			if parsed.RawQuery != "" {
				parsed.RawQuery += "&"
			}
			parsed.RawQuery += "_refresh=" + strconv.FormatInt(time.Now().UnixNano(), 10)
			fetchURL = parsed.String()
		}
	}
	client, err := a.originalPluginMaterialHTTPClient(30 * time.Second)
	var discoveries []artifacts.Discovery
	sourcePackages, packagesCached, packageSucceeded := false, false, false
	loadPackages := func(ctx context.Context, sourceURL string, client *http.Client) ([]artifacts.Discovery, error) {
		sourcePackages = true
		if sourceProbeAt.IsZero() {
			sourceProbeAt = time.Now()
		}
		values, cached, succeeded, e := a.originalMarketSourcePackages(ctx, repository, sourceURL, force, client)
		packagesCached, packageSucceeded = cached, succeeded
		return values, e
	}
	if err == nil {
		defer client.CloseIdleConnections()
		if !sourceProbeAt.IsZero() {
			discoveries, err = loadPackages(ctx, fetchURL, client)
		} else {
			discoveries, err = artifacts.DiscoverRepositoryWithSourcePackages(ctx, fetchURL, "", validatePluginArtifactContract, client, loadPackages)
		}
	}
	for i := range discoveries {
		discoveries[i].RepositoryURL = repository
	}
	result := originalMarketResult{discoveries: discoveries, accessedAt: time.Now(), sourcePackages: sourcePackages, sourceProbeAt: sourceProbeAt, status: originalMarketSourceStatus{RepositoryURL: repository, Success: err == nil, CheckedAt: time.Now().UTC(), Cached: sourcePackages && packagesCached}}
	if err != nil {
		// Do not expose remote bodies or request URLs contained in network errors.
		result.status.Message = "仓库索引读取失败或与当前平台不兼容，请刷新重试"
		a.log("ERROR", "plugins", "market_discovery_failed", "插件仓库元数据读取失败", "", "")
	}
	cache.mu.Lock()
	if ctx.Err() == nil && (err == nil || sourcePackages && packageSucceeded) {
		if cache.entries == nil {
			cache.entries = make(map[string]originalMarketResult)
		}
		// Match the original helper's finite cache; old sources need not remain
		// forever after the repository list is edited.
		if _, exists := cache.entries[repository]; !exists && len(cache.entries) >= 128 {
			var oldest string
			var at time.Time
			for key, entry := range cache.entries {
				if oldest == "" || entry.accessedAt.Before(at) {
					oldest, at = key, entry.accessedAt
				}
			}
			delete(cache.entries, oldest)
		}
		entry := result
		if sourcePackages {
			// Never reuse a partial/full combined package list as one cache value.
			// The marker preserves native-index precedence until force/its TTL.
			entry.discoveries = nil
		}
		cache.entries[repository] = entry
	}
	// app/core/cache.py's skip_none returns the failed fresh read to its
	// caller without touching a prior successful entry. A later ordinary read
	// may still use that entry until its original TTL expires.
	pending.result = result
	delete(cache.pending, repository)
	close(pending.done)
	cache.mu.Unlock()
	return result
}

// process_plugins_list deliberately uses the original delimiter-free id+version
// key before selecting by ID. Legal cross-ID concatenation collisions therefore
// keep only the first ranked declaration. Package precedence is applied before
// repository ranking; it is unrelated to plugin version or permission level.
func selectOriginalMarketDeclarations(higher, base []artifacts.Discovery, markets []string) []artifacts.Discovery {
	marketOrder := make(map[string]int)
	marketCount := 0
	for _, market := range markets {
		if market == "" {
			continue
		}
		if _, exists := marketOrder[market]; !exists {
			marketOrder[market] = marketCount
		}
		marketCount++
	}
	isLocal := func(source artifacts.Discovery) bool {
		return strings.HasPrefix(source.RepositoryURL, "local://")
	}
	rank := func(source artifacts.Discovery) int {
		if isLocal(source) {
			return marketCount + 1
		}
		if order, listed := marketOrder[source.RepositoryURL]; listed {
			return order
		}
		return marketCount
	}
	key := originalMarketDeclarationKey
	higherKeys := make(map[string]bool, len(higher))
	sources := make([]artifacts.Discovery, 0, len(higher)+len(base))
	for _, source := range higher {
		higherKeys[key(source)] = true
		sources = append(sources, source)
	}
	for _, source := range base {
		if !higherKeys[key(source)] {
			sources = append(sources, source)
		}
	}
	sort.SliceStable(sources, func(i, j int) bool { return rank(sources[i]) < rank(sources[j]) })
	deduplicated := make([]artifacts.Discovery, 0, len(sources))
	keyPositions := make(map[string]int, len(sources))
	for _, source := range sources {
		if position, exists := keyPositions[key(source)]; exists {
			if isLocal(deduplicated[position]) && !isLocal(source) {
				deduplicated[position] = source
			}
			continue
		}
		keyPositions[key(source)] = len(deduplicated)
		deduplicated = append(deduplicated, source)
	}
	sort.SliceStable(deduplicated, func(i, j int) bool { return rank(deduplicated[i]) < rank(deduplicated[j]) })
	selected := make([]artifacts.Discovery, 0, len(deduplicated))
	positions := make(map[string]int, len(deduplicated))
	for _, source := range deduplicated {
		if position, exists := positions[source.PluginID]; exists {
			previous := selected[position]
			if originalMarketVersionGreater(source, previous) ||
				(originalMarketVersionEqual(source, previous) && isLocal(previous) && !isLocal(source)) {
				// Python dictionary value replacement preserves first insertion order.
				selected[position] = source
			}
			continue
		}
		positions[source.PluginID] = len(selected)
		selected = append(selected, source)
	}
	return selected
}

// PluginHelper.get_local_plugin_candidates selects by ID directly, before the
// outer market process_plugins_list. Do not apply its concat-key quirk here:
// both legal local identities must remain available to exact local lookup/watch.
func selectOriginalLocalMarketDeclarations(sources []artifacts.Discovery) []artifacts.Discovery {
	selected := make([]artifacts.Discovery, 0, len(sources))
	positions := make(map[string]int)
	for _, source := range sources {
		if n, exists := positions[source.PluginID]; exists {
			if compareOriginalPluginVersion(source.CurrentVersion, selected[n].CurrentVersion) > 0 {
				selected[n] = source
			}
			continue
		}
		positions[source.PluginID] = len(selected)
		selected = append(selected, source)
	}
	return selected
}

// StringUtils.compare_version is deliberately not SemVer: separators are dot
// and hyphen, missing parts are zero, and stable/rc/beta/alpha have -1/-2/-3/-4
// weights. Unknown (including build-metadata) parts are -5. Python metadata
// uses the fixed source Unicode digit/strip semantics; native Go artifact
// manifest validation remains strict and is not changed by this comparator.
func compareOriginalPluginVersion(left, right string) int {
	parts := func(value string) ([]*big.Int, bool) {
		if value == "" {
			return nil, false
		}
		value = strings.TrimLeft(strings.TrimFunc(value, originalPluginVersionSpace), "vV")
		raw := strings.Split(strings.ReplaceAll(value, "-", "."), ".")
		out := make([]*big.Int, 0, len(raw))
		for _, part := range raw {
			ascii, digits, valid := originalPluginVersionDecimal(part)
			if !valid {
				return nil, false
			}
			if digits {
				n, _ := new(big.Int).SetString(ascii, 10)
				out = append(out, n)
				continue
			}
			weight := int64(-5)
			switch part {
			case "stable":
				weight = -1
			case "rc":
				weight = -2
			case "beta":
				weight = -3
			case "alpha":
				weight = -4
			}
			out = append(out, big.NewInt(weight))
		}
		return out, true
	}
	a, okA := parts(left)
	b, okB := parts(right)
	if !okA || !okB {
		return 0
	}
	zero := new(big.Int)
	for i := 0; i < max(len(a), len(b)); i++ {
		av, bv := zero, zero
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if result := av.Cmp(bv); result != 0 {
			return result
		}
	}
	return 0
}
