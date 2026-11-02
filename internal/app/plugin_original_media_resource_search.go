package app

import (
	"context"
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"sync"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/decision"
	"light-media/internal/sites"
	"light-media/internal/sourcejson"
)

// Existing media search retains its site collector, guarded native clients,
// cache and decision consumers. This operation only owns the fixed module
// caller sequence: complete MediaInfo -> ordered keywords -> all-site query.
type originalMediaResourceSearch struct {
	app                    *App
	ctx                    context.Context
	configs                []SiteConfig
	information            sourceMediaInformation
	originalInformation    *sourceMediaInformation
	initialGroups          []originalPluginModuleGroup
	keywords               []string
	mtype                  string
	auto, stream, multiple bool
	once                   sync.Once
	results                map[string]originalResourceSiteResult
	queries                map[string]map[string]string
	successful             map[string]int
	searched               int
	err                    error
}

func originalMediaSearchKeywords(fields map[string]any, explicit string, limit *big.Int) ([]string, error) {
	if explicit != "" {
		return []string{explicit}, nil
	}
	keywords, seen := []string{}, map[string]bool{}
	for _, key := range []string{"title", "original_title", "en_title", "hk_title", "tw_title", "sg_title"} {
		value := fields[key]
		if !sourceSubscriptionTruth(value) {
			continue
		}
		word, ok := value.(string)
		if !ok {
			return nil, bad("PLUGIN_MODULE_RESULT_INVALID", "插件媒体名称无法用于本地站点搜索")
		}
		if !seen[word] {
			keywords = append(keywords, word)
			seen[word] = true
		}
	}
	if limit != nil && limit.Sign() != 0 {
		end := new(big.Int).Set(limit)
		if end.Sign() < 0 {
			end.Add(end, big.NewInt(int64(len(keywords))))
		}
		if end.Sign() < 0 {
			end.SetInt64(0)
		}
		if end.Cmp(big.NewInt(int64(len(keywords)))) < 0 {
			keywords = keywords[:int(end.Int64())]
		}
	}
	return keywords, nil
}

func (a *App) prepareOriginalMediaResourceSearch(ctx context.Context, in SearchRequest, auto bool) (*originalMediaResourceSearch, error) {
	groups, err := a.originalPluginModules(ctx, "")
	if err != nil {
		return nil, err
	}
	relevant := false
	for _, method := range []string{"recognize_media", "async_recognize_media", "filter_torrents", "batch_search_torrents", "search_torrents", "async_batch_search_torrents", "async_search_torrents", "async_worker_search_site", "get_search_page_size"} {
		if originalModuleGroupsHaveMethod(groups, method) {
			relevant = true
			break
		}
	}
	if !relevant {
		return nil, nil
	}
	input := catalog.Media{ID: in.TMDBID, MediaType: in.MediaType}
	var information sourceMediaInformation
	groupsConsumed := false
	if in.originalSearchInfo != nil {
		information = *in.originalSearchInfo
	} else if auto && in.originalSubscription != nil {
		// The existing subscription caller owns its original MetaInfo/IDs/cache
		// boundary; a TV precheck can pass the very same resolved value above.
		raw, failure := a.subscriptionSourceProjection(ctx, *in.originalSubscription)
		if failure != nil {
			return nil, failure
		}
		decoded, failure := decodeSourceJSON(raw)
		if failure != nil {
			return nil, failure
		}
		fields := sourceMap(decoded)
		meta, failure := a.originalSubscriptionRecognitionMeta(ctx, fields)
		if failure != nil {
			return nil, failure
		}
		encoded, failure := meta.ToDict()
		if failure != nil {
			return nil, failure
		}
		information, err = a.originalRecognizeTMDBModuleGroups(ctx, input, "recognize_media", encoded, sourceText(fields["media_id"]), originalRecognitionEpisodeGroup(encoded, fields["episode_group"]), false, func() (sourceMediaInformation, error) { return a.sourceRecognizeTMDBCore(ctx, input) }, groups)
		groupsConsumed = true
	} else {
		method := "async_recognize_media" // Web async_search_by_id[_stream]
		if auto {
			method = "recognize_media"
		}
		information, err = a.originalRecognizeTMDBModuleGroups(ctx, input, method, nil, nil, nil, true, func() (sourceMediaInformation, error) { return a.sourceRecognizeTMDBCore(ctx, input) }, groups)
		groupsConsumed = true
	}
	if err != nil {
		return nil, err
	}
	decoded, err := decodeSourceJSON(information.MediaInfo)
	if err != nil || sourceMap(decoded) == nil {
		return nil, originalResourceProjectionError()
	}
	fields := sourceMap(decoded)
	var originalInformation *sourceMediaInformation
	if in.originalSearchInfo != nil && sourceSubscriptionTruth(fields["names"]) {
		originalInformation = in.originalSearchInfo
	}
	if !sourceSubscriptionTruth(fields["names"]) {
		// Fixed process/async_process fills missing names once before planning
		// queries. This second request resolves the full MediaInfo source ID;
		// it is distinct from the first Web request's requested mediaid=None.
		method := "async_recognize_media"
		if auto {
			method = "recognize_media"
		}
		var recognitionGroups []originalPluginModuleGroup
		if !groupsConsumed {
			recognitionGroups = groups
		}
		information, err = a.originalRecognizeTMDBModuleGroups(ctx, input, method, nil, strconv.FormatInt(input.ID, 10), nil, true, func() (sourceMediaInformation, error) { return a.sourceRecognizeTMDBCore(ctx, input) }, recognitionGroups)
		groupsConsumed = true
		if err != nil {
			return nil, err
		}
		decoded, err = decodeSourceJSON(information.MediaInfo)
		if err != nil || sourceMap(decoded) == nil {
			return nil, originalResourceProjectionError()
		}
		fields = sourceMap(decoded)
	}
	limit, multiple := a.mediaSearchConfiguration.MaxNames, a.mediaSearchConfiguration.MultipleNames
	keywords, err := originalMediaSearchKeywords(fields, in.Query, limit)
	if err != nil {
		return nil, err
	}
	if in.originalSubscription != nil && in.originalSubscription.SourceSearchIMDb {
		// Native clients require string queries. The configured source IMDb
		// identity is already frozen by the subscription's ordinary request.
		keyword, ok := fields["imdb_id"].(string)
		if !ok || keyword == "" {
			return nil, bad("PLUGIN_MODULE_RESULT_INVALID", "插件媒体缺少订阅要求的IMDB搜索编号")
		}
		keywords = []string{keyword}
	}
	_, stream := ctx.Value(searchProgressKey{}).(func(SearchOutput))
	plan := &originalMediaResourceSearch{app: a, information: information, originalInformation: originalInformation, keywords: keywords, mtype: sourceText(fields["type"]), auto: auto, stream: stream, multiple: multiple}
	if !groupsConsumed {
		plan.initialGroups = groups
	}
	return plan, nil
}

func (op *originalMediaResourceSearch) run() {
	op.results, op.queries, op.successful = map[string]originalResourceSiteResult{}, map[string]map[string]string{}, map[string]int{}
	for _, keyword := range op.keywords {
		if err := op.ctx.Err(); err != nil {
			op.err = err
			return
		}
		if op.searched > 0 {
			if err := op.app.waitOriginalMediaSearchKeyword(op.ctx); err != nil {
				op.err = err
				return
			}
		}
		batch := &originalResourceBatchSearch{app: op.app, ctx: op.ctx, configs: op.configs, keyword: keyword, mtype: op.mtype, page: 1, initialGroups: op.initialGroups}
		op.initialGroups = nil
		if op.auto {
			batch.method = "batch_search_torrents"
		}
		batch.native = func(ctx context.Context, site SiteConfig, page int) (sites.SearchResult, error) {
			ctx = originalPluginSiteContext(ctx)
			var result sites.SearchResult
			err := op.app.withSite(ctx, site, op.auto, func(client *sites.Client) error {
				var err error
				result, err = client.Search(ctx, keyword, page)
				return err
			})
			return result, err
		}
		var results map[string]originalResourceSiteResult
		if op.stream && !op.auto {
			results = batch.parallel(op.configs, func(ctx context.Context, site SiteConfig) (sites.SearchResult, error) {
				return op.app.originalResourceSiteSearchPagesMedia(ctx, "async_worker_search_site", site, keyword, op.mtype, 1, func(page int) (sites.SearchResult, error) { return batch.native(ctx, site, page) })
			})
		} else {
			batch.run()
			results, op.err = batch.results, batch.err
		}
		op.searched++
		anyResults := false
		for _, site := range op.configs {
			current, accumulated := results[site.ID], op.results[site.ID]
			if current.err == nil && op.err == nil {
				op.successful[site.ID]++
			}
			if op.queries[site.ID] == nil {
				op.queries[site.ID] = map[string]string{}
			}
			for _, resource := range current.result.Resources {
				op.queries[site.ID][resource.ID] = keyword
			}
			accumulated.result.Page = 1
			accumulated.result.Resources = append(accumulated.result.Resources, current.result.Resources...)
			accumulated.result.HasMore = current.result.HasMore
			if current.err != nil {
				accumulated.err = current.err
			}
			op.results[site.ID] = accumulated
			anyResults = anyResults || len(current.result.Resources) > 0
		}
		if op.err != nil || !op.multiple && anyResults {
			return
		}
	}
}

// The fixed MinIntervalLimiter reserves only between keywords, sharing its
// monotonic next slot across searches. Cancellation releases the caller while
// keeping that reserved slot, just as the source reservation precedes await.
func (a *App) waitOriginalMediaSearchKeyword(ctx context.Context) error {
	wait, err := a.reserveOriginalMediaSearchKeyword(time.Now(), a.mediaSearchConfiguration.KeywordInterval)
	if err != nil {
		return err
	}
	if wait <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) reserveOriginalMediaSearchKeyword(now time.Time, interval float64) (time.Duration, error) {
	if math.IsNaN(interval) || interval <= 0 {
		return 0, nil
	}
	a.originalSearchKeywordMu.Lock()
	defer a.originalSearchKeywordMu.Unlock()
	if a.originalSearchKeywordOverflow {
		return 0, bad("PLUGIN_SEARCH_INTERVAL_INVALID", "搜索关键词间隔超出本地时钟范围")
	}
	if math.IsInf(interval, 0) || interval >= float64(math.MaxInt64)/float64(time.Second) {
		// Source first reservation returns0 even when its next float slot
		// overflows sleep's supported range. Failure belongs to the next wait.
		a.originalSearchKeywordOverflow = true
		return 0, nil
	}
	if a.originalSearchKeywordNext.After(now.Add(time.Duration(math.MaxInt64))) {
		a.originalSearchKeywordOverflow = true
		return 0, bad("PLUGIN_SEARCH_INTERVAL_INVALID", "搜索关键词间隔超出本地时钟范围")
	}
	wait := a.originalSearchKeywordNext.Sub(now)
	if wait < 0 {
		wait = 0
	}
	a.originalSearchKeywordNext = now.Add(wait).Add(time.Duration(interval * float64(time.Second)))
	return wait, nil
}

func (a *App) originalMediaResourceSiteOutput(ctx context.Context, site SiteConfig, in SearchRequest, target decision.Target, workflow string, op *originalMediaResourceSearch) siteSearchOutput {
	op.once.Do(op.run)
	result := op.results[site.ID]
	if op.err != nil {
		result.err = op.err
	}
	found := siteSearchOutput{status: a.searchStatus(ctx, site.ID, len(result.result.Resources), result.err, op.successful[site.ID]), candidates: []Candidate{}}
	for _, resource := range result.result.Resources {
		resource.ModuleMediaFields = append(json.RawMessage(nil), op.information.MediaInfo...)
		choice := a.evaluateResourceContext(ctx, resource, target, decision.NativePolicy(decisionPolicy(in.Policy)))
		found.candidates = append(found.candidates, Candidate{Resource: resource, Accepted: choice.Accepted, Reasons: choice.Reasons, Season: choice.Season, Episodes: choice.Episodes, NeedsConfirmation: choice.NeedsConfirmation})
	}
	cacheResources := append([]sites.Resource(nil), result.result.Resources...)
	for i := range cacheResources {
		cacheResources[i].ModuleMediaFields = append(json.RawMessage(nil), op.information.MediaInfo...)
	}
	if err := a.cacheSearchResources(ctx, cacheResources, op.queries[site.ID], 1, mediaKey(target.Media)); err != nil && ctx.Err() == nil {
		a.log("WARN", "sites", "search_cache_failed", "搜索结果已返回，但预览检索记录保存失败", workflow, "")
	}
	return found
}

// Fixed __remove_duplicate runs after sorting. A dict retains the first key
// position but its last value; equal source names/titles/descriptions must not
// be deduplicated by native resource ID or Go pointer instead.
func originalMediaResourceDeduplicate(out SearchOutput, configs []SiteConfig) (SearchOutput, error) {
	names := map[string]string{}
	for _, site := range configs {
		names[site.ID] = site.Name
	}
	ordered, positions := []Candidate{}, map[string]int{}
	for _, candidate := range out.Candidates {
		var fields map[string]json.RawMessage
		if len(candidate.Resource.ModuleSourceFields) != 0 {
			if json.Unmarshal(candidate.Resource.ModuleSourceFields, &fields) != nil || fields == nil {
				return out, originalResourceProjectionError()
			}
		} else {
			fields = map[string]json.RawMessage{}
			fields["site_name"], _ = json.Marshal(names[candidate.Resource.SiteID])
			fields["title"], _ = json.Marshal(candidate.Resource.Title)
			var description any
			if candidate.Resource.SourceSubtitle != nil {
				description = *candidate.Resource.SourceSubtitle
			} else if candidate.Resource.Subtitle != "" {
				description = candidate.Resource.Subtitle
			}
			fields["description"], _ = json.Marshal(description)
		}
		key := ""
		for i, field := range []string{"site_name", "title", "description"} {
			raw := fields[field]
			if len(raw) == 0 {
				raw = json.RawMessage(`null`)
			}
			text, err := sourcejson.String(raw)
			if err != nil {
				return out, err
			}
			if i > 0 {
				key += "_"
			}
			key += text
		}
		if position, found := positions[key]; found {
			ordered[position] = candidate
		} else {
			positions[key] = len(ordered)
			ordered = append(ordered, candidate)
		}
	}
	out.Candidates = ordered
	return out, nil
}
