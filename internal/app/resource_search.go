package app

import (
	"context"
	"path/filepath"
	"strings"

	"light-media/internal/catalog"
	"light-media/internal/decision"
	"light-media/internal/media"
	"light-media/internal/sites"
)

// Keyword search applies resource filters without inventing a TMDB identity.
// Directory organization performs the separate media recognition step.
func (a *App) searchResources(ctx context.Context, in SearchRequest) (SearchOutput, error) {
	ctx, stop := context.WithTimeout(ctx, interactiveSearchBudget)
	defer stop()
	ctx, err := a.ensureResourceRecognition(ctx)
	if err != nil {
		return SearchOutput{}, err
	}
	in.Policy.FilterParams = snapshotAdditionalFilter(in.Policy.FilterParams).params
	in.Query = strings.TrimSpace(in.Query)
	if in.Query == "" || len([]rune(in.Query)) > 200 || strings.ContainsAny(in.Query, "\x00\r\n") {
		return SearchOutput{}, bad("INVALID_QUERY", "请输入200字以内的资源关键词")
	}
	if in.Page == 0 {
		in.Page = 1
	}
	if in.Page < 1 || in.Page > 500 || len(in.SiteIDs) > 128 {
		return SearchOutput{}, bad("INVALID_SEARCH", "搜索页码或站点范围无效")
	}
	if err := validateSubscriptionPolicy(in.Policy); err != nil {
		return SearchOutput{}, err
	}
	out := SearchOutput{Candidates: []Candidate{}, Sites: []SiteStatus{}, WorkflowID: a.workflow(ctx, in.WorkflowID, catalog.Media{}), CachedAt: now()}
	fetch := func(siteCtx context.Context, config SiteConfig, page int) (sites.SearchResult, error) {
		var fetched sites.SearchResult
		err := a.withSite(siteCtx, config, false, func(client *sites.Client) error {
			var err error
			fetched, err = client.Search(siteCtx, in.Query, page)
			return err
		})
		return fetched, err
	}
	_, streaming := ctx.Value(searchProgressKey{}).(func(SearchOutput))
	if !streaming {
		in.resourceBatch = &originalResourceBatchSearch{app: a, keyword: in.Query, page: in.Page, native: fetch}
	}
	ctx = withKeywordSiteUsage(ctx)
	out, err = a.searchKeywordSites(ctx, in, out, func(siteCtx context.Context, config SiteConfig, emit func([]Candidate)) siteSearchOutput {
		var result sites.SearchResult
		native := func(page int) (sites.SearchResult, error) {
			return fetch(siteCtx, config, page)
		}
		var err error
		published := 0
		page := keywordCandidatePage{emit: emit, seen: make(map[keywordResourceID]struct{})}
		if streaming {
			// Only nonterminal pages need a separate progress event. The final
			// page (including a partial parse on error) goes through the terminal
			// event, preserving its real site status and elapsed time.
			siteCtx = context.WithValue(siteCtx, originalResourcePageProgressKey{}, originalResourcePageProgress(func(_ SiteConfig, accumulated sites.SearchResult, finished bool) {
				if !finished && len(accumulated.Resources) > published {
					if candidates := page.evaluate(siteCtx, a, in.Policy, accumulated.Resources[published:]); len(candidates) != 0 {
						emit(candidates)
					}
					published = len(accumulated.Resources)
				}
			}))
			result, err = a.originalStreamResourceSite(siteCtx, config, in.Query, in.Page, native)
		} else {
			result, err = in.resourceBatch.site(siteCtx, config)
		}
		candidates := page.evaluate(siteCtx, a, in.Policy, result.Resources[published:])
		return siteSearchOutput{status: a.searchStatus(siteCtx, config.ID, page.total, err, 0), candidates: candidates}
	})
	if err != nil {
		return out, err
	}
	a.log("INFO", "sites", "keyword_search_completed", "关键词搜索完成，资源筛选规则已应用；影视身份由整理流程单独识别", out.WorkflowID, "")
	return out, nil
}

type keywordResourceID struct{ site, id string }

type keywordCandidatePage struct {
	seen  map[keywordResourceID]struct{}
	emit  func([]Candidate)
	total int
}

func (page *keywordCandidatePage) evaluate(ctx context.Context, a *App, policy Policy, resources []sites.Resource) []Candidate {
	// Keep most of a large result page if its last titles take us past the deadline.
	const batchSize = 16
	candidates := make([]Candidate, 0, min(batchSize, len(resources)))
	native := decision.NativePolicy(decisionPolicy(policy))
	for _, resource := range resources {
		if ctx.Err() != nil {
			break
		}
		if resource.ID != "" {
			key := keywordResourceID{resource.SiteID, resource.ID}
			if _, duplicate := page.seen[key]; duplicate {
				continue
			}
			page.seen[key] = struct{}{}
		}
		if len(candidates) == batchSize {
			page.emit(candidates)
			candidates = make([]Candidate, 0, batchSize)
		}
		season, episodes := a.resourceNumberingSuggestionContext(ctx, resource.Title)
		choice := decision.EvaluateResourceContext(ctx, resource, native)
		if choice.Accepted {
			choice.Reasons = append(choice.Reasons, "media_identity_required")
		}
		candidates = append(candidates, Candidate{Resource: resource, Accepted: choice.Accepted, NeedsConfirmation: choice.Accepted, Reasons: choice.Reasons, Season: season, Episodes: episodes})
		page.total++
	}
	return candidates
}

// The site card opens that site's recent torrents. Empty queries are allowed
// only here, with one explicit site, rather than as an unbounded global search.
func (a *App) browseManagedSiteResources(ctx context.Context, key, query string, page int) (any, error) {
	ctx, err := a.ensureResourceRecognition(ctx)
	if err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if len([]rune(query)) > 200 || strings.ContainsAny(query, "\x00\r\n") {
		return nil, bad("INVALID_QUERY", "资源关键词不能超过200字或包含控制字符")
	}
	if page == 0 {
		page = 1
	}
	if page < 1 || page > 500 {
		return nil, bad("INVALID_SEARCH", "搜索页码无效")
	}
	ctx = allowDisabledSiteRequests(ctx)
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, interactiveSearchBudget)
	defer cancel()
	config, err := a.siteForRequest(ctx, key)
	if err != nil {
		return nil, err
	}
	out := struct {
		SearchOutput
		Page    int  `json:"page"`
		HasMore bool `json:"has_more"`
	}{SearchOutput: SearchOutput{Candidates: []Candidate{}, Sites: []SiteStatus{}, WorkflowID: a.workflow(ctx, "", catalog.Media{}), CachedAt: now()}, Page: page}
	var result sites.SearchResult
	result, err = a.originalBrowseSiteResources(ctx, config, query, page, func() (sites.SearchResult, error) {
		var native sites.SearchResult
		err := a.withSite(ctx, config, false, func(client *sites.Client) error {
			var err error
			native, err = client.Search(ctx, query, page)
			return err
		})
		return native, err
	})
	out.HasMore = result.HasMore
	out.Sites = append(out.Sites, a.searchStatus(ctx, key, len(result.Resources), err, 0))
	for _, resource := range result.Resources {
		season, episodes := a.resourceNumberingSuggestionContext(ctx, resource.Title)
		choice := decision.EvaluateResource(resource, decision.Policy{})
		if choice.Accepted {
			choice.Reasons = append(choice.Reasons, "media_identity_required")
		}
		out.Candidates = append(out.Candidates, Candidate{Resource: resource, Accepted: choice.Accepted, NeedsConfirmation: choice.Accepted, Reasons: choice.Reasons, Season: season, Episodes: episodes})
	}
	return out, nil
}

// These are syntax-only suggestions. Absolute numbering has no season and is
// intentionally left unresolved until the user supplies an identity and mapping.
func resourceNumberingSuggestion(title string) (int, []int) {
	name := strings.TrimSpace(title)
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mkv", ".mp4", ".avi", ".m4v", ".ts", ".mov":
	default:
		name += ".mkv"
	}
	parsed := media.Parse(name)
	if parsed.Status != media.Parsed || parsed.Original == nil || parsed.Original.Form != "season_episode" || parsed.Original.Season == nil || *parsed.Original.Season < 1 {
		return 0, []int{}
	}
	return *parsed.Original.Season, append([]int{}, parsed.Original.Episodes...)
}
