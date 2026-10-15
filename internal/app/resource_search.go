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
	in.Query = strings.TrimSpace(in.Query)
	if in.Query == "" || len([]rune(in.Query)) > 200 || strings.ContainsAny(in.Query, "\x00\r\n") {
		return SearchOutput{}, bad("INVALID_QUERY", "请输入200字以内的资源关键词")
	}
	if in.Page == 0 {
		in.Page = 1
	}
	if in.Page < 1 || in.Page > 500 || len(in.SiteIDs) > 32 {
		return SearchOutput{}, bad("INVALID_SEARCH", "搜索页码或站点范围无效")
	}
	if err := validateSubscriptionPolicy(in.Policy); err != nil {
		return SearchOutput{}, err
	}
	out := SearchOutput{Candidates: []Candidate{}, Sites: []SiteStatus{}, WorkflowID: a.workflow(ctx, in.WorkflowID, catalog.Media{}), CachedAt: now()}
	out, err := a.searchSites(ctx, in, out, func(siteCtx context.Context, config SiteConfig) siteSearchOutput {
		var result sites.SearchResult
		err := a.withSite(siteCtx, config, false, func(client *sites.Client) error {
			var err error
			result, err = client.Search(siteCtx, in.Query, in.Page)
			return err
		})
		found := siteSearchOutput{status: a.searchStatus(siteCtx, config.ID, len(result.Resources), err, 0), candidates: []Candidate{}}
		for _, resource := range result.Resources {
			season, episodes := resourceNumberingSuggestion(resource.Title)
			choice := decision.EvaluateResource(resource, decisionPolicy(in.Policy))
			if choice.Accepted {
				choice.Reasons = append(choice.Reasons, "media_identity_required")
			}
			found.candidates = append(found.candidates, Candidate{Resource: resource, Accepted: choice.Accepted, NeedsConfirmation: choice.Accepted, Reasons: choice.Reasons, Season: season, Episodes: episodes})
		}
		return found
	})
	if err != nil {
		return out, err
	}
	a.log("INFO", "sites", "keyword_search_completed", "关键词搜索完成，资源筛选规则已应用；影视身份由整理流程单独识别", out.WorkflowID, "")
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
