package app

import (
	"context"
	"encoding/json"

	"light-media/internal/decision"
	"light-media/internal/rsscontext"
)

func (a *App) originalTitleFilterResourceModel(ctx context.Context, candidate Candidate) (json.RawMessage, error) {
	resource := candidate.Resource
	if len(resource.ModuleSourceFields) > 0 {
		return append(json.RawMessage(nil), resource.ModuleSourceFields...), nil
	}
	site, err := a.siteForRequest(ctx, resource.SiteID)
	if err != nil {
		return nil, err
	}
	identity, err := a.Store.nativeSiteByAppID(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	nullable := func(value string) any {
		if value == "" {
			return nil
		}
		return value
	}
	var description any
	if resource.SourceSubtitle != nil {
		description = *resource.SourceSubtitle
	} else {
		description = nullable(resource.Subtitle)
	}
	// Use the existing source model and known native host fields. An unfetched
	// enclosure/category/grab count is not invented as download evidence. A
	// complete fresh SDK model above retains its actual extra/private attributes.
	torrent, err := rsscontext.NewTorrentInfo(map[string]any{"site": identity.SourceID, "site_name": site.Name, "site_cookie": nullable(site.Cookie), "site_ua": nullable(site.UserAgent), "site_proxy": site.Proxy, "site_order": site.Priority, "site_downloader": nullable(site.Downloader), "title": resource.Title, "description": description, "page_url": resource.DetailsURL, "size": resource.Size, "seeders": resource.Seeders, "peers": resource.Leechers, "pubdate": nullable(resource.PublishedAt), "freedate": nullable(resource.PromotionEnd), "uploadvolumefactor": resource.UploadFactor, "downloadvolumefactor": resource.DownloadFactor, "hit_and_run": resource.HitAndRun, "labels": resource.Labels, "pri_order": resource.PriorityOrder})
	if err != nil {
		return nil, err
	}
	fields, err := torrent.ToDict()
	if err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

func (a *App) originalTitleFilterCandidates(ctx context.Context, filter priorityFilter, rows *originalTitleFilterList) ([]Candidate, error) {
	out := []Candidate{}
	for _, model := range rows.rows {
		raw, err := model.sourceJSON()
		if err != nil {
			return nil, err
		}
		var fallback SiteConfig
		if model.candidate != nil {
			fallback, err = a.siteForRequest(ctx, model.candidate.Resource.SiteID)
			if err != nil {
				return nil, err
			}
		}
		resource, err := a.originalResourceFromSourceTorrent(ctx, fallback, raw)
		if err != nil {
			return nil, err
		}
		choice := decision.EvaluateResourceContext(ctx, resource, decision.NativePolicy(decisionPolicy(filter.policy)))
		if choice.Accepted {
			choice.Reasons = append(choice.Reasons, "media_identity_required")
		}
		season, episodes := a.resourceNumberingSuggestionContext(ctx, resource.Title)
		candidate := Candidate{Resource: resource, Accepted: choice.Accepted, NeedsConfirmation: choice.Accepted, Reasons: choice.Reasons, Season: season, Episodes: episodes, originalFilterModel: model}
		model.candidate = &candidate
		out = append(out, candidate)
	}
	return out, nil
}

func (a *App) originalTitleFilterResources(ctx context.Context, filter priorityFilter, out SearchOutput) (SearchOutput, error) {
	groups, err := a.originalPluginModules(ctx, "")
	if err != nil {
		return out, err
	}
	if !originalModuleGroupsHaveMethod(groups, "filter_torrents") {
		return filter.applyNative(ctx, a, out)
	}
	models := []json.RawMessage{}
	for _, candidate := range out.Candidates {
		raw, err := a.originalTitleFilterResourceModel(ctx, candidate)
		if err != nil {
			return out, err
		}
		models = append(models, raw)
	}
	state, err := newOriginalTitleFilterState(filter.names, models)
	if err != nil {
		return out, err
	}
	for i, model := range state.input.rows {
		candidate := out.Candidates[i]
		model.candidate = &candidate
	}
	if err = a.runOriginalTorrentFilterState(ctx, filter, out, groups, state); err != nil {
		return out, err
	}
	if state.result.list == nil {
		out.Candidates = []Candidate{}
		if state.empty() || !originalModuleResultTruthy(state.result.scalar) {
			return out, nil
		}
		return out, originalResourceProjectionError()
	}
	out.Candidates, err = a.originalTitleFilterCandidates(ctx, filter, state.result.list)
	return out, err
}

// Both ordinary filter callers use this same module/core boundary. Title has
// no MediaInfo; media search shares its concrete input across site groups.
func (a *App) runOriginalTorrentFilterState(ctx context.Context, filter priorityFilter, out SearchOutput, groups []originalPluginModuleGroup, state *originalTitleFilterState) error {
	input, err := state.callJSON()
	if err != nil {
		return err
	}
	var call struct{ Kwargs json.RawMessage }
	_ = json.Unmarshal(input, &call)
	_, err = a.invokeOriginalPluginModuleGroupsWithTitleFilter(ctx, "filter_torrents", nil, json.RawMessage(`[]`), call.Kwargs, nil, groups, state)
	if err != nil {
		return err
	}
	if state.empty() || state.result.list != nil {
		if state.media != nil {
			filter.media, err = originalPrioritySourceMedia(*state.media)
			if err != nil {
				return err
			}
		}
		core := out
		core.Candidates, err = a.originalTitleFilterCandidates(ctx, filter, state.input)
		if err != nil {
			return err
		}
		var returned *originalTitleFilterList
		if !filter.engine.HasApplicableGroups(state.groups, filter.media) {
			returned = state.input // actual FilterModule returns its input object
		} else {
			coreFilter := filter
			coreFilter.names = state.groups
			core, err = coreFilter.applyNative(ctx, a, core)
			if err == nil {
				returned = &originalTitleFilterList{}
				for _, candidate := range core.Candidates {
					returned.rows = append(returned.rows, candidate.originalFilterModel)
				}
			}
		}
		if err != nil {
			// ChainBase's default system-module boundary reports the failure and
			// keeps preceding plugin results; no new API failure discards them.
			a.logOriginalPluginContext(ctx, "ERROR", "module_execution_failed", "标题过滤核心模块执行失败")
			a.reportOriginalPluginExecutionError(ctx, "system", "标题过滤核心模块执行失败", map[string]any{"type": "module", "module_method": "filter_torrents"}, err)
		} else {
			state.merge(originalTitleFilterValue{list: returned})
		}
	}
	return nil
}
