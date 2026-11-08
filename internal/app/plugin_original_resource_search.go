package app

import (
	"context"
	"encoding/json"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"light-media/internal/notice"
	"light-media/internal/sites"
)

// The native site-card resource entry corresponds to site_resource ->
// TorrentsChain.async_browse -> async_refresh_torrents, not a synchronous hook
// chosen merely because the Go caller blocks until its SDK response completes.
func (a *App) originalBrowseSiteResources(ctx context.Context, site SiteConfig, query string, page int, native func() (sites.SearchResult, error)) (sites.SearchResult, error) {
	var keyword any
	if query != "" {
		keyword = query
	}
	return a.originalSiteTorrentModule(ctx, site, "async_refresh_torrents", map[string]any{"keyword": keyword, "cat": nil, "page": page - 1}, page, native)
}

func (a *App) originalSiteTorrentModule(ctx context.Context, site SiteConfig, method string, sourceKwargs map[string]any, page int, native func() (sites.SearchResult, error)) (sites.SearchResult, error) {
	groups, err := a.originalPluginModules(ctx, "")
	if err != nil {
		return sites.SearchResult{}, err
	}
	if !originalModuleGroupsHaveMethod(groups, method) {
		return native()
	}
	sourceSite, err := a.originalResourceSiteFields(ctx, site)
	if err != nil {
		return sites.SearchResult{}, err
	}
	sourceKwargs["site"] = sourceSite
	kwargs, _ := json.Marshal(sourceKwargs)
	result, err := a.invokeOriginalPluginModuleGroups(ctx, method, nil, json.RawMessage(`[]`), kwargs, nil, groups)
	if err != nil {
		return sites.SearchResult{}, err
	}
	if originalModuleResultEmpty(result) {
		return native()
	}
	out := sites.SearchResult{Resources: []sites.Resource{}, Page: page}
	var rows []json.RawMessage
	if json.Unmarshal(result, &rows) != nil || rows == nil {
		// site_resource returns [] for falsey results. Truthy non-list values cannot
		// become native public resources by pretending the provider returned rows.
		if !originalModuleResultTruthy(result) {
			return out, nil
		}
		return out, bad("PLUGIN_MODULE_RESULT_INVALID", "站点资源插件没有返回资源列表")
	}
	for _, row := range rows {
		resource, err := a.originalResourceFromSourceTorrent(ctx, site, row)
		if err != nil {
			return out, err
		}
		out.Resources = append(out.Resources, resource)
	}
	// ChainBase appends system lists after plugin lists, including an empty list.
	// The already-existing native site query is that real core implementation.
	core, err := native()
	if err != nil {
		return out, err
	}
	out.Resources = append(out.Resources, core.Resources...)
	out.HasMore = core.HasMore
	return out, nil
}

// Reuse the existing saved site row and its authenticated numeric identity.
// Computed MoviePilot parser metadata has no second registry in this host;
// known mTorrent metadata is explicit, generic native sites use the generic
// indexer path. Credentials remain private SDK input, never Resource JSON.
func (a *App) originalResourceSiteFields(ctx context.Context, site SiteConfig) (map[string]any, error) {
	identity, err := a.Store.nativeSiteByAppID(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(site.BaseURL)
	if err != nil || base.Host == "" {
		return nil, bad("PLUGIN_SITE_REFERENCE_INVALID", "站点地址不可用")
	}
	// This is the indexer dictionary, not the DB Site row. Fixed SiteSpider
	// and MTorrentSpider consume domain as their complete HTTP base URL.
	domain := site.BaseURL
	flag := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	nullable := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	note := any(nil)
	if site.Note != "" {
		note, err = decodeSourceJSON([]byte(site.Note))
		if err != nil {
			return nil, err
		}
	}
	sourceID, err := a.Store.nativeSourcePublicSiteID(ctx, a.Store.DB, identity)
	if err != nil {
		return nil, err
	}
	fields := map[string]any{"id": sourceID, "name": site.Name, "domain": domain, "url": site.BaseURL, "cookie": nullable(site.Cookie), "ua": nullable(site.UserAgent), "apikey": nullable(site.APIKey), "token": nullable(site.Token), "rss": nullable(site.RSS), "proxy": flag(site.Proxy), "render": flag(site.Render), "public": flag(site.Public), "pri": site.Priority, "downloader": nullable(site.Downloader), "filter": nullable(site.Filter), "note": note, "limit_interval": site.LimitInterval, "limit_count": site.LimitCount, "limit_seconds": site.LimitSeconds, "timeout": site.Timeout, "is_active": site.Enabled}
	if site.Kind == "mteam" {
		fields["parser"] = "mTorrent"
	}
	return fields, nil
}

var originalResourceNumericID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

func originalResourceProjectionError() error {
	return bad("PLUGIN_MODULE_RESULT_INVALID", "插件资源不能映射到当前本地站点协议")
}
func originalResourceInteger(v any) (int64, error) {
	if v == nil {
		return 0, nil
	}
	n, ok := notice.CoerceSourceInteger(v)
	if !ok {
		return 0, originalResourceProjectionError()
	}
	value, err := strconv.ParseInt(n.String(), 10, 64)
	if err != nil {
		return 0, originalResourceProjectionError()
	}
	return value, nil
}
func originalResourceFactor(v any) (float64, error) {
	if v == nil {
		return 1, nil
	}
	if b, ok := v.(bool); ok {
		if b {
			return 1, nil
		}
		return 0, nil
	}
	value, err := strconv.ParseFloat(sourceNumberString(v), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, originalResourceProjectionError()
	}
	return value, nil
}

// JSON TorrentInfo is the Go SDK representation of the original model. Keep
// that complete object privately for later module consumers; expose only the
// host's existing safe Resource fields. Download still reacquires and verifies
// real material via the configured site/downloader, not this search response.
func (a *App) originalResourceFromSourceTorrent(ctx context.Context, fallback SiteConfig, raw json.RawMessage) (sites.Resource, error) {
	fail := func() (sites.Resource, error) { return sites.Resource{}, originalResourceProjectionError() }
	value, err := decodeSourceJSON(raw)
	if err != nil || sourceMap(value) == nil {
		return fail()
	}
	fields := sourceMap(value)
	site := fallback
	if fields["site"] != nil {
		number, err := originalResourceInteger(fields["site"])
		if err != nil || number == 0 {
			return fail()
		}
		identity, err := a.Store.nativeSiteByPublicSourceID(ctx, number)
		if err != nil {
			return fail()
		}
		site, err = a.siteForRequest(ctx, identity.SiteID)
		if err != nil {
			return sites.Resource{}, err
		}
	}
	title, ok := fields["title"].(string)
	if !ok || strings.TrimSpace(title) == "" {
		return fail()
	}
	base, err := url.Parse(site.BaseURL)
	if err != nil || base.Host == "" {
		return fail()
	}
	var resourceID string
	for _, key := range []string{"page_url", "enclosure"} {
		address, ok := fields[key].(string)
		if !ok || address == "" {
			continue
		}
		parsed, err := url.Parse(address)
		if err != nil || parsed.User != nil {
			continue
		}
		parsed = base.ResolveReference(parsed)
		if !strings.EqualFold(parsed.Scheme, base.Scheme) || !strings.EqualFold(parsed.Host, base.Host) {
			continue
		}
		for _, idkey := range []string{"id", "tid", "torrentid"} {
			if v := parsed.Query().Get(idkey); originalResourceNumericID.MatchString(v) {
				resourceID = v
				break
			}
		}
		if resourceID == "" && site.Kind == "mteam" {
			parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
			if len(parts) == 2 && parts[0] == "detail" && originalResourceNumericID.MatchString(parts[1]) {
				resourceID = parts[1]
			}
		}
		if resourceID != "" {
			break
		}
	}
	if resourceID == "" {
		return fail()
	}
	size, err := originalResourceInteger(fields["size"])
	if err != nil {
		return fail()
	}
	seeds, err := originalResourceInteger(fields["seeders"])
	if err != nil || int64(int(seeds)) != seeds {
		return fail()
	}
	peers, err := originalResourceInteger(fields["peers"])
	if err != nil || int64(int(peers)) != peers {
		return fail()
	}
	priority, err := originalResourceInteger(fields["pri_order"])
	if err != nil || int64(int(priority)) != priority {
		return fail()
	}
	download, err := originalResourceFactor(fields["downloadvolumefactor"])
	if err != nil {
		return fail()
	}
	upload, err := originalResourceFactor(fields["uploadvolumefactor"])
	if err != nil {
		return fail()
	}
	labels := []string{}
	if fields["labels"] != nil {
		rows, ok := fields["labels"].([]any)
		if !ok {
			return fail()
		}
		for _, row := range rows {
			label, ok := row.(string)
			if !ok {
				return fail()
			}
			labels = append(labels, label)
		}
	}
	details := base.ResolveReference(&url.URL{Path: "details.php", RawQuery: url.Values{"id": {resourceID}}.Encode()}).String()
	if site.Kind == "mteam" {
		details = base.ResolveReference(&url.URL{Path: "detail/" + resourceID}).String()
	}
	out := sites.Resource{ID: resourceID, SiteID: site.ID, Title: title, Subtitle: sourceText(fields["description"]), Size: size, Seeders: int(seeds), Leechers: int(peers), PriorityOrder: int(priority), PublishedAt: sourceText(fields["pubdate"]), PromotionEnd: sourceText(fields["freedate"]), DownloadFactor: download, UploadFactor: upload, Free: download == 0, HitAndRun: sourceSubscriptionTruth(fields["hit_and_run"]), Labels: labels, DetailsURL: details, ModuleSourceFields: append(json.RawMessage(nil), raw...)}
	if description, ok := fields["description"].(string); ok {
		out.SourceSubtitle = &description
	}
	return out, nil
}
