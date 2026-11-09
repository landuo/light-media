package app

import (
	"context"
	"encoding/json"
	"math/big"
	"strconv"
	"sync"

	"light-media/internal/plugins"
	"light-media/internal/sites"
)

type originalResourceSiteResult struct {
	result sites.SearchResult
	err    error
}

// One ordinary non-stream keyword search owns one source batch sequence. The
// existing searchSites collector supplies its selected, ordered site snapshot
// and still owns HTTP progress, policy decisions, sorting and cancellation.
type originalResourceBatchSearch struct {
	app           *App
	keyword       string
	mtype         any
	method        string
	initialGroups []originalPluginModuleGroup
	page          int
	auto          bool
	native        func(context.Context, SiteConfig, int) (sites.SearchResult, error)
	ctx           context.Context
	configs       []SiteConfig
	once          sync.Once
	results       map[string]originalResourceSiteResult
	err           error
}

func (op *originalResourceBatchSearch) attach(ctx context.Context, configs []SiteConfig) {
	op.ctx, op.configs = ctx, append([]SiteConfig(nil), configs...)
}

func (op *originalResourceBatchSearch) site(config SiteConfig) (sites.SearchResult, error) {
	op.once.Do(op.run)
	value := op.results[config.ID]
	if op.err != nil {
		return value.result, op.err
	}
	return value.result, value.err
}

func (op *originalResourceBatchSearch) parallel(configs []SiteConfig, run func(context.Context, SiteConfig) (sites.SearchResult, error)) map[string]originalResourceSiteResult {
	out := map[string]originalResourceSiteResult{}
	var mu sync.Mutex
	var workers sync.WaitGroup
	var slots chan struct{}
	if op.auto {
		slots = make(chan struct{}, concurrentBackgroundSearchSites)
	}
	for _, config := range configs {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if slots != nil {
				select {
				case slots <- struct{}{}:
				case <-op.ctx.Done():
					return
				}
				defer func() { <-slots }()
			}
			if op.ctx.Err() != nil {
				return
			}
			ctx, cancel := context.WithTimeout(op.ctx, siteSearchTimeout)
			defer cancel()
			result, err := run(ctx, config)
			mu.Lock()
			out[config.ID] = originalResourceSiteResult{result, err}
			mu.Unlock()
		}()
	}
	workers.Wait()
	return out
}

func (op *originalResourceBatchSearch) nativeBatch(configs []SiteConfig, page int) map[string]originalResourceSiteResult {
	return op.parallel(configs, func(ctx context.Context, site SiteConfig) (sites.SearchResult, error) {
		return op.native(ctx, site, page)
	})
}

func (op *originalResourceBatchSearch) dispatch(configs []SiteConfig, page int) (map[string]originalResourceSiteResult, bool, error) {
	method := op.method
	if method == "" {
		method = "async_batch_search_torrents"
	}
	groups := op.initialGroups
	op.initialGroups = nil
	if groups == nil {
		var err error
		groups, err = op.app.originalPluginModules(op.ctx, "")
		if err != nil {
			return nil, false, err
		}
	}
	if !originalModuleGroupsHaveMethod(groups, method) {
		return op.nativeBatch(configs, page), false, op.ctx.Err()
	}
	sourceSites := []map[string]any{}
	keys := map[string]string{}
	for _, site := range configs {
		fields, err := op.app.originalResourceSiteFields(op.ctx, site)
		if err != nil {
			return nil, false, err
		}
		sourceSites = append(sourceSites, fields)
		keys[site.ID] = strconv.FormatInt(fields["id"].(int64), 10)
	}
	kwargs, _ := json.Marshal(map[string]any{"sites": sourceSites, "keyword": op.keyword, "mtype": op.mtype, "page": page - 1})
	result, err := op.app.invokeOriginalPluginModuleGroups(op.ctx, method, nil, json.RawMessage(`[]`), kwargs, nil, groups)
	if err != nil {
		return nil, false, err
	}
	if originalModuleResultEmpty(result) {
		return op.nativeBatch(configs, page), false, op.ctx.Err()
	}
	// A plugin list executes the real native batch, but that core dictionary
	// cannot concatenate to the list. Source then raises AttributeError and
	// repeats the query through its legacy per-site wrapper. Preserve both calls.
	var list []json.RawMessage
	if json.Unmarshal(result, &list) == nil && list != nil {
		_ = op.nativeBatch(configs, page)
		return nil, true, op.ctx.Err()
	}
	if _, tuple := plugins.DecodeModuleResultTuple(result); tuple {
		return nil, true, nil
	}
	if _, bytes := plugins.DecodeModuleResultBytes(result); bytes {
		return nil, true, nil
	}
	var dict map[string]json.RawMessage
	if json.Unmarshal(result, &dict) != nil || dict == nil {
		return nil, true, nil
	}
	out := map[string]originalResourceSiteResult{}
	for _, site := range configs {
		rows := dict[keys[site.ID]]
		value := originalResourceSiteResult{result: sites.SearchResult{Page: page, Resources: []sites.Resource{}}}
		if originalModuleResultTruthy(rows) {
			var models []json.RawMessage
			if json.Unmarshal(rows, &models) != nil {
				return nil, false, originalResourceProjectionError()
			}
			for _, model := range models {
				resource, err := op.app.originalResourceFromSourceTorrent(op.ctx, site, model)
				if err != nil {
					return nil, false, err
				}
				value.result.Resources = append(value.result.Resources, resource)
			}
		}
		out[site.ID] = value
	}
	return out, false, nil
}

func (op *originalResourceBatchSearch) run() {
	op.results = map[string]originalResourceSiteResult{}
	pages := op.app.resourceSearchPages
	if pages == nil {
		pages = originalResourceSearchPages()
	}
	if pages.Sign() < 1 {
		pages = big.NewInt(1)
	}
	if !pages.IsInt64() || int64(int(pages.Int64())) != pages.Int64() {
		op.err = bad("PLUGIN_SEARCH_PAGES_INVALID", "搜索页数超出本地整数范围")
		return
	}
	active := append([]SiteConfig(nil), op.configs...)
	page := op.page
	for offset := new(big.Int); offset.Cmp(pages) < 0 && len(active) > 0; offset.Add(offset, big.NewInt(1)) {
		values, fallback, err := op.dispatch(active, page)
		if err != nil {
			op.err = err
			return
		}
		if fallback {
			// The fixed AttributeError fallback resets accumulated batch results
			// and retries every original selected site from the original page.
			op.results = op.parallel(op.configs, func(ctx context.Context, site SiteConfig) (sites.SearchResult, error) {
				method := "async_search_torrents"
				if op.method == "batch_search_torrents" {
					method = "search_torrents"
				}
				return op.app.originalResourceSiteSearchPagesMedia(ctx, method, site, op.keyword, op.mtype, op.page, func(page int) (sites.SearchResult, error) { return op.native(ctx, site, page) })
			})
			op.err = op.ctx.Err()
			return
		}
		next := []SiteConfig{}
		for _, site := range active {
			value := values[site.ID]
			accumulated := op.results[site.ID]
			accumulated.result.Page = op.page
			accumulated.result.Resources = append(accumulated.result.Resources, value.result.Resources...)
			accumulated.result.HasMore = value.result.HasMore
			if value.err != nil {
				accumulated.err = value.err
			}
			op.results[site.ID] = accumulated
			more, err := op.app.originalResourceSearchContinues(op.ctx, site, op.keyword, len(value.result.Resources))
			if err != nil {
				op.err = err
				return
			}
			if more && new(big.Int).Add(offset, big.NewInt(1)).Cmp(pages) < 0 {
				next = append(next, site)
			}
		}
		active = next
		if len(active) > 0 {
			if page+1 <= page {
				op.err = bad("PLUGIN_SEARCH_PAGES_INVALID", "搜索页码超出本地整数范围")
				return
			}
			page++
		}
	}
	op.err = op.ctx.Err()
}
