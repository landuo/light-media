package app

import (
	"context"
	"math/big"
	"net/url"
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
}

type originalMarketPending struct {
	done   chan struct{}
	result originalMarketResult
}

type originalMarketCache struct {
	mu      sync.Mutex
	entries map[string]originalMarketResult
	pending map[string]*originalMarketPending
	// Release lists have their own TTL and force/background behavior.
	releases       map[string]originalMarketResult
	releaseRefresh map[string]bool
}

func (a *App) originalMarketSource(ctx context.Context, repository string, force bool) originalMarketResult {
	cache := &a.pluginOriginalMarkets
	cache.mu.Lock()
	if !force {
		if entry, ok := cache.entries[repository]; ok && time.Since(entry.status.CheckedAt) < originalMarketTTL {
			entry.accessedAt = time.Now()
			cache.entries[repository] = entry
			cache.mu.Unlock()
			entry.status.Cached = true
			return entry
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
	client, err := a.originalPluginHTTPClient(30*time.Second, true)
	var discoveries []artifacts.Discovery
	if err == nil {
		defer client.CloseIdleConnections()
		discoveries, err = artifacts.DiscoverRepository(ctx, fetchURL, "", validatePluginArtifactContract, client)
	}
	for i := range discoveries {
		discoveries[i].RepositoryURL = repository
	}
	result := originalMarketResult{discoveries: discoveries, accessedAt: time.Now(), status: originalMarketSourceStatus{RepositoryURL: repository, Success: err == nil, CheckedAt: time.Now().UTC()}}
	if err != nil {
		// Do not expose remote bodies or request URLs contained in network errors.
		result.status.Message = "仓库索引读取失败或与当前平台不兼容，请刷新重试"
		a.log("ERROR", "plugins", "market_discovery_failed", "插件仓库元数据读取失败", "", "")
	}
	cache.mu.Lock()
	if ctx.Err() == nil && err == nil {
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
		cache.entries[repository] = result
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

// process_plugins_list keeps the highest source version for each ID. Equal
// versions retain configured repository order, independent of fetch completion.
func selectOriginalMarketDeclarations(sources []artifacts.Discovery) []artifacts.Discovery {
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
// weights. Unknown (including build-metadata) parts are -5. Artifact manifests
// already restrict versions to ASCII, so Python's wider Unicode input is not
// accepted by this Go artifact contract.
func compareOriginalPluginVersion(left, right string) int {
	parts := func(value string) ([]*big.Int, bool) {
		if value == "" {
			return nil, false
		}
		value = strings.TrimLeft(strings.TrimSpace(value), "vV")
		raw := strings.Split(strings.ReplaceAll(value, "-", "."), ".")
		out := make([]*big.Int, 0, len(raw))
		for _, part := range raw {
			digits := part != "" && strings.IndexFunc(part, func(r rune) bool { return r < '0' || r > '9' }) < 0
			if digits {
				// Python's default decimal conversion rejects more than 4300 digits.
				if len(part) > 4300 {
					return nil, false
				}
				n, _ := new(big.Int).SetString(part, 10)
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
