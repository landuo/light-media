package app

import (
	"context"
	"path/filepath"
	"strings"

	"light-media/internal/catalog"
	"light-media/internal/media"
	"light-media/internal/sites"
)

// Keyword search does not invent a TMDB identity. The user chooses that identity
// before the existing material preview and download confirmation can run.
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
	selected := map[string]bool{}
	for _, key := range in.SiteIDs {
		selected[key] = true
	}
	for _, config := range a.Settings().Sites {
		if !config.Enabled || len(selected) > 0 && !selected[config.ID] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
		state := SiteStatus{ID: config.ID, Status: "ok"}
		var result sites.SearchResult
		err := a.withSite(ctx, config, false, func(client *sites.Client) error {
			var err error
			result, err = client.Search(ctx, in.Query, in.Page)
			return err
		})
		if err != nil {
			state.Status, state.Message = "failed", a.clean(err.Error())
			if len(result.Resources) > 0 {
				state.Status = "partial"
			}
		}
		if err == nil && len(result.Resources) == 0 {
			state.Status = "empty"
		}
		for _, resource := range result.Resources {
			season, episodes := resourceNumberingSuggestion(resource.Title)
			out.Candidates = append(out.Candidates, Candidate{Resource: resource, Accepted: true, NeedsConfirmation: true, Reasons: []string{"media_identity_required"}, Season: season, Episodes: episodes})
		}
		state.Count = len(result.Resources)
		out.Sites = append(out.Sites, state)
		a.logSite(state, out.WorkflowID)
		emitSearchProgress(ctx, out)
	}
	if len(out.Sites) == 0 {
		return out, bad("NO_SITES", "请先添加并启用资源站点")
	}
	a.log("INFO", "sites", "keyword_search_completed", "关键词搜索完成；下载前仍须确认影视身份与实际种子文件", out.WorkflowID, "")
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
