package app

import (
	"context"
	"encoding/json"
	"math/big"
	"os"

	"light-media/internal/releasemeta/legacyanime"
	"light-media/internal/sites"
)

type originalResourcePageProgressKey struct{}
type originalResourcePageProgress func(SiteConfig, sites.SearchResult, bool)

// SearchChain's ordinary SSE path uses the site worker wrapper, independently
// of the non-stream batch_search_torrents path. Existing Go site concurrency,
// request guards and HTTP progress collection remain their actual host.
func (a *App) originalStreamResourceSite(ctx context.Context, site SiteConfig, keyword string, page int, native func(int) (sites.SearchResult, error)) (sites.SearchResult, error) {
	return a.originalResourceSiteSearchPages(ctx, "async_worker_search_site", site, keyword, page, native)
}

func (a *App) originalResourceSiteSearchPages(ctx context.Context, method string, site SiteConfig, keyword string, page int, native func(int) (sites.SearchResult, error)) (sites.SearchResult, error) {
	return a.originalResourceSiteSearchPagesMedia(ctx, method, site, keyword, nil, page, native)
}

// Media callers pass the source MediaType while ordinary title search keeps
// None. The sync subscription fallback uses search_torrents, not an async name
// inferred from the Go process transport.
func (a *App) originalResourceSiteSearchPagesMedia(ctx context.Context, method string, site SiteConfig, keyword string, mtype any, page int, native func(int) (sites.SearchResult, error)) (sites.SearchResult, error) {
	out := sites.SearchResult{Resources: []sites.Resource{}, Page: page}
	pages := a.resourceSearchPages
	if pages == nil {
		pages = originalResourceSearchPages()
	}
	if pages.Sign() < 1 {
		pages = big.NewInt(1)
	}
	// Fixed _build_search_pages materializes list(range(...)); a count beyond
	// platform ssize_t fails before provider/network calls, rather than looping.
	if !pages.IsInt64() || int64(int(pages.Int64())) != pages.Int64() {
		return out, bad("PLUGIN_SEARCH_PAGES_INVALID", "搜索页数超出本地整数范围")
	}
	for offset := new(big.Int); offset.Cmp(pages) < 0; offset.Add(offset, big.NewInt(1)) {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if offset.Sign() == 0 {
			if publish, present := ctx.Value(originalResourcePageProgressKey{}).(originalResourcePageProgress); present {
				publish(site, sites.SearchResult{Resources: []sites.Resource{}, Page: page}, false)
			}
		}
		kwargs := map[string]any{"keyword": keyword, "mtype": mtype, "page": page - 1}
		if method == "async_worker_search_site" {
			kwargs["cat"] = nil
		}
		result, err := a.originalSiteTorrentModule(ctx, site, method, kwargs, page, func() (sites.SearchResult, error) { return native(page) })
		out.Resources = append(out.Resources, result.Resources...)
		out.HasMore = result.HasMore
		if err != nil {
			return out, err
		}
		// Fixed _should_continue_search_pages runs before the next-page bound,
		// including when SEARCH_RESOURCE_PAGES is the default one page.
		more, err := a.originalResourceSearchContinues(ctx, site, keyword, len(result.Resources))
		if err != nil {
			return out, err
		}
		finished := !more || new(big.Int).Add(offset, big.NewInt(1)).Cmp(pages) >= 0
		if publish, present := ctx.Value(originalResourcePageProgressKey{}).(originalResourcePageProgress); present {
			snapshot := out
			snapshot.Resources = append([]sites.Resource{}, out.Resources...)
			publish(site, snapshot, finished)
		}
		if finished {
			break
		}
		next := page + 1
		if next <= page {
			return out, bad("PLUGIN_MODULE_RESULT_INVALID", "搜索页码超出本地整数范围")
		}
		page = next
	}
	return out, nil
}

// Reuse the source's ordinary SEARCH_RESOURCE_PAGES configuration name and
// _get_search_resource_pages int/fallback/clamp algorithm; no new settings UI.
func originalResourceSearchPages() *big.Int {
	n, _ := ResourceSearchPagesEnvironment(os.LookupEnv)
	if n.Sign() > 0 {
		return n
	}
	return big.NewInt(1)
}

// Settings.generic_type_converter runs before its Pydantic int field: Python
// int strings normalize, conversion failures become default1 with a warning.
// Keep that full caller boundary rather than applying bare Pydantic coercion.
func ResourceSearchPagesEnvironment(lookup func(string) (string, bool)) (*big.Int, bool) {
	value, exists := lookup("SEARCH_RESOURCE_PAGES")
	if !exists {
		return big.NewInt(1), false
	}
	n, err := legacyanime.Integer(legacyanime.Strip(value))
	if err != nil {
		return big.NewInt(1), true
	}
	return n, n.String() != value
}

func (a *App) originalResourceSearchContinues(ctx context.Context, site SiteConfig, keyword string, count int) (bool, error) {
	const method = "get_search_page_size"
	groups, err := a.originalPluginModules(ctx, "")
	if err != nil {
		return false, err
	}
	// The two existing native protocols use the fixed default100 (ordinary
	// SiteSpider.default_result_num and MTorrentSpider._size). Other source
	// parser business systems are not invented to supply unrelated metadata.
	result := json.RawMessage(`100`)
	if originalModuleGroupsHaveMethod(groups, method) {
		sourceSite, err := a.originalResourceSiteFields(ctx, site)
		if err != nil {
			return false, err
		}
		kwargs, _ := json.Marshal(map[string]any{"site": sourceSite, "keyword": keyword})
		result, err = a.invokeOriginalPluginModuleGroups(ctx, method, nil, json.RawMessage(`[]`), kwargs, nil, groups)
		if err != nil {
			return false, err
		}
		if originalModuleResultEmpty(result) {
			result = json.RawMessage(`100`)
		}
	}
	value, err := decodeSourceJSON(result)
	if err != nil {
		return false, err
	}
	if value == nil {
		return false, nil
	}
	number, infinity, ok := originalBaseNumber(value)
	if !ok {
		return false, bad("PLUGIN_MODULE_RESULT_INVALID", "插件搜索单页容量不能与资源数量比较")
	}
	if infinity != 0 {
		return infinity < 0, nil
	}
	return new(big.Rat).SetInt64(int64(count)).Cmp(number) >= 0, nil
}
